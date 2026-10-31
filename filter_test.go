package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
)

// filterSeed is a root (planned) with a running and a planned child, a
// running root with a planned child, and a done root.
const filterSeed = `{"lanes":[
	{"name":"Release","project":"alpha"},
	{"name":"Build","parent":"Release"},
	{"name":"Docs","parent":"Release"},
	{"name":"Hotfix"},
	{"name":"Hotfix tests","parent":"Hotfix"},
	{"name":"Old"}
]}`

func seedFilter(t *testing.T, a *App) {
	t.Helper()
	seedPage(t, a, json.RawMessage(filterSeed))
	for _, name := range []string{"Build", "Hotfix"} {
		if _, e := a.store.Start(name, Pane{}, "", ""); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := a.store.State("Old", "done", ""); e != nil {
		t.Fatal(e)
	}
}

func labels(rows []map[string]any, key string) string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r[key].(string))
	}
	slices.Sort(out)
	return strings.Join(out, ",")
}

func asRows(t *testing.T, v any) []map[string]any {
	t.Helper()
	raw, _ := json.Marshal(v)
	var rows []map[string]any
	if e := json.Unmarshal(raw, &rows); e != nil {
		t.Fatal(e, string(raw))
	}
	return rows
}

func TestCLIStateFilter(t *testing.T) {
	f := newCLI(t)
	f.a.store.BackupDaily = false
	for _, args := range [][]string{
		{"add", "Release", "--project", "alpha"}, {"add", "Build", "--parent", "Release"}, {"add", "Docs", "--parent", "Release"},
		{"add", "Hotfix"}, {"add", "Hotfix tests", "--parent", "Hotfix"}, {"add", "Old"},
		{"start", "Build"}, {"start", "Hotfix"}, {"state", "Old", "done"},
	} {
		f.lane(args...)
	}
	if got := labels(asRows(t, f.call("lane", "list", "--state", "running")), "name"); got != "Build,Hotfix" {
		t.Fatal("list --state running:", got)
	}
	// Comma list; spaces and repeats are ignored.
	if got := labels(asRows(t, f.call("lane", "list", "--state", "running, done,running")), "name"); got != "Build,Hotfix,Old" {
		t.Fatal("comma list:", got)
	}
	// --json (and MCP) pass the same comma list.
	if got := labels(asRows(t, f.call("lane", "list", "--json", `{"state":"done"}`)), "name"); got != "Old" {
		t.Fatal("json:", got)
	}
	// Tree: a matching child keeps its ancestor; a matching root's
	// non-matching children stay hidden.
	if got := labels(asRows(t, f.call("lane", "tree", "--state", "running")), "name"); got != "Build,Hotfix,Release" {
		t.Fatal("tree --state:", got)
	}
	if got := labels(asRows(t, f.call("lane", "rows", "--state", "running")), "label"); got != "Build,Hotfix,Release" {
		t.Fatal("rows --state:", got)
	}
	if got := labels(asRows(t, f.call("project", "lanes", "alpha", "--state", "planned")), "name"); got != "Docs,Release" {
		t.Fatal("project lanes --state:", got)
	}
	f.fail(`unknown state "runing"`, "lane", "list", "--state", "runing")
	f.fail(`unknown state "nope"`, "lane", "rows", "--state", "nope")
	// lane.state keeps its own state argument (the state to record).
	if q := f.lane("state", "Docs", "review"); q["state"] != "review" {
		t.Fatal(q)
	}
}

func TestPageStateFilterSetting(t *testing.T) {
	a := pageFixture(t)
	seedFilter(t, a)
	names := func() string {
		rows, e := laneRows(a.store, a.pageOptions())
		if e != nil {
			t.Fatal(e)
		}
		out := []string{}
		for _, r := range rows {
			out = append(out, r.Label)
		}
		return strings.Join(out, ",")
	}
	all := names()
	if e := a.r.Settings().Set("lanes.state-filter", []any{"running"}); e != nil {
		t.Fatal(e)
	}
	// Tree order is kept; Docs and Hotfix tests do not match, Old is done.
	if got := names(); got != "Release,Build,Hotfix" {
		t.Fatal("state-filter running:", got)
	}
	if e := a.r.Settings().Set("lanes.state-filter", []any{"planned"}); e != nil {
		t.Fatal(e)
	}
	// Hotfix (running) stays as the context ancestor of Hotfix tests.
	if got := names(); got != "Release,Docs,Hotfix,Hotfix tests" {
		t.Fatal("state-filter planned:", got)
	}
	if e := a.r.Settings().Set("lanes.state-filter", []any{}); e != nil {
		t.Fatal(e)
	}
	if got := names(); got != all {
		t.Fatal("empty setting must show all:", got, all)
	}
	// The setting composes with the project filter (AND).
	if e := a.r.Settings().Set("lanes.state-filter", []any{"running"}); e != nil {
		t.Fatal(e)
	}
	if e := a.r.Settings().Set("lanes.project-filter", "alpha"); e != nil {
		t.Fatal(e)
	}
	if got := names(); got != "Release,Build" {
		t.Fatal("state and project:", got)
	}
	// An unknown name in the setting surfaces from lane.rows, not as "no rows".
	if e := a.r.Settings().Set("lanes.state-filter", []any{"bogus"}); e != nil {
		t.Fatal(e)
	}
	if e := a.store.checkStates(a.pageOptions().states); e == nil || !strings.Contains(e.Error(), "bogus") {
		t.Fatal(e)
	}
}

func TestPagePropFilters(t *testing.T) {
	a := pageFixture(t)
	seedFilter(t, a)
	if e := a.r.Settings().Set("lanes.live", false); e != nil {
		t.Fatal(e)
	}
	p, e := mountPage(a)
	if e != nil {
		t.Fatal(e)
	}
	rows, e := laneRows(a.store, a.pageOptions())
	if e != nil {
		t.Fatal(e)
	}
	if e = p.v.SetRows(listID, rows); e != nil {
		t.Fatal(e)
	}
	l := p.v.List(listID)
	shown := func() string {
		// The list exposes only the shown count; read names off the frame.
		out := []string{}
		for _, line := range strings.Split(ansi.Strip(l.View(200, 30)), "\n") {
			line = strings.TrimLeft(line, "▾▸└├│─ ")
			if line == "" || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "Name ") {
				continue
			}
			name, _, _ := strings.Cut(line, "  ")
			out = append(out, name)
		}
		if len(out) != l.Shown() {
			t.Fatalf("parsed %v, shown %d", out, l.Shown())
		}
		return strings.Join(out, ",")
	}
	set := func(filters ...ochazuke.PropFilter) {
		t.Helper()
		if e := l.SetPropFilters(filters); e != nil {
			t.Fatal(e)
		}
	}
	set(ochazuke.PropFilter{Prop: "status", Value: "running"})
	if got := shown(); got != "Release,Build,Hotfix" {
		t.Fatal("status = running:", got)
	}
	// Conditions AND: running lanes in project alpha.
	set(ochazuke.PropFilter{Prop: "status", Value: "running"}, ochazuke.PropFilter{Prop: "effective_project", Value: "alpha"})
	if got := shown(); got != "Release,Build" {
		t.Fatal("AND:", got)
	}
	// Every declared filter prop is present in row data, so != on an empty
	// value works and integers compare typed.
	set(ochazuke.PropFilter{Prop: "repo", Op: "!=", Value: "x"}, ochazuke.PropFilter{Prop: "pane_count", Value: 0}, ochazuke.PropFilter{Prop: "finished", Value: ""})
	if got := shown(); got != "Release,Docs,Build,Hotfix,Hotfix tests" {
		t.Fatal("present props:", got)
	}
	set(ochazuke.PropFilter{Prop: "live_state", Value: ""}, ochazuke.PropFilter{Prop: "rolled_state", Value: "done"})
	if got := shown(); got != "Old" {
		t.Fatal("rolled_state:", got)
	}
	// The default "open" saved filter: status differs from every closed state.
	if e = l.ApplyFilter("open"); e != nil {
		t.Fatal(e)
	}
	if got := shown(); got != "Release,Docs,Build,Hotfix,Hotfix tests" {
		t.Fatal("open:", got)
	}
	// Saved filters round-trip through list.filters.
	set(ochazuke.PropFilter{Prop: "status", Value: "running"})
	if e = l.SaveFilter("active"); e != nil {
		t.Fatal(e)
	}
	set()
	if e = l.ApplyFilter("active"); e != nil {
		t.Fatal(e)
	}
	if got := shown(); got != "Release,Build,Hotfix" {
		t.Fatal("saved active:", got)
	}
	// Derived filter props are read-only: set never writes them.
	if _, e = a.r.Execute(ochazuke.Request{Noun: "lane", Verb: "set", Args: ochazuke.Arguments{"id": "Build", "status": "done"}}); e == nil {
		t.Fatal("set wrote a read-only filter prop")
	}
}

func TestOpenFilterFollowsStates(t *testing.T) {
	a := pageFixture(t)
	if e := a.r.Settings().Set("lanes.states", `[{"name":"todo","open":true},{"name":"shipped","ends":true}]`); e != nil {
		t.Fatal(e)
	}
	if e := a.configure(); e != nil {
		t.Fatal(e)
	}
	filters, e := a.r.SavedFilters("", "lane")
	if e != nil {
		t.Fatal(e)
	}
	i := slices.IndexFunc(filters, func(f ochazuke.SavedFilter) bool { return f.Name == "open" })
	if i < 0 || len(filters[i].Conditions) != 1 || filters[i].Conditions[0].Value != "shipped" {
		t.Fatalf("open filter %+v", filters)
	}
}

// TODO(search-text): ochazuke's ListItem.Search (unreleased, branch
// release-v075) lets a row carry hidden text the / filter matches. Once
// o-lane requires that release, give lane rows a Search string (state,
// live state, project, repo, branch, estimate) through the catalog row and
// unskip this test.
func TestRowSearchText(t *testing.T) {
	t.Skip("needs ochazuke ListItem.Search (release-v075); o-lane is on v0.72.1")
	a := pageFixture(t)
	seedFilter(t, a)
	p, e := mountPage(a)
	if e != nil {
		t.Fatal(e)
	}
	_ = p
	// Expect: typing "/alpha" with the project column off still finds Release.
}
