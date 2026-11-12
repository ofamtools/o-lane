package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const hostProjectsJSON = `{"items":[{"id":"1","name":"Alpha"},{"id":"2","name":"Beta"},{"id":"3","name":"Dup"},{"id":"4","name":"dup"}]}`

func withHostProjects(f *cliFixture) {
	f.a.run = func(_ context.Context, argv []string) ([]byte, error) {
		if strings.Join(argv, " ") != "mtm project list" {
			return nil, os.ErrNotExist
		}
		return []byte(hostProjectsJSON), nil
	}
	f.a.projectsAt = f.a.projectsAt.Add(-1 << 40) // forget a cached list
}

func rootBytes(t *testing.T, dir string) map[string]string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "roots", "*.json"))
	out := map[string]string{}
	for _, p := range files {
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		out[filepath.Base(p)] = string(b)
	}
	return out
}

// The migration rewrites what names exactly one project, keeps the name,
// leaves every other row byte for byte, and a second run does nothing.
func TestProjectResolveMigration(t *testing.T) {
	f := newCLI(t)
	// Stored before the host's projects were known: as typed.
	for _, row := range [][2]string{{"a", "alpha"}, {"b", "mtm:2"}, {"c", "1"}, {"d", "ghost"}, {"e", "DUP"}, {"f", ""}} {
		args := []string{"lane", "add", row[0]}
		if row[1] != "" {
			args = append(args, "--project", row[1])
		}
		f.call(args...)
	}
	before := rootBytes(t, f.dir)
	withHostProjects(f)
	rep := f.call("project", "resolve", "--dry-run").(map[string]any)
	if rep["rows"] != 6.0 || rep["empty"] != 1.0 || rep["already_id"] != 1.0 || rep["kind_id_to_id"] != 1.0 || rep["name_to_id"] != 1.0 || rep["rewritten"] != 2.0 {
		t.Fatalf("dry run counts: %v", rep)
	}
	if un := rep["unmatched"].([]any); len(un) != 1 || un[0].(map[string]any)["name"] != "ghost" {
		t.Fatalf("unmatched: %v", rep["unmatched"])
	}
	if am := rep["ambiguous"].([]any); len(am) != 1 || am[0].(map[string]any)["name"] != "DUP" {
		t.Fatalf("ambiguous: %v", rep["ambiguous"])
	}
	for name, b := range rootBytes(t, f.dir) {
		if before[name] != b {
			t.Fatalf("dry run wrote %s", name)
		}
	}
	rep = f.call("project", "resolve").(map[string]any)
	if rep["rewritten"] != 2.0 || rep["backup"] == "" {
		t.Fatalf("real run: %v", rep)
	}
	if _, e := os.Stat(filepath.Join(rep["backup"].(string), "roots")); e != nil {
		t.Fatal("no backup:", e)
	}
	after := rootBytes(t, f.dir)
	changed := 0
	for name, b := range after {
		if before[name] != b {
			changed++
		}
	}
	if changed != 2 {
		t.Fatalf("%d root files changed, want 2 (a and b)", changed)
	}
	for id, want := range map[string][2]string{"a": {"1", "Alpha"}, "b": {"2", "Beta"}, "c": {"1", ""}, "d": {"ghost", ""}, "e": {"DUP", ""}} {
		if q := mustResolve(t, f.a.store, id); q.Project != want[0] || q.ProjectLabel != want[1] {
			t.Fatalf("lane %s: project %q label %q, want %v", id, q.Project, q.ProjectLabel, want)
		}
	}
	if rep = f.call("project", "resolve").(map[string]any); rep["rewritten"] != 0.0 || rep["backup"] != nil && rep["backup"] != "" {
		t.Fatalf("second run: %v", rep)
	}
}

// A name typed on write is stored as the project's id; the list shows and
// filters by the name; an ambiguous name stays as typed.
func TestProjectNameResolvesOnWriteAndShowsAsName(t *testing.T) {
	f := newCLI(t)
	withHostProjects(f)
	f.call("lane", "add", "x", "--project", "beta")
	f.call("lane", "add", "y", "--project", "dup")
	if q := mustResolve(t, f.a.store, "x"); q.Project != "2" || q.ProjectLabel != "Beta" {
		t.Fatalf("stored %q (%q)", q.Project, q.ProjectLabel)
	}
	if q := mustResolve(t, f.a.store, "y"); q.Project != "dup" {
		t.Fatalf("ambiguous name stored as %q", q.Project)
	}
	rows := f.call("lane", "rows", "--project", "BETA").([]any)
	if len(rows) != 1 {
		t.Fatalf("rows by name: %v", rows)
	}
	got := f.call("lane", "list").([]any)
	for _, r := range got {
		if m := r.(map[string]any); m["name"] == "x" && (m["effective_project"] != "Beta" || m["effective_project_id"] != "2") {
			t.Fatalf("list row: %v", m)
		}
	}
	// The project is gone from the host: the name kept on the lane is shown.
	f.a.run = func(context.Context, []string) ([]byte, error) { return []byte(`[]`), nil }
	f.a.projectsAt = f.a.projectsAt.Add(-1 << 40)
	if v := f.a.store.view(mustResolve(t, f.a.store, "x"), nil); v.EffectiveProject != "Beta" {
		t.Fatalf("gone project shown as %q", v.EffectiveProject)
	}
}

// With no host projects nothing is rewritten and nothing errors.
func TestProjectStandaloneKeepsNames(t *testing.T) {
	f := newCLI(t)
	if e := f.a.r.Settings().Set("lanes.project-source", "none"); e != nil {
		t.Fatal(e)
	}
	f.call("lane", "add", "x", "--project", "Beta")
	if q := mustResolve(t, f.a.store, "x"); q.Project != "Beta" || q.ProjectLabel != "" {
		t.Fatalf("standalone stored %q (%q)", q.Project, q.ProjectLabel)
	}
	if v := f.a.store.view(mustResolve(t, f.a.store, "x"), nil); v.EffectiveProject != "Beta" {
		t.Fatalf("shown as %q", v.EffectiveProject)
	}
}
