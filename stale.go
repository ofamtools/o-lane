package main

import (
	"fmt"
	"strings"
	"time"
)

// Staleness considers current participants on the lane and working descendants,
// independent of role filters and presentation labels. Unknown evidence
// cannot prove that nobody is working. It never changes status or history.
func (s *Store) stalePanes(rows []Lane, q Lane) []Pane {
	return s.paneTree(rows).stale(q, s.statuses())
}

func (t *paneTree) stale(q Lane, statuses map[string]Status) []Pane {
	return t.subtree(q, func(row Lane) bool { return statuses[row.Status].Starts })
}

func staleLane(q Lane, statuses map[string]Status, live Live, enabled bool, now time.Time) (bool, string) {
	if !enabled || q.Deleted || !statuses[q.Status].Starts || live.Status == "off" {
		return false, ""
	}
	parts := []string{}
	for _, p := range live.Panes {
		if !p.known || p.working || p.Error != "" {
			return false, ""
		}
		who := "pane " + p.Window
		if p.AgentID != "" {
			who = "agent " + p.AgentID
		}
		when := " (observed " + now.UTC().Format(time.RFC3339) + "; status-change time unavailable)"
		if p.StatusSince != nil {
			when = " since " + p.StatusSince.UTC().Format(time.RFC3339)
		}
		parts = append(parts, fmt.Sprintf("%s is %s%s", who, p.Status, when))
	}
	if len(parts) == 0 {
		parts = append(parts, "no pane or agent is associated")
	}
	return true, "No agent is working on this lane: " + strings.Join(parts, "; ") + ". Set review, parked or done with a note."
}
