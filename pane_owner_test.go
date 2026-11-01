package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPaneMovesToNewLane(t *testing.T) {
	a := pageFixture(t)
	seedPage(t, a, json.RawMessage(`{"lanes":[{"name":"Old"},{"name":"New"}]}`))
	pane := Pane{Session: "s", Window: "w1", Harness: "codex"}
	if _, e := a.store.Associate("Old", pane, false); e != nil {
		t.Fatal(e)
	}
	a.store.Now = func() time.Time { return time.Date(2026, 1, 1, 14, 0, 0, 0, time.UTC) }
	got, e := a.store.AssociateMoving("New", pane, false)
	if e != nil {
		t.Fatal(e)
	}
	old := mustResolve(t, a.store, "Old")
	if len(old.Panes) != 0 || len(got.Panes) != 1 {
		t.Fatalf("old %+v new %+v", old.Panes, got.Panes)
	}
	if len(got.MovedFrom) != 1 || got.MovedFrom[0].ID != old.ID || got.MovedFrom[0].Name != "Old" {
		t.Fatal(got.MovedFrom)
	}
	last := old.History[len(old.History)-1]
	if last.Kind != "unassociate" || last.MovedTo != got.ID || last.Pane == nil || last.Pane.Window != "w1" || !strings.Contains(last.Text, "New") {
		t.Fatalf("%+v", last)
	}
	// One snapshot covers both lanes, so one undo restores both.
	if e = a.store.Undo(); e != nil {
		t.Fatal(e)
	}
	if old, q := mustResolve(t, a.store, "Old"), mustResolve(t, a.store, "New"); len(old.Panes) != 1 || len(q.Panes) != 0 {
		t.Fatalf("undo: old %+v new %+v", old.Panes, q.Panes)
	}
	// Re-associating with the same lane moves nothing.
	if got, e = a.store.AssociateMoving("Old", pane, false); e != nil || len(got.MovedFrom) != 0 {
		t.Fatal(got.MovedFrom, e)
	}
}

func TestPaneOneLaneSettingOff(t *testing.T) {
	a := pageFixture(t)
	if e := a.r.Settings().Set("lanes.pane-one-lane", false); e != nil {
		t.Fatal(e)
	}
	if e := a.configure(); e != nil {
		t.Fatal(e)
	}
	seedPage(t, a, json.RawMessage(`{"lanes":[{"name":"Old"},{"name":"New"}]}`))
	pane := Pane{Session: "s", Window: "w1"}
	for _, lane := range []string{"Old", "New"} {
		if got, e := a.store.AssociateMoving(lane, pane, false); e != nil || len(got.MovedFrom) != 0 {
			t.Fatal(got, e)
		}
	}
	rows, _ := a.store.List()
	for _, name := range []string{"Old", "New"} {
		if q := mustResolve(t, a.store, name); len(q.Panes) != 1 || len(a.store.panesInSubtree(rows, q)) != 1 {
			t.Fatalf("%s lost its shared pane", name)
		}
	}
}

// Data recorded before the rule may list one pane on two lanes: only the
// lane with the latest association counts it.
func TestPaneReadsForLatestLane(t *testing.T) {
	a := pageFixture(t)
	seedPage(t, a, json.RawMessage(`{"lanes":[{"name":"Old"},{"name":"New"},{"name":"Other"}]}`))
	a.store.PanesShared = true
	pane := Pane{Session: "s", Window: "w1", Harness: "codex"}
	for i, lane := range []string{"Old", "New"} {
		at := time.Date(2026, 1, 1, 13+i, 0, 0, 0, time.UTC)
		a.store.Now = func() time.Time { return at }
		if _, e := a.store.Associate(lane, pane, false); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := a.store.Associate("Other", Pane{Session: "s", Window: "w2"}, false); e != nil {
		t.Fatal(e)
	}
	a.store.PanesShared = false
	rows, _ := a.store.List()
	count := func(name string) int { return len(a.store.panesInSubtree(rows, mustResolve(t, a.store, name))) }
	if count("Old") != 0 || count("New") != 1 || count("Other") != 1 {
		t.Fatal(count("Old"), count("New"), count("Other"))
	}
	page, e := laneRows(a.store, a.pageOptions())
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range page {
		data := row.Data.(map[string]any)
		switch row.Label {
		case "Old":
			if detail := data["pane_detail"].(string); !strings.Contains(detail, "moved to lane New") || strings.Contains(detail, "codex") {
				t.Fatal(detail)
			}
		case "New":
			if !strings.Contains(data["pane_detail"].(string), "codex") {
				t.Fatal(data["pane_detail"])
			}
		}
	}
	a.store.PanesShared = true
	if count("Old") != 1 || count("New") != 1 {
		t.Fatal("shared panes should count on both lanes")
	}
}

func TestCLIAssociateReportsMove(t *testing.T) {
	dir := fixture(t)
	mustMain(t, dir, "lane", "add", "Old")
	mustMain(t, dir, "lane", "add", "New")
	mustMain(t, dir, "lane", "associate", "Old", "w1", "--session", "s")
	out := mustMain(t, dir, "lane", "associate", "New", "w1", "--session", "s")
	if !strings.Contains(out, "moved_from") || !strings.Contains(out, "Old") {
		t.Fatal(out)
	}
}
