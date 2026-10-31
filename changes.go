package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Only domain fields exposed by set are editable; wording and parent retain
// their dedicated owner-policy/cycle-check verbs.
var editable = []string{"estimate", "repo", "base", "branch", "worktree", "depends", "plan", "task", "scope", "done_when", "tests", "project"}

func setSpec(q *Spec, prop string, value any) error {
	if !slices.Contains(editable, prop) {
		return fmt.Errorf("property %q is not settable; use rename, reword or move", prop)
	}
	b, e := json.Marshal(q)
	if e != nil {
		return e
	}
	var obj map[string]any
	if e = json.Unmarshal(b, &obj); e != nil {
		return e
	}
	if prop == "depends" {
		if text, ok := value.(string); ok {
			var xs []string
			if strings.HasPrefix(strings.TrimSpace(text), "[") {
				if e = json.Unmarshal([]byte(text), &xs); e != nil {
					return e
				}
			} else if text != "" {
				xs = []string{text}
			}
			value = xs
		}
	}
	obj[prop] = value
	b, e = json.Marshal(obj)
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, q); e != nil {
		return e
	}
	if len(q.Depends) == 0 {
		q.Depends = nil
	}
	return nil
}
func (s *Store) add(rows *[]Lane, spec Spec) (Lane, error) {
	if strings.TrimSpace(spec.Name) == "" {
		return Lane{}, errors.New("lane name must not be empty")
	}
	if spec.Parent != "" {
		p, e := resolveExact(visible(*rows), spec.Parent)
		if e != nil {
			return Lane{}, e
		}
		spec.Parent = p.ID
	}
	id := slug(spec.Name)
	base := id
	for n := 2; ; n++ {
		found := false
		for _, q := range *rows {
			if q.ID == id {
				found = true
				break
			}
		}
		if !found {
			break
		}
		id = base + "-" + strconv.Itoa(n)
	}
	state := s.stateRows()[0].Name
	q := Lane{Spec: spec, ID: id, State: state, Created: s.now(), Updated: s.now(), Panes: []Pane{}, Links: []Link{}, History: []Entry{}}
	s.entry(&q, "create", "", state, "", nil, nil)
	*rows = append(*rows, q)
	return q, nil
}
func (s *Store) Add(spec Spec) (out Lane, err error) {
	err = s.change("add", func(rows *[]Lane) error { var e error; out, e = s.add(rows, spec); return e })
	return
}

// Define is an atomic batch. Parents may precede or follow children.
// Repeated definitions match (parent, name), update supplied spec fields,
// and preserve the owner's existing wording.
func (s *Store) Define(specs []Spec) (out []Lane, err error) {
	return s.defineFields(specs, nil)
}

func validateDefinitions(specs []Spec) error {
	if len(specs) == 0 {
		return errors.New("define needs at least one lane")
	}
	seen := map[[2]string]bool{}
	for _, spec := range specs {
		if strings.TrimSpace(spec.Name) == "" {
			return errors.New("lane name must not be empty")
		}
		key := [2]string{spec.Parent, spec.Name}
		if seen[key] {
			return fmt.Errorf("duplicate definition %q under %q", spec.Name, spec.Parent)
		}
		seen[key] = true
	}
	return nil
}

// Supplied fields retain explicit empty values from JSON or a table.
func (s *Store) defineFields(specs []Spec, supplied []map[string]any) (out []Lane, err error) {
	if err = validateDefinitions(specs); err != nil {
		return nil, err
	}
	err = s.change("define", func(rows *[]Lane) error {
		type definition struct {
			Spec
			Index int
		}
		pending := []definition{}
		batch := map[string][]int{}
		defined := map[int]string{}
		for i, spec := range specs {
			pending = append(pending, definition{spec, i})
			batch[spec.Name] = append(batch[spec.Name], i)
		}
		for len(pending) > 0 {
			progress := false
			next := []definition{}
			for _, item := range pending {
				spec := item.Spec
				if strings.TrimSpace(spec.Name) == "" {
					return errors.New("lane name must not be empty")
				}
				if spec.Parent != "" {
					if indices, ok := batch[spec.Parent]; ok {
						if len(indices) != 1 {
							return fmt.Errorf("define: ambiguous batch parent %q", spec.Parent)
						}
						id, ready := defined[indices[0]]
						if !ready {
							next = append(next, item)
							continue
						}
						spec.Parent = id
					} else {
						p, e := resolveExact(visible(*rows), spec.Parent)
						if e != nil {
							return e
						}
						spec.Parent = p.ID
					}
				}
				index := -1
				for i, q := range *rows {
					if !q.Deleted && q.Parent == spec.Parent && q.Name == spec.Name {
						if index >= 0 {
							return fmt.Errorf("ambiguous definition %q", spec.Name)
						}
						index = i
					}
				}
				if index < 0 {
					q, e := s.add(rows, spec)
					if e != nil {
						return e
					}
					out = append(out, q)
					defined[item.Index] = q.ID
				} else {
					q := &(*rows)[index]
					before := q.Spec
					b, e := json.Marshal(spec)
					if e != nil {
						return e
					}
					var fields map[string]any
					if e = json.Unmarshal(b, &fields); e != nil {
						return e
					}
					if supplied != nil {
						fields = supplied[item.Index]
					}
					for _, prop := range editable {
						if v, ok := fields[prop]; ok {
							if e = setSpec(&q.Spec, prop, v); e != nil {
								return e
							}
						}
					}
					if !reflect.DeepEqual(before, q.Spec) {
						s.entry(q, "edit", "", "", "define", nil, nil)
					}
					out = append(out, *q)
					defined[item.Index] = q.ID
				}
				progress = true
			}
			if !progress {
				return fmt.Errorf("define: unresolved or cyclic parent %q", next[0].Parent)
			}
			pending = next
		}
		return nil
	})
	return
}
func (s *Store) Set(target, prop string, value any) (Lane, error) {
	return s.mutate("set", target, func(q *Lane, _ *[]Lane) error {
		if e := setSpec(&q.Spec, prop, value); e != nil {
			return e
		}
		s.entry(q, "edit", "", "", prop, nil, nil)
		return nil
	})
}
func (s *Store) Rename(target, name string) (Lane, error) {
	return s.mutate("rename", target, func(q *Lane, _ *[]Lane) error {
		if strings.TrimSpace(name) == "" {
			return errors.New("name must not be empty")
		}
		old := q.Name
		q.Name = name
		s.entry(q, "edit", old, name, "name", nil, nil)
		return nil
	})
}
func (s *Store) Reword(target, text string) (Lane, error) {
	return s.mutate("reword", target, func(q *Lane, _ *[]Lane) error {
		old := q.Wording
		q.Wording = text
		s.entry(q, "edit", old, text, "wording", nil, nil)
		return nil
	})
}
func (s *Store) Move(target, parent string) (Lane, error) {
	return s.mutate("move", target, func(q *Lane, rows *[]Lane) error {
		if parent != "" {
			p, e := resolve(visible(*rows), parent)
			if e != nil {
				return e
			}
			parent = p.ID
			if descendant(*rows, parent, q.ID) {
				return errors.New("cannot move a lane under itself or its descendant: this would create a cycle")
			}
		}
		old := q.Parent
		q.Parent = parent
		s.entry(q, "move", old, parent, "", nil, nil)
		return nil
	})
}
func (s *Store) Copy(target, name string) (out Lane, err error) {
	err = s.change("copy", func(rows *[]Lane) error {
		q, e := resolve(visible(*rows), target)
		if e != nil {
			return e
		}
		spec := q.Spec
		if name != "" {
			spec.Name = name
		} else {
			spec.Name += " copy"
		}
		out, e = s.add(rows, spec)
		if e != nil {
			return e
		}
		out.Links = append(out.Links, Link{Kind: "retry-of", Target: q.ID})
		s.entry(&out, "link", "", "", "retry-of", out.Links, nil)
		(*rows)[len(*rows)-1] = out
		return nil
	})
	return
}
func (s *Store) state(q *Lane, state, note string) error {
	if _, ok := s.states()[state]; !ok {
		return fmt.Errorf("unknown state %q; configure lanes.states to add it", state)
	}
	old := q.State
	q.State = state
	s.entry(q, "state", old, state, note, nil, nil)
	return nil
}
func (s *Store) State(target, state, note string) (Lane, error) {
	return s.mutate("state", target, func(q *Lane, _ *[]Lane) error { return s.state(q, state, note) })
}
func (s *Store) Note(target, text string) (Lane, error) {
	return s.mutate("note", target, func(q *Lane, _ *[]Lane) error {
		if strings.TrimSpace(text) == "" {
			return errors.New("note must not be empty")
		}
		s.entry(q, "note", "", "", text, nil, nil)
		return nil
	})
}
func (s *Store) link(q *Lane, link Link, remove bool) error {
	if link.Kind == "" || link.Target == "" {
		return errors.New("link needs kind and target")
	}
	kind := "link"
	if remove {
		kind = "unlink"
		q.Links = slices.DeleteFunc(q.Links, func(l Link) bool { return l.Kind == link.Kind && l.Target == link.Target })
	} else {
		if !slices.Contains(q.Links, link) {
			q.Links = append(q.Links, link)
		}
	}
	s.entry(q, kind, "", "", "", []Link{link}, nil)
	return nil
}
func (s *Store) Link(target string, link Link, remove bool) (Lane, error) {
	action := "link"
	if remove {
		action = "unlink"
	}
	return s.mutate(action, target, func(q *Lane, _ *[]Lane) error { return s.link(q, link, remove) })
}
func samePane(a, b Pane) bool {
	return a.Host == b.Host && a.Session == b.Session && a.Window == b.Window && a.Role == b.Role && a.Harness == b.Harness && a.Model == b.Model && a.Agent == b.Agent && a.AgentID == b.AgentID && a.AgentSession == b.AgentSession
}

// A participant is keyed by its agent when it names one (an agent may run in
// a pane another participant also names), else by its pane.
func paneKey(a, b Pane) bool {
	if a.AgentID != "" || b.AgentID != "" {
		return a.AgentID == b.AgentID && a.Harness == b.Harness
	}
	if a.Window != "" || b.Window != "" {
		return a.Host == b.Host && a.Session == b.Session && a.Window == b.Window
	}
	return samePane(a, b)
}

// A query's empty host/session are wildcards on both relation views.
func paneMatches(stored, query Pane) bool {
	if query.AgentID != "" {
		return stored.AgentID == query.AgentID && (query.Harness == "" || stored.Harness == query.Harness)
	}
	if query.Window != "" {
		return stored.Window == query.Window && (query.Session == "" || stored.Session == query.Session) && (query.Host == "" || stored.Host == query.Host)
	}
	return paneKey(stored, query)
}
func (s *Store) associate(q *Lane, p Pane, remove bool) {
	kind := "associate"
	if p.Role == "" {
		p.Role = "implementer"
	}
	p.Since = s.now()
	if remove {
		kind = "unassociate"
		q.Panes = slices.DeleteFunc(q.Panes, func(old Pane) bool {
			return paneMatches(old, p)
		})
	} else {
		found := false
		for i, old := range q.Panes {
			if paneKey(old, p) {
				p.Since = old.Since
				q.Panes[i] = p
				found = true
				break
			}
		}
		if !found {
			q.Panes = append(q.Panes, p)
		}
	}
	s.entry(q, kind, "", "", "", nil, &p)
}
func (s *Store) Associate(target string, p Pane, remove bool) (Lane, error) {
	action := "associate"
	if remove {
		action = "unassociate"
	}
	return s.mutate(action, target, func(q *Lane, _ *[]Lane) error { s.associate(q, p, remove); return nil })
}
func (s *Store) preset(q *Lane, verb, note string) error {
	table := s.StatePresets
	if table == nil {
		_ = json.Unmarshal([]byte(defaultStatePresets), &table)
	}
	state, ok := table[verb]
	if !ok || state == "" {
		return fmt.Errorf("lanes.state-presets: preset %q is not configured", verb)
	}
	if _, ok := s.states()[state]; !ok {
		return fmt.Errorf("lanes.state-presets: preset %q names %q, which is absent from lanes.states", verb, state)
	}
	return s.state(q, state, note)
}
func (s *Store) Preset(target, verb, note string) (Lane, error) {
	return s.mutate("state", target, func(q *Lane, _ *[]Lane) error { return s.preset(q, verb, note) })
}
func (s *Store) Start(target string, p Pane, branch, worktree string) (Lane, error) {
	return s.mutate("start", target, func(q *Lane, _ *[]Lane) error {
		if e := s.preset(q, "start", ""); e != nil {
			return e
		}
		if branch != "" {
			q.Branch = branch
		}
		if worktree != "" {
			q.Worktree = worktree
		}
		// Capture pane-less subagents as an unassociated participation record.
		if p.Window != "" || p.Harness != "" || p.Agent != "" || p.Model != "" || p.AgentID != "" {
			s.associate(q, p, false)
		}
		return nil
	})
}
func (s *Store) Merge(target, commit, note string) (Lane, error) {
	return s.mutate("merge", target, func(q *Lane, _ *[]Lane) error {
		if e := s.preset(q, "merge", note); e != nil {
			return e
		}
		if commit != "" {
			return s.link(q, Link{Kind: "commit", Target: commit}, false)
		}
		return nil
	})
}
func (s *Store) Delete(target string) (Lane, error) {
	return s.mutate("delete", target, func(q *Lane, rows *[]Lane) error {
		for i := range *rows {
			if (*rows)[i].ID != q.ID && descendant(*rows, (*rows)[i].ID, q.ID) {
				(*rows)[i].Deleted = true
				s.entry(&(*rows)[i], "delete", "", "", "subtree", nil, nil)
			}
		}
		q.Deleted = true
		s.entry(q, "delete", "", "", "", nil, nil)
		return nil
	})
}
