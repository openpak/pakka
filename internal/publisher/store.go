package publisher

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// jsonFile persists one JSON document atomically under a state directory.
// With no directory it is a no-op (memory only).
type jsonFile struct {
	path string
}

func newJSONFile(dir, name string) (jsonFile, error) {
	if dir == "" {
		return jsonFile{}, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return jsonFile{}, err
	}
	return jsonFile{path: filepath.Join(dir, name)}, nil
}

func (f jsonFile) load(v any) error {
	if f.path == "" {
		return nil
	}
	raw, err := os.ReadFile(f.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func (f jsonFile) save(v any) error {
	if f.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

// doneStore remembers which event ids already published (tool + event id),
// so a repeat never reaches News or OpenVerse twice even when the far side
// has no idempotency of its own.
type doneStore struct {
	mu   sync.Mutex
	file jsonFile
	done map[string]Result
}

func newDoneStore(dir string) (*doneStore, error) {
	f, err := newJSONFile(dir, "publisher-done.json")
	if err != nil {
		return nil, err
	}
	d := &doneStore{file: f, done: map[string]Result{}}
	if err := f.load(&d.done); err != nil {
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
	return d.file.save(d.done)
}
