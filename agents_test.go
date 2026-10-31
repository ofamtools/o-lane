package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runMain(t *testing.T, dir string, stdin io.Reader, args ...string) (string, string, error) {
	t.Helper()
	var out, errs bytes.Buffer
	if stdin == nil {
		stdin = &bytes.Buffer{}
	}
	err := Main(append([]string{"--data", dir}, args...), stdin, &out, &errs)
	return out.String(), errs.String(), err
}

func mustMain(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, errs, err := runMain(t, dir, nil, args...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, errs)
	}
	return out
}

func TestAgentAssociationRoundTrip(t *testing.T) {
	dir := fixture(t)
	mustMain(t, dir, "lane", "add", "Impl")
	mustMain(t, dir, "lane", "associate", "Impl", "--agent-id", "a3f09c2e71d4b5a68", "--harness", "claude-code", "--agent-session", "sess-1", "--agent", "ofam-lane")
	// The agent side shares the handler: re-associating the same agent
	// updates the one participant instead of adding a second.
	mustMain(t, dir, "agent", "associate", "a3f09c2e71d4b5a68", "Impl", "--harness", "claude-code", "--agent-session", "sess-1", "--agent", "ofam-lane", "--role", "reviewer")
	var q View
	q = View{}
	if err := json.Unmarshal([]byte(mustMain(t, dir, "lane", "show", "Impl")), &q); err != nil {
		t.Fatal(err)
	}
	if len(q.Panes) != 1 || q.Panes[0].AgentID != "a3f09c2e71d4b5a68" || q.Panes[0].AgentSession != "sess-1" || q.Panes[0].Role != "reviewer" || q.Association != "associated" {
		t.Fatalf("association: %+v", q)
	}
	var lanes []View
	if err := json.Unmarshal([]byte(mustMain(t, dir, "agent", "lanes", "a3f09c2e71d4b5a68")), &lanes); err != nil || len(lanes) != 1 || lanes[0].Name != "Impl" {
		t.Fatalf("agent lanes: %v %+v", err, lanes)
	}
	// A pane participant with no agent stays separate from the agent.
	mustMain(t, dir, "lane", "associate", "Impl", "--pane", "w1", "--session", "s")
	mustMain(t, dir, "agent", "unassociate", "a3f09c2e71d4b5a68", "Impl")
	q = View{}
	if err := json.Unmarshal([]byte(mustMain(t, dir, "lane", "show", "Impl")), &q); err != nil {
		t.Fatal(err)
	}
	if len(q.Panes) != 1 || q.Panes[0].Window != "w1" || q.Panes[0].AgentID != "" {
		t.Fatalf("unassociate kept the wrong participant: %+v", q.Panes)
	}
	if _, _, err := runMain(t, dir, nil, "lane", "associate", "Impl", "--agent-id", "a1; rm -rf ~"); err == nil {
		t.Fatal("unsafe agent id was recorded")
	}
	// Undo restores the agent participant like any other relation change.
	mustMain(t, dir, "lane", "undo")
	mustMain(t, dir, "lane", "undo")
	q = View{}
	if err := json.Unmarshal([]byte(mustMain(t, dir, "lane", "show", "Impl")), &q); err != nil {
		t.Fatal(err)
	}
	if len(q.Panes) != 1 || q.Panes[0].AgentID != "a3f09c2e71d4b5a68" {
		t.Fatalf("undo: %+v", q.Panes)
	}
}

func writeTranscript(t *testing.T, home, session, agent string, at time.Time) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", "-src", session, "subagents")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "agent-"+agent+".jsonl")
	line := `{"type":"user","timestamp":"2026-01-01T00:00:00Z","agentId":"` + agent + `"}` + "\n" + `{"type":"assistant","timestamp":"` + at.Format(time.RFC3339Nano) + `","agentId":"` + agent + `"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAgentStateFromTranscript(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	rows, err := parseAgentHarnesses(defaultAgentHarnesses)
	if err != nil {
		t.Fatal(err)
	}
	o := agentOptions{harnesses: rows, idle: 3 * time.Minute, now: now, home: home, getenv: func(string) string { return "" }}
	writeTranscript(t, home, "sess-1", "aworking", now.Add(-time.Minute))
	writeTranscript(t, home, "sess-1", "aidle", now.Add(-10*time.Minute))
	writeTranscript(t, home, "sess-2", "adone", now.Add(-time.Minute))
	stop := &agentInfo{Harness: "claude-code", Agent: "adone", Session: "sess-2", Last: agentEvent{At: now.Add(-30 * time.Second), Kind: "stop"}}
	restarted := &agentInfo{Harness: "claude-code", Agent: "aidle", Last: agentEvent{At: now.Add(-20 * time.Second), Kind: "start"}}
	for _, tc := range []struct {
		name string
		pane Pane
		info *agentInfo
		want string
	}{
		{"recent transcript is working", Pane{Harness: "claude-code", AgentID: "aworking", AgentSession: "sess-1"}, nil, "working"},
		{"session unknown still finds the transcript", Pane{AgentID: "aworking"}, nil, "working"},
		{"old transcript is idle", Pane{Harness: "claude-code", AgentID: "aidle"}, nil, "idle"},
		{"a later start event is activity", Pane{Harness: "claude-code", AgentID: "aidle"}, restarted, "working"},
		{"stop event is done", Pane{Harness: "claude-code", AgentID: "adone"}, stop, "done"},
		{"no transcript and no events is missing", Pane{Harness: "claude-code", AgentID: "agone"}, nil, "missing"},
		{"unknown harness is missing", Pane{Harness: "nope", AgentID: "aworking"}, nil, "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := readAgent(tc.pane, o, tc.info); got.State != tc.want {
				t.Fatalf("got %+v, want %s", got, tc.want)
			}
		})
	}
	// Zero idle never idles; the per-harness row overrides the setting.
	o.idle = 0
	if got := readAgent(Pane{AgentID: "aidle"}, o, nil); got.State != "working" {
		t.Fatalf("zero idle: %+v", got)
	}
	row := rows["claude-code"]
	row.Idle = "30m"
	o.harnesses = map[string]harnessRow{"claude-code": row}
	o.idle = time.Minute
	if got := readAgent(Pane{AgentID: "aidle"}, o, nil); got.State != "working" || got.Activity == nil || !got.Activity.Equal(now.Add(-10*time.Minute)) {
		t.Fatalf("row idle override: %+v", got)
	}
	// Without a time field the modification time is the activity.
	row.TimeField, row.Idle = "", ""
	o.harnesses = map[string]harnessRow{"claude-code": row}
	o.now = time.Now()
	o.idle = time.Hour
	if got := readAgent(Pane{AgentID: "aidle"}, o, nil); got.State != "working" {
		t.Fatalf("mtime activity: %+v", got)
	}
	// CLAUDE_CONFIG_DIR moves the transcript root, as it does in Claude Code.
	other := t.TempDir()
	writeTranscript(t, other, "sess-9", "amoved", now)
	o = agentOptions{harnesses: rows, idle: time.Minute, now: now, home: home, getenv: func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return filepath.Join(other, ".claude")
		}
		return ""
	}}
	if got := readAgent(Pane{AgentID: "amoved"}, o, nil); got.State != "working" {
		t.Fatalf("CLAUDE_CONFIG_DIR: %+v", got)
	}
}

func TestAgentLiveThroughApp(t *testing.T) {
	dir := fixture(t)
	home := os.Getenv("HOME")
	a, err := newApp(dir, &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.configure(); err != nil {
		t.Fatal(err)
	}
	a.store.BackupDaily = false
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	a.store.Now = func() time.Time { return now }
	writeTranscript(t, home, "sess-1", "a1", now.Add(-time.Minute))
	if _, err = a.store.Add(Spec{Name: "Impl"}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.Associate("Impl", Pane{Harness: "claude-code", AgentID: "a1"}, false); err != nil {
		t.Fatal(err)
	}
	q, _ := a.store.Resolve("Impl")
	live, err := a.live(q)
	if err != nil || live.State != "working" || len(live.Panes) != 1 || live.Panes[0].AgentLive == nil {
		t.Fatalf("live: %v %+v", err, live)
	}
	// A recorded stop wins; the log is read incrementally on the next refresh.
	a.in = strings.NewReader(`{"session_id":"sess-1","hook_event_name":"SubagentStop","agent_id":"a1"}`)
	if err = a.recordHook([]string{"claude-code"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if live, _ = a.live(q); live.State != "done" {
		t.Fatalf("after stop: %+v", live)
	}
	a.in = strings.NewReader(`{"session_id":"sess-1","hook_event_name":"SubagentStart","agent_id":"a1"}`)
	if err = a.recordHook([]string{"claude-code"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if live, _ = a.live(q); live.State != "working" {
		t.Fatalf("teammate restart: %+v", live)
	}
	// lanes.agent-live off leaves agent participants unread.
	if err = a.r.Settings().Set("lanes.agent-live", false); err != nil {
		t.Fatal(err)
	}
	if live, _ = a.live(q); live.State != "unassociated" || live.Panes[0].AgentLive != nil {
		t.Fatalf("agent-live off: %+v", live)
	}
	if err = a.r.Settings().Set("lanes.agent-harnesses", `{"x":{"events":{"A":"begin"},"fields":{"agent":"id"}}}`); err == nil {
		t.Fatal("invalid harness row accepted")
	}
}

func TestAgentHookPayloads(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "agents", "claude-code-hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name    string
		Payload json.RawMessage
		Want    *agentEvent
		Skip    bool
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	dir := fixture(t)
	log := filepath.Join(dir, agentEventsFile)
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			before, _ := os.ReadFile(log)
			out, _, err := runMain(t, dir, bytes.NewReader(tc.Payload), "agent-hook", "claude-code")
			if err != nil || out != "" {
				t.Fatalf("hook must succeed silently: %v %q", err, out)
			}
			after, _ := os.ReadFile(log)
			added := strings.TrimSpace(string(after[len(before):]))
			if tc.Skip {
				if added != "" {
					t.Fatalf("recorded %s", added)
				}
				return
			}
			var got agentEvent
			if err := json.Unmarshal([]byte(added), &got); err != nil {
				t.Fatal(err, added)
			}
			got.At, got.Harness, got.Event = time.Time{}, "", ""
			if got != *tc.Want {
				t.Fatalf("got %+v, want %+v", got, *tc.Want)
			}
		})
	}
	// Errors never fail the harness: bad JSON, an unknown harness, no args.
	for _, args := range [][]string{{"agent-hook", "claude-code"}, {"agent-hook", "nope"}, {"agent-hook"}} {
		if _, _, err := runMain(t, dir, strings.NewReader("garbage"), args...); err != nil {
			t.Fatalf("%v failed: %v", args, err)
		}
	}
	// An explicit event argument overrides the payload's event field.
	if _, _, err := runMain(t, dir, strings.NewReader(`{"agent_id":"a9"}`), "agent-hook", "claude-code", "SubagentStop"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(log); !strings.Contains(string(b), `"kind":"stop","agent_id":"a9"`) {
		t.Fatalf("event argument: %s", b)
	}
	// A pipe that never closes cannot hold the hook past its bound.
	r, w := io.Pipe()
	defer w.Close()
	a, err := newApp(dir, r, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err = a.recordHook([]string{"claude-code"}, 50*time.Millisecond); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("stuck stdin: %v after %v", err, time.Since(start))
	}
	var agents []AgentStatus
	if err = json.Unmarshal([]byte(mustMain(t, dir, "agent", "list")), &agents); err != nil || len(agents) != 4 || agents[0].Agent != "a9" || agents[0].State != "done" {
		t.Fatalf("agent list: %v %+v", err, agents)
	}
}

const tuiosHooks = `{
  "model": "opus",
  "hooks": {
    "SubagentStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "tuios agent-hook claude-code --integration 3",
            "timeout": 5
          }
        ]
      }
    ],
    "SubagentStop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "tuios agent-hook claude-code --integration 3",
            "timeout": 5
          }
        ]
      }
    ]
  },
  "theme": "dark"
}
`

func hookCommands(t *testing.T, path, event string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Model string
		Theme string
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string
				Command string
				Timeout int
			}
		}
	}
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Model != "opus" || doc.Theme != "dark" {
		t.Fatalf("other settings lost: %s", data)
	}
	out := []string{}
	for _, entry := range doc.Hooks[event] {
		for _, h := range entry.Hooks {
			out = append(out, h.Command)
		}
	}
	return out
}

func TestSetupClaudeSubagentHook(t *testing.T) {
	dir := fixture(t)
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(tuiosHooks), 0600); err != nil {
		t.Fatal(err)
	}
	if out := mustMain(t, dir, "setup", "status", "--home", home); !strings.Contains(out, "claude-subagent-start: missing") || !strings.Contains(out, "claude-subagent-stop: missing") {
		t.Fatalf("status: %s", out)
	}
	// The plan is a dry run: it shows the resulting file and writes nothing.
	plan := mustMain(t, dir, "setup", "plan", "--home", home)
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(settings)); err == nil {
		settings = filepath.Join(resolved, "settings.json")
	}
	if !strings.Contains(plan, "write: "+settings) || !strings.Contains(plan, "o-lane agent-hook claude-code") {
		t.Fatalf("plan: %s", plan)
	}
	if b, _ := os.ReadFile(settings); string(b) != tuiosHooks {
		t.Fatal("plan changed the file")
	}
	// Install needs confirmation; agents cannot give it.
	if _, _, err := runMain(t, dir, nil, "setup", "install", "--home", home); err == nil {
		t.Fatal("install ran without confirmation")
	}
	mustMain(t, dir, "setup", "install", "--yes", "--home", home)
	for _, event := range []string{"SubagentStart", "SubagentStop"} {
		got := hookCommands(t, settings, event)
		if len(got) != 2 || got[0] != "tuios agent-hook claude-code --integration 3" || got[1] != "o-lane agent-hook claude-code" {
			t.Fatalf("%s: %q", event, got)
		}
	}
	if b, err := os.ReadFile(settings + ".o-lane.bak"); err != nil || string(b) != tuiosHooks {
		t.Fatalf("backup: %v %s", err, b)
	}
	first, _ := os.ReadFile(settings)
	mustMain(t, dir, "setup", "install", "--yes", "--home", home)
	if again, _ := os.ReadFile(settings); !bytes.Equal(first, again) {
		t.Fatalf("second install changed the file:\n%s", again)
	}
	if out := mustMain(t, dir, "setup", "status", "--home", home); !strings.Contains(out, "everything is set up") {
		t.Fatalf("status after install: %s", out)
	}
	mustMain(t, dir, "setup", "uninstall", "--yes", "--home", home)
	for _, event := range []string{"SubagentStart", "SubagentStop"} {
		if got := hookCommands(t, settings, event); len(got) != 1 || got[0] != "tuios agent-hook claude-code --integration 3" {
			t.Fatalf("uninstall %s: %q", event, got)
		}
	}
	// No Claude Code directory: the items are not applicable, nothing written.
	empty := t.TempDir()
	if out := mustMain(t, dir, "setup", "status", "--home", empty); !strings.Contains(out, "n/a") {
		t.Fatalf("no claude dir: %s", out)
	}
	if _, err := os.Stat(filepath.Join(empty, ".claude")); err == nil {
		t.Fatal("status created ~/.claude")
	}
}
