package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLaneDocRows(t *testing.T) {
	f := newCLI(t)
	f.lane("add", "Implementation")
	f.lane("link", "Implementation", "doc", "/docs/older.md")
	f.lane("link", "Implementation", "report", "/docs/newer.md")
	f.lane("link", "Implementation", "url", "https://example.com/ignored")
	f.lane("associate", "Implementation", "w1", "--session", "s")
	f.lane("associate", "Implementation", "w2", "--session", "s", "--role", "reviewer")
	row := func(verb string) map[string]any {
		t.Helper()
		return f.call("lane", verb).([]any)[0].(map[string]any)["data"].(map[string]any)
	}
	for _, verb := range []string{"rows", "page-rows"} {
		r := row(verb)
		if r["doc"] != "/docs/newer.md" || r["pane"] != "w1" {
			t.Fatalf("%s doc/pane = %v/%v", verb, r["doc"], r["pane"])
		}
	}
	if err := f.a.r.Settings().Set("lanes.doc-link-kinds", []string{"doc"}); err != nil {
		t.Fatal(err)
	}
	if r := row("page-rows"); r["doc"] != "/docs/older.md" {
		t.Fatal(r)
	}
	if err := f.a.r.Settings().Set("lanes.doc-link-kinds", []string{}); err != nil {
		t.Fatal(err)
	}
	if err := f.a.r.Settings().Set("lanes.live", false); err != nil {
		t.Fatal(err)
	}
	if r := row("page-rows"); r["doc"] != "" || r["pane"] != "" {
		t.Fatal(r)
	}
}

func TestLaneDocRowsSkipMovedAndInactivePanes(t *testing.T) {
	a := pageFixture(t)
	seedPage(t, a, json.RawMessage(`{"lanes":[{"name":"Implementation"},{"name":"Other"}],"associations":[{"lane":"Implementation","pane":{"session":"fixture-session","window":"idle","role":"reviewer"}},{"lane":"Implementation","pane":{"session":"fixture-session","window":"working","role":"implementer"}}],"live_windows":[{"window_id":"idle","agent_state":"idle"},{"window_id":"working","agent_state":"working"}]}`))
	rows, err := loadLaneRows(context.Background(), a.store, a.pageOptions(), nil, a.run, tuiosPaneCommands())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, r := range rows {
		got[r.Label] = r.Data.(map[string]any)["pane"]
	}
	if !reflect.DeepEqual(got, map[string]any{"Implementation": "working", "Other": ""}) {
		t.Fatal(got)
	}
	if _, err := a.store.Associate("Other", Pane{Session: "fixture-session", Window: "working", Role: "implementer"}, false); err != nil {
		t.Fatal(err)
	}
	rows, err = loadLaneRows(context.Background(), a.store, a.pageOptions(), nil, a.run, tuiosPaneCommands())
	if err != nil {
		t.Fatal(err)
	}
	got = map[string]any{}
	for _, r := range rows {
		got[r.Label] = r.Data.(map[string]any)["pane"]
	}
	if !reflect.DeepEqual(got, map[string]any{"Implementation": "", "Other": "working"}) {
		t.Fatal(got)
	}
}

func TestProjectSourceHostAndStandalone(t *testing.T) {
	f := newCLI(t)
	if err := f.a.r.Settings().Set("lanes.project-source", "mtm"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCHAZUKE_HOST", "")
	f.a.run = func(context.Context, []string) ([]byte, error) { return []byte(`[{"name":"Standalone"}]`), nil }
	rows, err := f.a.sourceProjects(context.Background())
	if err != nil || !reflect.DeepEqual(rows, []projectRow{{Ref: "Standalone", Name: "Standalone", Source: "mtm"}}) {
		t.Fatal(rows, err)
	}
	bin := filepath.Join(t.TempDir(), "host with spaces")
	// The fixture only answers project.list at this fixture's exact --data.
	script := "#!/bin/sh\nif [ \"$1\" = --data ] && [ \"$2\" = \"" + f.dir + "\" ] && [ \"$3 $4\" = 'project list' ] && [ \"$OCHAZUKE_CALLER\" = lane ]; then\n echo '[{\"name\":\"Hosted\"}]'\nelse exit 9; fi\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	argv, _ := json.Marshal([]string{bin, "--data", f.dir})
	t.Setenv("OCHAZUKE_HOST", string(argv))
	t.Setenv("OCHAZUKE_EXTENSION", "lane")
	rows, err = f.a.sourceProjects(context.Background())
	if err != nil || !reflect.DeepEqual(rows, []projectRow{{Ref: "Hosted", Name: "Hosted", Source: "mtm"}}) {
		t.Fatal(rows, err)
	}
	t.Setenv("OCHAZUKE_HOST", "not JSON")
	_, err = f.a.sourceProjects(context.Background())
	if err == nil || !strings.Contains(err.Error(), "OCHAZUKE_HOST must be a nonempty JSON argv") {
		t.Fatal(err)
	}
}
