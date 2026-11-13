package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func paneSample(t *testing.T, command, shape string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "panes", command+"-"+shape+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPaneListEmptyAndInvalid(t *testing.T) {
	for _, data := range []string{"[]", `{"windows":[]}`, "null", `{"windows":null}`, " \n [] \t"} {
		rows, err := decodePaneList[windowStatus]([]byte(data), "windows")
		if err != nil || len(rows) != 0 {
			t.Fatalf("%q: rows = %v, err = %v", data, rows, err)
		}
	}
	for _, data := range []string{"", "[", `{"windows":`, `{"success":true}`, `{"windows":{}}`, `{"windows":[{"window_id":1}]}`, `[] []`} {
		if _, err := decodePaneList[windowStatus]([]byte(data), "windows"); err == nil {
			t.Fatalf("accepted invalid listing %q", data)
		}
	}
}

func TestPaneListChoicesAndFocus(t *testing.T) {
	for _, sessions := range []string{"array", "object"} {
		for _, windows := range []string{"array", "object"} {
			t.Run(sessions+"/"+windows, func(t *testing.T) {
				a := pageFixture(t)
				var calls [][]string
				a.run = func(_ context.Context, argv []string) ([]byte, error) {
					calls = append(calls, append([]string(nil), argv...))
					switch {
					case reflect.DeepEqual(argv, []string{"mtm", "list-sessions", "--json"}):
						return paneSample(t, "list-sessions", sessions), nil
					case reflect.DeepEqual(argv, []string{"mtm", "list-windows", "--json", "-s", "hub"}):
						return paneSample(t, "list-windows", windows), nil
					case reflect.DeepEqual(argv, []string{"mtm", "pane", "switch", "--session", "hub", "--window", "fixture-window"}):
						return nil, nil
					default:
						return nil, fmt.Errorf("unexpected command %q", argv)
					}
				}
				choices, err := a.paneChoices(context.Background())
				if err != nil || len(choices) != 1 {
					t.Fatalf("choices = %v, err = %v", choices, err)
				}
				if choices[0].ID != "hub/fixture-window" || choices[0].Label != "Worker" {
					t.Fatal(choices)
				}
				pane, err := paneChoice(choices[0].ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := a.store.Add(Spec{Name: "Implementation"}); err != nil {
					t.Fatal(err)
				}
				if _, err := a.store.Associate("Implementation", pane, false); err != nil {
					t.Fatal(err)
				}
				p, err := mountPage(a)
				if err != nil {
					t.Fatal(err)
				}
				p.m.Width, p.m.Height = 140, 38
				p.m.View()
				pageKey(t, p, "g")
				pageKey(t, p, "p")
				want := []string{"mtm", "pane", "switch", "--session", "hub", "--window", "fixture-window"}
				if !reflect.DeepEqual(calls[len(calls)-1], want) {
					t.Fatalf("focus did not reach recorded session/window: %v", calls)
				}
			})
		}
	}
}

func TestLiveSnapshotsPaneListShapes(t *testing.T) {
	for _, shape := range []string{"array", "object"} {
		t.Run(shape, func(t *testing.T) {
			panes := []Pane{{Session: "hub", Window: "fixture-window"}, {Session: "other", Window: "fixture-window"}}
			snapshots := snapshotScopes(context.Background(), panes, func(_ context.Context, argv []string) ([]byte, error) {
				if !reflect.DeepEqual(argv, []string{"mtm", "list-windows", "--json", "-s", "hub"}) {
					return []byte(`{"windows":[{"window_id":"fixture-window","agent_state":"working"}]}`), nil
				}
				return paneSample(t, "list-windows", shape), nil
			}, stubPaneCommands())
			for _, want := range []struct{ session, status string }{{"hub", "idle"}, {"other", "working"}} {
				snap := snapshots[liveScope{Session: want.session}]
				if snap.err != nil || snap.windows["fixture-window"] != want.status {
					t.Fatalf("%s snapshot = %+v", want.session, snap)
				}
			}
		})
	}
}
