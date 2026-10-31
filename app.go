package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

const version = "0.1.0-dev"

type App struct {
	dir         string
	store       *Store
	r           *ochazuke.Runtime
	actor       Actor
	in          io.Reader
	out, errOut io.Writer
	run         func(context.Context, []string) ([]byte, error)
	changed     func()
	showUI      func(View) error
	rowsLoaded  func([]ocli.Row, error)
	reader      *liveReader
}
type verbSpec struct {
	noun, name, description string
	params                  map[string]ochazuke.Parameter
	positional              []string
	read                    bool
}

func param(desc string) ochazuke.Parameter {
	return ochazuke.Parameter{Type: "string", Description: desc}
}
func required(desc string) ochazuke.Parameter { p := param(desc); p.Required = true; return p }
func newApp(dir string, in io.Reader, out, errOut io.Writer) (*App, error) {
	actor, e := cliActor(os.Getenv)
	if e != nil {
		return nil, e
	}
	a := &App{dir: dir, store: &Store{Dir: dir, Actor: actor}, actor: actor, r: ochazuke.NewRuntime(), in: in, out: out, errOut: errOut}
	a.run = func(ctx context.Context, argv []string) ([]byte, error) {
		if len(argv) == 0 {
			return nil, fmt.Errorf("empty command")
		}
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.WaitDelay = 500 * time.Millisecond
		if writer, ok := ctx.Value(commandOutputKey{}).(io.Writer); ok {
			cmd.Stdout = writer
			return nil, cmd.Run()
		}
		return cmd.Output()
	}
	a.store.Warn = func(message string) { fmt.Fprintln(errOut, message) }
	a.r.Config().App = "o-lane"
	if e = a.r.DeclareSetting(appSettings()...); e != nil {
		return nil, e
	}
	a.declareProps()
	for _, spec := range a.specs() {
		v := spec
		if v.name == "set" {
			continue
		}
		right := ochazuke.AgentAllow
		if never[v.name] {
			right = ochazuke.AgentNever
		}
		b := a.r.DefineOwn(v.noun).Verb(v.name, func(c ochazuke.Ctx) ochazuke.Result { return a.handle(v.noun, v.name, c) }, v.description).Agent(right)
		if v.read {
			b.Fetch()
		}
		if v.name == "delete" {
			b.Destructive()
		}
		if len(v.positional) > 0 {
			b.Positional(v.positional...)
		}
		op := a.r.Operations[v.noun+"."+v.name]
		op.Parameters = v.params
		if !v.read {
			op.HistoryKind = ochazuke.HistoryChange
		}
		a.r.Operations[op.Key()] = op
	}
	if e = a.registerPage(); e != nil {
		return nil, e
	}
	return a, nil
}
func (a *App) shell() *ocli.Shell {
	sh := ocli.New(a.r, "o-lane", a.in, a.out, a.errOut)
	sh.Version = version
	sh.Actor = &a.actor
	sh.Prepare = func(context.Context) error { return a.configure() }
	sh.Reveal = func(target string) error {
		q, e := a.store.Resolve(target)
		if e != nil {
			return e
		}
		l := a.r.List("lane-targets")
		if l == nil {
			l, e = a.r.AddList("lane-targets", nil)
			if e != nil {
				return e
			}
		}
		return l.SetItems([]ochazuke.ListItem{{ID: target, Noun: "lane", Label: q.Name, Data: q}})
	}
	return sh
}
func (a *App) configure() error {
	a.r.EnsureConfigSettings()
	cfg := a.r.Settings()
	states, e := parseStates(cfg.String("lanes.states"))
	if e != nil {
		return e
	}
	a.store.States = states
	a.refreshOpenFilter()
	var presets map[string]string
	if e = json.Unmarshal([]byte(cfg.String("lanes.state-presets")), &presets); e != nil {
		return fmt.Errorf("lanes.state-presets: %w", e)
	}
	if presets == nil {
		presets = map[string]string{}
	}
	a.store.StatePresets = presets
	a.store.BackupDaily = cfg.Bool("backup.daily")
	a.store.BackupKeep = cfg.Int("backup.keep")
	return nil
}
func (a *App) specs() []verbSpec {
	target := param("Lane ID, unique prefix or name")
	common := func(extra map[string]ochazuke.Parameter) map[string]ochazuke.Parameter {
		m := map[string]ochazuke.Parameter{"target": target}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	fields := map[string]ochazuke.Parameter{"name": required("Lane name"), "parent": param("Parent lane; empty is a root"), "wording": param("Owner's verbatim words")}
	for _, p := range editable {
		fields[p] = param("Lane " + p)
	}
	fields["depends"] = ochazuke.Parameter{Type: "array", Description: "Dependencies, including external events"}
	fields["project"] = projectParam(projectHelp)
	projectFilter := param("Only lanes whose effective project matches this reference")
	rows := []verbSpec{
		{"lane", "add", "Create one lane; no parent creates a root", fields, []string{"name"}, false},
		{"lane", "define", "Atomically define JSON specs or a plan Lanes table; exact parent/name updates", defineParams(), nil, false},
		{"plan", "define", "Define a plan root and its lanes; same handler as lane.define --plan", planDefineParams(), []string{"plan"}, false},
		{"plan", "lanes", "List recorded lanes linked to an exact plan path", map[string]ochazuke.Parameter{"plan": required("Plan path or exact filename in lanes.plans-dir")}, []string{"plan"}, true},
		{"lane", "list", "List recorded lanes", map[string]ochazuke.Parameter{"parent": param("Filter by parent"), "deleted": {Type: "boolean", Description: "Include deleted lanes and their audit history"}, "project": projectFilter, "state": stateParam("")}, nil, true},
		{"lane", "tree", "List lanes in parent-first order", map[string]ochazuke.Parameter{"open": {Type: "boolean", Description: "Hide closed roots"}, "project": param("Only subtrees containing lanes of this project"), "state": stateParam("ancestors of matches stay as context")}, nil, true},
		{"lane", "show", "Show a lane and derived lifecycle timestamps", common(nil), []string{"target"}, true},
		{"lane", "history", "Show a lane's actor-attributed history", common(nil), []string{"target"}, true},
		{"lane", "set", "Set one spec property", common(map[string]ochazuke.Parameter{"prop": required("Property"), "value": required("New value; JSON array for depends")}), []string{"target", "prop", "value"}, false},
		{"lane", "rename", "Change lane name", common(map[string]ochazuke.Parameter{"name": required("New name")}), []string{"target", "name"}, false},
		{"lane", "reword", "Change the owner's wording, retaining undo", common(map[string]ochazuke.Parameter{"text": required("Verbatim wording")}), []string{"target", "text"}, false},
		{"lane", "move", "Change parent; refuses cycles", common(map[string]ochazuke.Parameter{"parent": param("New parent; empty makes root")}), []string{"target", "parent"}, false},
		{"lane", "copy", "Copy spec into a planned retry linked to the original", common(map[string]ochazuke.Parameter{"name": param("New name")}), []string{"target", "name"}, false},
		{"lane", "start", "Start a lane, optionally recording a pane or pane-less agent", common(map[string]ochazuke.Parameter{"pane": param("Window ID"), "host": param("Pane host"), "session": param("Pane session"), "role": param("Participant role"), "harness": param("Harness"), "model": param("Model"), "agent": param("Agent kind"), "branch": param("Branch"), "worktree": param("Worktree path")}), []string{"target"}, false},
		{"lane", "state", "Record any configured state; from-tag uses lanes.tag-states", common(map[string]ochazuke.Parameter{"state": param("Lifecycle state"), "note": param("History note"), "from-tag": param("Watcher tag; unmapped tags append notes")}), []string{"target", "state"}, false},
		{"lane", "note", "Append a history note", common(map[string]ochazuke.Parameter{"text": required("Note")}), []string{"target", "text"}, false},
		{"lane", "live", "Read each pane and the aggregate live state of the lane subtree", common(nil), []string{"target"}, true},
		{"lane", "focus-pane", "Focus an associated pane using the configured argv", common(map[string]ochazuke.Parameter{"role": param("Optional role filter")}), []string{"target"}, true},
		{"lane", "yank-id", "Copy the lane ID to the framework register", common(nil), []string{"target"}, true},
		{"lane", "yank-path", "Copy the worktree path to the framework register", common(nil), []string{"target"}, true},
		{"lane", "delete", "Hide a lane and subtree; undo restores them", common(nil), []string{"target"}, false},
		{"lane", "undo", "Restore the latest change; agents can undo their own latest change", nil, nil, false},
	}
	for _, v := range []struct{ name, desc string }{{"park", "Park"}, {"review", "Request review"}, {"merge", "Record merge and optional commit"}, {"finish", "Mark done"}, {"cancel", "Cancel"}, {"reopen", "Reopen as planned"}} {
		rows = append(rows, verbSpec{"lane", v.name, v.desc, common(map[string]ochazuke.Parameter{"note": param("History note"), "commit": param("Merge commit")}), []string{"target"}, false})
	}
	for _, verb := range []string{"link", "unlink"} {
		rows = append(rows, verbSpec{"lane", verb, "Add or remove a typed link", common(map[string]ochazuke.Parameter{"kind": required("Link kind"), "link": required("Link target"), "label": param("Label")}), []string{"target", "kind", "link"}, false})
	}
	for _, noun := range []string{"lane", "pane", "project"} {
		for _, verb := range []string{"associate", "unassociate"} {
			m := common(map[string]ochazuke.Parameter{"lane": param("Lane ID for pane-side relation"), "pane": param("Window ID"), "host": param("Pane host"), "session": param("Pane session"), "role": param("Participant role; default implementer"), "harness": param("Harness"), "model": param("Model"), "agent": param("Agent kind")})
			pos := []string{"target", "pane"}
			desc := "Associate or unassociate a lane and pane"
			switch noun {
			case "lane":
				m["project"] = projectParam("Project reference to associate (or, for unassociate, to remove); a root's project covers its subtree")
				desc = "Associate or unassociate a lane with a pane or project"
			case "pane":
				pos = []string{"pane", "lane"}
			case "project":
				m = map[string]ochazuke.Parameter{"project": projectParam("Project reference"), "lane": required("Lane ID, unique prefix or name")}
				pos = []string{"project", "lane"}
				desc = "Associate or unassociate a project and lane; same handler as lane." + verb + " --project"
			}
			rows = append(rows, verbSpec{noun, verb, desc, m, pos, false})
		}
	}
	rows = append(rows, verbSpec{"project", "lanes", "List lanes whose effective project matches the reference", map[string]ochazuke.Parameter{"project": projectParam("Project reference"), "state": stateParam("")}, []string{"project"}, true})
	rows = append(rows, verbSpec{"project", "list", "List project references recorded on lanes, plus lanes.project-source projects", nil, nil, true})
	rows = append(rows, verbSpec{"pane", "lanes", "List lanes associated with a pane", map[string]ochazuke.Parameter{"pane": param("Window ID"), "host": param("Host"), "session": param("Session")}, []string{"pane"}, true})
	return rows
}
func done(v any, e error) ochazuke.Result {
	if e != nil {
		return ochazuke.Failed(e)
	}
	return ochazuke.Done(v)
}
func decode(v any, out any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, out)
}
func (a *App) handle(noun, verb string, c ochazuke.Ctx) ochazuke.Result {
	if a.changed != nil {
		defer a.changed()
	}
	if e := a.configure(); e != nil {
		return ochazuke.Failed(e)
	}
	a.bindActor(c)
	s := a.store
	str := func(k string) string { return ochazuke.StringArg(c.Args, k, "") }
	target := str("target")
	if target == "" && c.Instance != nil {
		target = c.Instance.ID
		if id, ok := c.Instance.Data.(string); ok {
			target = id
		}
		target = strings.TrimPrefix(target, "lane/")
	}
	p := Pane{Host: str("host"), Session: str("session"), Window: str("pane"), Role: str("role"), Harness: str("harness"), Model: str("model"), Agent: str("agent")}
	if noun == "lane" && verb == "choose-state" {
		verb = "state"
	}
	if noun == "lane" && verb == "choose-pane" {
		// Choice IDs carry the full relation, so equal window labels in
		// different sessions never lose their session at a mounted host.
		var e error
		p, e = paneChoice(p.Window)
		if e != nil {
			return done(nil, fmt.Errorf("pane choice: %w", e))
		}
		if role := str("role"); role != "" {
			p.Role = role
		}
		verb = "associate"
	}
	if p.Session == "" && p.Window != "" {
		p.Session = s.Actor.Session
	}
	if noun == "pane" {
		if verb == "lanes" {
			rows, e := s.List()
			if e != nil {
				return done(nil, e)
			}
			out := []View{}
			for _, q := range rows {
				for _, old := range q.Panes {
					if paneMatches(old, p) {
						out = append(out, s.view(q, rows))
						break
					}
				}
			}
			return done(out, nil)
		}
		target = str("lane")
		if p.Window == "" {
			p.Window = str("target")
		}
	}
	project := str("project")
	// --state filters only the listing verbs; lane.state's own state
	// argument is the state to record.
	var states []string
	if (noun == "project" && verb == "lanes") || (noun == "lane" && (verb == "list" || verb == "tree")) {
		states = stateList(c.Args["state"])
		if e := s.checkStates(states); e != nil {
			return done(nil, e)
		}
	}
	inStates := func(q Lane) bool { return len(states) == 0 || slices.Contains(states, q.State) }
	if noun == "project" {
		switch verb {
		case "lanes":
			rows, e := s.List()
			if e != nil {
				return done(nil, e)
			}
			out := []View{}
			for _, q := range rows {
				if v := s.view(q, rows); projectMatches(v.EffectiveProject, project) && inStates(q) {
					out = append(out, v)
				}
			}
			e = a.enrichLive(out)
			return done(out, e)
		case "list":
			out, e := a.projectRows(context.Background())
			return done(out, e)
		}
		target = str("lane")
		if project == "" {
			project = str("target")
		}
		if project == "" {
			return done(nil, fmt.Errorf("project.%s needs a project reference", verb))
		}
		q, e := s.AssociateProject(target, project, verb == "unassociate")
		return done(q, e)
	}
	if noun == "plan" && verb == "lanes" {
		return a.planLanes(str("plan"))
	}
	switch verb {
	case "add":
		var spec Spec
		if e := decode(c.Args, &spec); e != nil {
			return done(nil, e)
		}
		q, e := s.Add(spec)
		return done(q, e)
	case "define":
		return a.define(c.Args)
	case "list", "tree":
		var rows []Lane
		var e error
		includeDeleted, _ := c.Args["deleted"].(bool)
		if verb == "list" && includeDeleted {
			rows, e = s.ListAll()
		} else {
			rows, e = s.List()
		}
		if e != nil {
			return done(nil, e)
		}
		out := []View{}
		kept := stateKept(rows, states)
		inProject := func(q Lane) bool {
			return (project == "" || subtreeProject(rows, q, project)) && (kept == nil || kept[q.ID])
		}
		var walk func(string, bool)
		walk = func(parent string, hide bool) {
			for _, q := range rows {
				if q.Parent != parent || !inProject(q) {
					continue
				}
				if q.Parent == "" && hide {
					st, ok := s.states()[s.view(q, rows).RolledState]
					if ok && !st.Open {
						continue
					}
				}
				out = append(out, s.view(q, rows))
				walk(q.ID, false)
			}
		}
		if verb == "tree" {
			open, _ := c.Args["open"].(bool)
			walk("", open || a.r.Settings().Bool("lanes.hide-closed-roots"))
		} else {
			parent := str("parent")
			if parent != "" {
				q, e := resolve(rows, parent)
				if e != nil {
					return done(nil, e)
				}
				parent = q.ID
			}
			for _, q := range rows {
				v := s.view(q, rows)
				if (parent == "" || q.Parent == parent) && (project == "" || projectMatches(v.EffectiveProject, project)) && inStates(q) {
					out = append(out, v)
				}
			}
		}
		e = a.enrichLive(out)
		return done(out, e)
	case "show", "history", "live", "focus-pane", "yank-id", "yank-path":
		q, e := s.Resolve(target)
		if e != nil {
			return done(nil, e)
		}
		switch verb {
		case "history":
			return done(q.History, nil)
		case "live":
			return a.liveResult(c, q)
		case "focus-pane":
			return a.focusPane(c, q, str("role"))
		case "yank-id", "yank-path":
			text := q.ID
			if verb == "yank-path" {
				text = q.Worktree
			}
			e := a.r.StoreYank(ochazuke.Yank{Text: text, Shape: "char"}, "\"", false)
			return done(text, e)
		}
		rows, e := s.List()
		if e == nil && a.showUI != nil && a.r.Settings().Bool("lanes.detail") {
			if e = a.showUI(s.view(q, rows)); e != nil {
				return done(nil, e)
			}
		}
		v := s.view(q, rows)
		if e == nil && a.showUI == nil {
			views := []View{v}
			e = a.enrichLive(views)
			v = views[0]
		}
		return done(v, e)
	case "set":
		q, e := s.Set(target, str("prop"), str("value"))
		return done(q, e)
	case "rename":
		q, e := s.Rename(target, str("name"))
		return done(q, e)
	case "reword":
		q, e := s.Reword(target, str("text"))
		return done(q, e)
	case "move":
		q, e := s.Move(target, str("parent"))
		return done(q, e)
	case "copy":
		q, e := s.Copy(target, str("name"))
		return done(q, e)
	case "note":
		q, e := s.Note(target, str("text"))
		return done(q, e)
	case "state":
		state := str("state")
		note := str("note")
		if tag := str("from-tag"); tag != "" {
			table := map[string]string{}
			if e := json.Unmarshal([]byte(a.r.Settings().String("lanes.tag-states")), &table); e != nil {
				return done(nil, e)
			}
			state = table[tag]
			note = strings.TrimSpace(tag + " " + note)
			if state == "" {
				q, e := s.Note(target, note)
				return done(q, e)
			}
		}
		q, e := s.State(target, state, note)
		return done(q, e)
	case "start":
		if p.Role != "" && !slices.Contains(a.r.Settings().Strings("lanes.pane-roles"), p.Role) {
			return done(nil, fmt.Errorf("unknown role %q; configure lanes.pane-roles", p.Role))
		}
		q, e := s.Start(target, p, str("branch"), str("worktree"))
		return done(q, e)
	case "associate", "unassociate":
		if p.Role != "" && !slices.Contains(a.r.Settings().Strings("lanes.pane-roles"), p.Role) {
			return done(nil, fmt.Errorf("unknown role %q; configure lanes.pane-roles", p.Role))
		}
		remove := verb == "unassociate"
		if project != "" && p.Window == "" && p.Host == "" && p.Harness == "" && p.Model == "" && p.Agent == "" {
			q, e := s.AssociateProject(target, project, remove)
			return done(q, e)
		}
		if project != "" {
			// Both relations in one call share one snapshot and one undo.
			q, e := s.mutate(verb, target, func(q *Lane, _ *[]Lane) error {
				if e := s.project(q, project, remove); e != nil {
					return e
				}
				s.associate(q, p, remove)
				return nil
			})
			return done(q, e)
		}
		q, e := s.Associate(target, p, remove)
		return done(q, e)
	case "link", "unlink":
		link := Link{Kind: str("kind"), Target: str("link"), Label: str("label")}
		if !slices.Contains(a.r.Settings().Strings("lanes.link-kinds"), link.Kind) {
			return done(nil, fmt.Errorf("unknown link kind %q; configure lanes.link-kinds", link.Kind))
		}
		q, e := s.Link(target, link, verb == "unlink")
		return done(q, e)
	case "delete":
		q, e := s.Delete(target)
		return done(q, e)
	case "undo":
		e := s.Undo()
		return done("undone", e)
	case "park", "review", "merge", "finish", "cancel", "reopen":
		if verb == "merge" {
			q, e := s.Merge(target, str("commit"), str("note"))
			return done(q, e)
		}
		q, e := s.Preset(target, verb, str("note"))
		return done(q, e)
	}

	return done(nil, fmt.Errorf("unknown verb %s.%s", noun, verb))
}

func (a *App) bindActor(c ochazuke.Ctx) {

	s := a.store
	s.Actor = c.Actor()
	// Store direct callers use the same defaults. App calls honour owner rights
	// overrides using the one framework policy source, never a second allowlist.
	s.Permit = func(action string) error {
		op, ok := a.r.Operations["lane."+action]
		if !ok {
			return fmt.Errorf("unknown action %s", action)
		}
		r := a.r.AgentRight(op, false)
		if r.Right != ochazuke.AgentAllow {
			return &ochazuke.RefusedError{Verb: op.Key(), Rule: r.Rule, Reason: "agent right is " + r.Right}
		}
		return nil
	}
}
