package main

import (
	"strings"

	"ofam.dev/ochazuke"
)

// paneCommands are the argv templates o-lane runs to read the panes it
// associates lanes with. They are data (settings), so a different pane
// provider is a settings change, not a code change. A `{name}` in a word is
// replaced by the call's value; a word that still names a placeholder with no
// value is dropped with the flag before it never, so a template lists only
// words it fills.
//
//	sessions  list the live sessions (rows hold name and id)
//	panes     list one session's panes ({session}); rows hold window_id and agent_state
//	events    follow agent-state and window-closed events (a stream of JSON lines, or
//	          one JSON document holding an `events` array, re-run after each document)
//	resume    words appended to events once a cursor is known ({after-seq}, {boot-id})
type paneCommands struct {
	Sessions, Panes, Events, Resume []string
}

// defaultPaneCommands are mtm's own commands (the daemon is inside mtm).
func defaultPaneCommands() paneCommands {
	return paneCommands{
		Sessions: []string{"mtm", "session", "list", "--json"},
		Panes:    []string{"mtm", "session", "list-panes", "--json", "--session", "{session}"},
		Events:   []string{"mtm", "session", "events", "--wait_ms", "25000", "--types", `["agent-state","window-closed"]`},
		Resume:   []string{"--after_seq", "{after-seq}", "--boot_id", "{boot-id}"},
	}
}

// builtinPaneCommands are the setting defaults. Tests that exercise the older
// pane CLI replace it before newApp declares the settings.
var builtinPaneCommands = defaultPaneCommands()

func paneCommandSettings() []ochazuke.Setting {
	d := builtinPaneCommands
	return []ochazuke.Setting{
		{Name: "lanes.sessions-command", Type: ochazuke.SettingList, Default: d.Sessions, Description: "Argv that lists live sessions as JSON (rows with name and id, bare or under sessions/items)"},
		{Name: "lanes.panes-command", Type: ochazuke.SettingList, Default: d.Panes, Description: "Argv that lists one session's panes as JSON ({session}); rows with window_id and agent_state, under windows"},
		{Name: "lanes.events-command", Type: ochazuke.SettingList, Default: d.Events, Description: "Argv that follows agent-state and window-closed events: JSON lines, or one JSON document with an events array (re-run after each document)"},
		{Name: "lanes.events-resume", Type: ochazuke.SettingList, Default: d.Resume, Description: "Words appended to the events command to resume after a cursor ({after-seq}, {boot-id})"},
	}
}

// paneCommands reads the current settings.
func (a *App) paneCommands() paneCommands {
	s := a.r.Settings()
	return paneCommands{
		Sessions: s.Strings("lanes.sessions-command"),
		Panes:    s.Strings("lanes.panes-command"),
		Events:   s.Strings("lanes.events-command"),
		Resume:   s.Strings("lanes.events-resume"),
	}
}

// expand returns a copy of argv with each {key} replaced; pairs are key, value.
func expand(argv []string, pairs ...string) []string {
	r := strings.NewReplacer(pairs...)
	out := make([]string, len(argv))
	for i, w := range argv {
		out[i] = r.Replace(w)
	}
	return out
}
