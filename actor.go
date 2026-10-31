package main

import (
	"fmt"
	"ofam.dev/ochazuke"
)

// Identity is advisory. Harness detection always keeps agent status; the one
// explicit override may fill identity fields, never promote a detected agent.
func cliActor(env func(string) string) (Actor, error) {
	a := Actor{Kind: "owner", Name: env("TUIOS_AGENT_NAME"), Harness: env("TUIOS_HARNESS"), Pane: env("TUIOS_PANE_ID"), Session: env("TUIOS_SESSION"), Model: env("AGENT_MODEL")}
	for _, key := range []string{"AGENT_HARNESS", "OCHAZUKE_HARNESS"} {
		if a.Harness == "" {
			a.Harness = env(key)
		}
	}
	if a.Model == "" {
		a.Model = env("OCHAZUKE_MODEL")
	}
	run := ""
	if id := env("CODEX_THREAD_ID"); id != "" {
		run = "codex:" + id
		if a.Harness == "" {
			a.Harness = "codex"
		}
	}
	if a.Harness == "" && env("CODEX_CI") != "" {
		a.Harness = "codex"
	}
	for _, key := range []string{"CLAUDE_SESSION_ID", "CLAUDE_CODE_SESSION_ID"} {
		if id := env(key); id != "" && run == "" {
			run = "claude-code:" + id
			if a.Harness == "" {
				a.Harness = "claude-code"
			}
		}
	}
	if a.Harness == "" && env("CLAUDE_CODE_ENTRYPOINT") != "" {
		a.Harness = "claude-code"
	}
	detected := a.Harness != ""
	if detected {
		a.Kind = "agent"
	}
	if env("OCHAZUKE_ACTOR") != "" {
		explicit, e := ochazuke.ActorFromEnv(env)
		if e != nil {
			return Actor{}, e
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

var never = map[string]bool{"delete": true, "reword": true, "import": true}

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
	if never[action] {
		return &ochazuke.RefusedError{Verb: "lane." + action, Rule: "verb declaration", Reason: "agents may never run this verb; ask the owner"}
	}
	if action == "export" {
		return fmt.Errorf("lane.export: needs owner confirmation (verb declaration)")
	}
	return nil
}
