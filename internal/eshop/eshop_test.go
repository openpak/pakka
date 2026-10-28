package eshop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

type fakeCatalog struct {
	titles []Title
	err    error
}

func (f *fakeCatalog) Titles(context.Context) ([]Title, error) { return f.titles, f.err }

type fakeReleases struct {
	latest map[string]Release
	has    map[string]bool
	err    error
}

func (f *fakeReleases) Latest(_ context.Context, slug string) (Release, bool, error) {
	if f.err != nil {
		return Release{}, false, f.err
	}
	return f.latest[slug], f.has[slug], nil
}

type fakeAnnouncer struct {
	posts []string
}

func (f *fakeAnnouncer) Announce(_ string, content string) error {
	f.posts = append(f.posts, content)
	return nil
}

type fakeTrigger struct {
	requests []string
	started  bool
	note     string
}

func (f *fakeTrigger) Request(reason string, post func(string)) (bool, string) {
	f.requests = append(f.requests, reason)
	if f.started && post != nil {
		post(":white_check_mark: eShop sync finished (fake)")
	}
	return f.started, f.note
}

func (f *fakeTrigger) Status() TriggerStatus { return TriggerStatus{} }

func testWatcher(cat *fakeCatalog, rel *fakeReleases, trig Trigger) (*Watcher, *fakeAnnouncer, *fakeAnnouncer) {
	staff, public := &fakeAnnouncer{}, &fakeAnnouncer{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := New(cat, rel, trig, staff, public, "staff1", "public1",
		map[string]string{"ftpd": "mtheall/ftpd"}, log)
	return w, staff, public
}

var ftpdBase = Title{
	TitleID: "01fef84c9c71f000", Name: "ftpd", Version: 65536,
	VersionLabel: "3.2.1", RevisedAt: 1790000000,
	Source:    "https://github.com/mtheall/ftpd/releases/tag/v3.2.1",
	Published: true,
}

func mkrel(tag, at string) Release {
	return Release{Tag: tag, PublishedAt: at, HTMLURL: "https://github.com/mtheall/ftpd/releases/tag/" + tag}
}

func TestBaselineIsSilent(t *testing.T) {
	cat := &fakeCatalog{titles: []Title{ftpdBase}}
	rel := &fakeReleases{latest: map[string]Release{"mtheall/ftpd": mkrel("v3.2.2", "2026-10-20T00:00:00Z")}, has: map[string]bool{"mtheall/ftpd": true}}
	w, staff, public := testWatcher(cat, rel, &fakeTrigger{})
	w.RunOnce(context.Background())
	if len(staff.posts) != 0 || len(public.posts) != 0 {
		t.Fatalf("baseline announced: staff=%v public=%v", staff.posts, public.posts)
	}
	snap := w.Snapshot()
	if snap.Watched != 1 || snap.Behind != 1 {
		t.Fatalf("snapshot = %+v, want watched=1 behind=1", snap)
	}
}

func TestNewReleaseAlertsStaffAndTriggers(t *testing.T) {
	cat := &fakeCatalog{titles: []Title{ftpdBase}}
	rel := &fakeReleases{latest: map[string]Release{"mtheall/ftpd": mkrel("v3.2.1", "2026-10-01T00:00:00Z")}, has: map[string]bool{"mtheall/ftpd": true}}
	trig := &fakeTrigger{started: true}
	w, staff, _ := testWatcher(cat, rel, trig)
	ctx := context.Background()
	w.RunOnce(ctx) // baseline at v3.2.1
	rel.latest["mtheall/ftpd"] = mkrel("v3.2.2", "2026-10-20T00:00:00Z")
	w.RunOnce(ctx)
	if len(trig.requests) != 1 {
		t.Fatalf("trigger requests = %d, want 1", len(trig.requests))
	}
	if len(staff.posts) != 2 { // drift alert + fake finish notice
		t.Fatalf("staff posts = %v", staff.posts)
	}
	found := false
	for _, p := range staff.posts {
		found = found || (strings.Contains(p, "v3.2.2") && strings.Contains(p, "ftpd"))
	}
	if !found {
		t.Errorf("no drift post in %v", staff.posts)
	}
	w.RunOnce(ctx) // steady state: silent
	if len(staff.posts) != 2 || len(trig.requests) != 1 {
		t.Fatalf("steady state refired: posts=%v requests=%v", staff.posts, trig.requests)
	}
}

func TestRollingTagMoveAlerts(t *testing.T) {
	cat := &fakeCatalog{titles: []Title{ftpdBase}}
	rel := &fakeReleases{latest: map[string]Release{"mtheall/ftpd": mkrel("nightly", "2026-10-01T00:00:00Z")}, has: map[string]bool{"mtheall/ftpd": true}}
	trig := &fakeTrigger{}
	w, staff, _ := testWatcher(cat, rel, trig)
	ctx := context.Background()
	w.RunOnce(ctx)
	rel.latest["mtheall/ftpd"] = mkrel("nightly", "2026-10-02T00:00:00Z")
	w.RunOnce(ctx)
	if len(trig.requests) != 1 || len(staff.posts) != 1 {
		t.Fatalf("rolling tag move missed: posts=%v requests=%v", staff.posts, trig.requests)
	}
}

func TestLandedUpdateAnnouncesPublic(t *testing.T) {
	cat := &fakeCatalog{titles: []Title{ftpdBase}}
	rel := &fakeReleases{latest: map[string]Release{"mtheall/ftpd": mkrel("v3.2.1", "2026-10-01T00:00:00Z")}, has: map[string]bool{"mtheall/ftpd": true}}
	w, staff, public := testWatcher(cat, rel, &fakeTrigger{})
	ctx := context.Background()
	w.RunOnce(ctx)
	landed := ftpdBase
	landed.Version = 131072
	landed.VersionLabel = "3.2.2"
	landed.Source = "https://github.com/mtheall/ftpd/releases/tag/v3.2.2"
	landed.Changelog = "Fixes the connection bug."
	cat.titles = []Title{landed}
	rel.latest["mtheall/ftpd"] = mkrel("v3.2.2", "2026-10-20T00:00:00Z")
	w.RunOnce(ctx)
	if len(public.posts) != 1 {
		t.Fatalf("public posts = %v", public.posts)
	}
	if !strings.Contains(public.posts[0], "ftpd") || !strings.Contains(public.posts[0], "3.2.2") {
		t.Errorf("announce post = %q", public.posts[0])
	}
	found := false
	for _, p := range staff.posts {
		found = found || strings.Contains(p, "landed")
	}
	if !found {
		t.Errorf("no staff landed confirmation: %v", staff.posts)
	}
}

var mtgnxBase = Title{
	TitleID: "01fe111111110000", Name: "MTGnx", Version: 65536,
	VersionLabel: "1.0.0", RevisedAt: 1790000000,
	Source:    "https://github.com/lostmaguss/mtgnx/releases/tag/1.0.0",
	Published: true,
}

func TestDigestBatchesPublicPost(t *testing.T) {
	cat := &fakeCatalog{titles: []Title{ftpdBase, mtgnxBase}}
	frel := &fakeReleases{
		latest: map[string]Release{
			"mtheall/ftpd":     mkrel("v3.2.1", "2026-10-01T00:00:00Z"),
			"lostmaguss/mtgnx": mkrel("1.0.0", "2026-10-01T00:00:00Z"),
		},
		has: map[string]bool{"mtheall/ftpd": true, "lostmaguss/mtgnx": true},
	}
	w, staff, public := testWatcher(cat, frel, &fakeTrigger{})
	ctx := context.Background()
	w.RunOnce(ctx)
	landed1, landed2 := ftpdBase, mtgnxBase
	landed1.Version, landed1.VersionLabel = 131072, "3.2.2"
	landed2.Version, landed2.VersionLabel = 131072, "1.0.1"
	cat.titles = []Title{landed1, landed2}
	w.RunOnce(ctx)
	if len(public.posts) != 1 {
		t.Fatalf("public posts = %d, want 1 digest", len(public.posts))
	}
	if !strings.Contains(public.posts[0], "2 homebrew updates") ||
		!strings.Contains(public.posts[0], "ftpd") || !strings.Contains(public.posts[0], "MTGnx") {
		t.Errorf("digest post = %q", public.posts[0])
	}
	if len(staff.posts) != 2 { // per-title staff confirmations stay granular
		t.Errorf("staff posts = %v", staff.posts)
	}
}

func TestDigestCapsLength(t *testing.T) {
	var titles []Title
	for i := 0; i < 60; i++ {
		titles = append(titles, Title{
			TitleID: "01fe000000000000", Name: "Homebrew Number " + strings.Repeat("X", 20),
			Version: 65536, VersionLabel: "1.0", Published: true,
		})
	}
	// Unique ids keep states separate.
	for i := range titles {
		titles[i].TitleID = fmt.Sprintf("01fe%09d000", i)
	}
	cat := &fakeCatalog{titles: titles}
	w, _, public := testWatcher(cat, &fakeReleases{}, &fakeTrigger{})
	ctx := context.Background()
	w.RunOnce(ctx)
	for i := range cat.titles {
		cat.titles[i].Version = 131072
	}
	w.RunOnce(ctx)
	if len(public.posts) != 1 {
		t.Fatalf("public posts = %d, want 1", len(public.posts))
	}
	if len(public.posts[0]) > 2000 || !strings.Contains(public.posts[0], "more.") {
		t.Errorf("digest not capped: %d chars", len(public.posts[0]))
	}
}

func TestStuckDriftAlertsFailureOnce(t *testing.T) {
	cat := &fakeCatalog{titles: []Title{ftpdBase}}
	frel := &fakeReleases{latest: map[string]Release{"mtheall/ftpd": mkrel("v3.2.1", "2026-10-01T00:00:00Z")}, has: map[string]bool{"mtheall/ftpd": true}}
	w, staff, _ := testWatcher(cat, frel, &fakeTrigger{})
	w.stuckAfter = time.Hour
	ctx := context.Background()
	w.RunOnce(ctx) // baseline
	frel.latest["mtheall/ftpd"] = mkrel("v3.2.2", "2026-10-20T00:00:00Z")
	w.RunOnce(ctx) // drift announced
	if len(staff.posts) != 1 {
		t.Fatalf("drift posts = %v", staff.posts)
	}
	w.RunOnce(ctx) // still behind, within grace: silent
	if len(staff.posts) != 1 {
		t.Fatalf("grace posts = %v", staff.posts)
	}
	w.mu.Lock()
	w.states[strings.ToLower(ftpdBase.TitleID)].driftSince = time.Now().Add(-2 * time.Hour)
	w.mu.Unlock()
	w.RunOnce(ctx) // past grace: failure alert
	if len(staff.posts) != 2 || !strings.Contains(staff.posts[1], "failed to update to **v3.2.2**") {
		t.Fatalf("failure posts = %v", staff.posts)
	}
	w.RunOnce(ctx) // reminded: silent
	if len(staff.posts) != 2 {
		t.Fatalf("refire posts = %v", staff.posts)
	}
}

func TestStuckResetOnLand(t *testing.T) {
	cat := &fakeCatalog{titles: []Title{ftpdBase}}
	frel := &fakeReleases{latest: map[string]Release{"mtheall/ftpd": mkrel("v3.2.1", "2026-10-01T00:00:00Z")}, has: map[string]bool{"mtheall/ftpd": true}}
	w, staff, _ := testWatcher(cat, frel, &fakeTrigger{})
	w.stuckAfter = time.Hour
	ctx := context.Background()
	w.RunOnce(ctx)
	frel.latest["mtheall/ftpd"] = mkrel("v3.2.2", "2026-10-20T00:00:00Z")
	w.RunOnce(ctx)
	landed := ftpdBase
	landed.Version, landed.VersionLabel = 131072, "3.2.2"
	landed.Source = "https://github.com/mtheall/ftpd/releases/tag/v3.2.2"
	cat.titles = []Title{landed}
	w.mu.Lock()
	w.states[strings.ToLower(ftpdBase.TitleID)].driftSince = time.Now().Add(-2 * time.Hour)
	w.mu.Unlock()
	w.RunOnce(ctx) // landed: landed-confirm only, no failure
	for _, p := range staff.posts {
		if strings.Contains(p, "failed to update") {
			t.Fatalf("failure after landing: %v", staff.posts)
		}
	}
}

func TestUnpublishedSkipped(t *testing.T) {
	hidden := ftpdBase
	hidden.Published = false
	cat := &fakeCatalog{titles: []Title{hidden}}
	rel := &fakeReleases{latest: map[string]Release{"mtheall/ftpd": mkrel("v9.9.9", "2026-10-20T00:00:00Z")}, has: map[string]bool{"mtheall/ftpd": true}}
	w, staff, public := testWatcher(cat, rel, &fakeTrigger{started: true})
	w.RunOnce(context.Background())
	w.RunOnce(context.Background())
	if len(staff.posts) != 0 || len(public.posts) != 0 {
		t.Fatalf("unpublished announced: staff=%v public=%v", staff.posts, public.posts)
	}
	if snap := w.Snapshot(); snap.Watched != 0 {
		t.Fatalf("snapshot = %+v, want watched=0", snap)
	}
}

func TestCatalogOutageAlertsOnce(t *testing.T) {
	cat := &fakeCatalog{err: errors.New("connection refused")}
	w, staff, _ := testWatcher(cat, &fakeReleases{}, &fakeTrigger{})
	ctx := context.Background()
	w.RunOnce(ctx)
	w.RunOnce(ctx)
	if len(staff.posts) != 1 || !strings.Contains(staff.posts[0], "unreachable") {
		t.Fatalf("outage posts = %v", staff.posts)
	}
	cat.err = nil
	cat.titles = []Title{ftpdBase}
	w.RunOnce(ctx)
	if len(staff.posts) != 2 || !strings.Contains(staff.posts[1], "reachable again") {
		t.Fatalf("recovery posts = %v", staff.posts)
	}
}

func TestTitleIDMatchesHbstore(t *testing.T) {
	// Vectors from hbstore-tools hb2nsp.py title_id().
	for name, want := range map[string]string{
		"ftpd":                 "01fef84c9c71f000",
		"appstore":             "01fed0479e03e000",
		"gh:xortroll/goldleaf": "01fe2fcfd3782000",
	} {
		if got := TitleID(name); got != want {
			t.Errorf("TitleID(%q) = %s, want %s", name, got, want)
		}
	}
}

func TestParseReleaseSource(t *testing.T) {
	slug, tag, ok := ParseReleaseSource("https://github.com/mtheall/ftpd/releases/tag/v3.2.1")
	if !ok || slug != "mtheall/ftpd" || tag != "v3.2.1" {
		t.Errorf("release URL parsed as %q %q %v", slug, tag, ok)
	}
	for _, src := range []string{
		"https://switch.cdn.fortheusers.org/zips/ftpd.zip",
		"https://github.com/mtheall/ftpd",
		"",
		"synthetic fixture",
	} {
		if _, _, ok := ParseReleaseSource(src); ok {
			t.Errorf("non-release source %q parsed as release", src)
		}
	}
}

func TestParseSources(t *testing.T) {
	m, err := ParseSources(`{"ftpd": "mtheall/ftpd", "bad": "noslash"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m["ftpd"] != "mtheall/ftpd" {
		t.Fatalf("sources = %v", m)
	}
	if _, err := ParseSources(`{oops`); err == nil {
		t.Error("invalid JSON accepted")
	}
	dir := t.TempDir()
	path := dir + "/sources.json"
	if err := os.WriteFile(path, []byte(`{"appstore": "fortheusers/hb-appstore"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err = ParseSources("@" + path)
	if err != nil || m["appstore"] != "fortheusers/hb-appstore" {
		t.Fatalf("file sources = %v, err=%v", m, err)
	}
}

func TestSnapshotBehindList(t *testing.T) {
	cat := &fakeCatalog{titles: []Title{ftpdBase}}
	rel := &fakeReleases{latest: map[string]Release{"mtheall/ftpd": mkrel("v3.2.2", "2026-10-20T00:00:00Z")}, has: map[string]bool{"mtheall/ftpd": true}}
	w, _, _ := testWatcher(cat, rel, &fakeTrigger{})
	w.RunOnce(context.Background())
	snap := w.Snapshot()
	if len(snap.BehindTitles) != 1 || snap.BehindTitles[0].Name != "ftpd" {
		t.Fatalf("snapshot = %+v", snap)
	}
	if time.Since(snap.LastPoll) > time.Minute {
		t.Errorf("last poll = %v", snap.LastPoll)
	}
}

func TestLandingClearsStaleSourceDrift(t *testing.T) {
	// MapRando-NX replay: drift announced, then the release lands while
	// distribution.source still points at the old tag (github_sync never
	// rewrites it). Past grace, no failure may fire: the landing resolved
	// the drift.
	cat := &fakeCatalog{titles: []Title{ftpdBase}}
	frel := &fakeReleases{latest: map[string]Release{"mtheall/ftpd": mkrel("v3.2.1", "2026-10-01T00:00:00Z")}, has: map[string]bool{"mtheall/ftpd": true}}
	w, staff, _ := testWatcher(cat, frel, &fakeTrigger{})
	w.stuckAfter = time.Hour
	ctx := context.Background()
	w.RunOnce(ctx)
	frel.latest["mtheall/ftpd"] = mkrel("v3.2.2", "2026-10-20T00:00:00Z")
	w.RunOnce(ctx) // drift announced
	landed := ftpdBase
	landed.Version, landed.VersionLabel = 131072, "v3.2.2"
	// Source deliberately NOT updated: the sync left the old tag URL.
	cat.titles = []Title{landed}
	w.RunOnce(ctx) // landed
	w.mu.Lock()
	w.states[strings.ToLower(ftpdBase.TitleID)].driftSince = time.Now().Add(-2 * time.Hour)
	w.mu.Unlock()
	w.RunOnce(ctx) // past grace
	for _, p := range staff.posts {
		if strings.Contains(p, "failed to update") {
			t.Fatalf("failure after landing with stale source: %v", staff.posts)
		}
	}
}

func TestSamePollLandingSkipsDriftAlert(t *testing.T) {
	// Release and landing observed in one poll: only the landed note
	// posts, no "new upstream release" drift alert, even when the served
	// label differs textually from the tag (labels are normalized on some
	// sync paths). A rebuild landing without the awaited release stays
	// visible via the behind list instead of alerting here.
	for _, label := range []string{"v3.2.2", "3.2.2"} {
		cat := &fakeCatalog{titles: []Title{ftpdBase}}
		frel := &fakeReleases{latest: map[string]Release{"mtheall/ftpd": mkrel("v3.2.1", "2026-10-01T00:00:00Z")}, has: map[string]bool{"mtheall/ftpd": true}}
		w, staff, public := testWatcher(cat, frel, &fakeTrigger{})
		ctx := context.Background()
		w.RunOnce(ctx)
		landed := ftpdBase
		landed.Version, landed.VersionLabel = 131072, label
		cat.titles = []Title{landed}
		frel.latest["mtheall/ftpd"] = mkrel("v3.2.2", "2026-10-20T00:00:00Z")
		w.RunOnce(ctx)
		if len(public.posts) != 1 {
			t.Fatalf("label %s: public posts = %v", label, public.posts)
		}
		if len(staff.posts) != 1 || !strings.Contains(staff.posts[0], "landed") {
			t.Fatalf("label %s: staff posts = %v, want landed note only", label, staff.posts)
		}
	}
}

func TestBehindExcusesServedLabel(t *testing.T) {
	// The behind list trusts the live served label as well as the source
	// tag, so a landed release with a stale source URL is not behind.
	cat := &fakeCatalog{titles: []Title{ftpdBase}}
	frel := &fakeReleases{latest: map[string]Release{"mtheall/ftpd": mkrel("v3.2.2", "2026-10-20T00:00:00Z")}, has: map[string]bool{"mtheall/ftpd": true}}
	w, _, _ := testWatcher(cat, frel, &fakeTrigger{})
	ctx := context.Background()
	w.RunOnce(ctx)
	if snap := w.Snapshot(); snap.Behind != 1 {
		t.Fatalf("behind before landing = %+v", snap)
	}
	landed := ftpdBase
	landed.Version, landed.VersionLabel = 131072, "v3.2.2"
	cat.titles = []Title{landed}
	w.RunOnce(ctx)
	if snap := w.Snapshot(); snap.Watched != 1 || snap.Behind != 0 {
		t.Fatalf("behind after landing = %+v", snap)
	}
}

func TestDriftServesLiveLabel(t *testing.T) {
	// Drift and stuck text name the catalog's live served label, not the
	// possibly stale source tag.
	cat := &fakeCatalog{titles: []Title{ftpdBase}}
	frel := &fakeReleases{latest: map[string]Release{"mtheall/ftpd": mkrel("v3.2.1", "2026-10-01T00:00:00Z")}, has: map[string]bool{"mtheall/ftpd": true}}
	w, staff, _ := testWatcher(cat, frel, &fakeTrigger{})
	w.stuckAfter = time.Hour
	ctx := context.Background()
	w.RunOnce(ctx)
	frel.latest["mtheall/ftpd"] = mkrel("v3.2.2", "2026-10-20T00:00:00Z")
	w.RunOnce(ctx)
	if len(staff.posts) != 1 || !strings.Contains(staff.posts[0], "store serves 3.2.1") {
		t.Fatalf("drift posts = %v", staff.posts)
	}
	w.mu.Lock()
	w.states[strings.ToLower(ftpdBase.TitleID)].driftSince = time.Now().Add(-2 * time.Hour)
	w.mu.Unlock()
	w.RunOnce(ctx)
	if len(staff.posts) != 2 || !strings.Contains(staff.posts[1], "store still serves 3.2.1") {
		t.Fatalf("stuck posts = %v", staff.posts)
	}
}
