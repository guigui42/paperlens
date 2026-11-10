package web

import (
	"net/url"
	"strings"
	"testing"
)

func TestCancelAfterCompletionKeepsResultsAndVocabulary(t *testing.T) {
	h := newHarness(t)
	id := finishedSearch(t, h, "home insurance")
	_, home := h.request(t, "GET", "/", "", "")
	status, body := h.request(t, "POST", "/jobs/"+id+"/cancel", url.Values{"csrf": {hidden(t, home, "csrf")}}.Encode(), "application/x-www-form-urlencoded")
	if status != 200 || !strings.Contains(body, "Home insurance certificate") || !strings.Contains(body, "Example household") || strings.Contains(body, "No matching evidence found.") {
		t.Fatal("late cancellation discarded completed results")
	}
}
