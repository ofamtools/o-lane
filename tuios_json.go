package main

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// tuios CLI lists may be bare arrays or objects containing the named array.
func decodeTuiosList[T any](data []byte, field string) ([]T, error) {
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			return nil, err
		}
		var ok bool
		data, ok = object[field]
		if !ok {
			return nil, fmt.Errorf("tuios listing is missing %q", field)
		}
	}
	var rows []T
	err := json.Unmarshal(data, &rows)
	return rows, err
}
