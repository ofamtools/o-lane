package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A store written before the lexicon rename holds "state" on each lane and
// history entries of kind "state". Reading migrates both to status; the
// next write stores the new names.
func TestLegacyStateKeyLoadsAsStatus(t *testing.T) {
	s := testStore(t)
	if err := os.MkdirAll(filepath.Join(s.Dir, "roots"), 0700); err != nil {
		t.Fatal(err)
	}
	legacy := `[{"name":"Old work","id":"old","state":"review","created":"2026-01-01T00:00:00Z","updated":"2026-01-02T00:00:00Z","panes":[],"links":[],` +
		`"history":[{"id":"e1","at":"2026-01-01T00:00:00Z","by":{"kind":"owner"},"kind":"create","to":"planned"},` +
		`{"id":"e2","at":"2026-01-02T00:00:00Z","by":{"kind":"owner"},"kind":"state","from":"planned","to":"review","text":"ready"}]}]`
	if err := os.WriteFile(filepath.Join(s.Dir, "roots", "old.json"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	q := mustResolve(t, s, "old")
	if q.Status != "review" || q.History[1].Kind != "status" || q.History[1].To != "review" {
		t.Fatalf("legacy lane not migrated: %+v", q)
	}
	if v := s.view(q, []Lane{q}); v.StatusReason != "ready" {
		t.Fatalf("the migrated status entry keeps its reason: %q", v.StatusReason)
	}
	if _, e := s.Status("old", "done", ""); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(s.Dir, "roots", "old.json"))
	if e != nil {
		t.Fatal(e)
	}
	if got := string(b); contains(got, `"state"`) || !contains(got, `"status": "done"`) {
		t.Fatalf("the write stores status: %s", got)
	}
}

// An undo receipt written before the rename migrates the same way.
func TestLegacyUndoSnapshotLoads(t *testing.T) {
	b, e := migrateLegacy([]byte(`{"rows":[{"id":"x","state":"parked","history":[{"id":"e","by":{},"kind":"state"}]}],"action":"state"}`))
	if e != nil {
		t.Fatal(e)
	}
	var snap Snapshot
	if e = json.Unmarshal(b, &snap); e != nil || snap.Rows[0].Status != "parked" || snap.Rows[0].History[0].Kind != "status" {
		t.Fatalf("%v %+v", e, snap)
	}
}

func contains(s, sub string) bool { return len(sub) > 0 && strings.Contains(s, sub) }
