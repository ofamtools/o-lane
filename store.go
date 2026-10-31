package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// The o-questions lock/snapshot/WAL pattern, with one file per root tree.
// A transaction includes its undo receipt, so recovery cannot lose undo state.
type Store struct {
	Dir         string
	Actor       Actor
	Now         func() time.Time
	States      []State
	BackupDaily bool
	BackupKeep  int
	Permit      func(string) error
	Remove      func(string) error
}
type Snapshot struct {
	Rows   []Lane    `json:"rows"`
	At     time.Time `json:"at"`
	Action string    `json:"action"`
	By     Actor     `json:"by"`
	Undone bool      `json:"undone"`
}
type transaction struct {
	Writes map[string]json.RawMessage `json:"writes"`
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s *Store) remove(p string) error {
	if s.Remove != nil {
		return s.Remove(p)
	}
	return os.Remove(p)
}
func (s *Store) stateRows() []State {
	if len(s.States) > 0 {
		return s.States
	}
	rows, _ := parseStates(defaultStates)
	return rows
}
func (s *Store) states() map[string]State {
	out := map[string]State{}
	for _, x := range s.stateRows() {
		out[x.Name] = x
	}
	return out
}
func (s *Store) locked(fn func() error) error {
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(s.Dir, 0700); err != nil {
		return err
	}
	for _, d := range []string{"roots", ".history"} {
		if err := os.MkdirAll(filepath.Join(s.Dir, d), 0700); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
	b, err := os.ReadFile(filepath.Join(s.Dir, ".transaction.json"))
	if err == nil {
		var tx transaction
		if err = json.Unmarshal(b, &tx); err != nil {
			return err
		}
		if err = s.apply(tx); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return fn()
}
func (s *Store) apply(tx transaction) error {
	keys := make([]string, 0, len(tx.Writes))
	for key := range tx.Writes {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		parts := strings.Split(filepath.ToSlash(key), "/")
		if len(parts) != 2 || (parts[0] != "roots" && parts[0] != ".history") || !safeID(strings.TrimSuffix(parts[1], ".json")) || !strings.HasSuffix(parts[1], ".json") {
			return fmt.Errorf("unsafe transaction member %q", key)
		}
		if err := atomicBytes(filepath.Join(s.Dir, key), tx.Writes[key]); err != nil {
			return err
		}
	}
	if err := s.remove(filepath.Join(s.Dir, ".transaction.json")); err != nil {
		return err
	}
	return syncDir(s.Dir)
}
func (s *Store) write(rows []Lane, extra map[string]json.RawMessage) error {
	tx := transaction{Writes: map[string]json.RawMessage{}}
	files, err := filepath.Glob(filepath.Join(s.Dir, "roots", "*.json"))
	if err != nil {
		return err
	}
	// Empty root documents replace trees moved or undone; no record files are
	// unlinked, and a partially applied transaction remains fully replayable.
	for _, p := range files {
		tx.Writes["roots/"+filepath.Base(p)] = json.RawMessage("[]\n")
	}
	groups := map[string][]Lane{}
	for _, q := range rows {
		root := q.ID
		seen := map[string]bool{}
		cur := q
		for cur.Parent != "" {
			if seen[cur.ID] {
				return errors.New("lane tree contains a cycle")
			}
			seen[cur.ID] = true
			found := false
			for _, p := range rows {
				if p.ID == cur.Parent {
					cur = p
					root = p.ID
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("parent %q not found", cur.Parent)
			}
		}
		groups[root] = append(groups[root], q)
	}
	for id, group := range groups {
		b, e := json.MarshalIndent(group, "", "  ")
		if e != nil {
			return e
		}
		tx.Writes["roots/"+id+".json"] = append(b, '\n')
	}
	for k, v := range extra {
		tx.Writes[k] = v
	}
	if err = atomicJSON(filepath.Join(s.Dir, ".transaction.json"), tx); err != nil {
		return err
	}
	return s.apply(tx)
}
func safeID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, "/\\") && !strings.HasPrefix(id, ".")
}
func (s *Store) all() ([]Lane, error) {
	files, err := filepath.Glob(filepath.Join(s.Dir, "roots", "*.json"))
	if err != nil {
		return nil, err
	}
	out := []Lane{}
	seen := map[string]bool{}
	for _, f := range files {
		b, e := os.ReadFile(f)
		if e != nil {
			return nil, e
		}
		var rows []Lane
		if e = json.Unmarshal(b, &rows); e != nil {
			return nil, fmt.Errorf("%s: %w", f, e)
		}
		for _, q := range rows {
			if !safeID(q.ID) || seen[q.ID] {
				return nil, fmt.Errorf("invalid or duplicate lane id %q", q.ID)
			}
			seen[q.ID] = true
			out = append(out, q)
		}
	}
	slices.SortFunc(out, func(a, b Lane) int {
		if c := a.Created.Compare(b.Created); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}
func visible(rows []Lane) []Lane {
	out := []Lane{}
	for _, q := range rows {
		if !q.Deleted {
			out = append(out, q)
		}
	}
	return out
}
func resolve(rows []Lane, target string) (Lane, error) {
	target = strings.TrimPrefix(target, "lane/")
	for _, q := range rows {
		if q.ID == target {
			return q, nil
		}
	}
	matches := []Lane{}
	for _, q := range rows {
		if q.Name == target {
			matches = append(matches, q)
		}
	}
	if len(matches) == 0 && target != "" {
		for _, q := range rows {
			if strings.HasPrefix(q.ID, target) {
				matches = append(matches, q)
			}
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return Lane{}, fmt.Errorf("ambiguous lane %q (%d matches)", target, len(matches))
	}
	return Lane{}, fmt.Errorf("lane %q not found", target)
}
func (s *Store) List() (out []Lane, err error) {
	err = s.locked(func() error { rows, e := s.all(); out = visible(rows); return e })
	return
}
func (s *Store) Resolve(target string) (Lane, error) {
	rows, e := s.List()
	if e != nil {
		return Lane{}, e
	}
	return resolve(rows, target)
}
func clone(rows []Lane) []Lane {
	b, _ := json.Marshal(rows)
	var out []Lane
	_ = json.Unmarshal(b, &out)
	return out
}
func (s *Store) historyFiles() ([]string, error) {
	return filepath.Glob(filepath.Join(s.Dir, ".history", "*.json"))
}
func (s *Store) commit(before, after []Lane, action string) error {
	if s.BackupDaily {
		if _, e := s.backup(filepath.Join(s.Dir, "backups"), true); e != nil {
			return e
		}
	}
	files, e := s.historyFiles()
	if e != nil {
		return e
	}
	next := 1
	if len(files) > 0 {
		n, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(files[len(files)-1]), ".json"))
		if err != nil {
			return err
		}
		next = n + 1
	}
	b, e := json.Marshal(Snapshot{Rows: before, At: s.now(), Action: action, By: s.Actor})
	if e != nil {
		return e
	}
	return s.write(after, map[string]json.RawMessage{fmt.Sprintf(".history/%020d.json", next): b})
}
func (s *Store) change(action string, fn func(*[]Lane) error) error {
	if e := s.permit(action); e != nil {
		return e
	}
	return s.locked(func() error {
		rows, e := s.all()
		if e != nil {
			return e
		}
		before := clone(rows)
		if e = fn(&rows); e != nil {
			return e
		}
		if reflect.DeepEqual(before, rows) {
			return nil
		}
		return s.commit(before, rows, action)
	})
}
func (s *Store) entry(q *Lane, kind, from, to, text string, links []Link, pane *Pane) {
	id := fmt.Sprintf("e-%d-%d", s.now().UnixNano(), len(q.History)+1)
	q.History = append(q.History, Entry{ID: id, At: s.now(), By: s.Actor, Kind: kind, From: from, To: to, Text: text, Links: links, Pane: pane})
	q.Updated = s.now()
}
func (s *Store) mutate(action, target string, fn func(*Lane, *[]Lane) error) (out Lane, err error) {
	err = s.change(action, func(rows *[]Lane) error {
		q, e := resolve(visible(*rows), target)
		if e != nil {
			return e
		}
		if e = fn(&q, rows); e != nil {
			return e
		}
		for i := range *rows {
			if (*rows)[i].ID == q.ID {
				(*rows)[i] = q
			}
		}
		out = q
		return nil
	})
	return
}
func (s *Store) Undo() error {
	if e := s.permit("undo"); e != nil {
		return e
	}
	return s.locked(func() error {
		files, e := s.historyFiles()
		if e != nil {
			return e
		}
		for i := len(files) - 1; i >= 0; i-- {
			b, e := os.ReadFile(files[i])
			if e != nil {
				return e
			}
			var snap Snapshot
			if e = json.Unmarshal(b, &snap); e != nil {
				return e
			}
			if snap.Undone {
				continue
			}
			if s.Actor.IsAgent() && !s.Actor.SameAgent(snap.By) {
				return errors.New("undo refused (history.own-entry): the latest change belongs to another actor")
			}
			snap.Undone = true
			b, e = json.Marshal(snap)
			if e != nil {
				return e
			}
			return s.write(snap.Rows, map[string]json.RawMessage{".history/" + filepath.Base(files[i]): b})
		}
		return errors.New("nothing to undo")
	})
}
