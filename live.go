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
type commandRunner func(context.Context, []string) ([]byte, error)
type liveOptions struct {
	on                bool
	mapping           map[string]string
	roles, precedence []string
}

func (a *App) liveOptions() (liveOptions, error) {
	cfg := a.r.Settings()
	o := liveOptions{on: cfg.Bool("lanes.live"), roles: cfg.Strings("lanes.live-roles"), precedence: cfg.Strings("lanes.live-precedence"), mapping: map[string]string{}}
	if !o.on {
		return o, nil
	}
	e := json.Unmarshal([]byte(cfg.String("lanes.live-states")), &o.mapping)
	return o, e
}
func (a *App) associatedPanes(q Lane) ([]Pane, error) {
	rows, e := a.store.List()
	if e != nil {
		return nil, e
	}
	panes := []Pane{}
	for _, row := range rows {
		if row.ID == q.ID || q.Parent == "" && descendant(rows, row.ID, q.ID) {
			panes = append(panes, row.Panes...)
		}
	}
	return panes, nil
}

// Snapshot options on the loop; command work consumes only values and an
// injectable runner. Neither live reads nor focus persist derived state.
func (a *App) live(q Lane) (Live, error) {
	o, e := a.liveOptions()
	if e != nil {
		return Live{}, e
	}
	if !o.on {
		return Live{State: "off", Panes: []LivePane{}}, nil
	}
	panes, e := a.associatedPanes(q)
	if e != nil {
		return Live{}, e
	}
	return readLive(context.Background(), panes, o, a.run)
}
func readLive(parent context.Context, panes []Pane, o liveOptions, run commandRunner) (Live, error) {
	out := Live{State: "unassociated", Panes: []LivePane{}}
	if !o.on {
		out.State = "off"
		return out, nil
	}
	type snapshot struct {
		windows map[string]string
		err     error
	}
	snapshots := map[string]snapshot{}
	states := map[string]bool{}
	for _, p := range panes {
		if len(o.roles) > 0 && !slices.Contains(o.roles, p.Role) {
			continue
		}
		item := LivePane{Pane: p, State: "unassociated"}
		if p.Window != "" {
			key := p.Host + "\x00" + p.Session
			snap, ok := snapshots[key]
			if !ok {
				snap.windows = map[string]string{}
				if p.Session == "" {
					snap.err = errors.New("pane session is unassociated")
				} else {
					argv := []string{"tuios", "list-windows", "--json", "-s", p.Session}
					if p.Host != "" {
						b, _ := json.Marshal(map[string]string{"host": p.Host, "session": p.Session})
						argv = []string{"tuios", "call", "list-windows", string(b)}
					}
					ctx, cancel := context.WithTimeout(parent, 3*time.Second)
					b, err := run(ctx, argv)
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
				item.State = o.mapping[raw]
				if item.State == "" {
					if raw == "" {
						item.State = "shell"
					} else {
						item.State = raw
					}
				}
			} else {
				item.State = o.mapping["closed"]
				if item.State == "" {
					item.State = "closed"
				}
			}
		}
		out.Panes = append(out.Panes, item)
		states[item.State] = true
	}
	for _, name := range o.precedence {
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
