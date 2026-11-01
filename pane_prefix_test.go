package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestPanePrefixAssociation(t *testing.T) {
	const full = "8a3bcdef-1234-5678-90ab-123456789abc"
	for _, verb := range []string{"associate", "start", "pane-associate"} {
		for _, tc := range []struct {
			name, listing, want string
			fail                bool
		}{
			{"unique", `[{"window_id":"` + full + `"}]`, full, false},
			{"object", `{"windows":[{"window_id":"` + full + `"}]}`, full, false},
			{"ambiguous", `[{"window_id":"` + full + `"},{"window_id":"8a3bcdef-other"}]`, "8a3bcdef", false},
			{"missing", `[]`, "8a3bcdef", false},
			{"offline", ``, "8a3bcdef", true},
			{"bad json", `{`, "8a3bcdef", false},
		} {
			t.Run(verb+"/"+tc.name, func(t *testing.T) {
				f := newCLI(t)
				f.lane("add", "Worker")
				f.a.run = func(_ context.Context, argv []string) ([]byte, error) {
					if !reflect.DeepEqual(argv, []string{"tuios", "list-windows", "--json", "-s", "remote:s"}) {
						t.Fatalf("lookup %v", argv)
					}
					if tc.fail {
						return nil, errors.New("offline")
					}
					return []byte(tc.listing), nil
				}
				if verb == "pane-associate" {
					f.call("pane", "associate", "--lane", "Worker", "--pane", "8a3bcdef", "--session", "s", "--host", "remote")
				} else {
					f.lane(verb, "Worker", "--pane", "8a3bcdef", "--session", "s", "--host", "remote")
				}
				q := mustResolve(t, f.a.store, "Worker")
				if len(q.Panes) != 1 || q.Panes[0].Window != tc.want {
					t.Fatal(q.Panes)
				}
				if e := f.a.store.Undo(); e != nil {
					t.Fatal(e)
				}
				if q := mustResolve(t, f.a.store, "Worker"); len(q.Panes) != 0 {
					t.Fatal("association undo", q.Panes)
				}
			})
		}
	}
}

func TestPanePrefixLive(t *testing.T) {
	const full = "8a3bcdef-1234-5678-90ab-123456789abc"
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	mapping, e := parseLiveStates(defaultLiveStates)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name, ref, want string
		windows         map[string]string
	}{
		{"unique", "8a3bcdef", "working", map[string]string{full: "working", "other": "idle"}},
		{"waiting", "8a3bcdef", "needs input", map[string]string{full: "needs_input"}},
		{"ambiguous", "8a3bcdef", "closed", map[string]string{full: "working", "8a3bcdef-other": "working"}},
		{"exact wins", "8a3bcdef", "idle", map[string]string{"8a3bcdef": "idle", full: "working"}},
		{"empty", "", "unassociated", map[string]string{full: "working"}},
		{"missing", "absent", "closed", map[string]string{full: "working"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := Pane{Window: tc.ref, Session: "s"}
			snapshots := liveSnapshots{{Session: "s"}: {windows: tc.windows, stateTimes: map[string]time.Time{full: at}}}
			r := newLiveReader(nil, []Pane{p}, snapshots)
			o := liveOptions{on: true, mapping: mapping}
			got := aggregateLive([]Pane{p}, o, r.current([]Pane{p}))
			if got.State != tc.want {
				t.Fatalf("got %s want %s", got.State, tc.want)
			}
			if tc.name == "unique" || tc.name == "waiting" {
				if got.Panes[0].StateSince == nil || !got.Panes[0].StateSince.Equal(at) {
					t.Fatal("resolved timestamp", got.Panes)
				}
				r.apply(liveEvent{Type: "agent-state", Session: "s", Window: full, State: "needs_input"})
				select {
				case <-r.changed:
				default:
					t.Fatal("prefix update did not notify the page")
				}
				if live := aggregateLive([]Pane{p}, o, r.current([]Pane{p})); live.State != "needs input" {
					t.Fatal("prefix stream update", live)
				}
				r.apply(liveEvent{Type: "window-closed", Session: "s", Window: full})
				select {
				case <-r.changed:
				default:
					t.Fatal("prefix close did not notify the page")
				}
				if live := aggregateLive([]Pane{p}, o, r.current([]Pane{p})); live.State != "closed" {
					t.Fatal("prefix close", live)
				}
			}
		})
	}
}

func TestPanePrefixReaderUniquenessChanges(t *testing.T) {
	for _, tc := range []struct {
		name    string
		windows map[string]string
		event   liveEvent
	}{
		{"ambiguous becomes unique", map[string]string{"prefix-a": "working", "prefix-b": "idle"}, liveEvent{Type: "window-closed", Session: "s", Window: "prefix-b"}},
		{"closed becomes active", map[string]string{}, liveEvent{Type: "agent-state", Session: "s", Window: "prefix-a", State: "working"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := Pane{Window: "prefix-", Session: "s"}
			r := newLiveReader(nil, []Pane{p}, liveSnapshots{{Session: "s"}: {windows: tc.windows}})
			r.apply(tc.event)
			select {
			case <-r.changed:
			default:
				t.Fatal("changed prefix resolution did not notify the page")
			}
			mapping, err := parseLiveStates(defaultLiveStates)
			if err != nil {
				t.Fatal(err)
			}
			if live := aggregateLive([]Pane{p}, liveOptions{on: true, mapping: mapping}, r.current([]Pane{p})); live.State != "working" {
				t.Fatal(live)
			}
		})
	}
}
