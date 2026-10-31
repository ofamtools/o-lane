package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"
)

// The same injectable argv runner handles both snapshots and streams. The
// production runner writes stdout here instead of retaining an endless buffer.
// A buffered fake may simply return its recorded NDJSON as before.
type commandOutputKey struct{}

func streamCommand(ctx context.Context, run commandRunner, argv []string, receive func([]byte) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	input, output := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		b, err := run(context.WithValue(ctx, commandOutputKey{}, io.Writer(output)), argv)
		if len(b) > 0 {
			if _, e := output.Write(b); err == nil {
				err = e
			}
		}
		_ = output.CloseWithError(err)
	}()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var err error
	for scanner.Scan() {
		if err = receive(scanner.Bytes()); err != nil {
			break
		}
	}
	if err == nil {
		err = scanner.Err()
	}
	cancel()
	_ = input.Close()
	<-done
	return err
}

type liveEvent struct {
	Type    string `json:"type"`
	Seq     uint64 `json:"seq"`
	BootID  string `json:"boot_id"`
	Host    string `json:"host"`
	Session string `json:"session"`
	Window  string `json:"window"`
	State   string `json:"state"`
}

type liveReader struct {
	run       commandRunner
	mu        sync.Mutex
	panes     []Pane
	snapshots liveSnapshots
	wake      chan struct{}
	changed   chan struct{}
}

func newLiveReader(run commandRunner, panes []Pane, snapshots liveSnapshots) *liveReader {
	return &liveReader{run: run, panes: append([]Pane(nil), panes...), snapshots: snapshots, wake: make(chan struct{}, 1), changed: make(chan struct{}, 1)}
}
func (r *liveReader) notify() {
	select {
	case r.changed <- struct{}{}:
	default:
	}
}
func (r *liveReader) current(panes []Pane) liveSnapshots {
	r.mu.Lock()
	defer r.mu.Unlock()
	known := map[liveScope]bool{}
	for _, p := range r.panes {
		if p.Window != "" {
			known[scopeOf(p)] = true
		}
	}
	changed := false
	for _, p := range panes {
		if p.Window != "" {
			if !known[scopeOf(p)] {
				changed = true
			}
		}
	}
	r.panes = append([]Pane(nil), panes...)
	if changed {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
	out := liveSnapshots{}
	for key, snap := range r.snapshots {
		copy := liveSnapshot{windows: map[string]string{}, err: snap.err}
		for id, state := range snap.windows {
			copy.windows[id] = state
		}
		out[key] = copy
	}
	return out
}
func (r *liveReader) refresh(ctx context.Context) {
	r.mu.Lock()
	panes := append([]Pane(nil), r.panes...)
	r.mu.Unlock()
	snapshots := snapshotScopes(ctx, panes, r.run)
	r.mu.Lock()
	r.snapshots = snapshots
	r.mu.Unlock()
	r.notify()
}
func (r *liveReader) unavailable(err error) {
	r.mu.Lock()
	for key, snap := range r.snapshots {
		snap.err = err
		r.snapshots[key] = snap
	}
	r.mu.Unlock()
	r.notify()
}
func (r *liveReader) apply(event liveEvent) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := liveScope{event.Host, event.Session}
	snap, ok := r.snapshots[key]
	if !ok {
		return false
	}
	relevant := false
	for _, p := range r.panes {
		if scopeOf(p) == key && p.Window == event.Window {
			relevant = true
			break
		}
	}
	if relevant && snap.err != nil {
		return true
	}
	if event.Type == "window-closed" {
		delete(snap.windows, event.Window)
	} else {
		snap.windows[event.Window] = event.State
	}
	r.snapshots[key] = snap
	if relevant {
		r.notify()
	}
	return false
}

// One reader owns one subscription. Cache replacement and notifications are
// bounded; only the UI loop touches settings or framework objects. Seq numbers
// may jump because the stream is filtered: only an explicit gap means data loss.
func (r *liveReader) Run(ctx context.Context) {
	seq := uint64(0)
	boot := ""
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		started := time.Now()
		streamCtx, cancel := context.WithCancel(ctx)
		events := make(chan liveEvent, 64)
		ended := make(chan error, 1)
		argv := []string{"tuios", "subscribe", "--types", "agent-state,window-closed", "--hosts"}
		if boot != "" {
			argv = append(argv, "--after-seq", strconv.FormatUint(seq, 10), "--boot-id", boot)
		}
		go func() {
			ended <- streamCommand(streamCtx, r.run, argv, func(line []byte) error {
				var event liveEvent
				if err := json.Unmarshal(line, &event); err != nil {
					return fmt.Errorf("tuios subscribe: %w", err)
				}
				select {
				case events <- event:
					return nil
				case <-streamCtx.Done():
					return streamCtx.Err()
				}
			})
			close(events)
		}()
		// The ack's seq is a replay ceiling, not a delivered-event cursor. After a
		// snapshot, ignore replay below that ceiling so old events cannot regress it.
		ceiling := uint64(0)
		ready := false
		for events != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
			case <-r.wake:
				if ready {
					r.refresh(ctx)
				}
			case event, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				if event.BootID != "" && event.BootID != boot {
					boot = event.BootID
					seq = 0
					ceiling = 0
				}
				switch event.Type {
				case "subscribed":
					ready = true
					ceiling = event.Seq
					seq = max(seq, event.Seq)
					r.refresh(ctx)
				case "gap":
					r.refresh(ctx)
				case "agent-state", "window-closed":
					if event.Seq <= ceiling || event.Seq <= seq {
						continue
					}
					if r.apply(event) {
						r.refresh(ctx)
					}
					seq = event.Seq
				}
			}
		}
		cancel()
		err := <-ended
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = io.EOF
		}
		r.unavailable(fmt.Errorf("tuios stream disconnected: %w", err))
		if time.Since(started) >= 5*time.Second {
			backoff = 100 * time.Millisecond
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}
