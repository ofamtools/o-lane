package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	if err := os.MkdirAll("/tmp/o-lane-core", 0700); err != nil {
		t.Fatal(err)
	}
	base, err := os.MkdirTemp("/tmp/o-lane-core/", "test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
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
