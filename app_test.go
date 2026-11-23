package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke/widgets/table"
)

func TestSmokeAndCLIProtocol(t *testing.T) {
	// --smoke runs cleanly in headless mode.
	var out, errBuf bytes.Buffer
	if code := run([]string{"--smoke"}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("--smoke exited %d: %s\n%s", code, errBuf.String(), out.String())
	}
	if !strings.Contains(out.String(), "smoke:") || strings.Contains(out.String(), "FAIL") {
		t.Fatalf("unexpected smoke output:\n%s", out.String())
	}

	// --version
	out.Reset()
	errBuf.Reset()
	if code := run([]string{"--version"}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("--version exited %d", code)
	}
	if !strings.Contains(out.String(), "o-lane "+Version) {
		t.Fatalf("version output = %q", out.String())
	}

	// catalog
	out.Reset()
	if code := run([]string{"catalog"}, strings.NewReader(""), &out, &errBuf); code != 0 {
		t.Fatalf("catalog exited %d: %s", code, errBuf.String())
	}
	var cat manifestDoc
	if err := json.Unmarshal(out.Bytes(), &cat); err != nil || cat.Name != "o-lane" || len(cat.Schemas) != 2 {
		t.Fatalf("catalog JSON invalid: %v %+v", err, cat)
	}
	if cat.Schemas[0].Kind != laneKind || cat.Schemas[1].Kind != planKind {
		t.Fatalf("unexpected schema kinds: %+v", cat.Schemas)
	}

	// Isolated temp --data for CLI subcommands.
	tmp := t.TempDir()
	data := filepath.Join(tmp, "data")
	runCLI := func(args ...string) string {
		t.Helper()
		out.Reset()
		errBuf.Reset()
		full := append([]string{"--data", data}, args...)
		if code := run(full, strings.NewReader(""), &out, &errBuf); code != 0 {
			t.Fatalf("%v exited %d: %s", args, code, errBuf.String())
		}
		return out.String()
	}

	// plan define -> plan list -> plan show
	planFile := filepath.Join(tmp, "release.md")
	planSrc := "# Release v2\n\n## Lanes\n\n| Lane | Scope | Repo | Estimate |\n| --- | --- | --- | --- |\n| Build | Compile binary | o-lane | 1h |\n| Ship | Publish notes | o-lane | 30m |\n"
	if err := os.WriteFile(planFile, []byte(planSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	runCLI("plan", "define", planFile, "--project", "ofam")

	var plans []PlanSummary
	if err := json.Unmarshal([]byte(runCLI("plan", "list", "--format", "json")), &plans); err != nil || len(plans) != 1 || plans[0].LaneCount != 3 {
		t.Fatalf("plan list unexpected: %v %+v", err, plans)
	}
	if !strings.Contains(runCLI("plan", "show", plans[0].ID), "Release v2") {
		t.Fatalf("plan show missing title")
	}

	// lane define, state (including custom unknown status!), note, associate, show, list, rollup, archive, undo/redo.
	runCLI("lane", "define", "Adhoc", "--project", "sandbox")
	runCLI("lane", "state", "adhoc", "custom-wild-status", "--note", "never refused")
	var shown View
	if err := json.Unmarshal([]byte(runCLI("lane", "show", "adhoc", "--json")), &shown); err != nil {
		t.Fatal(err)
	}
	if shown.Status != "custom-wild-status" || shown.StatusReason != "never refused" {
		t.Fatalf("custom status not preserved: %+v", shown)
	}

	runCLI("lane", "note", "adhoc", "checked on disk")
	runCLI("lane", "associate", "adhoc", "--pane", "w-1", "--session", "main")
	runCLI("lane", "state", "build", "done")

	var rollups []rollupRow
	if err := json.Unmarshal([]byte(runCLI("rollup", "--format", "json")), &rollups); err != nil || len(rollups) != 2 {
		t.Fatalf("rollup unexpected: %v %+v", err, rollups)
	}
	foundRoot := false
	for _, r := range rollups {
		if r.ID == "release-v2" {
			foundRoot = true
			if r.ChildrenDone != 1 || r.ChildrenTotal != 2 {
				t.Fatalf("unexpected rollup counts: %+v", r)
			}
		}
	}
	if !foundRoot {
		t.Fatalf("missing release-v2 in rollup: %+v", rollups)
	}

	// query returns both lane.lane and lane.plan records.
	var qr queryResult
	if err := json.Unmarshal([]byte(runCLI("query", "custom-wild-status")), &qr); err != nil || qr.Total != 1 || qr.Items[0].Kind != laneKind {
		t.Fatalf("query unexpected: %v %+v", err, qr)
	}

	// archive + undo + redo
	runCLI("lane", "archive", "adhoc")
	var listed []View
	if err := json.Unmarshal([]byte(runCLI("lane", "list", "--format", "json")), &listed); err != nil {
		t.Fatal(err)
	}
	for _, v := range listed {
		if v.ID == "adhoc" {
			t.Fatalf("archived lane should be hidden by default")
		}
	}
	runCLI("lane", "undo")
	if err := json.Unmarshal([]byte(runCLI("lane", "show", "adhoc", "--json")), &shown); err != nil || shown.Status != "custom-wild-status" {
		t.Fatalf("undo did not restore custom status: %v %+v", err, shown)
	}
	runCLI("lane", "redo")
	if err := json.Unmarshal([]byte(runCLI("lane", "show", "adhoc", "--json")), &shown); err != nil || shown.Status != "archived" {
		t.Fatalf("redo did not re-archive: %v %+v", err, shown)
	}
}

func TestTUIActionsAndReadline(t *testing.T) {
	st := testStore(t)
	st.BackupDaily = false
	root := mustAdd(t, st, Spec{Name: "Root", Project: "alpha"})
	mustAdd(t, st, Spec{Name: "Child", Parent: root.ID})

	env := newEnv(st, Config{})
	sh := newShell(env)
	d := newDriver(sh, 120, 32)
	app := func() App { return appOf(sh.State) }

	// Filter + Readline ctrl+u
	d.keys("/")
	d.typeText("Child")
	if app().filteredCount() != 1 {
		t.Fatalf("expected 1 match for Child, got %d", app().filteredCount())
	}
	d.keys("ctrl+u", "enter")
	if app().Query != "" || app().filteredCount() != 2 {
		t.Fatalf("ctrl+u did not clear filter: %q", app().Query)
	}

	// Custom free-form status (`C`) accepts unknown status.
	d.keys("g", "g", "C", "ctrl+u")
	d.typeText("awaiting-vendor")
	d.keys("enter")
	if got := mustResolve(t, st, root.ID).Status; got != "awaiting-vendor" {
		t.Fatalf("expected awaiting-vendor, got %q (status: %s)", got, app().Status)
	}

	// Add note (`n`).
	d.keys("n")
	d.typeText("waiting on upstream")
	d.keys("enter")
	q := mustResolve(t, st, root.ID)
	if len(q.History) == 0 || q.History[len(q.History)-1].Text != "waiting on upstream" {
		t.Fatalf("note not recorded: %+v", q.History)
	}

	// Sort reverse (`S`).
	d.keys("s", "S")
	if app().List.SortDir != table.SortDesc {
		t.Fatalf("expected SortDesc, got %v", app().List.SortDir)
	}

	// Project filter picker (`g p`).
	d.keys("g", "p", "down", "enter")
	if app().ProjectFilter != "alpha" {
		t.Fatalf("expected project filter alpha, got %q", app().ProjectFilter)
	}
	d.keys("esc")
	if app().ProjectFilter != "" {
		t.Fatalf("esc should clear project filter, got %q", app().ProjectFilter)
	}
}

func TestActorAndStaleDomain(t *testing.T) {
	got, err := cliActor(func(k string) string {
		return map[string]string{"PI_SESSION_ID": "run-1", "MTM_AGENT_NAME": "worker", "OCHAZUKE_ACTOR": "owner"}[k]
	})
	if err != nil || got.Kind != "agent" || got.Harness != "omp" || got.Name != "worker (omp:run-1)" {
		t.Fatalf("cliActor = %+v (%v)", got, err)
	}

	st := testStore(t)
	q := Lane{Spec: Spec{Name: "Work"}, ID: "l1", Status: "running"}
	stale, reason := staleLane(q, st.statuses(), Live{Status: "idle"}, true, time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC))
	if !stale || !strings.Contains(reason, "no pane or agent is associated") {
		t.Fatalf("staleLane = %v, %q", stale, reason)
	}
}
