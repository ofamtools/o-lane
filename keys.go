package main

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// unknownKeys names each key of row that known does not hold, with the
// nearest known key when one is close (did-you-mean). Nothing is refused:
// the caller warns and keeps the rest of the row.
func unknownKeys(row map[string]any, known []string) []string {
	var out []string
	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if slices.Contains(known, k) {
			continue
		}
		text := fmt.Sprintf("unknown key %q was not saved", k)
		if near := nearest(k, known); near != "" {
			text += fmt.Sprintf(" (did you mean %q?)", near)
		}
		out = append(out, text)
	}
	return out
}

// nearest is the known word closest to w by edit distance, when it is
// within two edits or a third of the word's length; "" otherwise.
func nearest(w string, known []string) string {
	best, bestD := "", max(2, len(w)/3)+1
	for _, k := range known {
		if d := editDistance(strings.ToLower(w), k); d < bestD {
			best, bestD = k, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// jsonFields are the JSON names of a struct's fields.
func jsonFields(v any) []string {
	var out []string
	t := reflect.TypeOf(v)
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}
