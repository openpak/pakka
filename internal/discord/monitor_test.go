package discord

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseTargets(t *testing.T) {
	got := ParseTargets("a=https://a.invalid/,b=tcp://h:1, bad, =x, c=ftp://x, d=  ")
	if len(got) != 2 || got[0].Name != "a" || got[1].URL != "tcp://h:1" {
		t.Errorf("targets = %+v", got)
	}
	if len(ParseTargets("")) != 0 {
		t.Error("empty string parsed to targets")
	}
}

type funcProber func(ctx context.Context, t Target) (bool, string)

func (f funcProber) Probe(ctx context.Context, t Target) (bool, string) { return f(ctx, t) }

type fakeAnnouncer struct {
	posts []string
}

func (f *fakeAnnouncer) Announce(_ string, content string) error {
	f.posts = append(f.posts, content)
	return nil
}

func testMonitor(up map[string]bool, ann *fakeAnnouncer) *Monitor {
	prober := funcProber(func(_ context.Context, t Target) (bool, string) {
		return up[t.Name], "detail"
	})
	targets := ParseTargets("a=https://a.invalid/,b=https://b.invalid/")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewMonitor(targets, time.Minute, prober, ann, "status1", log)
}

func TestMonitorTransitions(t *testing.T) {
	up := map[string]bool{"a": true, "b": true}
	ann := &fakeAnnouncer{}
	m := testMonitor(up, ann)
	ctx := context.Background()

	m.RunOnce(ctx)
	if len(ann.posts) != 0 {
		t.Fatalf("baseline announced: %v", ann.posts)
	}
	up["a"] = false
	m.RunOnce(ctx)
	if len(ann.posts) != 1 || !strings.Contains(ann.posts[0], "DOWN") || !strings.Contains(ann.posts[0], "**a**") {
		t.Fatalf("posts = %v", ann.posts)
	}
	m.RunOnce(ctx)
	if len(ann.posts) != 1 {
		t.Fatalf("steady state re-announced: %v", ann.posts)
	}
	up["a"] = true
	m.RunOnce(ctx)
	if len(ann.posts) != 2 || !strings.Contains(ann.posts[1], "UP") {
		t.Fatalf("posts = %v", ann.posts)
	}
}

func TestAlertTag(t *testing.T) {
	for name, want := range map[string][2]string{
		"Switch/nx-baas": {"[Switch - nx-baas]", "nx-baas"},
		"Core/accounts":  {"[Core - accounts]", "accounts"},
		"website":        {"[Status - website]", "website"},
		"Bad/":           {"[Status - Bad/]", "Bad/"},
	} {
		if got := AlertTag(name); got != want[0] {
			t.Errorf("AlertTag(%q) = %q, want %q", name, got, want[0])
		}
		if got := shortName(name); got != want[1] {
			t.Errorf("shortName(%q) = %q, want %q", name, got, want[1])
		}
	}
}

func TestAlertsMirror(t *testing.T) {
	up := map[string]bool{"Switch/nx-baas": true}
	targets := ParseTargets("Switch/nx-baas=https://a.invalid/")
	prober := funcProber(func(_ context.Context, tg Target) (bool, string) {
		return up[tg.Name], "boom"
	})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	status, alerts := &fakeAnnouncer{}, &fakeAnnouncer{}
	m := NewMonitor(targets, time.Minute, prober, status, "status1", log)
	m.SetAlertsMirror(alerts, "alerts1")
	ctx := context.Background()
	m.RunOnce(ctx)
	if len(alerts.posts) != 0 {
		t.Fatalf("baseline mirrored: %v", alerts.posts)
	}
	up["Switch/nx-baas"] = false
	m.RunOnce(ctx)
	if len(alerts.posts) != 1 || alerts.posts[0] != "[Switch - nx-baas] nx-baas is DOWN — boom" {
		t.Fatalf("mirror posts = %v", alerts.posts)
	}
	if len(status.posts) != 1 {
		t.Fatalf("status posts = %v", status.posts)
	}
}

func TestNetProber(t *testing.T) {
	p := NewNetProber(time.Second)
	ctx := context.Background()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	if up, _ := p.Probe(ctx, Target{Name: "ok", URL: ok.URL}); !up {
		t.Error("200 reported down")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	if up, _ := p.Probe(ctx, Target{Name: "bad", URL: bad.URL}); up {
		t.Error("500 reported up")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if up, _ := p.Probe(ctx, Target{Name: "tcp", URL: "tcp://" + addr}); !up {
		t.Error("open port reported down")
	}
	_ = ln.Close()
	if up, _ := p.Probe(ctx, Target{Name: "tcp", URL: "tcp://" + addr}); up {
		t.Error("closed port reported up")
	}
}
