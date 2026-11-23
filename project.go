package main

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
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
		if ref != "" && !s.projectMatcher(ref, nil)(q.Project, q.ProjectLabel) {
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
	if q.Project == ref || s.projectMatcher(ref, nil)(q.Project, "") {
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

const projectHelp = "Project reference (id or name, optional kind such as mtm:<id>); agents should always set one. Children inherit it; missing is unassociated"
