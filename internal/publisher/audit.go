package publisher

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"pakka/internal/store"
	"sync"
	"time"
)

// AuditEntry is one line of the audit log.
type AuditEntry struct {
	Time     time.Time `json:"time"`
	Actor    string    `json:"actor"`
	Event    string    `json:"event"` // attempt, published, denied, queued, approved, rejected, failed, killed
	Tool     string    `json:"tool,omitempty"`
	Category string    `json:"category,omitempty"`
	EventID  string    `json:"event_id,omitempty"`
	DraftID  string    `json:"draft_id,omitempty"`
	Detail   string    `json:"detail,omitempty"`
}

// Audit is an append-only JSON-lines log plus a bounded in-memory tail.
// With no directory it keeps the tail and the process log only.
type Audit struct {
	mu   sync.Mutex
	st   *store.Store
	tail []AuditEntry
	log  *slog.Logger
	now  func() time.Time
}

const auditTail = 200

// NewAudit opens the log under dir (empty = memory only).
func NewAudit(st *store.Store, log *slog.Logger) (*Audit, error) {
	a := &Audit{log: log, now: time.Now, st: st}
	rows, err := st.AuditTail(auditTail)
	if err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}
	for _, raw := range rows {
		var e AuditEntry
		if json.Unmarshal(raw, &e) == nil {
			a.push(e)
		}
	}
	return a, nil
}

func (a *Audit) push(e AuditEntry) {
	a.tail = append(a.tail, e)
	if len(a.tail) > auditTail {
		a.tail = a.tail[len(a.tail)-auditTail:]
	}
}

// Record writes one entry. A write failure is returned so a caller that is
// about to publish can refuse instead of publishing unrecorded.
func (a *Audit) Record(e AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e.Time.IsZero() {
		e.Time = a.now().UTC()
	}
	a.push(e)
	a.log.Info("publisher audit", "actor", e.Actor, "event", e.Event, "tool", e.Tool,
		"category", e.Category, "event_id", e.EventID, "draft", e.DraftID, "detail", e.Detail)
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := a.st.AuditAppend(raw); err != nil {
		return fmt.Errorf("audit log: %w", err)
	}
	return nil
}

func (a *Audit) Recent(n int) []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n <= 0 || n > len(a.tail) {
		n = len(a.tail)
	}
	return append([]AuditEntry(nil), a.tail[len(a.tail)-n:]...)
}
