// Package search enforces retrieval, evidence and privacy boundaries independently of the model.
package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/guigui42/paperlens/internal/paperless"
)

// Limits bounds each job regardless of agent behavior.
type Limits struct{ Searches, Candidates, Excerpts, Characters int }

// Candidate deliberately omits OCR and excluded records.
type Candidate struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	Filename string `json:"filename"`
	Date     string `json:"issue_date"`
}

// Evidence is the exact material eligible for a model quote.
type Evidence struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

// Pick is the only accepted model result, never a URL/title/date.
type Pick struct {
	ID       int    `json:"id"`
	Quote    string `json:"quote"`
	Strength string `json:"strength"`
}

// Ranking is the strict model output contract.
type Ranking struct {
	Results []Pick `json:"results"`
}

// Seed is the original-query context supplied with the first model turn.
// Excerpts were read through Excerpt, so they count toward the budget and
// remain valid quote sources.
type Seed struct {
	Candidates []Candidate `json:"candidates"`
	Excerpts   []Evidence  `json:"excerpts"`
}

// Tools exposes app-owned functions, not arbitrary archive access.
type Tools interface {
	Lookup() paperless.Vocabulary
	Search(context.Context, string) ([]Candidate, error)
	Excerpt(context.Context, int) (Evidence, error)
	Seed() Seed
	Limits() Limits
}

// Agent runs one disposable search session.
type Agent interface {
	Rank(context.Context, string, Tools) (Ranking, error)
}

// Starter is optionally implemented by an Agent whose per-search runtime can
// start before retrieval, so start-up overlaps the original Paperless query.
// The context only bounds start-up. On error Begin returns no Run and has
// already released anything it started.
type Starter interface {
	Begin(context.Context) (Run, error)
}

// Run is one started per-search runtime. Rank may be called at most once.
// Close releases the runtime if Rank did not; it is safe to call after Rank
// and the engine always calls it.
type Run interface {
	Rank(context.Context, string, Tools) (Ranking, error)
	Close() error
}

type started struct {
	run Run
	err error
}

// Result contains canonical metadata and validated evidence.
type Result struct {
	Document       paperless.Document
	Quote, Reason  string
	Weak, Excluded bool
}

// Output is a stable snapshot, safe to paginate without rerunning the model.
type Output struct {
	Results           []Result
	Mode, Notice      string
	Candidates, Calls int
	Limited           bool
	Vocabulary        paperless.Vocabulary
}

// Engine orchestrates the original query and optional model retrieval.
type Engine struct {
	API          paperless.API
	Agent        Agent
	Limits       Limits
	ExcludedTags []string
}

type job struct {
	mu        sync.Mutex
	api       paperless.API
	filters   paperless.Filters
	vocab     paperless.Vocabulary
	excluded  map[int]bool
	knownTags map[int]bool
	limits    Limits
	docs      map[int]paperless.Document
	ranks     map[int]float64
	evidence  map[int]string
	calls     int
	limited   bool
	stage     func(string)
	queries   map[string]bool
	reserve   int
	seed      Seed
}

// Run never relaxes explicit filters and never sends excluded metadata to the agent.
func (e *Engine) Run(ctx context.Context, query string, f paperless.Filters, keyword bool, stage func(string)) (out Output, err error) {
	if strings.TrimSpace(query) == "" || len([]rune(query)) > 500 {
		return Output{}, errors.New("enter a query of 1 to 500 characters")
	}
	// Timings and counts only: never log query text, titles or OCR.
	start, mark := time.Now(), time.Now()
	var metadataTime, paperlessTime, prefetchTime, agentTime, startWaitTime time.Duration
	var j *job
	var overlap bool
	defer func() {
		attrs := []any{"mode", out.Mode, "failed", err != nil, "metadata_ms", metadataTime.Milliseconds(), "paperless_ms", paperlessTime.Milliseconds(),
			"prefetch_ms", prefetchTime.Milliseconds(), "agent_ms", agentTime.Milliseconds(), "overlap", overlap, "start_wait_ms", startWaitTime.Milliseconds(),
			"total_ms", time.Since(start).Milliseconds(), "results", len(out.Results)}
		if j != nil {
			j.mu.Lock()
			attrs = append(attrs, "search_calls", j.calls, "candidates", len(j.docs), "excerpts", len(j.evidence), "prefetched", len(j.seed.Excerpts))
			j.mu.Unlock()
		}
		slog.Info("search timing", attrs...)
	}()
	v, err := e.API.Metadata(ctx)
	metadataTime = time.Since(mark)
	if err != nil {
		return Output{}, err
	}
	if err = f.Validate(v); err != nil {
		return Output{}, err
	}
	excluded, policyErr := e.exclusionIDs(v)
	useAI := !keyword && e.Agent != nil && policyErr == nil
	var begun chan started
	if starter, ok := e.Agent.(Starter); ok && useAI {
		overlap = true
		begun = make(chan started, 1)
		go func() {
			run, beginErr := starter.Begin(ctx)
			begun <- started{run, beginErr}
		}()
		// Every exit releases the runtime: an unconsumed start is awaited and
		// closed so no CLI process or state directory outlives Run. A failed
		// Paperless query does not interrupt start-up; ctx still bounds it.
		defer func() {
			if begun == nil {
				return
			}
			if b := <-begun; b.err == nil && b.run != nil {
				if closeErr := b.run.Close(); closeErr != nil {
					slog.Warn("unused AI runtime cleanup failed")
				}
			}
		}()
	}
	j = &job{api: e.API, filters: f, vocab: v, excluded: excluded, knownTags: map[int]bool{}, limits: e.Limits, docs: map[int]paperless.Document{}, ranks: map[int]float64{}, evidence: map[int]string{}, stage: stage, queries: map[string]bool{}}
	if useAI && e.Limits.Searches > 1 {
		// Keep room for model expansions so a broad original query cannot
		// occupy every candidate slot before they run.
		j.reserve = e.Limits.Candidates / 4
	}
	for _, t := range v.Tags {
		j.knownTags[t.ID] = true
	}
	if stage != nil {
		stage("Searching Paperless")
	}
	mark = time.Now()
	_, err = j.Search(ctx, query)
	paperlessTime = time.Since(mark)
	if err != nil {
		return Output{}, err
	}
	out = Output{Mode: "Keyword search", Vocabulary: v}
	if keyword || e.Agent == nil {
		out.Results = j.fallback(query)
		out.Notice = "Ordinary Paperless keyword results. No document text was sent to AI."
	} else if policyErr != nil {
		slog.Warn("AI search blocked", "reason", "exclusion_policy_unresolved")
		out.Results = j.fallback(query)
		out.Mode = "Keyword fallback"
		out.Notice = ErrExclusionPolicy.Error() + ". No document text was sent to AI."
	} else {
		if stage != nil {
			stage("Finding and checking evidence")
		}
		mark = time.Now()
		j.prefetch(ctx, query)
		prefetchTime = time.Since(mark)
		mark = time.Now()
		var rank Ranking
		var agentErr error
		if begun != nil {
			b := <-begun
			begun = nil
			startWaitTime = time.Since(mark)
			switch {
			case b.err != nil:
				agentErr = b.err
			case b.run == nil:
				agentErr = ErrAIRuntime
			default:
				rank, agentErr = b.run.Rank(ctx, query, j)
				if closeErr := b.run.Close(); closeErr != nil {
					agentErr = closeErr
				}
			}
		} else {
			rank, agentErr = e.Agent.Rank(ctx, query, j)
		}
		agentTime = time.Since(mark)
		if ctx.Err() != nil {
			return Output{}, ctx.Err()
		}
		if agentErr == nil {
			out.Results, agentErr = j.validate(rank)
		}
		if agentErr != nil {
			out.Results = j.fallback(query)
			out.Mode = "Keyword fallback"
			out.Notice = "AI search could not produce verified results. Showing ordinary keyword matches instead."
			if safe := agentFailureNotice(agentErr); safe != "" {
				out.Notice = safe + ". Showing keyword results."
			}
			switch {
			case errors.Is(agentErr, ErrAIDisabled):
				out.Notice = ErrAIDisabled.Error() + ". Showing keyword results."
			case errors.Is(agentErr, ErrAITeardown):
				out.Notice = ErrAITeardown.Error() + ". A new search can use AI."
			}
		} else {
			out.Mode = "Evidence-ranked search"
			if Latest(query) {
				sort.SliceStable(out.Results, func(a, b int) bool {
					x, y := out.Results[a], out.Results[b]
					if x.Weak != y.Weak {
						return !x.Weak
					}
					sx, sy := TitleScore(query, x.Document), TitleScore(query, y.Document)
					if sx != sy {
						return sx > sy
					}
					return x.Document.Created > y.Document.Created
				})
			}
			for _, r := range j.fallback(query) {
				if r.Excluded {
					out.Results = append(out.Results, r)
				}
			}
		}
	}
	out.Candidates = len(j.docs)
	out.Calls = j.calls
	out.Limited = j.limited
	return out, nil
}
func (j *job) isExcluded(d paperless.Document) bool {
	for _, t := range d.Tags {
		if j.excluded[t] || (j.knownTags != nil && !j.knownTags[t]) {
			return true
		}
	}
	return false
}
func (j *job) Lookup() paperless.Vocabulary {
	v := j.vocab
	v.Tags = nil
	for _, t := range j.vocab.Tags {
		if !j.excluded[t.ID] {
			v.Tags = append(v.Tags, t)
		}
	}
	return v
}
func (j *job) Search(ctx context.Context, query string) ([]Candidate, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "" || len([]rune(query)) > 500 {
		return nil, errors.New("query exceeds limit")
	}
	if j.queries[query] {
		return j.candidates(), nil
	}
	if j.calls >= j.limits.Searches {
		j.limited = true
		return nil, errors.New("search call budget exhausted")
	}
	capacity := j.limits.Candidates
	if j.calls == 0 {
		capacity = max(1, capacity-j.reserve)
	}
	j.calls++
	p, err := j.api.Search(ctx, query, j.filters, capacity)
	if err != nil {
		return nil, err
	}
	if p.Next != nil || p.Count > len(p.Results) {
		j.limited = true
	}
	if j.queries == nil {
		j.queries = map[string]bool{}
	}
	j.queries[query] = true
	seen := map[int]bool{}
	for n, d := range p.Results {
		if d.ID < 1 || seen[d.ID] || !j.filters.Matches(d) {
			continue
		}
		seen[d.ID] = true
		if _, ok := j.docs[d.ID]; !ok && len(j.docs) >= capacity {
			j.limited = true
			continue
		}
		d.Content = excerptWindow(d.Content, query, j.limits.Characters)
		j.docs[d.ID] = d
		j.ranks[d.ID] += 1 / float64(60+n+1)
	}
	return j.candidates(), nil
}

func (j *job) candidates() []Candidate {
	var result []Candidate
	for _, d := range j.docs {
		if !j.isExcluded(d) {
			result = append(result, Candidate{ID: d.ID, Title: clip(d.Title, 250), Filename: clip(filename(d), 250), Date: d.Created})
		}
	}
	sort.Slice(result, func(a, b int) bool {
		x, y := result[a].ID, result[b].ID
		if j.ranks[x] != j.ranks[y] {
			return j.ranks[x] > j.ranks[y]
		}
		return x < y
	})
	return result
}

// Seed returns the prefetched original-query context.
func (j *job) Seed() Seed {
	j.mu.Lock()
	defer j.mu.Unlock()
	return Seed{Candidates: append([]Candidate{}, j.seed.Candidates...), Excerpts: append([]Evidence{}, j.seed.Excerpts...)}
}

// Limits reports the job budgets for tool descriptions.
func (j *job) Limits() Limits { return j.limits }

// prefetch reads the likeliest original-query excerpts before the first model
// turn, always leaving at least two reads for the model's own choices.
func (j *job) prefetch(ctx context.Context, query string) {
	j.mu.Lock()
	cs := j.candidates()
	ordered := append([]Candidate{}, cs...)
	scores := map[int]int{}
	for _, c := range ordered {
		scores[c.ID] = TitleScore(query, j.docs[c.ID])
	}
	j.mu.Unlock()
	sort.SliceStable(ordered, func(a, b int) bool { return scores[ordered[a].ID] > scores[ordered[b].ID] })
	excerpts := []Evidence{}
	for _, c := range ordered[:max(0, min(len(ordered), 3, j.limits.Excerpts-2))] {
		if ev, err := j.Excerpt(ctx, c.ID); err == nil {
			excerpts = append(excerpts, ev)
		}
	}
	j.mu.Lock()
	j.seed = Seed{Candidates: cs, Excerpts: excerpts}
	j.mu.Unlock()
}

func (j *job) Excerpt(ctx context.Context, id int) (Evidence, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Evidence{}, err
	}
	d, ok := j.docs[id]
	if !ok || j.isExcluded(d) {
		return Evidence{}, errors.New("document is not an eligible retrieved candidate")
	}
	if text, ok := j.evidence[id]; ok {
		return Evidence{id, text}, nil
	}
	if len(j.evidence) >= j.limits.Excerpts {
		j.limited = true
		return Evidence{}, errors.New("excerpt budget exhausted")
	}
	// OCR was retrieved in the bounded search response. A fresh unfiltered read
	// could race a tag/filter change and bypass the policy snapshot.
	text := clip(d.Title+"\n"+filename(d)+"\n"+d.Content, j.limits.Characters)
	j.evidence[id] = text
	return Evidence{id, text}, nil
}
func (j *job) validate(r Ranking) ([]Result, error) {
	if len(r.Results) > j.limits.Excerpts {
		return nil, errors.New("too many ranked documents")
	}
	results := make([]Result, 0, len(r.Results))
	seen := map[int]bool{}
	for _, p := range r.Results {
		d, ok := j.docs[p.ID]
		text, read := j.evidence[p.ID]
		if !ok || !read || j.isExcluded(d) || seen[p.ID] || !j.filters.Matches(d) || strings.TrimSpace(p.Quote) == "" || len([]rune(p.Quote)) > 500 || !strings.Contains(text, p.Quote) || (p.Strength != "strong" && p.Strength != "weak") {
			return nil, errors.New("invalid or ungrounded model evidence")
		}
		seen[p.ID] = true
		reason := "Matched evidence in this document."
		if p.Strength == "weak" {
			reason = "Possible match. Check the quoted evidence."
		}
		results = append(results, Result{Document: d, Quote: p.Quote, Reason: reason, Weak: p.Strength == "weak"})
	}
	sort.SliceStable(results, func(i, k int) bool { return !results[i].Weak && results[k].Weak })
	return results, nil
}
func (j *job) fallback(query string) []Result {
	out := make([]Result, 0, len(j.docs))
	for _, d := range j.docs {
		excluded := j.isExcluded(d)
		reason := "Paperless keyword match; not AI-ranked."
		if excluded {
			reason = "Excluded from AI by the required tag policy or unavailable tag metadata. Keyword match only."
		}
		out = append(out, Result{Document: d, Quote: clip(d.Content, 300), Reason: reason, Excluded: excluded})
	}
	sort.SliceStable(out, func(a, b int) bool {
		x, y := out[a].Document, out[b].Document
		sx, sy := TitleScore(query, x), TitleScore(query, y)
		if sx != sy {
			return sx > sy
		}
		if Latest(query) && x.Created != y.Created {
			return x.Created > y.Created
		}
		if j.ranks[x.ID] != j.ranks[y.ID] {
			return j.ranks[x.ID] > j.ranks[y.ID]
		}
		return x.ID < y.ID
	})
	return out
}

// Decode rejects extra fields, duplicate JSON documents and unbounded output.
func Decode(raw string) (Ranking, error) {
	if len(raw) > 32000 {
		return Ranking{}, errors.New("ranking exceeds limit")
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var r Ranking
	if err := dec.Decode(&r); err != nil {
		return r, fmt.Errorf("invalid ranking: %w", err)
	}
	if r.Results == nil {
		return r, errors.New("results array is required")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return r, errors.New("unexpected trailing output")
	}
	return r, nil
}

// Fold provides deterministic accent/case normalization for local relevance signals.
func Fold(s string) string { return accentFold.Replace(strings.ToLower(s)) }

var accentFold = strings.NewReplacer("é", "e", "è", "e", "ê", "e", "ë", "e", "à", "a", "â", "a", "ä", "a", "î", "i", "ï", "i", "ô", "o", "ö", "o", "ù", "u", "û", "u", "ü", "u", "ç", "c", "œ", "oe")

// Latest detects recency intent without converting mentioned years into issue dates.
func Latest(q string) bool {
	q = Fold(q)
	return strings.Contains(q, "latest") || strings.Contains(q, "newest") || strings.Contains(q, "dernier") || strings.Contains(q, "recent")
}

// TitleScore protects direct title/filename relevance from incidental OCR hits.
func TitleScore(q string, d paperless.Document) int {
	hay := Fold(d.Title + " " + filename(d))
	score := 0
	for _, t := range strings.FieldsFunc(Fold(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(t) < 3 || strings.Contains(" latest newest dernier derniere recent trouve find avec pour the and mon mes une des ", " "+t+" ") {
			continue
		}
		if strings.Contains(hay, t) {
			score++
		}
	}
	return score
}
func filename(d paperless.Document) string {
	if d.Filename != "" {
		return d.Filename
	}
	return d.OriginalFilename
}
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// Keep a contiguous source window near a distinctive query term rather than
// dropping all evidence beyond the first page of a long OCR document.
func excerptWindow(content, query string, limit int) string {
	runes := []rune(content)
	if len(runes) <= limit {
		return content
	}
	terms := strings.FieldsFunc(Fold(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	sort.SliceStable(terms, func(i, j int) bool { return len(terms[i]) > len(terms[j]) })
	hay := Fold(content)
	for _, term := range terms {
		if len(term) < 4 || strings.Contains(" latest newest dernier derniere recent trouve find avec pour ", " "+term+" ") {
			continue
		}
		if pos := strings.Index(hay, term); pos >= 0 {
			sourcePos, normalizedBytes := 0, 0
			for sourcePos < len(runes) {
				width := len(Fold(string(runes[sourcePos])))
				if normalizedBytes+width > pos {
					break
				}
				normalizedBytes += width
				sourcePos++
			}
			start := max(0, min(len(runes)-limit, sourcePos-120))
			return string(runes[start : start+limit])
		}
	}
	return string(runes[:limit])
}
