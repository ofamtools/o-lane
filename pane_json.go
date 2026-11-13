package main

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Pane CLI lists may be bare arrays or objects containing the array under one
// of the named fields (sessions or items).
func decodePaneList[T any](data []byte, fields ...string) ([]T, error) {
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			return nil, err
		}
		var ok bool
		for _, field := range fields {
			if data, ok = object[field]; ok {
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("pane listing is missing %q", fields)
		}
	}
	var rows []T
	err := json.Unmarshal(data, &rows)
	return rows, err
}
