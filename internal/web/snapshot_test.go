package web

import (
	"context"
	"errors"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guigui42/paperlens/internal/paperless"
)

type metadataProbeAPI struct {
	paperless.API
	calls atomic.Int32
	fail  atomic.Bool
}

func (a *metadataProbeAPI) Metadata(ctx context.Context) (paperless.Vocabulary, error) {
	a.calls.Add(1)
	if a.fail.Load() {
		return paperless.Vocabulary{}, errors.New("synthetic metadata failure")
	}
	return a.API.Metadata(ctx)
}

func finishedSearch(t *testing.T, h *harness, query string) string {
	t.Helper()
	_, home := h.request(t, "GET", "/", "", "")
	_, fragment := h.request(t, "POST", "/search", url.Values{"q": {query}, "mode": {"keyword"}, "csrf": {hidden(t, home, "csrf")}}.Encode(), "application/x-www-form-urlencoded")
	m := regexp.MustCompile(`hx-get="/jobs/([^"]+)"`).FindStringSubmatch(fragment)
	if len(m) < 2 {
		t.Fatal("job not admitted", fragment)
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		_, fragment = h.request(t, "GET", "/jobs/"+m[1], "", "")
		if strings.Contains(fragment, "Keyword search") {
			return m[1]
		}
		select {
		case <-deadline.C:
			t.Fatal("job did not finish")
		case <-ticker.C:
		}
	}
}

func TestPollingUsesStableVocabularyAndBoundsPages(t *testing.T) {
	h := newHarness(t)
	api := &metadataProbeAPI{API: h.app.api}
	h.app.api = api
	h.app.jobs.engine.API = api
	id := finishedSearch(t, h, "latest home insurance")
	calls := api.calls.Load()
	api.fail.Store(true)
	for _, page := range []string{"0", "-999", "bad", "1", "1000000000000000000", strconv.Itoa(math.MaxInt), "9999999999999999999999999999999999999"} {
		t.Run(page, func(t *testing.T) {
			status, body := h.request(t, "GET", "/jobs/"+id+"?page="+url.QueryEscape(page), "", "")
			if status != 200 || !strings.Contains(body, "Home insurance certificate 2025") || !strings.Contains(body, "Example household") {
				t.Fatal("snapshot or pagination broken", status)
			}
			if page != "0" && !strings.Contains(body, "Showing the first page") {
				t.Fatal("invalid page was not explained")
			}
		})
	}
	if api.calls.Load() != calls {
		t.Fatal("polling fetched metadata")
	}
}

func TestEmptySnapshotRejectsOversizedPageWithoutPanic(t *testing.T) {
	h := newHarness(t)
	id := finishedSearch(t, h, "qzjxvnbpahelqr")
	status, body := h.request(t, "GET", "/jobs/"+id+"?page=999999999999999999999999", "", "")
	if status != 200 || !strings.Contains(body, "No matching evidence found.") {
		t.Fatal("empty snapshot pagination failed")
	}
}

func TestHomeExplainsUnavailableExclusionPolicy(t *testing.T) {
	h := newHarness(t)
	h.app.config.AI = true
	h.app.jobs.engine.ExcludedTags = []string{"Missing required privacy tag"}
	_, body := h.request(t, "GET", "/", "", "")
	if !strings.Contains(body, "required exclusion tag") || !strings.Contains(body, `name="mode" value="keyword" checked`) {
		t.Fatal("unresolved exclusion policy was hidden")
	}
}
