package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// noServerStatus is the data behind "the tool is up but has nothing to talk
// to": the exit status a program uses when its daemon or server is not
// running, by program name. tuios exits 3 for that (a distinct status so a
// script tells "no daemon" from "the daemon answered" and from a real failure;
// cmd/tuios/diagnostics.go noDaemonStatus) and prints an empty JSON list. A
// tool is added here as one row; nothing else changes.
var noServerStatus = map[string]int{"tuios": 3}

// commandError is a finished command that exited nonzero. Its message says
// which command failed, with what status, and what it printed on stderr,
// instead of the bare "exit status N" of os/exec.
type commandError struct {
	argv   []string
	status int
	stderr string
	err    error
}

func (e *commandError) Error() string {
	cmd := strings.Join(e.argv, " ")
	if e.stderr != "" {
		return fmt.Sprintf("%s failed (exit %d): %s", cmd, e.status, e.stderr)
	}
	return fmt.Sprintf("%s failed (exit %d)", cmd, e.status)
}
func (e *commandError) Unwrap() error { return e.err }

// wrapExit turns an os/exec exit error into a commandError.
func wrapExit(argv []string, err error) error {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return err
	}
	stderr := strings.TrimSpace(string(exit.Stderr))
	if i := strings.IndexByte(stderr, '\n'); i >= 0 {
		stderr = strings.TrimSpace(stderr[:i])
	}
	return &commandError{argv: append([]string(nil), argv...), status: exit.ExitCode(), stderr: stderr, err: err}
}

// noServer reports whether err is the program's "no server running" status
// (see noServerStatus).
func noServer(err error) bool {
	var ce *commandError
	if !errors.As(err, &ce) || len(ce.argv) == 0 {
		return false
	}
	status, ok := noServerStatus[ce.argv[0]]
	return ok && status == ce.status
}
