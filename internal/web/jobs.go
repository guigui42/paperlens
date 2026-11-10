package web

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/guigui42/paperlens/internal/paperless"
	"github.com/guigui42/paperlens/internal/search"
)

type job struct {
	ID, Owner, Query, State, Error string
	Filters                        paperless.Filters
	Output                         search.Output
	Created                        time.Time
	ctx                            context.Context
	cancel                         context.CancelFunc
	keyword                        bool
}
type jobs struct {
	mu       sync.Mutex
	items    map[string]*job
	queue    chan *job
	engine   *search.Engine
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	timeout  time.Duration
	capacity int
}

func newJobs(engine *search.Engine, workers, queued int, timeout time.Duration) *jobs {
	ctx, cancel := context.WithCancel(context.Background())
	j := &jobs{items: map[string]*job{}, queue: make(chan *job, workers+queued), engine: engine, ctx: ctx, cancel: cancel, timeout: timeout, capacity: workers + queued}
	for range workers {
		j.wg.Add(1)
		go j.worker()
	}
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				j.mu.Lock()
				j.prune()
				j.mu.Unlock()
			}
		}
	}()
	return j
}
func (j *jobs) prune() {
	for id, item := range j.items {
		if time.Since(item.Created) > 10*time.Minute {
			item.cancel()
			delete(j.items, id)
		}
	}
}
func (j *jobs) start(owner, query string, f paperless.Filters, keyword bool) (job, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.prune()
	active := 0
	for _, item := range j.items {
		if item.ctx.Err() == nil && item.State != "done" && item.State != "error" {
			active++
		}
	}
	if active >= j.capacity || len(j.items) >= 64 {
		return job{}, errors.New("search is busy; wait for an active search to finish")
	}
	for _, item := range j.items {
		if item.Owner == owner && item.State != "done" && item.State != "error" {
			item.cancel()
			item.State = "cancelled"
		}
	}
	ctx, cancel := context.WithTimeout(j.ctx, j.timeout)
	item := &job{ID: randomID(), Owner: owner, Query: query, Filters: f, State: "Queued", Created: time.Now(), ctx: ctx, cancel: cancel, keyword: keyword}
	select {
	case j.queue <- item:
		j.items[item.ID] = item
		return *item, nil
	default:
		cancel()
		return job{}, errors.New("search queue is full; try again later")
	}
}
func (j *jobs) worker() {
	defer j.wg.Done()
	for {
		select {
		case <-j.ctx.Done():
			return
		case item := <-j.queue:
			out, err := j.engine.Run(item.ctx, item.Query, item.Filters, item.keyword, func(stage string) {
				j.mu.Lock()
				defer j.mu.Unlock()
				if item.ctx.Err() == nil {
					item.State = stage
				}
			})
			j.mu.Lock()
			switch {
			case errors.Is(item.ctx.Err(), context.DeadlineExceeded):
				item.State = "expired"
				item.Error = "Search deadline reached. Try a more focused query or keyword search."
			case item.ctx.Err() != nil:
				item.State = "cancelled"
			case err != nil:
				item.State = "error"
				item.Error = err.Error()
			default:
				item.State = "done"
				item.Output = out
			}
			item.cancel()
			j.mu.Unlock()
		}
	}
}
func (j *jobs) get(id, owner string) (job, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.prune()
	item, ok := j.items[id]
	if !ok || item.Owner != owner {
		return job{}, false
	}
	return *item, true
}
func (j *jobs) stop(id, owner string) (job, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	item, ok := j.items[id]
	if !ok || item.Owner != owner {
		return job{}, false
	}
	if item.State != "done" && item.State != "error" {
		item.cancel()
		item.State = "cancelled"
	}
	return *item, true
}
func (j *jobs) close() { j.cancel(); j.wg.Wait() }
func (j job) running() bool {
	return j.State != "done" && j.State != "error" && j.State != "cancelled" && j.State != "expired"
}
