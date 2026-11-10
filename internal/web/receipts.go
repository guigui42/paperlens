package web

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type receipt struct {
	ID, Owner, Task, State string
	Document               int
	Created                time.Time
}
type receipts struct {
	mu    sync.Mutex
	path  string
	items map[string]receipt
}

func openReceipts(dir string) (*receipts, error) {
	r := &receipts{path: filepath.Join(dir, "receipts.json"), items: map[string]receipt{}}
	b, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) > 1<<20 {
		return nil, errors.New("receipt storage exceeds size limit")
	}
	if err = json.Unmarshal(b, &r.items); err != nil {
		return nil, errors.New("invalid receipt storage; restore or inspect it before accepting uploads")
	}
	if len(r.items) > 200 {
		return nil, errors.New("too many upload receipts")
	}
	for id, item := range r.items {
		if time.Since(item.Created) > 24*time.Hour {
			delete(r.items, id)
			continue
		}
		if item.State == "uploading" {
			item.State = "unknown"
			r.items[id] = item
		}
	}
	if err = r.save(); err != nil {
		return nil, err
	}
	return r, nil
}
func (r *receipts) save() error {
	b, err := json.Marshal(r.items)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(r.path), "receipts-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, r.path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(r.path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (r *receipts) reserve(id, owner string) (receipt, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if item, ok := r.items[id]; ok {
		if item.Owner != owner {
			return receipt{}, false, errors.New("invalid upload receipt")
		}
		return item, false, nil
	}
	for key, item := range r.items {
		if time.Since(item.Created) > 24*time.Hour {
			delete(r.items, key)
		}
	}
	if len(r.items) >= 200 {
		return receipt{}, false, errors.New("upload receipt capacity reached; try later")
	}
	item := receipt{ID: id, Owner: owner, State: "uploading", Created: time.Now()}
	r.items[id] = item
	if err := r.save(); err != nil {
		delete(r.items, id)
		return receipt{}, false, err
	}
	return item, true, nil
}
func (r *receipts) update(item receipt) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[item.ID] = item
	return r.save()
}
func (r *receipts) get(id, owner string) (receipt, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	x, ok := r.items[id]
	return x, ok && x.Owner == owner && time.Since(x.Created) < 24*time.Hour
}
func (r *receipts) list(owner string) []receipt {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []receipt
	for _, x := range r.items {
		if x.Owner == owner && time.Since(x.Created) < 24*time.Hour {
			out = append(out, x)
		}
	}
	return out
}
