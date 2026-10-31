package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
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
	a.r.DefineOwn("pane").Verb("roles", func(c ochazuke.Ctx) ochazuke.Result {
		return done(a.r.Settings().Strings("lanes.pane-roles"), nil)
	}, "List configured participant roles").Fetch().Quiet().Agent(ochazuke.AgentAllow)
	a.r.DefineOwn("lane").Verb("rows", func(c ochazuke.Ctx) ochazuke.Result {
		if e := a.configure(); e != nil {
			return done(nil, e)
		}
		s, o := *a.store, a.pageOptions()
		return ochazuke.Pending(func(context.Context) (any, error) { return laneRows(&s, o) }).Replacing("lane-rows").Then(func(_ ochazuke.Ctx, value any, e error) {
			if a.rowsLoaded != nil {
				rows, _ := value.([]ocli.Row)
				a.rowsLoaded(rows, e)
			}
		})
	}, "List portable tree page rows").Rows("lane").Fetch().Quiet().Agent(ochazuke.AgentAllow)
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
}

func (a *App) pageOptions() pageOptions {
	cfg := a.r.Settings()
	return pageOptions{tree: cfg.Bool("lanes.tree"), guides: true, durations: cfg.Bool("lanes.durations"), panes: cfg.Bool("lanes.pane-column"), history: cfg.Bool("lanes.history-in-detail"), tones: cfg.Bool("lanes.state-tones"), detail: cfg.Bool("lanes.detail"), hideClosed: cfg.Bool("lanes.hide-closed-roots"), now: a.store.now()}
}
func (o pageOptions) columns() []ochazuke.Column {
	cols := []ochazuke.Column{{Title: "Name", Min: 16, Pinned: true}, {Title: "State", Width: 11, Priority: 1}, {Title: "Repo", Max: 24, Priority: 4}, {Title: "Estimate", Width: 9, Priority: 5}}
	if o.durations {
		cols = append(cols, ochazuke.Column{Title: "Elapsed / duration", Width: 18, Priority: 3})
	}
	if o.panes {
		cols = append(cols, ochazuke.Column{Title: "Panes", Max: 28, Priority: 2})
	}
	return append(cols, ochazuke.Column{Title: "Agents", Max: 32, Priority: 6})
}
func lanePage(o pageOptions) ocli.CatalogPage {
	p := ocli.CatalogPage{ID: pageID, Title: "Lanes", Layout: ocli.LayoutStack, Lists: []ocli.CatalogList{{ID: listID, Noun: "lane", Source: "lane.rows", Title: "Lanes", Tree: o.tree, Guides: o.guides}}}
	if !o.detail {
		return p
	}
	fields := []ocli.CatalogDetailField{}
	for _, name := range []string{"id", "name", "parent", "state", "rolled_state", "repo", "base", "branch", "worktree", "estimate", "depends", "plan", "task", "tests", "links", "created", "updated", "started", "finished", "panes"} {
		fields = append(fields, ocli.CatalogDetailField{Label: strings.ReplaceAll(name, "_", " "), Field: name})
	}
	p.Detail = &ocli.CatalogDetail{Fields: []string{"name", "state", "wording", "scope", "done_when", "repo", "branch", "worktree", "estimate", "depends", "links", "panes"}, Sections: []ocli.CatalogDetailSection{
		{Kind: "markdown", Title: "Owner wording", Field: "wording"},
		{Kind: "markdown", Title: "Scope", Field: "scope"},
		{Kind: "markdown", Title: "Done when", Field: "done_when"},
		{Kind: "markdown", Title: "Panes", Field: "pane_detail"},
		{Kind: "fields", Title: "Fields", Fields: fields},
	}}
	if o.history {
		p.Detail.Sections = append(p.Detail.Sections, ocli.CatalogDetailSection{Kind: "thread", Title: "History", Field: "history", Author: "author", At: "at", Body: "body"})
		p.Detail.Fields = append(p.Detail.Fields, "history")
	}
	return p
}

// The page is a projection only: durations, root rollup and presentation never
// write derived values back into the store. A single read supplies the tree.
func laneRows(s *Store, o pageOptions) ([]ocli.Row, error) {
	lanes, e := s.List()
	if e != nil {
		return nil, e
	}
	sort.SliceStable(lanes, func(i, j int) bool {
		if lanes[i].Created.Equal(lanes[j].Created) {
			return lanes[i].ID > lanes[j].ID
		}
		return lanes[i].Created.After(lanes[j].Created)
	})
	out := []ocli.Row{}
	var walk func(string)
	walk = func(parent string) {
		for _, q := range lanes {
			if q.Parent != parent {
				continue
			}
			v := s.view(q, lanes)
			if parent == "" && o.hideClosed && !s.states()[v.RolledState].Open {
				continue
			}
			state := v.RolledState
			cells := []string{q.Name, state, q.Repo, q.Estimate}
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
				if p.Window != "" {
					roles[p.Role]++
				}
				agent := strings.TrimSpace(strings.Join([]string{p.Harness, p.Model, p.Agent}, " "))
				if agent != "" && !slices.Contains(agents, agent) {
					agents = append(agents, agent)
				}
				window := p.Window
				if window == "" {
					window = "unassociated"
				}
				paneDetail = append(paneDetail, fmt.Sprintf("- %s: %s · %s · session %s · since %s", p.Role, window, agent, p.Session, p.Since.Format(time.RFC3339)))
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
			if o.tones {
				row.Tones = []ochazuke.ToneSpan{{Cell: 1, Tone: s.states()[state].Tone}}
			}
			out = append(out, row)
			walk(q.ID)
		}
	}
	walk("")
	return out, e
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
	var sessions struct {
		Sessions []struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"sessions"`
	}
	if e = json.Unmarshal(b, &sessions); e != nil {
		return nil, e
	}
	out := []ochazuke.Choice{}
	for _, session := range sessions.Sessions {
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
		var windows struct {
			Windows []struct {
				ID      string `json:"window_id"`
				Name    string `json:"name"`
				Title   string `json:"title"`
				Harness string `json:"harness"`
			} `json:"windows"`
		}
		if e = json.Unmarshal(b, &windows); e != nil {
			return nil, e
		}
		for _, w := range windows.Windows {
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
