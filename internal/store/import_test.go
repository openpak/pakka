package store

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// Needs a scratch Postgres: PAKKA_TEST_DATABASE_URL; skipped otherwise.
func TestImportedFilesAreRetiredAndErasureSticks(t *testing.T) {
	url := os.Getenv("PAKKA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PAKKA_TEST_DATABASE_URL unset")
	}
	ctx := context.Background()
	st, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.db.Exec(ctx, `DELETE FROM pakka_chat_conversations WHERE account='imp-acct'`)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "chat"), 0o755)
	conv := filepath.Join(dir, "chat", "impconv1.json")
	os.WriteFile(conv, []byte(`{"id":"impconv1","account":"imp-acct","title":"t"}`), 0o600)
	n, err := st.ImportFiles(ctx, dir, slog.Default())
	if err != nil || n.Conversations != 1 {
		t.Fatalf("import: %+v %v", n, err)
	}
	if _, err := os.Stat(conv + ".imported-20261104"); err != nil {
		t.Fatalf("file not retired: %v", err)
	}
	if err := st.ChatDeleteAccount("imp-acct"); err != nil {
		t.Fatal(err)
	}
	// A restart imports again: the erased conversation must not come back.
	if n, err := st.ImportFiles(ctx, dir, slog.Default()); err != nil || n.Conversations != 0 {
		t.Fatalf("second import: %+v %v", n, err)
	}
}
