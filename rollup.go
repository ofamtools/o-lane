package main

import (
	"errors"
	"sort"
)

// Complete is a status that closes work: one with the ends role, or the
// archive role (a lane put away counts as finished with). Both are data in
// lanes.statuses.
func (s *Store) complete(status string) bool {
	st := s.statuses()[status]
	return st.Ends || st.Archive
}

// rollups counts, for every lane, the lanes below it (all descendants, not
// only children) whose status has the ends role, out of all those not in an
// archive status. Archived lanes are put away and count in neither. One pass
// over the parent chains, safe against cycles.
func (s *Store) rollups(rows []Lane) map[string][2]int {
	statuses := s.statuses()
	parent := map[string]string{}
	for _, q := range rows {
		parent[q.ID] = q.Parent
	}
	out := map[string][2]int{}
	for _, c := range rows {
		if c.Deleted || statuses[c.Status].Archive {
			continue
		}
		seen := map[string]bool{c.ID: true}
		for id := parent[c.ID]; id != "" && !seen[id]; id = parent[id] {
			seen[id] = true
			n := out[id]
			n[1]++
			if statuses[c.Status].Ends {
				n[0]++
			}
			out[id] = n
		}
	}
	return out
}

// FinishedTree is one root whose whole tree is complete.
type FinishedTree struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Lanes int    `json:"lanes"`
}

// ArchiveFinished is the result of lane archive --finished-trees.
type ArchiveFinished struct {
	DryRun   bool           `json:"dry_run"`
	Archived []FinishedTree `json:"archived"`
	Kept     int            `json:"kept"`
}

// finishedRoots are the roots (not yet in an archive status) whose every
// lane, itself included, is complete. A tree with one incomplete lane is not
// listed; roots are in list order.
func (s *Store) finishedRoots(rows []Lane) (roots []FinishedTree, kept int) {
	statuses := s.statuses()
	parent := map[string]string{}
	for _, q := range rows {
		parent[q.ID] = q.Parent
	}
	// rootOf walks to the top of the lane's parent chain; "" for a cycle.
	rootOf := func(id string) string {
		seen := map[string]bool{}
		for !seen[id] {
			seen[id] = true
			if parent[id] == "" {
				return id
			}
			id = parent[id]
		}
		return ""
	}
	size, ok := map[string]int{}, map[string]bool{}
	for _, q := range rows {
		if q.Deleted {
			continue
		}
		r := rootOf(q.ID)
		if r == "" {
			continue
		}
		if _, seen := ok[r]; !seen {
			ok[r] = true
		}
		size[r]++
		ok[r] = ok[r] && s.complete(q.Status)
	}
	for _, q := range rows {
		if q.Deleted || q.Parent != "" || statuses[q.Status].Archive {
			continue
		}
		if ok[q.ID] {
			roots = append(roots, FinishedTree{ID: q.ID, Name: q.Name, Lanes: size[q.ID]})
		} else {
			kept++
		}
	}
	sort.SliceStable(roots, func(i, j int) bool { return roots[i].Name < roots[j].Name })
	return
}

// ArchiveFinished archives the root of every fully complete tree in one
// change (one undo); the descendants keep their statuses and are hidden with
// the root, and restoring the root brings the whole tree back. With dryRun it
// reports and writes nothing.
func (s *Store) ArchiveFinished(dryRun bool, note string) (ArchiveFinished, error) {
	out := ArchiveFinished{DryRun: dryRun, Archived: []FinishedTree{}}
	archive, ok := s.archiveStatus()
	if !ok {
		return out, errors.New("no lanes.statuses row has archive:true; add one (for example name archived) to archive lanes")
	}
	if dryRun {
		rows, e := s.List()
		if e != nil {
			return out, e
		}
		out.Archived, out.Kept = s.finishedRoots(rows)
		return out, nil
	}
	e := s.change("archive", func(rows *[]Lane) error {
		out.Archived, out.Kept = s.finishedRoots(*rows)
		for _, t := range out.Archived {
			for i := range *rows {
				if (*rows)[i].ID == t.ID {
					if e := s.status(&(*rows)[i], archive.Name, note); e != nil {
						return e
					}
				}
			}
		}
		return nil
	})
	return out, e
}
