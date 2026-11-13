package main

import (
	"bytes"
	"context"
	_ "embed"
	"path/filepath"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
	"ofam.dev/ochazuke/setup"
)

// setup.json declares o-lane's integrations as data for ochazuke's shared
// setup verbs (o-lane setup status|list|plan|install|uninstall). The Claude
// Code items add `o-lane agent-hook claude-code` to SubagentStart and
// SubagentStop beside any existing entries (for example mtm's), and own
// only entries whose first command contains "o-lane agent-hook". The two
// block items (claude-md, codex-agents) add the "Tracking work (o-lane)"
// note to the agents' user instructions between o-lane markers, taken from
// the owner's own global instructions; they share one marker pair, so two
// paths that resolve to one file hold one note.
//
//go:embed setup.json
var setupItems []byte

func (a *App) declareSetup() error {
	items, err := setup.Decode(bytes.NewReader(setupItems))
	if err != nil {
		return err
	}
	opts := setup.Options{Tool: "o-lane", Data: a.dir}
	skip, err := setupSkipDefaults(items, opts)
	if err != nil {
		return err
	}
	if err := a.r.DeclareSetting(ochazuke.Setting{Name: ocli.SetupSkipSetting, Type: ochazuke.SettingList, Default: skip, Section: "setup", Description: "Setup item ids to skip; defaults omit earlier identical blocks in the same resolved directory"}); err != nil {
		return err
	}
	return ocli.DeclareSetup(a.r, items, opts)
}

// Co-located instruction files commonly import each other. Prefer the last
// declaration of an identical block there, without parsing harness-specific
// import syntax. This is only a default: an explicit setup.skip overrides it.
// (The same function is in o-questions and o-cmds; upstream todo: one copy in
// ochazuke's cli package.)
func setupSkipDefaults(items []setup.Item, opts setup.Options) ([]string, error) {
	var blocks []setup.Item
	for _, item := range items {
		if item.Kind == setup.Block && item.Follow {
			blocks = append(blocks, item)
		}
	}
	engine, err := setup.New(blocks, opts)
	if err != nil {
		return nil, err
	}
	rows, err := engine.List(context.Background())
	if err != nil {
		return nil, err
	}
	type block struct{ dir, begin, end, content string }
	seen := map[block]bool{}
	skip := []string{}
	for n := len(rows) - 1; n >= 0; n-- {
		row := rows[n]
		path, err := filepath.EvalSymlinks(row.Path)
		if err != nil {
			continue
		}
		key := block{filepath.Dir(path), row.Begin, row.End, row.Content}
		if seen[key] {
			skip = append(skip, row.ID)
		}
		seen[key] = true
	}
	return skip, nil
}
