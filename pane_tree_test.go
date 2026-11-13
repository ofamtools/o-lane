package main

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

// The one-pass subtree and holder reads give what the per-lane walk gave: the
// walk is kept here as the reference, over a fixture with deleted lanes, a
// pane moved between lanes, a shared agent and a parent cycle.
func TestPaneTreeMatchesWalk(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []Lane{}
	for i := 0; i < 40; i++ {
		q := Lane{ID: fmt.Sprint("l", i), Status: "running"}
		if i > 0 {
			q.Parent = fmt.Sprint("l", (i-1)/3)
		}
		q.Deleted = i%11 == 5
		q.Panes = []Pane{{Window: fmt.Sprint("w", i%9), Session: "s", Since: base.Add(time.Duration(i%7) * time.Minute)}}
		if i%6 == 0 {
			q.Panes = append(q.Panes, Pane{AgentID: "a", Harness: "h", Since: base.Add(time.Duration(i) * time.Minute)})
		}
		rows = append(rows, q)
	}
	rows[0].Parent = "l39" // a cycle must not loop
	walkDescendant := func(id, parent string) bool { return descendant(rows, id, parent) }
	holder := func(laneID string, p Pane) string {
		if !identified(p) {
			return ""
		}
		h, since := "", p.Since
		for _, r := range rows {
			if r.ID == laneID || r.Deleted {
				continue
			}
			for _, o := range r.Panes {
				if paneKey(o, p) && o.Since.After(since) {
					h, since = r.ID, o.Since
				}
			}
		}
		return h
	}
	for _, shared := range []bool{false, true} {
		s := &Store{PanesShared: shared}
		tree := s.paneTree(rows)
		for _, q := range rows {
			want := []Pane{}
			for _, row := range rows {
				if !row.Deleted && (row.ID == q.ID || walkDescendant(row.ID, q.ID)) {
					for _, p := range row.Panes {
						if shared || holder(row.ID, p) == "" {
							want = append(want, p)
						}
					}
				}
			}
			if got := tree.subtree(q, nil); !reflect.DeepEqual(got, want) {
				t.Fatalf("shared=%v lane %s: got %v, want %v", shared, q.ID, got, want)
			}
			for _, p := range q.Panes {
				if g, w := tree.holder(q.ID, p), holder(q.ID, p); g != w {
					t.Fatalf("lane %s holder %q, want %q", q.ID, g, w)
				}
			}
		}
	}
}
