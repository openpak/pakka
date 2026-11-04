package eshop

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"pakka/internal/shopcore"
	"pakka/internal/store"
)

// brokenEntry is one package's tracked failure.
type brokenEntry struct {
	Since   time.Time `json:"since"`
	Alerted bool      `json:"alerted"`
	Context string    `json:"context"`
}

// BrokenBackend persists a console's tracked set. Save replaces the whole set.
type BrokenBackend interface {
	Load(ctx context.Context) (map[string]shopcore.BrokenEntry, error)
	Save(ctx context.Context, set map[string]shopcore.BrokenEntry) error
}

// ConsoleLabel is the alert prefix of a console's shop: "Switch - eshop" for the Switch (its
// established wording), "<Name> - shop" for every other console.
func ConsoleLabel(console, name string) string {
	if console == "switch" {
		return "Switch - eshop"
	}
	if name == "" {
		name = console
	}
	return name + " - shop"
}

// BrokenTracker receives the sync's broken-package reports (package name to
// failure context: the 4TU date or gh:tag it failed at) and alerts staff
// once a package stays broken past the grace period. Reports arrive with
// every sync; first-seen persists to the backend so restarts and
// redeploys don't reset the clock. One tracker per console. A nil backend
// keeps memory only.
type BrokenTracker struct {
	console string
	label   string
	staff   Announcer
	staffID string
	after   time.Duration
	backend BrokenBackend
	log     *slog.Logger

	mu     sync.Mutex
	state  map[string]brokenEntry
	loaded bool
}

// docBackend is the legacy Pakka state-doc store (the Switch tracker before shop-core).
type docBackend struct{ doc store.Doc }

func (d docBackend) Load(context.Context) (map[string]shopcore.BrokenEntry, error) {
	var saved map[string]shopcore.BrokenEntry
	err := d.doc.Load(&saved)
	return saved, err
}

func (d docBackend) Save(_ context.Context, set map[string]shopcore.BrokenEntry) error {
	return d.doc.Save(set)
}

// coreBackend keeps one console's set in shop-core's shop_broken table.
type coreBackend struct {
	c       *shopcore.Client
	console string
}

func (b coreBackend) Load(ctx context.Context) (map[string]shopcore.BrokenEntry, error) {
	return b.c.Broken(ctx, b.console)
}

func (b coreBackend) Save(ctx context.Context, set map[string]shopcore.BrokenEntry) error {
	return b.c.SetBroken(ctx, b.console, set)
}

// NewBrokenTracker builds the Switch tracker on the legacy state doc, restoring persisted state
// from st. New wiring uses NewConsoleBrokenTracker.
func NewBrokenTracker(staff Announcer, staffID string, after time.Duration, st *store.Store, log *slog.Logger) *BrokenTracker {
	var be BrokenBackend
	if st.Durable() {
		be = docBackend{st.Doc("eshop-broken")}
	} else {
		log.Info("eshop broken watch: no durable store, memory only")
	}
	return NewConsoleBrokenTracker("switch", ConsoleLabel("switch", ""), staff, staffID, after, be, log)
}

// NewConsoleBrokenTracker builds a console's tracker on a backend (nil = memory only).
func NewConsoleBrokenTracker(console, label string, staff Announcer, staffID string, after time.Duration, be BrokenBackend, log *slog.Logger) *BrokenTracker {
	if after <= 0 {
		after = 168 * time.Hour
	}
	b := &BrokenTracker{
		console: console, label: label, staff: staff, staffID: staffID, after: after,
		backend: be, log: log, state: map[string]brokenEntry{},
	}
	b.mu.Lock()
	b.loadLocked()
	b.mu.Unlock()
	return b
}

// MigrateLegacy moves a tracker's state from the old state doc into its backend, once: when the
// backend holds nothing and the doc does, the doc's entries are saved there and the doc is
// emptied so erased rows cannot return from it.
func (b *BrokenTracker) MigrateLegacy(doc store.Doc) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.backend == nil || !b.loaded || len(b.state) > 0 {
		return
	}
	var old map[string]brokenEntry
	if err := doc.Load(&old); err != nil || len(old) == 0 {
		return
	}
	b.state = old
	if err := b.saveLocked(); err != nil {
		b.state = map[string]brokenEntry{}
		return
	}
	if err := doc.Save(map[string]brokenEntry{}); err != nil {
		b.log.Warn("shop broken watch: legacy state not cleared", "console", b.console, "err", err)
	}
	b.log.Info("shop broken watch: migrated legacy state", "console", b.console, "packages", len(old))
}

// Console reports which console this tracker serves.
func (b *BrokenTracker) Console() string { return b.console }

// loadLocked reads the backend once; if it is unreachable the next Report tries again, so a
// shop-core that starts after Pakka costs nothing. Remote entries win only when older.
func (b *BrokenTracker) loadLocked() {
	if b.backend == nil || b.loaded {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	saved, err := b.backend.Load(ctx)
	if err != nil {
		b.log.Warn("shop broken watch: state unreadable, will retry", "console", b.console, "err", err)
		return
	}
	for pkg, e := range saved {
		cur, ok := b.state[pkg]
		if !ok || e.Since.Before(cur.Since) {
			b.state[pkg] = brokenEntry{Since: e.Since, Alerted: e.Alerted || cur.Alerted, Context: e.Context}
		}
	}
	b.loaded = true
}

// Snapshot returns the tracked set for the staff command.
func (b *BrokenTracker) Snapshot() map[string]time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]time.Time, len(b.state))
	for p, e := range b.state {
		out[p] = e.Since
	}
	return out
}

// Report records one sync's broken set, alerting for entries past the grace
// period and noting entries that cleared after alerting. Breaches and
// clears each batch into a single staff post.
func (b *BrokenTracker) Report(broken map[string]string) {
	now := time.Now()
	b.mu.Lock()
	b.loadLocked()
	var breached, cleared []string
	for pkg, ctx := range broken {
		e := b.state[pkg]
		if e.Since.IsZero() {
			e.Since = now
		}
		e.Context = ctx
		if !e.Alerted && now.Sub(e.Since) >= b.after {
			e.Alerted = true
			breached = append(breached, fmt.Sprintf("**%s** (stuck at %s, %dd)", pkg, ctx, int(now.Sub(e.Since).Hours()/24)))
		}
		b.state[pkg] = e
	}
	for pkg, e := range b.state {
		if _, ok := broken[pkg]; ok {
			continue
		}
		if e.Alerted {
			cleared = append(cleared, "**"+pkg+"**")
		}
		delete(b.state, pkg)
	}
	if err := b.saveLocked(); err != nil {
		b.log.Warn("shop broken watch: state unwritable", "console", b.console, "err", err)
	}
	b.mu.Unlock()
	if len(breached) > 0 {
		sort.Strings(breached)
		head := fmt.Sprintf("[%s] **%d packages failing to build** (past the grace period, check the sync log):", b.label, len(breached))
		b.postStaff(cappedList(head, breached))
	}
	if len(cleared) > 0 {
		sort.Strings(cleared)
		b.postStaff(cappedList("["+b.label+"] building again:", cleared))
	}
}

// saveLocked persists the set. The caller holds b.mu. With nothing loaded yet it refuses, so a
// backend that was unreachable at start cannot be overwritten by a partial view.
func (b *BrokenTracker) saveLocked() error {
	if b.backend == nil || !b.loaded {
		return nil
	}
	set := make(map[string]shopcore.BrokenEntry, len(b.state))
	for p, e := range b.state {
		set[p] = shopcore.BrokenEntry{Since: e.Since, Alerted: e.Alerted, Context: e.Context}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return b.backend.Save(ctx, set)
}

func (b *BrokenTracker) postStaff(msg string) {
	if b.staffID == "" {
		return
	}
	if err := b.staff.Announce(b.staffID, msg); err != nil {
		b.log.Error("shop broken watch: staff announce failed", "console", b.console, "err", err)
	}
}

// CoreBackend keeps a console's broken set in shop-core's shop_broken table.
func CoreBackend(c *shopcore.Client, console string) BrokenBackend {
	return coreBackend{c: c, console: console}
}
