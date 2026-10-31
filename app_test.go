package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "TUIOS_") || strings.HasPrefix(key, "CLAUDE_") || strings.HasPrefix(key, "CODEX_") {
			t.Setenv(key, "")
		}
	}
	// Every default-runner test also has a fail-closed tuios on PATH. In
	// particular, MCP read answers must never reach a real daemon.
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "tuios"), []byte("#!/bin/sh\nprintf 'tuios unavailable in fixture\\n' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	for _, k := range []string{"TUIOS_SESSION", "TUIOS_PANE_ID", "TUIOS_HARNESS", "TUIOS_AGENT_NAME", "AGENT_HARNESS", "AGENT_MODEL", "OCHAZUKE_ACTOR", "OCHAZUKE_HARNESS", "OCHAZUKE_MODEL", "O_LANE_ACTOR", "CODEX_THREAD_ID", "CODEX_CI", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_SESSION_ID", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(k, "")
	}
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, filepath.Join(base, k))
	}
	t.Setenv("OCHAZUKE_ACTOR", "owner")
	return filepath.Join(base, "store")
}
func TestCatalog(t *testing.T) {
	dir := fixture(t)
	var out, errs bytes.Buffer
	if err := Main([]string{"--data", dir, "catalog"}, &bytes.Buffer{}, &out, &errs); err != nil {
		t.Fatal(err, errs.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err, out.String())
	}
	if doc["schema"] != float64(1) {
		t.Fatalf("catalog: %s", out.String())
	}
}
