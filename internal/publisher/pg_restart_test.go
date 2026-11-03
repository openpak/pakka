package publisher

import (
	"context"
	"os"
	"testing"

	"pakka/internal/store"
)

// Needs a scratch Postgres: PAKKA_TEST_DATABASE_URL=postgres://...; skipped otherwise.
func TestQueueMirrorAuditSurviveRestartOnPostgres(t *testing.T) {
	url := os.Getenv("PAKKA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PAKKA_TEST_DATABASE_URL unset")
	}
	open := func() *store.Store {
		st, err := store.Open(context.Background(), url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(st.Close)
		return st
	}
	a := open()
	wipe := func() {
		st, err := store.Open(context.Background(), url)
		if err == nil {
			st.Doc("publisher-queue").Save([]string{})
			st.Doc("publisher-mirror").Save(map[string]any{})
			st.Close()
		}
	}
	wipe()
	t.Cleanup(wipe)
	q, err := NewQueue(a)
	if err != nil {
		t.Fatal(err)
	}
	d, err := q.Add(free(), "preview", "model")
	if err != nil {
		t.Fatal(err)
	}
	m, _ := newMirrorState(a)
	m.add("openpak_news:7")
	au, _ := NewAudit(a, quiet)
	if err := au.Record(AuditEntry{Actor: "model", Event: "queued", DraftID: d.ID}); err != nil {
		t.Fatal(err)
	}

	b := open() // a new instance on the same database
	q2, err := NewQueue(b)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := q2.Get(d.ID); !ok || got.Status != StatusPending {
		t.Fatalf("draft after restart: %+v %v", got, ok)
	}
	m2, _ := newMirrorState(b)
	if !m2.has("openpak_news:7") {
		t.Fatal("mirror state lost")
	}
	au2, _ := NewAudit(b, quiet)
	found := false
	for _, e := range au2.Recent(0) {
		found = found || e.DraftID == d.ID
	}
	if !found {
		t.Fatal("audit lost")
	}
}
