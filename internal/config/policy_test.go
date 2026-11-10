package config

import (
	"strings"
	"testing"
)

func TestRequiredExclusionConfiguration(t *testing.T) {
	for _, raw := range []string{"", " ", ",", "Private,", strings.Repeat("x", 201)} {
		t.Run(raw[:min(len(raw), 20)], func(t *testing.T) {
			defaults(t)
			t.Setenv("PAPERLENS_DEMO", "true")
			t.Setenv("PAPERLENS_EXCLUDED_TAGS", raw)
			if _, err := Load(); err == nil {
				t.Fatal("invalid required tag policy accepted")
			}
		})
	}
	defaults(t)
	t.Setenv("PAPERLENS_DEMO", "true")
	t.Setenv("PAPERLENS_EXCLUDED_TAGS", " Export financier, Private ")
	c, err := Load()
	if err != nil || len(c.ExcludedTags) != 2 || c.ExcludedTags[0] != "Export financier" || c.ExcludedTags[1] != "Private" {
		t.Fatal("required tag names not parsed", err)
	}
}
