package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStoreDefineExactParents(t *testing.T) {
	s := testStore(t)
	mustAdd(t, s, Spec{Name: "core-ui"})
	specs := []Spec{{Name: "child", Parent: "core"}, {Name: "core"}}
	for i := 0; i < 2; i++ {
		if _, e := s.Define(specs); e != nil {
			t.Fatal(e)
		}
	}
	rows, e := s.List()
	if e != nil || len(rows) != 3 {
		t.Fatal(rows, e)
	}
	if q := mustResolve(t, s, "child"); q.Parent != "core" {
		t.Fatalf("prefix parent won: %+v", q)
	}
	if _, e = s.Add(Spec{Name: "bad-prefix", Parent: "core-u"}); e == nil {
		t.Fatal("add accepted a parent prefix")
	}
	// A batch name also wins over a pre-existing exact ID of the same text.
	mustAdd(t, s, Spec{Name: "collision"})
	if _, e = s.Rename("collision", "old name"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Define([]Spec{{Name: "nested", Parent: "collision"}, {Name: "collision", Parent: "core"}}); e != nil {
		t.Fatal(e)
	}
	if q := mustResolve(t, s, "nested"); q.Parent != "collision-2" {
		t.Fatalf("batch parent did not win: %+v", q)
	}
}
func TestCLIReviewAgentRunIdentity(t *testing.T) {
	s := testStore(t)
	q := mustAdd(t, s, Spec{Name: "root"})
	t.Setenv("OCHAZUKE_ACTOR", "")
	for _, kind := range []struct{ key, harness string }{{"CODEX_THREAD_ID", "codex"}, {"CLAUDE_SESSION_ID", "claude-code"}} {
		t.Setenv(kind.key, "run-a")
		if kind.harness == "claude-code" {
			t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
		}
		a, e := cliActor(os.Getenv)
		if e != nil {
			t.Fatal(e)
		}
		s.Actor = a
		if _, e = s.Note(q.ID, "run a"); e != nil {
			t.Fatal(e)
		}
		t.Setenv(kind.key, "run-b")
		b, e := cliActor(os.Getenv)
		if e != nil {
			t.Fatal(e)
		}
		s.Actor = b
		if e = s.Undo(); e == nil || !strings.Contains(e.Error(), "history.own-entry") {
			t.Fatalf("run b undid run a: %v; %v %v", e, a, b)
		}
		s.Actor = a
		if e = s.Undo(); e != nil {
			t.Fatal(e)
		}
		t.Setenv(kind.key, "")
		t.Setenv("CLAUDE_CODE_ENTRYPOINT", "")
	}
	t.Setenv("AGENT_HARNESS", "codex")
	anonymous, e := cliActor(os.Getenv)
	if e != nil {
		t.Fatal(e)
	}
	s.Actor = anonymous
	if anonymous.Name != "" {
		t.Fatalf("anonymous name: %+v", anonymous)
	}
	if _, e = s.Note(q.ID, "anonymous"); e != nil {
		t.Fatal(e)
	}
	if e = s.Undo(); e == nil {
		t.Fatal("anonymous agent claimed a change")
	}
}
func TestCLIReviewNoOwnerEscalation(t *testing.T) {
	dir := fixture(t)
	var out, errs bytes.Buffer
	if e := Main([]string{"--data", dir, "lane", "add", "root"}, &bytes.Buffer{}, &out, &errs); e != nil {
		t.Fatal(e)
	}
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("O_LANE_ACTOR", "owner")
	for _, override := range []string{"", "owner", `{"kind":"owner","name":"forged"}`} {
		t.Setenv("OCHAZUKE_ACTOR", override)
		e := Main([]string{"--data", dir, "lane", "delete", "root"}, &bytes.Buffer{}, &out, &errs)
		if e == nil || !strings.Contains(e.Error(), "verb declaration") {
			t.Fatalf("owner escalation with %q: %v", override, e)
		}
	}
}
func TestStoreUndoKeepsAudit(t *testing.T) {
	s := testStore(t)
	q := mustAdd(t, s, Spec{Name: "root"})
	s.Actor = Actor{Kind: "agent", Name: "author", Harness: "test"}
	if _, e := s.State(q.ID, "running", "started"); e != nil {
		t.Fatal(e)
	}
	before := mustResolve(t, s, q.ID)
	s.Actor = Actor{Kind: "owner", Name: "undoer"}
	if e := s.Undo(); e != nil {
		t.Fatal(e)
	}
	after := mustResolve(t, s, q.ID)
	if after.State != "planned" || len(after.History) != len(before.History)+1 || !reflect.DeepEqual(after.History[:len(before.History)], before.History) {
		t.Fatalf("audit was rewritten: %+v", after)
	}
	b, _ := json.Marshal(after.History[len(after.History)-1])
	var en map[string]any
	_ = json.Unmarshal(b, &en)
	undo, ok := en["undo"].(map[string]any)
	if !ok || en["kind"] != "undo" || undo["action"] != "state" {
		t.Fatal(string(b))
	}
	if undo["original_actor"].(map[string]any)["name"] != "author" || en["by"].(map[string]any)["name"] != "undoer" {
		t.Fatal(string(b))
	}
	if len(undo["entries"].([]any)) != 1 {
		t.Fatal(string(b))
	}
	if v := s.view(after, []Lane{after}); v.Started != nil {
		t.Fatal("undone start still effective")
	}
	if _, e := s.Note(q.ID, "keep note"); e != nil {
		t.Fatal(e)
	}
	if e := s.Undo(); e != nil {
		t.Fatal(e)
	}
	if e := s.Undo(); e != nil {
		t.Fatal(e)
	} // undo creation, retaining its audit tombstone
	var rows []Lane
	if e := s.locked(func() error { var e error; rows, e = s.all(); return e }); e != nil {
		t.Fatal(e)
	}
	if len(rows) != 1 || !rows[0].Deleted || len(rows[0].History) != 6 {
		t.Fatalf("creation undo lost audit: %+v", rows)
	}
}
func TestCLIReviewPresetData(t *testing.T) {
	f := newCLI(t)
	states := `[{"name":"queued","open":true},{"name":"active","starts":true,"open":true},{"name":"waiting","open":true},{"name":"checking","open":true},{"name":"integrated","ends":true},{"name":"released","ends":true},{"name":"abandoned","ends":true}]`
	if _, e := f.a.r.Settings().SetSession("lanes.states", states); e != nil {
		t.Fatal(e)
	}
	presets := `{"start":"active","park":"waiting","review":"checking","merge":"integrated","finish":"released","cancel":"abandoned","reopen":"queued"}`
	if _, e := f.a.r.Settings().SetSession("lanes.state-presets", presets); e != nil {
		t.Fatal(e)
	}
	f.lane("add", "custom")
	for _, tc := range []struct{ verb, state string }{{"start", "active"}, {"park", "waiting"}, {"review", "checking"}, {"merge", "integrated"}, {"finish", "released"}, {"cancel", "abandoned"}, {"reopen", "queued"}} {
		if q := f.lane(tc.verb, "custom"); q["state"] != tc.state {
			t.Fatal(tc, q)
		}
	}
	if _, e := f.a.r.Settings().SetSession("lanes.state-presets", `{"start":"missing"}`); e != nil {
		t.Fatal(e)
	}
	f.fail("lanes.state-presets", "lane", "start", "custom")
	f.fail("lanes.state-presets", "lane", "finish", "custom")
}
func TestCLIReviewDataPermissions(t *testing.T) {
	dir := fixture(t)
	if e := os.MkdirAll(dir, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(dir, 0755); e != nil {
		t.Fatal(e)
	}
	var out, errs bytes.Buffer
	if e := Main([]string{"--data", dir, "lane", "list"}, &bytes.Buffer{}, &out, &errs); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(dir)
	if e != nil || info.Mode().Perm() != 0755 {
		t.Fatal("existing permissions changed", info, e)
	}
	if !strings.Contains(errs.String(), "warning") || !strings.Contains(errs.String(), "0755") {
		t.Fatal("missing permission warning", errs.String())
	}
}
func TestCLIFixtureClearsIdentity(t *testing.T) {
	for _, k := range []string{"TUIOS_SESSION", "TUIOS_PANE_ID", "TUIOS_HARNESS", "TUIOS_AGENT_NAME", "CLAUDE_CODE_ENTRYPOINT", "CODEX_THREAD_ID", "CLAUDE_SESSION_ID"} {
		t.Setenv(k, "inherited")
	}
	dir := fixture(t)
	if !strings.HasPrefix(dir, os.TempDir()+string(os.PathSeparator)) {
		t.Fatal(dir)
	}
	for _, k := range []string{"TUIOS_SESSION", "TUIOS_PANE_ID", "TUIOS_HARNESS", "TUIOS_AGENT_NAME", "CLAUDE_CODE_ENTRYPOINT", "CODEX_THREAD_ID", "CLAUDE_SESSION_ID"} {
		if os.Getenv(k) != "" {
			t.Fatal("identity inherited", k)
		}
	}
}
func TestCLIPaneMatchSymmetry(t *testing.T) {
	f := newCLI(t)
	f.lane("add", "root")
	f.lane("associate", "root", "window", "--host", "remote", "--session", "session")
	if xs := f.call("pane", "lanes", "window").([]any); len(xs) != 1 {
		t.Fatal("host wildcard differs from unassociate", xs)
	}
	if xs := f.call("pane", "lanes", "window", "--host", "elsewhere").([]any); len(xs) != 0 {
		t.Fatal(xs)
	}
	f.call("pane", "unassociate", "window", "root")
	if xs := f.call("pane", "lanes", "window").([]any); len(xs) != 0 {
		t.Fatal(xs)
	}
}
func TestStoreReopenClearsFinished(t *testing.T) {
	s := testStore(t)
	q := mustAdd(t, s, Spec{Name: "root"})
	if _, e := s.State(q.ID, "done", ""); e != nil {
		t.Fatal(e)
	}
	if v := s.view(mustResolve(t, s, q.ID), nil); v.Finished == nil {
		t.Fatal("not finished")
	}
	if _, e := s.State(q.ID, "running", ""); e != nil {
		t.Fatal(e)
	}
	if v := s.view(mustResolve(t, s, q.ID), nil); v.Finished != nil {
		t.Fatal("stale finish", v.Finished)
	}
}
func TestCLIRunnerWaitDelay(t *testing.T) {
	f := newCLI(t)
	// The child exits but leaves its stdout inherited by another process.
	script := filepath.Join(t.TempDir(), "held-pipe.sh")
	if e := os.WriteFile(script, []byte("#!/bin/sh\nsleep 3 &\n"), 0700); e != nil {
		t.Fatal(e)
	}
	real, e := newApp(f.dir, &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{})
	if e != nil {
		t.Fatal(e)
	}
	at := time.Now()
	_, e = real.run(context.Background(), []string{script})
	if time.Since(at) > 2*time.Second || e == nil {
		t.Fatal("inherited pipe was not bounded", time.Since(at), e)
	}
}

func TestCLIListDeletedAudit(t *testing.T) {
	f := newCLI(t)
	f.lane("add", "record")
	f.lane("delete", "record")
	if rows := f.call("lane", "list").([]any); len(rows) != 0 {
		t.Fatal(rows)
	}
	rows := f.call("lane", "list", "--deleted").([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["deleted"] != true {
		t.Fatal(rows)
	}
	if history := rows[0].(map[string]any)["history"].([]any); len(history) != 2 {
		t.Fatal(history)
	}
}
func TestStoreUndoAuditRecovery(t *testing.T) {
	s := testStore(t)
	mustAdd(t, s, Spec{Name: "root"})
	if _, e := s.Note("root", "survives undo"); e != nil {
		t.Fatal(e)
	}
	// Read the legacy snapshot format too (no affected-entry receipt).
	files, e := s.historyFiles()
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(files[len(files)-1])
	if e != nil {
		t.Fatal(e)
	}
	var snap Snapshot
	if e = json.Unmarshal(b, &snap); e != nil {
		t.Fatal(e)
	}
	snap.Changes = nil
	if e = atomicJSON(files[len(files)-1], snap); e != nil {
		t.Fatal(e)
	}
	s.Remove = func(string) error { return os.ErrPermission }
	if e = s.Undo(); e == nil {
		t.Fatal("expected simulated crash")
	}
	s.Remove = nil
	q := mustResolve(t, s, "root")
	if len(q.History) != 3 || q.History[2].Kind != "undo" || len(activeHistory(q.History)) != 1 {
		t.Fatal(q.History)
	}
	if e = s.Undo(); e != nil {
		t.Fatal(e)
	}
	rows, e := s.ListAll()
	if e != nil || len(rows) != 1 || !rows[0].Deleted || len(rows[0].History) != 4 {
		t.Fatal(rows, e)
	}
}
