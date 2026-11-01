package main

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

const pageID = "lanes"
const listID = "lanes/tree"

func (a *App) registerPage() error {
	a.r.EnsureConfigSettings()
	target := param("Lane ID, unique prefix or name")
	target.Focus = true
	target.Noun = "lane"
	for _, v := range []struct{ name, field, source, desc string }{
		{"choose-state", "state", "lane.states", "Choose and record a lifecycle state"},
		{"choose-pane", "pane", "pane.choices", "Choose a tuios window to associate"},
	} {
		v := v
		a.r.DefineOwn("lane").Verb(v.name, func(c ochazuke.Ctx) ochazuke.Result { return a.handle("lane", v.name, c) }, v.desc).Agent(ochazuke.AgentAllow).Positional("target", v.field)
		op := a.r.Operations["lane."+v.name]
		op.Parameters = map[string]ochazuke.Parameter{"target": target, v.field: {Type: "string", Required: true, Source: v.source, Description: v.desc}}
		if v.name == "choose-pane" {
			pane := op.Parameters["pane"]
			pane.Free = true // A window closing after the pick never loses the relation.
			op.Parameters["pane"] = pane
			op.Parameters["role"] = ochazuke.Parameter{Type: "string", Source: "pane.roles", Description: "Participant role; default implementer"}
		}
		a.r.Operations[op.Key()] = op
	}
	a.r.DefineOwn("lane").Verb("states", func(c ochazuke.Ctx) ochazuke.Result {
		if e := a.configure(); e != nil {
			return done(nil, e)
		}
		rows := []ochazuke.Choice{}
		for _, st := range a.store.stateRows() {
			rows = append(rows, ochazuke.Choice{ID: st.Name, Label: st.Name})
		}
		return done(rows, nil)
	}, "List configured lifecycle choices").Fetch().Quiet().Agent(ochazuke.AgentAllow)
	a.r.DefineOwn("pane").Verb("choices", func(c ochazuke.Ctx) ochazuke.Result {
		return ochazuke.Pending(func(ctx context.Context) (any, error) { return a.paneChoices(ctx) }).Replacing("pane-choices")
	}, "List tuios windows with explicit session references").Fetch().Quiet().Agent(ochazuke.AgentAllow)
	a.r.DefineOwn("project").Verb("choices", func(c ochazuke.Ctx) ochazuke.Result {
		if e := a.configure(); e != nil {
			return done(nil, e)
		}
		return ochazuke.Pending(func(ctx context.Context) (any, error) { return a.projectChoices(ctx) }).Replacing("project-choices")
	}, "List project references for completion (recorded on lanes plus lanes.project-source)").Fetch().Quiet().Agent(ochazuke.AgentAllow)
	a.r.DefineOwn("pane").Verb("roles", func(c ochazuke.Ctx) ochazuke.Result {
		return done(a.r.Settings().Strings("lanes.pane-roles"), nil)
	}, "List configured participant roles").Fetch().Quiet().Agent(ochazuke.AgentAllow)
	a.r.DefineOwn("lane").Verb("rows", func(c ochazuke.Ctx) ochazuke.Result {
		if e := a.configure(); e != nil {
			return done(nil, e)
		}
		s, o := *a.store, a.pageOptions()
		if project := ochazuke.StringArg(c.Args, "project", ""); project != "" {
			o.project = project
		}
		if states := stateList(c.Args["state"]); len(states) > 0 {
			o.states = states
		}
		if e := a.store.checkStates(o.states); e != nil {
			return done(nil, e)
		}
		reader, run := a.reader, commandRunner(a.run)
		return ochazuke.Pending(func(ctx context.Context) (any, error) { return loadLaneRows(ctx, &s, o, reader, run) }).Replacing("lane-rows").Then(func(_ ochazuke.Ctx, value any, e error) {
			if a.rowsLoaded != nil {
				rows, _ := value.([]ocli.Row)
				a.rowsLoaded(rows, e)
			}
		})
	}, "List portable tree page rows").Rows("lane").Fetch().Quiet().Agent(ochazuke.AgentAllow)
	rowsOp := a.r.Operations["lane.rows"]
	rowsOp.Parameters = map[string]ochazuke.Parameter{"project": param("Only subtrees containing lanes of this project; default lanes.project-filter"), "state": stateParam("default lanes.state-filter")}
	a.r.Operations[rowsOp.Key()] = rowsOp
	a.r.Define("lane").Columns(a.pageOptions().columns()...)
	b := a.r.Define("lane").Mode("normal")
	for _, row := range []struct{ key, verb string }{{"enter", "show"}, {"s", "choose-state"}, {"g p", "focus-pane"}, {"a", "choose-pane"}, {"n", "note"}, {"y", "yank-id"}} {
		b.Bind(row.key, ochazuke.Do("lane."+row.verb).FromPayload("target", "row"))
	}
	detail := a.r.Define("lane-detail").Pin("normal").Mode("normal")
	for _, row := range []struct {
		key   string
		lines int
	}{{"j", 1}, {"k", -1}, {"ctrl+d", 8}, {"ctrl+u", -8}, {"g g", -1000000}, {"G", 1000000}} {
		detail.Bind(row.key, ochazuke.Do("ochazuke.detail.scroll", ochazuke.Arguments{"id": "lane-detail/text", "lines": row.lines}))
	}
	detail.Bind("q", ochazuke.Do("ochazuke.modal.pop")).Bind("esc", ochazuke.Do("ochazuke.modal.pop"))
	return ocli.DeclarePage(a.r, lanePage(a.pageOptions()))
}

type pageOptions struct {
	tree, guides, durations, panes, history, tones, detail, hideClosed bool
	now                                                                time.Time
	live                                                               liveOptions
	liveTones                                                          bool
	liveRows                                                           map[string]Live
	staleLiveRows                                                      map[string]Live
	staleFlag                                                          bool
	groupProjects, projectColumn, nameTone, stateAbout                 bool
	project                                                            string
	states                                                             []string
}

func (a *App) pageOptions() pageOptions {
	cfg := a.r.Settings()
	live, _ := a.liveOptions()
	return pageOptions{live: live, staleFlag: cfg.Bool("lanes.stale-flag"), liveTones: cfg.Bool("lanes.live-tones"), tree: cfg.Bool("lanes.tree"), guides: true, durations: cfg.Bool("lanes.durations"), panes: cfg.Bool("lanes.pane-column"), history: cfg.Bool("lanes.history-in-detail"), tones: cfg.Bool("lanes.state-tones"), detail: cfg.Bool("lanes.detail"), hideClosed: cfg.Bool("lanes.hide-closed-roots"), now: a.store.now(), groupProjects: cfg.Bool("lanes.group-by-project"), projectColumn: cfg.Bool("lanes.project-column"), nameTone: cfg.Bool("lanes.name-tone"), stateAbout: cfg.Bool("lanes.state-descriptions"), project: cfg.String("lanes.project-filter"), states: cfg.Strings("lanes.state-filter")}
}
func (o pageOptions) columns() []ochazuke.Column {
	cols := []ochazuke.Column{{Title: "Name", Min: 16, Pinned: true}, {Title: "State", Max: 34, Priority: 1}}
	if o.projectColumn {
		cols = append(cols, ochazuke.Column{Title: "Project", Max: 24, Priority: 3})
	}
	cols = append(cols, ochazuke.Column{Title: "Repo", Max: 24, Priority: 4}, ochazuke.Column{Title: "Estimate", Width: 9, Priority: 5})
	if o.durations {
		cols = append(cols, ochazuke.Column{Title: "Elapsed / duration", Width: 18, Priority: 3})
	}
	if o.panes {
		cols = append(cols, ochazuke.Column{Title: "Panes", Max: 28, Priority: 2})
	}
	return append(cols, ochazuke.Column{Title: "Agents", Max: 44, Priority: 6})
}
func lanePage(o pageOptions) ocli.CatalogPage {
	p := ocli.CatalogPage{ID: pageID, Title: "Lanes", Layout: ocli.LayoutStack, Lists: []ocli.CatalogList{{ID: listID, Noun: "lane", Source: "lane.rows", Title: "Lanes", Tree: o.tree, Guides: o.guides}}}
	if !o.detail {
		return p
	}
	fields := []ocli.CatalogDetailField{}
	for _, name := range []string{"id", "name", "parent", "state", "rolled_state", "live_state", "project", "effective_project", "project_association", "repo", "base", "branch", "worktree", "estimate", "depends", "plan", "task", "tests", "links", "created", "updated", "started", "finished", "panes"} {
		label := strings.ReplaceAll(name, "_", " ")
		if name == "live_state" {
			// Live state is the panes' and agents' status, not the lane's.
			label = "agent status"
		}
		fields = append(fields, ocli.CatalogDetailField{Label: label, Field: name})
	}
	p.Detail = &ocli.CatalogDetail{Fields: []string{"name", "state", "effective_project", "wording", "scope", "done_when", "repo", "branch", "worktree", "estimate", "depends", "links", "panes"}, Sections: []ocli.CatalogDetailSection{
		{Kind: "markdown", Title: "Owner wording", Field: "wording"},
		{Kind: "markdown", Title: "Scope", Field: "scope"},
		{Kind: "markdown", Title: "Done when", Field: "done_when"},
		{Kind: "markdown", Title: "Panes", Field: "pane_detail"},
		{Kind: "fields", Title: "Fields", Fields: fields},
	}}
	if o.staleFlag {
		p.Detail.Sections = append([]ocli.CatalogDetailSection{{Kind: "markdown", Field: "stale_reason"}}, p.Detail.Sections...)
		p.Detail.Fields = append(p.Detail.Fields, "stale_reason")
	}
	if o.stateAbout {
		// Untitled, so a state without a description draws nothing at all.
		p.Detail.Sections = append([]ocli.CatalogDetailSection{{Kind: "markdown", Field: "state_about"}}, p.Detail.Sections...)
		p.Detail.Fields = append(p.Detail.Fields, "state_about")
	}
	if o.history {
		p.Detail.Sections = append(p.Detail.Sections, ocli.CatalogDetailSection{Kind: "thread", Title: "History", Field: "history", Author: "author", At: "at", Body: "body"})
		p.Detail.Fields = append(p.Detail.Fields, "history")
	}
	return p
}

func allPanes(rows []Lane) []Pane {
	panes := []Pane{}
	for _, q := range rows {
		panes = append(panes, q.Panes...)
	}
	return panes
}
func loadLaneRows(ctx context.Context, s *Store, o pageOptions, reader *liveReader, run commandRunner) ([]ocli.Row, error) {
	rows, err := s.List()
	if err != nil {
		return nil, err
	}
	if o.live.on {
		panes := allPanes(rows)
		var snapshots liveSnapshots
		if reader != nil {
			snapshots = reader.current(panes)
		} else {
			snapshots = snapshotScopes(ctx, panes, run)
		}
		o.liveRows = map[string]Live{}
		o.staleLiveRows = map[string]Live{}
		for _, q := range rows {
			o.liveRows[q.ID] = aggregateLive(s.panesInSubtree(rows, q), o.live, snapshots)
			o.staleLiveRows[q.ID] = aggregateLive(s.stalePanes(rows, q), o.live, snapshots)
		}
	}
	return projectLaneRows(s, o, rows)
}

// The page is a projection only: durations, root rollup and presentation never
// write derived values back into the store. A single read supplies the tree.
func laneRows(s *Store, o pageOptions) ([]ocli.Row, error) {
	lanes, e := s.List()
	if e != nil {
		return nil, e
	}
	return projectLaneRows(s, o, lanes)
}
func projectLaneRows(s *Store, o pageOptions, lanes []Lane) ([]ocli.Row, error) {
	var e error
	sort.SliceStable(lanes, func(i, j int) bool {
		if lanes[i].Created.Equal(lanes[j].Created) {
			return lanes[i].ID > lanes[j].ID
		}
		return lanes[i].Created.After(lanes[j].Created)
	})
	if o.groupProjects {
		// Roots group by effective project (stable, so newest first within a
		// group); unassociated roots come last. Children keep tree order.
		group := func(q Lane) string { p, _ := effectiveProject(lanes, q); return strings.ToLower(p) }
		// Children all sort after roots; stability keeps sibling order.
		rank := func(q Lane) (int, string) {
			if q.Parent != "" {
				return 2, ""
			}
			if g := group(q); g != "" {
				return 0, g
			}
			return 1, ""
		}
		sort.SliceStable(lanes, func(i, j int) bool {
			ri, gi := rank(lanes[i])
			rj, gj := rank(lanes[j])
			if ri != rj {
				return ri < rj
			}
			return gi < gj
		})
	}
	out := []ocli.Row{}
	kept := stateKept(lanes, o.states)
	var walk func(string)
	walk = func(parent string) {
		for _, q := range lanes {
			if q.Parent != parent {
				continue
			}
			if o.project != "" && !subtreeProject(lanes, q, o.project) {
				continue
			}
			if kept != nil && !kept[q.ID] {
				continue
			}
			v := s.view(q, lanes)
			if parent == "" && o.hideClosed && !s.states()[v.RolledState].Open {
				continue
			}
			state := v.RolledState
			cells := []string{q.Name, state}
			if o.projectColumn {
				project := v.EffectiveProject
				if project == "" {
					project = "unassociated"
				}
				cells = append(cells, project)
			}
			cells = append(cells, q.Repo, q.Estimate)
			live, hasLive := o.liveRows[q.ID]
			showLive := hasLive && o.live.on && s.states()[q.State].Open
			if showLive {
				cells[1] += " · " + live.State
				if live.Glyph != "" {
					cells[0] = live.Glyph + " " + q.Name
				}
			}
			stale, staleReason := false, ""
			if activity, ok := o.staleLiveRows[q.ID]; ok && o.live.on {
				stale, staleReason = staleLane(q, s.states(), activity, o.staleFlag, o.now)
			}
			staleStart := len([]rune(cells[1])) + 3
			if stale {
				cells[1] += " · stale"
			}
			duration := ""
			if o.durations {
				if v.Started != nil {
					end := o.now
					if v.Finished != nil {
						end = *v.Finished
					}
					duration = humanDuration(end.Sub(*v.Started))
				}
				cells = append(cells, duration)
			}
			paneLabels, agents, paneDetail := []string{}, []string{}, []string{}
			roles := map[string]int{}
			for _, p := range q.Panes {
				if holder := paneHolder(lanes, q.ID, p); !s.PanesShared && holder != "" {
					// Another lane associated this pane later: it is history
					// here, so it shows no role, agent or live status.
					name := holder
					for _, r := range lanes {
						if r.ID == holder {
							name = r.Name
						}
					}
					ref := p.Window
					if ref == "" {
						ref = "agent " + p.AgentID
					}
					paneDetail = append(paneDetail, fmt.Sprintf("- %s: %s · moved to lane %s", p.Role, ref, name))
					continue
				}
				if p.Window != "" {
					roles[p.Role]++
				}
				agent := strings.Join(slices.DeleteFunc([]string{p.Harness, p.Model, p.Agent, shortAgentID(p.AgentID)}, func(w string) bool { return w == "" }), " ")
				window := p.Window
				if window == "" {
					window = "unassociated"
				}
				liveText, agentLive := "", ""
				if hasLive && o.live.on {
					for _, lp := range live.Panes {
						if lp.Pane == p {
							liveText = " · agent " + lp.State
							if lp.AgentLive != nil {
								// An agent's own state and last-activity age sit
								// beside it in the Agents column.
								agentLive = " · " + lp.State
								if lp.AgentLive.Activity != nil {
									agentLive += " " + humanDuration(o.now.Sub(*lp.AgentLive.Activity))
									liveText += " · active " + lp.AgentLive.Activity.Format(time.RFC3339)
								}
								if lp.AgentLive.Session != "" {
									liveText += " · agent session " + lp.AgentLive.Session
								}
							}
							break
						}
					}
				}
				if label := agent + agentLive; agent != "" && !slices.Contains(agents, label) {
					agents = append(agents, label)
				}
				if p.AgentID != "" {
					liveText = " · agent id " + p.AgentID + liveText
				}
				paneDetail = append(paneDetail, fmt.Sprintf("- %s: %s · %s · session %s · since %s", p.Role, window, agent, p.Session, p.Since.Format(time.RFC3339))+liveText)
			}
			keys := make([]string, 0, len(roles))
			for role := range roles {
				keys = append(keys, role)
			}
			slices.Sort(keys)
			for _, role := range keys {
				label := role
				if roles[role] > 1 {
					label = fmt.Sprintf("%s ×%d", role, roles[role])
				}
				paneLabels = append(paneLabels, label)
			}
			paneText := strings.Join(paneLabels, ", ")
			if paneText == "" {
				paneText = "unassociated"
			}
			if o.panes {
				cells = append(cells, paneText)
			}
			cells = append(cells, strings.Join(agents, ", "))
			data, err := ochazuke.ThingOf(v)
			if err != nil {
				e = err
				return
			}
			if hasLive && o.live.on {
				data["live"] = live
				data["live_state"] = live.State
			}
			data["stale"], data["stale_reason"] = stale, staleReason
			laneFilterValues(data, q)
			if about := stateAbout(q.State, v.StateDescription, v.StateReason); o.stateAbout && about != "" {
				data["state_about"] = about
			}
			data["pane_detail"] = strings.Join(paneDetail, "\n")
			if len(paneDetail) == 0 {
				data["pane_detail"] = "unassociated"
			}
			if o.durations {
				data["duration"] = duration
			}
			if o.history {
				history := []any{}
				for _, en := range q.History {
					author := en.By.Kind
					if en.By.Name != "" {
						author += " · " + en.By.Name
					}
					body := strings.TrimSpace(strings.Join([]string{en.Kind, en.From, en.To, en.Text}, " "))
					if en.Pane != nil {
						body += " · " + en.Pane.Role + " " + en.Pane.Window
					}
					if en.Undo != nil {
						body += " · undo " + en.Undo.Action + " (" + en.Undo.OriginalActor.Kind + ")"
					}
					entry, err := ochazuke.ThingOf(en)
					if err != nil {
						e = err
						return
					}
					entry["author"], entry["body"] = author, body
					history = append(history, map[string]any(entry))
				}
				data["history"] = history
			} else {
				delete(data, "history")
			}
			row := ocli.Row{Noun: "lane", ID: q.ID, Label: q.Name, Cells: cells, Data: map[string]any(data)}
			if o.tree {
				row.Parent = q.Parent
			}
			// The name takes whichever tone draws the state cell: the live tone
			// when it is shown, else the lifecycle tone. Both come from data.
			nameTone := ""
			if o.tones {
				nameTone = s.states()[state].Tone
			}
			if showLive && o.liveTones && live.Tone != "" {
				nameTone = live.Tone
			}
			if o.tones {
				span := ochazuke.CellTone(1, s.states()[state].Tone)
				if showLive {
					span.To = len([]rune(state))
				}
				row.Tones = append(row.Tones, span)
			}
			if showLive && o.liveTones && live.Tone != "" {
				row.Tones = append(row.Tones, ochazuke.SpanTone(1, len([]rune(state))+3, 0, live.Tone))
				if live.Glyph != "" {
					row.Tones = append(row.Tones, ochazuke.SpanTone(0, 0, len([]rune(live.Glyph)), live.Tone))
				}
			}
			if stale && o.tones {
				row.Tones = append(row.Tones, ochazuke.SpanTone(1, staleStart, 0, ochazuke.ToneNeedsInput))
			}
			if o.nameTone && nameTone != "" {
				row.Tones = append(row.Tones, ochazuke.CellTone(0, nameTone))
			}
			out = append(out, row)
			walk(q.ID)
		}
	}
	walk("")
	return out, e
}

// shortAgentID keeps agent ids readable in a cell; the full id is in detail.
func shortAgentID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Minute)
	if d < time.Minute {
		return "<1m"
	}
	h := int(d / time.Hour)
	m := int(d/time.Minute) % 60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

// A chooser discovers sessions once, then addresses each session explicitly.
// The runner is shared with live/focus and has no shell evaluation.
func (a *App) paneChoices(parent context.Context) ([]ochazuke.Choice, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	b, e := a.run(ctx, []string{"tuios", "list-sessions", "--json"})
	if e != nil {
		return nil, e
	}
	sessions, e := decodeTuiosList[struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	}](b, "sessions")
	if e != nil {
		return nil, e
	}
	out := []ochazuke.Choice{}
	for _, session := range sessions {
		name := session.Name
		if name == "" {
			name = session.ID
		}
		if name == "" {
			continue
		}
		b, e = a.run(ctx, []string{"tuios", "list-windows", "--json", "-s", name})
		if e != nil {
			return nil, fmt.Errorf("session %s: %w", name, e)
		}
		windows, e := decodeTuiosList[struct {
			ID      string `json:"window_id"`
			Name    string `json:"name"`
			Title   string `json:"title"`
			Harness string `json:"harness"`
		}](b, "windows")
		if e != nil {
			return nil, e
		}
		for _, w := range windows {
			if w.ID == "" {
				continue
			}
			id := url.PathEscape(name) + "/" + url.PathEscape(w.ID)
			if w.Harness != "" {
				id += "?harness=" + url.QueryEscape(w.Harness)
			}
			label := w.Name
			if label == "" {
				label = w.Title
			}
			out = append(out, ochazuke.Choice{ID: id, Label: strings.TrimSpace(label)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func paneChoice(ref string) (Pane, error) {
	path, query, _ := strings.Cut(ref, "?")
	session, window, ok := strings.Cut(path, "/")
	if !ok || session == "" || window == "" {
		return Pane{}, fmt.Errorf("pane choice needs session/window")
	}
	session, e := url.PathUnescape(session)
	if e != nil {
		return Pane{}, e
	}
	window, e = url.PathUnescape(window)
	if e != nil {
		return Pane{}, e
	}
	values, e := url.ParseQuery(query)
	if e != nil {
		return Pane{}, e
	}
	return Pane{Session: session, Window: window, Harness: values.Get("harness"), Role: "implementer"}, nil
}
