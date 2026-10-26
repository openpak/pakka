package discord

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestParseJournalEvent(t *testing.T) {
	ev, err := ParseJournalEvent([]byte(`{"MESSAGE":"boom","PRIORITY":"3","_SYSTEMD_UNIT":"openpak-x.service","SYSLOG_IDENTIFIER":"app","_HOSTNAME":"draughts-server"}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Message != "boom" || ev.Priority != 3 || ev.Unit != "openpak-x.service" || ev.Hostname != "draughts-server" {
		t.Errorf("event = %+v", ev)
	}
	ev, err = ParseJournalEvent([]byte(`{"MESSAGE":["a","b"],"PRIORITY":"6"}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Message != "a\nb" || ev.Priority != 6 {
		t.Errorf("array event = %+v", ev)
	}
	if _, err := ParseJournalEvent([]byte(`{`)); err == nil {
		t.Error("want error for bad JSON, got nil")
	}
}

func TestMatchUnit(t *testing.T) {
	for _, tc := range []struct {
		pattern, unit string
		want          bool
	}{
		{"openpak-*", "openpak-x.service", true},
		{"openpak-*", "ntfy.service", false},
		{"ntfy.service", "ntfy.service", true},
		{"*", "anything.service", true},
		{"", "anything.service", true},
	} {
		if got := matchUnit(tc.pattern, tc.unit); got != tc.want {
			t.Errorf("match(%q,%q) = %v", tc.pattern, tc.unit, got)
		}
	}
}

func watchAnnouncer() (*fakeAnnouncer, *LogWatcher) {
	ann := &fakeAnnouncer{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewLogWatcher([]string{"openpak-*", "ntfy.service"}, "mod1", "ops1", ann, time.Hour, log)
	return ann, w
}

func TestLogWatcherOps(t *testing.T) {
	ann, w := watchAnnouncer()
	w.Handle([]byte(`{"MESSAGE":"connection refused","PRIORITY":"3","_SYSTEMD_UNIT":"openpak-x.service","_HOSTNAME":"draughts-server"}`))
	if len(ann.posts) != 1 || !strings.Contains(ann.posts[0], "draughts-server/openpak-x.service") || !strings.Contains(ann.posts[0], "error") {
		t.Fatalf("posts = %v", ann.posts)
	}
	// Info priority stays silent.
	w.Handle([]byte(`{"MESSAGE":"started","PRIORITY":"6","_SYSTEMD_UNIT":"openpak-x.service"}`))
	// Non-matching unit stays silent.
	w.Handle([]byte(`{"MESSAGE":"boom","PRIORITY":"3","_SYSTEMD_UNIT":"other.service"}`))
	if len(ann.posts) != 1 {
		t.Errorf("posts = %v", ann.posts)
	}
}

func TestLogWatcherModeration(t *testing.T) {
	ann, w := watchAnnouncer()
	w.Handle([]byte(`{"MESSAGE":"player reported for spam","PRIORITY":"6","_SYSTEMD_UNIT":"openpak-x.service","_HOSTNAME":"h"}`))
	if len(ann.posts) != 1 || !strings.Contains(ann.posts[0], "moderation signal") {
		t.Fatalf("posts = %v", ann.posts)
	}
}

func TestLogWatcherCooldown(t *testing.T) {
	ann, w := watchAnnouncer()
	line := []byte(`{"MESSAGE":"boom","PRIORITY":"3","_SYSTEMD_UNIT":"openpak-x.service"}`)
	w.Handle(line)
	w.Handle(line)
	if len(ann.posts) != 1 {
		t.Errorf("cooldown failed: %v", ann.posts)
	}
}
