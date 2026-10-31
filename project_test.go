package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestProjectRefMatching(t *testing.T) {
	for _, c := range []struct {
		stored, query string
		want          bool
	}{
		{"mtm:abc", "mtm:abc", true},
		{"mtm:abc", "abc", true},
		{"abc", "mtm:abc", true},
		{"mtm:abc", "other:abc", false},
		{"o-lane", "o-lane", true},
		{"Some: Thing", "Some: Thing", true},
		{"", "abc", false},
		{"abc", "", false},
		{"abc", "ab", false},
	} {
		if got := projectMatches(c.stored, c.query); got != c.want {
			t.Errorf("projectMatches(%q, %q) = %v", c.stored, c.query, got)
		}
	}
}

func TestStoreProjectInheritanceAndHistory(t *testing.T) {
	s := testStore(t)
	root := mustAdd(t, s, Spec{Name: "root"})
	child := mustAdd(t, s, Spec{Name: "child", Parent: root.ID})
	leaf := mustAdd(t, s, Spec{Name: "leaf", Parent: child.ID})
	other := mustAdd(t, s, Spec{Name: "other", Parent: root.ID, Project: "own"})
	rows, _ := s.List()
	// Records without a project load as unassociated, never refused.
	if v := s.view(mustResolve(t, s, leaf.ID), rows); v.EffectiveProject != "" || v.ProjectAssociation != "unassociated" {
		t.Fatal(v)
	}
	if _, e := s.AssociateProject(root.ID, "mtm:p1", false); e != nil {
		t.Fatal(e)
	}
	rows, _ = s.List()
	v := s.view(mustResolve(t, s, leaf.ID), rows)
	if v.EffectiveProject != "mtm:p1" || v.ProjectFrom != root.ID || v.ProjectAssociation != "inherited" || v.Project != "" {
		t.Fatal(v)
	}
	if v := s.view(mustResolve(t, s, other.ID), rows); v.EffectiveProject != "own" || v.ProjectAssociation != "associated" {
		t.Fatal(v)
	}
	q := mustResolve(t, s, root.ID)
	last := q.History[len(q.History)-1]
	if last.Kind != "associate" || last.Text != "project" || last.To != "mtm:p1" || last.From != "" {
		t.Fatal(last)
	}
	// Repeating the same association writes nothing.
	before := len(mustResolve(t, s, root.ID).History)
	if _, e := s.AssociateProject(root.ID, "mtm:p1", false); e != nil || len(mustResolve(t, s, root.ID).History) != before {
		t.Fatal("repeat association changed history", e)
	}
	if _, e := s.AssociateProject(leaf.ID, "p1", true); e == nil || !strings.Contains(e.Error(), "no project of its own") {
		t.Fatal(e)
	}
	if _, e := s.AssociateProject(root.ID, "elsewhere", true); e == nil || !strings.Contains(e.Error(), "not \"elsewhere\"") {
		t.Fatal(e)
	}
	if _, e := s.AssociateProject(root.ID, "p1", true); e != nil {
		t.Fatal(e)
	}
	if mustResolve(t, s, root.ID).Project != "" {
		t.Fatal("unassociate kept project")
	}
	if e := s.Undo(); e != nil {
		t.Fatal(e)
	}
	if mustResolve(t, s, root.ID).Project != "mtm:p1" {
		t.Fatal("undo did not restore project")
	}
}

func TestCLIProjectVerbs(t *testing.T) {
	f := newCLI(t)
	f.call("lane", "define", "--project", "mtm:alpha", "--lanes", `[{"name":"task"},{"name":"impl","parent":"task"},{"name":"side","project":"beta"}]`)
	task := mustResolve(t, f.a.store, "task")
	impl := mustResolve(t, f.a.store, "impl")
	if task.Project != "mtm:alpha" || impl.Project != "" || mustResolve(t, f.a.store, "side").Project != "beta" {
		t.Fatal(task, impl)
	}
	if v := f.lane("show", "impl"); v["effective_project"] != "mtm:alpha" || v["project_association"] != "inherited" {
		t.Fatal(v)
	}
	f.lane("add", "loose")
	f.lane("add", "extra", "--project", "beta")
	names := func(v any) []string {
		out := []string{}
		for _, r := range v.([]any) {
			out = append(out, r.(map[string]any)["name"].(string))
		}
		return out
	}
	if got := strings.Join(names(f.call("lane", "list", "--project", "alpha")), ","); got != "task,impl" {
		t.Fatal(got)
	}
	if got := strings.Join(names(f.call("project", "lanes", "beta")), ","); got != "side,extra" && got != "extra,side" {
		t.Fatal(got)
	}
	if got := names(f.call("lane", "tree", "--project", "beta")); len(got) != 2 {
		t.Fatal(got)
	}
	// Both nouns share one handler; a root association covers its subtree.
	f.call("project", "associate", "gamma", "loose")
	if mustResolve(t, f.a.store, "loose").Project != "gamma" {
		t.Fatal("project.associate")
	}
	f.call("project", "unassociate", "gamma", "loose")
	f.lane("associate", "task", "--project", "delta")
	if v := f.lane("show", "impl"); v["effective_project"] != "delta" {
		t.Fatal(v)
	}
	f.lane("unassociate", "task", "--project", "delta")
	if mustResolve(t, f.a.store, "task").Project != "" {
		t.Fatal("lane.unassociate --project")
	}
	f.fail("no project of its own", "lane", "unassociate", "impl", "--project", "delta")
	// Pane and project in one call share one snapshot.
	f.lane("associate", "impl", "--project", "eps", "--pane", "w9", "--session", "s")
	q := mustResolve(t, f.a.store, "impl")
	if q.Project != "eps" || len(q.Panes) != 1 || q.Panes[0].Window != "w9" {
		t.Fatal(q)
	}
	if e := f.a.store.Undo(); e != nil {
		t.Fatal(e)
	}
	if q := mustResolve(t, f.a.store, "impl"); q.Project != "" || len(q.Panes) != 0 {
		t.Fatal("combined association was not one undo", q)
	}
	f.lane("set", "loose", "project", "zeta")
	if mustResolve(t, f.a.store, "loose").Project != "zeta" {
		t.Fatal("set project")
	}
}

func TestCLIProjectRowsAndPlan(t *testing.T) {
	f := newCLI(t)
	path := writePlan(t, t.TempDir(), "example.md", smallPlan)
	f.call("plan", "define", path, "--project", "mtm:p")
	root := mustResolve(t, f.a.store, "Main task")
	if root.Project != "mtm:p" || mustResolve(t, f.a.store, "core").Project != "" {
		t.Fatal(root)
	}
	// Re-definition without --project keeps the recorded project.
	f.call("plan", "define", path)
	if mustResolve(t, f.a.store, "Main task").Project != "mtm:p" {
		t.Fatal("redefine dropped project")
	}
	f.lane("add", "loose")
	v := f.call("lane", "rows", "--project", "p")
	rows, _ := v.([]any)
	if len(rows) != 3 {
		t.Fatal(v)
	}
}

func TestProjectListSources(t *testing.T) {
	f := newCLI(t)
	f.lane("add", "a", "--project", "mtm:Alpha")
	if e := f.a.r.Settings().Set("lanes.project-source", "none"); e != nil {
		t.Fatal(e)
	}
	*f.calls = nil
	v := f.call("project", "list")
	if rows := v.([]any); len(rows) != 1 || rows[0].(map[string]any)["ref"] != "mtm:Alpha" {
		t.Fatal(v)
	}
	if len(*f.calls) != 0 {
		t.Fatal("source none ran a command", *f.calls)
	}
	if e := f.a.r.Settings().Set("lanes.project-source", "mtm"); e != nil {
		t.Fatal(e)
	}
	f.a.run = func(_ context.Context, argv []string) ([]byte, error) {
		if strings.Join(argv, " ") != "mtm project list" {
			return nil, errors.New("unexpected")
		}
		return []byte(`[{"id":"1","name":"Alpha"},{"id":"2","name":"Beta"}]`), nil
	}
	v = f.call("project", "list")
	rows := v.([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["lanes"] != float64(1) || rows[1].(map[string]any)["ref"] != "Beta" {
		t.Fatal(v)
	}
	choices, e := f.a.projectChoices(context.Background())
	if e != nil || len(choices) != 2 {
		t.Fatal(choices, e)
	}
	// A failing source still offers recorded references and never blocks.
	f.a.run = func(context.Context, []string) ([]byte, error) { return nil, errors.New("mtm down") }
	choices, e = f.a.projectChoices(context.Background())
	if e != nil || len(choices) != 1 {
		t.Fatal(choices, e)
	}
	f.lane("add", "b", "--project", "not-in-mtm")
	if mustResolve(t, f.a.store, "b").Project != "not-in-mtm" {
		t.Fatal("unknown project refused")
	}
}

func TestPageProjectGroupingAndFilter(t *testing.T) {
	a := pageFixture(t)
	seedPage(t, a, json.RawMessage(`{"lanes":[{"name":"Loose"},{"name":"Zed","project":"zeta"},{"name":"Zed child","parent":"Zed"},{"name":"Alpha","project":"mtm:alpha"},{"name":"Mixed"},{"name":"Mixed child","parent":"Mixed","project":"alpha"}]}`))
	order := func(o pageOptions) []string {
		rows, e := laneRows(a.store, o)
		if e != nil {
			t.Fatal(e)
		}
		out := []string{}
		for _, r := range rows {
			out = append(out, r.Label+"="+r.Cells[2])
		}
		return out
	}
	got := strings.Join(order(a.pageOptions()), ",")
	if got != "Alpha=mtm:alpha,Zed=zeta,Zed child=zeta,Mixed=unassociated,Mixed child=alpha,Loose=unassociated" {
		t.Fatal(got)
	}
	o := a.pageOptions()
	o.project = "alpha"
	if got := strings.Join(order(o), ","); got != "Alpha=mtm:alpha,Mixed=unassociated,Mixed child=alpha" {
		t.Fatal(got)
	}
	if e := a.r.Settings().Set("lanes.group-by-project", false); e != nil {
		t.Fatal(e)
	}
	// Ungrouped roots keep newest-first order (equal times: ID descending).
	if got := order(a.pageOptions()); got[len(got)-1] != "Alpha=mtm:alpha" {
		t.Fatal("ungrouped page still grouped", got)
	}
	if e := a.r.Settings().Set("lanes.project-column", false); e != nil {
		t.Fatal(e)
	}
	for _, c := range a.pageOptions().columns() {
		if c.Title == "Project" {
			t.Fatal("project column shown when disabled")
		}
	}
}

func TestMCPProjectDefine(t *testing.T) {
	dir := fixture(t)
	t.Setenv("OCHAZUKE_ACTOR", `{"kind":"agent","name":"mcp-project","harness":"test-harness"}`)
	var input, out, errs bytes.Buffer
	enc := json.NewEncoder(&input)
	send := func(id int, method string, params any) {
		t.Helper()
		if e := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); e != nil {
			t.Fatal(e)
		}
	}
	send(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "project-test", "version": "1"}})
	if e := enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); e != nil {
		t.Fatal(e)
	}
	for i, req := range []map[string]any{
		{"verb": "lane.define", "args": map[string]any{"project": "mtm:p", "lanes": []map[string]any{{"name": "task"}, {"name": "w", "parent": "task"}}}},
		{"verb": "lane.add", "args": map[string]any{"name": "solo", "project": "q"}},
		{"verb": "project.associate", "args": map[string]any{"project": "r", "lane": "solo"}},
		{"verb": "lane.associate", "target": "task", "args": map[string]any{"project": "s"}},
		{"verb": "project.lanes", "args": map[string]any{"project": "s"}},
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
		if reply.ID > 1 && reply.Result.Content["status"] != "done" {
			t.Fatal(reply.ID, reply.Result.Content)
		}
		if reply.ID == 6 {
			if rows, _ := reply.Result.Content["result"].([]any); len(rows) != 2 {
				t.Fatal("project.lanes", reply.Result.Content)
			}
		}
	}
	s := &Store{Dir: dir}
	if q := mustResolve(t, s, "task"); q.Project != "s" || q.History[len(q.History)-1].By.Kind != "agent" {
		t.Fatal(q)
	}
	if mustResolve(t, s, "solo").Project != "r" {
		t.Fatal("project.associate over MCP")
	}
}
