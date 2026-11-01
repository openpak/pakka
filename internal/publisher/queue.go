package publisher

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Queue errors.
var (
	ErrNoDraft     = errors.New("no such draft")
	ErrNotPending  = errors.New("draft is not waiting for a decision")
	ErrQueueFull   = errors.New("review queue is full")
	maxPendingSize = 200
)

// Queue is the review queue: proposals wait here until a staff member
// approves or rejects them. It persists to the state directory.
type Queue struct {
	mu     sync.Mutex
	file   jsonFile
	drafts map[string]*Draft
	now    func() time.Time
}

// NewQueue loads the queue from dir (empty = memory only).
func NewQueue(dir string) (*Queue, error) {
	f, err := newJSONFile(dir, "publisher-queue.json")
	if err != nil {
		return nil, err
	}
	q := &Queue{file: f, drafts: map[string]*Draft{}, now: time.Now}
	var list []*Draft
	if err := f.load(&list); err != nil {
		return nil, fmt.Errorf("queue: %w", err)
	}
	for _, d := range list {
		// A draft that was mid-approval when the process died is retryable.
		if d.Status == StatusApproving {
			d.Status = StatusFailed
			d.Error = "interrupted; approve again to retry"
		}
		q.drafts[d.ID] = d
	}
	return q, nil
}

func (q *Queue) persist() error {
	list := make([]*Draft, 0, len(q.drafts))
	for _, d := range q.drafts {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	return q.file.save(list)
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Add queues an action. A draft with the same event id that is still pending
// or published is returned instead of a second one.
func (q *Queue) Add(a Action, preview, by string) (Draft, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if a.EventID != "" {
		for _, d := range q.drafts {
			if d.Action.EventID == a.EventID && d.Status != StatusRejected && d.Status != StatusFailed {
				return *d, nil
			}
		}
	}
	pending := 0
	for _, d := range q.drafts {
		if d.Status == StatusPending {
			pending++
		}
	}
	if pending >= maxPendingSize {
		return Draft{}, ErrQueueFull
	}
	id := newID()
	for q.drafts[id] != nil {
		id = newID()
	}
	if a.EventID == "" {
		a.EventID = "draft:" + id
	}
	d := &Draft{ID: id, Action: a, Preview: preview, CreatedBy: by, CreatedAt: q.now().UTC(), Status: StatusPending}
	q.drafts[id] = d
	if err := q.persist(); err != nil {
		delete(q.drafts, id)
		return Draft{}, err
	}
	return *d, nil
}

// Get returns one draft.
func (q *Queue) Get(id string) (Draft, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	d, ok := q.drafts[id]
	if !ok {
		return Draft{}, false
	}
	return *d, true
}

// List returns drafts with the given status ("" = all), oldest first.
func (q *Queue) List(status string) []Draft {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []Draft
	for _, d := range q.drafts {
		if status == "" || d.Status == status {
			out = append(out, *d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Counts returns how many drafts have each status.
func (q *Queue) Counts() map[string]int {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := map[string]int{}
	for _, d := range q.drafts {
		out[d.Status]++
	}
	return out
}

// Begin moves a pending (or failed) draft to approving, exactly once: a
// second approval while one is running, or after it published, is refused.
func (q *Queue) Begin(id, reviewer string) (Draft, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	d, ok := q.drafts[id]
	if !ok {
		return Draft{}, ErrNoDraft
	}
	if d.Status != StatusPending && d.Status != StatusFailed {
		return Draft{}, fmt.Errorf("%w (status %s)", ErrNotPending, d.Status)
	}
	d.Status, d.Reviewer, d.Error = StatusApproving, reviewer, ""
	_ = q.persist()
	return *d, nil
}

// Finish records the outcome of an approval.
func (q *Queue) Finish(id string, res *Result, runErr error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	d, ok := q.drafts[id]
	if !ok {
		return
	}
	t := q.now().UTC()
	d.DecidedAt = &t
	if runErr != nil {
		d.Status, d.Error = StatusFailed, runErr.Error()
	} else {
		d.Status, d.Result = StatusPublished, res
	}
	_ = q.persist()
}

// Reject discards a pending draft.
func (q *Queue) Reject(id, reviewer string) (Draft, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	d, ok := q.drafts[id]
	if !ok {
		return Draft{}, ErrNoDraft
	}
	if d.Status != StatusPending && d.Status != StatusFailed {
		return Draft{}, fmt.Errorf("%w (status %s)", ErrNotPending, d.Status)
	}
	t := q.now().UTC()
	d.Status, d.Reviewer, d.DecidedAt = StatusRejected, reviewer, &t
	_ = q.persist()
	return *d, nil
}
