package evaluation

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

func words(s string) []string {
	return strings.FieldsFunc(search.Fold(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// Strict approximates Paperless full-text search, which requires every
// accent-folded query word to appear. It is a simplified proxy, not Whoosh:
// a query word matches any document word it is a prefix of (a stand-in for
// stemming), there are no stopwords or field queries, and result order comes
// from the demo archive's scorer. The demo archive itself scores any matching
// word, which hides recall lost to filler words ("latest") or words the
// document never uses.
type Strict struct{ paperless.API }

// Search keeps the documents where every query word starts a document word.
// It filters the whole archive, because the demo scorer ignores short words
// and stopwords ("82", "the") and would hide documents that contain them.
func (s Strict) Search(ctx context.Context, q string, f paperless.Filters, limit int) (paperless.Page, error) {
	p, err := s.API.Search(ctx, q, f, 100)
	if err != nil {
		return p, err
	}
	scored := map[int]int{}
	for i, d := range p.Results {
		scored[d.ID] = i
	}
	terms := words(q)
	var matches []paperless.Document
	// Fixture IDs are contiguous from 1.
	for id := 1; ; id++ {
		d, err := s.Document(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return paperless.Page{}, ctx.Err()
			}
			break
		}
		if i, ok := scored[id]; ok {
			d = p.Results[i]
		}
		if len(terms) > 0 && f.Matches(d) && hasAll(terms, words(d.Title+" "+d.Content)) {
			matches = append(matches, d)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		a, aok := scored[matches[i].ID]
		b, bok := scored[matches[j].ID]
		return aok && (!bok || a < b)
	})
	out := paperless.Page{Count: len(matches), Results: append([]paperless.Document{}, matches[:min(max(limit, 0), len(matches))]...)}
	if out.Count > len(out.Results) {
		next := "?page=2"
		out.Next = &next
	}
	return out, nil
}

func hasAll(terms, tokens []string) bool {
	for _, t := range terms {
		if !slices.ContainsFunc(tokens, func(w string) bool { return strings.HasPrefix(w, t) }) {
			return false
		}
	}
	return true
}

// recorder notes every document ID Paperless returned during one search job,
// including ones the engine later drops (candidate cap, filters, exclusions).
type recorder struct {
	paperless.API
	mu   sync.Mutex
	seen map[int]bool
}

func (r *recorder) Search(ctx context.Context, q string, f paperless.Filters, limit int) (paperless.Page, error) {
	p, err := r.API.Search(ctx, q, f, limit)
	r.mu.Lock()
	for _, d := range p.Results {
		r.seen[d.ID] = true
	}
	r.mu.Unlock()
	return p, err
}

func (r *recorder) reset() {
	r.mu.Lock()
	r.seen = map[int]bool{}
	r.mu.Unlock()
}

func (r *recorder) retrieved(id int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen[id]
}
