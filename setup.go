package main

import (
	"bytes"
	_ "embed"

	ocli "ofam.dev/ochazuke/cli"
	"ofam.dev/ochazuke/setup"
)

// setup.json declares o-lane's integrations as data for ochazuke's shared
// setup verbs (o-lane setup status|list|plan|install|uninstall). The Claude
// Code items add `o-lane agent-hook claude-code` to SubagentStart and
// SubagentStop beside any existing entries (for example tuios's), and own
// only entries whose first command contains "o-lane agent-hook".
//
//go:embed setup.json
var setupItems []byte

func (a *App) declareSetup() error {
	items, err := setup.Decode(bytes.NewReader(setupItems))
	if err != nil {
		return err
	}
	return ocli.DeclareSetup(a.r, items, setup.Options{Tool: "o-lane", Data: a.dir})
}
