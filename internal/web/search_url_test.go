package web

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/guigui42/paperlens/internal/paperless"
)

func TestSearchURLRoundTrip(t *testing.T) {
	f := paperless.Filters{After: "2025-01-01", Correspondent: 1, Tag: 2, Field: 1, Value: "a&b"}
	got := searchURL("home & insurance", f, true)
	if got != "/?after=2025-01-01&correspondent=1&field=1&mode=keyword&q=home+%26+insurance&tag=2&value=a%26b" {
		t.Fatal(got)
	}
	if searchURL("x", paperless.Filters{}, false) != "/?mode=ai&q=x" {
		t.Fatal("AI mode must be explicit")
	}
	h := newHarness(t)
	_, body := h.request(t, "GET", got, "", "")
	for _, want := range []string{`data-autorun`, `value="home &amp; insurance"`, `name="mode" value="keyword" checked`, `value="2025-01-01"`, `value="a&amp;b"`} {
		if !strings.Contains(body, want) {
			t.Fatal("search URL was not restored:", want)
		}
	}
	if !regexp.MustCompile(`<option value="1" selected>`).MatchString(body) {
		t.Fatal("correspondent not restored")
	}
	_, body = h.request(t, "GET", "/?q=x&mode=ai", "", "")
	if strings.Contains(body, `name="mode" value="keyword" checked`) {
		t.Fatal("explicit AI mode ignored")
	}
}

func TestInvalidSearchURLDoesNotAutorun(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"/?q=x&tag=abc", "/?q=x&tag=99999", "/?q=" + url.QueryEscape(" ")} {
		_, body := h.request(t, "GET", path, "", "")
		if strings.Contains(body, "data-autorun") || !strings.Contains(body, "This search link") {
			t.Fatal("invalid search link was run or silently accepted:", path)
		}
	}
	_, body := h.request(t, "GET", "/", "", "")
	if strings.Contains(body, "data-autorun") {
		t.Fatal("plain home autoran")
	}
}

func TestUnavailableAIPolicyDoesNotAutorunAIURL(t *testing.T) {
	h := newHarness(t)
	h.app.config.AI = true
	h.app.jobs.engine.ExcludedTags = []string{"Missing required privacy tag"}
	_, body := h.request(t, "GET", "/?q=home+insurance&mode=ai", "", "")
	if strings.Contains(body, "data-autorun") || !strings.Contains(body, `name="mode" value="keyword" checked`) {
		t.Fatal("URL autoran AI despite the unavailable exclusion policy")
	}
}

func TestSearchResultsExposeURLAndCredits(t *testing.T) {
	h := newHarness(t)
	_, home := h.request(t, "GET", "/", "", "")
	_, fragment := h.request(t, "POST", "/search", url.Values{"q": {"home insurance"}, "mode": {"keyword"}, "tag": {"1"}, "csrf": {hidden(t, home, "csrf")}}.Encode(), "application/x-www-form-urlencoded")
	if !strings.Contains(fragment, `data-search-accepted="true"`) || !strings.Contains(fragment, `data-search-url="/?mode=keyword&amp;q=home&#43;insurance&amp;tag=1"`) {
		t.Fatal("accepted search did not expose its canonical URL", fragment)
	}
	id := finishedSearch(t, h, "home insurance")
	_, fragment = h.request(t, "GET", "/jobs/"+id, "", "")
	if strings.Contains(fragment, "data-search-accepted") || !strings.Contains(fragment, `data-credits-status="none"`) || !strings.Contains(fragment, "0 AI credits (no AI used)") {
		t.Fatal("finished keyword search did not show credits", fragment)
	}
}
