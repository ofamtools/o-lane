package main

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	otea "ofam.dev/ochazuke/tea"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/markdown"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
)

const (
	pageID       = "main"
	nodeList     = "list"
	nodeDetail   = "detail"
	nodeFilter   = "filter"
	modalPalette = "palette"
	modalHelp    = "help"
	modalForm    = "form"
	modalChoose  = "choose"
)

const (
	tabLanes = "lanes"
	tabPlans = "plans"
)

const (
	cellName    = "name"
	cellStatus  = "status"
	cellProject = "project"
	cellPlan    = "plan"
	cellPanes   = "panes"
	cellUpdated = "updated"
	cellLanes   = "lanes"
	cellDone    = "done"
	cellOpen    = "open"
)

var laneSortKeys = []string{"", cellName, cellStatus, cellProject, cellPlan, cellUpdated}
var planSortKeys = []string{"", cellName, cellLanes, cellDone, cellOpen, cellUpdated}
var laneGroupKeys = []string{"", cellStatus, cellProject, cellPlan}

var cellNames = map[string]string{
	"":          "tree order",
	cellName:    "name",
	cellStatus:  "status",
	cellProject: "project",
	cellPlan:    "plan",
	cellPanes:   "panes",
	cellUpdated: "updated",
	cellLanes:   "lanes",
	cellDone:    "done",
	cellOpen:    "open",
}

// App is the page state kept by value in the page frame's Data.
type App struct {
	Tab   string // tabLanes or tabPlans
	Lanes []Lane
	Views map[string]View
	Roll  map[string][2]int
	Plans []PlanSummary
	base  []table.Row
	List  table.Model

	Query     string
	QueryLine input.Line
	Match     table.MatchMode
	FilterErr string
	Typing    bool

	StatusFilter  string
	ProjectFilter string
	ShowArchived  bool

	Detail   markdown.Model
	DetailID string

	Focus string
	Ratio float64

	Status, Tone string
}

func (a App) clone() App {
	a.List = a.List.Copy()
	a.QueryLine.Text = slices.Clone(a.QueryLine.Text)
	return a
}

func appOf(s core.State) App {
	a, _ := s.Pages[pageID].Data.(App)
	return a
}

func withApp(s core.State, a App) core.State {
	f := s.Pages[pageID]
	f.Data = a
	f.FocusID = a.Focus
	if a.Typing {
		f.FocusID = nodeFilter
	}
	s.Pages = maps.Clone(s.Pages)
	s.Pages[pageID] = f
	return s
}

func (a *App) note(msg string) { a.Status, a.Tone = msg, "success" }
func (a *App) info(msg string) { a.Status, a.Tone = msg, "info" }
func (a *App) warn(msg string) { a.Status, a.Tone = msg, "warning" }
func (a *App) fail(msg string) { a.Status, a.Tone = msg, "error" }

// Env is the immutable session configuration shared by handlers and the view.
type Env struct {
	St           *Store
	Km           *core.Keymap
	Bindings     []core.Binding
	Specs        []core.CommandSpec
	Theme        ui.Theme
	Match        table.MatchMode
	Ratio        float64
	StackW       int
	HideArchived bool
	RollupCounts bool
	Notes        []string
}

type Config struct {
	Bindings []core.Binding
	Theme    *ui.Theme
	Settings *config.Settings
	Notes    []string
}

var modes = []core.Mode{{Name: "normal"}, {Name: "input"}, {Name: "palette"}, {Name: "help"}, {Name: "form"}, {Name: "choose"}}

func newEnv(st *Store, cfg Config) *Env {
	env := &Env{St: st, Theme: ui.DefaultDarkTheme(), Notes: cfg.Notes}
	if cfg.Theme != nil {
		env.Theme = *cfg.Theme
	}
	set := cfg.Settings
	if set == nil {
		set, _ = config.LoadSettings(nil, defaultSettings)
	}
	switch m := table.MatchMode(set.String("matchMode", "substring")); m {
	case table.MatchSubstring, table.MatchFuzzy, table.MatchRegex:
		env.Match = m
	default:
		env.Match = table.MatchSubstring
		env.Notes = append(env.Notes, "settings: matchMode must be substring, fuzzy or regex")
	}
	env.Ratio = min(max(set.Float("listRatio", 0.55), 0.2), 0.8)
	env.StackW = set.Int("stackWidth", 100)
	env.HideArchived = set.Bool("lanes.hide-archived", true)
	env.RollupCounts = set.Bool("lanes.rollup-counts", true)
	env.Specs = commandSpecs()
	for _, d := range config.CheckBindings(modes, cfg.Bindings, env.Specs...) {
		if d.Severity == config.SevError {
			env.Notes = append(env.Notes, fmt.Sprintf("bindings.json #%d: %s", d.Index, d.Message))
		}
	}
	env.Bindings = config.MergeBindings(defaultBindings(), cfg.Bindings)
	km, err := core.NewKeymap(modes, env.Bindings, env.Specs...)
	if err != nil && len(env.Notes) == 0 {
		env.Notes = append(env.Notes, "bindings: "+strings.ReplaceAll(err.Error(), "\n", "; "))
	}
	env.Km = km
	return env
}

// --- commands and bindings ------------------------------------------------------

func commandSpecs() []core.CommandSpec {
	user := func(id, desc string) core.CommandSpec {
		return core.CommandSpec{ID: id, Description: desc, Tags: []string{"user"}}
	}
	internal := func(id, desc string) core.CommandSpec { return core.CommandSpec{ID: id, Description: desc} }
	specs := []core.CommandSpec{
		user("view.lanes", "Switch to the Lanes table"),
		user("view.plans", "Switch to the Plans table"),
		user("filter.open", "Filter rows (name, status, project, plan)"),
		user("filter.mode.cycle", "Cycle filter match mode: substring, fuzzy, regex"),
		user("filter.clear", "Clear active filter"),
		user("sort.cycle", "Sort by the next column"),
		user("sort.reverse", "Reverse sort direction"),
		user("group.cycle", "Group lanes by status, project, plan, or none"),
		user("group.collapse", "Collapse or expand the focused group"),
		user("filter.status.pick", "Filter lanes by lifecycle status"),
		user("filter.project.pick", "Filter lanes by project"),
		user("archived.toggle", "Show or hide archived lanes"),
		user("list.reload", "Reload lanes from disk"),
		user("focus.toggle", "Switch focus between the table and the detail pane"),
		user("focus.detail", "Focus the detail pane"),
		user("focus.list", "Focus the table"),
		user("lane.define", "Define a new lane"),
		user("lane.state.pick", "Change focused lane status (picker)"),
		user("lane.state.custom", "Set focused lane status (free-form, any status)"),
		user("lane.note", "Add a note to the focused lane"),
		user("lane.associate", "Associate a project or pane with the focused lane"),
		user("lane.archive", "Archive or restore the focused lane"),
		user("lane.undo", "Undo the last store mutation"),
		user("lane.redo", "Redo the last undone store mutation"),
		user("palette.open", "Open the command palette"),
		user("help.open", "Show active key bindings"),
		user("app.close", "Quit"),

		internal("input.insert", "Type into the filter"),
		internal("input.backspace", "Delete the last filter character"),
		internal("input.accept", "Keep the filter and return to the table"),
		internal("input.cancel", "Clear the filter and return to the table"),
		internal("palette.insert", "Type into the palette query"),
		internal("palette.backspace", "Delete the last query character"),
		internal("palette.close", "Close the palette"),
		internal("choose.insert", "Type into the picker query"),
		internal("choose.backspace", "Delete the last query character"),
		internal("choose.close", "Close the picker"),
		internal(picker.CmdConfirm, "Choose the highlighted item"),
		internal(picker.CmdModeCycle, "Cycle the match mode"),
		internal("help.close", "Close help"),
		internal(form.CmdInsert, "Type into the field"),
		internal(form.CmdBackspace, "Delete the last character"),
		internal("form.clear", "Clear the field"),
		internal(form.CmdSubmit, "Apply the form"),
		internal(form.CmdCancel, "Discard the form"),
	}
	for _, sp := range markdown.Specs() {
		sp.Tags = append(slices.Clone(sp.Tags), "user")
		specs = append(specs, sp)
	}
	return append(specs, input.ReadlineSpecs()...)
}

func b(mode, keys, cmd, hint string) core.Binding {
	return core.Binding{Mode: mode, Keys: strings.Fields(keys), Command: cmd, Hint: hint}
}

func sb(scope, mode, keys, cmd, hint string) core.Binding {
	bd := b(mode, keys, cmd, hint)
	bd.Scope = scope
	return bd
}

func class(mode, cls, cmd string, args map[string]any, hint string) core.Binding {
	return core.Binding{Mode: mode, Class: cls, Command: cmd, Args: args, Hint: hint}
}

var unusedMotions = []string{"motion.word.next", "motion.word.prev", "motion.word.end", "motion.group.next", "motion.group.prev", "motion.left", "motion.right", "motion.start", "motion.end"}

func standard(mode string) []core.Binding {
	var out []core.Binding
	for _, bd := range core.StandardBindings(mode) {
		if !slices.Contains(unusedMotions, bd.Command) {
			out = append(out, bd)
		}
	}
	return out
}

func defaultBindings() []core.Binding {
	text := map[string]any{"text": "$text"}
	out := []core.Binding{
		b("normal", "1", "view.lanes", "lanes tab"),
		b("normal", "2", "view.plans", "plans tab"),
		b("normal", "/", "filter.open", "filter"),
		b("normal", "alt+m", "filter.mode.cycle", "match mode"),
		sb("list", "normal", "esc", "filter.clear", "clear filter"),
		b("normal", "s", "sort.cycle", "sort"),
		b("normal", "S", "sort.reverse", "reverse sort"),
		b("normal", "g b", "group.cycle", "group"),
		b("normal", "z a", "group.collapse", "fold group"),
		b("normal", "g s", "filter.status.pick", "status filter"),
		b("normal", "g p", "filter.project.pick", "project filter"),
		b("normal", ".", "archived.toggle", "archived"),
		b("normal", "R", "list.reload", "reload"),
		b("normal", "tab", "focus.toggle", "table / detail"),
		sb("list", "normal", "enter", "focus.detail", "detail"),
		sb("list", "normal", "l", "focus.detail", "detail"),
		sb("markdown", "normal", "esc", "focus.list", "back to table"),
		sb("markdown", "normal", "h", "focus.list", "back to table"),
		b("normal", "t", markdown.CmdTOCToggle, "contents"),
		b("normal", "]", "motion.block.next", "next heading"),
		b("normal", "[", "motion.block.prev", "previous heading"),
		b("normal", "a", "lane.define", "define lane"),
		b("normal", "c", "lane.state.pick", "set status"),
		b("normal", "C", "lane.state.custom", "custom status"),
		b("normal", "n", "lane.note", "add note"),
		b("normal", "p", "lane.associate", "associate"),
		b("normal", "D", "lane.archive", "archive / restore"),
		b("normal", "u", "lane.undo", "undo"),
		b("normal", "U", "lane.redo", "redo"),
		b("normal", "ctrl+r", "lane.redo", "redo"),
		b("normal", ":", "palette.open", "commands"),
		b("normal", "?", "help.open", "help"),
		b("normal", "q", "app.close", "quit"),
		b("normal", "ctrl+c", "app.close", "quit"),
	}
	out = append(out, standard("normal")...)
	nav := func(mode, next string) []core.Binding {
		return []core.Binding{
			b(mode, "down", "motion.down", next), b(mode, "ctrl+n", "motion.down", next),
			b(mode, "up", "motion.up", "previous"), b(mode, "ctrl+p", "motion.up", "previous"),
			b(mode, "pgdown", "motion.page.down", "page down"), b(mode, "pgup", "motion.page.up", "page up"),
		}
	}
	out = append(out,
		class("input", core.ClassPrintable, "input.insert", text, "type"),
		class("input", core.ClassPaste, "input.insert", text, "paste"),
		b("input", "backspace", "input.backspace", "delete"),
		b("input", "enter", "input.accept", "keep filter"),
		b("input", "esc", "input.cancel", "clear filter"),
		b("input", "ctrl+c", "input.cancel", "clear filter"),
		b("input", "alt+m", "filter.mode.cycle", "match mode"),
		b("input", "tab", "filter.mode.cycle", "match mode"),
	)
	out = append(out, nav("input", "next row")...)
	for _, m := range []string{"palette", "choose"} {
		out = append(out,
			class(m, core.ClassPrintable, m+".insert", text, "type"),
			class(m, core.ClassPaste, m+".insert", text, "paste"),
			b(m, "backspace", m+".backspace", "delete"),
			b(m, "enter", picker.CmdConfirm, "choose"),
			b(m, "esc", m+".close", "close"),
			b(m, "ctrl+c", m+".close", "close"),
			b(m, "alt+m", picker.CmdModeCycle, "match mode"),
			b(m, "tab", picker.CmdModeCycle, "match mode"),
		)
		out = append(out, nav(m, "next")...)
	}
	out = append(out,
		class("form", core.ClassPrintable, form.CmdInsert, text, "type"),
		class("form", core.ClassPaste, form.CmdInsert, text, "paste"),
		b("form", "backspace", form.CmdBackspace, "delete"),
		b("form", "ctrl+u", "form.clear", "clear field"),
		b("form", "tab", "motion.down", "next field"),
		b("form", "down", "motion.down", "next field"),
		b("form", "shift+tab", "motion.up", "previous field"),
		b("form", "up", "motion.up", "previous field"),
		b("form", "enter", form.CmdSubmit, "apply"),
		b("form", "esc", form.CmdCancel, "cancel"),
		b("form", "ctrl+c", form.CmdCancel, "cancel"),

		b("help", "esc", "help.close", "close"),
		b("help", "q", "help.close", "close"),
		b("help", "?", "help.close", "close"),
		b("help", "ctrl+c", "help.close", "close"),
	)
	// Readline editing in every text line; a mode's own rows win.
	for _, mode := range []string{"input", "palette", "choose", "form"} {
		taken := map[string]bool{}
		for _, bd := range out {
			if bd.Mode == mode && len(bd.Keys) > 0 {
				taken[strings.Join(bd.Keys, " ")] = true
			}
		}
		for _, rb := range input.ReadlineBindings(mode, "") {
			k := strings.Join(rb.Keys, " ")
			if taken[k] {
				continue
			}
			taken[k] = true
			out = append(out, rb)
		}
	}
	return append(out, standard("help")...)
}

// --- columns & projection -------------------------------------------------------

func laneColumns() []table.Column {
	return []table.Column{
		{Key: cellName, Title: "Lane"},
		{Key: cellStatus, Title: "Status", Width: 16},
		{Key: cellProject, Title: "Project", Width: 14},
		{Key: cellPlan, Title: "Plan", Width: 18},
		{Key: cellPanes, Title: "Panes", Width: 5, Align: "right"},
		{Key: cellUpdated, Title: "Updated", Width: 16},
	}
}

func planColumns() []table.Column {
	return []table.Column{
		{Key: cellName, Title: "Plan"},
		{Key: cellLanes, Title: "Lanes", Width: 6, Align: "right"},
		{Key: cellDone, Title: "Done", Width: 6, Align: "right"},
		{Key: cellOpen, Title: "Open", Width: 6, Align: "right"},
		{Key: cellProject, Title: "Projects", Width: 18},
		{Key: cellUpdated, Title: "Updated", Width: 16},
	}
}

func (env *Env) reload(a *App, focusID string) {
	keep := focusID
	if keep == "" {
		keep, _ = a.focusedID()
	}
	rows, err := env.St.List()
	if err != nil {
		a.fail("store: " + err.Error())
		return
	}
	a.Lanes = rows
	a.Roll = env.St.rollups(rows)
	a.Views = make(map[string]View, len(rows))
	for _, q := range rows {
		a.Views[q.ID] = env.St.view(q, rows)
	}
	a.Plans = listPlans(env.St, rows)
	env.rebuildBase(a)
	a.reproject()
	if keep != "" {
		a.focusRow(keep)
	}
	a.DetailID = ""
	env.syncDetail(a)
}

func (env *Env) rebuildBase(a *App) {
	if a.Tab == tabPlans {
		a.List.Columns = planColumns()
		base := make([]table.Row, len(a.Plans))
		for i, ps := range a.Plans {
			base[i] = table.Row{
				ID:  ps.ID,
				Ref: &core.Ref{Kind: planKind, ID: ps.ID},
				Cells: map[string]any{
					cellName:    ps.Name,
					cellLanes:   ps.LaneCount,
					cellDone:    ps.DoneCount,
					cellOpen:    ps.OpenCount,
					cellProject: strings.Join(ps.Projects, ", "),
					cellUpdated: ps.Updated,
				},
			}
		}
		a.base = base
		return
	}

	a.List.Columns = laneColumns()
	var named []string
	if a.StatusFilter != "" {
		named = []string{a.StatusFilter}
	}
	hidden := env.St.archiveHidden(a.Lanes, named, env.HideArchived && !a.ShowArchived)
	ordered := treeOrder(a.Lanes)
	base := make([]table.Row, 0, len(ordered))
	for _, ol := range ordered {
		q := ol.Lane
		if hidden[q.ID] {
			continue
		}
		v := a.Views[q.ID]
		if a.StatusFilter != "" && !strings.EqualFold(q.Status, a.StatusFilter) {
			continue
		}
		if a.ProjectFilter != "" && !strings.EqualFold(v.EffectiveProject, a.ProjectFilter) && !projectMatches(v.EffectiveProjectID, a.ProjectFilter) {
			continue
		}
		name := q.Name
		if ol.Depth > 0 {
			name = strings.Repeat("  ", ol.Depth) + "└ " + name
		}
		stText := q.Status
		if env.RollupCounts {
			if r := a.Roll[q.ID]; r[1] > 0 && r[0] < r[1] {
				stText = fmt.Sprintf("%s %d/%d", stText, r[0], r[1])
			}
		}
		planName := ""
		if q.Plan != "" {
			planName = strings.TrimSuffix(filepath.Base(q.Plan), filepath.Ext(q.Plan))
		}
		panes := any("")
		if len(q.Panes) > 0 {
			panes = len(q.Panes)
		}
		base = append(base, table.Row{
			ID:  q.ID,
			Ref: &core.Ref{Kind: laneKind, ID: q.ID},
			Cells: map[string]any{
				cellName:    name,
				cellStatus:  stText,
				cellProject: v.EffectiveProject,
				cellPlan:    planName,
				cellPanes:   panes,
				cellUpdated: shortTimeRFC(q.Updated),
			},
		})
	}
	a.base = base
}

func (a *App) reproject() {
	keep, onRow := a.focusedID()
	a.List.Rows = a.base
	a.List.Query, a.List.MatchMode = a.Query, a.Match
	a.List.Items()
	a.FilterErr = a.List.FilterErr
	if onRow {
		a.focusRow(keep)
	}
	a.List.Focused()
}

func (a App) filteredCount() int {
	m := a.List.Copy()
	return len(m.Filtered())
}

func (a *App) focusedID() (string, bool) {
	r, ok := a.List.FocusedRow()
	return r.ID, ok
}

func (a *App) focusedLane() (Lane, bool) {
	if a.Tab != tabLanes {
		return Lane{}, false
	}
	id, ok := a.focusedID()
	if !ok {
		return Lane{}, false
	}
	for _, q := range a.Lanes {
		if q.ID == id {
			return q, true
		}
	}
	return Lane{}, false
}

func (a *App) focusedPlan() (PlanSummary, bool) {
	if a.Tab != tabPlans {
		return PlanSummary{}, false
	}
	id, ok := a.focusedID()
	if !ok {
		return PlanSummary{}, false
	}
	for _, ps := range a.Plans {
		if ps.ID == id {
			return ps, true
		}
	}
	return PlanSummary{}, false
}

func (a *App) focusRow(id string) bool {
	if id == "" {
		return false
	}
	for i, it := range a.List.Items() {
		if !it.Header && it.Row.ID == id {
			a.List.SelectedIdx = i
			a.List.Focused()
			return true
		}
	}
	return false
}

// --- engine & handlers ----------------------------------------------------------

type fn func(s core.State, a *App, c core.Command) (core.State, []core.Effect)

func (env *Env) wrap(f fn) core.Handler {
	return func(s core.State, c core.Command) core.StepResult {
		a := appOf(s).clone()
		a.Status, a.Tone = "", ""
		env.size(&a, s)
		s, effects := f(s, &a, c)
		env.syncDetail(&a)
		env.size(&a, s)
		return core.Next(withApp(s, a), effects...)
	}
}

func (env *Env) size(a *App, s core.State) {
	g := env.geometry(a, s.Width, s.Height)
	a.List.PageSize = g.listRows
	a.Detail.PageSize = g.detailRows
}

func (env *Env) syncDetail(a *App) {
	id, ok := a.focusedID()
	if !ok {
		if a.DetailID != "" || a.Detail.Title == "" {
			a.DetailID = ""
			a.Detail = *markdown.New("Detail", "*No item selected.*")
		}
		return
	}
	key := a.Tab + ":" + id
	if key == a.DetailID {
		return
	}
	ps := a.Detail.PageSize
	toc := a.Detail.ShowTOC
	if a.Tab == tabPlans {
		if p, ok := a.focusedPlan(); ok {
			var pl []View
			for _, q := range a.Lanes {
				if q.Plan == p.Path {
					pl = append(pl, a.Views[q.ID])
				}
			}
			a.Detail = *markdown.New(p.Name, renderPlanMarkdown(p, pl))
		}
	} else {
		if q, ok := a.focusedLane(); ok {
			a.Detail = *markdown.New(q.Name, renderLaneMarkdown(env.St, a.Views[q.ID], a.Lanes, a.Roll))
		}
	}
	a.Detail.PageSize, a.Detail.ShowTOC = ps, toc
	a.DetailID = key
}

func setMode(s core.State, mode string) core.State {
	f := s.Pages[pageID]
	f.Mode = mode
	s.Pages = maps.Clone(s.Pages)
	s.Pages[pageID] = f
	return s
}

func openModal(s core.State, id, mode string, data any) core.State {
	s = closeModal(s, id)
	s.Modals = append(slices.Clone(s.Modals), core.Frame{ID: id, Kind: core.FrameModal, Mode: mode, FocusID: id, Data: data})
	return s
}

func closeModal(s core.State, id string) core.State {
	if i := slices.IndexFunc(s.Modals, func(f core.Frame) bool { return f.ID == id }); i >= 0 {
		s.Modals = slices.Delete(slices.Clone(s.Modals), i, i+1)
	}
	return s
}

func modalData(s core.State, id string) any {
	if i := slices.IndexFunc(s.Modals, func(f core.Frame) bool { return f.ID == id }); i >= 0 {
		return s.Modals[i].Data
	}
	return nil
}

func setModalData(s core.State, id string, data any) core.State {
	if i := slices.IndexFunc(s.Modals, func(f core.Frame) bool { return f.ID == id }); i >= 0 {
		s.Modals = slices.Clone(s.Modals)
		s.Modals[i].Data = data
	}
	return s
}

func dispatch(c core.Command) core.Effect { return core.CustomEffect{Kind: "dispatch", Data: c} }

var motions = []string{"motion.down", "motion.up", "motion.top", "motion.bottom", "motion.page.down", "motion.page.up", "motion.half.down", "motion.half.up", "motion.block.next", "motion.block.prev"}

func newEngine(env *Env) *core.Engine {
	e := core.NewEngine(env.Km)
	h := func(id string, f fn) { e.Handle(id, env.wrap(f)) }
	scoped := func(scope, id string, f fn) { e.HandleScope(scope, id, env.wrap(f)) }

	for _, id := range motions {
		scoped("list", id, env.listMotion)
		scoped("markdown", id, env.detailCmd)
		scoped("palette", id, env.pickCmd(modalPalette))
		scoped("choose", id, env.pickCmd(modalChoose))
		scoped("help", id, env.helpScroll)
		scoped("form", id, env.formCmd)
	}
	for _, sp := range markdown.Specs() {
		h(sp.ID, env.detailCmd)
	}

	h("view.lanes", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if a.Tab != tabLanes {
			a.Tab = tabLanes
			a.List.SortField, a.List.SortDir, a.List.GroupField = "", table.SortNone, ""
			a.List.SelectedIdx = 0
			env.rebuildBase(a)
			a.reproject()
			a.DetailID = ""
		}
		a.Focus = nodeList
		a.info("lanes view")
		return s, nil
	})
	h("view.plans", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if a.Tab != tabPlans {
			a.Tab = tabPlans
			a.List.SortField, a.List.SortDir, a.List.GroupField = "", table.SortNone, ""
			a.List.SelectedIdx = 0
			env.rebuildBase(a)
			a.reproject()
			a.DetailID = ""
		}
		a.Focus = nodeList
		a.info("plans view")
		return s, nil
	})

	h("filter.open", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		a.Typing, a.Focus = true, nodeList
		return setMode(s, "input"), nil
	})
	h("filter.mode.cycle", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		m := table.Model{MatchMode: a.Match}
		m.CycleMatchMode()
		a.Match = m.MatchMode
		a.reproject()
		a.info("match mode: " + string(a.Match))
		return s, nil
	})
	h("filter.clear", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if a.Query == "" && a.StatusFilter == "" && a.ProjectFilter == "" {
			return s, nil
		}
		a.Query, a.StatusFilter, a.ProjectFilter = "", "", ""
		env.rebuildBase(a)
		a.reproject()
		a.info("filters cleared")
		return s, nil
	})
	lineEdit := func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		a.QueryLine.Sync(a.Query)
		var changed bool
		switch c.ID {
		case "input.insert":
			changed = a.QueryLine.Insert(c.Arg("text"))
		default:
			changed, _ = a.QueryLine.HandleCommand(c)
		}
		if changed {
			a.Query = a.QueryLine.Value()
			a.reproject()
		}
		return s, nil
	}
	h("input.insert", lineEdit)
	for _, id := range input.ReadlineCommandIDs() {
		h(id, lineEdit)
	}
	h("input.accept", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		a.Typing = false
		return setMode(s, "normal"), nil
	})
	h("input.cancel", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		a.Typing, a.Query = false, ""
		a.reproject()
		return setMode(s, "normal"), nil
	})

	h("sort.cycle", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		keys := laneSortKeys
		if a.Tab == tabPlans {
			keys = planSortKeys
		}
		i := (slices.Index(keys, a.List.SortField) + 1) % len(keys)
		a.List.SortField = keys[i]
		a.List.SortDir = table.SortAsc
		if a.List.SortField == "" {
			a.List.SortDir = table.SortNone
		}
		a.reproject()
		a.info(sortLabel(a))
		return s, nil
	})
	h("sort.reverse", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if a.List.SortField == "" {
			a.List.SortField, a.List.SortDir = cellUpdated, table.SortDesc
		} else if a.List.SortDir == table.SortAsc {
			a.List.SortDir = table.SortDesc
		} else {
			a.List.SortDir = table.SortAsc
		}
		a.reproject()
		a.info(sortLabel(a))
		return s, nil
	})
	h("group.cycle", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if a.Tab != tabLanes {
			a.info("grouping applies to the lanes table")
			return s, nil
		}
		keep, onRow := a.focusedID()
		a.List.GroupField = laneGroupKeys[(slices.Index(laneGroupKeys, a.List.GroupField)+1)%len(laneGroupKeys)]
		a.List.Collapsed = nil
		if a.List.GroupField == "" {
			a.info("not grouped")
		} else {
			a.info("grouped by " + cellNames[a.List.GroupField])
		}
		if !onRow || !a.focusRow(keep) {
			a.List.SelectedIdx = 0
		}
		a.List.Focused()
		return s, nil
	})
	h("group.collapse", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if a.List.GroupField == "" {
			a.warn("not grouped — g b groups the list")
			return s, nil
		}
		a.List.ToggleGroup()
		return s, nil
	})
	h("filter.status.pick", env.statusFilterPick)
	h("filter.project.pick", env.projectFilterPick)
	h("archived.toggle", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		a.ShowArchived = !a.ShowArchived
		env.rebuildBase(a)
		a.reproject()
		if a.ShowArchived {
			a.info("showing archived lanes")
		} else {
			a.info("archived lanes hidden")
		}
		return s, nil
	})
	h("list.reload", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		env.reload(a, "")
		a.note(fmt.Sprintf("%d lanes · %d plans", len(a.Lanes), len(a.Plans)))
		return s, nil
	})
	h("focus.toggle", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if a.Focus == nodeDetail {
			a.Focus = nodeList
		} else {
			a.Focus = nodeDetail
		}
		return s, nil
	})
	h("focus.detail", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if _, ok := a.focusedID(); !ok {
			a.warn("nothing under the cursor")
			return s, nil
		}
		a.Focus = nodeDetail
		return s, nil
	})
	h("focus.list", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		a.Focus = nodeList
		return s, nil
	})

	// Domain mutations
	h("lane.define", env.laneForm("define"))
	h("lane.state.pick", env.statePick)
	h("lane.state.custom", env.laneForm("state"))
	h("lane.note", env.laneForm("note"))
	h("lane.associate", env.laneForm("associate"))
	h("lane.archive", env.laneArchive)
	h("lane.undo", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if err := env.St.Undo(); err != nil {
			a.warn(err.Error())
			return s, nil
		}
		env.reload(a, "")
		a.note("undone")
		return s, nil
	})
	h("lane.redo", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		if err := env.St.Redo(); err != nil {
			a.warn(err.Error())
			return s, nil
		}
		env.reload(a, "")
		a.note("redone")
		return s, nil
	})

	for _, id := range append([]string{form.CmdInsert, form.CmdBackspace, "form.clear", form.CmdSubmit, form.CmdCancel}, input.ReadlineCommandIDs()...) {
		scoped("form", id, env.formCmd)
	}
	h("palette.open", env.paletteOpen)
	for _, id := range append([]string{"palette.insert", "palette.backspace", "palette.close", picker.CmdConfirm, picker.CmdModeCycle}, input.ReadlineCommandIDs()...) {
		scoped("palette", id, env.pickCmd(modalPalette))
	}
	for _, id := range append([]string{"choose.insert", "choose.backspace", "choose.close", picker.CmdConfirm, picker.CmdModeCycle}, input.ReadlineCommandIDs()...) {
		scoped("choose", id, env.pickCmd(modalChoose))
	}
	h("help.open", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		return openModal(s, modalHelp, "help", 0), nil
	})
	h("help.close", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		return closeModal(s, modalHelp), nil
	})
	h("app.close", func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		return s, []core.Effect{core.QuitEffect{}}
	})
	return e
}

func sortLabel(a *App) string {
	if a.List.SortField == "" || a.List.SortDir == table.SortNone {
		return "tree order"
	}
	dir := "ascending"
	if a.List.SortDir == table.SortDesc {
		dir = "descending"
	}
	return "sorted by " + cellNames[a.List.SortField] + " " + dir
}

func (env *Env) listMotion(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	if (c.ID == "motion.bottom" || c.ID == "motion.top") && c.Count > 0 {
		a.List.SelectedIdx = c.Count - 1
		a.List.Focused()
		return s, nil
	}
	a.List.HandleCommand(c)
	return s, nil
}

func (env *Env) detailCmd(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	a.Detail.HandleCommand(c)
	return s, nil
}

func (env *Env) helpScroll(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	off, _ := modalData(s, modalHelp).(int)
	lines := len(env.helpLines())
	step := max(c.Count, 1)
	switch c.ID {
	case "motion.down":
		off += step
	case "motion.up":
		off -= step
	case "motion.page.down", "motion.half.down":
		off += 10
	case "motion.page.up", "motion.half.up":
		off -= 10
	case "motion.top":
		off = 0
	case "motion.bottom":
		off = max(lines-10, 0)
	}
	off = min(max(off, 0), max(lines-1, 0))
	return setModalData(s, modalHelp, off), nil
}

// --- forms & pickers ------------------------------------------------------------

type laneFormData struct {
	M      *form.Model
	Action string // define, state, note, associate
	LaneID string
}

func (f laneFormData) clone() laneFormData {
	m := *f.M
	m.Values = maps.Clone(m.Values)
	m.Errors = maps.Clone(m.Errors)
	f.M = &m
	return f
}

func (env *Env) laneForm(action string) fn {
	return func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		q, hasLane := a.focusedLane()
		str := func(name, desc string, req bool) core.ParamSpec {
			return core.ParamSpec{Name: name, Type: core.ParamString, Required: req, Description: desc}
		}
		var m *form.Model
		switch action {
		case "define":
			parent := ""
			proj := a.ProjectFilter
			if hasLane {
				parent = q.ID
				if proj == "" {
					proj = a.Views[q.ID].EffectiveProject
				}
			}
			m = form.New("Define lane", []core.ParamSpec{
				str("name", "lane name", false),
				str("parent", "parent lane ID (blank for root)", false),
				str("project", "project reference", false),
				str("repo", "repository", false),
				str("estimate", "estimate (e.g. 2h)", false),
				str("scope", "scope summary", false),
			}, map[string]any{"name": "", "parent": parent, "project": proj, "repo": "", "estimate": "", "scope": ""})
		case "state":
			if !hasLane {
				a.warn("focus a lane first")
				return s, nil
			}
			m = form.New("Set status · "+q.Name, []core.ParamSpec{
				str("status", "any lifecycle status", true),
				str("note", "optional reason/note", false),
			}, map[string]any{"status": q.Status, "note": ""})
		case "note":
			if !hasLane {
				a.warn("focus a lane first")
				return s, nil
			}
			m = form.New("Add note · "+q.Name, []core.ParamSpec{
				str("text", "note text", true),
			}, map[string]any{"text": ""})
		case "associate":
			if !hasLane {
				a.warn("focus a lane first")
				return s, nil
			}
			m = form.New("Associate · "+q.Name, []core.ParamSpec{
				str("project", "project reference (optional)", false),
				str("pane", "pane window ID (optional)", false),
				str("session", "pane session (optional)", false),
				str("role", "role (default implementer)", false),
			}, map[string]any{"project": q.Project, "pane": "", "session": "", "role": "implementer"})
		}
		m.SubmitLabel = "Apply (enter) · esc cancels"
		return openModal(s, modalForm, "form", laneFormData{M: m, Action: action, LaneID: q.ID}), nil
	}
}

func (env *Env) formCmd(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	old, ok := modalData(s, modalForm).(laneFormData)
	if !ok {
		return s, nil
	}
	f := old.clone()
	switch c.ID {
	case "form.clear":
		if i := f.M.ActiveIdx; i >= 0 && i < len(f.M.Specs) {
			f.M.SetValue(f.M.Specs[i].Name, "")
		}
	case form.CmdSubmit:
		res, _ := f.M.HandleCommand(c)
		if !res.Submitted {
			var msgs []string
			for _, name := range res.ErrorFields {
				msgs = append(msgs, name+": "+f.M.Errors[name])
			}
			a.fail("not applied — " + strings.Join(msgs, "; "))
			return setModalData(s, modalForm, f), nil
		}
		val := func(k string) string { v, _ := f.M.Values[k].(string); return strings.TrimSpace(v) }
		switch f.Action {
		case "define":
			q, err := env.St.Add(Spec{
				Name:     val("name"),
				Parent:   val("parent"),
				Project:  val("project"),
				Repo:     val("repo"),
				Estimate: val("estimate"),
				Scope:    val("scope"),
			})
			if err != nil {
				a.fail("define: " + err.Error())
				return setModalData(s, modalForm, f), nil
			}
			a.Tab = tabLanes
			env.reload(a, q.ID)
			a.note("defined " + q.ID)
		case "state":
			q, err := env.St.Status(f.LaneID, val("status"), val("note"))
			if err != nil {
				a.fail("state: " + err.Error())
				return setModalData(s, modalForm, f), nil
			}
			env.reload(a, q.ID)
			a.note(q.Name + " → " + q.Status)
		case "note":
			q, err := env.St.Annotate(f.LaneID, val("text"))
			if err != nil {
				a.fail("note: " + err.Error())
				return setModalData(s, modalForm, f), nil
			}
			env.reload(a, q.ID)
			a.note("note added to " + q.Name)
		case "associate":
			changed := false
			if proj := val("project"); proj != "" {
				if _, err := env.St.AssociateProject(f.LaneID, proj, false); err != nil {
					a.fail("associate project: " + err.Error())
					return setModalData(s, modalForm, f), nil
				}
				changed = true
			}
			if win := val("pane"); win != "" {
				if _, err := env.St.Associate(f.LaneID, Pane{Window: win, Session: val("session"), Role: val("role")}, false); err != nil {
					a.fail("associate pane: " + err.Error())
					return setModalData(s, modalForm, f), nil
				}
				changed = true
			}
			if !changed {
				a.warn("enter a project or pane window")
				return setModalData(s, modalForm, f), nil
			}
			env.reload(a, f.LaneID)
			a.note("associated")
		}
		return closeModal(s, modalForm), nil
	default:
		res, _ := f.M.HandleCommand(c)
		if res.Cancelled {
			a.info("cancelled")
			return closeModal(s, modalForm), nil
		}
	}
	return setModalData(s, modalForm, f), nil
}

func (env *Env) laneArchive(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	q, ok := a.focusedLane()
	if !ok {
		a.warn("focus a lane first")
		return s, nil
	}
	wasArchived := env.St.statuses()[q.Status].Archive
	got, err := env.St.mutate("archive", q.ID, func(l *Lane, _ *[]Lane) error {
		return env.St.setArchived(l, !wasArchived, "")
	})
	if err != nil {
		a.fail(err.Error())
		return s, nil
	}
	env.reload(a, got.ID)
	if !wasArchived {
		a.note("archived " + got.Name + " (. shows archived)")
	} else {
		a.note("restored " + got.Name + " → " + got.Status)
	}
	return s, nil
}

type choice struct {
	Kind   string // status-filter, project-filter, set-status
	LaneID string
	M      *picker.Model
}

func (env *Env) statePick(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	q, ok := a.focusedLane()
	if !ok {
		a.warn("focus a lane first")
		return s, nil
	}
	rows := env.St.statusRows()
	items := make([]picker.Item, len(rows))
	for i, st := range rows {
		items[i] = picker.Item{ID: st.Name, Title: st.Name, Subtitle: st.Description}
	}
	m := picker.New("Set status for "+q.Name, items)
	m.Scope, m.MatchMode = "choose", picker.MatchMode(a.Match)
	return openModal(s, modalChoose, "choose", choice{Kind: "set-status", LaneID: q.ID, M: m}), nil
}

func (env *Env) statusFilterPick(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	counts := map[string]int{}
	for _, q := range a.Lanes {
		counts[q.Status]++
	}
	items := []picker.Item{{ID: "", Title: "All statuses", Subtitle: strconv.Itoa(len(a.Lanes)) + " lanes"}}
	seen := map[string]bool{}
	for _, st := range env.St.statusRows() {
		seen[st.Name] = true
		items = append(items, picker.Item{ID: st.Name, Title: st.Name, Subtitle: fmt.Sprintf("%d lanes · %s", counts[st.Name], st.Description)})
	}
	for name, n := range counts {
		if !seen[name] {
			items = append(items, picker.Item{ID: name, Title: name, Subtitle: fmt.Sprintf("%d lanes (custom)", n)})
		}
	}
	m := picker.New("Filter by status", items)
	m.Scope, m.MatchMode = "choose", picker.MatchMode(a.Match)
	return openModal(s, modalChoose, "choose", choice{Kind: "status-filter", M: m}), nil
}

func (env *Env) projectFilterPick(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	counts := map[string]int{}
	var projs []string
	for _, q := range a.Lanes {
		p := a.Views[q.ID].EffectiveProject
		if p == "" {
			continue
		}
		if counts[p] == 0 {
			projs = append(projs, p)
		}
		counts[p]++
	}
	slices.Sort(projs)
	items := []picker.Item{{ID: "", Title: "All projects", Subtitle: strconv.Itoa(len(a.Lanes)) + " lanes"}}
	for _, p := range projs {
		items = append(items, picker.Item{ID: p, Title: p, Subtitle: strconv.Itoa(counts[p]) + " lanes"})
	}
	m := picker.New("Filter by project", items)
	m.Scope, m.MatchMode = "choose", picker.MatchMode(a.Match)
	return openModal(s, modalChoose, "choose", choice{Kind: "project-filter", M: m}), nil
}

func (env *Env) pickCmd(id string) fn {
	return func(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
		var m picker.Model
		var ch choice
		switch d := modalData(s, id).(type) {
		case *picker.Model:
			m = *d
		case choice:
			ch, m = d, *d.M
		default:
			return s, nil
		}
		store := func() core.State {
			if id == modalPalette {
				return setModalData(s, id, &m)
			}
			ch.M = &m
			return setModalData(s, id, ch)
		}
		switch strings.TrimPrefix(strings.TrimPrefix(c.ID, "palette."), "choose.") {
		case "insert":
			m.AppendQuery(c.Arg("text"))
			return store(), nil
		case "backspace":
			m.BackspaceQuery()
			return store(), nil
		case "close":
			return closeModal(s, id), nil
		}
		m.PageSize = max(s.Height*70/100-4, 1)
		picked, _ := m.HandleCommand(c)
		if m.FilterErr != "" {
			a.warn("regex: " + m.FilterErr)
		}
		if picked == nil {
			return store(), nil
		}
		s = closeModal(s, id)
		if id == modalPalette {
			return s, []core.Effect{dispatch(core.Command{ID: picked.ID, Source: "palette", Actor: core.Actor{Kind: "user"}})}
		}
		switch ch.Kind {
		case "status-filter":
			a.StatusFilter = picked.ID
			a.Tab = tabLanes
			env.rebuildBase(a)
			a.reproject()
			if a.StatusFilter == "" {
				a.info("all statuses")
			} else {
				a.info("status: " + a.StatusFilter)
			}
		case "project-filter":
			a.ProjectFilter = picked.ID
			a.Tab = tabLanes
			env.rebuildBase(a)
			a.reproject()
			if a.ProjectFilter == "" {
				a.info("all projects")
			} else {
				a.info("project: " + a.ProjectFilter)
			}
		case "set-status":
			q, err := env.St.Status(ch.LaneID, picked.ID, "")
			if err != nil {
				a.fail(err.Error())
				return s, nil
			}
			env.reload(a, q.ID)
			a.note(q.Name + " → " + q.Status)
		}
		return s, nil
	}
}

func (env *Env) paletteOpen(s core.State, a *App, c core.Command) (core.State, []core.Effect) {
	keys := keysByCommand(env)
	var items []picker.Item
	for _, spec := range env.Specs {
		if spec.HasTag("user") {
			items = append(items, paletteItem(spec, keys))
		}
	}
	for _, spec := range core.MotionSpecs() {
		if !slices.Contains(unusedMotions, spec.ID) {
			items = append(items, paletteItem(spec, keys))
		}
	}
	m := picker.New("Commands", items)
	m.Scope, m.Placeholder, m.MatchMode = "palette", "type to filter commands · tab cycles match mode", picker.MatchMode(a.Match)
	return openModal(s, modalPalette, "palette", m), nil
}

func paletteItem(spec core.CommandSpec, keys map[string][]string) picker.Item {
	sub := spec.Description
	if k := keys[spec.ID]; len(k) > 0 {
		sub = "[" + strings.Join(k, ", ") + "] " + sub
	}
	return picker.Item{ID: spec.ID, Title: spec.ID, Subtitle: sub, Keywords: append([]string{spec.Description}, keys[spec.ID]...)}
}

// --- shell & driver -------------------------------------------------------------

func newShell(env *Env) *otea.Shell {
	a := App{Tab: tabLanes, Match: env.Match, Ratio: env.Ratio, Focus: nodeList}
	a.List = *table.New(laneColumns(), nil)
	a.List.MatchMode = env.Match
	env.reload(&a, "")
	if len(env.Notes) > 0 {
		a.warn(strings.Join(env.Notes, " · "))
	}
	initial := withApp(core.NewState(core.Frame{ID: pageID, Mode: "normal"}), a)
	sh := otea.New(newEngine(env), initial, env.view, env.Theme)
	sh.OnPassThrough = func(e core.PassToWidgetEffect) tea.Cmd {
		if e.Mouse == nil {
			return nil
		}
		id := map[string]string{"wheeldown": "motion.down", "wheelup": "motion.up"}[e.Mouse.Button]
		if id == "" {
			return nil
		}
		return func() tea.Msg { return core.CommandEvent{Command: core.Command{ID: id, Count: 3, Source: "mouse"}} }
	}
	sh.OnCustom = func(e core.CustomEffect) tea.Cmd {
		c, ok := e.Data.(core.Command)
		if e.Kind != "dispatch" || !ok {
			return nil
		}
		return func() tea.Msg { return core.CommandEvent{Command: c} }
	}
	return sh
}

type driver struct{ sh *otea.Shell }

func newDriver(sh *otea.Shell, w, h int) *driver {
	sh.Engine.PrefixTimeout = 0
	sh.MaxFPS = 0
	d := &driver{sh: sh}
	d.send(tea.WindowSizeMsg{Width: w, Height: h})
	return d
}

func (d *driver) send(msg tea.Msg) {
	_, cmd := d.sh.Update(msg)
	d.pump(cmd)
}

func (d *driver) pump(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var m tea.Msg
	select {
	case m = <-done:
	case <-time.After(5 * time.Second):
		return
	}
	switch m := m.(type) {
	case tea.BatchMsg:
		for _, c := range m {
			d.pump(c)
		}
	case tea.QuitMsg, nil:
	default:
		d.send(m)
	}
}

func (d *driver) keys(names ...string) {
	for _, n := range names {
		d.send(keyMsg(n))
	}
}

func (d *driver) typeText(s string) {
	for _, r := range s {
		if r == ' ' {
			d.send(keyMsg("space"))
		} else {
			d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
}

var namedKeys = map[string]rune{
	"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "backspace": tea.KeyBackspace,
	"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
	"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
}

func keyMsg(n string) tea.KeyPressMsg {
	var mod tea.KeyMod
	for {
		if rest, ok := strings.CutPrefix(n, "ctrl+"); ok && len(rest) > 0 {
			mod, n = mod|tea.ModCtrl, rest
		} else if rest, ok := strings.CutPrefix(n, "alt+"); ok && len(rest) > 0 {
			mod, n = mod|tea.ModAlt, rest
		} else if rest, ok := strings.CutPrefix(n, "shift+"); ok && len(rest) > 0 {
			mod, n = mod|tea.ModShift, rest
		} else {
			break
		}
	}
	if r, ok := namedKeys[n]; ok {
		return tea.KeyPressMsg{Code: r, Mod: mod}
	}
	if n == "space" {
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " ", Mod: mod}
	}
	r := []rune(n)[0]
	if mod != 0 {
		return tea.KeyPressMsg{Code: r, Mod: mod}
	}
	return tea.KeyPressMsg{Code: r, Text: n}
}
