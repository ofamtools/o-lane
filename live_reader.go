package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	Status  string `json:"state"` // the stream names this field state
	// Data holds the state of an event the event command returns as a
	// document row (mtm: data.state); Status is the top-level form.
	Data struct {
		State string `json:"state"`
	} `json:"data"`
}

// eventDoc is one document of the event command: its rows, with the cursor to
// resume after. A line without rows is a single event (a stream).
type eventDoc struct {
	Events  []liveEvent `json:"events"`
	LastSeq uint64      `json:"last_seq"`
	BootID  string      `json:"boot_id"`
}

// decodeEvents turns one output line of the event command into events. A
// document (an object with an events array) becomes its rows, each stamped
// with the document's boot id, and, when sawBoot is false, a leading
// "subscribed" event whose seq is the document's cursor: the same ack a stream
// opens with. A single event decodes as itself. polled reports a document.
func decodeEvents(line []byte, sawBoot bool) (events []liveEvent, polled bool, err error) {
	var probe map[string]json.RawMessage
	if err = json.Unmarshal(line, &probe); err != nil {
		return nil, false, err
	}
	if _, ok := probe["events"]; !ok {
		var event liveEvent
		if err = json.Unmarshal(line, &event); err != nil {
			return nil, false, err
		}
		event.normalize()
		return []liveEvent{event}, false, nil
	}
	var doc eventDoc
	if err = json.Unmarshal(line, &doc); err != nil {
		return nil, true, err
	}
	if !sawBoot {
		events = append(events, liveEvent{Type: "subscribed", Seq: doc.LastSeq, BootID: doc.BootID})
	}
	for _, event := range doc.Events {
		if event.BootID == "" {
			event.BootID = doc.BootID
		}
		event.normalize()
		events = append(events, event)
	}
	return events, true, nil
}

func (e *liveEvent) normalize() {
	if e.Status == "" {
		e.Status = e.Data.State
	}
}

type liveReader struct {
	run       commandRunner
	cmds      paneCommands
	mu        sync.Mutex
	panes     []Pane
	snapshots liveSnapshots
	wake      chan struct{}
	changed   chan struct{}
}

func newLiveReader(run commandRunner, cmds paneCommands, panes []Pane, snapshots liveSnapshots) *liveReader {
	return &liveReader{run: run, cmds: cmds, panes: append([]Pane(nil), panes...), snapshots: snapshots, wake: make(chan struct{}, 1), changed: make(chan struct{}, 1)}
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
		copy := liveSnapshot{windows: map[string]string{}, statusTimes: map[string]time.Time{}, err: snap.err}
		for id, status := range snap.windows {
			copy.windows[id] = status
		}
		for id, at := range snap.statusTimes {
			copy.statusTimes[id] = at
		}
		out[key] = copy
	}
	return out
}
func (r *liveReader) refresh(ctx context.Context) {
	r.mu.Lock()
	panes := append([]Pane(nil), r.panes...)
	r.mu.Unlock()
	snapshots := snapshotScopes(ctx, panes, r.run, r.cmds)
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
		// Any candidate can change a prefix's uniqueness, including a close
		// that leaves one surviving window. aggregateLive resolves the winner.
		if scopeOf(p) == key && p.Window != "" && strings.HasPrefix(event.Window, p.Window) {
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
		snap.windows[event.Window] = event.Status
	}
	// Events do not carry a status-change timestamp. Drop the snapshot time
	// rather than attaching an old status's time to this observation.
	delete(snap.statusTimes, event.Window)
	r.snapshots[key] = snap
	if relevant {
		r.notify()
	}
	return false
}

// eventLineHandler decodes each output line of the events command and sends
// its rows on events; a document line marks sawDoc and that the boot id is known.
func eventLineHandler(ctx context.Context, events chan<- liveEvent, sawBoot *bool, sawDoc *atomic.Bool) func([]byte) error {
	return func(line []byte) error {
		rows, doc, err := decodeEvents(line, *sawBoot)
		if err != nil {
			return fmt.Errorf("pane events: %w", err)
		}
		if doc {
			sawDoc.Store(true)
			*sawBoot = true
		}
		for _, event := range rows {
			select {
			case events <- event:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
}

// One reader owns one subscription. Cache replacement and notifications are
// bounded; only the UI loop touches settings or framework objects. Seq numbers
// may jump because the stream is filtered: only an explicit gap means data loss.
func (r *liveReader) Run(ctx context.Context) {
	seq := uint64(0)
	boot := ""
	polled := false
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		started := time.Now()
		streamCtx, cancel := context.WithCancel(ctx)
		events := make(chan liveEvent, 64)
		ended := make(chan error, 1)
		argv := append([]string{}, r.cmds.Events...)
		if boot != "" {
			argv = append(argv, expand(r.cmds.Resume, "{after-seq}", strconv.FormatUint(seq, 10), "{boot-id}", boot)...)
		}
		sawBoot := boot != ""
		var sawDoc atomic.Bool
		go func() {
			ended <- streamCommand(streamCtx, r.run, argv, eventLineHandler(streamCtx, events, &sawBoot, &sawDoc))
			close(events)
		}()
		// The ack's seq is a replay ceiling, not a delivered-event cursor. After a
		// snapshot, ignore replay below that ceiling so old events cannot regress it.
		ceiling := uint64(0)
		// A document-style command is re-run from a cursor and sends no ack
		// then: the reader stays subscribed across the runs.
		ready := polled && boot != ""
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
		if err == nil && sawDoc.Load() {
			// One document per run is the normal end of a polled command.
			polled = true
			backoff = 100 * time.Millisecond
			continue
		}
		if err == nil {
			err = io.EOF
		}
		r.unavailable(fmt.Errorf("pane event stream disconnected: %w", err))
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
