package main

import (
	"encoding/json"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

func TestDefaultStatusTonesAreDistinct(t *testing.T) {
	rows, e := parseStatuses(defaultStatuses)
	if e != nil {
		t.Fatal(e)
	}
	known := ochazuke.DefaultTones()
	seen := map[string]string{}
	light, dark := map[string]string{}, map[string]string{}
	for _, st := range rows {
		if st.Name == "planned" {
			if st.Tone != "" {
				t.Fatalf("planned keeps the default colour, got tone %q", st.Tone)
			}
			continue
		}
		if st.Tone == "" {
			t.Fatalf("status %s has no tone", st.Name)
		}
		if other, dup := seen[st.Tone]; dup {
			t.Fatalf("%s and %s share tone %s", st.Name, other, st.Tone)
		}
		seen[st.Tone] = st.Name
		style, ok := statusTones[st.Tone]
		if !ok {
			style, ok = known[st.Tone]
		}
		if !ok {
			t.Fatalf("status %s: tone %s is declared nowhere", st.Name, st.Tone)
		}
		for _, c := range []struct {
			by  map[string]string
			col string
		}{{light, style.Fg.Light}, {dark, style.Fg.Dark}} {
			if other, dup := c.by[c.col]; dup {
				t.Fatalf("%s and %s draw the same colour %s", st.Name, other, c.col)
			}
			c.by[c.col] = st.Name
		}
	}
	if len(seen) != len(rows)-1 {
		t.Fatal(seen)
	}
}

func lanePageRow(t *testing.T, f *cliFixture, name string) (cell string, data map[string]any) {
	t.Helper()
	f.call("lane", "list")
	rows, e := laneRows(f.a.store, f.a.pageOptions())
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range rows {
		if r.Label == name {
			d, _ := r.Data.(map[string]any)
			return r.Cells[1], d
		}
	}
	t.Fatalf("no row %s", name)
	return
}

func TestRollupCountsOnPageRows(t *testing.T) {
	f := newCLI(t)
	f.a.store.BackupDaily = false
	for _, args := range [][]string{
		{"add", "Root"}, {"add", "A", "--parent", "Root"}, {"add", "B", "--parent", "Root"}, {"add", "C", "--parent", "A"},
		{"add", "D", "--parent", "Root"}, {"add", "E", "--parent", "Root"},
		{"set", "A", "status", "done"}, {"set", "B", "status", "merged"}, {"set", "D", "status", "cancelled"}, {"set", "E", "status", "parked"},
	} {
		f.lane(args...)
	}
	// C (planned), E (parked) are not complete; A, B, D are (done, merged, cancelled).
	cell, data := lanePageRow(t, f, "Root")
	if !strings.HasSuffix(cell, " · 3/5") {
		t.Fatalf("root cell %q", cell)
	}
	if data["children_done"] != 3 || data["children_total"] != 5 {
		t.Fatal(data["children_done"], data["children_total"])
	}
	if cell, _ = lanePageRow(t, f, "A"); !strings.HasSuffix(cell, " · 0/1") {
		t.Fatalf("A cell %q", cell)
	}
	if cell, _ = lanePageRow(t, f, "C"); strings.Contains(cell, "/") {
		t.Fatalf("leaf shows a count: %q", cell)
	}
	// Finishing the rest hides the count; archived lanes count in neither number.
	f.lane("set", "C", "status", "done")
	f.lane("set", "E", "status", "archived")
	if cell, _ = lanePageRow(t, f, "Root"); strings.Contains(cell, "/") {
		t.Fatalf("complete tree still counts: %q", cell)
	}
	// The setting turns it off.
	f.lane("set", "C", "status", "planned")
	if _, e := f.a.r.Settings().SetSession("lanes.rollup-counts", false); e != nil {
		t.Fatal(e)
	}
	if cell, _ = lanePageRow(t, f, "Root"); strings.Contains(cell, "/") {
		t.Fatalf("setting off still counts: %q", cell)
	}
}

func archivedNames(t *testing.T, f *cliFixture) string {
	t.Helper()
	return labels(asRows(t, f.call("lane", "list", "--status", "archived")), "name")
}

func TestArchiveFinishedTreesOnly(t *testing.T) {
	f := newCLI(t)
	f.a.store.BackupDaily = false
	for _, args := range [][]string{
		{"add", "AllDone"}, {"add", "x", "--parent", "AllDone"}, {"add", "y", "--parent", "x"},
		{"add", "Mixed"}, {"add", "m1", "--parent", "Mixed"}, {"add", "m2", "--parent", "Mixed"},
		{"add", "Lone"}, {"add", "Open"},
		{"set", "AllDone", "status", "done"}, {"set", "x", "status", "merged"}, {"set", "y", "status", "cancelled"},
		{"set", "Mixed", "status", "done"}, {"set", "m1", "status", "done"}, {"set", "m2", "status", "review"},
		{"set", "Lone", "status", "merged"},
	} {
		f.lane(args...)
	}
	// Dry run lists and changes nothing.
	res := f.call("lane", "archive", "--finished-trees", "--dry-run")
	raw, _ := json.Marshal(res)
	var got ArchiveFinished
	if e := json.Unmarshal(raw, &got); e != nil {
		t.Fatal(e, string(raw))
	}
	if !got.DryRun || len(got.Archived) != 2 || got.Archived[0].Name != "AllDone" || got.Archived[0].Lanes != 3 || got.Archived[1].Name != "Lone" || got.Kept != 2 {
		t.Fatalf("%+v", got)
	}
	if a := archivedNames(t, f); a != "" {
		t.Fatal("dry run archived", a)
	}
	// The real run archives the roots of whole-complete trees only.
	f.call("lane", "archive", "--finished-trees")
	if a := archivedNames(t, f); a != "AllDone,Lone" {
		t.Fatal("archived:", a)
	}
	for _, name := range []string{"Mixed", "m1", "m2", "Open"} {
		if q := f.lane("show", name); q["status"] == "archived" {
			t.Fatal(name, "was archived")
		}
	}
	// Descendants keep their statuses and are hidden with the root; nothing is deleted.
	if q := f.lane("show", "y"); q["status"] != "cancelled" {
		t.Fatal(q["status"])
	}
	if got := labels(asRows(t, f.call("lane", "list")), "name"); got != "Mixed,Open,m1,m2" {
		t.Fatal("visible:", got)
	}
	// Repeating finds nothing; finishing the last child makes Mixed eligible; restore returns a tree.
	f.call("lane", "archive", "--finished-trees")
	if a := archivedNames(t, f); a != "AllDone,Lone" {
		t.Fatal(a)
	}
	f.lane("set", "m2", "status", "done")
	f.call("lane", "archive", "--finished-trees")
	if a := archivedNames(t, f); a != "AllDone,Lone,Mixed" {
		t.Fatal(a)
	}
	f.call("lane", "set", "AllDone", "archived=false")
	if q := f.lane("show", "AllDone"); q["status"] != "done" {
		t.Fatal(q["status"])
	}
}
