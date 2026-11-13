package main

import (
	"strings"
	"testing"
)

// lane define takes the props lane set takes (status, reason) and names an
// unknown key with did-you-mean instead of dropping it in silence.
func TestDefineTakesSetPropsAndNamesUnknownKeys(t *testing.T) {
	f := newCLI(t)
	v := f.call("lane", "define", "--lanes", `[{"name":"x","status":"running","reason":"pane 3","stauts":"done"}]`)
	rows, ok := v.([]any)
	if !ok || len(rows) != 1 {
		t.Fatal(v)
	}
	if got := rows[0].(map[string]any)["status"]; got != "running" {
		t.Fatalf("status %v", got)
	}
	if !strings.Contains(f.errs.String(), `unknown key "stauts" was not saved (did you mean "status"?)`) {
		t.Fatalf("warning: %q", f.errs.String())
	}
	if got := f.call("lane", "get", "x", "reason"); got != "pane 3" {
		t.Fatalf("reason %v", got)
	}
}

// Record first: a missing parent saves the lane at the top level, an empty
// name saves untitled-STAMP, an empty note is a note; each with a notice
// where something was changed.
func TestRecordFirstLaneInput(t *testing.T) {
	f := newCLI(t)
	q := f.lane("add", "child", "--parent", "nosuch")
	if q["parent"] != nil && q["parent"] != "" {
		t.Fatalf("parent %v", q["parent"])
	}
	if !strings.Contains(f.errs.String(), "unknown lane nosuch; saved at the top level") {
		t.Fatalf("notice: %q", f.errs.String())
	}
	u := f.lane("add", "--name", "")
	if name, _ := u["name"].(string); !strings.HasPrefix(name, "untitled-") {
		t.Fatalf("name %q", name)
	}
	n := f.lane("annotate", "child", "--text", "")
	if h, _ := n["history"].([]any); len(h) != 2 {
		t.Fatalf("history %v", n["history"])
	}
}

// reopen returns a closed lane to the open status it had before it closed
// (lanes.restore-presets); a reason written alone is the status's reason.
func TestReopenRestoresAndReasonAlone(t *testing.T) {
	f := newCLI(t)
	f.lane("add", "r")
	f.lane("set", "r", "status=review", "reason=branch a")
	f.lane("set", "r", "reason=branch b")
	if got := f.call("lane", "get", "r", "reason"); got != "branch b" {
		t.Fatalf("reason %v", got)
	}
	f.lane("finish", "r")
	if got := f.lane("reopen", "r")["status"]; got != "review" {
		t.Fatalf("reopen %v", got)
	}
	if _, e := f.a.r.Settings().SetSession("lanes.restore-presets", []string{}); e != nil {
		t.Fatal(e)
	}
	f.lane("finish", "r")
	if got := f.lane("reopen", "r")["status"]; got != "planned" {
		t.Fatalf("restore off: %v", got)
	}
}
