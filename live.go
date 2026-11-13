package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"ofam.dev/ochazuke"
)

// LiveStatus is presentation data, shared by the CLI and portable page.
type LiveStatus struct {
	Name    string `json:"name"`
	Tone    string `json:"tone,omitempty"`
	Glyph   string `json:"glyph,omitempty"`
	Working bool   `json:"working,omitempty"`
}
type LivePane struct {
	Pane
	// AgentLive is an agent participant's derived status, when agents are read.
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
type windowStatus struct {
	Window   string `json:"window_id"`
	Status   string `json:"agent_state"`
	StatusAt int64  `json:"agent_state_at"`
}
type commandRunner func(context.Context, []string) ([]byte, error)
type liveOptions struct {
	on                bool
	mapping           map[string]LiveStatus
	roles, precedence []string
	// agent reads an agent participant's status; nil leaves agents unread.
	agent func(Pane) AgentStatus
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
	// Label-only overrides retain the activity semantics from the default
	// data. An object can explicitly override working, including to false.
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
func (a *App) liveOptions() (liveOptions, error) {
	cfg := a.r.Settings()
	o := liveOptions{on: cfg.Bool("lanes.live"), roles: append([]string(nil), cfg.Strings("lanes.live-roles")...), precedence: append([]string(nil), cfg.Strings("lanes.live-precedence")...)}
	if !o.on {
		return o, nil
	}
	var err error
	o.mapping, err = parseLiveStatuses(cfg.String("lanes.live-statuses"))
	if err == nil && cfg.Bool("lanes.agent-live") {
		o.agent = a.agentReader()
	}
	return o, err
}

// panesInSubtree lists the panes that count for q's subtree. Unless panes
// are shared, a pane counts only for the lane holding its most recent
// association, so data recorded before the one-lane rule reads correctly.
// A caller that asks for many lanes builds one paneTree and reuses it.
func (s *Store) panesInSubtree(rows []Lane, q Lane) []Pane {
	return s.paneTree(rows).subtree(q, nil)
}

// paneTree is one read's parent-to-children and pane-to-lanes maps, so each
// lane's subtree and each pane's holder cost one pass over what they touch,
// not a walk of every lane per lane.
type paneTree struct {
	shared   bool
	rows     []Lane
	rowsByID map[string][]int // row indexes by lane id, in row order
	children map[string][]string
	held     []heldPane // every pane of a live lane, in row order
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

// subtree lists the panes of q and its descendants (rows keep their order),
// limited to the rows include accepts.
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

// holder returns the lane that associated p after laneID did, or "" when
// laneID holds p's most recent association (ties keep every holder).
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

func (a *App) associatedPanes(q Lane) ([]Pane, error) {
	rows, err := a.store.List()
	return a.store.panesInSubtree(rows, q), err
}

type liveScope struct{ Host, Session string }
type liveSnapshot struct {
	windows     map[string]string
	statusTimes map[string]time.Time
	err         error
}
type liveSnapshots map[liveScope]liveSnapshot

func scopeOf(p Pane) liveScope { return liveScope{p.Host, p.Session} }

// windowID keeps exact IDs authoritative and resolves only unique prefixes
// within this host/session snapshot. Ambiguous and missing refs stay unresolved.
func (s liveSnapshot) windowID(ref string) (string, bool) {
	if ref == "" {
		return "", false
	}
	if _, ok := s.windows[ref]; ok {
		return ref, true
	}
	match := ""
	for id := range s.windows {
		if strings.HasPrefix(id, ref) {
			if match != "" {
				return "", false
			}
			match = id
		}
	}
	return match, match != ""
}

// Association is still record-first when tuios is unavailable or a ref cannot
// be resolved. The same bounded snapshot lookup serves live reads and writes.
func (a *App) resolvePane(p Pane) Pane {
	if p.Window == "" || p.Session == "" {
		return p
	}
	snap := snapshotScopes(context.Background(), []Pane{p}, a.run, a.paneCommands())[scopeOf(p)]
	if id, ok := snap.windowID(p.Window); snap.err == nil && ok {
		p.Window = id
	}
	return p
}

// snapshotConcurrency bounds how many session reads run at once: each read
// starts a process, so the cost of N sessions is about N/4 starts, not N.
const snapshotConcurrency = 4

// snapshotScopes reads each distinct host/session once, concurrently (bounded),
// so one command or page load never asks twice for the same session and never
// waits on sessions one after another. A failed or slow read leaves that scope
// with an error (lanes list without live status for it), never blocks the rest.
func snapshotScopes(parent context.Context, panes []Pane, run commandRunner, cmds paneCommands) liveSnapshots {
	out := liveSnapshots{}
	var keys []liveScope
	for _, p := range panes {
		if p.Window == "" {
			continue
		}
		key := scopeOf(p)
		if _, ok := out[key]; ok {
			continue
		}
		out[key] = liveSnapshot{}
		keys = append(keys, key)
	}
	snaps := make([]liveSnapshot, len(keys))
	sem := make(chan struct{}, snapshotConcurrency)
	var wg sync.WaitGroup
	for i, key := range keys {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			snaps[i] = readScope(parent, key, run, cmds)
		}()
	}
	wg.Wait()
	for i, key := range keys {
		out[key] = snaps[i]
	}
	return out
}

func readScope(parent context.Context, key liveScope, run commandRunner, cmds paneCommands) liveSnapshot {
	snap := liveSnapshot{windows: map[string]string{}, statusTimes: map[string]time.Time{}}
	if key.Session == "" {
		snap.err = errors.New("pane session is unassociated")
		return snap
	}
	session := key.Session
	if key.Host != "" {
		session = key.Host + ":" + session
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	b, err := run(ctx, expand(cmds.Panes, "{session}", session))
	snap.err = err
	if err != nil {
		return snap
	}
	windows, err := decodeTuiosList[windowStatus](b, "windows")
	snap.err = err
	for _, w := range windows {
		snap.windows[w.Window] = w.Status
		if w.StatusAt != 0 {
			snap.statusTimes[w.Window] = time.Unix(0, w.StatusAt).UTC()
		}
	}
	return snap
}
func aggregateLive(panes []Pane, o liveOptions, snapshots liveSnapshots) Live {
	out := Live{Status: "unassociated", Panes: []LivePane{}}
	if !o.on {
		out.Status = "off"
		return out
	}
	statuses := map[string]LivePane{}
	for _, p := range panes {
		item := LivePane{Pane: p, Status: "unassociated"}
		if p.AgentID != "" && o.agent != nil {
			st := o.agent(p)
			item.AgentLive = &st
			item.Error = st.Error
			mapped, ok := o.mapping[st.Status]
			if !ok {
				mapped.Name = st.Status
			}
			item.Status, item.Tone, item.Glyph = mapped.Name, mapped.Tone, mapped.Glyph
			item.known, item.working = ok, mapped.Working
		} else if p.Window != "" {
			snap, ok := snapshots[scopeOf(p)]
			if !ok || snap.err != nil {
				item.Status = "unknown"
				if snap.err != nil {
					item.Error = snap.err.Error()
				}
			} else {
				id, exists := snap.windowID(p.Window)
				raw := snap.windows[id]
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
				item.Status, item.Tone, item.Glyph = st.Name, st.Tone, st.Glyph
				item.known, item.working = ok, st.Working
				if at, ok := snap.statusTimes[id]; ok {
					item.StatusSince = &at
				}
			}
		}
		out.Panes = append(out.Panes, item)
		if len(o.roles) == 0 || slices.Contains(o.roles, p.Role) {
			statuses[item.Status] = item
		}
	}
	winner := ""
	for _, name := range o.precedence {
		if _, ok := statuses[name]; ok {
			winner = name
			break
		}
	}
	if winner == "" {
		// Unknown/custom statuses have deterministic fallback ordering as well.
		names := make([]string, 0, len(statuses))
		for name := range statuses {
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
		p := statuses[winner]
		out.Status, out.Tone, out.Glyph = p.Status, p.Tone, p.Glyph
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
	return readLive(context.Background(), panes, o, a.run, a.paneCommands())
}
func readLive(ctx context.Context, panes []Pane, o liveOptions, run commandRunner, cmds paneCommands) (Live, error) {
	if !o.on {
		return aggregateLive(nil, o, nil), nil
	}
	return aggregateLive(panes, o, snapshotScopes(ctx, panes, run, cmds)), nil
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
	run, cmds := a.run, a.paneCommands()
	return ochazuke.Pending(func(ctx context.Context) (any, error) {
		live, e := readLive(ctx, panes, o, run, cmds)
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
			if p.Status == live.Status {
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
	tree := a.store.paneTree(rows)
	statuses := a.store.statuses()
	panes := []Pane{}
	for _, v := range views {
		panes = append(panes, tree.subtree(v.Lane, nil)...)
	}
	snapshots := snapshotScopes(context.Background(), panes, a.run, a.paneCommands())
	for i := range views {
		v := aggregateLive(tree.subtree(views[i].Lane, nil), o, snapshots)
		views[i].Live = &v
		activity := aggregateLive(tree.stale(views[i].Lane, statuses), o, snapshots)
		views[i].Stale, views[i].StaleReason = staleLane(views[i].Lane, statuses, activity, a.r.Settings().Bool("lanes.stale-flag"), a.store.now())
	}
	return nil
}
