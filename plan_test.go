package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

func testColumns(t *testing.T) map[string]string {
	t.Helper()
	columns, e := parseImportColumns(defaultImportColumns)
	if e != nil {
		t.Fatal(e)
	}
	return columns
}

func TestPlanTableGolden(t *testing.T) {
	fixture(t)
	for _, name := range []string{"standard", "est", "lane-time"} {
		t.Run(name, func(t *testing.T) {
			src, e := os.ReadFile(filepath.Join("testdata", "plans", name+".md"))
			if e != nil {
				t.Fatal(e)
			}
			p, e := parsePlan("plan.md", src, testColumns(t))
			if e != nil {
				t.Fatal(e)
			}
			actual, e := json.MarshalIndent(p, "", "  ")
			if e != nil {
				t.Fatal(e)
			}
			want, e := os.ReadFile(filepath.Join("testdata", "plans", name+".golden.json"))
			if e != nil {
				t.Fatal(e)
			}
			var gotJSON, wantJSON any
			if e = json.Unmarshal(actual, &gotJSON); e != nil {
				t.Fatal(e)
			}
			if e = json.Unmarshal(want, &wantJSON); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(gotJSON, wantJSON) {
				t.Fatalf("plan differs from golden:\n%s", actual)
			}
		})
	}
}

func TestPlanColumnsAndMarkdown(t *testing.T) {
	fixture(t)
	columns, e := parseImportColumns(`{"STAGE":"name","Effort":"estimate","Work":"scope","Tests":"tests","Tests and stories":"done_when"}`)
	if e != nil {
		t.Fatal(e)
	}
	src := "# **Portable** plan\n\n```md\n| Stage | Effort |\n|---|---|\n| fake | 1 h |\n```\n\n## Lanes\n\n| Stage | Effort | Work | Tests and stories (keys) | Tests |\n|---|---|---|---|---|\n| **Exact** `stage` | ~1–2 h | `a\\|b` and **details** | ready | x → y |\n"
	p, e := parsePlan("x.md", []byte(src), columns)
	if e != nil {
		t.Fatal(e)
	}
	if p.Name != "Portable plan" || len(p.Lanes) != 1 {
		t.Fatal(p)
	}
	q := p.Lanes[0]
	if q.Name != "Exact stage" || q.Estimate != "~1–2 h" || q.Scope != "`a\\|b` and **details**" || q.DoneWhen != "ready" || q.Tests != "x → y" {
		t.Fatalf("lost source fields: %+v", q)
	}
	for _, raw := range []string{`{}`, `null`, `{"lane":"bogus"}`, `{"Lane":"name","lane":"scope"}`, `{"":"name"}`} {
		if _, e = parseImportColumns(raw); e == nil {
			t.Errorf("accepted columns %s", raw)
		}
	}
	for _, body := range []string{
		"| Lane | Estimate | Est. |\n|---|---|---|\n| A | 1 h | 2 h |",
		"| Lane | Scope |\n|---|---|\n| A | first |\n| **A** | second |",
		"| Lane | Scope |\n|---|---|\n| | blank |",
	} {
		if _, e = parsePlan("x.md", []byte(body), testColumns(t)); e == nil {
			t.Errorf("accepted invalid table: %s", body)
		}
	}
	ordered, e := parsePlan("x.md", []byte("## Lanes\n\n| Estimate | Lane | Scope |\n|---|---|---|\n| 2 h | **a** | body |"), testColumns(t))
	if e != nil || len(ordered.Lanes) != 1 || ordered.Lanes[0].Name != "a" || ordered.Lanes[0].Estimate != "2 h" {
		t.Fatal(ordered, e)
	}
	// A table under Lanes takes precedence over earlier design tables.
	p, e = parsePlan("x.md", []byte("## Jobs from earlier feature lanes\n\n| Job | Lane | Scope |\n|---|---|---|\n| J1 | current | design |\n\n## Lanes\n\n| Lane | Scope |\n|---|---|\n| current | work |"), testColumns(t))
	if e != nil || len(p.Lanes) != 1 || p.Lanes[0].Name != "current" {
		t.Fatal(p, e)
	}
}

func writePlan(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if e := os.WriteFile(path, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	return path
}

const smallPlan = "# Main task\n\n## Lanes\n\n| Lane | Scope | Estimate | Depends on |\n|---|---|---|---|\n| **core-ui** | first | 1 h | external event |\n| core | second | 30 min | core-ui |\n"

func TestPlanDefineCLI(t *testing.T) {
	f := newCLI(t)
	path := writePlan(t, t.TempDir(), "example.md", smallPlan)
	f.call("lane", "define", "--plan", path, "--wording", "exact owner\nwords", "--task", "task-1")
	rows, e := f.a.store.List()
	if e != nil || len(rows) != 3 {
		t.Fatal(rows, e)
	}
	root := mustResolve(t, f.a.store, "Main task")
	core := mustResolve(t, f.a.store, "core")
	ui := mustResolve(t, f.a.store, "core-ui")
	if core.Parent != root.ID || ui.Parent != root.ID || root.Wording != "exact owner\nwords" || root.Task != "task-1" {
		t.Fatal(rows)
	}
	if core.Scope != "second" || ui.Scope != "first" || !reflect.DeepEqual(core.Depends, []string{"core-ui"}) {
		t.Fatal(rows)
	}
	f.call("plan", "define", path, "--wording", "must not replace")
	after, _ := f.a.store.List()
	if !reflect.DeepEqual(rows, after) {
		t.Fatal("unchanged redefine changed records or history")
	}
	writePlan(t, filepath.Dir(path), filepath.Base(path), strings.Replace(smallPlan, "first | 1 h", "updated | 2 h", 1))
	f.call("plan", "define", path)
	if mustResolve(t, f.a.store, ui.ID).Scope != "updated" || mustResolve(t, f.a.store, root.ID).Wording != "exact owner\nwords" {
		t.Fatal("redefine failed")
	}
	linked := f.call("plan", "lanes", path).([]any)
	if len(linked) != 3 {
		t.Fatal(linked)
	}
	if len(f.call("plan", "lanes", path+"-other").([]any)) != 0 {
		t.Fatal("plan prefix matched")
	}
	// Removing a table row never deletes the existing lane.
	writePlan(t, filepath.Dir(path), filepath.Base(path), strings.Replace(smallPlan, "| core | second | 30 min | core-ui |\n", "", 1))
	f.call("lane", "define", "--plan", path)
	if rows, _ = f.a.store.List(); len(rows) != 3 {
		t.Fatal(rows)
	}
	if e = f.a.store.Undo(); e != nil {
		t.Fatal(e)
	}
	if mustResolve(t, f.a.store, ui.ID).Scope != "updated" {
		t.Fatal("plan redefine not undoable")
	}
}

func TestDefineJSONPresenceAndExactNames(t *testing.T) {
	f := newCLI(t)
	f.call("lane", "define", "--json", `{"lanes":[{"name":"core-ui","scope":"keep"},{"name":"core","scope":"old","repo":"keep","depends":["x"],"wording":"owner"}]}`)
	f.call("lane", "define", "--json", `{"lanes":[{"name":"core","scope":"","depends":[],"wording":"replace"}]}`)
	q := mustResolve(t, f.a.store, "core")
	if q.Scope != "" || len(q.Depends) != 0 || q.Repo != "keep" || q.Wording != "owner" || mustResolve(t, f.a.store, "core-ui").Scope != "keep" {
		t.Fatal(q)
	}
	before, _ := f.a.store.List()
	f.call("lane", "define", "--json", `{"lanes":[{"name":"core","scope":"","depends":[]}]}`)
	after, _ := f.a.store.List()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("empty-value repeat wrote history")
	}
	f.fail("duplicate definition", "lane", "define", "--json", `{"lanes":[{"name":"new"},{"name":"new"}]}`)
	f.fail("exactly one", "lane", "define")
	f.fail("exactly one", "lane", "define", "--plan", "x", "--json", `{"lanes":[]}`)
}

func TestPlanDryRunDirectoryAndSettings(t *testing.T) {
	f := newCLI(t)
	dir := t.TempDir()
	path := writePlan(t, dir, "a.md", smallPlan)
	writePlan(t, dir, "b.md", "# No lanes\n")
	writePlan(t, dir, "c.md", "| Lane | Scope |\n|---|---|\n| same | a |\n| same | b |\n")
	result := f.call("lane", "define", "--plans-dir", dir, "--dry-run").(map[string]any)
	if result["errors"] != float64(1) || len(result["plans"].([]any)) != 3 {
		t.Fatal(result)
	}
	if _, e := os.Stat(f.dir); !os.IsNotExist(e) {
		t.Fatal("dry run touched lane store", e)
	}
	if len(f.call("plan", "define", path, "--dry-run").([]any)) != 3 {
		t.Fatal("missing dry-run specs")
	}
	if _, e := os.Stat(f.dir); !os.IsNotExist(e) {
		t.Fatal("plan dry run touched store", e)
	}
	f.fail("requires dry-run", "lane", "define", "--plans-dir", dir)
	if _, e := f.a.r.Settings().SetSession("lanes.plans-dir", dir); e != nil {
		t.Fatal(e)
	}
	f.call("plan", "define", "a")
	if len(f.call("plan", "lanes", "a").([]any)) != 3 {
		t.Fatal("configured plan lookup failed")
	}
	if _, e := f.a.r.Settings().SetSession("lanes.import-columns", `{"stage":"name","effort":"estimate"}`); e != nil {
		t.Fatal(e)
	}
	custom := writePlan(t, dir, "custom.md", "| Stage | Effort |\n|---|---|\n| exact | 90 min |\n")
	f.call("plan", "define", custom)
	if mustResolve(t, f.a.store, "exact").Estimate != "90 min" {
		t.Fatal("custom columns ignored")
	}
}

func TestPlanAgentRights(t *testing.T) {
	f := newCLI(t)
	path := writePlan(t, t.TempDir(), "plan.md", smallPlan)
	f.a.actor = Actor{Kind: "agent", Name: "worker", Harness: "test"}
	f.call("plan", "define", path)
	q := mustResolve(t, f.a.store, "core")
	if q.History[0].By.Kind != "agent" {
		t.Fatal(q.History)
	}
	f.fail("verb declaration", "lane", "reword", q.ID, "no")
	f.fail("verb declaration", "lane", "delete", q.ID)
	for _, key := range []string{"lane.define", "plan.define", "plan.lanes"} {
		if f.a.r.AgentRight(f.a.r.Operations[key], false).Right != ochazuke.AgentAllow {
			t.Fatalf("%s not allowed", key)
		}
	}
}

func TestMCPPlanDefine(t *testing.T) {
	dir := fixture(t)
	path := writePlan(t, t.TempDir(), "example.md", smallPlan)
	var input, out, errs bytes.Buffer
	enc := json.NewEncoder(&input)
	send := func(id int, method string, params any) {
		t.Helper()
		if e := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); e != nil {
			t.Fatal(e)
		}
	}
	send(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "plan-test", "version": "1"}})
	if e := enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); e != nil {
		t.Fatal(e)
	}
	for i, req := range []map[string]any{
		{"verb": "plan.define", "args": map[string]any{"plan": path}, "dry_run": true},
		{"verb": "plan.lanes", "args": map[string]any{"plan": path}},
		{"verb": "plan.define", "args": map[string]any{"plan": path, "wording": "owner words"}},
		{"verb": "lane.define", "args": map[string]any{"plan": path, "wording": "must stay original"}},
		{"verb": "plan.lanes", "args": map[string]any{"plan": path}},
	} {
		send(i+2, "tools/call", map[string]any{"name": "run", "arguments": req})
	}
	if e := Main([]string{"--data", dir, "mcp", "serve"}, &input, &out, &errs); e != nil {
		t.Fatal(e, errs.String())
	}
	dec := json.NewDecoder(&out)
	for {
		var reply struct {
			ID     int `json:"id"`
			Result struct {
				Content map[string]any `json:"structuredContent"`
			} `json:"result"`
		}
		e := dec.Decode(&reply)
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e, out.String())
		}
		if reply.ID == 1 {
			continue
		}
		if reply.Result.Content["status"] != "done" {
			t.Fatal(reply)
		}
		if reply.ID == 3 {
			// Framework dry-run did not execute the define handler.
			rows, ok := reply.Result.Content["result"].([]any)
			if !ok || len(rows) != 0 {
				t.Fatalf("dry run created records: %v", reply.Result.Content)
			}
		}
	}
	s := &Store{Dir: dir}
	rows, e := s.List()
	if e != nil || len(rows) != 3 {
		t.Fatal(rows, e)
	}
	for _, q := range rows {
		if len(q.History) != 1 || q.History[0].By.Kind != "agent" {
			t.Fatal("identity/idempotency lost", q)
		}
	}
	if mustResolve(t, s, "Main task").Wording != "owner words" {
		t.Fatal("reworded through define")
	}
}
