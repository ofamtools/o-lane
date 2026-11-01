package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke"
)

// LiveState is presentation data, shared by the CLI and portable page.
type LiveState struct {
	Name  string `json:"name"`
	Tone  string `json:"tone,omitempty"`
	Glyph string `json:"glyph,omitempty"`
}
type LivePane struct {
	Pane
	// AgentLive is an agent participant's derived state, when agents are read.
	AgentLive *AgentStatus `json:"agent_live,omitempty"`
	State     string       `json:"live"`
	Tone      string       `json:"tone,omitempty"`
	Glyph     string       `json:"glyph,omitempty"`
	Error     string       `json:"error,omitempty"`
}
type Live struct {
	State string     `json:"state"`
	Tone  string     `json:"tone,omitempty"`
	Glyph string     `json:"glyph,omitempty"`
	Panes []LivePane `json:"panes"`
}
type windowState struct {
	Window string `json:"window_id"`
	State  string `json:"agent_state"`
}
type commandRunner func(context.Context, []string) ([]byte, error)
type liveOptions struct {
	on                bool
	mapping           map[string]LiveState
	roles, precedence []string
	// agent reads an agent participant's state; nil leaves agents unread.
	agent func(Pane) AgentStatus
}

// String values from the L2 settings remain accepted for existing users.
func parseLiveStates(text string) (map[string]LiveState, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, errors.New("lanes.live-states needs a JSON object")
	}
	out := map[string]LiveState{}
	for key, value := range raw {
		var state LiveState
		if json.Unmarshal(value, &state.Name) != nil {
			if err := json.Unmarshal(value, &state); err != nil {
				return nil, err
			}
		}
		if strings.TrimSpace(state.Name) == "" {
			return nil, errors.New("lanes.live-states rows need a name")
		}
		out[key] = state
	}
	return out, nil
}
func (a *App) liveOptions() (liveOptions, error) {
	cfg := a.r.Settings()
	o := liveOptions{on: cfg.Bool("lanes.live"), roles: append([]string(nil), cfg.Strings("lanes.live-roles")...), precedence: append([]string(nil), cfg.Strings("lanes.live-precedence")...)}
	if !o.on {
		return o, nil
	}
	var err error
	o.mapping, err = parseLiveStates(cfg.String("lanes.live-states"))
	if err == nil && cfg.Bool("lanes.agent-live") {
		o.agent = a.agentReader()
	}
	return o, err
}

// panesInSubtree lists the panes that count for q's subtree. Unless panes
// are shared, a pane counts only for the lane holding its most recent
// association, so data recorded before the one-lane rule reads correctly.
func (s *Store) panesInSubtree(rows []Lane, q Lane) []Pane {
	panes := []Pane{}
	for _, row := range rows {
		if row.ID == q.ID || descendant(rows, row.ID, q.ID) {
			for _, p := range row.Panes {
				if s.PanesShared || paneHolder(rows, row.ID, p) == "" {
					panes = append(panes, p)
				}
			}
		}
	}
	return panes
}

// paneHolder returns the lane that associated p after laneID did, or "" when
// laneID holds p's most recent association (ties keep every holder).
func paneHolder(rows []Lane, laneID string, p Pane) string {
	if !identified(p) {
		return ""
	}
	holder, since := "", p.Since
	for _, r := range rows {
		if r.ID == laneID || r.Deleted {
			continue
		}
		for _, o := range r.Panes {
			if paneKey(o, p) && o.Since.After(since) {
				holder, since = r.ID, o.Since
			}
		}
	}
	return holder
}
func (a *App) associatedPanes(q Lane) ([]Pane, error) {
	rows, err := a.store.List()
	return a.store.panesInSubtree(rows, q), err
}

type liveScope struct{ Host, Session string }
type liveSnapshot struct {
	windows map[string]string
	err     error
}
type liveSnapshots map[liveScope]liveSnapshot

func scopeOf(p Pane) liveScope { return liveScope{p.Host, p.Session} }
func snapshotScopes(parent context.Context, panes []Pane, run commandRunner) liveSnapshots {
	out := liveSnapshots{}
	for _, p := range panes {
		if p.Window == "" {
			continue
		}
		key := scopeOf(p)
		if _, ok := out[key]; ok {
			continue
		}
		snap := liveSnapshot{windows: map[string]string{}}
		if p.Session == "" {
			snap.err = errors.New("pane session is unassociated")
		} else {
			session := p.Session
			if p.Host != "" {
				session = p.Host + ":" + session
			}
			ctx, cancel := context.WithTimeout(parent, 3*time.Second)
			b, err := run(ctx, []string{"tuios", "list-windows", "--json", "-s", session})
			cancel()
			snap.err = err
			if err == nil {
				windows, err := decodeTuiosList[windowState](b, "windows")
				snap.err = err
				for _, w := range windows {
					snap.windows[w.Window] = w.State
				}
			}
		}
		out[key] = snap
	}
	return out
}
func aggregateLive(panes []Pane, o liveOptions, snapshots liveSnapshots) Live {
	out := Live{State: "unassociated", Panes: []LivePane{}}
	if !o.on {
		out.State = "off"
		return out
	}
	states := map[string]LivePane{}
	for _, p := range panes {
		item := LivePane{Pane: p, State: "unassociated"}
		if p.AgentID != "" && o.agent != nil {
			st := o.agent(p)
			item.AgentLive = &st
			item.Error = st.Error
			mapped, ok := o.mapping[st.State]
			if !ok {
				mapped.Name = st.State
			}
			item.State, item.Tone, item.Glyph = mapped.Name, mapped.Tone, mapped.Glyph
		} else if p.Window != "" {
			snap, ok := snapshots[scopeOf(p)]
			if !ok || snap.err != nil {
				item.State = "unknown"
				if snap.err != nil {
					item.Error = snap.err.Error()
				}
			} else {
				raw, exists := snap.windows[p.Window]
				if !exists {
					raw = "closed"
				}
				st, ok := o.mapping[raw]
				if !ok {
					st.Name = raw
					if raw == "" || raw == "none" {
						st.Name = "shell"
					}
				}
				item.State, item.Tone, item.Glyph = st.Name, st.Tone, st.Glyph
			}
		}
		out.Panes = append(out.Panes, item)
		if len(o.roles) == 0 || slices.Contains(o.roles, p.Role) {
			states[item.State] = item
		}
	}
	winner := ""
	for _, name := range o.precedence {
		if _, ok := states[name]; ok {
			winner = name
			break
		}
	}
	if winner == "" {
		// Unknown/custom states have deterministic fallback ordering as well.
		names := make([]string, 0, len(states))
		for name := range states {
			if name != "unassociated" {
				names = append(names, name)
			}
		}
		slices.Sort(names)
		if len(names) > 0 {
			winner = names[0]
		}
	}
	if winner != "" {
		p := states[winner]
		out.State, out.Tone, out.Glyph = p.State, p.Tone, p.Glyph
	}
	return out
}
func (a *App) live(q Lane) (Live, error) {
	o, err := a.liveOptions()
	if err != nil {
		return Live{}, err
	}
	if !o.on {
		return aggregateLive(nil, o, nil), nil
	}
	panes, err := a.associatedPanes(q)
	if err != nil {
		return Live{}, err
	}
	return readLive(context.Background(), panes, o, a.run)
}
func readLive(ctx context.Context, panes []Pane, o liveOptions, run commandRunner) (Live, error) {
	if !o.on {
		return aggregateLive(nil, o, nil), nil
	}
	return aggregateLive(panes, o, snapshotScopes(ctx, panes, run)), nil
}
func (a *App) liveResult(_ ochazuke.Ctx, q Lane) ochazuke.Result {
	v, err := a.live(q)
	return done(v, err)
}
func (a *App) focusPane(c ochazuke.Ctx, q Lane, role string) ochazuke.Result {
	if c.Actor().IsAgent() && a.showUI == nil {
		return done(nil, errors.New("focus-pane needs a live UI (agents.live-only)"))
	}
	panes, e := a.associatedPanes(q)
	if e != nil {
		return done(nil, e)
	}
	o, e := a.liveOptions()
	if e != nil {
		return done(nil, e)
	}
	argv := append([]string{}, a.r.Settings().Strings("lanes.pane-open")...)
	run := a.run
	return ochazuke.Pending(func(ctx context.Context) (any, error) {
		live, e := readLive(ctx, panes, o, run)
		if e != nil {
			return nil, e
		}
		if !o.on {
			for _, p := range panes {
				live.Panes = append(live.Panes, LivePane{Pane: p})
			}
		}
		candidates := []LivePane{}
		for _, p := range live.Panes {
			if p.Window != "" && p.Session != "" && (role == "" || p.Role == role) {
				candidates = append(candidates, p)
			}
		}
		if len(candidates) == 0 {
			return nil, errors.New("lane is unassociated; associate a pane with a session first")
		}
		selected := candidates[0]
		for _, p := range candidates {
			if p.Role == "implementer" {
				selected = p
				break
			}
		}
		for _, p := range candidates {
			if p.State == live.State {
				selected = p
				break
			}
		}
		replace := strings.NewReplacer("{session}", selected.Session, "{window}", selected.Window, "{host}", selected.Host)
		for i := range argv {
			argv[i] = replace.Replace(argv[i])
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_, e = run(ctx, argv)
		return selected.Pane, e
	})
}

// Enrich a whole read answer using one snapshot per explicit host/session,
// irrespective of how many lanes share those panes. Nothing is stored.
func (a *App) enrichLive(views []View) error {
	o, err := a.liveOptions()
	if err != nil {
		return err
	}
	if !o.on {
		return nil
	}
	rows, err := a.store.List()
	if err != nil {
		return err
	}
	panes := []Pane{}
	for _, v := range views {
		panes = append(panes, a.store.panesInSubtree(rows, v.Lane)...)
	}
	snapshots := snapshotScopes(context.Background(), panes, a.run)
	for i := range views {
		v := aggregateLive(a.store.panesInSubtree(rows, views[i].Lane), o, snapshots)
		views[i].Live = &v
	}
	return nil
}
