package main

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// AgentStatus is the derived live status of an associated harness sub-agent.
type AgentStatus struct {
	Status     string     `json:"status"`
	Since      *time.Time `json:"since,omitempty"`
	Transcript string     `json:"transcript,omitempty"`
	Error      string     `json:"error,omitempty"`
}

// LiveStatus is presentation data, shared by the CLI and TUI.
type LiveStatus struct {
	Name    string `json:"name"`
	Tone    string `json:"tone,omitempty"`
	Glyph   string `json:"glyph,omitempty"`
	Working bool   `json:"working,omitempty"`
}

type LivePane struct {
	Pane
	AgentLive      *AgentStatus `json:"agent_live,omitempty"`
	Status         string       `json:"live"`
	Tone           string       `json:"tone,omitempty"`
	Glyph          string       `json:"glyph,omitempty"`
	Error          string       `json:"error,omitempty"`
	StatusSince    *time.Time   `json:"status_since,omitempty"`
	known, working bool
}

type Live struct {
	Status string     `json:"status"`
	Tone   string     `json:"tone,omitempty"`
	Glyph  string     `json:"glyph,omitempty"`
	Panes  []LivePane `json:"panes"`
}

// String values from the L2 settings remain accepted for existing users.
func parseLiveStatuses(text string) (map[string]LiveStatus, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, errors.New("lanes.live-statuses needs a JSON object")
	}
	out := map[string]LiveStatus{}
	defaults := map[string]LiveStatus{}
	_ = json.Unmarshal([]byte(defaultLiveStatuses), &defaults)
	for key, value := range raw {
		status := LiveStatus{Working: defaults[key].Working}
		if json.Unmarshal(value, &status.Name) != nil {
			if err := json.Unmarshal(value, &status); err != nil {
				return nil, err
			}
		}
		if strings.TrimSpace(status.Name) == "" {
			return nil, errors.New("lanes.live-statuses rows need a name")
		}
		out[key] = status
	}
	return out, nil
}

// panesInSubtree lists the panes that count for q's subtree. Unless panes
// are shared, a pane counts only for the lane holding its most recent
// association, so data recorded before the one-lane rule reads correctly.
func (s *Store) panesInSubtree(rows []Lane, q Lane) []Pane {
	return s.paneTree(rows).subtree(q, nil)
}

type paneTree struct {
	shared   bool
	rows     []Lane
	rowsByID map[string][]int
	children map[string][]string
	held     []heldPane
	own      map[int][]Pane
}

type heldPane struct {
	lane string
	pane Pane
}

func (s *Store) paneTree(rows []Lane) *paneTree {
	t := &paneTree{shared: s.PanesShared, rows: rows, rowsByID: map[string][]int{}, children: map[string][]string{}, own: map[int][]Pane{}}
	for i, r := range rows {
		first := len(t.rowsByID[r.ID]) == 0
		t.rowsByID[r.ID] = append(t.rowsByID[r.ID], i)
		if first && r.Parent != "" {
			t.children[r.Parent] = append(t.children[r.Parent], r.ID)
		}
		if !r.Deleted {
			for _, p := range r.Panes {
				t.held = append(t.held, heldPane{r.ID, p})
			}
		}
	}
	return t
}

func (t *paneTree) subtree(q Lane, include func(Lane) bool) []Pane {
	ids := map[string]bool{q.ID: true}
	for queue := []string{q.ID}; len(queue) > 0; queue = queue[1:] {
		for _, c := range t.children[queue[0]] {
			if !ids[c] {
				ids[c] = true
				queue = append(queue, c)
			}
		}
	}
	idx := []int{}
	for id := range ids {
		idx = append(idx, t.rowsByID[id]...)
	}
	sort.Ints(idx)
	panes := []Pane{}
	for _, i := range idx {
		if row := t.rows[i]; !row.Deleted && (include == nil || include(row)) {
			panes = append(panes, t.ownPanes(i)...)
		}
	}
	return panes
}

func (t *paneTree) ownPanes(i int) []Pane {
	if own, ok := t.own[i]; ok {
		return own
	}
	own := []Pane{}
	for _, p := range t.rows[i].Panes {
		if t.shared || t.holder(t.rows[i].ID, p) == "" {
			own = append(own, p)
		}
	}
	t.own[i] = own
	return own
}

func (t *paneTree) holder(laneID string, p Pane) string {
	if !identified(p) {
		return ""
	}
	holder, since := "", p.Since
	for _, h := range t.held {
		if h.lane != laneID && paneKey(h.pane, p) && h.pane.Since.After(since) {
			holder, since = h.lane, h.pane.Since
		}
	}
	return holder
}
