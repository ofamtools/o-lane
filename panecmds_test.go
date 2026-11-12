package main

import "testing"

// tuiosPaneCommands are the settings that keep the older pane CLI working.
// The existing live tests drive a fake of it; fixture installs them.
func tuiosPaneCommands() paneCommands {
	return paneCommands{
		Sessions: []string{"tuios", "list-sessions", "--json"},
		Panes:    []string{"tuios", "list-windows", "--json", "-s", "{session}"},
		Events:   []string{"tuios", "subscribe", "--types", "agent-state,window-closed", "--hosts"},
		Resume:   []string{"--after-seq", "{after-seq}", "--boot-id", "{boot-id}"},
	}
}

func useTuiosPaneCommands(t *testing.T) {
	t.Helper()
	old := builtinPaneCommands
	builtinPaneCommands = tuiosPaneCommands()
	t.Cleanup(func() { builtinPaneCommands = old })
}
