package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Agent participants are lanes' relations to a harness's sub-agent (a Claude
// Code subagent, later Codex or omp ones) rather than to a tuios pane. Their
// live state is derived from data only: lanes.agent-harnesses rows say where a
// harness writes an agent's transcript, which line field holds its time and
// which hook events start and stop it. Nothing here branches on a harness name.

const defaultAgentHarnesses = `{"claude-code":{"transcripts":["${CLAUDE_CONFIG_DIR:-~/.claude}/projects/*/{session}/subagents/agent-{agent}.jsonl"],"time_field":"timestamp","events":{"SubagentStart":"start","SubagentStop":"stop"},"fields":{"event":"hook_event_name","agent":"agent_id","session":"session_id","type":"agent_type","transcript":"agent_transcript_path"}}}`

// agentEventsFile is the append-only hook log in the data directory. It is
// separate from the lane store: the hook never takes the store lock.
const agentEventsFile = "agent-events.jsonl"

type hookFields struct {
	Event      string `json:"event,omitempty"`
	Agent      string `json:"agent"`
	Session    string `json:"session,omitempty"`
	Type       string `json:"type,omitempty"`
	Transcript string `json:"transcript,omitempty"`
}

// harnessRow is one lanes.agent-harnesses entry.
type harnessRow struct {
	// Transcripts are path templates tried in order. {agent} and {session}
	// are substituted ({session} becomes * when unknown), then ~, $VAR and
	// ${VAR:-default} expand, then the result is globbed.
	Transcripts []string `json:"transcripts"`
	// TimeField names the RFC 3339 time field of a transcript line; empty or
	// unreadable falls back to the file's modification time.
	TimeField string `json:"time_field,omitempty"`
	// Idle overrides lanes.agent-idle for this harness (Go duration).
	Idle string `json:"idle,omitempty"`
	// Events maps hook event names to start or stop.
	Events map[string]string `json:"events"`
	// Fields names the hook payload fields.
	Fields hookFields `json:"fields"`
}

func parseAgentHarnesses(text string) (map[string]harnessRow, error) {
	var rows map[string]harnessRow
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		return nil, fmt.Errorf("lanes.agent-harnesses: %w", err)
	}
	if rows == nil {
		return nil, errors.New("lanes.agent-harnesses needs a JSON object")
	}
	for name, row := range rows {
		if !safeAgentID(name) {
			return nil, fmt.Errorf("lanes.agent-harnesses: harness name %q must be a simple name", name)
		}
		if row.Fields.Agent == "" {
			return nil, fmt.Errorf("lanes.agent-harnesses: %s needs fields.agent", name)
		}
		for event, kind := range row.Events {
			if kind != "start" && kind != "stop" {
				return nil, fmt.Errorf("lanes.agent-harnesses: %s event %s maps to %q; use start or stop", name, event, kind)
			}
		}
		if row.Idle != "" {
			if _, err := time.ParseDuration(row.Idle); err != nil {
				return nil, fmt.Errorf("lanes.agent-harnesses: %s idle: %w", name, err)
			}
		}
	}
	return rows, nil
}

var agentIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// safeAgentID keeps ids usable as path and glob parts: no separators, no
// glob metacharacters, no shell words.
func safeAgentID(id string) bool {
	return agentIDPattern.MatchString(id) && !strings.Contains(id, "..")
}

// agentEvent is one recorded hook event.
type agentEvent struct {
	At         time.Time `json:"at"`
	Harness    string    `json:"harness"`
	Event      string    `json:"event,omitempty"`
	Kind       string    `json:"kind"`
	Agent      string    `json:"agent_id"`
	Session    string    `json:"session,omitempty"`
	Type       string    `json:"agent_type,omitempty"`
	Transcript string    `json:"transcript,omitempty"`
}

// agentInfo merges every event for one agent: the latest event decides start
// or stop; the other fields keep the latest non-empty value.
type agentInfo struct {
	Harness    string     `json:"harness"`
	Agent      string     `json:"agent_id"`
	Session    string     `json:"session,omitempty"`
	Type       string     `json:"agent_type,omitempty"`
	Transcript string     `json:"transcript,omitempty"`
	Last       agentEvent `json:"last"`
}

// agentLog reads the hook log incrementally: each refresh reads only bytes
// appended since the last one, so cost stays flat as the log grows.
type agentLog struct {
	mu     sync.Mutex
	path   string
	offset int64
	agents map[string]*agentInfo
	order  []string
}

func agentKey(harness, agent string) string { return harness + "\x00" + agent }

func (l *agentLog) refresh() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.agents == nil {
		l.agents = map[string]*agentInfo{}
	}
	f, err := os.Open(l.path)
	if errors.Is(err, os.ErrNotExist) {
		l.offset, l.agents, l.order = 0, map[string]*agentInfo{}, nil
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() < l.offset {
		l.offset, l.agents, l.order = 0, map[string]*agentInfo{}, nil
	}
	if _, err = f.Seek(l.offset, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		return nil
	}
	sc := bufio.NewScanner(bytes.NewReader(data[:end+1]))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var ev agentEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Agent == "" {
			continue
		}
		key := agentKey(ev.Harness, ev.Agent)
		cur := l.agents[key]
		if cur == nil {
			cur = &agentInfo{Harness: ev.Harness, Agent: ev.Agent}
			l.agents[key] = cur
			l.order = append(l.order, key)
		}
		for _, f := range []struct {
			dst *string
			src string
		}{{&cur.Session, ev.Session}, {&cur.Type, ev.Type}, {&cur.Transcript, ev.Transcript}} {
			if f.src != "" {
				*f.dst = f.src
			}
		}
		cur.Last = ev
	}
	l.offset += int64(end + 1)
	return nil
}

// lookup finds an agent by harness and id; an empty harness matches any.
func (l *agentLog) lookup(harness, agent string) *agentInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	if harness != "" {
		if v := l.agents[agentKey(harness, agent)]; v != nil {
			c := *v
			return &c
		}
		return nil
	}
	for _, key := range l.order {
		if v := l.agents[key]; v.Agent == agent {
			c := *v
			return &c
		}
	}
	return nil
}
func (l *agentLog) all() []agentInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]agentInfo, 0, len(l.order))
	for _, key := range l.order {
		out = append(out, *l.agents[key])
	}
	return out
}

func (a *App) agentLog() *agentLog {
	a.agentsOnce.Do(func() { a.agents = &agentLog{path: filepath.Join(a.dir, agentEventsFile)} })
	return a.agents
}

// AgentStatus is an agent participant's derived live state. Nothing is stored.
type AgentStatus struct {
	Harness    string     `json:"harness,omitempty"`
	Agent      string     `json:"agent_id"`
	Session    string     `json:"session,omitempty"`
	Type       string     `json:"agent_type,omitempty"`
	State      string     `json:"state"`
	Activity   *time.Time `json:"activity,omitempty"`
	Transcript string     `json:"transcript,omitempty"`
	Error      string     `json:"error,omitempty"`
}

type agentOptions struct {
	harnesses map[string]harnessRow
	idle      time.Duration
	now       time.Time
	home      string
	getenv    func(string) string
}

func (a *App) agentOptions() (agentOptions, error) {
	cfg := a.r.Settings()
	rows, err := parseAgentHarnesses(cfg.String("lanes.agent-harnesses"))
	if err != nil {
		return agentOptions{}, err
	}
	home, _ := os.UserHomeDir()
	return agentOptions{harnesses: rows, idle: cfg.Duration("lanes.agent-idle"), now: a.store.now(), home: home, getenv: os.Getenv}, nil
}

// agentReader returns a per-refresh memoised status function. Nothing is
// read until the first call, so building options on the UI loop stays free;
// then it reads the hook log once and stats and tails each transcript once.
func (a *App) agentReader() func(Pane) AgentStatus {
	o, err := a.agentOptions()
	log := a.agentLog()
	var mu sync.Mutex
	var logErr error
	loaded := false
	memo := map[string]AgentStatus{}
	return func(p Pane) AgentStatus {
		mu.Lock()
		defer mu.Unlock()
		if !loaded {
			loaded, logErr = true, log.refresh()
		}
		key := agentKey(p.Harness, p.AgentID) + "\x00" + p.AgentSession
		if st, ok := memo[key]; ok {
			return st
		}
		var st AgentStatus
		if err != nil {
			st = AgentStatus{Harness: p.Harness, Agent: p.AgentID, State: "unknown", Error: err.Error()}
		} else {
			st = readAgent(p, o, log.lookup(p.Harness, p.AgentID))
			if logErr != nil && st.Error == "" {
				st.Error = logErr.Error()
			}
		}
		memo[key] = st
		return st
	}
}

// readAgent derives one agent's state: done after a recorded stop event,
// missing with no transcript and no events, otherwise working or idle by the
// age of the last transcript activity against the idle threshold.
func readAgent(p Pane, o agentOptions, info *agentInfo) AgentStatus {
	st := AgentStatus{Harness: p.Harness, Agent: p.AgentID, Session: p.AgentSession, Type: p.Agent}
	if info != nil {
		if st.Harness == "" {
			st.Harness = info.Harness
		}
		if st.Session == "" {
			st.Session = info.Session
		}
		if info.Type != "" {
			st.Type = info.Type
		}
	}
	if !safeAgentID(p.AgentID) {
		st.State, st.Error = "missing", "agent id is not a simple id"
		return st
	}
	names := []string{}
	if st.Harness != "" {
		if _, ok := o.harnesses[st.Harness]; !ok {
			st.State, st.Error = "missing", "no lanes.agent-harnesses row for "+st.Harness
			return st
		}
		names = append(names, st.Harness)
	} else {
		for name := range o.harnesses {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	var activity time.Time
	for _, name := range names {
		row := o.harnesses[name]
		path := ""
		if info != nil && info.Transcript != "" && regularFile(info.Transcript) {
			path = info.Transcript
		} else {
			path = findTranscript(row, p.AgentID, st.Session, o)
		}
		if path == "" {
			continue
		}
		st.Transcript = path
		if st.Harness == "" {
			st.Harness = name
		}
		activity = lastActivity(path, row.TimeField)
		break
	}
	idle := o.idle
	if row, ok := o.harnesses[st.Harness]; ok && row.Idle != "" {
		idle, _ = time.ParseDuration(row.Idle)
	}
	if info != nil && info.Last.At.After(activity) {
		activity = info.Last.At
	}
	if !activity.IsZero() {
		at := activity.UTC()
		st.Activity = &at
	}
	switch {
	case info != nil && info.Last.Kind == "stop":
		st.State = "done"
	case activity.IsZero():
		st.State = "missing"
	case idle <= 0 || o.now.Sub(activity) < idle:
		st.State = "working"
	default:
		st.State = "idle"
	}
	return st
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// findTranscript expands the row's templates and returns the newest match.
func findTranscript(row harnessRow, agent, session string, o agentOptions) string {
	if session == "" || !safeAgentID(session) {
		session = "*"
	}
	best, bestAt := "", time.Time{}
	for _, tmpl := range row.Transcripts {
		pattern := strings.NewReplacer("{agent}", agent, "{session}", session).Replace(tmpl)
		pattern = expandPath(pattern, o.home, o.getenv)
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			if info, err := os.Stat(m); err == nil && info.Mode().IsRegular() && (best == "" || info.ModTime().After(bestAt)) {
				best, bestAt = m, info.ModTime()
			}
		}
		if best != "" {
			return best
		}
	}
	return ""
}

// expandPath expands $VAR, ${VAR} and ${VAR:-default}, then a leading ~.
func expandPath(path, home string, getenv func(string) string) string {
	out := os.Expand(path, func(name string) string {
		key, fallback, _ := strings.Cut(name, ":-")
		if v := getenv(key); v != "" {
			return v
		}
		return fallback
	})
	if out == "~" {
		return home
	}
	if strings.HasPrefix(out, "~/") {
		return filepath.Join(home, out[2:])
	}
	return out
}

// lastActivity reads only the file's tail: the last complete line's time
// field, or the modification time.
func lastActivity(path, field string) time.Time {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return time.Time{}
	}
	mtime := info.ModTime().UTC()
	if field == "" {
		return mtime
	}
	const tail = 64 * 1024
	start := info.Size() - tail
	if start < 0 {
		start = 0
	}
	buf := make([]byte, info.Size()-start)
	if _, err = f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return mtime
	}
	lines := bytes.Split(bytes.TrimRight(buf, "\r\n"), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		var row map[string]any
		if json.Unmarshal(lines[i], &row) != nil {
			continue
		}
		if s, ok := row[field].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				return t.UTC()
			}
		}
	}
	return mtime
}

// agentHook records one hook payload. It never fails the harness: every error
// goes to stderr and the command still succeeds. Input is bounded in size and
// time so a stuck pipe cannot outlast the harness's hook timeout.
func (a *App) agentHook(args []string) {
	if err := a.recordHook(args, 2*time.Second); err != nil {
		fmt.Fprintln(a.errOut, "o-lane agent-hook:", err)
	}
}

func (a *App) recordHook(args []string, wait time.Duration) error {
	if len(args) == 0 {
		return errors.New("usage: o-lane agent-hook HARNESS [EVENT]")
	}
	harness := args[0]
	if err := a.configure(); err != nil {
		return err
	}
	rows, err := parseAgentHarnesses(a.r.Settings().String("lanes.agent-harnesses"))
	if err != nil {
		return err
	}
	row, ok := rows[harness]
	if !ok {
		return fmt.Errorf("no lanes.agent-harnesses row for %s", harness)
	}
	raw, err := readBounded(a.in, 1<<20, wait)
	if err != nil {
		return err
	}
	var payload map[string]any
	if err = json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("hook payload: %w", err)
	}
	field := func(name string) string {
		if name == "" {
			return ""
		}
		s, _ := payload[name].(string)
		return s
	}
	event := field(row.Fields.Event)
	if len(args) > 1 {
		event = args[1]
	}
	kind := row.Events[event]
	if kind == "" {
		return nil // not an agent lifecycle event for this harness
	}
	ev := agentEvent{At: a.store.now(), Harness: harness, Event: event, Kind: kind, Agent: field(row.Fields.Agent), Session: field(row.Fields.Session), Type: field(row.Fields.Type), Transcript: field(row.Fields.Transcript)}
	if !safeAgentID(ev.Agent) {
		return fmt.Errorf("hook payload: %s %q is not a simple id", row.Fields.Agent, ev.Agent)
	}
	if ev.Session != "" && !safeAgentID(ev.Session) {
		ev.Session = ""
	}
	if ev.Transcript != "" && !filepath.IsAbs(ev.Transcript) {
		ev.Transcript = ""
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(a.dir, 0700); err != nil {
		return err
	}
	// One O_APPEND write per event: concurrent hooks never interleave lines.
	f, err := os.OpenFile(filepath.Join(a.dir, agentEventsFile), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return errors.Join(err, f.Close())
}

func readBounded(r io.Reader, limit int64, wait time.Duration) ([]byte, error) {
	if r == nil {
		return nil, errors.New("no hook payload on stdin")
	}
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(r, limit+1))
		ch <- result{data, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		if int64(len(res.data)) > limit {
			return nil, errors.New("hook payload is too large")
		}
		return res.data, nil
	case <-time.After(wait):
		return nil, errors.New("timed out reading the hook payload")
	}
}

// agentRows lists every agent seen by a hook, with its derived state.
func (a *App) agentRows() ([]AgentStatus, error) {
	read := a.agentReader()
	if err := a.agentLog().refresh(); err != nil {
		return nil, err
	}
	out := []AgentStatus{}
	for _, info := range a.agentLog().all() {
		out = append(out, read(Pane{Harness: info.Harness, AgentID: info.Agent}))
	}
	slices.Reverse(out)
	return out, nil
}
