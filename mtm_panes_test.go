package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func mtmSample(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "mtm", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The settings default to mtm's own commands.
func TestPaneCommandDefaultsAreMtm(t *testing.T) {
	a, err := newApp(fixtureNoPaneOverride(t), strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	got := a.paneCommands()
	if !reflect.DeepEqual(got, defaultPaneCommands()) || got.Sessions[0] != "mtm" || got.Panes[0] != "mtm" || got.Events[0] != "mtm" {
		t.Fatalf("%+v", got)
	}
}

// fixtureNoPaneOverride is a data dir whose app keeps the shipped defaults.
func fixtureNoPaneOverride(t *testing.T) string {
	dir := fixture(t)
	builtinPaneCommands = defaultPaneCommands() // fixture installed the older commands; this test wants the shipped ones
	return dir
}

// Sessions and panes come from mtm's own output shapes.
func TestMtmPaneChoicesAndSnapshot(t *testing.T) {
	a, err := newApp(fixtureNoPaneOverride(t), strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	a.run = func(_ context.Context, argv []string) ([]byte, error) {
		calls = append(calls, strings.Join(argv, " "))
		switch strings.Join(argv, " ") {
		case "mtm session list --json":
			return mtmSample(t, "session-list.json"), nil
		case "mtm session list-panes --json --session mtm-only":
			return mtmSample(t, "list-panes.json"), nil
		}
		t.Fatalf("unexpected command %v", argv)
		return nil, nil
	}
	choices, err := a.paneChoices(context.Background())
	if err != nil || len(choices) == 0 || !strings.HasPrefix(choices[0].ID, "mtm-only/") {
		t.Fatalf("%v %v", choices, err)
	}
	snaps := snapshotScopes(context.Background(), []Pane{{Session: "mtm-only", Window: "24073e50"}}, a.run, a.paneCommands())
	snap := snaps[liveScope{Session: "mtm-only"}]
	if snap.err != nil || snap.windows["24073e50-b8b4-4916-99fa-f4c0b4526b07"] != "unknown" || snap.windows["ff0343c3-058e-4eea-8f1e-1dfe797dd026"] != "none" {
		t.Fatalf("%+v", snap)
	}
	if id, ok := snap.windowID("24073e50"); !ok || id != "24073e50-b8b4-4916-99fa-f4c0b4526b07" {
		t.Fatal(id, ok)
	}
}

// A stream line is one event; a document is its rows, behind one ack the first time.
func TestDecodeEvents(t *testing.T) {
	events, polled, err := decodeEvents([]byte(`{"type":"agent-state","seq":3,"boot_id":"b","session":"s","window":"w","state":"idle"}`), false)
	if err != nil || polled || len(events) != 1 || events[0].Status != "idle" {
		t.Fatalf("%v %v %+v", err, polled, events)
	}
	first, polled, err := decodeEvents(compact(mtmSample(t, "events-first.json")), false)
	if err != nil || !polled || len(first) != 1 || first[0].Type != "subscribed" || first[0].Seq != 11 || first[0].BootID != "0bb6425cc36241c5" {
		t.Fatalf("%v %v %+v", err, polled, first)
	}
	next, polled, err := decodeEvents(compact(mtmSample(t, "events-agent-state.json")), true)
	if err != nil || !polled || len(next) != 2 || next[0].Type != "agent-state" || next[0].Status != "working" || next[0].Window != "24073e50-b8b4-4916-99fa-f4c0b4526b07" || next[0].BootID != "0bb6425cc36241c5" {
		t.Fatalf("%v %v %+v", err, polled, next)
	}
}

func compact(b []byte) []byte { return []byte(strings.Join(strings.Fields(string(b)), " ")) }

// The reader re-runs a document-style command from its cursor, applies the
// rows, and does not call the clean end of a run a disconnect.
func TestLiveReaderPollsDocuments(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	runs := [][]string{}
	r := newLiveReader(func(ctx context.Context, argv []string) ([]byte, error) {
		if strings.Contains(strings.Join(argv, " "), "list-panes") {
			return []byte(`{"windows":[{"window_id":"w","agent_state":"idle"}]}`), nil
		}
		mu.Lock()
		runs = append(runs, append([]string(nil), argv...))
		n := len(runs)
		mu.Unlock()
		switch n {
		case 1:
			return []byte(`{"boot_id":"b1","events":[],"last_seq":4}` + "\n"), nil
		case 2:
			return []byte(`{"boot_id":"b1","events":[{"seq":5,"type":"agent-state","session":"s","window":"w","boot_id":"b1","data":{"state":"needs_input"}}],"last_seq":5}` + "\n"), nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}, defaultPaneCommands(), []Pane{{Session: "s", Window: "w"}}, liveSnapshots{})
	done := make(chan struct{})
	go func() { defer close(done); r.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitLive(t, r, func(s liveSnapshots) bool { return s[liveScope{Session: "s"}].windows["w"] == "needs_input" })
	mu.Lock()
	defer mu.Unlock()
	if len(runs) < 2 || strings.Contains(strings.Join(runs[0], " "), "after_seq") || !strings.HasSuffix(strings.Join(runs[1], " "), "--after_seq 4 --boot_id b1") {
		t.Fatal(runs)
	}
	snaps := r.current([]Pane{{Session: "s", Window: "w"}})
	if snaps[liveScope{Session: "s"}].err != nil {
		t.Fatalf("polled end counted as a disconnect: %v", snaps[liveScope{Session: "s"}].err)
	}
}
