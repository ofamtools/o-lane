package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/unixsuperhero/ochazuke"
)

type LivePane struct {
	Pane
	State string `json:"live"`
	Error string `json:"error,omitempty"`
}
type Live struct {
	State string     `json:"state"`
	Panes []LivePane `json:"panes"`
}
type windowState struct {
	Window string `json:"window_id"`
	State  string `json:"agent_state"`
}

// The CLI snapshot needs no daemon client or subscription. The runner is
// injectable; tests never connect to a user's tuios or drive a real pane.
func (a *App) live(q Lane) (Live, error) {
	out := Live{State: "unassociated", Panes: []LivePane{}}
	if !a.r.Settings().Bool("lanes.live") {
		out.State = "off"
		return out, nil
	}
	rows, e := a.store.List()
	if e != nil {
		return out, e
	}
	panes := []Pane{}
	for _, row := range rows {
		if row.ID == q.ID || q.Parent == "" && descendant(rows, row.ID, q.ID) {
			panes = append(panes, row.Panes...)
		}
	}
	mapping := map[string]string{}
	if e = json.Unmarshal([]byte(a.r.Settings().String("lanes.live-states")), &mapping); e != nil {
		return out, e
	}
	type snapshot struct {
		windows map[string]string
		err     error
	}
	snapshots := map[string]snapshot{}
	roles := a.r.Settings().Strings("lanes.live-roles")
	states := map[string]bool{}
	for _, p := range panes {
		if len(roles) > 0 && !slices.Contains(roles, p.Role) {
			continue
		}
		item := LivePane{Pane: p, State: "unassociated"}
		if p.Window != "" {
			key := p.Host + "\x00" + p.Session
			snap, ok := snapshots[key]
			if !ok {
				snap.windows = map[string]string{}
				// An explicit session is required: never fall back to the focused pane.
				if p.Session == "" {
					snap.err = errors.New("pane session is unassociated")
				} else {
					argv := []string{"tuios", "list-windows", "--json", "-s", p.Session}
					if p.Host != "" {
						b, _ := json.Marshal(map[string]string{"host": p.Host, "session": p.Session})
						argv = []string{"tuios", "call", "list-windows", string(b)}
					}
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					b, err := a.run(ctx, argv)
					cancel()
					snap.err = err
					if err == nil {
						var doc struct {
							Windows []windowState `json:"windows"`
						}
						snap.err = json.Unmarshal(b, &doc)
						for _, w := range doc.Windows {
							snap.windows[w.Window] = w.State
						}
					}
				}
				snapshots[key] = snap
			}
			if snap.err != nil {
				item.State = "unknown"
				item.Error = snap.err.Error()
			} else if raw, ok := snap.windows[p.Window]; ok {
				item.State = mapping[raw]
				if item.State == "" {
					if raw == "" {
						item.State = "shell"
					} else {
						item.State = raw
					}
				}
			} else {
				item.State = mapping["closed"]
				if item.State == "" {
					item.State = "closed"
				}
			}
		}
		out.Panes = append(out.Panes, item)
		states[item.State] = true
	}
	out.State = "unassociated"
	for _, name := range a.r.Settings().Strings("lanes.live-precedence") {
		if states[name] {
			out.State = name
			return out, nil
		}
	}
	for _, p := range out.Panes {
		if p.State != "unassociated" {
			out.State = p.State
			break
		}
	}
	return out, nil
}
func (a *App) liveResult(_ ochazuke.Ctx, q Lane) ochazuke.Result {
	v, e := a.live(q)
	return done(v, e)
}
func (a *App) focusPane(c ochazuke.Ctx, q Lane, role string) ochazuke.Result {
	// MCP is headless. Agent focus requires a live UI host (L4), while an
	// owner's explicit CLI command may use the configured external opener.
	if c.Actor().IsAgent() {
		return done(nil, errors.New("focus-pane needs a live UI (agents.live-only)"))
	}
	live, e := a.live(q)
	if e != nil {
		return done(nil, e)
	}
	candidates := []LivePane{}
	for _, p := range live.Panes {
		if p.Window != "" && p.Session != "" && (role == "" || p.Role == role) {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		return done(nil, errors.New("lane is unassociated; associate a pane with a session first"))
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
	argv := append([]string{}, a.r.Settings().Strings("lanes.pane-open")...)
	replace := strings.NewReplacer("{session}", selected.Session, "{window}", selected.Window, "{host}", selected.Host)
	for i := range argv {
		argv[i] = replace.Replace(argv[i])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, e = a.run(ctx, argv)
	return done(selected.Pane, e)
}
