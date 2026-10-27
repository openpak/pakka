// Package eshop watches the OpenPak eShop catalog against upstream homebrew
// releases. When a tracked title's upstream publishes a new release, the
// watcher alerts the staff channel and triggers the sync pipeline
// (hbstore-tools) that builds the new NSP and publishes it through the
// store admin API. When the new version lands in the catalog, the watcher
// posts the standardized public announcement.
//
// Upstream joins mirror hbstore-tools: a title whose distribution source is
// a GitHub release URL tracks that repo directly; the rest join through a
// sources.json-style package->repo map via the hbstore title id scheme.
package eshop

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Title is the catalog subset the watcher reads per eShop title.
type Title struct {
	TitleID      string
	Name         string
	Version      uint32
	VersionLabel string
	RevisedAt    int64
	Changelog    string
	Source       string // distribution.source
	Published    bool
}

// Release is an upstream's latest GitHub release.
type Release struct {
	Tag         string
	PublishedAt string // RFC3339; rolling tags move it
	HTMLURL     string
	Body        string
}

// Catalog lists the store's titles (published and unpublished).
type Catalog interface {
	Titles(ctx context.Context) ([]Title, error)
}

// Releases resolves a repo slug (owner/repo) to its latest release.
// ok=false means the repo publishes no releases; that is not an error.
type Releases interface {
	Latest(ctx context.Context, slug string) (rel Release, ok bool, err error)
}

// Announcer posts a message to a channel.
type Announcer interface {
	Announce(channelID, content string) error
}

// Trigger runs the sync pipeline that builds new NSPs and publishes them
// to the eShop. Implementations are single-flight: overlapping requests
// are refused while a run is in flight or cooling down.
type Trigger interface {
	// Request asks for a sync run for reason. When a run starts, post
	// receives the finish notice; when refused, started is false and note
	// explains why (already running, cooling down).
	Request(reason string, post func(msg string)) (started bool, note string)
	// Status describes the last or current run.
	Status() TriggerStatus
}

// TriggerStatus is one trigger's last known run state.
type TriggerStatus struct {
	Running   bool
	LastStart time.Time
	LastEnd   time.Time
	LastOK    bool
	LastInfo  string
}

// titleState is one watched title's last known state.
type titleState struct {
	version uint32
	label   string
	slug    string // "" = upstream-untracked (4TU-only)
	seen    string // tag@published_at last observed upstream
	shipped string // tag recorded in the catalog source URL
	name    string
	// drift tracks the announced-but-unlanded upstream release for the
	// failed-update alert: tag@published_at, when announced, and whether
	// the failure was already reported.
	drift      string
	driftSince time.Time
	reminded   bool
}

// BehindTitle is one title whose upstream is newer than the store.
type BehindTitle struct {
	Name    string
	Shipped string // "" when the store never shipped an upstream release
	Latest  string
}

// Status is the watcher's snapshot for `!pakka eshop`.
type Status struct {
	Watched      int
	Behind       int
	BehindTitles []BehindTitle // capped
	LastPoll     time.Time
	LastErr      string
	CatalogDown  bool
	Trigger      TriggerStatus
	TriggerSet   bool
}

// Watcher polls the catalog and upstream releases, alerting staff on drift
// and announcing landed updates publicly. The first poll only baselines,
// so (re)starts never flood either channel.
type Watcher struct {
	catalog  Catalog
	releases Releases
	trigger  Trigger // nil = detect and announce only
	staff    Announcer
	public   Announcer
	staffID  string
	publicID string
	// byTitle maps hbstore title ids to repo slugs for titles whose
	// catalog source is not a GitHub release URL yet.
	byTitle map[string]string
	log     *slog.Logger

	mu        sync.Mutex
	states    map[string]*titleState
	baselined bool
	lastPoll  time.Time
	lastErr   string
	down      bool
	// stuckAfter is how long a drift may go unlanded before it counts
	// as failed (six hourly sync chances by default).
	stuckAfter time.Duration
}

// New builds the watcher; sources maps 4TU package names to repo slugs and
// may be nil for catalog-derived tracking only.
func New(catalog Catalog, releases Releases, trigger Trigger, staff, public Announcer, staffID, publicID string, sources map[string]string, log *slog.Logger) *Watcher {
	byTitle := map[string]string{}
	for pkg, slug := range sources {
		byTitle[TitleID(pkg)] = slug
	}
	return &Watcher{
		catalog: catalog, releases: releases, trigger: trigger,
		staff: staff, public: public, staffID: staffID, publicID: publicID,
		byTitle: byTitle, log: log, states: map[string]*titleState{},
		stuckAfter: 6 * time.Hour,
	}
}

// Run polls every interval until ctx ends.
func (w *Watcher) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	w.RunOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.RunOnce(ctx)
		}
	}
}

// RunOnce performs a single poll (used by tests and the run loop).
func (w *Watcher) RunOnce(ctx context.Context) {
	titles, err := w.catalog.Titles(ctx)
	w.mu.Lock()
	baselined := w.baselined
	w.mu.Unlock()
	if err != nil {
		w.mu.Lock()
		w.lastErr = err.Error()
		wasDown := w.down
		w.down = true
		w.mu.Unlock()
		w.log.Error("eshop watch: catalog fetch failed", "err", err)
		if !wasDown {
			w.postStaff(fmt.Sprintf("[Switch - eshop] catalog unreachable: %s", err))
		}
		return
	}
	w.mu.Lock()
	wasDown := w.down
	w.down = false
	w.lastErr = ""
	w.lastPoll = time.Now()
	w.mu.Unlock()
	if wasDown {
		w.postStaff("[Switch - eshop] catalog reachable again.")
	}
	var landed []landedUpdate
	for _, t := range titles {
		if !t.Published {
			continue
		}
		if lu, ok := w.checkTitle(ctx, t, baselined); ok {
			landed = append(landed, lu)
		}
	}
	// One public post per poll: a single update keeps its own
	// announcement, several land as one digest listing.
	switch len(landed) {
	case 0:
	case 1:
		w.postPublic(announceUpdate(landed[0].title))
	default:
		w.postPublic(announceDigest(landed))
	}
	w.mu.Lock()
	w.baselined = true
	w.mu.Unlock()
}

// landedUpdate is one title whose served version increased this poll.
type landedUpdate struct {
	title     Title
	prevLabel string
}

// checkTitle handles one published title: landed-version detection first
// (any source; the caller batches the public post), then upstream drift
// (tracked titles only). It reports a landed update for the digest.
func (w *Watcher) checkTitle(ctx context.Context, t Title, baselined bool) (landedUpdate, bool) {
	slug, shipped, _ := ParseReleaseSource(t.Source)
	if slug == "" {
		w.mu.Lock()
		slug = w.byTitle[strings.ToLower(t.TitleID)]
		w.mu.Unlock()
	}
	w.mu.Lock()
	st, seen := w.states[strings.ToLower(t.TitleID)]
	if st == nil {
		st = &titleState{}
		w.states[strings.ToLower(t.TitleID)] = st
	}
	prevVersion, prevLabel, prevSeen := st.version, st.label, st.seen
	st.version, st.label, st.slug, st.shipped, st.name = t.Version, t.VersionLabel, slug, shipped, t.Name
	w.mu.Unlock()

	var landed landedUpdate
	isLanded := false
	if !baselined || !seen {
		if baselined && !seen {
			w.postStaff(fmt.Sprintf("[Switch - eshop] **%s** is new on the eShop (%s).", t.Name, displayLabel(t.VersionLabel)))
		}
		// Newly seen titles still record upstream state below without alerting.
	} else if t.Version > prevVersion {
		landed, isLanded = landedUpdate{title: t, prevLabel: prevLabel}, true
		w.postStaff(fmt.Sprintf("[Switch - eshop] **%s** %s → **%s** landed on the eShop (announced in #announcements).",
			t.Name, displayLabel(prevLabel), t.VersionLabel))
	}
	if slug == "" {
		return landed, isLanded
	}
	rel, ok, err := w.releases.Latest(ctx, slug)
	if err != nil {
		w.log.Error("eshop watch: release lookup failed", "slug", slug, "err", err)
		return landed, isLanded
	}
	if !ok {
		return landed, isLanded
	}
	current := rel.Tag + "@" + rel.PublishedAt
	w.mu.Lock()
	st.seen = current
	w.mu.Unlock()
	if !baselined || !seen {
		return landed, isLanded
	}
	if msg := w.checkStuck(t, st, shipped); msg != "" {
		w.postStaff(msg)
	}
	if current == prevSeen {
		return landed, isLanded
	}
	// A new upstream release (moved tag or moved rolling-tag date).
	served := shipped
	if served == "" {
		served = "a 4TU build"
	}
	syncNote := "no sync command configured — the hourly sync will pick it up"
	if w.trigger != nil {
		reason := fmt.Sprintf("%s: %s -> %s", t.Name, served, rel.Tag)
		if started, note := w.trigger.Request(reason, w.postStaff); started {
			syncNote = "sync started"
		} else {
			syncNote = note
		}
	}
	w.postStaff(fmt.Sprintf("[Switch - eshop] **%s** has a new upstream release **%s** (`%s`); store serves %s. %s.",
		t.Name, rel.Tag, slug, served, syncNote))
	w.mu.Lock()
	st.drift, st.driftSince, st.reminded = current, time.Now(), false
	w.mu.Unlock()
	return landed, isLanded
}

// checkStuck reports a drifted title whose update never lands: the
// upstream release was announced, the grace period passed, and the store
// still serves an older tag. Fires once per drift tag; landing or a newer
// release re-arms it. It returns the staff message, posted by the caller
// outside the lock.
func (w *Watcher) checkStuck(t Title, st *titleState, shipped string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if st.drift == "" || st.reminded {
		return ""
	}
	if shipped != "" && shipped == seenTag(st.drift) {
		st.drift, st.reminded = "", false
		return ""
	}
	if time.Since(st.driftSince) < w.stuckAfter {
		return ""
	}
	st.reminded = true
	served := shipped
	if served == "" {
		served = "a 4TU build"
	}
	return fmt.Sprintf("[Switch - eshop] **%s** failed to update to **%s** — store still serves %s. Check the sync log.",
		t.Name, seenTag(st.drift), served)
}

// Snapshot returns the watch state for `!pakka eshop`; safe for concurrent use.
func (w *Watcher) Snapshot() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	snap := Status{
		LastPoll: w.lastPoll, LastErr: w.lastErr, CatalogDown: w.down,
		TriggerSet: w.trigger != nil,
	}
	if w.trigger != nil {
		snap.Trigger = w.trigger.Status()
	}
	for _, st := range w.states {
		if st.slug == "" {
			continue
		}
		snap.Watched++
		latest := seenTag(st.seen)
		if st.seen != "" && latest != st.shipped {
			snap.Behind++
			if len(snap.BehindTitles) < 10 {
				snap.BehindTitles = append(snap.BehindTitles, BehindTitle{Name: st.name, Shipped: st.shipped, Latest: latest})
			}
		}
	}
	return snap
}

// seenTag splits the tag off a tag@published_at marker. Tags never carry
// @, so the last one is the separator.
func seenTag(seen string) string {
	if i := strings.LastIndexByte(seen, '@'); i >= 0 {
		return seen[:i]
	}
	return seen
}

func (w *Watcher) postStaff(msg string) {
	if w.staffID == "" {
		return
	}
	if err := w.staff.Announce(w.staffID, msg); err != nil {
		w.log.Error("eshop watch: staff announce failed", "err", err)
	}
}

func (w *Watcher) postPublic(msg string) {
	if w.publicID == "" {
		return
	}
	if err := w.public.Announce(w.publicID, msg); err != nil {
		w.log.Error("eshop watch: public announce failed", "err", err)
	}
}

func displayLabel(label string) string {
	if label == "" {
		return "a new version"
	}
	return label
}

// announceUpdate renders the standardized public post for a landed update.
func announceUpdate(t Title) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, ":arrow_up: **%s** updated to **%s** — available now on the OpenPak eShop. Update from the eShop to play the latest version.",
		t.Name, displayLabel(t.VersionLabel))
	if excerpt := changelogExcerpt(t.Changelog); excerpt != "" {
		fmt.Fprintf(&sb, "\n> %s", excerpt)
	}
	return sb.String()
}

// announceDigest renders one public post listing every title that landed
// this poll. The list is capped to stay under Discord's message limit;
// the overflow counts as one closing line, never a second post.
func announceDigest(landed []landedUpdate) string {
	const maxLen = 1900
	var sb strings.Builder
	fmt.Fprintf(&sb, ":arrow_up: **%d homebrew updates** are available on the OpenPak eShop. Update from the eShop to play the latest versions.", len(landed))
	shown := 0
	for _, lu := range landed {
		line := fmt.Sprintf("\n- **%s** updated to **%s**", lu.title.Name, displayLabel(lu.title.VersionLabel))
		if sb.Len()+len(line) > maxLen {
			break
		}
		sb.WriteString(line)
		shown++
	}
	if left := len(landed) - shown; left > 0 {
		fmt.Fprintf(&sb, "\n…and %d more.", left)
	}
	return sb.String()
}

// changelogExcerpt takes the first paragraph of the changelog, one line,
// capped so the announcement stays a fixed shape.
func changelogExcerpt(body string) string {
	body = strings.TrimSpace(body)
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[:i]
	}
	body = strings.Join(strings.Fields(body), " ")
	if len(body) > 300 {
		body = body[:300] + "…"
	}
	return body
}

// TitleID recomputes hbstore-tools' stable title id for a package name
// (or "gh:owner/repo…" key): 01fe + sha1 hex [:9] + 000.
func TitleID(name string) string {
	sum := sha1.Sum([]byte(name))
	return fmt.Sprintf("01fe%x000", sum)[:13] + "000"
}

var releaseURL = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([^/]+/[^/]+)/releases/tag/([^/]+?)/?$`)

// ParseReleaseSource reads a catalog distribution source: a GitHub release
// URL yields its repo slug and shipped tag; anything else is untracked.
func ParseReleaseSource(src string) (slug, tag string, ok bool) {
	m := releaseURL.FindStringSubmatch(strings.TrimSpace(src))
	if m == nil {
		return "", "", false
	}
	repo := strings.TrimSuffix(m[1], ".git")
	if !strings.Contains(repo, "/") || strings.ContainsAny(repo, " \t") {
		return "", "", false
	}
	return strings.ToLower(repo), m[2], true
}

var slugShape = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

// ParseSources reads a sources.json-style package->slug map: inline JSON,
// or @path to read the file. Malformed slugs are dropped.
func ParseSources(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]string{}, nil
	}
	if path, ok := strings.CutPrefix(raw, "@"); ok {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		raw = string(b)
	}
	var in map[string]string
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for pkg, slug := range in {
		slug = strings.TrimSpace(slug)
		if pkg == "" || !slugShape.MatchString(slug) {
			continue
		}
		out[pkg] = strings.ToLower(slug)
	}
	return out, nil
}
