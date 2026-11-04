// Package store is Pakka's one place for durable state. In production it is
// Postgres (one database backup covers everything); a state directory of JSON
// files remains only as an explicit local-development fallback, and a nil
// *Store keeps everything in memory (tests, features that need no state).
package store

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const schema = `
CREATE TABLE IF NOT EXISTS pakka_docs (
	name       text PRIMARY KEY,
	doc        jsonb NOT NULL,
	updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS pakka_chat_conversations (
	id         text PRIMARY KEY,
	account    text NOT NULL,
	doc        jsonb NOT NULL,
	updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS pakka_chat_conversations_account ON pakka_chat_conversations (account);
CREATE TABLE IF NOT EXISTS pakka_publisher_audit (
	id  bigserial PRIMARY KEY,
	at  timestamptz NOT NULL DEFAULT now(),
	doc jsonb NOT NULL
);
`

const timeout = 10 * time.Second

// Store is either Postgres-backed (db set), file-backed (dir set), or both
// empty (memory only; callers keep their own in-memory copy).
type Store struct {
	db  *pgxpool.Pool
	dir string
}

// Open connects to Postgres with a small pool and creates the schema.
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pakka database url: %w", err)
	}
	cfg.MaxConns = 3
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if _, err := pool.Exec(c, schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pakka schema: %w", err)
	}
	return &Store{db: pool}, nil
}

// OpenFiles is the local-development fallback: JSON files under dir.
func OpenFiles(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Close releases the pool.
func (s *Store) Close() {
	if s != nil && s.db != nil {
		s.db.Close()
	}
}

// Durable reports whether writes survive a restart.
func (s *Store) Durable() bool { return s != nil && (s.db != nil || s.dir != "") }

// Backend names the backing store for logs.
func (s *Store) Backend() string {
	switch {
	case s == nil:
		return "memory"
	case s.db != nil:
		return "postgres"
	case s.dir != "":
		return "files"
	}
	return "memory"
}

// Dir is the file store directory ("" for Postgres or memory).
func (s *Store) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// Doc is one named JSON document.
type Doc struct {
	s    *Store
	name string
}

// Doc returns the document called name (for example "publisher-queue").
func (s *Store) Doc(name string) Doc { return Doc{s, name} }

// Load decodes the document into v. A missing document leaves v untouched.
func (d Doc) Load(v any) error {
	switch {
	case d.s == nil:
		return nil
	case d.s.db != nil:
		c, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		var raw []byte
		err := d.s.db.QueryRow(c, `SELECT doc FROM pakka_docs WHERE name=$1`, d.name).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, v)
	case d.s.dir != "":
		raw, err := os.ReadFile(d.path())
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, v)
	}
	return nil
}

// Save replaces the document with v.
func (d Doc) Save(v any) error {
	if d.s == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return d.s.putDoc(d.name, raw)
}

func (d Doc) path() string { return filepath.Join(d.s.dir, d.name+".json") }

func (s *Store) putDoc(name string, raw []byte) error {
	switch {
	case s.db != nil:
		c, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		_, err := s.db.Exec(c, `INSERT INTO pakka_docs (name, doc) VALUES ($1, $2)
			ON CONFLICT (name) DO UPDATE SET doc = EXCLUDED.doc, updated_at = now()`, name, raw)
		return err
	case s.dir != "":
		p := filepath.Join(s.dir, name+".json")
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, raw, 0o600); err != nil {
			return err
		}
		return os.Rename(tmp, p)
	}
	return nil
}

// Conversation rows (chat). Documents are opaque JSON keyed by id and owner.

// ChatLoad returns every stored conversation document.
func (s *Store) ChatLoad() ([][]byte, error) {
	switch {
	case s == nil:
		return nil, nil
	case s.db != nil:
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rows, err := s.db.Query(c, `SELECT doc FROM pakka_chat_conversations`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out [][]byte
		for rows.Next() {
			var b []byte
			if err := rows.Scan(&b); err != nil {
				return nil, err
			}
			out = append(out, b)
		}
		return out, rows.Err()
	case s.dir != "":
		dir := filepath.Join(s.dir, "chat")
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var out [][]byte
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || e.Name() == "chat-consent.json" {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
				out = append(out, b)
			}
		}
		return out, nil
	}
	return nil, nil
}

// ChatSave writes one conversation.
func (s *Store) ChatSave(id, account string, raw []byte) error {
	switch {
	case s == nil:
		return nil
	case s.db != nil:
		c, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		_, err := s.db.Exec(c, `INSERT INTO pakka_chat_conversations (id, account, doc) VALUES ($1,$2,$3)
			ON CONFLICT (id) DO UPDATE SET doc = EXCLUDED.doc, updated_at = now()`, id, account, raw)
		return err
	case s.dir != "":
		dir := filepath.Join(s.dir, "chat")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		p := filepath.Join(dir, id+".json")
		if err := os.WriteFile(p+".tmp", raw, 0o600); err != nil {
			return err
		}
		return os.Rename(p+".tmp", p)
	}
	return nil
}

// ChatDelete removes one conversation.
func (s *Store) ChatDelete(id string) error {
	switch {
	case s == nil:
		return nil
	case s.db != nil:
		c, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		_, err := s.db.Exec(c, `DELETE FROM pakka_chat_conversations WHERE id=$1`, id)
		return err
	case s.dir != "":
		err := os.Remove(filepath.Join(s.dir, "chat", id+".json"))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return nil
}

// AuditAppend records one publisher audit entry.
func (s *Store) AuditAppend(raw []byte) error {
	switch {
	case s == nil:
		return nil
	case s.db != nil:
		c, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		_, err := s.db.Exec(c, `INSERT INTO pakka_publisher_audit (doc) VALUES ($1)`, raw)
		return err
	case s.dir != "":
		f, err := os.OpenFile(filepath.Join(s.dir, "publisher-audit.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Write(append(raw, '\n')); err != nil {
			return err
		}
		return f.Sync()
	}
	return nil
}

// AuditTail returns up to n most recent audit documents, oldest first.
func (s *Store) AuditTail(n int) ([][]byte, error) {
	switch {
	case s == nil:
		return nil, nil
	case s.db != nil:
		c, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		rows, err := s.db.Query(c, `SELECT doc FROM (SELECT id, doc FROM pakka_publisher_audit ORDER BY id DESC LIMIT $1) t ORDER BY id`, n)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out [][]byte
		for rows.Next() {
			var b []byte
			if err := rows.Scan(&b); err != nil {
				return nil, err
			}
			out = append(out, b)
		}
		return out, rows.Err()
	case s.dir != "":
		f, err := os.Open(filepath.Join(s.dir, "publisher-audit.jsonl"))
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		defer f.Close()
		var out [][]byte
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			out = append(out, append([]byte(nil), sc.Bytes()...))
			if len(out) > n {
				out = out[1:]
			}
		}
		return out, nil
	}
	return nil, nil
}

// AuditFor returns every audit document for one draft, oldest first: those
// carrying its draft id and those carrying its event id (the publish
// attempt and outcome are written under the event id).
func (s *Store) AuditFor(draftID, eventID string) ([][]byte, error) {
	if draftID == "" && eventID == "" {
		return nil, nil
	}
	switch {
	case s == nil:
		return nil, nil
	case s.db != nil:
		c, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		rows, err := s.db.Query(c, `SELECT doc FROM pakka_publisher_audit
			WHERE ($1 <> '' AND doc->>'draft_id' = $1) OR ($2 <> '' AND doc->>'event_id' = $2) ORDER BY id`, draftID, eventID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out [][]byte
		for rows.Next() {
			var b []byte
			if err := rows.Scan(&b); err != nil {
				return nil, err
			}
			out = append(out, b)
		}
		return out, rows.Err()
	case s.dir != "":
		f, err := os.Open(filepath.Join(s.dir, "publisher-audit.jsonl"))
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		defer f.Close()
		var out [][]byte
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			var e struct {
				DraftID string `json:"draft_id"`
				EventID string `json:"event_id"`
			}
			if json.Unmarshal(sc.Bytes(), &e) != nil {
				continue
			}
			if (draftID != "" && e.DraftID == draftID) || (eventID != "" && e.EventID == eventID) {
				out = append(out, append([]byte(nil), sc.Bytes()...))
			}
		}
		return out, nil
	}
	return nil, nil
}

// ChatDeleteAccount removes every conversation owned by account and returns
// the ids it removed from the database or directory.
func (s *Store) ChatDeleteAccount(account string) error {
	switch {
	case s == nil:
		return nil
	case s.db != nil:
		c, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		_, err := s.db.Exec(c, `DELETE FROM pakka_chat_conversations WHERE account=$1`, account)
		return err
	}
	return nil // file backend: the chat store deletes each id it holds
}

// Counts is what ImportFiles moved.
type Counts struct {
	Docs, Conversations, Audit int
}

var importDocs = []string{"chat-consent", "eshop-broken", "publisher-mirror", "publisher-queue", "publisher-done", "releases"}

// ImportFiles copies the legacy state directory into Postgres. It never
// touches or removes the files, never overwrites a row that already exists,
// and imports the audit log only into an empty audit table, so it is safe on
// every start.
func (s *Store) ImportFiles(ctx context.Context, dir string, log *slog.Logger) (Counts, error) {
	var n Counts
	if s == nil || s.db == nil || dir == "" {
		return n, nil
	}
	if _, err := os.Stat(dir); err != nil {
		return n, nil
	}
	for _, name := range importDocs {
		// chat-consent.json lives in the state dir itself, like the others.
		raw, err := os.ReadFile(filepath.Join(dir, name+".json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return n, err
		}
		if !json.Valid(raw) {
			log.Warn("state import: skipping invalid json", "doc", name)
			continue
		}
		tag, err := s.db.Exec(ctx, `INSERT INTO pakka_docs (name, doc) VALUES ($1,$2) ON CONFLICT (name) DO NOTHING`, name, raw)
		if err != nil {
			return n, err
		}
		n.Docs += int(tag.RowsAffected())
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "chat"))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "chat", e.Name()))
		if err != nil {
			continue
		}
		var h struct {
			ID      string `json:"id"`
			Account string `json:"account"`
		}
		if json.Unmarshal(raw, &h) != nil || h.ID == "" || h.Account == "" {
			continue
		}
		tag, err := s.db.Exec(ctx, `INSERT INTO pakka_chat_conversations (id, account, doc) VALUES ($1,$2,$3) ON CONFLICT (id) DO NOTHING`, h.ID, h.Account, raw)
		if err != nil {
			return n, err
		}
		n.Conversations += int(tag.RowsAffected())
	}
	var have int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM pakka_publisher_audit`).Scan(&have); err != nil {
		return n, err
	}
	if have == 0 {
		if f, err := os.Open(filepath.Join(dir, "publisher-audit.jsonl")); err == nil {
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 64*1024), 1<<20)
			for sc.Scan() {
				if !json.Valid(sc.Bytes()) {
					continue
				}
				if _, err := s.db.Exec(ctx, `INSERT INTO pakka_publisher_audit (doc) VALUES ($1)`, append([]byte(nil), sc.Bytes()...)); err != nil {
					f.Close()
					return n, err
				}
				n.Audit++
			}
			f.Close()
		}
	}
	return n, nil
}

// MustFiles opens a file store at dir, or returns nil (memory only) for an
// empty dir. It panics on failure and is meant for tests.
func MustFiles(dir string) *Store {
	if dir == "" {
		return nil
	}
	s, err := OpenFiles(dir)
	if err != nil {
		panic(err)
	}
	return s
}
