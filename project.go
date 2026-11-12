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
	ocli "ofam.dev/ochazuke/cli"
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

// effectiveProjectLabel is effectiveProject with the name kept on the lane
// that recorded the project.
func effectiveProjectLabel(rows []Lane, q Lane) (project, from, label string) {
	project, from = effectiveProject(rows, q)
	if from == q.ID {
		return project, from, q.ProjectLabel
	}
	return project, from, labelOf(rows, from)
}

// subtreeProject reports whether q or any visible descendant belongs to the
// queried project, so filtered trees keep the ancestors of matching lanes.
func subtreeProject(rows []Lane, q Lane, match func(stored, label string) bool) bool {
	for _, c := range rows {
		if c.ID == q.ID || descendant(rows, c.ID, q.ID) {
			if p, from := effectiveProject(rows, c); p != "" && match(p, labelOf(rows, from)) {
				return true
			}
		}
	}
	return false
}

// labelOf is the project name kept on the lane with that id.
func labelOf(rows []Lane, id string) string {
	for _, p := range rows {
		if p.ID == id {
			return p.ProjectLabel
		}
	}
	return ""
}

func (s *Store) project(q *Lane, ref string, remove bool) error {
	ref = strings.TrimSpace(ref)
	if remove {
		if q.Project == "" {
			return fmt.Errorf("lane %s names no project of its own; unassociate the ancestor that records it", q.ID)
		}
		if ref != "" && !s.projectMatcher(ref)(q.Project, q.ProjectLabel) {
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
	if q.Project == ref || s.projectMatcher(ref)(q.Project, "") {
		return nil // already this project (by id, name or kind:id)
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

// hostProject is a project of the configured source: the id mtm keys it by
// and the name people see and type.
type hostProject struct{ ID, Name string }

// hostProjects asks the configured source (lanes.project-source) for its
// projects. Mounted calls use the host's complete argv; standalone calls
// retain the injectable PATH runner. Source "none" has no projects.
func (a *App) hostProjects(ctx context.Context) ([]hostProject, error) {
	switch source := a.r.Settings().String("lanes.project-source"); source {
	case "", "none":
		return nil, nil
	case "mtm":
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		host, hosted, e := ocli.HostFromEnv()
		if e != nil {
			return nil, fmt.Errorf("lanes.project-source mtm: %w", e)
		}
		var b []byte
		if hosted {
			b, e = host.Run(ctx, "project.list", nil)
		} else {
			b, e = a.run(ctx, []string{"mtm", "project", "list"})
		}
		if e != nil {
			return nil, fmt.Errorf("lanes.project-source mtm: %w", e)
		}
		// mtm answers an array, or {"items": [...]} for a paged list.
		var rows []struct{ ID, Name, Label string }
		if e = json.Unmarshal(b, &rows); e != nil {
			var page struct {
				Items []struct{ ID, Name, Label string }
			}
			if e2 := json.Unmarshal(b, &page); e2 != nil {
				return nil, fmt.Errorf("lanes.project-source mtm: %w", e)
			}
			rows = page.Items
		}
		out := []hostProject{}
		for _, r := range rows {
			name := r.Name
			if name == "" {
				name = r.Label
			}
			if name == "" {
				name = r.ID
			}
			out = append(out, hostProject{ID: r.ID, Name: name})
		}
		return out, nil
	default:
		return nil, fmt.Errorf("lanes.project-source: unknown source %q; use mtm or none", source)
	}
}

// sourceProjects is the source's projects as choosable references: the name.
func (a *App) sourceProjects(ctx context.Context) ([]projectRow, error) {
	list, e := a.hostProjects(ctx)
	out := []projectRow{}
	for _, p := range list {
		out = append(out, projectRow{Ref: p.Name, Name: p.Name, Source: a.r.Settings().String("lanes.project-source")})
	}
	return out, e
}

// projectRows merges references recorded on lanes with the source's projects.
func (a *App) projectRows(ctx context.Context) ([]projectRow, error) {
	lanes, e := a.store.List()
	if e != nil {
		return nil, e
	}
	counts := map[string]int{}
	for _, q := range lanes {
		if p, from := effectiveProject(lanes, q); p != "" {
			counts[a.store.projectDisplay(p, labelOf(lanes, from))]++
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
