package main

import (
	"fmt"
	"slices"
	"strings"
)

// archiveProp is the lane prop with the archive role; its value is whether
// the lane's status row carries archive:true.
const archiveProp = "archived"

// statusProp is the lane prop that holds the lifecycle status.
const statusProp = "status"

// reasonProp is the lane prop that holds why the lane is in its status: the
// note of the latest status change. Written in the same set as a status (or
// archived) change it is that change's note; written alone it appends a note.
const reasonProp = "reason"

// statusNames are the names of the configured status rows.
func statusNames(rows []Status) []string {
	out := make([]string, 0, len(rows))
	for _, st := range rows {
		out = append(out, st.Name)
	}
	return out
}

// archiveFlag reads the archive prop's value from a typed or text write.
func archiveFlag(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "yes", "1", "on":
			return true, nil
		case "false", "no", "0", "off", "":
			return false, nil
		}
	}
	return false, fmt.Errorf("%s: expected true or false", archiveProp)
}

// lifecycleProps are the lane props a write records as a lifecycle change
// (status, archived) or as that change's note (reason). lane set and lane
// define write them through the same patch.
var lifecycleProps = []string{statusProp, archiveProp, reasonProp}

// patch applies one set to a lane: the props in key order, with the
// reason as the note of the status (or archived) change in the same write.
func (s *Store) patch(q *Lane, changes map[string]any) error {
	keys := make([]string, 0, len(changes))
	for key := range changes {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	edited := false
	reason, hasReason := "", false
	if v, ok := changes[reasonProp]; ok && v != nil {
		reason, hasReason = fmt.Sprint(v), true
	}
	for _, key := range keys {
		if key == reasonProp {
			continue
		}
		if key == statusProp {
			if e := s.status(q, fmt.Sprint(changes[key]), reason); e != nil {
				return e
			}
			hasReason = false
			continue
		}
		if key == archiveProp {
			on, e := archiveFlag(changes[key])
			if e != nil {
				return e
			}
			if e = s.setArchived(q, on, reason); e != nil {
				return e
			}
			hasReason = false
			continue
		}
		if e := setSpec(&q.Spec, key, changes[key]); e != nil {
			return e
		}
		edited = true
	}
	if hasReason && strings.TrimSpace(reason) != "" {
		// A reason written alone is the current status's reason (statusReason).
		s.entry(q, reasonProp, "", "", reason, nil, nil)
	}
	if edited {
		s.entry(q, "edit", "", "", "set", nil, nil)
	}
	return nil
}
