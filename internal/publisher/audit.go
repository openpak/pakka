package publisher

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
	path string
	tail []AuditEntry
	log  *slog.Logger
	now  func() time.Time
}

const auditTail = 200

// NewAudit opens the log under dir (empty = memory only).
func NewAudit(dir string, log *slog.Logger) (*Audit, error) {
	a := &Audit{log: log, now: time.Now}
	if dir == "" {
		return a, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("audit dir: %w", err)
	}
	a.path = filepath.Join(dir, "publisher-audit.jsonl")
	if f, err := os.Open(a.path); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			var e AuditEntry
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				a.push(e)
			}
		}
		f.Close()
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
	if a.path == "" {
		return nil
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("audit log: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("audit log: %w", err)
	}
	return f.Sync()
}

// Recent returns up to n newest entries, oldest first.
func (a *Audit) Recent(n int) []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n <= 0 || n > len(a.tail) {
		n = len(a.tail)
	}
	return append([]AuditEntry(nil), a.tail[len(a.tail)-n:]...)
}
