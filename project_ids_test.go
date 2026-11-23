package main

import (
	"os"
	"path/filepath"
	"testing"
)

var testHostProjects = []hostProject{
	{ID: "1", Name: "Alpha"},
	{ID: "2", Name: "Beta"},
	{ID: "3", Name: "Dup"},
	{ID: "4", Name: "dup"},
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
	st := testStore(t)
	st.BackupDaily = false
	for _, row := range [][2]string{{"a", "alpha"}, {"b", "mtm:2"}, {"c", "1"}, {"d", "ghost"}, {"e", "DUP"}, {"f", ""}} {
		mustAdd(t, st, Spec{Name: row[0], Project: row[1]})
	}
	before := rootBytes(t, st.Dir)

	rep, err := st.ResolveProjects(testHostProjects, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 6 || rep.Empty != 1 || rep.AlreadyID != 1 || rep.KindToID != 1 || rep.NameToID != 1 || rep.Rewritten != 2 {
		t.Fatalf("dry run counts: %+v", rep)
	}
	if len(rep.Unmatched) != 1 || rep.Unmatched[0].Name != "ghost" {
		t.Fatalf("unmatched: %+v", rep.Unmatched)
	}
	if len(rep.Ambiguous) != 1 || rep.Ambiguous[0].Name != "DUP" {
		t.Fatalf("ambiguous: %+v", rep.Ambiguous)
	}
	for name, b := range rootBytes(t, st.Dir) {
		if before[name] != b {
			t.Fatalf("dry run wrote %s", name)
		}
	}

	rep, err = st.ResolveProjects(testHostProjects, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rewritten != 2 || rep.Backup == "" {
		t.Fatalf("real run: %+v", rep)
	}
	if _, e := os.Stat(filepath.Join(rep.Backup, "roots")); e != nil {
		t.Fatal("no backup:", e)
	}
	after := rootBytes(t, st.Dir)
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
		if q := mustResolve(t, st, id); q.Project != want[0] || q.ProjectLabel != want[1] {
			t.Fatalf("lane %s: project %q label %q, want %v", id, q.Project, q.ProjectLabel, want)
		}
	}
	rep, err = st.ResolveProjects(testHostProjects, false)
	if err != nil || rep.Rewritten != 0 || rep.Backup != "" {
		t.Fatalf("second run: %+v (%v)", rep, err)
	}
}

func TestProjectNameResolvesOnWrite(t *testing.T) {
	st := testStore(t)
	st.BackupDaily = false
	st.ProjectRef = func(ref string) projectMatch { return resolveProjectRef(testHostProjects, ref) }

	mustAdd(t, st, Spec{Name: "x", Project: "beta"})
	mustAdd(t, st, Spec{Name: "y", Project: "dup"})
	if q := mustResolve(t, st, "x"); q.Project != "2" || q.ProjectLabel != "Beta" {
		t.Fatalf("stored %q (%q)", q.Project, q.ProjectLabel)
	}
	if q := mustResolve(t, st, "y"); q.Project != "dup" {
		t.Fatalf("ambiguous name stored as %q", q.Project)
	}
	if v := st.view(mustResolve(t, st, "x"), nil); v.EffectiveProject != "Beta" || v.EffectiveProjectID != "2" {
		t.Fatalf("view = %+v", v)
	}
}
