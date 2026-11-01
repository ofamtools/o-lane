package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke"
)

func TestStaleEvidence(t *testing.T) {
	mapping, err := parseLiveStates(defaultLiveStates)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC)
	states := map[string]State{"building": {Starts: true}, "running": {Open: true}}
	for _, tc := range []struct {
		name, state               string
		raw                       []string
		on, enabled, failed, want bool
	}{
		{"idle", "building", []string{"idle"}, true, true, false, true},
		{"done", "building", []string{"done"}, true, true, false, true},
		{"none", "building", []string{"none"}, true, true, false, true},
		{"empty", "building", []string{""}, true, true, false, true},
		{"gone", "building", nil, true, true, false, true},
		{"working", "building", []string{"working"}, true, true, false, false},
		{"one active", "building", []string{"idle", "working"}, true, true, false, false},
		{"needs input", "building", []string{"needs_input"}, true, true, false, false},
		{"unreachable", "building", nil, true, true, true, false},
		{"unknown", "building", []string{"novel"}, true, true, false, false},
		{"live off", "building", nil, false, true, false, false},
		{"flag off", "building", nil, true, false, false, false},
		{"name is not policy", "running", nil, true, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			panes := []Pane{{Window: "w", Session: "s", Role: "reviewer"}}
			snap := liveSnapshot{windows: map[string]string{}, stateTimes: map[string]time.Time{"w": now}}
			for i, raw := range tc.raw {
				if i > 0 {
					panes = append(panes, Pane{Window: "w2", Session: "s", Role: "implementer"})
				}
				snap.windows[panes[i].Window] = raw
			}
			if tc.failed {
				snap.err = errors.New("offline")
			}
			live := aggregateLive(panes, liveOptions{on: tc.on, mapping: mapping, roles: []string{"implementer"}}, liveSnapshots{{Session: "s"}: snap})
			got, reason := staleLane(Lane{State: tc.state}, states, live, tc.enabled, now)
			if got != tc.want || (reason != "") != tc.want {
				t.Fatalf("stale=%v reason=%q", got, reason)
			}
			if got && len(tc.raw) > 0 && !strings.Contains(reason, "since 2026-01-01T13:00:00Z") {
				t.Fatal(reason)
			}
		})
	}
	for _, custom := range []string{`{"working":"busy"}`, `{"working":{"name":"busy"}}`, `{"custom":{"name":"busy","working":true}}`} {
		m, e := parseLiveStates(custom)
		if e != nil {
			t.Fatal(e)
		}
		for _, row := range m {
			if !row.Working {
				t.Fatal("activity lost on label override", custom)
			}
		}
	}
}

func TestStaleRowsCLIAndFilters(t *testing.T) {
	f := newCLI(t)
	f.lane("add", "Parent")
	f.lane("add", "Idle", "--parent", "Parent")
	f.lane("start", "Parent")
	f.lane("start", "Idle", "--pane", "w1", "--session", "s")
	f.lane("add", "Active", "--parent", "Parent")
	f.lane("start", "Active", "--pane", "w2", "--session", "s")
	f.a.run = func(context.Context, []string) ([]byte, error) {
		return []byte(`{"windows":[{"window_id":"w1","agent_state":"idle","agent_state_at":1767272400000000000},{"window_id":"w2","agent_state":"working"}]}`), nil
	}
	before, e := f.a.store.List()
	if e != nil {
		t.Fatal(e)
	}
	views := f.call("lane", "list", "--stale").([]any)
	if len(views) != 1 {
		t.Fatal(views)
	}
	if got := f.lane("show", "Idle"); got["stale"] != true || !strings.Contains(got["stale_reason"].(string), "since 2026-01-01T13:00:00Z") {
		t.Fatal(got)
	}
	rows, e := loadLaneRows(context.Background(), f.a.store, f.a.pageOptions(), nil, f.a.run)
	if e != nil {
		t.Fatal(e)
	}
	p, e := mountPage(f.a)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.v.SetRows(listID, rows); e != nil {
		t.Fatal(e)
	}
	l := p.v.List(listID)
	if e = l.ApplyFilter("stale"); e != nil {
		t.Fatal(e)
	}
	if l.Shown() != 2 {
		t.Fatal("saved stale filter", l.Shown())
	}
	if e = l.SetPropFilters([]ochazuke.PropFilter{{Prop: "stale", Value: false}}); e != nil {
		t.Fatal(e)
	}
	if l.Shown() != 2 {
		t.Fatal("false filter must show active child and parent context", l.Shown())
	}
	after, e := f.a.store.List()
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("read projection persisted", e)
	}
	for _, setting := range []string{"lanes.stale-flag", "lanes.live"} {
		if e = f.a.r.Settings().Set(setting, false); e != nil {
			t.Fatal(e)
		}
		if got := f.call("lane", "list", "--stale").([]any); len(got) != 0 {
			t.Fatal(setting, got)
		}
		off, e := loadLaneRows(context.Background(), f.a.store, f.a.pageOptions(), nil, f.a.run)
		if e != nil {
			t.Fatal(e)
		}
		for _, row := range off {
			data := row.Data.(map[string]any)
			if data["stale"] != false || data["stale_reason"] != "" || strings.Contains(row.Cells[1], "stale") {
				t.Fatal(setting, row)
			}
		}
		if e = f.a.r.Settings().Set(setting, true); e != nil {
			t.Fatal(e)
		}
	}
}

func TestStaleParticipantOwnershipAndAgents(t *testing.T) {
	mapping, err := parseLiveStates(defaultLiveStates)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	states := map[string]State{"running": {Starts: true}}
	old := Pane{Window: "w", Session: "s", Since: now.Add(-time.Minute)}
	newer := old
	newer.Since = now
	q := Lane{ID: "old", State: "running", Panes: []Pane{old}}
	s := &Store{}
	rows := []Lane{q, {ID: "new", Panes: []Pane{newer}}}
	o := liveOptions{on: true, mapping: mapping}
	snap := liveSnapshots{{Session: "s"}: {windows: map[string]string{"w": "working"}}}
	if stale, _ := staleLane(q, states, aggregateLive(s.ownPanes(rows, q), o, snap), true, now); !stale {
		t.Fatal("old pane holder still counts activity")
	}
	s.PanesShared = true
	if stale, _ := staleLane(q, states, aggregateLive(s.ownPanes(rows, q), o, snap), true, now); stale {
		t.Fatal("shared pane activity ignored")
	}
	q.Panes = []Pane{{AgentID: "a", Harness: "custom"}}
	for _, state := range []string{"working", "idle", "done", "missing"} {
		o.agent = func(Pane) AgentStatus { return AgentStatus{State: state} }
		got, _ := staleLane(q, states, aggregateLive(q.Panes, o, nil), true, now)
		if got != (state != "working") {
			t.Fatal(state, got)
		}
	}
	o.agent = nil
	if got, _ := staleLane(q, states, aggregateLive(q.Panes, o, nil), true, now); got {
		t.Fatal("unread agent treated as inactive")
	}
	o.agent = func(Pane) AgentStatus { return AgentStatus{State: "missing", Error: "unreadable agent evidence"} }
	if got, _ := staleLane(q, states, aggregateLive(q.Panes, o, nil), true, now); got {
		t.Fatal("failed agent read treated as proof of inactivity")
	}
}

func TestStaleReaderTimestampInvalidation(t *testing.T) {
	p := Pane{Window: "w", Session: "s"}
	at := time.Now().Add(-time.Hour)
	r := newLiveReader(nil, []Pane{p}, liveSnapshots{{Session: "s"}: {windows: map[string]string{"w": "idle"}, stateTimes: map[string]time.Time{"w": at}}})
	snap := r.current([]Pane{p})
	if !snap[scopeOf(p)].stateTimes["w"].Equal(at) {
		t.Fatal("snapshot time lost")
	}
	// current must copy the timestamp map just as it copies windows.
	delete(snap[scopeOf(p)].stateTimes, "w")
	if r.current([]Pane{p})[scopeOf(p)].stateTimes["w"].IsZero() {
		t.Fatal("snapshot map leaked")
	}
	r.apply(liveEvent{Type: "agent-state", Session: "s", Window: "w", State: "done"})
	if !r.current([]Pane{p})[scopeOf(p)].stateTimes["w"].IsZero() {
		t.Fatal("previous state's time survived transition")
	}
}

func TestStaleWorkingDescendants(t *testing.T) {
	for _, tc := range []struct {
		name, descendantState, raw string
		want                       bool
	}{
		{"working grandchild", "running", "working", false},
		{"waiting grandchild", "running", "needs_input", false},
		{"idle grandchild", "running", "idle", true},
		{"review grandchild", "review", "working", true},
		{"parked grandchild", "parked", "working", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCLI(t)
			f.lane("add", "Root")
			f.lane("add", "Parent", "--parent", "Root")
			f.lane("add", "Middle", "--parent", "Parent")
			f.lane("add", "Worker", "--parent", "Middle")
			f.lane("start", "Root")
			f.lane("start", "Parent")
			f.lane("state", "Worker", tc.descendantState)
			f.lane("associate", "Worker", "--pane", "w1", "--session", "s", "--role", "reviewer")
			if e := f.a.r.Settings().Set("lanes.live-roles", []string{"implementer"}); e != nil {
				t.Fatal(e)
			}
			f.a.run = func(context.Context, []string) ([]byte, error) {
				return []byte(`[{"window_id":"w1","agent_state":"` + tc.raw + `"}]`), nil
			}
			for _, name := range []string{"Root", "Parent"} {
				if got := f.lane("show", name); got["stale"] != tc.want {
					t.Fatalf("%s stale=%v want %v", name, got["stale"], tc.want)
				}
			}
			rows, e := loadLaneRows(context.Background(), f.a.store, f.a.pageOptions(), nil, f.a.run)
			if e != nil {
				t.Fatal(e)
			}
			for _, row := range rows {
				data := row.Data.(map[string]any)
				if name := data["name"]; name == "Root" || name == "Parent" {
					if data["stale"] != tc.want {
						t.Fatalf("page %v stale=%v want %v", name, data["stale"], tc.want)
					}
				}
			}
		})
	}
}

func TestStaleWaitingActivityMapping(t *testing.T) {
	// tuios uses needs_input for approval, question and auth waits. Test both
	// legacy label overrides and object rows against the same activity defaults.
	for _, setting := range []string{defaultLiveStates, `{"needs_input":"waiting"}`, `{"needs_input":{"name":"awaiting person"}}`} {
		f := newCLI(t)
		if e := f.a.r.Settings().Set("lanes.live-states", setting); e != nil {
			t.Fatal(e)
		}
		f.lane("add", "Waiting")
		f.lane("start", "Waiting", "--pane", "w2", "--session", "s")
		if got := f.lane("show", "Waiting"); got["stale"] != false {
			t.Fatal(got)
		}
	}
}
