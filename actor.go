package main

import (
	"fmt"
	"strings"

	"ofam.dev/ochazuke"
)

// Detectors describe identifying run variables and weaker presence signals.
// Keep model attribution in the common AGENT_MODEL/OCHAZUKE_MODEL fields:
// the installed omp bash tool exports PI_SESSION_ID/FILE, but no model.
var harnessDetectors = []struct {
	harness       string
	runs, signals []string
}{
	{"codex", []string{"CODEX_THREAD_ID"}, []string{"CODEX_CI"}},
	{"claude-code", []string{"CLAUDE_SESSION_ID", "CLAUDE_CODE_SESSION_ID"}, []string{"CLAUDE_CODE_ENTRYPOINT"}},
	{"omp", []string{"PI_SESSION_ID"}, []string{"PI_SESSION_FILE"}},
}

func firstEnv(env func(string) string, keys []string) string {
	for _, key := range keys {
		if value := env(key); value != "" {
			return value
		}
	}
	return ""
}

func detectHarness(env func(string) string) (string, string) {
	explicit := paneEnv(env, "HARNESS")
	if explicit == "" {
		explicit = firstEnv(env, []string{"AGENT_HARNESS", "OCHAZUKE_HARNESS"})
	}
	for _, row := range harnessDetectors {
		// An explicit harness scopes the run ID; a nested harness may inherit
		// another harness's environment without owning that other run.
		if explicit != "" && explicit != row.harness {
			continue
		}
		if id := firstEnv(env, row.runs); id != "" {
			return row.harness, row.harness + ":" + id
		}
	}
	if explicit != "" {
		return explicit, ""
	}
	for _, row := range harnessDetectors {
		if firstEnv(env, row.signals) != "" {
			return row.harness, ""
		}
	}
	return "", ""
}

// Identity is advisory. Harness detection always keeps agent status; the one
// explicit override may fill identity fields, never promote a detected agent.
func cliActor(env func(string) string) (Actor, error) {
	harness, run := detectHarness(env)
	a := Actor{Kind: "owner", Name: paneEnv(env, "AGENT_NAME"), Harness: harness, Pane: paneEnv(env, "PANE_ID"), Session: paneEnv(env, "SESSION"), Model: firstEnv(env, []string{"AGENT_MODEL", "OCHAZUKE_MODEL"})}
	detected := a.Harness != ""
	if detected {
		a.Kind = "agent"
	}
	if env("OCHAZUKE_ACTOR") != "" {
		// ParseActor, not ActorFromEnv: the framework's own environment
		// detection names a Pi session "pi", and this file's table owns the
		// harness name (omp).
		explicit, e := ochazuke.ParseActor(strings.TrimSpace(env("OCHAZUKE_ACTOR")))
		if e != nil {
			return Actor{}, fmt.Errorf("OCHAZUKE_ACTOR: %w", e)
		}
		if detected {
			explicit.Kind = "agent"
		}
		if explicit.Name != "" {
			a.Name = explicit.Name
		}
		if explicit.Harness != "" {
			a.Harness = explicit.Harness
		}
		if explicit.Pane != "" {
			a.Pane = explicit.Pane
		}
		if explicit.Session != "" {
			a.Session = explicit.Session
		}
		if explicit.Model != "" {
			a.Model = explicit.Model
		}
		a.Kind = explicit.Kind
	}
	if a.IsAgent() && run != "" {
		if a.Name == "" {
			a.Name = run
		} else {
			a.Name += " (" + run + ")"
		}
	}
	return a, nil
}

var defaultLaneVerbs []verbSpec

// init fills the table in init, not in the declaration: the rows' run
// methods reach Store.permit, which reads the table.
func init() { defaultLaneVerbs = (&App{}).specs() }

func (s *Store) permit(action string) error {
	if s.Actor.Kind == "" || s.Actor.Kind == "owner" {
		return nil
	}
	if s.Actor.Kind != "agent" {
		return &ochazuke.RefusedError{Verb: "lane." + action, Rule: "actor.unknown", Reason: "unknown actor cannot change lanes"}
	}
	if s.Permit != nil {
		return s.Permit(action)
	}
	for _, v := range defaultLaneVerbs {
		if v.noun == "lane" && v.name == action && v.agent == ochazuke.AgentNever {
			return &ochazuke.RefusedError{Verb: "lane." + action, Rule: "verb declaration", Reason: "agents may never run this verb; ask the owner"}
		}
	}
	return nil
}
