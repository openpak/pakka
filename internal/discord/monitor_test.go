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
	m.RunOnce(ctx)
	if len(ann.posts) != 0 {
		t.Fatalf("damped failures announced: %v", ann.posts)
	}
	m.RunOnce(ctx)
	if len(ann.posts) != 1 || !strings.Contains(ann.posts[0], "DOWN") ||
		!strings.Contains(ann.posts[0], "**a**") || !strings.Contains(ann.posts[0], "3 bad polls") {
		t.Fatalf("posts = %v", ann.posts)
	}
	m.RunOnce(ctx)
	if len(ann.posts) != 1 {
		t.Fatalf("steady state re-announced: %v", ann.posts)
	}
	up["a"] = true
	m.RunOnce(ctx)
	if len(ann.posts) != 2 || !strings.Contains(ann.posts[1], "UP") || !strings.Contains(ann.posts[1], "was down") {
		t.Fatalf("posts = %v", ann.posts)
	}
}

func TestDownAfterOne(t *testing.T) {
	up := map[string]bool{"a": true, "b": true}
	ann := &fakeAnnouncer{}
	m := testMonitor(up, ann)
	m.SetDownAfter(1)
	ctx := context.Background()
	m.RunOnce(ctx)
	up["a"] = false
	m.RunOnce(ctx)
	if len(ann.posts) != 1 || !strings.Contains(ann.posts[0], "1 bad poll") {
		t.Fatalf("posts = %v", ann.posts)
	}
}

func TestRecoverySilentWhenNeverPosted(t *testing.T) {
	up := map[string]bool{"a": true, "b": true}
	ann := &fakeAnnouncer{}
	m := testMonitor(up, ann)
	ctx := context.Background()
	m.RunOnce(ctx)
	up["a"] = false
	m.RunOnce(ctx) // damped, never posted
	up["a"] = true
	m.RunOnce(ctx)
	if len(ann.posts) != 0 {
		t.Fatalf("unposted outage announced: %v", ann.posts)
	}
}

func TestBaselineDownEventuallyPages(t *testing.T) {
	up := map[string]bool{"a": false, "b": true}
	ann := &fakeAnnouncer{}
	m := testMonitor(up, ann)
	ctx := context.Background()
	m.RunOnce(ctx) // baseline: silent even though down
	m.RunOnce(ctx)
	if len(ann.posts) != 0 {
		t.Fatalf("baseline announced: %v", ann.posts)
	}
	m.RunOnce(ctx) // third consecutive failure: pages
	if len(ann.posts) != 1 || !strings.Contains(ann.posts[0], "DOWN") {
		t.Fatalf("posts = %v", ann.posts)
	}
}

func TestAckSuppressesAndLists(t *testing.T) {
	up := map[string]bool{"a": true, "b": true}
	ann := &fakeAnnouncer{}
	m := testMonitor(up, ann)
	m.SetDownAfter(1)
	ctx := context.Background()
	m.RunOnce(ctx)
	if matched, ok := m.Ack("a", time.Hour); !ok || matched != "a" {
		t.Fatalf("ack = %q,%v", matched, ok)
	}
	if _, ok := m.Ack("nope", time.Hour); ok {
		t.Fatal("unknown target acked")
	}
	if len(m.Acks()) != 1 {
		t.Fatalf("acks = %v", m.Acks())
	}
	up["a"] = false
	m.RunOnce(ctx)
	m.RunOnce(ctx)
	if len(ann.posts) != 0 {
		t.Fatalf("acked outage announced: %v", ann.posts)
	}
	if !m.Unack("A") || m.Unack("a") {
		t.Fatal("unack mismatch")
	}
	m.RunOnce(ctx)
	if len(ann.posts) != 1 || !strings.Contains(ann.posts[0], "DOWN") {
		t.Fatalf("posts after unack = %v", ann.posts)
	}
}

func TestAckExpires(t *testing.T) {
	up := map[string]bool{"a": true, "b": true}
	ann := &fakeAnnouncer{}
	m := testMonitor(up, ann)
	m.SetDownAfter(1)
	ctx := context.Background()
	m.RunOnce(ctx)
	if _, ok := m.Ack("a", time.Nanosecond); !ok {
		t.Fatal("ack failed")
	}
	up["a"] = false
	m.RunOnce(ctx)
	if len(ann.posts) != 1 {
		t.Fatalf("expired ack suppressed: %v", ann.posts)
	}
	if len(m.Acks()) != 0 {
		t.Fatalf("expired ack listed: %v", m.Acks())
	}
}

func TestAckMatch(t *testing.T) {
	ann := &fakeAnnouncer{}
	m := testMonitor(map[string]bool{"a": true, "b": true}, ann)
	if matched, ok := m.Ack("A", time.Hour); !ok || matched != "a" {
		t.Errorf("exact match = %q,%v", matched, ok)
	}
	m2targets := ParseTargets("Core/accounts=https://x.invalid/,Core/website=https://y.invalid/")
	prober := funcProber(func(_ context.Context, _ Target) (bool, string) { return true, "ok" })
	m2 := NewMonitor(m2targets, time.Minute, prober, ann, "status1", m.log)
	if matched, ok := m2.Ack("website", time.Hour); !ok || matched != "Core/website" {
		t.Errorf("substring match = %q,%v", matched, ok)
	}
	if _, ok := m2.Ack("core", time.Hour); ok {
		t.Error("ambiguous substring matched")
	}
}

func TestDependsSuppress(t *testing.T) {
	up := map[string]bool{"db": true, "app": true}
	ann := &fakeAnnouncer{}
	prober := funcProber(func(_ context.Context, tg Target) (bool, string) { return up[tg.Name], "boom" })
	targets := ParseTargets("db=https://db.invalid/,app=https://app.invalid/")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := NewMonitor(targets, time.Minute, prober, ann, "status1", log)
	m.SetDownAfter(1)
	m.SetDepends(ParseDepends("app=db"))
	ctx := context.Background()
	m.RunOnce(ctx)
	up["db"], up["app"] = false, false
	m.RunOnce(ctx)
	if len(ann.posts) != 1 || !strings.Contains(ann.posts[0], "**db**") {
		t.Fatalf("cascade posts = %v", ann.posts)
	}
	up["db"] = true // parent recovers; child still failing pages now
	m.RunOnce(ctx)
	if len(ann.posts) != 3 {
		t.Fatalf("posts = %v", ann.posts)
	}
	if !strings.Contains(ann.posts[1], "back UP") || !strings.Contains(ann.posts[2], "**app**") ||
		!strings.Contains(ann.posts[2], "DOWN") {
		t.Fatalf("posts = %v", ann.posts)
	}
}

func TestParseDepends(t *testing.T) {
	d := ParseDepends("a=b, a=c, bad, =x, y=")
	if len(d) != 1 || len(d["a"]) != 2 || d["a"][0] != "b" || d["a"][1] != "c" {
		t.Errorf("depends = %v", d)
	}
	if ParseDepends("") != nil {
		t.Error("empty string parsed to depends")
	}
}

func TestDowntimeFormat(t *testing.T) {
	for d, want := range map[time.Duration]string{
		45 * time.Second:            "45s",
		14 * time.Minute:            "14m",
		2 * time.Hour:               "2h",
		2*time.Hour + 5*time.Minute: "2h5m",
	} {
		if got := fmtDowntime(d); got != want {
			t.Errorf("fmtDowntime(%s) = %q, want %q", d, got, want)
		}
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
	m.SetDownAfter(1)
	ctx := context.Background()
	m.RunOnce(ctx)
	if len(alerts.posts) != 0 {
		t.Fatalf("baseline mirrored: %v", alerts.posts)
	}
	up["Switch/nx-baas"] = false
	m.RunOnce(ctx)
	if len(alerts.posts) != 1 || alerts.posts[0] != "[Switch - nx-baas] nx-baas is DOWN — boom (failing 0s, 1 bad poll)" {
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
