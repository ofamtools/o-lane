package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
)

const archivePageSeed = `{"lanes":[
	{"name":"Release"},
	{"name":"Build","parent":"Release"},
	{"name":"Old"},
	{"name":"Gone"},
	{"name":"Orphan","parent":"Gone"}
]}`

// archivePage mounts the Lanes page over a store with two archived lanes: Old
// (a root) and Gone, whose child Orphan is not archived itself.
func archivePage(t *testing.T) (*App, *page) {
	t.Helper()
	a := pageFixture(t)
	seedPage(t, a, json.RawMessage(archivePageSeed))
	for _, name := range []string{"Old", "Gone"} {
		if _, e := a.store.State(name, "archived", ""); e != nil {
			t.Fatal(e)
		}
	}
	if e := a.r.Settings().Set("lanes.live", false); e != nil {
		t.Fatal(e)
	}
	p, e := mountPage(a)
	if e != nil {
		t.Fatal(e)
	}
	p.m.Width, p.m.Height = 140, 34
	p.m.View()
	return a, p
}

func frameText(p *page) string { return ansi.Strip(p.m.View().Content) }

func hasLane(p *page, name string) bool {
	for _, line := range strings.Split(frameText(p), "\n") {
		line = strings.TrimLeft(line, "┃▾▸└├│─ ")
		if strings.HasPrefix(line, name+" ") {
			return true
		}
	}
	return false
}

func reloadPage(t *testing.T, p *page) {
	t.Helper()
	if cmd := p.reload(); cmd != nil {
		pageDeliver(t, p, cmd())
	}
	p.m.View()
}

func filterIsArchived(l *ochazuke.List) bool {
	return slices.ContainsFunc(l.PropFilters(), func(f ochazuke.PropFilter) bool { return f.Prop == archiveProp })
}

// TestPageArchivedFilterReveals drives the page the owner uses: archived
// lanes are out of the default view, the saved archived filter (z ]) shows
// them in the rendered frame, clearing hides them again, and restoring a lane
// moves it from the filtered view to the default one.
func TestPageArchivedFilterReveals(t *testing.T) {
	a, p := archivePage(t)
	l := p.v.List(listID)
	if !hasLane(p, "Release") || !hasLane(p, "Build") || hasLane(p, "Old") || hasLane(p, "Gone") || hasLane(p, "Orphan") {
		t.Fatalf("default frame:\n%s", frameText(p))
	}
	if !strings.Contains(frameText(p), "Lanes (2)") {
		t.Fatalf("default count:\n%s", frameText(p))
	}
	// Cycle the saved filters with z ] until the archived one is on.
	for i := 0; i < 12 && !filterIsArchived(l); i++ {
		pageKey(t, p, "z")
		pageKey(t, p, "]")
	}
	if !filterIsArchived(l) {
		t.Fatalf("z ] never reached the archived filter: %v", l.PropFilters())
	}
	f := frameText(p)
	if !hasLane(p, "Old") || !hasLane(p, "Gone") || hasLane(p, "Release") {
		t.Fatalf("archived frame:\n%s", f)
	}
	// The archived parent shows with its subtree context; Orphan is not itself archived.
	if hasLane(p, "Orphan") {
		t.Fatalf("unarchived child of an archived parent leaked into the archived view:\n%s", f)
	}
	// A refresh keeps the filtered view (the feed carries archived lanes).
	reloadPage(t, p)
	if !hasLane(p, "Old") {
		t.Fatalf("archived lane lost on refresh:\n%s", frameText(p))
	}
	// Restoring Old from the page: it leaves the archived view.
	old, e := a.store.Resolve("Old")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.r.Execute(ochazuke.Request{Noun: "lane", Verb: "restore", Args: ochazuke.Arguments{"id": old.ID}}); e != nil {
		t.Fatal(e)
	}
	reloadPage(t, p)
	if hasLane(p, "Old") || !hasLane(p, "Gone") {
		t.Fatalf("after restore (archived filter):\n%s", frameText(p))
	}
	if e = l.SetPropFilters(nil); e != nil {
		t.Fatal(e)
	}
	if !hasLane(p, "Old") || !hasLane(p, "Release") || hasLane(p, "Gone") || hasLane(p, "Orphan") {
		t.Fatalf("after clear:\n%s", frameText(p))
	}
}

func TestPageArchivedHidingSetting(t *testing.T) {
	a, p := archivePage(t)
	if _, e := a.r.Settings().SetSession("list.hide-archived", false); e != nil {
		t.Fatal(e)
	}
	p.m.View()
	for _, name := range []string{"Release", "Old", "Gone", "Orphan"} {
		if !hasLane(p, name) {
			t.Fatalf("hide-archived off, %s missing:\n%s", name, frameText(p))
		}
	}
}

// TestPageStateFilterNamesArchive: a lanes.state-filter naming an archive
// state shows those lanes on the page without a prop filter.
func TestPageStateFilterNamesArchive(t *testing.T) {
	a, p := archivePage(t)
	if e := a.r.Settings().Set("lanes.state-filter", []any{"archived"}); e != nil {
		t.Fatal(e)
	}
	reloadPage(t, p)
	if !hasLane(p, "Old") || !hasLane(p, "Gone") || hasLane(p, "Release") {
		t.Fatalf("state-filter archived:\n%s", frameText(p))
	}
	if e := a.r.Settings().Set("lanes.state-filter", []any{"planned"}); e != nil {
		t.Fatal(e)
	}
	reloadPage(t, p)
	if hasLane(p, "Old") || !hasLane(p, "Release") {
		t.Fatalf("state-filter planned:\n%s", frameText(p))
	}
}

// TestRowsArchivedParameter: the portable feed leaves archived lanes out
// unless asked (archived=true, which the page passes); a state naming an
// archive state still returns them.
func TestRowsArchivedParameter(t *testing.T) {
	f := newCLI(t)
	f.a.store.BackupDaily = false
	for _, args := range [][]string{{"add", "Keep"}, {"add", "Old"}, {"add", "Gone"}, {"add", "Orphan", "--parent", "Gone"}, {"archive", "Old"}, {"archive", "Gone"}} {
		f.lane(args...)
	}
	if got := listed(t, f, "label", "lane", "rows"); got != "Keep" {
		t.Fatal("rows:", got)
	}
	if got := listed(t, f, "label", "lane", "rows", "--archived"); !strings.Contains(got, "Old") || !strings.Contains(got, "Orphan") {
		t.Fatal("rows --archived:", got)
	}
}
