package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

func listed(t *testing.T, f *cliFixture, key string, args ...string) string {
	t.Helper()
	return labels(asRows(t, f.call(args...)), key)
}

func TestArchiveHidesAndRestores(t *testing.T) {
	f := newCLI(t)
	f.a.store.BackupDaily = false
	for _, args := range [][]string{{"add", "Release"}, {"add", "Build", "--parent", "Release"}, {"add", "Old"}, {"state", "Old", "review"}} {
		f.lane(args...)
	}
	if q := f.lane("archive", "Old"); q["state"] != "archived" {
		t.Fatal(q)
	}
	// Hidden from the unfiltered list, the tree, the page rows and project lanes.
	if got := listed(t, f, "name", "lane", "list"); got != "Build,Release" {
		t.Fatal("list:", got)
	}
	if got := listed(t, f, "name", "lane", "tree"); got != "Build,Release" {
		t.Fatal("tree:", got)
	}
	if got := listed(t, f, "label", "lane", "rows"); got != "Build,Release" {
		t.Fatal("rows:", got)
	}
	// Other state filters keep hiding it; an explicit archived state shows it.
	if got := listed(t, f, "name", "lane", "list", "--state", "review,planned"); got != "Build,Release" {
		t.Fatal("other filter:", got)
	}
	if got := listed(t, f, "name", "lane", "list", "--state", "archived"); got != "Old" {
		t.Fatal("--state archived:", got)
	}
	if got := listed(t, f, "label", "lane", "rows", "--state", "archived"); got != "Old" {
		t.Fatal("rows --state archived:", got)
	}
	// The archive role: get reports the prop; the declared filter exists; open excludes it.
	if q := f.lane("get", "Old"); q["archived"] != true {
		t.Fatal(q)
	}
	filters := map[string]ochazuke.SavedFilter{}
	for _, sf := range f.a.r.Noun("lane").Filters {
		filters[sf.Name] = sf
	}
	if _, ok := filters["archived"]; !ok {
		t.Fatal("no archived saved filter", filters)
	}
	excluded := false
	for _, c := range filters["open"].Conditions {
		excluded = excluded || (c.Prop == "status" && c.Value == "archived")
	}
	if !excluded {
		t.Fatal("open filter keeps archived", filters["open"])
	}
	// Restore returns the lane to the state it had before archiving.
	f.call("lane", "restore", "Old")
	if q := f.lane("show", "Old"); q["state"] != "review" {
		t.Fatal("restore:", q["state"])
	}
	// list.hide-archived off shows archived lanes everywhere.
	f.lane("state", "Old", "archived")
	if _, e := f.a.r.Settings().SetSession("list.hide-archived", false); e != nil {
		t.Fatal(e)
	}
	if got := listed(t, f, "name", "lane", "list"); got != "Build,Old,Release" {
		t.Fatal("hide off:", got)
	}
}

func TestArchiveSetPropAndRepeat(t *testing.T) {
	f := newCLI(t)
	f.lane("add", "A")
	f.lane("finish", "A")
	f.call("lane", "set", "A", "archived=true")
	f.lane("archive", "A") // again: restore still finds the real previous state
	f.call("lane", "set", "A", "archived=false")
	if q := f.lane("show", "A"); q["state"] != "done" {
		t.Fatal(q["state"])
	}
	// Restoring a lane that was never archived changes nothing; one archived
	// straight from creation falls back to the first open state.
	f.call("lane", "restore", "A")
	f.lane("add", "B")
	f.lane("state", "B", "archived")
	f.call("lane", "restore", "B")
	if q := f.lane("show", "B"); q["state"] != "planned" {
		t.Fatal(q["state"])
	}
}

func TestArchiveRolledStateIgnoresArchivedChildren(t *testing.T) {
	f := newCLI(t)
	f.a.store.BackupDaily = false
	f.lane("add", "Root")
	f.lane("add", "Done child", "--parent", "Root")
	f.lane("add", "Dropped", "--parent", "Root")
	f.lane("finish", "Done child")
	f.lane("start", "Dropped")
	if q := f.lane("show", "Root"); q["rolled_state"] != "running" {
		t.Fatal(q["rolled_state"])
	}
	f.lane("archive", "Dropped")
	if q := f.lane("show", "Root"); q["rolled_state"] != "done" {
		t.Fatalf("archived child kept the parent open: %v", q["rolled_state"])
	}
	// Archiving a finished lane keeps its finished time.
	f.lane("archive", "Done child")
	s := f.a.store
	q := mustResolve(t, s, "Done child")
	if v := s.view(q, []Lane{q}); v.Finished == nil {
		t.Fatal("archiving cleared the finished time")
	}
}

func TestArchiveStaleIgnoresArchived(t *testing.T) {
	states, _ := parseStates(defaultStates)
	st := map[string]State{}
	for _, x := range states {
		st[x.Name] = x
	}
	if stale, _ := staleLane(Lane{State: "archived"}, st, Live{State: "idle"}, true, time.Now()); stale {
		t.Fatal("archived lane flagged stale")
	}
}

func TestArchiveWithoutStateRow(t *testing.T) {
	f := newCLI(t)
	// An older saved lanes.states without the archive row keeps working.
	if _, e := f.a.r.Settings().SetSession("lanes.states", `[{"name":"planned","open":true},{"name":"done","ends":true}]`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.a.r.Settings().SetSession("lanes.state-presets", `{"finish":"done"}`); e != nil {
		t.Fatal(e)
	}
	f.lane("add", "Plain")
	f.fail("archive:true", "lane", "set", "Plain", "archived=true")
	f.fail("lanes.state-presets", "lane", "archive", "Plain")
	f.fail("unknown state", "lane", "state", "Plain", "archived")
	f.call("lane", "restore", "Plain") // not archived: nothing to do
	if got := listed(t, f, "name", "lane", "list"); got != "Plain" {
		t.Fatal(got)
	}
}

func TestArchiveAgentRights(t *testing.T) {
	f := newCLI(t)
	f.a.actor = Actor{Kind: "agent", Name: "implementer", Harness: "test", Pane: "w", Session: "s", Model: "m"}
	f.lane("add", "Mine")
	f.lane("archive", "Mine")
	f.call("lane", "restore", "Mine")
	f.call("lane", "set", "Mine", "archived=true")
	f.fail("verb declaration", "lane", "delete", "Mine")
	doc := f.call("catalog").(map[string]any)
	seen := map[string]string{}
	for _, row := range doc["nouns"].([]any) {
		noun := row.(map[string]any)
		if noun["name"] != "lane" {
			continue
		}
		for _, row := range noun["verbs"].([]any) {
			verb := row.(map[string]any)
			seen[verb["name"].(string)], _ = verb["agent"].(string)
			if verb["name"] == "delete" && !strings.Contains(verb["description"].(string), "archive") {
				t.Fatal("delete does not point at archive:", verb["description"])
			}
		}
	}
	for _, name := range []string{"archive", "restore"} {
		if seen[name] != ochazuke.AgentAllow {
			t.Fatalf("lane.%s agent right = %q", name, seen[name])
		}
	}
}

// A mounted page fetches its list source with no arguments, so the catalog's
// source must itself answer archived lanes (flagged) while plain lane.rows
// with no arguments keeps dropping them.
func TestCatalogListSourceKeepsArchivedRows(t *testing.T) {
	f := newCLI(t)
	f.a.store.BackupDaily = false
	f.lane("add", "Live")
	f.lane("add", "Old")
	f.lane("archive", "Old")
	b, e := ocli.Catalog(f.a.r, "o-lane", version)
	if e != nil {
		t.Fatal(e)
	}
	var doc ocli.CatalogDoc
	if e = json.Unmarshal(b, &doc); e != nil {
		t.Fatal(e)
	}
	src := doc.Pages[0].Lists[0].Source
	if src == "lane.rows" {
		t.Fatal("list source is lane.rows, which drops archived lanes")
	}
	noun, verb, _ := strings.Cut(src, ".")
	rows := asRows(t, f.call(noun, verb))
	if got := labels(rows, "label"); got != "Live,Old" {
		t.Fatal("source rows:", got)
	}
	flagged := false
	for _, r := range rows {
		if r["label"] == "Old" {
			raw, _ := json.Marshal(r)
			flagged = strings.Contains(string(raw), `"archived":true`)
		}
	}
	if !flagged {
		t.Fatal("archived row not flagged:", rows)
	}
	if got := listed(t, f, "label", "lane", "rows"); got != "Live" {
		t.Fatal("lane.rows with no args:", got)
	}
}
