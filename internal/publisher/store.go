package publisher

import (
	"fmt"
	"sync"

	"pakka/internal/store"
)

// doneStore remembers which event ids already published (tool + event id),
// so a repeat never reaches News or OpenVerse twice even when the far side
// has no idempotency of its own.
type doneStore struct {
	mu   sync.Mutex
	file store.Doc
	done map[string]Result
}

func newDoneStore(st *store.Store) (*doneStore, error) {
	d := &doneStore{file: st.Doc("publisher-done"), done: map[string]Result{}}
	if err := d.file.Load(&d.done); err != nil {
		return nil, fmt.Errorf("done store: %w", err)
	}
	return d, nil
}

func (d *doneStore) get(key string) (Result, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r, ok := d.done[key]
	return r, ok
}

func (d *doneStore) put(key string, r Result) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.done[key] = r
	return d.file.Save(d.done)
}
