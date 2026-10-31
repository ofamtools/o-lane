package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/unixsuperhero/ochazuke"
	"io"
	"sync"
	"testing"
)

func TestLiveNestedAggregate(t *testing.T) {
	f := newCLI(t)
	f.lane("add", "root")
	f.lane("add", "branch", "--parent", "root")
	f.lane("add", "leaf", "--parent", "branch")
	f.lane("associate", "branch", "w1", "--session", "s")
	f.lane("associate", "leaf", "w2", "--session", "s", "--role", "reviewer")
	if got := f.lane("live", "branch")["state"]; got != "needs input" {
		t.Fatalf("nested aggregate: %v", got)
	}
}

func TestLivePageCell(t *testing.T) {
	a := pageFixture(t)
	q := mustAdd(t, a.store, Spec{Name: "Implementation"})
	if _, e := a.store.Start(q.ID, Pane{Session: "s", Window: "w1", Role: "implementer"}, "", ""); e != nil {
		t.Fatal(e)
	}
	a.run = func(context.Context, []string) ([]byte, error) {
		return []byte(`{"windows":[{"window_id":"w1","agent_state":"needs_input"}]}`), nil
	}
	p, e := mountPage(a)
	if e != nil {
		t.Fatal(e)
	}
	_ = p
	rows := p.v.List(listID).Items()
	if len(rows) != 1 || rows[0].Cells[1] != "running · needs input" {
		t.Fatalf("live cell: %+v", rows)
	}
}

func TestLiveAggregateTable(t *testing.T) {
	mapping, err := parseLiveStates(defaultLiveStates)
	if err != nil {
		t.Fatal(err)
	}
	o := liveOptions{on: true, mapping: mapping, precedence: []string{"needs input", "errored", "working", "done", "idle", "closed"}}
	for _, tc := range []struct {
		name       string
		raw        []string
		roles      []string
		filter     []string
		want, tone string
	}{
		{"implementer asks", []string{"needs_input", "working"}, nil, nil, "needs input", ochazuke.ToneNeedsInput},
		{"reviewer asks", []string{"working", "needs_input"}, nil, nil, "needs input", ochazuke.ToneNeedsInput},
		{"both working", []string{"working", "working"}, nil, nil, "working", ochazuke.ToneWorking},
		{"closed", nil, nil, nil, "closed", ochazuke.ToneExited},
		{"shell empty", []string{"", ""}, nil, nil, "shell", ""},
		{"shell none", []string{"none", "none"}, nil, nil, "shell", ""},
		{"watcher counts", []string{"working", "needs_input"}, []string{"implementer", "watcher"}, nil, "needs input", ochazuke.ToneNeedsInput},
		{"roles filter aggregate", []string{"working", "needs_input"}, nil, []string{"implementer"}, "working", ochazuke.ToneWorking},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roles := tc.roles
			if roles == nil {
				roles = []string{"implementer", "reviewer"}
			}
			panes := []Pane{{Session: "s", Window: "w1", Role: roles[0]}, {Session: "s", Window: "w2", Role: roles[1]}}
			snap := liveSnapshot{windows: map[string]string{}}
			for i, raw := range tc.raw {
				snap.windows[panes[i].Window] = raw
			}
			opts := o
			opts.roles = tc.filter
			got := aggregateLive(panes, opts, liveSnapshots{{Session: "s"}: snap})
			if got.State != tc.want || got.Tone != tc.tone || len(got.Panes) != 2 {
				t.Fatalf("%+v", got)
			}
		})
	}
	panes := []Pane{{Session: "s", Window: "w", Role: "reviewer"}}
	if got := aggregateLive(panes, o, nil); got.State != "unknown" {
		t.Fatal(got)
	}
	if got := aggregateLive(nil, o, nil); got.State != "unassociated" {
		t.Fatal(got)
	}
	o.on = false
	if got := aggregateLive(panes, o, nil); got.State != "off" || len(got.Panes) != 0 {
		t.Fatal(got)
	}
}
func TestLiveSettingsData(t *testing.T) {
	f := newCLI(t)
	f.lane("add", "root")
	f.lane("associate", "root", "w1", "--session", "s")
	f.lane("associate", "root", "w2", "--session", "s", "--role", "watcher")
	settings := f.a.r.Settings()
	if err := settings.Set("lanes.live-states", `{"working":{"name":"busy","tone":"accent","glyph":"!"},"needs_input":{"name":"waiting","tone":"error","glyph":"?"}}`); err != nil {
		t.Fatal(err)
	}
	if err := settings.Set("lanes.live-precedence", []string{"busy", "waiting"}); err != nil {
		t.Fatal(err)
	}
	if got := f.lane("live", "root"); got["state"] != "busy" || got["tone"] != "accent" || got["glyph"] != "!" {
		t.Fatal(got)
	}
	if err := settings.Set("lanes.live-roles", []string{"watcher"}); err != nil {
		t.Fatal(err)
	}
	if got := f.lane("live", "root"); got["state"] != "waiting" || len(got["panes"].([]any)) != 2 {
		t.Fatal(got)
	}
	if err := settings.Set("lanes.live-states", `{"working":"busy","needs_input":"waiting"}`); err != nil {
		t.Fatal(err)
	}
	if got := f.lane("live", "root"); got["state"] != "waiting" {
		t.Fatal(got)
	}
	for _, bad := range []string{`null`, `[]`, `{"working":{}}`, `{"working":7}`} {
		if err := settings.Set("lanes.live-states", bad); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
func TestLiveSnapshotsAndCLI(t *testing.T) {
	f := newCLI(t)
	f.lane("add", "root")
	f.lane("add", "child", "--parent", "root")
	f.lane("associate", "root", "w1", "--session", "s")
	f.lane("associate", "child", "w2", "--session", "s", "--role", "reviewer")
	before, err := f.a.store.List()
	if err != nil {
		t.Fatal(err)
	}
	n := len(*f.calls)
	tree := f.call("lane", "tree").([]any)
	if len(*f.calls) != n+1 {
		t.Fatalf("not shared: %v", *f.calls)
	}
	for _, row := range tree {
		if row.(map[string]any)["live"].(map[string]any)["state"] != "needs input" {
			t.Fatal(tree)
		}
	}
	if got := f.lane("show", "root")["live"].(map[string]any)["state"]; got != "needs input" {
		t.Fatal(got)
	}
	after, err := f.a.store.List()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("live read persisted data", err)
	}
	panes := []Pane{{Host: "remote", Session: "s", Window: "w1"}, {Session: "s", Window: "w1"}, {Session: "other", Window: "w1"}, {Window: "unassociated"}}
	calls := []string{}
	snaps := snapshotScopes(context.Background(), panes, func(_ context.Context, argv []string) ([]byte, error) {
		calls = append(calls, strings.Join(argv, " "))
		if strings.Contains(strings.Join(argv, " "), "remote:") {
			return nil, errors.New("offline")
		}
		return []byte(`{"windows":[{"window_id":"w1","agent_state":"working"}]}`), nil
	})
	if len(calls) != 3 || calls[0] != "tuios list-windows --json -s remote:s" {
		t.Fatal(calls)
	}
	o, _ := f.a.liveOptions()
	got := aggregateLive(panes, o, snaps)
	if got.Panes[0].State != "unknown" || got.Panes[0].Error == "" || got.Panes[1].State != "working" || got.Panes[2].State != "working" || got.Panes[3].State != "unknown" {
		t.Fatal(got)
	}
}

func TestLivePageOptOuts(t *testing.T) {
	a := pageFixture(t)
	q := mustAdd(t, a.store, Spec{Name: "Implementation"})
	if _, err := a.store.Start(q.ID, Pane{Session: "s", Window: "w1", Role: "implementer"}, "", ""); err != nil {
		t.Fatal(err)
	}
	a.run = func(context.Context, []string) ([]byte, error) {
		return []byte(`{"windows":[{"window_id":"w1","agent_state":"needs_input"}]}`), nil
	}
	rows, err := loadLaneRows(context.Background(), a.store, a.pageOptions(), nil, a.run)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Cells[0] != "? Implementation" || len(rows[0].Tones) != 4 || rows[0].Tones[3] != ochazuke.CellTone(0, ochazuke.ToneNeedsInput) || rows[0].Tones[0].To != 7 || rows[0].Tones[1].From != 10 || rows[0].Tones[1].Tone != ochazuke.ToneNeedsInput {
		t.Fatal(rows)
	}
	for _, tone := range []bool{false, true} {
		if err := a.r.Settings().Set("lanes.live-tones", tone); err != nil {
			t.Fatal(err)
		}
		rows, err = loadLaneRows(context.Background(), a.store, a.pageOptions(), nil, a.run)
		if err != nil {
			t.Fatal(err)
		}
		// The name cell's tone follows whichever tone draws the state cell.
		want, name := 2, ochazuke.ToneWorking
		if tone {
			want, name = 4, ochazuke.ToneNeedsInput
		}
		if last := rows[0].Tones[len(rows[0].Tones)-1]; last != ochazuke.CellTone(0, name) {
			t.Fatal(rows[0].Tones)
		}
		if len(rows[0].Tones) != want || rows[0].Cells[1] != "running · needs input" {
			t.Fatal(rows)
		}
	}
	if _, err := a.store.State(q.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	rows, err = loadLaneRows(context.Background(), a.store, a.pageOptions(), nil, a.run)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Cells[1] != "done" || rows[0].Cells[0] != "Implementation" {
		t.Fatal(rows)
	}
	if err := a.r.Settings().Set("lanes.live", false); err != nil {
		t.Fatal(err)
	}
	a.run = func(context.Context, []string) ([]byte, error) { t.Fatal("live off ran tuios"); return nil, nil }
	rows, err = loadLaneRows(context.Background(), a.store, a.pageOptions(), nil, a.run)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rows[0].Data.(map[string]any)["live"]; ok {
		t.Fatal(rows)
	}
}

// A real executable on PATH exercises the production runner, incremental
// stdout, gap snapshots, reconnect argv and cancellation, with no daemon.
func stubTuios(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, file := range []string{"tuios", "subscribe.ndjson", "resume.ndjson"} {
		data, err := os.ReadFile(filepath.Join("testdata", "live", file))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0600)
		if file == "tuios" {
			mode = 0700
		}
		if err = os.WriteFile(filepath.Join(dir, file), data, mode); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("LANE_STUB_DIR", dir)
	return dir
}
func waitLive(t *testing.T, r *liveReader, predicate func(liveSnapshots) bool) {
	t.Helper()
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	for {
		r.mu.Lock()
		panes := append([]Pane(nil), r.panes...)
		r.mu.Unlock()
		if predicate(r.current(panes)) {
			return
		}
		select {
		case <-r.changed:
		case <-timer.C:
			t.Fatal("reader did not reach expected state")
		}
	}
}
func TestLiveReaderRecordedStream(t *testing.T) {
	a := pageFixture(t)
	dir := stubTuios(t)
	// Restore the production injectable runner, without changing its contract.
	production, err := newApp(a.dir, &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	panes := []Pane{{Session: "s", Window: "w1", Role: "implementer"}, {Session: "s", Window: "w2", Role: "reviewer"}}
	r := newLiveReader(production.run, panes, liveSnapshots{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("reader failed to cancel")
		}
	}()
	waitLive(t, r, func(s liveSnapshots) bool {
		return s[liveScope{Session: "s"}].windows["w1"] == "working" && s[liveScope{Session: "s"}].windows["w2"] == "needs_input"
	})
	log, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "--after-seq 15 --boot-id boot-a") {
		t.Fatalf("no resume: %s", log)
	}
	snapshots, err := os.ReadFile(filepath.Join(dir, "snapshots"))
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Fields(string(snapshots))) < 3 {
		t.Fatalf("missing startup, gap or reconnect snapshots: %s", snapshots)
	}
	// Close is delivered from the recorded stream; w2 stays in the lane as closed.
	if err = os.WriteFile(filepath.Join(dir, "close"), []byte("close"), 0600); err != nil {
		t.Fatal(err)
	}
	waitLive(t, r, func(s liveSnapshots) bool {
		snap := s[liveScope{Session: "s"}]
		_, ok := snap.windows["w2"]
		return !ok && snap.err == nil
	})
	o, _ := a.liveOptions()
	got := aggregateLive(panes, o, r.current(panes))
	if got.State != "working" || got.Panes[1].State != "closed" {
		t.Fatal(got)
	}
	if err = os.WriteFile(filepath.Join(dir, "close-all"), []byte("close"), 0600); err != nil {
		t.Fatal(err)
	}
	waitLive(t, r, func(s liveSnapshots) bool {
		snap := s[liveScope{Session: "s"}]
		return len(snap.windows) == 0 && snap.err == nil
	})
	if got := aggregateLive(panes, o, r.current(panes)); got.State != "closed" || len(got.Panes) != 2 {
		t.Fatal(got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel timed out")
	}
}
func TestLiveStreamBufferedRunner(t *testing.T) {
	var got []liveEvent
	err := streamCommand(context.Background(), func(context.Context, []string) ([]byte, error) { return []byte("{\"type\":\"gap\"}\n"), nil }, []string{"fake"}, func(line []byte) error {
		var e liveEvent
		if err := json.Unmarshal(line, &e); err != nil {
			return err
		}
		got = append(got, e)
		return nil
	})
	if err != nil || len(got) != 1 || got[0].Type != "gap" {
		t.Fatal(got, err)
	}
	err = streamCommand(context.Background(), func(context.Context, []string) ([]byte, error) { return []byte("bad\n"), nil }, nil, func([]byte) error { return fmt.Errorf("bad event") })
	if err == nil {
		t.Fatal("invalid event accepted")
	}
}

func TestLivePageReaderLifecycle(t *testing.T) {
	a := pageFixture(t)
	dir := stubTuios(t)
	production, err := newApp(a.dir, &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	a.run = production.run
	q := mustAdd(t, a.store, Spec{Name: "Worker"})
	if _, err := a.store.Start(q.ID, Pane{Session: "s", Window: "w1", Role: "implementer"}, "", ""); err != nil {
		t.Fatal(err)
	}
	p, err := mountPage(a)
	if err != nil {
		t.Fatal(err)
	}
	if p.cancelLive != nil {
		t.Fatal("mount started a reader")
	}
	p.streaming = true
	cmd := p.syncLive()
	if cmd == nil || p.cancelLive == nil {
		t.Fatal("reader not started")
	}
	defer func() { p.stopLive(); <-p.liveDone }()
	reader := a.reader
	waitLive(t, reader, func(s liveSnapshots) bool { return s[liveScope{Session: "s"}].windows["w1"] == "working" })
	if cmd := p.syncLive(); cmd != nil {
		t.Fatal("duplicate reader")
	}
	generation := p.generation
	if err := a.r.Settings().Set("lanes.live", false); err != nil {
		t.Fatal(err)
	}
	p.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if p.cancelLive != nil || a.reader != nil {
		t.Fatal("off left reader active")
	}
	select {
	case <-p.liveDone:
	case <-time.After(2 * time.Second):
		t.Fatal("reader not stopped")
	}
	if _, cmd := p.Update(liveChanged{generation}); cmd != nil {
		t.Fatal("old generation refreshed page")
	}
	log, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(log), "subscribe --types") != 2 {
		t.Fatalf("unexpected subscriptions: %s", log)
	}
}

func TestLiveReaderBootGap(t *testing.T) {
	panes := []Pane{{Session: "s", Window: "w", Role: "implementer"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	subscriptions := [][]string{}
	snapshots := 0
	r := newLiveReader(func(ctx context.Context, argv []string) ([]byte, error) {
		if argv[1] == "list-windows" {
			mu.Lock()
			snapshots++
			mu.Unlock()
			return []byte(`{"windows":[{"window_id":"w","agent_state":"working"}]}`), nil
		}
		mu.Lock()
		subscriptions = append(subscriptions, append([]string(nil), argv...))
		n := len(subscriptions)
		mu.Unlock()
		if n == 1 {
			return []byte("{\"type\":\"subscribed\",\"seq\":50,\"boot_id\":\"old\"}\n"), nil
		}
		writer := ctx.Value(commandOutputKey{}).(io.Writer)
		text := "{\"type\":\"subscribed\",\"seq\":2,\"boot_id\":\"new\"}\n{\"type\":\"gap\",\"boot_id\":\"new\",\"reason\":\"boot_changed\"}\n{\"type\":\"agent-state\",\"seq\":3,\"boot_id\":\"new\",\"session\":\"s\",\"window\":\"w\",\"state\":\"needs_input\"}\n"
		if _, err := io.WriteString(writer, text); err != nil {
			return nil, err
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}, panes, liveSnapshots{})
	done := make(chan struct{})
	go func() { defer close(done); r.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitLive(t, r, func(s liveSnapshots) bool { return s[liveScope{Session: "s"}].windows["w"] == "needs_input" })
	mu.Lock()
	defer mu.Unlock()
	if len(subscriptions) != 2 || !strings.Contains(strings.Join(subscriptions[1], " "), "--after-seq 50 --boot-id old") || snapshots != 3 {
		t.Fatal(subscriptions, snapshots)
	}
}

func TestLiveReaderScopeIsolation(t *testing.T) {
	panes := []Pane{{Session: "s", Window: "w"}, {Session: "other", Window: "w"}, {Host: "remote", Session: "s", Window: "w"}}
	snapshots := liveSnapshots{}
	for _, p := range panes {
		snapshots[scopeOf(p)] = liveSnapshot{windows: map[string]string{"w": "idle"}}
	}
	r := newLiveReader(nil, panes, snapshots)
	r.apply(liveEvent{Type: "agent-state", Host: "remote", Session: "s", Window: "w", State: "needs_input"})
	got := r.current(panes)
	if got[liveScope{Session: "s"}].windows["w"] != "idle" || got[liveScope{Session: "other"}].windows["w"] != "idle" || got[liveScope{Host: "remote", Session: "s"}].windows["w"] != "needs_input" {
		t.Fatal(got)
	}
	r.mu.Lock()
	r.snapshots[liveScope{Host: "remote", Session: "s"}] = liveSnapshot{windows: map[string]string{}, err: errors.New("offline")}
	r.mu.Unlock()
	if !r.apply(liveEvent{Type: "agent-state", Host: "remote", Session: "s", Window: "w", State: "working"}) {
		t.Fatal("recovering scope did not request a snapshot")
	}
}
