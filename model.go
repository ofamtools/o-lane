package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/unixsuperhero/ochazuke"
)

type Actor = ochazuke.Actor

type State struct {
	Name   string `json:"name"`
	Tone   string `json:"tone"`
	Starts bool   `json:"starts"`
	Ends   bool   `json:"ends"`
	Open   bool   `json:"open"`
}
type Link struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Label  string `json:"label,omitempty"`
}
type Pane struct {
	Host    string    `json:"host,omitempty"`
	Session string    `json:"session,omitempty"`
	Window  string    `json:"window,omitempty"`
	Role    string    `json:"role"`
	Harness string    `json:"harness,omitempty"`
	Model   string    `json:"model,omitempty"`
	Agent   string    `json:"agent,omitempty"`
	Since   time.Time `json:"since"`
}
type UndoRecord struct {
	Action        string   `json:"action"`
	OriginalActor Actor    `json:"original_actor"`
	Entries       []string `json:"entries"`
}
type Entry struct {
	Undo  *UndoRecord `json:"undo,omitempty"`
	ID    string      `json:"id"`
	At    time.Time   `json:"at"`
	By    Actor       `json:"by"`
	Kind  string      `json:"kind"`
	From  string      `json:"from,omitempty"`
	To    string      `json:"to,omitempty"`
	Text  string      `json:"text,omitempty"`
	Links []Link      `json:"links,omitempty"`
	Pane  *Pane       `json:"pane,omitempty"`
}
type Spec struct {
	Name     string   `json:"name"`
	Parent   string   `json:"parent,omitempty"`
	Wording  string   `json:"wording,omitempty"`
	Scope    string   `json:"scope,omitempty"`
	DoneWhen string   `json:"done_when,omitempty"`
	Tests    string   `json:"tests,omitempty"`
	Repo     string   `json:"repo,omitempty"`
	Base     string   `json:"base,omitempty"`
	Branch   string   `json:"branch,omitempty"`
	Worktree string   `json:"worktree,omitempty"`
	Depends  []string `json:"depends,omitempty"`
	Estimate string   `json:"estimate,omitempty"`
	Plan     string   `json:"plan,omitempty"`
	Task     string   `json:"task,omitempty"`
}
type Lane struct {
	Spec
	ID      string    `json:"id"`
	State   string    `json:"state"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	Panes   []Pane    `json:"panes"`
	Links   []Link    `json:"links"`
	History []Entry   `json:"history"`
	Deleted bool      `json:"deleted,omitempty"`
}
type View struct {
	Lane
	Live            *Live      `json:"live,omitempty"`
	Started         *time.Time `json:"started"`
	Finished        *time.Time `json:"finished"`
	EstimateMinutes float64    `json:"estimate_minutes"`
	RolledState     string     `json:"rolled_state"`
	Association     string     `json:"association"`
}

func (s *Store) view(q Lane, rows []Lane) View {
	v := View{Lane: q, EstimateMinutes: estimateMinutes(q.Estimate), RolledState: q.State, Association: "unassociated"}
	for _, p := range q.Panes {
		if p.Window != "" {
			v.Association = "associated"
		}
	}
	for _, e := range activeHistory(q.History) {
		st, ok := s.states()[e.To]
		if !ok || (e.Kind != "state" && e.Kind != "create") {
			continue
		}
		at := e.At
		if st.Starts && v.Started == nil {
			v.Started = &at
		}
		if st.Ends {
			v.Finished = &at
		} else {
			v.Finished = nil
		}
	}
	// A homogeneous subtree has that state. Mixed children use the first
	// configured open state present, then the first configured closed state.
	if q.Parent == "" {
		present := map[string]bool{}
		for _, c := range rows {
			if c.ID != q.ID && descendant(rows, c.ID, q.ID) {
				present[c.State] = true
			}
		}
		if len(present) > 0 {
			for _, open := range []bool{true, false} {
				for _, st := range s.stateRows() {
					if st.Open == open && present[st.Name] {
						v.RolledState = st.Name
						return v
					}
				}
			}
		}
	}
	return v
}

var estimatePattern = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*(minutes?|mins?|m|hours?|hrs?|h|days?|d)\b`)

func estimateMinutes(text string) float64 {
	n := 0.0
	for _, m := range estimatePattern.FindAllStringSubmatch(text, -1) {
		x, _ := strconv.ParseFloat(m[1], 64)
		switch strings.ToLower(m[2][:1]) {
		case "h":
			x *= 60
		case "d":
			x *= 24 * 60
		}
		n += x
	}
	return n
}
func parseStates(raw string) ([]State, error) {
	var out []State
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("lanes.states must contain at least one state")
	}
	seen := map[string]bool{}
	for _, s := range out {
		if strings.TrimSpace(s.Name) == "" || seen[s.Name] {
			return nil, fmt.Errorf("invalid or duplicate state %q", s.Name)
		}
		seen[s.Name] = true
	}
	return out, nil
}
func descendant(rows []Lane, id, parent string) bool {
	seen := map[string]bool{}
	for id != "" && !seen[id] {
		if id == parent {
			return true
		}
		seen[id] = true
		next := ""
		for _, q := range rows {
			if q.ID == id {
				next = q.Parent
				break
			}
		}
		id = next
	}
	return false
}

// Undo appends a receipt instead of removing events. Derived values read only
// the effective entries; the complete audit remains available to the owner.
func activeHistory(history []Entry) []Entry {
	undone := map[string]bool{}
	for _, en := range history {
		if en.Undo != nil {
			for _, id := range en.Undo.Entries {
				undone[id] = true
			}
		}
	}
	out := []Entry{}
	for _, en := range history {
		if en.Kind != "undo" && !undone[en.ID] {
			out = append(out, en)
		}
	}
	return out
}
