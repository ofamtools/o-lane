package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"sync"
	"time"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

const version = "0.1.0-dev"

type App struct {
	dir            string
	store          *Store
	r              *ochazuke.Runtime
	actor          Actor
	in             io.Reader
	out, errOut    io.Writer
	run            func(context.Context, []string) ([]byte, error)
	changed        func()
	showUI         func(View) error
	rowsLoaded     func([]ocli.Row, error)
	reader         *liveReader
	agentsOnce     sync.Once
	agents         *agentLog
	pmu            sync.Mutex
	pflight        chan struct{}
	pstarted       time.Time
	projects       []hostProject
	projectsAt     time.Time
	projectsFailAt time.Time
}
type verbSpec struct {
	noun, name, description string
	params                  map[string]ochazuke.Parameter
	positional              []string
	read                    bool
	agent                   string
	run                     func(*App, *call) ochazuke.Result
	aliases                 []string
	remove                  bool // the inverse of a pair: unassociate, unlink
}

func param(desc string) ochazuke.Parameter {
	return ochazuke.Parameter{Type: "string", Description: desc}
}
func required(desc string) ochazuke.Parameter { p := param(desc); p.Required = true; return p }

// Namespace is the singular namespace o-lane declares (lexicon plan): its
// operations are lane.<noun>.<verb>, and a host mounts its catalog under it.
const Namespace = "lane"

func init() {
	if err := ochazuke.DeclareNamespace(Namespace); err != nil {
		panic(err)
	}
}

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
			return nil, wrapExit(argv, cmd.Run())
		}
		out, err := cmd.Output()
		return out, wrapExit(argv, err)
	}
	a.store.Warn = func(message string) { fmt.Fprintln(errOut, message) }
	a.r.Config().App = "o-lane"
	if e = a.r.DeclareSetting(appSettings()...); e != nil {
		return nil, e
	}
	a.r.Define("lane").Main() // the tool's main noun: `o-lane add` is `lane add`
	a.declareProps()
	for _, spec := range a.specs() {
		v := spec
		if v.run == nil {
			continue // lane.set is the standard set verb over the lane props; the row only carries its rights default
		}
		right := v.agent
		if right == "" {
			right = ochazuke.AgentAllow
		}
		b := a.r.DefineOwn(v.noun).Verb(v.name, a.handler(v), v.description).Agent(right)
		b.Alias(v.aliases...)
		if v.read {
			b.Fetch()
		}
		if len(v.positional) > 0 {
			b.Positional(v.positional...)
		}
		op := a.r.Operations.At(ochazuke.Key(v.noun + "." + v.name))
		op.Parameters = v.params
		if t, ok := op.Parameters["target"]; ok && v.noun == "lane" {
			// A lane verb's target is the focused lane row when a key, the help or the palette runs it.
			t.From = ochazuke.SourceRow + ":id"
			op.Parameters = maps.Clone(op.Parameters)
			op.Parameters["target"] = t
		}
		if !v.read {
			op.HistoryKind = ochazuke.HistoryChange
		}
		a.r.Operations.Set(op)
	}
	a.r.DefineOwn("project").Props(projectProps()...).Store(a.projectStore())
	a.r.DefineOwn("agent").Props(agentProps()...).Store(a.agentStore())
	if e = a.registerPage(); e != nil {
		return nil, e
	}
	if e = a.declareSetup(); e != nil {
		return nil, e
	}
	if e = declarePickers(a.r); e != nil {
		return nil, e
	}
	return a, nil
}
func (a *App) shell() *ocli.Shell {
	sh := ocli.New(a.r, "o-lane", a.in, a.out, a.errOut, ocli.PeerBindings(ochazuke.Binding{
		Mode: "normal", Noun: "lane.lane", Keys: "g s", Hint: "send the lane's doc notes to its pane",
		Request: ochazuke.Request{Noun: "md.doc", Verb: "send-notes"},
		From:    map[string]string{"target": "row:doc", "pane": "row:pane"},
	}))
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
	statuses, e := parseStatuses(cfg.String("lanes.statuses"))
	if e != nil {
		return e
	}
	a.store.Statuses = statuses
	a.refreshOpenFilter()
	a.refreshStatusEnum()
	var presets map[string]string
	if e = json.Unmarshal([]byte(cfg.String("lanes.status-presets")), &presets); e != nil {
		return fmt.Errorf("lanes.status-presets: %w", e)
	}
	if presets == nil {
		presets = map[string]string{}
	}
	a.store.StatusPresets = presets
	a.store.BackupDaily = cfg.Bool("backup.daily")
	a.store.BackupKeep = cfg.Int("backup.keep")
	a.store.HistoryKeep = cfg.Int("history.keep")
	a.store.PanesShared = !cfg.Bool("lanes.pane-one-lane")
	a.linkProjects()
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
		{"lane", "add", "Create one lane; no parent creates a root", fields, []string{"name"}, false, "", (*App).runLaneAdd, nil, false},
		{"lane", "define", "Atomically define JSON specs or a plan Lanes table; exact parent/name updates", defineParams(), nil, false, "", (*App).runLaneDefine, nil, false},
		{"plan", "define", "Define a plan root and its lanes; same handler as lane.define --plan", planDefineParams(), []string{"plan"}, false, "", (*App).runPlanDefine, nil, false},
		{"plan", "lanes", "List recorded lanes linked to an exact plan path", map[string]ochazuke.Parameter{"plan": required("Plan path or exact filename in lanes.plans-dir")}, []string{"plan"}, true, "", (*App).runPlanLanes, nil, false},
		{"lane", "list", "List recorded lanes", map[string]ochazuke.Parameter{"parent": param("Filter by parent"), "deleted": {Type: "boolean", Description: "Include deleted lanes and their audit history"}, "stale": {Type: "boolean", Description: "Only working lifecycle statuses with no working participant (requires live reads)"}, "project": projectFilter, "status": statusParam("")}, nil, true, "", (*App).runLaneList, nil, false},
		{"lane", "tree", "List lanes in parent-first order", map[string]ochazuke.Parameter{"open": {Type: "boolean", Description: "Hide closed roots"}, "project": param("Only subtrees containing lanes of this project"), "status": statusParam("ancestors of matches stay as context")}, nil, true, "", (*App).runLaneTree, nil, false},
		{"lane", "show", "Show a lane and derived lifecycle timestamps", common(nil), []string{"target"}, true, "", (*App).runLaneShow, nil, false},
		{"lane", "history", "Show a lane's actor-attributed history", common(nil), []string{"target"}, true, "", (*App).runLaneHistory, nil, false},
		{"lane", "set", "Set one spec property", common(map[string]ochazuke.Parameter{"prop": required("Property"), "value": required("New value; JSON array for depends")}), []string{"target", "prop", "value"}, false, "", nil, nil, false},
		{"lane", "rename", "Change lane name", common(map[string]ochazuke.Parameter{"name": required("New name")}), []string{"target", "name"}, false, "", (*App).runLaneRename, nil, false},
		{"lane", "reword", "Change the owner's wording, retaining undo", common(map[string]ochazuke.Parameter{"text": required("Verbatim wording")}), []string{"target", "text"}, false, "", (*App).runLaneReword, nil, false},
		{"lane", "move", "Change parent; refuses cycles", common(map[string]ochazuke.Parameter{"parent": param("New parent; empty makes root")}), []string{"target", "parent"}, false, "", (*App).runLaneMove, nil, false},
		{"lane", "duplicate", "Duplicate the spec into a planned retry linked to the original", common(map[string]ochazuke.Parameter{"name": param("New name")}), []string{"target", "name"}, false, "", (*App).runLaneDuplicate, []string{"dup"}, false},
		{"lane", "start", "Start a lane, optionally recording a pane or pane-less agent", common(map[string]ochazuke.Parameter{"pane": param("Window ID"), "host": param("Pane host"), "session": param("Pane session"), "role": param("Participant role"), "harness": param("Harness"), "model": param("Model"), "agent": param("Agent kind"), "agent-id": param("Harness sub-agent ID (live status from lanes.agent-harnesses)"), "agent-session": param("Harness session the agent runs in"), "branch": param("Branch"), "worktree": param("Worktree path")}), []string{"target"}, false, "", (*App).runLaneStart, nil, false},
		{"lane", "choose-status", "Choose the lane's status from the configured lanes.statuses (the standard set of the status prop, as a picker)", common(map[string]ochazuke.Parameter{"status": statusChoice()}), []string{"target", "status"}, false, "", (*App).runLaneChooseStatus, nil, false},
		{"lane", "tag", "Record a watcher tag: the status lanes.tag-statuses maps it to, else a note", common(map[string]ochazuke.Parameter{"tag": required("Watcher tag; unmapped tags append notes"), "note": param("History note")}), []string{"target", "tag"}, false, "", (*App).runLaneTag, nil, false},
		{"lane", "annotate", "Append a history note", common(map[string]ochazuke.Parameter{"text": required("Note")}), []string{"target", "text"}, false, "", (*App).runLaneAnnotate, nil, false},
		{"lane", "live", "Read each pane and the aggregate live status of the lane subtree", common(nil), []string{"target"}, true, "", (*App).runLaneLive, nil, false},
		{"lane", "focus-pane", "Focus an associated pane using the configured argv", common(map[string]ochazuke.Parameter{"role": param("Optional role filter")}), []string{"target"}, true, "", (*App).runLaneFocusPane, nil, false},
		{"lane", "yank-id", "Copy the lane ID to the framework register", common(nil), []string{"target"}, true, "", (*App).runLaneYankID, nil, false},
		{"lane", "yank-path", "Copy the worktree path to the framework register", common(nil), []string{"target"}, true, "", (*App).runLaneYankPath, nil, false},
		{"lane", "delete", "Hide a lane and subtree for good; undo restores them. Agents archive instead (lane archive, or lane set ID status=archived)", common(nil), []string{"target"}, false, ochazuke.AgentNever, (*App).runLaneDelete, nil, false},
		{"lane", "undo", "Restore the latest change; agents can undo their own latest change", nil, nil, false, "", (*App).runLaneUndo, nil, false},
	}
	finishedParams := map[string]ochazuke.Parameter{
		"finished-trees": {Type: "boolean", Description: "Collection scope: archive every root tree whose lanes are all complete (a status with ends or the archive role); partly complete trees stay untouched. Takes no target"},
		"dry-run":        {Type: "boolean", Description: "With --finished-trees: list the trees that would be archived and change nothing"},
	}
	for _, v := range []struct {
		name, desc string
		run        func(*App, *call) ochazuke.Result
		extra      map[string]ochazuke.Parameter
	}{{"park", "Park", (*App).runLanePreset, nil}, {"merge", "Record merge and optional commit", (*App).runLaneMerge, nil}, {"finish", "Mark done", (*App).runLanePreset, nil}, {"cancel", "Cancel", (*App).runLanePreset, nil}, {"reopen", "Reopen as planned", (*App).runLanePreset, nil}, {"archive", "Archive: hide from the default views, keep the record; restore undoes it. With --finished-trees, archive every fully complete root tree", (*App).runLaneArchive, finishedParams}} {
		params := map[string]ochazuke.Parameter{"note": param("History note"), "commit": param("Merge commit")}
		maps.Copy(params, v.extra)
		rows = append(rows, verbSpec{"lane", v.name, v.desc, common(params), []string{"target"}, false, "", v.run, nil, false})
	}
	for _, v := range []struct {
		name   string
		remove bool
	}{{"link", false}, {"unlink", true}} {
		rows = append(rows, verbSpec{noun: "lane", name: v.name, description: "Add or remove a typed link", params: common(map[string]ochazuke.Parameter{"kind": required("Link kind"), "link": required("Link target"), "label": param("Label")}), positional: []string{"target", "kind", "link"}, run: (*App).runLaneLink, remove: v.remove})
	}
	assoc := map[string]func(*App, *call) ochazuke.Result{"lane": (*App).runLaneAssociate, "pane": (*App).runPaneAssociate, "agent": (*App).runAgentAssociate, "project": (*App).runProjectAssociate}
	for _, noun := range []string{"lane", "pane", "agent", "project"} {
		for _, v := range []struct {
			verb   string
			remove bool
		}{{"associate", false}, {"unassociate", true}} {
			verb := v.verb
			m := common(map[string]ochazuke.Parameter{"lane": param("Lane ID for pane- or agent-side relation"), "pane": param("Window ID"), "host": param("Pane host"), "session": param("Pane session"), "role": param("Participant role; default implementer"), "harness": param("Harness"), "model": param("Model"), "agent": param("Agent kind"), "agent-id": param("Harness sub-agent ID (live status from lanes.agent-harnesses)"), "agent-session": param("Harness session the agent runs in")})
			pos := []string{"target", "pane"}
			desc := "Associate or unassociate a lane and pane"
			switch noun {
			case "lane":
				m["project"] = projectParam("Project reference to associate (or, for unassociate, to remove); a root's project covers its subtree")
				desc = "Associate or unassociate a lane with a pane, agent or project"
			case "pane":
				pos = []string{"pane", "lane"}
			case "agent":
				pos = []string{"agent-id", "lane"}
				desc = "Associate or unassociate an agent and lane; same handler as lane." + verb + " --agent-id"
			case "project":
				m = map[string]ochazuke.Parameter{"project": projectParam("Project reference"), "lane": required("Lane ID, unique prefix or name")}
				pos = []string{"project", "lane"}
				desc = "Associate or unassociate a project and lane; same handler as lane." + verb + " --project"
			}
			rows = append(rows, verbSpec{noun: noun, name: verb, description: desc, params: m, positional: pos, run: assoc[noun], remove: v.remove})
		}
	}
	rows = append(rows, verbSpec{"project", "lanes", "List lanes whose effective project matches the reference", map[string]ochazuke.Parameter{"project": projectParam("Project reference"), "status": statusParam("")}, []string{"project"}, true, "", (*App).runProjectLanes, nil, false})
	rows = append(rows, verbSpec{"project", "resolve", "Rewrite every lane's project reference that names exactly one host project (by name, id or mtm:<id>) as that project's id, keeping the name in project_label; unmatched and ambiguous names and rows without a project stay byte for byte as they were. --dry-run writes nothing and prints the counts; a real run first copies the lane roots to <data>.bak-<date>-project-ids and is idempotent", map[string]ochazuke.Parameter{"dry-run": {Type: "boolean", Description: "Print what would change and write nothing"}}, nil, false, ochazuke.AgentNever, (*App).runProjectResolve, nil, false})
	rows = append(rows, verbSpec{"project", "list", "List project references recorded on lanes, plus lanes.project-source projects", nil, nil, true, "", (*App).runProjectList, nil, false})
	rows = append(rows, verbSpec{"pane", "lanes", "List lanes associated with a pane", map[string]ochazuke.Parameter{"pane": param("Window ID"), "host": param("Host"), "session": param("Session")}, []string{"pane"}, true, "", (*App).runPaneLanes, nil, false})
	agentRef := map[string]ochazuke.Parameter{"agent-id": required("Harness sub-agent ID"), "harness": param("Harness; empty matches any")}
	rows = append(rows, verbSpec{"agent", "lanes", "List lanes associated with an agent", agentRef, []string{"agent-id"}, true, "", (*App).runAgentLanes, nil, false})
	rows = append(rows, verbSpec{"agent", "live", "Read an agent's live status (working, idle, done, missing) from lanes.agent-harnesses data", map[string]ochazuke.Parameter{"agent-id": required("Harness sub-agent ID"), "harness": param("Harness; empty matches any"), "agent-session": param("Harness session the agent runs in")}, []string{"agent-id"}, true, "", (*App).runAgentLive, nil, false})
	rows = append(rows, verbSpec{"agent", "list", "List agents recorded by agent-hook, newest first, with live status", nil, nil, true, "", (*App).runAgentList, nil, false})
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
func (a *App) bindActor(c ochazuke.Ctx) {

	s := a.store
	s.Actor = c.Actor()
	// Store direct callers use the same defaults. App calls honour owner rights
	// overrides using the one framework policy source, never a second allowlist.
	s.Permit = func(action string) error {
		op, ok := a.r.Operations.Find(ochazuke.Key("lane." + action))
		if !ok {
			return fmt.Errorf("unknown action %s", action)
		}
		r := a.r.AgentRight(op, false)
		if r.Right != ochazuke.AgentAllow {
			return &ochazuke.RefusedError{Verb: op.Key.Short(), Rule: r.Rule, Reason: "agent right is " + r.Right}
		}
		return nil
	}
}
