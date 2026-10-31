package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/unixsuperhero/ochazuke"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	return &Store{Dir: fixture(t), Actor: ochazuke.Owner()}
}
func mustAdd(t *testing.T, s *Store, spec Spec) Lane {
	t.Helper()
	q, e := s.Add(spec)
	if e != nil {
		t.Fatal(e)
	}
	return q
}
func mustResolve(t *testing.T, s *Store, id string) Lane {
	t.Helper()
	q, e := s.Resolve(id)
	if e != nil {
		t.Fatal(e)
	}
	return q
}
func TestStoreTreeUndoCycle(t *testing.T) {
	s := testStore(t)
	root := mustAdd(t, s, Spec{Name: "task", Wording: "exact\nowner words"})
	child := mustAdd(t, s, Spec{Name: "child", Parent: root.ID})
	leaf := mustAdd(t, s, Spec{Name: "leaf", Parent: child.ID})
	if _, e := s.Move(root.ID, leaf.ID); e == nil || !strings.Contains(e.Error(), "cycle") {
		t.Fatalf("cycle: %v", e)
	}
	if _, e := s.Move(child.ID, ""); e != nil {
		t.Fatal(e)
	}
	files, _ := filepath.Glob(filepath.Join(s.Dir, "roots", "*.json"))
	if len(files) != 2 {
		t.Fatalf("root files: %v", files)
	}
	if e := s.Undo(); e != nil {
		t.Fatal(e)
	}
	if got := mustResolve(t, s, child.ID).Parent; got != root.ID {
		t.Fatal(got)
	}
	before, _ := s.List()
	if _, e := s.Delete(child.ID); e != nil {
		t.Fatal(e)
	}
	after, _ := s.List()
	if len(after) != 1 {
		t.Fatal(after)
	}
	if e := s.Undo(); e != nil {
		t.Fatal(e)
	}
	restored, _ := s.List()
	if !reflect.DeepEqual(domainRows(before), domainRows(restored)) {
		t.Fatal("undo did not restore subtree exactly")
	}
	info, e := os.Stat(s.Dir)
	if e != nil || info.Mode().Perm() != 0700 {
		t.Fatal(info, e)
	}
	for _, q := range restored {
		if q.Wording != "" && q.Wording != root.Wording {
			t.Fatal("wording changed")
		}
	}
}
func TestStoreLifecycleDerived(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return at }
	q := mustAdd(t, s, Spec{Name: "demo", Estimate: "1.5 h 30 min"})
	for _, state := range []string{"done", "planned", "running", "parked", "review", "merged", "running", "done"} {
		at = at.Add(time.Minute)
		if _, e := s.State(q.ID, state, "free transition"); e != nil {
			t.Fatal(e)
		}
	}
	q = mustResolve(t, s, q.ID)
	v := s.view(q, []Lane{q})
	if v.Started == nil || !v.Started.Equal(q.History[3].At) || v.Finished == nil || !v.Finished.Equal(at) || v.EstimateMinutes != 120 {
		t.Fatalf("derived: %+v", v)
	}
	if e := s.Undo(); e != nil {
		t.Fatal(e)
	}
	q = mustResolve(t, s, q.ID)
	v = s.view(q, []Lane{q})
	if v.Finished != nil {
		t.Fatal("finish not derived from restored history")
	}
}
func TestStoreRecovery(t *testing.T) {
	s := testStore(t)
	q := mustAdd(t, s, Spec{Name: "original"})
	before, _ := s.List()
	_, e := s.Note(q.ID, "survive a crash")
	if e != nil {
		t.Fatal(e)
	}
	// Simulate a crash after all writes, but before unlinking the WAL.
	s.Remove = func(path string) error {
		if strings.HasSuffix(path, ".transaction.json") {
			return errors.New("simulated crash")
		}
		return os.Remove(path)
	}
	_, e = s.Move(q.ID, "")
	if e == nil {
		t.Fatal("missing crash")
	}
	if _, e = os.Stat(filepath.Join(s.Dir, ".transaction.json")); e != nil {
		t.Fatal(e)
	}
	s.Remove = nil
	q = mustResolve(t, s, q.ID)
	if len(q.History) != 3 {
		t.Fatal(q.History)
	}
	if e = s.Undo(); e != nil {
		t.Fatal(e)
	}
	q = mustResolve(t, s, q.ID)
	if len(activeHistory(q.History)) != 2 {
		t.Fatal("undo receipt not recovered")
	}
	// A redo recovery after only the first root write of a multi-root move.
	other := mustAdd(t, s, Spec{Name: "other"})
	rows, _ := s.List()
	for i := range rows {
		if rows[i].ID == q.ID {
			rows[i].Parent = other.ID
		}
	}
	raw, _ := json.Marshal(rows)
	tx := transaction{Writes: map[string]json.RawMessage{"roots/" + q.ID + ".json": json.RawMessage(`[]`), "roots/" + other.ID + ".json": raw}}
	if e = atomicJSON(filepath.Join(s.Dir, ".transaction.json"), tx); e != nil {
		t.Fatal(e)
	}
	if e = atomicJSON(filepath.Join(s.Dir, "roots", q.ID+".json"), []Lane{}); e != nil {
		t.Fatal(e)
	}
	if got := mustResolve(t, s, q.ID).Parent; got != other.ID {
		t.Fatal(got)
	}
	if len(before) != 1 {
		t.Fatal(before)
	}
	// Invalid recovery paths are refused before escaping the store.
	tx.Writes = map[string]json.RawMessage{"../escape.json": json.RawMessage(`{}`)}
	if e = atomicJSON(filepath.Join(s.Dir, ".transaction.json"), tx); e != nil {
		t.Fatal(e)
	}
	if _, e = s.List(); e == nil {
		t.Fatal("unsafe WAL accepted")
	}
}
func TestStoreDefineAtomicAndIdempotent(t *testing.T) {
	s := testStore(t)
	specs := []Spec{{Name: "child", Parent: "root", Scope: "first"}, {Name: "root", Wording: "words"}}
	out, e := s.Define(specs)
	if e != nil || len(out) != 2 {
		t.Fatal(out, e)
	}
	specs[0].Scope = "updated"
	specs[1].Wording = "replacement forbidden through define"
	if _, e = s.Define(specs); e != nil {
		t.Fatal(e)
	}
	rows, _ := s.List()
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	if mustResolve(t, s, "root").Wording != "words" || mustResolve(t, s, "child").Scope != "updated" {
		t.Fatal(rows)
	}
	before, _ := s.List()
	_, e = s.Define([]Spec{{Name: "valid"}, {Name: "bad", Parent: "missing"}})
	if e == nil {
		t.Fatal("unresolved parent accepted")
	}
	after, _ := s.List()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("batch partially committed")
	}
}
func TestStoreAgentUndo(t *testing.T) {
	s := testStore(t)
	q := mustAdd(t, s, Spec{Name: "owned"})
	s.Actor = Actor{Kind: "agent", Name: "worker", Harness: "test", Pane: "w", Session: "s"}
	if _, e := s.Note(q.ID, "mine"); e != nil {
		t.Fatal(e)
	}
	if e := s.Undo(); e != nil {
		t.Fatal(e)
	}
	if e := s.Undo(); e == nil || !strings.Contains(e.Error(), "history.own-entry") {
		t.Fatalf("undo crossed owner: %v", e)
	}
	for _, action := range []string{"delete", "reword"} {
		if e := s.permit(action); e == nil {
			t.Fatal(action)
		}
	}
}
func TestStoreBackups(t *testing.T) {
	s := testStore(t)
	s.BackupDaily = true
	s.BackupKeep = 2
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return at }
	q := mustAdd(t, s, Spec{Name: "backup"})
	for i := 0; i < 3; i++ {
		if _, e := s.Note(q.ID, "entry"); e != nil {
			t.Fatal(e)
		}
		at = at.Add(24 * time.Hour)
	}
	files, _ := filepath.Glob(filepath.Join(s.Dir, "backups", "*.tar.gz"))
	if len(files) != 2 {
		t.Fatal(files)
	}
}
func TestStoreLockProcess(t *testing.T) {
	if dir := os.Getenv("O_LANE_TEST_CHILD"); dir != "" {
		s := &Store{Dir: dir, Actor: ochazuke.Owner()}
		for i := 0; i < 8; i++ {
			if _, e := s.Note("root", fmt.Sprint(os.Getpid(), "-", i)); e != nil {
				t.Fatal(e)
			}
		}
		return
	}
	s := testStore(t)
	mustAdd(t, s, Spec{Name: "root"})
	cmds := []*exec.Cmd{}
	for i := 0; i < 3; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestStoreLockProcess$")
		cmd.Env = append(os.Environ(), "O_LANE_TEST_CHILD="+s.Dir)
		if e := cmd.Start(); e != nil {
			t.Fatal(e)
		}
		cmds = append(cmds, cmd)
	}
	for _, cmd := range cmds {
		if e := cmd.Wait(); e != nil {
			t.Fatal(e)
		}
	}
	if got := len(mustResolve(t, s, "root").History); got != 25 {
		t.Fatalf("lost process writes: %d", got)
	}
}

func domainRows(rows []Lane) []Lane {
	out := clone(rows)
	for i := range out {
		out[i].History = nil
		out[i].Updated = time.Time{}
	}
	return out
}
