package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ofam.dev/ochazuke"
)

type Actor = ochazuke.Actor

type Status struct {
	Name   string `json:"name"`
	Tone   string `json:"tone"`
	Starts bool   `json:"starts"`
	Ends   bool   `json:"ends"`
	Open   bool   `json:"open"`
	// Archive marks the status that carries ochazuke's archive role: lanes in
	// it are hidden from the default views (collection.hide-archived), do not hold
	// their parent's rolled status, and are what the archived prop reports.
	Archive bool `json:"archive,omitempty"`
	// Description says in plain words what the status means. The Lanes page
	// shows it under the focused lane; an empty description shows nothing.
	Description string `json:"description,omitempty"`
}
type Link struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Label  string `json:"label,omitempty"`
}
type Pane struct {
	Host    string `json:"host,omitempty"`
	Session string `json:"session,omitempty"`
	Window  string `json:"window,omitempty"`
	Role    string `json:"role"`
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
	Agent   string `json:"agent,omitempty"`
	// AgentID names a harness sub-agent (for example a Claude Code subagent)
	// whose live status comes from lanes.agent-harnesses; AgentSession is the
	// harness session it runs in, when known.
	AgentID      string    `json:"agent_id,omitempty"`
	AgentSession string    `json:"agent_session,omitempty"`
	Since        time.Time `json:"since"`
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
	// MovedTo names the lane a pane moved to when associating it there
	// removed it from this lane (lanes.pane-one-lane).
	MovedTo string `json:"moved_to,omitempty"`
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
	Project  string   `json:"project,omitempty"`
}
type Lane struct {
	Spec
	ID      string    `json:"id"`
	Status  string    `json:"status"`
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
	Stale           bool       `json:"stale"`
	StaleReason     string     `json:"stale_reason,omitempty"`
	Started         *time.Time `json:"started"`
	Finished        *time.Time `json:"finished"`
	EstimateMinutes float64    `json:"estimate_minutes"`
	RolledStatus    string     `json:"rolled_status"`
	Association     string     `json:"association"`
	// EffectiveProject is the lane's own project or the nearest ancestor's.
	EffectiveProject   string `json:"effective_project"`
	ProjectFrom        string `json:"project_from,omitempty"`
	ProjectAssociation string `json:"project_association"`
	// StatusDescription is the description of the recorded status from
	// lanes.statuses; StatusReason is the note that came with the latest status
	// change (see statusReason).
	StatusDescription string `json:"status_description,omitempty"`
	StatusReason      string `json:"status_reason,omitempty"`
}

func (s *Store) view(q Lane, rows []Lane) View {
	v := View{Lane: q, EstimateMinutes: estimateMinutes(q.Estimate), RolledStatus: q.Status, Association: "unassociated", ProjectAssociation: "unassociated"}
	v.EffectiveProject, v.ProjectFrom = effectiveProject(rows, q)
	v.StatusDescription = s.statuses()[q.Status].Description
	v.StatusReason = statusReason(q.History)
	switch {
	case v.ProjectFrom == q.ID:
		v.ProjectAssociation = "associated"
	case v.ProjectFrom != "":
		v.ProjectAssociation = "inherited"
	}
	for _, p := range q.Panes {
		if p.Window != "" || p.AgentID != "" {
			v.Association = "associated"
		}
	}
	for _, e := range activeHistory(q.History) {
		st, ok := s.statuses()[e.To]
		if !ok || st.Archive || (e.Kind != "status" && e.Kind != "create") {
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
	// A homogeneous subtree has that status. Mixed children use the first
	// configured open status present, then the first configured closed status.
	if q.Parent == "" {
		present := map[string]bool{}
		for _, c := range rows {
			if c.ID != q.ID && !s.statuses()[c.Status].Archive && descendant(rows, c.ID, q.ID) {
				present[c.Status] = true
			}
		}
		if len(present) > 0 {
			for _, open := range []bool{true, false} {
				for _, st := range s.statusRows() {
					if st.Open == open && present[st.Name] {
						v.RolledStatus = st.Name
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
func parseStatuses(raw string) ([]Status, error) {
	var out []Status
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("lanes.statuses must contain at least one status")
	}
	seen := map[string]bool{}
	for _, s := range out {
		if strings.TrimSpace(s.Name) == "" || seen[s.Name] {
			return nil, fmt.Errorf("invalid or duplicate status %q", s.Name)
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

// statusReason is why the lane is in its status: the latest status change's own
// note, else the latest note event recorded between the previous status change
// and that one (agents often write the reason as a note, then change status).
func statusReason(history []Entry) string {
	h := activeHistory(history)
	last := -1
	for i := len(h) - 1; i >= 0; i-- {
		if h[i].Kind == "status" || h[i].Kind == "create" {
			last = i
			break
		}
	}
	if last < 0 {
		return ""
	}
	if text := strings.TrimSpace(h[last].Text); text != "" {
		return text
	}
	for i := last - 1; i >= 0; i-- {
		if h[i].Kind == "status" || h[i].Kind == "create" {
			break
		}
		if text := strings.TrimSpace(h[i].Text); h[i].Kind == "note" && text != "" {
			return text
		}
	}
	return ""
}

// statusAbout is the detail text for a lane's status: the status name, its
// description and the reason. A status without a description shows nothing.
func statusAbout(name, description, reason string) string {
	if strings.TrimSpace(description) == "" {
		return ""
	}
	text := "**" + name + "** — " + strings.TrimSpace(description)
	if reason != "" {
		text += "\n\nNote: " + reason
	}
	return text
}
