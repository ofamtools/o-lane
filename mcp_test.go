package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// Exercise the released generic server through the real oden/CLI entry point.
// No bespoke server or tool dispatch exists in this application.
func TestMCPStdioSmoke(t *testing.T) {
	dir := fixture(t)
	actor := Actor{Kind: "agent", Name: "mcp-worker", Harness: "test-harness", Pane: "test-window", Session: "test-session", Model: "test-model"}
	b, _ := json.Marshal(actor)
	t.Setenv("OCHAZUKE_ACTOR", string(b))
	var input bytes.Buffer
	enc := json.NewEncoder(&input)
	send := func(id int, method string, params any) {
		t.Helper()
		if e := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); e != nil {
			t.Fatal(e)
		}
	}
	send(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "o-lane-test", "version": "1"}})
	if e := enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); e != nil {
		t.Fatal(e)
	}
	send(2, "tools/list", map[string]any{})
	send(3, "tools/call", map[string]any{"name": "catalog", "arguments": map[string]any{"noun": "lane"}})
	send(4, "tools/call", map[string]any{"name": "describe", "arguments": map[string]any{"verb": "lane.delete"}})
	run := func(id int, verb, target string, args map[string]any, dry bool) {
		t.Helper()
		send(id, "tools/call", map[string]any{"name": "run", "arguments": map[string]any{"verb": verb, "target": target, "args": args, "dry_run": dry}})
	}
	run(5, "lane.define", "", map[string]any{"lanes": []map[string]any{{"name": "task", "wording": "owner words"}, {"name": "worker", "parent": "task"}}}, false)
	run(6, "lane.start", "worker", map[string]any{"harness": "test-harness", "agent": "subagent"}, false)
	run(7, "lane.state", "worker", map[string]any{"state": "review"}, false)
	run(8, "lane.associate", "worker", map[string]any{"pane": "not-a-real-window", "session": "test-session", "role": "reviewer"}, false)
	run(9, "lane.delete", "worker", map[string]any{}, false)
	run(10, "lane.reword", "task", map[string]any{"text": "must refuse"}, false)
	run(11, "lane.add", "", map[string]any{"name": "dry-only"}, true)
	run(12, "lane.show", "task", map[string]any{}, false)
	run(13, "lane.import", "", map[string]any{}, false)
	run(14, "lane.note", "worker", map[string]any{"text": "forged actor", "actor": map[string]any{"kind": "owner"}}, false)
	var out, errs bytes.Buffer
	if e := Main([]string{"--data", dir, "mcp", "serve"}, &input, &out, &errs); e != nil {
		t.Fatal(e, errs.String())
	}
	dec := json.NewDecoder(&out)
	replies := map[int]map[string]any{}
	for {
		var row map[string]any
		e := dec.Decode(&row)
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e, out.String())
		}
		if row["error"] != nil {
			t.Fatal(row)
		}
		replies[int(row["id"].(float64))] = row["result"].(map[string]any)
	}
	if len(replies) != 14 {
		t.Fatalf("replies: %d: %v", len(replies), replies)
	}
	tools := replies[2]["tools"].([]any)
	if len(tools) != 4 {
		t.Fatal(tools)
	}
	structured := func(id int) map[string]any {
		t.Helper()
		v, ok := replies[id]["structuredContent"].(map[string]any)
		if !ok {
			t.Fatal(id, replies[id])
		}
		return v
	}
	for _, id := range []int{5, 6, 7, 8, 11, 12} {
		if got := structured(id)["status"]; got != "done" {
			t.Fatalf("call %d: %v", id, structured(id))
		}
	}
	for _, id := range []int{9, 10} {
		v := structured(id)
		if v["status"] != "refused" || !strings.Contains(v["reason"].(string), "verb declaration") {
			t.Fatalf("refusal %d: %v", id, v)
		}
	}
	// Import belongs to L6 and is absent rather than a nonfunctional tool.
	if got := structured(13)["status"]; got != "failed" {
		t.Fatal(structured(13))
	}
	if got := structured(14)["status"]; got != "failed" {
		t.Fatal("actor accepted from tool args", structured(14))
	}
	s := &Store{Dir: dir}
	rows, e := s.List()
	if e != nil || len(rows) != 2 {
		t.Fatal(rows, e)
	}
	q := mustResolve(t, s, "worker")
	if q.State != "review" || len(q.Panes) != 2 {
		t.Fatal(q)
	}
	for _, q := range rows {
		for _, en := range q.History {
			if en.By != actor {
				t.Fatal("transport actor lost", en)
			}
		}
	}
	if q := mustResolve(t, s, "task"); q.Wording != "owner words" {
		t.Fatal(q)
	}
}
func TestMCPDeclarations(t *testing.T) {
	f := newCLI(t)
	// Framework owns all four tools and the config output; extensions remain
	// hidden unless the owner opts in with mcp.include-extensions.
	if f.a.r.Settings().Bool("mcp.include-extensions") {
		t.Fatal("extension default")
	}
	tools := f.call("mcp", "tools").([]any)
	if len(tools) != 4 {
		t.Fatal(tools)
	}
	cfg := f.call("mcp", "config").(map[string]any)
	server := cfg["mcpServers"].(map[string]any)["o-lane"].(map[string]any)
	if server["command"] == "" {
		t.Fatal(server)
	}
	args := server["args"].([]any)
	if len(args) < 2 || args[len(args)-2] != "mcp" || args[len(args)-1] != "serve" {
		t.Fatal(args)
	}
}
