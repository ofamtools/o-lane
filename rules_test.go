package main

import (
	"io"
	"strings"
	"testing"

	"ofam.dev/ochazuke/rulecheck"
)

// TestRules runs ochazuke's hiddenkeys over o-lane' runtime and the
// recorded places in testdata/rules/places.json: a hidden default row takes
// no key that did something at any place and mode without it. The other
// rule checks have their own tests here (names, refusals, lexicon, nouns).
func TestRules(t *testing.T) {
	a, err := newApp(fixture(t), strings.NewReader(""), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	places, err := rulecheck.LoadPlaces("testdata/rules/places.json")
	if err != nil {
		t.Fatal(err)
	}
	const own = "has its own test in this package"
	rulecheck.Check(t, rulecheck.Config{
		Runtime: a.r,
		Places:  places,
		Root:    ".",
		Off: map[string]string{
			"deadkeys": own, "names": own, "refusals": own, "lexicon": own, "nouns": own,
		},
	})
}
