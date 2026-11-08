package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setup list offers the o-lane instructions note for Claude Code and Codex;
// installing writes only the "Tracking work (o-lane)" section between o-lane
// markers, keeps the rest of the file, and uninstalling gives it back.
func TestSetupInstructionNote(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	dir := fixture(t)
	home := t.TempDir()
	if resolved, e := filepath.EvalSymlinks(home); e == nil {
		home = resolved
	}
	claude, codex := filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(home, ".codex", "AGENTS.md")
	for _, p := range []string{claude, codex} {
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
	}
	const existing = "# Global rules\n\nkeep me\n"
	if e := os.WriteFile(claude, []byte(existing), 0600); e != nil {
		t.Fatal(e)
	}
	list := mustMain(t, dir, "setup", "list", "--home", home)
	for _, id := range []string{"claude-md", "codex-agents"} {
		if !strings.Contains(list, id) {
			t.Fatalf("setup list misses %s:\n%s", id, list)
		}
	}
	mustMain(t, dir, "setup", "install", "claude-md", "codex-agents", "--yes", "--home", home)
	for _, p := range []string{claude, codex} {
		b, _ := os.ReadFile(p)
		text := string(b)
		if strings.Count(text, "<!-- o-lane:begin -->") != 1 || !strings.Contains(text, "## Tracking work (o-lane)") || !strings.Contains(text, "`lane.lane define`") {
			t.Fatalf("%s:\n%s", p, text)
		}
		for _, want := range []string{"`lane.lane set`", "`md.doc new`", "`mtm lane status`", "mtm lane annotate", "mtm lane define", "mtm lane plan define", "mtm lane list --status running", "`lane.lane associate`", "`lane.lane annotate`", "`lane.lane list`", "`lane.plan define`"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s misses %s:\n%s", p, want, text)
			}
		}
		for _, bad := range []string{"lane.lane state", "mtm lane state", "lane.lane note", "o-lane lane ", "o-lane plan ", "otherwise the CLI", "lane lane", "mtm lanes"} {
			if strings.Contains(text, bad) {
				t.Fatalf("%s holds a direct o-lane command %q:\n%s", p, bad, text)
			}
		}
		if strings.Contains(text, "o-questions") || strings.Contains(text, "## One MCP server") {
			t.Fatalf("%s holds another tool's section:\n%s", p, text)
		}
	}
	if b, _ := os.ReadFile(claude); !strings.HasPrefix(string(b), existing) {
		t.Fatalf("existing text changed:\n%s", b)
	}
	mustMain(t, dir, "setup", "uninstall", "claude-md", "codex-agents", "--yes", "--home", home)
	if b, _ := os.ReadFile(claude); string(b) != existing {
		t.Fatalf("uninstall left:\n%s", b)
	}
}
