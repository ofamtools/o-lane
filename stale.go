package main

import (
	"fmt"
	"strings"
	"time"
)

// Staleness is a read-time projection of a lane's own participants, independent
// of subtree rollup, role filters and presentation labels. Unknown evidence
// cannot prove that nobody is working. It never changes state or history.
func staleLane(q Lane, states map[string]State, live Live, enabled bool, now time.Time) (bool, string) {
	if !enabled || q.Deleted || !states[q.State].Starts || live.State == "off" {
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
		when := " (observed " + now.UTC().Format(time.RFC3339) + "; state-change time unavailable)"
		if p.StateSince != nil {
			when = " since " + p.StateSince.UTC().Format(time.RFC3339)
		}
		parts = append(parts, fmt.Sprintf("%s is %s%s", who, p.State, when))
	}
	if len(parts) == 0 {
		parts = append(parts, "no pane or agent is associated")
	}
	return true, "No agent is working on this lane: " + strings.Join(parts, "; ") + ". Set review, parked or done with a note."
}
