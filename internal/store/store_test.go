package store

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// Needs a scratch Postgres: PAKKA_TEST_DATABASE_URL=postgres://...; skipped otherwise.
func pg(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("PAKKA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PAKKA_TEST_DATABASE_URL unset")
	}
	s, err := Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.db.Exec(context.Background(), `TRUNCATE pakka_docs, pakka_chat_conversations, pakka_publisher_audit`)
		s.Close()
	})
	_, _ = s.db.Exec(context.Background(), `TRUNCATE pakka_docs, pakka_chat_conversations, pakka_publisher_audit`)
	return s
}

func TestDocsSurviveRestart(t *testing.T) {
	a := pg(t)
	if err := a.Doc("publisher-queue").Save(map[string]int{"x": 1}); err != nil {
		t.Fatal(err)
	}
	if err := a.ChatSave("aaaaaaaaaaaaaaaaaaaaaaaa", "acct", []byte(`{"id":"aaaaaaaaaaaaaaaaaaaaaaaa","account":"acct"}`)); err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{`{"n":1}`, `{"n":2}`, `{"n":3}`} {
		if err := a.AuditAppend([]byte(e)); err != nil {
			t.Fatal(err)
		}
	}
	b, err := Open(context.Background(), os.Getenv("PAKKA_TEST_DATABASE_URL")) // "new instance"
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var got map[string]int
	if err := b.Doc("publisher-queue").Load(&got); err != nil || got["x"] != 1 {
		t.Fatalf("doc after restart: %v %v", got, err)
	}
	var none map[string]int
	if err := b.Doc("absent").Load(&none); err != nil || none != nil {
		t.Fatalf("absent doc: %v %v", none, err)
	}
	if c, _ := b.ChatLoad(); len(c) != 1 {
		t.Fatalf("conversations after restart: %d", len(c))
	}
	tail, _ := b.AuditTail(2)
	if len(tail) != 2 || string(tail[1]) != `{"n": 3}` && string(tail[1]) != `{"n":3}` {
		t.Fatalf("audit tail %q", tail)
	}
	if err := b.ChatDelete("aaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if c, _ := a.ChatLoad(); len(c) != 0 {
		t.Fatal("delete not visible")
	}
}

func TestImportFilesKeepsFilesAndIsIdempotent(t *testing.T) {
	s := pg(t)
	dir := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("chat-consent.json", `{"a":{"version":"v","at":"2026-11-01T00:00:00Z"}}`)
	write("eshop-broken.json", `{"p":{"since":"2026-11-01T00:00:00Z"}}`)
	write("publisher-queue.json", `[]`)
	write("chat/aaaaaaaaaaaaaaaaaaaaaaaa.json", `{"id":"aaaaaaaaaaaaaaaaaaaaaaaa","account":"acct"}`)
	write("publisher-audit.jsonl", "{\"n\":1}\n{\"n\":2}\n")
	log := slog.Default()
	n, err := s.ImportFiles(context.Background(), dir, log)
	if err != nil || n != (Counts{Docs: 3, Conversations: 1, Audit: 2}) {
		t.Fatalf("import = %+v %v", n, err)
	}
	n, err = s.ImportFiles(context.Background(), dir, log)
	if err != nil || n != (Counts{}) {
		t.Fatalf("second import = %+v %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "publisher-audit.jsonl")); err != nil {
		t.Fatal("source file removed")
	}
	// A row changed after the first import is never overwritten by a re-import.
	if err := s.Doc("chat-consent").Save(map[string]string{"changed": "yes"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportFiles(context.Background(), dir, log); err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := s.Doc("chat-consent").Load(&m); err != nil || m["changed"] != "yes" {
		t.Fatalf("overwritten: %v %v", m, err)
	}
}

func TestFileStoreRoundTrip(t *testing.T) {
	a, err := OpenFiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Doc("x").Save([]int{1, 2}); err != nil {
		t.Fatal(err)
	}
	var got []int
	if err := a.Doc("x").Load(&got); err != nil || len(got) != 2 {
		t.Fatal(got, err)
	}
	if !a.Durable() || (*Store)(nil).Durable() {
		t.Fatal("durable flags")
	}
}
