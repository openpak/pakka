package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"pakka/internal/config"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestOpenStateRefusesStatefulFeaturesWithoutDatabase(t *testing.T) {
	t.Setenv("PAKKA_DATABASE_URL", "")
	t.Setenv("PAKKA_ALLOW_FILE_STORE", "")
	t.Setenv("PAKKA_CHAT_KEY", "k")
	_, err := openState(context.Background(), config.Config{}, quiet())
	if err == nil || !strings.Contains(err.Error(), "PAKKA_DATABASE_URL is required") || !strings.Contains(err.Error(), "player chat") {
		t.Fatalf("got %v", err)
	}
	t.Setenv("PAKKA_CHAT_KEY", "")
	t.Setenv("PAKKA_OPENVERSE_URL", "http://x")
	t.Setenv("PAKKA_OPENVERSE_KEY", "k")
	if _, err := openState(context.Background(), config.Config{}, quiet()); err == nil || !strings.Contains(err.Error(), "publisher") {
		t.Fatalf("publisher: %v", err)
	}
}

func TestOpenStateFileStoreNeedsExplicitOptIn(t *testing.T) {
	t.Setenv("PAKKA_DATABASE_URL", "")
	t.Setenv("PAKKA_CHAT_KEY", "k")
	t.Setenv("PAKKA_ALLOW_FILE_STORE", "1")
	st, err := openState(context.Background(), config.Config{StateDir: t.TempDir()}, quiet())
	if err != nil || st.Backend() != "files" {
		t.Fatalf("%v %v", st.Backend(), err)
	}
}

func TestOpenStateStatelessInstanceNeedsNoDatabase(t *testing.T) {
	t.Setenv("PAKKA_DATABASE_URL", "")
	t.Setenv("PAKKA_ALLOW_FILE_STORE", "")
	t.Setenv("PAKKA_CHAT_KEY", "")
	t.Setenv("PAKKA_OPENVERSE_URL", "")
	st, err := openState(context.Background(), config.Config{DiscordToken: "t"}, quiet())
	if err != nil || st != nil {
		t.Fatalf("%v %v", st, err)
	}
}
