package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"ofam.dev/ochazuke"
)

// A project is a reference only: an id or name with an optional host kind
// ("mtm:<id>"). o-lane keeps no project store and never requires a host.
var projectKind = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func splitProject(ref string) (kind, id string) {
	ref = strings.TrimSpace(ref)
	if k, rest, ok := strings.Cut(ref, ":"); ok && projectKind.MatchString(k) && rest != "" {
		return k, rest
	}
	return "", ref
}

// References match exactly, or by id/name when either side omits its kind.
func projectMatches(stored, query string) bool {
	if stored == "" || query == "" {
		return false
	}
	sk, sid := splitProject(stored)
	qk, qid := splitProject(query)
	if sk != "" && qk != "" && sk != qk {
		return false
	}
	return sid == qid
}

// The nearest ancestor that names a project supplies it; a child's own
// reference wins. The second result is the lane that recorded it.
func effectiveProject(rows []Lane, q Lane) (string, string) {
	seen := map[string]bool{}
	for !seen[q.ID] {
		if q.Project != "" {
			return q.Project, q.ID
		}
		seen[q.ID] = true
		if q.Parent == "" {
			break
		}
		found := false
		for _, p := range rows {
			if p.ID == q.Parent {
				q, found = p, true
				break
			}
		}
		if !found {
			break
		}
	}
	return "", ""
}

// subtreeProject reports whether q or any visible descendant belongs to the
// queried project, so filtered trees keep the ancestors of matching lanes.
func subtreeProject(rows []Lane, q Lane, query string) bool {
	for _, c := range rows {
		if c.ID == q.ID || descendant(rows, c.ID, q.ID) {
			if p, _ := effectiveProject(rows, c); projectMatches(p, query) {
				return true
			}
		}
	}
	return false
}

func (s *Store) project(q *Lane, ref string, remove bool) error {
	ref = strings.TrimSpace(ref)
	if remove {
		if q.Project == "" {
			return fmt.Errorf("lane %s names no project of its own; unassociate the ancestor that records it", q.ID)
		}
		if ref != "" && !projectMatches(q.Project, ref) {
			return fmt.Errorf("lane %s is associated with project %q, not %q", q.ID, q.Project, ref)
		}
		old := q.Project
		q.Project = ""
		s.entry(q, "unassociate", old, "", "project", nil, nil)
		return nil
	}
	if ref == "" {
		return errors.New("project must not be empty")
	}
	if q.Project == ref {
		return nil
	}
	old := q.Project
	q.Project = ref
	s.entry(q, "associate", old, ref, "project", nil, nil)
	return nil
}

// AssociateProject records the lane's own project; descendants without their
// own reference inherit it, so a root association covers its subtree.
func (s *Store) AssociateProject(target, ref string, remove bool) (Lane, error) {
	action := "associate"
	if remove {
		action = "unassociate"
	}
	return s.mutate(action, target, func(q *Lane, _ *[]Lane) error { return s.project(q, ref, remove) })
}

type projectRow struct {
	Ref    string `json:"ref"`
	Name   string `json:"name,omitempty"`
	Source string `json:"source"`
	Lanes  int    `json:"lanes"`
}

// projectSourceDefault picks mtm when it is installed; nothing requires it.
func projectSourceDefault() string {
	if _, e := exec.LookPath("mtm"); e == nil {
		return "mtm"
	}
	return "none"
}

// sourceProjects asks the configured host for its projects through the
// injectable runner. Only completion and project.list use it.
func (a *App) sourceProjects(ctx context.Context) ([]projectRow, error) {
	switch source := a.r.Settings().String("lanes.project-source"); source {
	case "", "none":
		return nil, nil
	case "mtm":
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		b, e := a.run(ctx, []string{"mtm", "project", "list"})
		if e != nil {
			return nil, fmt.Errorf("lanes.project-source mtm: %w", e)
		}
		var rows []struct{ ID, Name, Label string }
		if e = json.Unmarshal(b, &rows); e != nil {
			return nil, fmt.Errorf("lanes.project-source mtm: %w", e)
		}
		out := []projectRow{}
		for _, r := range rows {
			name := r.Name
			if name == "" {
				name = r.Label
			}
			if name == "" {
				name = r.ID
			}
			out = append(out, projectRow{Ref: name, Name: name, Source: source})
		}
		return out, nil
	default:
		return nil, fmt.Errorf("lanes.project-source: unknown source %q; use mtm or none", source)
	}
}

// projectRows merges references recorded on lanes with the source's projects.
func (a *App) projectRows(ctx context.Context) ([]projectRow, error) {
	lanes, e := a.store.List()
	if e != nil {
		return nil, e
	}
	counts := map[string]int{}
	for _, q := range lanes {
		if p, _ := effectiveProject(lanes, q); p != "" {
			counts[p]++
		}
	}
	out := []projectRow{}
	source, srcErr := a.sourceProjects(ctx)
	for _, r := range source {
		for ref, n := range counts {
			if projectMatches(ref, r.Ref) {
				r.Lanes += n
				delete(counts, ref)
			}
		}
		out = append(out, r)
	}
	for ref, n := range counts {
		out = append(out, projectRow{Ref: ref, Source: "lanes", Lanes: n})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Ref) < strings.ToLower(out[j].Ref) })
	return out, srcErr
}

func (a *App) projectChoices(ctx context.Context) ([]ochazuke.Choice, error) {
	rows, e := a.projectRows(ctx)
	out := []ochazuke.Choice{}
	for _, r := range rows {
		out = append(out, ochazuke.Choice{ID: r.Ref, Label: r.Ref})
	}
	// A failed source still offers recorded references; free text is accepted.
	if len(out) > 0 {
		e = nil
	}
	return out, e
}

func projectParam(desc string) ochazuke.Parameter {
	return ochazuke.Parameter{Type: "string", Source: "project.choices", Free: true, Description: desc}
}

const projectHelp = "Project reference (id or name, optional kind such as mtm:<id>); agents should always set one. Children inherit it; missing is unassociated"
