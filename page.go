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
	target.From = ochazuke.SourceRow + ":id"
	// choose-pane is its own verb: its pane argument is a pane choice ID, not a window label.
	a.r.DefineOwn("lane").Verb("choose-pane", a.handler(verbSpec{name: "choose-pane", run: (*App).runLaneChoosePane}), "Choose a tuios window to associate").Agent(ochazuke.AgentAllow).Positional("target", "pane")
	op := a.r.Operations.At("lane.choose-pane")
	op.Parameters = map[string]ochazuke.Parameter{"target": target, "pane": {Type: "string", Required: true, Free: true, Source: "pane.choices", Description: "Choose a tuios window to associate"}, // A window closing after the pick never loses the relation.
		"role": {Type: "string", Source: "pane.roles", Description: "Participant role; default implementer"}}
	a.r.Operations.Set(op)
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
	rows := func(listHides bool) func(c ochazuke.Ctx) ochazuke.Result {
		return func(c ochazuke.Ctx) ochazuke.Result {
			if e := a.configure(); e != nil {
				return done(nil, e)
			}
			s, o := *a.store, a.pageOptions()
			if project := ochazuke.StringArg(c.Args, "project", ""); project != "" {
				o.project = project
			}
			if statuses := statusList(c.Args["status"]); len(statuses) > 0 {
				o.statuses = statuses
			}
			if e := a.store.checkStatuses(o.statuses); e != nil {
				return done(nil, e)
			}
			o.listHides = listHides
			if !listHides {
				o.listHides, _ = c.Args["archived"].(bool)
			}
			reader, run := a.reader, commandRunner(a.run)
			return ochazuke.Pending(func(ctx context.Context) (any, error) { return loadLaneRows(ctx, &s, o, reader, run) }).Replacing("lane-rows").Then(func(_ ochazuke.Ctx, value any, e error) {
				if a.rowsLoaded != nil {
					rows, _ := value.([]ocli.Row)
					a.rowsLoaded(rows, e)
				}
			})
		}
	}
	a.r.DefineOwn("lane").Verb("rows", rows(false), "List portable tree page rows").Rows("lane").Fetch().Quiet().Agent(ochazuke.AgentAllow)
	a.r.DefineOwn("lane").Verb(pageRowsVerb, rows(true), "List the Lanes page's rows: archived lanes kept and flagged for the list to hide or show (same handler and parameters as lane.rows, without archived)").Rows("lane").Fetch().Quiet().Agent(ochazuke.AgentAllow)
	params := map[string]ochazuke.Parameter{"project": param("Only subtrees containing lanes of this project; default lanes.project-filter"), "status": statusParam("default lanes.status-filter")}
	pageOp := a.r.Operations.At(ochazuke.Key("lane." + pageRowsVerb))
	pageOp.Parameters = params
	a.r.Operations.Set(pageOp)
	rowsOp := a.r.Operations.At("lane.rows")
	rowsOp.Parameters = map[string]ochazuke.Parameter{"project": params["project"], "status": params["status"], "archived": {Type: ochazuke.SettingBoolean, Description: "Keep archived lanes in the rows, flagged by the archived prop, for a list that hides or shows them itself (the Lanes page); default false leaves them out unless a status names an archive status"}}
	a.r.Operations.Set(rowsOp)
	a.r.Define("lane").Columns(a.pageOptions().columns()...)
	b := a.r.Define("lane").Mode("normal")
	for _, row := range []struct{ key, verb string }{{"enter", "show"}, {"s", "choose-status"}, {"g p", "focus-pane"}, {"a", "choose-pane"}, {"n", "annotate"}, {"y", "yank-id"}} {
		b.Bind(row.key, ochazuke.Do("lane."+row.verb))
	}
	detail := a.r.Define("lane-detail").Pin("normal").Mode("normal")
	for _, row := range []struct {
		key   string
		lines int
	}{{"j", 1}, {"k", -1}} {
		detail.Bind(row.key, ochazuke.Do("ochazuke.detail.scroll", ochazuke.Arguments{"id": "lane-detail/text", "lines": row.lines}))
	}
	return ocli.DeclarePage(a.r, lanePage(a.pageOptions()))
}

// pageRowsVerb is the Lanes page's row source: lane.rows that always leaves
// archived lanes to the list. A mounted page fetches its source with no
// arguments, so the flag cannot ride on lane.rows.
const pageRowsVerb = "page-rows"

type pageOptions struct {
	tree, guides, durations, panes, history, tones, detail, hideClosed bool
	now                                                                time.Time
	live                                                               liveOptions
	liveTones, rollup                                                  bool
	// toneScope is row (every cell draws in the lifecycle tone) or cell (the status cell only).
	toneScope                                           string
	liveRows                                            map[string]Live
	staleLiveRows                                       map[string]Live
	staleFlag                                           bool
	groupProjects, projectColumn, nameTone, statusAbout bool
	project                                             string
	hideArchived                                        bool
	// listHides leaves archived lanes to the list: the rows carry the archived
	// prop and collection.hide-archived and the archived filter decide what shows.
	// False is the portable feed (lane rows from the CLI or MCP), which drops
	// them itself unless the caller names an archive status.
	listHides    bool
	statuses     []string
	docLinkKinds []string
}

func (a *App) pageOptions() pageOptions {
	cfg := a.r.Settings()
	live, _ := a.liveOptions()
	return pageOptions{docLinkKinds: cfg.Strings("lanes.doc-link-kinds"), live: live, staleFlag: cfg.Bool("lanes.stale-flag"), liveTones: cfg.Bool("lanes.live-tones"), rollup: cfg.Bool("lanes.rollup-counts"), tree: cfg.Bool("lanes.tree"), guides: true, durations: cfg.Bool("lanes.durations"), panes: cfg.Bool("lanes.pane-column"), history: cfg.Bool("lanes.history-in-detail"), tones: cfg.Bool("lanes.status-tones"), detail: cfg.Bool("lanes.detail"), hideClosed: cfg.Bool("lanes.hide-closed-roots"), now: a.store.now(), groupProjects: cfg.Bool("lanes.group-by-project"), projectColumn: cfg.Bool("lanes.project-column"), nameTone: cfg.Bool("lanes.name-tone"), toneScope: cfg.String("lanes.status-tone-scope"), statusAbout: cfg.Bool("lanes.status-descriptions"), project: cfg.String("lanes.project-filter"), statuses: cfg.Strings("lanes.status-filter"), hideArchived: cfg.Bool("collection.hide-archived"), listHides: true}
}
func (o pageOptions) columns() []ochazuke.Column {
	cols := []ochazuke.Column{{Title: "Name", Min: 16, Pinned: true}, {Title: "Status", Max: 34, Priority: 1}}
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

// rollupText is the " · done/total" suffix of a status cell, empty when the
// subtree is complete or the lanes.rollup-counts setting is off.
func (o pageOptions) rollupText(done, total int) string {
	if !o.rollup || total <= done {
		return ""
	}
	return fmt.Sprintf(" · %d/%d", done, total)
}
func lanePage(o pageOptions) ocli.CatalogPage {
	p := ocli.CatalogPage{ID: pageID, Title: "Lanes", Layout: ocli.LayoutStack, Lists: []ocli.CatalogList{{ID: listID, Noun: "lane", Source: "lane." + pageRowsVerb, Title: "Lanes", Tree: o.tree, Guides: o.guides}}}
	if !o.detail {
		return p
	}
	fields := []ocli.CatalogDetailField{}
	for _, name := range []string{"id", "name", "parent", "status", "rolled_status", "live_status", "project", "effective_project", "project_association", "repo", "base", "branch", "worktree", "estimate", "depends", "plan", "task", "tests", "links", "created", "updated", "started", "finished", "panes"} {
		label := strings.ReplaceAll(name, "_", " ")
		if name == "live_status" {
			// Live status is the panes' and agents' status, not the lane's.
			label = "agent status"
		}
		fields = append(fields, ocli.CatalogDetailField{Label: label, Field: name})
	}
	p.Detail = &ocli.CatalogDetail{Fields: []string{"name", "status", "effective_project", "wording", "scope", "done_when", "repo", "branch", "worktree", "estimate", "depends", "links", "panes"}, Sections: []ocli.CatalogDetailSection{
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
	if o.statusAbout {
		// Untitled, so a status without a description draws nothing at all.
		p.Detail.Sections = append([]ocli.CatalogDetailSection{{Kind: "markdown", Field: "status_about"}}, p.Detail.Sections...)
		p.Detail.Fields = append(p.Detail.Fields, "status_about")
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
	kept := statusKept(lanes, o.statuses)
	var hidden map[string]bool
	if !o.listHides {
		hidden = s.archiveHidden(lanes, o.statuses, o.hideArchived)
	}
	// A status filter naming an archive status asks for those lanes: the rows
	// then do not flag them, so the list's own archive hiding leaves them be.
	asked := slices.ContainsFunc(o.statuses, func(name string) bool { return s.statuses()[name].Archive })
	rolls := s.rollups(lanes)
	var walk func(string)
	walk = func(parent string) {
		for _, q := range lanes {
			if q.Parent != parent {
				continue
			}
			if o.project != "" && !subtreeProject(lanes, q, o.project) {
				continue
			}
			if (kept != nil && !kept[q.ID]) || hidden[q.ID] {
				continue
			}
			v := s.view(q, lanes)
			if parent == "" && o.hideClosed && !s.statuses()[q.Status].Archive && !s.statuses()[v.RolledStatus].Open {
				continue
			}
			status := v.RolledStatus
			// A lane with unfinished work below it shows how much is finished.
			// Complete is the ends role in lanes.statuses, never a status name.
			rollDone, rollTotal := rolls[q.ID][0], rolls[q.ID][1]
			status += o.rollupText(rollDone, rollTotal)
			cells := []string{q.Name, status}
			if o.projectColumn {
				project := v.EffectiveProject
				if project == "" {
					project = "unassociated"
				}
				cells = append(cells, project)
			}
			cells = append(cells, q.Repo, q.Estimate)
			live, hasLive := o.liveRows[q.ID]
			showLive := hasLive && o.live.on && s.statuses()[q.Status].Open
			if showLive {
				cells[1] += " · " + live.Status
				if live.Glyph != "" {
					cells[0] = live.Glyph + " " + q.Name
				}
			}
			stale, staleReason := false, ""
			if activity, ok := o.staleLiveRows[q.ID]; ok && o.live.on {
				stale, staleReason = staleLane(q, s.statuses(), activity, o.staleFlag, o.now)
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
			pane := ""
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
							if pane == "" && p.Window != "" && lp.working {
								pane = p.Window
							}
							liveText = " · agent " + lp.Status
							if lp.AgentLive != nil {
								// An agent's own status and last-activity age sit
								// beside it in the Agents column.
								agentLive = " · " + lp.Status
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
			doc := ""
			for i := len(q.Links) - 1; i >= 0; i-- {
				if slices.Contains(o.docLinkKinds, q.Links[i].Kind) {
					doc = q.Links[i].Target
					break
				}
			}
			data["doc"], data["pane"] = doc, pane
			if hasLive && o.live.on {
				data["live"] = live
				data["live_status"] = live.Status
			}
			data["stale"], data["stale_reason"] = stale, staleReason
			data["children_done"], data["children_total"] = rollDone, rollTotal
			laneFilterValues(data, q, s.statuses()[q.Status].Archive && !asked)
			if about := statusAbout(q.Status, v.StatusDescription, v.StatusReason); o.statusAbout && about != "" {
				data["status_about"] = about
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
			// The name takes whichever tone draws the status cell: the live tone
			// when it is shown, else the lifecycle tone. Both come from data.
			nameTone := ""
			if o.tones {
				nameTone = s.statuses()[v.RolledStatus].Tone
			}
			if showLive && o.liveTones && live.Tone != "" {
				nameTone = live.Tone
			}
			row.Tones = append(row.Tones, rowBaseTones(o, s.statuses()[v.RolledStatus].Tone, len(cells))...)
			if o.tones {
				span := ochazuke.CellTone(1, s.statuses()[v.RolledStatus].Tone)
				if showLive {
					span.To = len([]rune(status))
				}
				row.Tones = append(row.Tones, span)
			}
			if showLive && o.liveTones && live.Tone != "" {
				row.Tones = append(row.Tones, ochazuke.SpanTone(1, len([]rune(status))+3, 0, live.Tone))
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
	if noServer(e) {
		// No tuios server is running: there are no windows to choose, which
		// is an answer and not a failure of the job.
		if a.errOut != nil {
			fmt.Fprintln(a.errOut, "o-lane: no tuios server is running, so there are no panes to choose; start tuios (or run inside a tuios session) and try again")
		}
		return []ochazuke.Choice{}, nil
	}
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
		if noServer(e) {
			break
		}
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

// rowBaseTones are the tones laid first on a row so the spans after them win:
// every cell in the status tone when lanes.status-tone-scope says the scope
// covers the row (toneScopes data), none otherwise. The name cell follows
// lanes.name-tone like it does for the status-cell spans.
func rowBaseTones(o pageOptions, tone string, cells int) []ochazuke.ToneSpan {
	if !o.tones || tone == "" || !toneScopes[o.toneScope] {
		return nil
	}
	var out []ochazuke.ToneSpan
	for i := 0; i < cells; i++ {
		if i > 0 || o.nameTone {
			out = append(out, ochazuke.CellTone(i, tone))
		}
	}
	return out
}
