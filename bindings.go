package main

import "embed"

// bindings.json holds key rows as data, loaded into the default layer
// (below the user's bindings.json) after the Go rows of page.go, so a row
// there never replaces one of those.

//go:embed bindings.json
var bindingsFS embed.FS

func (a *App) loadDefaultBindings() error {
	return a.r.LoadDefaultBindingsFS(bindingsFS, "bindings.json")
}
