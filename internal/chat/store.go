package chat

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"pakka/internal/store"
)

// Message roles stored in a conversation.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message is one stored turn. Tool traffic is never stored.
type Message struct {
	ID   string    `json:"id"`
	Role string    `json:"role"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// Conversation is one chat owned by exactly one account.
type Conversation struct {
	ID       string    `json:"id"`
	Account  string    `json:"account"`
	Title    string    `json:"title"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Messages []Message `json:"messages"`
}

// Errors returned by the store.
var (
	ErrNotFound = errors.New("conversation not found")
	ErrLimit    = errors.New("conversation limit reached")
)

// Store keeps conversations in memory and writes each through to a file
// under dir (empty dir = memory only). Every lookup is keyed by the owning
// account, so one account can never read another's conversation.
type Store struct {
	mu    sync.Mutex
	st    *store.Store
	byID  map[string]*Conversation
	max   int
	clock func() time.Time
}

// NewStore opens the store and loads any persisted conversations.
func NewStore(st *store.Store, maxPerAccount int) (*Store, error) {
	s := &Store{st: st, byID: map[string]*Conversation{}, max: maxPerAccount, clock: time.Now}
	docs, err := st.ChatLoad()
	if err != nil {
		return nil, err
	}
	for _, data := range docs {
		var c Conversation
		if json.Unmarshal(data, &c) == nil && c.ID != "" && c.Account != "" && validID(c.ID) {
			s.byID[c.ID] = &c
		}
	}
	return s, nil
}

func validID(id string) bool {
	if len(id) != 24 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Create starts an empty conversation for account.
func (s *Store) Create(account string) (Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.byID {
		if c.Account == account {
			n++
		}
	}
	if n >= s.max {
		return Conversation{}, ErrLimit
	}
	now := s.clock().UTC()
	c := &Conversation{ID: newID(), Account: account, Created: now, Updated: now, Messages: []Message{}}
	s.byID[c.ID] = c
	s.persist(c)
	return *c, nil
}

// Get returns account's conversation id.
func (s *Store) Get(account, id string) (Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.byID[id]
	if !ok || c.Account != account {
		return Conversation{}, ErrNotFound
	}
	out := *c
	out.Messages = append([]Message(nil), c.Messages...)
	return out, nil
}

// List returns account's conversations, newest first, without messages.
func (s *Store) List(account string) []Conversation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Conversation{}
	for _, c := range s.byID {
		if c.Account == account {
			cc := *c
			cc.Messages = nil
			out = append(out, cc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// Append adds a message to account's conversation; the first user message
// titles it.
func (s *Store) Append(account, id string, m Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.byID[id]
	if !ok || c.Account != account {
		return ErrNotFound
	}
	if m.ID == "" {
		m.ID = newID()
	}
	m.At = s.clock().UTC()
	c.Messages = append(c.Messages, m)
	c.Updated = m.At
	if c.Title == "" && m.Role == RoleUser {
		c.Title = titleOf(m.Text)
	}
	s.persist(c)
	return nil
}

// Delete removes account's conversation and its file.
func (s *Store) Delete(account, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.byID[id]
	if !ok || c.Account != account {
		return ErrNotFound
	}
	delete(s.byID, id)
	_ = s.st.ChatDelete(id)
	return nil
}

// EraseAccount deletes every conversation account owns and reports how many.
// Used when the account is erased everywhere; repeating it is harmless.
func (s *Store) EraseAccount(account string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, c := range s.byID {
		if c.Account == account {
			delete(s.byID, id)
			_ = s.st.ChatDelete(id)
			n++
		}
	}
	_ = s.st.ChatDeleteAccount(account)
	return n
}

// Purge deletes conversations idle longer than retention and returns how many.
func (s *Store) Purge(retention time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := s.clock().Add(-retention)
	n := 0
	for id, c := range s.byID {
		if c.Updated.Before(cut) {
			delete(s.byID, id)
			_ = s.st.ChatDelete(id)
			n++
		}
	}
	return n
}

func (s *Store) persist(c *Conversation) {
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	_ = s.st.ChatSave(c.ID, c.Account, data)
}

func titleOf(text string) string {
	t := strings.Join(strings.Fields(text), " ")
	r := []rune(t)
	if len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return t
}

// accountKey is a short stable digest for logs; account ids are not logged.
func accountKey(account string) string {
	sum := sha256.Sum256([]byte(account))
	return hex.EncodeToString(sum[:4])
}
