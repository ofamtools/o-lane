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
var editable = []string{"estimate", "repo", "base", "branch", "worktree", "depends", "plan", "task", "scope", "done_when", "tests"}

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
	return json.Unmarshal(b, q)
}
func (s *Store) add(rows *[]Lane, spec Spec) (Lane, error) {
	if strings.TrimSpace(spec.Name) == "" {
		return Lane{}, errors.New("lane name must not be empty")
	}
	if spec.Parent != "" {
		p, e := resolve(visible(*rows), spec.Parent)
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

// Define is an atomic JSON batch. Plan-table parsing belongs to L3. Parents
// may precede or follow children; repeated definitions match (parent, name),
// update supplied spec fields, and preserve the owner's existing wording.
func (s *Store) Define(specs []Spec) (out []Lane, err error) {
	if len(specs) == 0 {
		return nil, errors.New("define needs at least one lane")
	}
	err = s.change("define", func(rows *[]Lane) error {
		pending := append([]Spec{}, specs...)
		for len(pending) > 0 {
			progress := false
			next := []Spec{}
			for _, spec := range pending {
				if strings.TrimSpace(spec.Name) == "" {
					return errors.New("lane name must not be empty")
				}
				if spec.Parent != "" {
					p, e := resolve(visible(*rows), spec.Parent)
					if e != nil {
						next = append(next, spec)
						continue
					}
					spec.Parent = p.ID
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
	return a.Host == b.Host && a.Session == b.Session && a.Window == b.Window && a.Role == b.Role && a.Harness == b.Harness && a.Model == b.Model && a.Agent == b.Agent
}
func paneKey(a, b Pane) bool {
	if a.Window != "" || b.Window != "" {
		return a.Host == b.Host && a.Session == b.Session && a.Window == b.Window
	}
	return samePane(a, b)
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
			if p.Window != "" {
				return old.Window == p.Window && (p.Session == "" || old.Session == p.Session) && (p.Host == "" || old.Host == p.Host)
			}
			return paneKey(old, p)
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
func (s *Store) Start(target string, p Pane, branch, worktree string) (Lane, error) {
	return s.mutate("start", target, func(q *Lane, _ *[]Lane) error {
		if e := s.state(q, "running", ""); e != nil {
			return e
		}
		if branch != "" {
			q.Branch = branch
		}
		if worktree != "" {
			q.Worktree = worktree
		}
		// Capture pane-less subagents as an unassociated participation record.
		if p.Window != "" || p.Harness != "" || p.Agent != "" || p.Model != "" {
			s.associate(q, p, false)
		}
		return nil
	})
}
func (s *Store) Merge(target, commit, note string) (Lane, error) {
	return s.mutate("merge", target, func(q *Lane, _ *[]Lane) error {
		if e := s.state(q, "merged", note); e != nil {
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
