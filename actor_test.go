package main

import (
	"os"
	"strings"
	"testing"
)

func TestOMPFixtureClearsIdentity(t *testing.T) {
	keys := []string{"PI_SESSION_ID", "PI_SESSION_FILE", "OMP_PROFILE"}
	for _, key := range keys {
		t.Setenv(key, "inherited")
	}
	_ = fixture(t)
	for _, key := range keys {
		if os.Getenv(key) != "" {
			t.Fatal("identity inherited", key)
		}
	}
}

func TestOMPActorIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want Actor
	}{
		{"session", map[string]string{"PI_SESSION_ID": "run-1", "PI_SESSION_FILE": "/fixture/run.jsonl", "TUIOS_PANE_ID": "pane", "TUIOS_SESSION": "session"}, Actor{Kind: "agent", Harness: "omp", Name: "omp:run-1", Pane: "pane", Session: "session"}},
		{"file signal", map[string]string{"PI_SESSION_FILE": "/fixture/run.jsonl"}, Actor{Kind: "agent", Harness: "omp"}},
		{"generic model", map[string]string{"PI_SESSION_ID": "run-1", "AGENT_MODEL": "model-a"}, Actor{Kind: "agent", Harness: "omp", Name: "omp:run-1", Model: "model-a"}},
		{"framework model", map[string]string{"PI_SESSION_ID": "run-1", "OCHAZUKE_MODEL": "model-b"}, Actor{Kind: "agent", Harness: "omp", Name: "omp:run-1", Model: "model-b"}},
		{"named agent", map[string]string{"PI_SESSION_ID": "run-1", "TUIOS_AGENT_NAME": "worker"}, Actor{Kind: "agent", Harness: "omp", Name: "worker (omp:run-1)"}},
		{"no owner promotion", map[string]string{"PI_SESSION_ID": "run-1", "OCHAZUKE_ACTOR": "owner"}, Actor{Kind: "agent", Harness: "omp", Name: "omp:run-1"}},
		{"explicit harness scopes run", map[string]string{"TUIOS_HARNESS": "omp", "PI_SESSION_ID": "run-1", "CODEX_THREAD_ID": "inherited"}, Actor{Kind: "agent", Harness: "omp", Name: "omp:run-1"}},
		{"configuration is not detection", map[string]string{"PI_CODING_AGENT_DIR": "/fixture", "OMP_PROFILE": "work"}, Actor{Kind: "owner"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, e := cliActor(func(k string) string { return tc.env[k] })
			if e != nil || got != tc.want {
				t.Fatalf("got %+v (%v), want %+v", got, e, tc.want)
			}
		})
	}
}

func TestOMPHistoryAttributionAndUndoIsolation(t *testing.T) {
	f := newCLI(t)
	f.lane("add", "Work")
	actor := func(id string) Actor {
		a, e := cliActor(func(k string) string {
			return map[string]string{"PI_SESSION_ID": id, "TUIOS_PANE_ID": "pane", "TUIOS_SESSION": "session"}[k]
		})
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	a := actor("a")
	f.a.actor = a
	f.lane("note", "Work", "from omp")
	q := mustResolve(t, f.a.store, "Work")
	if got := q.History[len(q.History)-1].By; got != a {
		t.Fatalf("history actor %+v, want %+v", got, a)
	}
	f.a.store.Actor = actor("b")
	if e := f.a.store.Undo(); e == nil || !strings.Contains(e.Error(), "history.own-entry") {
		t.Fatal("another omp run undid the change", e)
	}
	f.a.store.Actor = a
	if e := f.a.store.Undo(); e != nil {
		t.Fatal(e)
	}
}
