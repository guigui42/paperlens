package main

import "testing"

func TestParseLabels(t *testing.T) {
	good := `[{"query":"tax 2023","expected":12,"holdout":true},{"query":"nothing","expected":0}]`
	cases, err := parseLabels([]byte(good))
	if err != nil || len(cases) != 2 || cases[0].Expected != 12 || !cases[0].Holdout || cases[1].Expected != 0 {
		t.Fatalf("parseLabels(good) = %+v, %v", cases, err)
	}
	for name, raw := range map[string]string{
		"missing expected": `[{"query":"a"}]`,
		"null expected":    `[{"query":"a","expected":null}]`,
		"misspelled":       `[{"query":"a","expectd":3}]`,
		"negative":         `[{"query":"a","expected":-1}]`,
		"empty query":      `[{"query":" ","expected":1}]`,
		"empty list":       `[]`,
		"trailing data":    `[{"query":"a","expected":1}] []`,
	} {
		if _, err := parseLabels([]byte(raw)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
