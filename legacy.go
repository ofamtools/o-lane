package main

import (
	"bytes"
	"encoding/json"
)

// migrateLegacy reads lane data written before the lexicon rename (a lane's
// "state" field, history entries of kind "state") as the current names
// ("status"). It is a data migration, not a name alias: the next write of
// the file stores the new names. Text without a legacy key is returned as is.
func migrateLegacy(b []byte) ([]byte, error) {
	if !bytes.Contains(b, []byte(`"state"`)) {
		return b, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if !migrateNode(doc) {
		return b, nil
	}
	return json.Marshal(doc)
}

// migrateNode renames the legacy keys in place and reports whether it did.
// A lane is an object with a history; a history entry has an id, a "by" and
// a kind.
func migrateNode(v any) (changed bool) {
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			changed = migrateNode(item) || changed
		}
	case map[string]any:
		if _, ok := x["history"]; ok {
			if old, has := x["state"]; has {
				if _, taken := x["status"]; !taken {
					x["status"] = old
				}
				delete(x, "state")
				changed = true
			}
		}
		if _, ok := x["by"]; ok && x["kind"] == "state" {
			x["kind"] = "status"
			changed = true
		}
		for _, item := range x {
			changed = migrateNode(item) || changed
		}
	}
	return changed
}
