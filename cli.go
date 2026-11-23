package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

var errUsage = errors.New("usage")

type parsedArgs struct {
	pos   []string
	flags map[string]string
	bools map[string]bool
}

func (a parsedArgs) has(k string) bool { return a.bools[k] }

// parseArgs parses flags anywhere in argv; boolFlags take no value unless given with =true/=false.
func parseArgs(argv []string, boolFlags ...string) (parsedArgs, error) {
	a := parsedArgs{flags: map[string]string{}, bools: map[string]bool{}}
	isBool := map[string]bool{}
	for _, k := range boolFlags {
		isBool[k] = true
	}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			a.pos = append(a.pos, argv[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "--") && len(arg) > 2 {
			rest := arg[2:]
			if k, v, ok := strings.Cut(rest, "="); ok {
				if isBool[k] {
					a.bools[k] = v != "false" && v != "0"
				} else {
					a.flags[k] = v
				}
				continue
			}
			if isBool[rest] {
				a.bools[rest] = true
				continue
			}
			i++
			if i >= len(argv) {
				return a, fmt.Errorf("--%s needs a value", rest)
			}
			a.flags[rest] = argv[i]
			continue
		}
		if strings.HasPrefix(arg, "-") && len(arg) == 2 && isBool[arg[1:]] {
			a.bools[arg[1:]] = true
			continue
		}
		a.pos = append(a.pos, arg)
	}
	return a, nil
}

// splitDataArgs removes --data DIR anywhere before `--`.
func splitDataArgs(argv []string) ([]string, string, error) {
	out := make([]string, 0, len(argv))
	data := ""
	for i := 0; i < len(argv); i++ {
		if argv[i] == "--" {
			out = append(out, argv[i:]...)
			break
		}
		if argv[i] == "--data" {
			i++
			if i >= len(argv) {
				return nil, "", errors.New("--data needs a directory")
			}
			data = argv[i]
		} else if value, ok := strings.CutPrefix(argv[i], "--data="); ok {
			data = value
		} else {
			out = append(out, argv[i])
		}
	}
	return out, data, nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func usageText() string {
	return `Usage:
  o-lane [--data DIR]                                 open the Lanes & Plans TUI
  o-lane lane define [NAME] [--parent P] [--project P] [--plan FILE] [--lanes JSON|-] [--dry-run]
  o-lane lane list [--status S] [--project P] [--archived] [--format table|json]
  o-lane lane show ID [--json]
  o-lane lane state ID STATUS [--note TEXT]
  o-lane lane note ID TEXT
  o-lane lane associate ID [--project REF] [--pane WIN] [--session S] [--host H] [--role R] [--remove]
  o-lane lane archive [ID] [--finished-trees] [--restore] [--dry-run] [--note TEXT]
  o-lane lane undo | redo
  o-lane plan define FILE [--name NAME] [--project REF] [--wording TEXT] [--dry-run]
  o-lane plan list [--format table|json]
  o-lane plan show PLAN [--json]
  o-lane rollup [ID] [--project REF] [--format table|json]
  o-lane catalog                                      extension manifest JSON (lane.lane, lane.plan)
  o-lane query [TEXT] [--kind KIND] [--archived]      read-only {"items": [...], "total": n}
  o-lane --version | --smoke | --help

Data lives in --data, else $XDG_DATA_HOME/o-lane, else ~/.local/share/o-lane.
Settings, bindings and themes load from $XDG_CONFIG_HOME/o-lane (else ~/.config/o-lane).
In the TUI press ? for live key help, : for the command palette.`
}

type cli struct {
	st          *Store
	plansDir    string
	importCols  string
	hideArchive bool
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
}

func (c *cli) dispatch(argv []string) error {
	if len(argv) == 0 {
		return errUsage
	}
	switch argv[0] {
	case "lane":
		return c.cmdLane(argv[1:])
	case "plan":
		return c.cmdPlan(argv[1:])
	case "rollup":
		return c.cmdRollup(argv[1:])
	case "catalog":
		return writeJSON(c.stdout, manifest())
	case "query":
		return c.cmdQuery(argv[1:])
	default:
		return fmt.Errorf("unknown subcommand %q (try --help)", argv[0])
	}
}

func (c *cli) cmdLane(argv []string) error {
	if len(argv) == 0 {
		return errors.New("lane needs a subcommand: define, list, show, state, note, associate, archive, undo, redo")
	}
	switch argv[0] {
	case "define", "add":
		return c.laneDefine(argv[1:])
	case "list", "ls":
		return c.laneList(argv[1:])
	case "show":
		return c.laneShow(argv[1:])
	case "state", "status":
		return c.laneState(argv[1:])
	case "note", "annotate":
		return c.laneNote(argv[1:])
	case "associate", "unassociate":
		return c.laneAssociate(argv[1:], argv[0] == "unassociate")
	case "archive", "restore":
		return c.laneArchive(argv[1:], argv[0] == "restore")
	case "undo":
		if err := c.st.Undo(); err != nil {
			return err
		}
		return writeJSON(c.stdout, map[string]any{"undone": true})
	case "redo":
		if err := c.st.Redo(); err != nil {
			return err
		}
		return writeJSON(c.stdout, map[string]any{"redone": true})
	case "rename":
		a, err := parseArgs(argv[1:])
		if err != nil {
			return err
		}
		if len(a.pos) < 2 {
			return errors.New("lane rename needs ID and NEW_NAME")
		}
		q, err := c.st.Rename(a.pos[0], strings.Join(a.pos[1:], " "))
		if err != nil {
			return err
		}
		return writeJSON(c.stdout, q)
	case "reword":
		a, err := parseArgs(argv[1:])
		if err != nil {
			return err
		}
		if len(a.pos) < 2 {
			return errors.New("lane reword needs ID and TEXT")
		}
		q, err := c.st.Reword(a.pos[0], strings.Join(a.pos[1:], " "))
		if err != nil {
			return err
		}
		return writeJSON(c.stdout, q)
	case "move":
		a, err := parseArgs(argv[1:])
		if err != nil {
			return err
		}
		if len(a.pos) < 1 {
			return errors.New("lane move needs ID [PARENT]")
		}
		parent := a.flags["parent"]
		if len(a.pos) > 1 {
			parent = a.pos[1]
		}
		q, err := c.st.Move(a.pos[0], parent)
		if err != nil {
			return err
		}
		return writeJSON(c.stdout, q)
	case "delete":
		a, err := parseArgs(argv[1:])
		if err != nil {
			return err
		}
		if len(a.pos) < 1 {
			return errors.New("lane delete needs ID")
		}
		q, err := c.st.Delete(a.pos[0])
		if err != nil {
			return err
		}
		return writeJSON(c.stdout, q)
	default:
		return fmt.Errorf("unknown lane subcommand %q", argv[0])
	}
}

func (c *cli) importColumns() (map[string]string, error) {
	raw := c.importCols
	if raw == "" {
		raw = defaultImportColumns
	}
	return parseImportColumns(raw)
}

func (c *cli) laneDefine(argv []string) error {
	a, err := parseArgs(argv, "dry-run")
	if err != nil {
		return err
	}
	dry := a.has("dry-run")

	// Option 1: --plan FILE
	if planFile := a.flags["plan"]; planFile != "" {
		cols, err := c.importColumns()
		if err != nil {
			return err
		}
		rows, specs, err := defineFromPlan(c.st, planFile, c.plansDir, a.flags["name"], a.flags["wording"], a.flags["task"], a.flags["project"], dry, cols)
		if err != nil {
			return err
		}
		if dry {
			return writeJSON(c.stdout, specs)
		}
		return writeJSON(c.stdout, rows)
	}

	// Option 2: --lanes JSON (or `-` for stdin)
	if rawJSON := a.flags["lanes"]; rawJSON != "" {
		data := []byte(rawJSON)
		if rawJSON == "-" {
			data, err = io.ReadAll(c.stdin)
			if err != nil {
				return err
			}
		}
		var specs []Spec
		if err := json.Unmarshal(data, &specs); err != nil {
			return err
		}
		var fields []map[string]any
		_ = json.Unmarshal(data, &fields)
		defineKeyNotices(specs, fields, func(msg string) { fmt.Fprintln(c.stderr, msg) })
		if proj := a.flags["project"]; proj != "" {
			names := map[string]bool{}
			for _, sp := range specs {
				names[sp.Name] = true
			}
			for i := range specs {
				if specs[i].Project == "" && !names[specs[i].Parent] {
					specs[i].Project = proj
					if i < len(fields) {
						if fields[i] == nil {
							fields[i] = map[string]any{}
						}
						fields[i]["project"] = proj
					}
				}
			}
		}
		c.st.fillUntitled(specs)
		if err := validateDefinitions(specs); err != nil {
			return err
		}
		if dry {
			return writeJSON(c.stdout, specs)
		}
		rows, err := c.st.defineFields(specs, fields)
		if err != nil {
			return err
		}
		return writeJSON(c.stdout, rows)
	}

	// Option 3: single lane via positional NAME or --name
	name := a.flags["name"]
	if name == "" && len(a.pos) > 0 {
		name = strings.Join(a.pos, " ")
	}
	spec := Spec{
		Name:     name,
		Parent:   a.flags["parent"],
		Wording:  a.flags["wording"],
		Scope:    a.flags["scope"],
		DoneWhen: a.flags["done-when"],
		Tests:    a.flags["tests"],
		Repo:     a.flags["repo"],
		Base:     a.flags["base"],
		Branch:   a.flags["branch"],
		Worktree: a.flags["worktree"],
		Estimate: a.flags["estimate"],
		Task:     a.flags["task"],
		Project:  a.flags["project"],
	}
	if dep := a.flags["depends"]; dep != "" {
		spec.Depends = strings.Split(dep, ",")
	}
	if dry {
		return writeJSON(c.stdout, []Spec{spec})
	}
	q, err := c.st.Add(spec)
	if err != nil {
		return err
	}
	if st := a.flags["status"]; st != "" {
		q, err = c.st.Status(q.ID, st, a.flags["note"])
		if err != nil {
			return err
		}
	}
	return writeJSON(c.stdout, q)
}

func (c *cli) laneList(argv []string) error {
	a, err := parseArgs(argv, "archived", "json")
	if err != nil {
		return err
	}
	rows, err := c.st.List()
	if err != nil {
		return err
	}
	statusFilter := strings.TrimSpace(a.flags["status"])
	projectFilter := strings.TrimSpace(a.flags["project"])
	var named []string
	if statusFilter != "" {
		named = []string{statusFilter}
	}
	hidden := c.st.archiveHidden(rows, named, c.hideArchive && !a.has("archived"))
	var matcher func(string, string) bool
	if projectFilter != "" {
		matcher = c.st.projectMatcher(projectFilter, rows)
	}
	roll := c.st.rollups(rows)
	views := []View{}
	for _, q := range rows {
		if hidden[q.ID] {
			continue
		}
		if statusFilter != "" && !strings.EqualFold(q.Status, statusFilter) {
			continue
		}
		v := c.st.view(q, rows)
		if matcher != nil && !matcher(v.EffectiveProjectID, v.EffectiveProjectLabel) && !strings.EqualFold(v.EffectiveProject, projectFilter) {
			continue
		}
		views = append(views, v)
	}
	format := a.flags["format"]
	if a.has("json") {
		format = "json"
	}
	switch format {
	case "json":
		return writeJSON(c.stdout, views)
	case "", "table":
		for _, v := range views {
			r := roll[v.ID]
			rc := ""
			if r[1] > 0 && r[0] < r[1] {
				rc = fmt.Sprintf(" (%d/%d)", r[0], r[1])
			}
			proj := v.EffectiveProject
			if proj == "" {
				proj = "-"
			}
			fmt.Fprintf(c.stdout, "%-24s  %-10s  %-14s  %s%s\n", v.ID, v.Status, proj, v.Name, rc)
		}
		return nil
	default:
		return fmt.Errorf("unknown --format %q (table or json)", format)
	}
}

func (c *cli) laneShow(argv []string) error {
	a, err := parseArgs(argv, "json")
	if err != nil {
		return err
	}
	if len(a.pos) == 0 {
		return errors.New("lane show needs a lane ID or name")
	}
	rows, err := c.st.List()
	if err != nil {
		return err
	}
	q, err := resolve(rows, a.pos[0])
	if err != nil {
		return err
	}
	v := c.st.view(q, rows)
	if a.has("json") {
		return writeJSON(c.stdout, v)
	}
	roll := c.st.rollups(rows)
	fmt.Fprint(c.stdout, renderLaneMarkdown(c.st, v, rows, roll))
	return nil
}

func (c *cli) laneState(argv []string) error {
	a, err := parseArgs(argv)
	if err != nil {
		return err
	}
	if len(a.pos) < 2 {
		return errors.New("lane state needs ID and STATUS")
	}
	note := a.flags["note"]
	if note == "" {
		note = a.flags["reason"]
	}
	if note == "" && len(a.pos) > 2 {
		note = strings.Join(a.pos[2:], " ")
	}
	q, err := c.st.Status(a.pos[0], a.pos[1], note)
	if err != nil {
		return err
	}
	return writeJSON(c.stdout, q)
}

func (c *cli) laneNote(argv []string) error {
	a, err := parseArgs(argv)
	if err != nil {
		return err
	}
	if len(a.pos) < 2 {
		return errors.New("lane note needs ID and TEXT")
	}
	q, err := c.st.Annotate(a.pos[0], strings.Join(a.pos[1:], " "))
	if err != nil {
		return err
	}
	return writeJSON(c.stdout, q)
}

func (c *cli) laneAssociate(argv []string, removeDefault bool) error {
	a, err := parseArgs(argv, "remove")
	if err != nil {
		return err
	}
	remove := removeDefault || a.has("remove")
	if len(a.pos) == 0 {
		return errors.New("lane associate needs a lane ID")
	}
	target := a.pos[0]
	if proj := a.flags["project"]; proj != "" {
		q, err := c.st.AssociateProject(target, proj, remove)
		if err != nil {
			return err
		}
		return writeJSON(c.stdout, q)
	}
	p := Pane{
		Host:         a.flags["host"],
		Session:      a.flags["session"],
		Window:       a.flags["pane"],
		Role:         a.flags["role"],
		Harness:      a.flags["harness"],
		Model:        a.flags["model"],
		Agent:        a.flags["agent"],
		AgentID:      a.flags["agent-id"],
		AgentSession: a.flags["agent-session"],
	}
	if p.Window == "" && a.flags["window"] != "" {
		p.Window = a.flags["window"]
	}
	if p.Window == "" && p.AgentID == "" && p.Harness == "" && p.Agent == "" {
		return errors.New("lane associate needs --project REF or --pane WIN (or --agent-id ID)")
	}
	res, err := c.st.AssociateMoving(target, p, remove)
	if err != nil {
		return err
	}
	return writeJSON(c.stdout, res)
}

func (c *cli) laneArchive(argv []string, restoreDefault bool) error {
	a, err := parseArgs(argv, "finished-trees", "dry-run", "restore")
	if err != nil {
		return err
	}
	note := a.flags["note"]
	if a.has("finished-trees") {
		res, err := c.st.ArchiveFinished(a.has("dry-run"), note)
		if err != nil {
			return err
		}
		return writeJSON(c.stdout, res)
	}
	if len(a.pos) == 0 {
		return errors.New("lane archive needs a lane ID or --finished-trees")
	}
	archiveOn := !(restoreDefault || a.has("restore"))
	q, err := c.st.mutate("archive", a.pos[0], func(q *Lane, _ *[]Lane) error {
		return c.st.setArchived(q, archiveOn, note)
	})
	if err != nil {
		return err
	}
	return writeJSON(c.stdout, q)
}

func (c *cli) cmdPlan(argv []string) error {
	if len(argv) == 0 {
		return errors.New("plan needs a subcommand: define, list, show")
	}
	switch argv[0] {
	case "define":
		a, err := parseArgs(argv[1:], "dry-run")
		if err != nil {
			return err
		}
		planFile := a.flags["plan"]
		if planFile == "" && len(a.pos) > 0 {
			planFile = a.pos[0]
		}
		if planFile == "" {
			return errors.New("plan define needs a Markdown plan file")
		}
		cols, err := c.importColumns()
		if err != nil {
			return err
		}
		rows, specs, err := defineFromPlan(c.st, planFile, c.plansDir, a.flags["name"], a.flags["wording"], a.flags["task"], a.flags["project"], a.has("dry-run"), cols)
		if err != nil {
			return err
		}
		if a.has("dry-run") {
			return writeJSON(c.stdout, specs)
		}
		return writeJSON(c.stdout, rows)
	case "list", "ls":
		a, err := parseArgs(argv[1:], "json")
		if err != nil {
			return err
		}
		rows, err := c.st.List()
		if err != nil {
			return err
		}
		plans := listPlans(c.st, rows)
		format := a.flags["format"]
		if a.has("json") {
			format = "json"
		}
		if format == "json" {
			return writeJSON(c.stdout, plans)
		}
		for _, p := range plans {
			fmt.Fprintf(c.stdout, "%-24s  %d/%d done  %s\n", p.ID, p.DoneCount, p.LaneCount, p.Name)
		}
		return nil
	case "show":
		a, err := parseArgs(argv[1:], "json")
		if err != nil {
			return err
		}
		if len(a.pos) == 0 {
			return errors.New("plan show needs a plan ID, name, or path")
		}
		rows, err := c.st.List()
		if err != nil {
			return err
		}
		target := a.pos[0]
		absTarget, _ := filepath.Abs(target)
		plans := listPlans(c.st, rows)
		var matched *PlanSummary
		for i := range plans {
			p := &plans[i]
			if p.ID == target || p.Path == target || p.Path == absTarget || strings.EqualFold(p.Name, target) {
				matched = p
				break
			}
		}
		if matched == nil {
			return fmt.Errorf("plan %q not found", target)
		}
		var planLanes []View
		for _, q := range rows {
			if q.Plan == matched.Path {
				planLanes = append(planLanes, c.st.view(q, rows))
			}
		}
		if a.has("json") {
			return writeJSON(c.stdout, map[string]any{"plan": matched, "lanes": planLanes})
		}
		fmt.Fprint(c.stdout, renderPlanMarkdown(*matched, planLanes))
		return nil
	default:
		return fmt.Errorf("unknown plan subcommand %q", argv[0])
	}
}

type rollupRow struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	RolledStatus  string `json:"rolled_status"`
	Project       string `json:"project,omitempty"`
	ChildrenDone  int    `json:"children_done"`
	ChildrenTotal int    `json:"children_total"`
}

func (c *cli) cmdRollup(argv []string) error {
	a, err := parseArgs(argv, "json", "archived")
	if err != nil {
		return err
	}
	rows, err := c.st.List()
	if err != nil {
		return err
	}
	hidden := c.st.archiveHidden(rows, nil, c.hideArchive && !a.has("archived"))
	roll := c.st.rollups(rows)
	projFilter := strings.TrimSpace(a.flags["project"])
	var matcher func(string, string) bool
	if projFilter != "" {
		matcher = c.st.projectMatcher(projFilter, rows)
	}
	var out []rollupRow
	if len(a.pos) > 0 {
		q, err := resolve(rows, a.pos[0])
		if err != nil {
			return err
		}
		v := c.st.view(q, rows)
		r := roll[q.ID]
		out = append(out, rollupRow{ID: q.ID, Name: q.Name, Status: q.Status, RolledStatus: v.RolledStatus, Project: v.EffectiveProject, ChildrenDone: r[0], ChildrenTotal: r[1]})
	} else {
		for _, q := range rows {
			if q.Parent != "" || hidden[q.ID] {
				continue
			}
			v := c.st.view(q, rows)
			if matcher != nil && !matcher(v.EffectiveProjectID, v.EffectiveProjectLabel) && !strings.EqualFold(v.EffectiveProject, projFilter) {
				continue
			}
			r := roll[q.ID]
			out = append(out, rollupRow{ID: q.ID, Name: q.Name, Status: q.Status, RolledStatus: v.RolledStatus, Project: v.EffectiveProject, ChildrenDone: r[0], ChildrenTotal: r[1]})
		}
	}
	format := a.flags["format"]
	if a.has("json") {
		format = "json"
	}
	if format == "json" {
		return writeJSON(c.stdout, out)
	}
	for _, r := range out {
		proj := r.Project
		if proj == "" {
			proj = "-"
		}
		fmt.Fprintf(c.stdout, "%-24s  %-10s  %2d/%-2d  %-14s  %s\n", r.ID, r.RolledStatus, r.ChildrenDone, r.ChildrenTotal, proj, r.Name)
	}
	return nil
}

func (c *cli) cmdQuery(argv []string) error {
	a, err := parseArgs(argv, "archived")
	if err != nil {
		return err
	}
	res, err := queryStore(c.st, strings.Join(a.pos, " "), a.flags["kind"], a.has("archived"))
	if err != nil {
		return err
	}
	return writeJSON(c.stdout, res)
}

func shortTimeRFC(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

// renderLaneMarkdown builds the Markdown detail view for a lane (shared by TUI and CLI).
func renderLaneMarkdown(st *Store, v View, rows []Lane, roll map[string][2]int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", v.Name)
	fmt.Fprintf(&b, "- **ID**: `%s`\n", v.ID)
	fmt.Fprintf(&b, "- **Status**: `%s`", v.Status)
	if v.RolledStatus != "" && v.RolledStatus != v.Status {
		fmt.Fprintf(&b, " (rolled: `%s`)", v.RolledStatus)
	}
	if r := roll[v.ID]; r[1] > 0 {
		fmt.Fprintf(&b, " · **Rollup**: %d/%d complete", r[0], r[1])
	}
	b.WriteByte('\n')
	if v.StatusDescription != "" {
		fmt.Fprintf(&b, "- **Meaning**: %s\n", v.StatusDescription)
	}
	if v.StatusReason != "" {
		fmt.Fprintf(&b, "- **Reason**: %s\n", v.StatusReason)
	}
	if v.EffectiveProject != "" {
		fmt.Fprintf(&b, "- **Project**: `%s` (%s)\n", v.EffectiveProject, v.ProjectAssociation)
	}
	if v.Parent != "" {
		fmt.Fprintf(&b, "- **Parent**: `%s`\n", v.Parent)
	}
	if v.Plan != "" {
		fmt.Fprintf(&b, "- **Plan**: `%s`\n", v.Plan)
	}
	if v.Repo != "" {
		fmt.Fprintf(&b, "- **Repo**: `%s`", v.Repo)
		if v.Branch != "" {
			fmt.Fprintf(&b, " · **Branch**: `%s`", v.Branch)
		}
		b.WriteByte('\n')
	}
	if v.Estimate != "" {
		fmt.Fprintf(&b, "- **Estimate**: %s\n", v.Estimate)
	}
	if v.Started != nil {
		fmt.Fprintf(&b, "- **Started**: %s\n", shortTimeRFC(*v.Started))
	}
	if v.Finished != nil {
		fmt.Fprintf(&b, "- **Finished**: %s\n", shortTimeRFC(*v.Finished))
	}
	if v.Stale {
		fmt.Fprintf(&b, "\n> **Stale**: %s\n", v.StaleReason)
	}
	if v.Wording != "" {
		fmt.Fprintf(&b, "\n## Owner Wording\n\n%s\n", v.Wording)
	}
	if v.Scope != "" {
		fmt.Fprintf(&b, "\n## Scope\n\n%s\n", v.Scope)
	}
	if v.DoneWhen != "" {
		fmt.Fprintf(&b, "\n## Done When\n\n%s\n", v.DoneWhen)
	}
	if v.Tests != "" {
		fmt.Fprintf(&b, "\n## Tests\n\n%s\n", v.Tests)
	}
	// Children summary
	var children []Lane
	for _, r := range rows {
		if r.Parent == v.ID && !r.Deleted {
			children = append(children, r)
		}
	}
	if len(children) > 0 {
		b.WriteString("\n## Sub-lanes\n\n")
		for _, ch := range children {
			fmt.Fprintf(&b, "- `%s` **%s** (`%s`)\n", ch.ID, ch.Name, ch.Status)
		}
	}
	if len(v.Panes) > 0 {
		b.WriteString("\n## Participants\n\n")
		for _, p := range v.Panes {
			label := p.Window
			if label == "" {
				label = p.AgentID
			}
			if label == "" {
				label = p.Harness
			}
			if p.Session != "" {
				label = p.Session + ":" + label
			}
			fmt.Fprintf(&b, "- **%s**: `%s`", p.Role, label)
			if p.Model != "" {
				fmt.Fprintf(&b, " (%s)", p.Model)
			}
			b.WriteByte('\n')
		}
	}
	if len(v.Links) > 0 {
		b.WriteString("\n## Links\n\n")
		for _, l := range v.Links {
			fmt.Fprintf(&b, "- **%s**: `%s`", l.Kind, l.Target)
			if l.Label != "" {
				fmt.Fprintf(&b, " — %s", l.Label)
			}
			b.WriteByte('\n')
		}
	}
	hist := activeHistory(v.History)
	if len(hist) > 0 {
		b.WriteString("\n## Notes & History\n\n")
		for i := len(hist) - 1; i >= 0; i-- {
			e := hist[i]
			ts := shortTimeRFC(e.At)
			who := e.By.Label()
			switch e.Kind {
			case "status":
				fmt.Fprintf(&b, "- `%s` **%s** status `%s` → `%s`", ts, who, e.From, e.To)
				if e.Text != "" {
					fmt.Fprintf(&b, " — %s", e.Text)
				}
				b.WriteByte('\n')
			case "note", reasonProp:
				fmt.Fprintf(&b, "- `%s` **%s** note: %s\n", ts, who, e.Text)
			default:
				line := fmt.Sprintf("- `%s` **%s** %s", ts, who, e.Kind)
				if e.To != "" {
					line += " `" + e.To + "`"
				}
				if e.Text != "" {
					line += " — " + e.Text
				}
				b.WriteString(line + "\n")
			}
		}
	}
	return b.String()
}

func renderPlanMarkdown(ps PlanSummary, lanes []View) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", ps.Name)
	fmt.Fprintf(&b, "- **Path**: `%s`\n", ps.Path)
	fmt.Fprintf(&b, "- **Progress**: %d/%d done (%d open)\n", ps.DoneCount, ps.LaneCount, ps.OpenCount)
	if len(ps.Projects) > 0 {
		fmt.Fprintf(&b, "- **Projects**: %s\n", strings.Join(ps.Projects, ", "))
	}
	if len(lanes) > 0 {
		b.WriteString("\n## Lanes\n\n")
		for _, l := range lanes {
			indent := ""
			if l.Parent != "" {
				indent = "  "
			}
			fmt.Fprintf(&b, "%s- `%s` **%s** (`%s`)\n", indent, l.ID, l.Name, l.Status)
		}
	}
	return b.String()
}

// treeOrder orders lanes depth-first with their indentation level.
type orderedLane struct {
	Lane  Lane
	Depth int
}

func treeOrder(rows []Lane) []orderedLane {
	byID := map[string]Lane{}
	children := map[string][]Lane{}
	var roots []Lane
	for _, r := range rows {
		byID[r.ID] = r
	}
	for _, r := range rows {
		if r.Parent == "" {
			roots = append(roots, r)
		} else if _, ok := byID[r.Parent]; ok {
			children[r.Parent] = append(children[r.Parent], r)
		} else {
			roots = append(roots, r)
		}
	}
	var out []orderedLane
	seen := map[string]bool{}
	var walk func(Lane, int)
	walk = func(q Lane, depth int) {
		if seen[q.ID] {
			return
		}
		seen[q.ID] = true
		out = append(out, orderedLane{Lane: q, Depth: depth})
		for _, c := range children[q.ID] {
			walk(c, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	for _, r := range rows {
		if !seen[r.ID] {
			walk(r, 0)
		}
	}
	return out
}

func envOr(k string) string { return os.Getenv(k) }

func containsCI(hay, needle string) bool {
	return slices.ContainsFunc(strings.Split(hay, "\x00"), func(s string) bool {
		return strings.Contains(strings.ToLower(s), strings.ToLower(needle))
	})
}
