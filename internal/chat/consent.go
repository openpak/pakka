package chat

import (
	"sync"
	"time"

	"pakka/internal/store"
)

// NoticeVersion identifies the data-use notice text players accept. Bump it
// when the wording changes so everyone is asked again.
const NoticeVersion = "2026-11-01"

// Consents records, per account, which notice version was accepted.
type Consents struct {
	mu  sync.Mutex
	doc store.Doc
	on  bool
	m   map[string]consent
}

type consent struct {
	Version string    `json:"version"`
	At      time.Time `json:"at"`
}

// NewConsents loads consents from st (nil = memory only).
func NewConsents(st *store.Store) *Consents {
	c := &Consents{m: map[string]consent{}}
	if st.Durable() {
		c.doc, c.on = st.Doc("chat-consent"), true
		_ = c.doc.Load(&c.m)
		if c.m == nil {
			c.m = map[string]consent{}
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
	if c.on {
		_ = c.doc.Save(c.m)
	}
}
