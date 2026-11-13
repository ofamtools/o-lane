package main

import "testing"

// stubPaneCommands are the settings that keep the older pane CLI working.
// The existing live tests drive a fake of it; fixture installs them.
func stubPaneCommands() paneCommands {
	return paneCommands{
		Sessions: []string{"mtm", "list-sessions", "--json"},
		Panes:    []string{"mtm", "list-windows", "--json", "-s", "{session}"},
		Events:   []string{"mtm", "subscribe", "--types", "agent-state,window-closed", "--hosts"},
		Resume:   []string{"--after-seq", "{after-seq}", "--boot-id", "{boot-id}"},
	}
}

func useStubPaneCommands(t *testing.T) {
	t.Helper()
	old := builtinPaneCommands
	builtinPaneCommands = stubPaneCommands()
	t.Cleanup(func() { builtinPaneCommands = old })
}
