package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

type cliFixture struct {
	t         *testing.T
	dir       string
	a         *App
	out, errs *bytes.Buffer
	calls     *[][]string
}

func newCLI(t *testing.T) *cliFixture {
	t.Helper()
	dir := fixture(t)
	out, errs := &bytes.Buffer{}, &bytes.Buffer{}
	a, e := newApp(dir, &bytes.Buffer{}, out, errs)
	if e != nil {
		t.Fatal(e)
	}
	calls := [][]string{}
	a.run = func(_ context.Context, argv []string) ([]byte, error) {
		calls = append(calls, append([]string{}, argv...))
		if len(argv) > 1 && argv[1] == "focus-window" {
			return nil, nil
		}
		return []byte(`{"windows":[{"window_id":"w1","agent_state":"working"},{"window_id":"w2","agent_state":"needs_input"}]}`), nil
	}
	return &cliFixture{t, dir, a, out, errs, &calls}
}
func (f *cliFixture) call(args ...string) any {
	f.t.Helper()
	f.out.Reset()
	f.errs.Reset()
	// Same global argument extraction and oden+shell path as Main; injecting the
	// runner keeps live/focus commands away from the owner's daemon.
	words, dir, e := globalData(append([]string{"--data", f.dir}, args...))
	if e != nil || dir != f.dir {
		f.t.Fatal(e)
	}
	if e = dispatch(f.a, words); e != nil {
		f.t.Fatalf("%v: %v; %s", args, e, f.errs.String())
	}
	var v any
	if e = json.Unmarshal(f.out.Bytes(), &v); e != nil {
		f.t.Fatalf("%v: %v: %s", args, e, f.out.String())
	}
	return v
}
func (f *cliFixture) lane(args ...string) map[string]any {
	f.t.Helper()
	v := f.call(append([]string{"lane"}, args...)...)
	m, ok := v.(map[string]any)
	if !ok {
		f.t.Fatalf("not lane: %#v", v)
	}
	return m
}
func (f *cliFixture) fail(want string, args ...string) {
	f.t.Helper()
	f.out.Reset()
	words, _, e := globalData(append([]string{"--data", f.dir}, args...))
	if e != nil {
		f.t.Fatal(e)
	}
	e = dispatch(f.a, words)
	if e == nil || !strings.Contains(e.Error(), want) {
		f.t.Fatalf("%v: want %q, got %v", args, want, e)
	}
}
func TestCLIVerbs(t *testing.T) {
	f := newCLI(t)
	f.lane("add", "root", "--wording", "owner exact\nwords")
	f.lane("add", "child", "--parent", "root")
	f.lane("set", "child", "estimate", "90 min")
	for _, p := range editable {
		if p == "estimate" {
			continue
		}
		value := "value " + p
		if p == "depends" {
			value = `["root","external event"]`
		}
		f.lane("set", "child", p, value)
	}
	if got := f.call("lane", "get", "child", "estimate"); got != "90 min" {
		t.Fatal(got)
	}
	f.lane("rename", "child", "renamed")
	f.lane("reword", "child", "amended wording")
	f.lane("move", "child", "")
	f.lane("move", "child", "root")
	f.fail("cycle", "lane", "move", "root", "child")
	copied := f.lane("copy", "child", "retry")
	if copied["state"] != "planned" {
		t.Fatal(copied)
	}
	f.lane("start", "child", "--pane", "w1", "--session", "s", "--branch", "branch", "--worktree", "/tmp/o-lane-core/wt", "--harness", "test")
	f.lane("associate", "child", "w2", "--session", "s", "--role", "reviewer")
	if got := f.lane("live", "root")["state"]; got != "needs input" {
		t.Fatal(got)
	}
	f.lane("focus-pane", "child")
	found := false
	for _, cmd := range *f.calls {
		if reflect.DeepEqual(cmd, []string{"tuios", "focus-window", "-s", "s", "w2"}) {
			found = true
		}
	}
	if !found {
		t.Fatal(*f.calls)
	}
	f.lane("unassociate", "child", "w2")
	if q := mustResolve(t, f.a.store, "child"); len(q.Panes) != 1 {
		t.Fatalf("unassociate by window ID left the pane: %+v", q.Panes)
	}
	f.call("pane", "associate", "w2", "child", "--session", "s", "--role", "reviewer")
	if got := f.call("pane", "lanes", "w2", "--session", "s").([]any); len(got) != 1 {
		t.Fatal(got)
	}
	f.call("pane", "unassociate", "w2", "child", "--session", "s")
	f.lane("associate", "child") // no pane is recordable and unassociated
	f.lane("note", "child", "a note")
	f.lane("link", "child", "report", "/tmp/o-lane-core/report.md")
	f.lane("unlink", "child", "report", "/tmp/o-lane-core/report.md")
	for _, verb := range []string{"park", "review", "merge", "finish", "cancel", "reopen"} {
		args := []string{verb, "child"}
		if verb == "merge" {
			args = append(args, "--commit", "abc123")
		}
		f.lane(args...)
	}
	f.lane("state", "child", "done")
	f.lane("state", "child", "--from-tag", "LANE-DONE")
	f.lane("state", "child", "--from-tag", "APPROVAL")
	f.call("lane", "history", "child")
	f.call("lane", "list")
	f.call("lane", "tree", "--open")
	shown := f.lane("show", "child")
	if shown["state"] != "review" || shown["started"] == nil || shown["finished"] == nil {
		t.Fatal(shown)
	}
	if got := f.call("lane", "yank-id", "child"); got != "child" {
		t.Fatal(got)
	}
	if got := f.call("lane", "yank-path", "child"); got != "/tmp/o-lane-core/wt" {
		t.Fatal(got)
	}
	f.lane("delete", "retry")
	f.call("lane", "undo")
	f.lane("show", "retry")
	f.call("lane", "define", "--json", `{"lanes":[{"name":"defined"},{"name":"nested","parent":"defined"}]}`)
}
func TestCLIStateAsData(t *testing.T) {
	f := newCLI(t)
	raw := `[{"name":"queued","open":true},{"name":"testing","starts":true,"open":true},{"name":"released","ends":true}]`
	if _, e := f.a.r.Settings().SetSession("lanes.states", raw); e != nil {
		t.Fatal(e)
	}
	q := f.lane("add", "custom")
	if q["state"] != "queued" {
		t.Fatal(q)
	}
	f.lane("state", "custom", "testing")
	f.lane("state", "custom", "released")
	v := f.lane("show", "custom")
	if v["started"] == nil || v["finished"] == nil {
		t.Fatal(v)
	}
	f.fail("unknown state", "lane", "state", "custom", "bogus")
}
func TestCLIRightsAndActors(t *testing.T) {
	f := newCLI(t)
	f.lane("add", "owner")
	actor := Actor{Kind: "agent", Name: "implementer", Harness: "test", Pane: "w", Session: "s", Model: "test-model"}
	f.a.actor = actor
	for _, v := range f.a.specs() {
		op := f.a.r.Operations[v.noun+"."+v.name]
		right := f.a.r.AgentRight(op, false)
		want := ochazuke.AgentAllow
		if never[v.name] {
			want = ochazuke.AgentNever
		}
		if right.Right != want {
			t.Fatalf("%s: %v", op.Key(), right)
		}
	}
	f.lane("note", "owner", "agent note")
	q := mustResolve(t, f.a.store, "owner")
	if q.History[len(q.History)-1].By != actor {
		t.Fatal(q.History)
	}
	f.fail("verb declaration", "lane", "delete", "owner")
	f.fail("verb declaration", "lane", "reword", "owner", "replacement")
	if _, e := f.a.r.Settings().SetSession("agents.rights", `[{"match":"lane.note","right":"never"}]`); e != nil {
		t.Fatal(e)
	}
	f.fail("agents.rights[0]", "lane", "note", "owner", "blocked")
	if _, e := f.a.r.Settings().SetSession("agents.rights", `[{"match":"lane.reword","right":"allow"}]`); e != nil {
		t.Fatal(e)
	}
	f.lane("reword", "owner", "owner-approved override")
}
func TestCLIActorEnv(t *testing.T) {
	for _, tc := range []struct {
		env           map[string]string
		kind, harness string
	}{
		{map[string]string{}, "owner", ""}, {map[string]string{"TUIOS_PANE_ID": "pane"}, "owner", ""}, {map[string]string{"CODEX_THREAD_ID": "id", "TUIOS_PANE_ID": "pane"}, "agent", "codex"}, {map[string]string{"CLAUDE_CODE_ENTRYPOINT": "cli"}, "agent", "claude-code"}, {map[string]string{"OCHAZUKE_ACTOR": "owner", "AGENT_HARNESS": "codex"}, "owner", ""},
	} {
		a, e := cliActor(func(k string) string { return tc.env[k] })
		if e != nil || a.Kind != tc.kind || a.Harness != tc.harness {
			t.Fatal(a, e, tc)
		}
	}
}
func TestCLILiveOffUnknown(t *testing.T) {
	f := newCLI(t)
	f.lane("add", "live")
	f.lane("associate", "live", "w1", "--session", "s")
	f.a.run = func(context.Context, []string) ([]byte, error) { return nil, errors.New("offline") }
	if got := f.lane("live", "live")["state"]; got != "unknown" {
		t.Fatal(got)
	}
	if _, e := f.a.r.Settings().SetSession("lanes.live", false); e != nil {
		t.Fatal(e)
	}
	f.a.run = func(context.Context, []string) ([]byte, error) { t.Fatal("live-off invoked command"); return nil, nil }
	if got := f.lane("live", "live")["state"]; got != "off" {
		t.Fatal(got)
	}
}
func TestCLIDataAndConfig(t *testing.T) {
	dir := fixture(t)
	var out, errs bytes.Buffer
	if e := Main([]string{"lane", "add", "standalone", "--data", dir}, &bytes.Buffer{}, &out, &errs); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(dir, "roots", "standalone.json")); e != nil {
		t.Fatal(e)
	}
	_, d, e := globalData(nil)
	want := filepath.Join(os.Getenv("XDG_DATA_HOME"), "o-lane")
	if e != nil || d != want {
		t.Fatal(d, e)
	}
	if _, _, e = globalData([]string{"--data"}); e == nil {
		t.Fatal("missing dir accepted")
	}
}
