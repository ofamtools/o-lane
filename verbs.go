package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ofam.dev/ochazuke"
)

// call is one verb run: the context, the verb name the spec row declared
// and the arguments every lane verb reads the same way.
type call struct {
	c       ochazuke.Ctx
	verb    string
	target  string
	project string
	remove  bool
	p       Pane
}

func (k *call) str(key string) string { return ochazuke.StringArg(k.c.Args, key, "") }

// handler wraps a spec row's run with the steps every verb shares:
// refresh the configured statuses, bind the actor and read the arguments.
func (a *App) handler(v verbSpec) func(ochazuke.Ctx) ochazuke.Result {
	return func(c ochazuke.Ctx) ochazuke.Result {
		if a.changed != nil {
			defer a.changed()
		}
		if e := a.configure(); e != nil {
			return ochazuke.Failed(e)
		}
		a.bindActor(c)
		k := &call{c: c, verb: v.name, remove: v.remove}
		k.target = k.str("target")
		if k.target == "" && c.Instance != nil {
			k.target = c.Instance.ID
			if id, ok := c.Instance.Data.(string); ok {
				k.target = id
			}
			k.target = strings.TrimPrefix(k.target, "lane/")
		}
		k.p = Pane{Host: k.str("host"), Session: k.str("session"), Window: k.str("pane"), Role: k.str("role"), Harness: k.str("harness"), Model: k.str("model"), Agent: k.str("agent"), AgentID: k.str("agent-id"), AgentSession: k.str("agent-session")}
		k.project = k.str("project")
		if k.p.Session == "" && k.p.Window != "" {
			k.p.Session = a.store.Actor.Session
		}
		return v.run(a, k)
	}
}

// listStatuses reads and checks the --status filter of a listing verb.
func (a *App) listStatuses(k *call) ([]string, error) {
	statuses := statusList(k.c.Args["status"])
	return statuses, a.store.checkStatuses(statuses)
}

func (a *App) runLaneAdd(k *call) ochazuke.Result {
	var spec Spec
	if e := decode(k.c.Args, &spec); e != nil {
		return done(nil, e)
	}
	q, e := a.store.Add(spec)
	return done(q, e)
}

func (a *App) runLaneDefine(k *call) ochazuke.Result { return a.define(k.c.Args) }

func (a *App) runPlanDefine(k *call) ochazuke.Result { return a.define(k.c.Args) }

func (a *App) runPlanLanes(k *call) ochazuke.Result { return a.planLanes(k.str("plan")) }

func (a *App) runLaneList(k *call) ochazuke.Result { return a.listLanes(k, false) }

func (a *App) runLaneTree(k *call) ochazuke.Result { return a.listLanes(k, true) }

// listLanes is the one listing handler behind lane.list and lane.tree: a
// flat filtered list, or the parent-first walk.
func (a *App) listLanes(k *call, tree bool) ochazuke.Result {
	s := a.store
	statuses, e := a.listStatuses(k)
	if e != nil {
		return done(nil, e)
	}
	var rows []Lane
	includeDeleted, _ := k.c.Args["deleted"].(bool)
	if !tree && includeDeleted {
		rows, e = s.ListAll()
	} else {
		rows, e = s.List()
	}
	if e != nil {
		return done(nil, e)
	}
	var out []View
	if tree {
		out = a.treeViews(k, rows, statuses)
	} else if out, e = a.flatViews(k, rows, statuses); e != nil {
		return done(nil, e)
	}
	e = a.enrichLive(out)
	if only, _ := k.c.Args["stale"].(bool); only {
		out = slices.DeleteFunc(out, func(v View) bool { return !v.Stale })
	}
	return done(out, e)
}

// treeViews walks the lanes parent-first, keeping the subtrees the project
// and status filters match.
func (a *App) treeViews(k *call, rows []Lane, statuses []string) []View {
	s := a.store
	out := []View{}
	kept := statusKept(rows, statuses)
	hidden := s.archiveHidden(rows, statuses, a.r.Settings().Bool("collection.hide-archived"))
	match := s.projectMatcher(k.project)
	inProject := func(q Lane) bool {
		return (k.project == "" || subtreeProject(rows, q, match)) && (kept == nil || kept[q.ID]) && !hidden[q.ID]
	}
	var walk func(string, bool)
	walk = func(parent string, hide bool) {
		for _, q := range rows {
			if q.Parent != parent || !inProject(q) {
				continue
			}
			if q.Parent == "" && hide {
				st, ok := s.statuses()[s.view(q, rows).RolledStatus]
				if ok && !st.Open {
					continue
				}
			}
			out = append(out, s.view(q, rows))
			walk(q.ID, false)
		}
	}
	open, _ := k.c.Args["open"].(bool)
	walk("", open || a.r.Settings().Bool("lanes.hide-closed-roots"))
	return out
}

// flatViews filters the lanes by parent, project and status.
func (a *App) flatViews(k *call, rows []Lane, statuses []string) ([]View, error) {
	s := a.store
	hidden := s.archiveHidden(rows, statuses, a.r.Settings().Bool("collection.hide-archived"))
	parent := k.str("parent")
	if parent != "" {
		q, e := resolve(rows, parent)
		if e != nil {
			return nil, e
		}
		parent = q.ID
	}
	out := []View{}
	match := s.projectMatcher(k.project)
	for _, q := range rows {
		v := s.view(q, rows)
		if (parent == "" || q.Parent == parent) && (k.project == "" || v.EffectiveProjectID != "" && match(v.EffectiveProjectID, v.EffectiveProjectLabel)) && inStatuses(statuses, q) && !hidden[q.ID] {
			out = append(out, v)
		}
	}
	return out, nil
}

func inStatuses(statuses []string, q Lane) bool {
	return len(statuses) == 0 || slices.Contains(statuses, q.Status)
}

func (a *App) runLaneShow(k *call) ochazuke.Result {
	s := a.store
	q, e := s.Resolve(k.target)
	if e != nil {
		return done(nil, e)
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
}

func (a *App) runLaneHistory(k *call) ochazuke.Result {
	q, e := a.store.Resolve(k.target)
	if e != nil {
		return done(nil, e)
	}
	return done(q.History, nil)
}

func (a *App) runLaneLive(k *call) ochazuke.Result {
	q, e := a.store.Resolve(k.target)
	if e != nil {
		return done(nil, e)
	}
	return a.liveResult(k.c, q)
}

func (a *App) runLaneFocusPane(k *call) ochazuke.Result {
	q, e := a.store.Resolve(k.target)
	if e != nil {
		return done(nil, e)
	}
	return a.focusPane(k.c, q, k.str("role"))
}

func (a *App) runLaneYankID(k *call) ochazuke.Result {
	q, e := a.store.Resolve(k.target)
	if e != nil {
		return done(nil, e)
	}
	return a.yank(q.ID)
}

func (a *App) runLaneYankPath(k *call) ochazuke.Result {
	q, e := a.store.Resolve(k.target)
	if e != nil {
		return done(nil, e)
	}
	return a.yank(q.Worktree)
}

func (a *App) yank(text string) ochazuke.Result {
	e := a.r.StoreYank(ochazuke.Yank{Text: text, Shape: "char"}, "\"", false)
	return done(text, e)
}

func (a *App) runLaneRename(k *call) ochazuke.Result {
	q, e := a.store.Rename(k.target, k.str("name"))
	return done(q, e)
}

func (a *App) runLaneReword(k *call) ochazuke.Result {
	q, e := a.store.Reword(k.target, k.str("text"))
	return done(q, e)
}

func (a *App) runLaneMove(k *call) ochazuke.Result {
	q, e := a.store.Move(k.target, k.str("parent"))
	return done(q, e)
}

func (a *App) runLaneDuplicate(k *call) ochazuke.Result {
	q, e := a.store.Duplicate(k.target, k.str("name"))
	return done(q, e)
}

func (a *App) runLaneAnnotate(k *call) ochazuke.Result {
	q, e := a.store.Annotate(k.target, k.str("text"))
	return done(q, e)
}

// statusChoice is the lifecycle status parameter of choose-status: its
// choices are the configured lanes.statuses (refreshStatusEnum), so the
// args form offers them.
func statusChoice() ochazuke.Parameter {
	p := required("Lifecycle status")
	p.Free = true // Any configured status is valid; the store names the bad ones.
	return p
}

// runLaneChooseStatus is the Lanes page's s row: the status prop's write
// (lane set ID status X) with the status asked for as a required argument.
func (a *App) runLaneChooseStatus(k *call) ochazuke.Result {
	q, e := a.store.StatusAs(k.verb, k.target, k.str("status"), "")
	return done(q, e)
}

// runLaneTag maps a watcher tag through lanes.tag-statuses to a status; a
// tag the table does not map is appended as a note.
func (a *App) runLaneTag(k *call) ochazuke.Result {
	s := a.store
	tag := k.str("tag")
	note := strings.TrimSpace(tag + " " + k.str("note"))
	table := map[string]string{}
	if e := json.Unmarshal([]byte(a.r.Settings().String("lanes.tag-statuses")), &table); e != nil {
		return done(nil, e)
	}
	status := table[tag]
	if status == "" {
		q, e := s.Annotate(k.target, note)
		return done(q, e)
	}
	q, e := s.StatusAs(k.verb, k.target, status, note)
	return done(q, e)
}

func (a *App) runLaneStart(k *call) ochazuke.Result {
	p := k.p
	if p.Role != "" && !slices.Contains(a.r.Settings().Strings("lanes.pane-roles"), p.Role) {
		return done(nil, fmt.Errorf("unknown role %q; configure lanes.pane-roles", p.Role))
	}
	p = a.resolvePane(p)
	q, e := a.store.Start(k.target, p, k.str("branch"), k.str("worktree"))
	return done(q, e)
}

// runLanePreset runs a status preset verb (park, review, finish, cancel,
// reopen, archive); the row's name is the preset.
func (a *App) runLanePreset(k *call) ochazuke.Result {
	q, e := a.store.Preset(k.target, k.verb, k.str("note"))
	return done(q, e)
}

func (a *App) runLaneMerge(k *call) ochazuke.Result {
	q, e := a.store.Merge(k.target, k.str("commit"), k.str("note"))
	return done(q, e)
}

func (a *App) runLaneLink(k *call) ochazuke.Result {
	link := Link{Kind: k.str("kind"), Target: k.str("link"), Label: k.str("label")}
	if !slices.Contains(a.r.Settings().Strings("lanes.link-kinds"), link.Kind) {
		return done(nil, fmt.Errorf("unknown link kind %q; configure lanes.link-kinds", link.Kind))
	}
	q, e := a.store.Link(k.target, link, k.remove)
	return done(q, e)
}

func (a *App) runLaneDelete(k *call) ochazuke.Result {
	q, e := a.store.Delete(k.target)
	return done(q, e)
}

func (a *App) runLaneUndo(k *call) ochazuke.Result {
	return done("undone", a.store.Undo())
}

// associate is the one relation handler behind lane., pane., agent. and
// project.associate and unassociate; each noun's row reads its own
// positional arguments into the call first.
func (a *App) associate(k *call) ochazuke.Result {
	s := a.store
	p := k.p
	if p.Role != "" && !slices.Contains(a.r.Settings().Strings("lanes.pane-roles"), p.Role) {
		return done(nil, fmt.Errorf("unknown role %q; configure lanes.pane-roles", p.Role))
	}
	remove := k.remove
	if p.AgentID != "" && !safeAgentID(p.AgentID) {
		return done(nil, fmt.Errorf("agent id %q must be a simple id (letters, digits, _ . -)", p.AgentID))
	}
	if k.project != "" && p.Window == "" && p.Host == "" && p.Harness == "" && p.Model == "" && p.Agent == "" && p.AgentID == "" {
		q, e := s.AssociateProject(k.target, k.project, remove)
		return done(q, e)
	}
	if !remove {
		p = a.resolvePane(p)
	}
	if k.project != "" {
		// Both relations in one call share one snapshot and one undo.
		var moved []LaneRef
		q, e := s.mutate(k.verb, k.target, func(q *Lane, rows *[]Lane) error {
			if e := s.project(q, k.project, remove); e != nil {
				return e
			}
			moved = s.associate(q, rows, p, remove)
			return nil
		})
		return done(Associated{Lane: q, MovedFrom: moved}, e)
	}
	q, e := s.AssociateMoving(k.target, p, remove)
	return done(q, e)
}

func (a *App) runLaneAssociate(k *call) ochazuke.Result { return a.associate(k) }

// runLaneChoosePane is lane.associate with the pane argument read as a
// pane choice, which carries the full relation, so equal window labels in
// different sessions never lose their session at a mounted host.
func (a *App) runLaneChoosePane(k *call) ochazuke.Result {
	p, e := paneChoice(k.p.Window)
	if e != nil {
		return done(nil, fmt.Errorf("pane choice: %w", e))
	}
	if role := k.str("role"); role != "" {
		p.Role = role
	}
	k.p = p
	k.verb = "associate"
	return a.associate(k)
}

func (a *App) runPaneAssociate(k *call) ochazuke.Result {
	k.target = k.str("lane")
	if k.p.Window == "" {
		k.p.Window = k.str("target")
	}
	return a.associate(k)
}

func (a *App) runAgentAssociate(k *call) ochazuke.Result {
	k.target = k.str("lane")
	if k.p.AgentID == "" {
		k.p.AgentID = k.str("target")
	}
	return a.associate(k)
}

func (a *App) runProjectAssociate(k *call) ochazuke.Result {
	k.target = k.str("lane")
	if k.project == "" {
		k.project = k.str("target")
	}
	if k.project == "" {
		return done(nil, fmt.Errorf("project.%s needs a project reference", k.verb))
	}
	q, e := a.store.AssociateProject(k.target, k.project, k.remove)
	return done(q, e)
}

func (a *App) runProjectLanes(k *call) ochazuke.Result {
	s := a.store
	statuses, e := a.listStatuses(k)
	if e != nil {
		return done(nil, e)
	}
	rows, e := s.List()
	if e != nil {
		return done(nil, e)
	}
	out := []View{}
	hidden := s.archiveHidden(rows, statuses, a.r.Settings().Bool("collection.hide-archived"))
	match := s.projectMatcher(k.project)
	for _, q := range rows {
		if v := s.view(q, rows); v.EffectiveProjectID != "" && match(v.EffectiveProjectID, v.EffectiveProjectLabel) && inStatuses(statuses, q) && !hidden[q.ID] {
			out = append(out, v)
		}
	}
	return done(out, a.enrichLive(out))
}

func (a *App) runProjectList(k *call) ochazuke.Result {
	out, e := a.projectRows(context.Background())
	return done(out, e)
}

// lanesWithPane lists the lanes that hold a pane matching p.
func (a *App) lanesWithPane(p Pane) ([]View, error) {
	rows, e := a.store.List()
	if e != nil {
		return nil, e
	}
	out := []View{}
	for _, q := range rows {
		for _, old := range q.Panes {
			if paneMatches(old, p) {
				out = append(out, a.store.view(q, rows))
				break
			}
		}
	}
	return out, nil
}

func (a *App) runPaneLanes(k *call) ochazuke.Result {
	out, e := a.lanesWithPane(k.p)
	return done(out, e)
}

func (a *App) runAgentLanes(k *call) ochazuke.Result {
	out, e := a.lanesWithPane(k.p)
	if e != nil {
		return done(nil, e)
	}
	return done(out, a.enrichLive(out))
}

func (a *App) runAgentLive(k *call) ochazuke.Result {
	if !safeAgentID(k.p.AgentID) {
		return done(nil, fmt.Errorf("agent.live needs a simple agent id"))
	}
	return done(a.agentReader()(k.p), nil)
}

func (a *App) runAgentList(k *call) ochazuke.Result {
	out, e := a.agentRows()
	return done(out, e)
}

// runLaneArchive is lane.archive: the archive preset on one lane, or with
// --finished-trees the collection form over every root tree that is entirely
// complete (--dry-run lists them and writes nothing).
func (a *App) runLaneArchive(k *call) ochazuke.Result {
	if finished, _ := k.c.Args["finished-trees"].(bool); finished {
		dry, _ := k.c.Args["dry-run"].(bool)
		return done(a.store.ArchiveFinished(dry, k.str("note")))
	}
	return a.runLanePreset(k)
}

// runProjectResolve is the one-time migration of stored project references
// to the host project's id (Store.ResolveProjects). With no projects from
// lanes.project-source there is nothing to resolve against, and it says so
// rather than reporting every reference unmatched.
func (a *App) runProjectResolve(k *call) ochazuke.Result {
	list, e := a.hostProjects(context.Background())
	if e != nil {
		return done(nil, e)
	}
	if len(list) == 0 {
		return done(nil, errors.New("no projects from lanes.project-source (mtm); nothing to resolve against, nothing written"))
	}
	dry, _ := k.c.Args["dry-run"].(bool)
	rep, e := a.store.ResolveProjects(list, dry)
	return done(rep, e)
}
