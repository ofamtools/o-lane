package main

import (
	"fmt"
	"github.com/unixsuperhero/ochazuke"
)

// Resolve identity once at the CLI boundary, as in o-questions. An explicit
// framework identity wins; a pane alone never implies an agent.
func cliActor(env func(string) string) (Actor, error) {
	if env("OCHAZUKE_ACTOR") != "" {
		return ochazuke.ActorFromEnv(env)
	}
	a := Actor{Kind: "owner", Name: env("TUIOS_AGENT_NAME"), Harness: env("TUIOS_HARNESS"), Pane: env("TUIOS_PANE_ID"), Session: env("TUIOS_SESSION"), Model: env("AGENT_MODEL")}
	if a.Harness == "" {
		a.Harness = env("AGENT_HARNESS")
	}
	if a.Harness == "" && (env("CODEX_THREAD_ID") != "" || env("CODEX_CI") != "") {
		a.Harness = "codex"
	}
	if a.Harness == "" && env("CLAUDE_CODE_ENTRYPOINT") != "" {
		a.Harness = "claude-code"
	}
	if a.Harness != "" {
		a.Kind = "agent"
	}
	if a.Name == "" {
		a.Name = a.Harness
	}
	if raw := env("O_LANE_ACTOR"); raw != "" {
		return ochazuke.ParseActor(raw)
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
