package main

import (
	"io"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
	"ofam.dev/ochazuke/cli"
)

// TestLaneOffersEveryCoreVerb: lane has a Store with only Get and Patch, and
// every thing noun has all seven core verbs. A verb its store cannot serve
// is listed and unavailable with a reason, never missing; a verb the store
// serves, or that o-lane declares itself (list, delete, archive), is not
// marked unavailable.
func TestLaneOffersEveryCoreVerb(t *testing.T) {
	a, err := newApp(fixture(t), strings.NewReader(""), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	for _, verb := range ochazuke.CoreVerbs() {
		op, ok := a.r.Operation("lane." + verb)
		if !ok {
			t.Errorf("lane.%s is not offered", verb)
			continue
		}
		if op.Unavailable != "" {
			t.Logf("lane.%s unavailable: %s", verb, op.Unavailable)
		}
	}
	if len(ochazuke.CoreVerbs()) != 7 {
		t.Errorf("core verbs = %v, want seven", ochazuke.CoreVerbs())
	}
	for _, verb := range []string{"get", "set", "list", "delete", "archive", "edit"} {
		if op, _ := a.r.Operation("lane." + verb); op.Unavailable != "" {
			t.Errorf("lane.%s is unavailable (%s) but lane serves it", verb, op.Unavailable)
		}
	}
	if op, _ := a.r.Operation("lane.create"); op.Unavailable == "" {
		t.Error("lane.create is available, but the store has no Create")
	}
}

// TestListAnswerParsesInTheHost: mtm's host reads a mounted noun's list
// with cli.DecodeRows (both the bare array and the standard {items, page,
// per_page, total} page are read), and writes with get and set {props}.
// lane's own list keeps its bare array, and the standard get and set read
// and write the same lane.
func TestListAnswerParsesInTheHost(t *testing.T) {
	f := newCLI(t)
	f.a.store.BackupDaily = false
	f.lane("add", "Release")
	f.lane("add", "Build", "--parent", "Release")
	f.call("lane", "list")
	rows, err := cli.DecodeRows("lane", f.out.Bytes())
	if err != nil || len(rows) != 2 {
		t.Fatalf("host cannot read lane list: %v rows=%v out=%s", err, rows, f.out.String())
	}
	for _, row := range rows {
		if row.ID == "" || row.Label == "" {
			t.Errorf("row without id or label: %+v", row)
		}
	}
	got := f.call("lane", "get", rows[0].ID)
	if m, _ := got.(map[string]any); m["id"] != rows[0].ID {
		t.Fatalf("get %s: %#v", rows[0].ID, got)
	}
	f.call("lane", "set", rows[0].ID, "--json", `{"props":{"scope":"from the host"}}`)
	if m, _ := f.call("lane", "get", rows[0].ID).(map[string]any); m["scope"] != "from the host" {
		t.Fatalf("set {props} did not write: %#v", m)
	}
}
