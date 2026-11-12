package main

import "testing"

func TestPaneEnv(t *testing.T) {
	m := map[string]string{"TUIOS_PANE_ID": "old", "TUIOS_SESSION": "s"}
	get := func(k string) string { return m[k] }
	if got := paneEnv(get, "PANE_ID"); got != "old" {
		t.Fatal("falls back to TUIOS_", got)
	}
	m["MTM_PANE_ID"] = "new"
	if got := paneEnv(get, "PANE_ID"); got != "new" {
		t.Fatal("MTM_ wins", got)
	}
	if got := paneEnv(get, "SESSION"); got != "s" {
		t.Fatal(got)
	}
	if got := paneEnv(get, "HARNESS"); got != "" {
		t.Fatal(got)
	}
}
