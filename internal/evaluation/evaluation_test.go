package evaluation

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSyntheticRelevance(t *testing.T) {
	r, err := Run(context.Background(), FakeAgent{}, "fake-agent contract evaluation, not AI quality", DefaultLimits)
	b, _ := json.MarshalIndent(r, "", "  ")
	t.Log(string(b))
	if err != nil {
		t.Fatal(err)
	}
}
