package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// NoticeVersion identifies the data-use notice text players accept. Bump it
// when the wording changes so everyone is asked again.
const NoticeVersion = "2026-11-01"

// Consents records, per account, which notice version was accepted.
type Consents struct {
	mu   sync.Mutex
	path string
	m    map[string]consent
}

type consent struct {
	Version string    `json:"version"`
	At      time.Time `json:"at"`
}

// NewConsents loads consents from dir (empty = memory only).
func NewConsents(dir string) *Consents {
	c := &Consents{m: map[string]consent{}}
	if dir != "" {
		c.path = filepath.Join(filepath.Dir(dir), "chat-consent.json")
		if data, err := os.ReadFile(c.path); err == nil {
			_ = json.Unmarshal(data, &c.m)
		}
	}
	return c
}

// Accepted reports whether account accepted the current notice.
func (c *Consents) Accepted(account string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[account].Version == NoticeVersion
}

// Accept stores account's acceptance of the current notice.
func (c *Consents) Accept(account string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[account] = consent{Version: NoticeVersion, At: time.Now().UTC()}
	c.save()
}

// Withdraw removes account's consent.
func (c *Consents) Withdraw(account string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, account)
	c.save()
}

func (c *Consents) save() {
	if c.path == "" {
		return
	}
	if data, err := json.Marshal(c.m); err == nil {
		tmp := c.path + ".tmp"
		if os.WriteFile(tmp, data, 0o600) == nil {
			_ = os.Rename(tmp, c.path)
		}
	}
}
