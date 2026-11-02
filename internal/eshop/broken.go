package eshop

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// brokenStateFile is the persisted first-seen map, relative to the state dir.
const brokenStateFile = "eshop-broken.json"

// brokenEntry is one package's tracked failure.
type brokenEntry struct {
	Since   time.Time `json:"since"`
	Alerted bool      `json:"alerted"`
	Context string    `json:"context"`
}

// BrokenTracker receives the sync's broken-package reports (package name to
// failure context: the 4TU date or gh:tag it failed at) and alerts staff
// once a package stays broken past the grace period. Reports arrive with
// every sync; first-seen persists to the state dir so restarts and
// redeploys don't reset the clock. An empty state dir keeps memory only.
type BrokenTracker struct {
	staff   Announcer
	staffID string
	after   time.Duration
	path    string
	log     *slog.Logger

	mu    sync.Mutex
	state map[string]brokenEntry
}

// NewBrokenTracker builds the tracker, restoring persisted state when
// stateDir is set.
func NewBrokenTracker(staff Announcer, staffID string, after time.Duration, stateDir string, log *slog.Logger) *BrokenTracker {
	if after <= 0 {
		after = 168 * time.Hour
	}
	b := &BrokenTracker{
		staff: staff, staffID: staffID, after: after,
		log: log, state: map[string]brokenEntry{},
	}
	if stateDir == "" {
		log.Info("eshop broken watch: no state dir, memory only")
		return b
	}
	b.path = filepath.Join(stateDir, brokenStateFile)
	if raw, err := os.ReadFile(b.path); err == nil {
		var saved map[string]brokenEntry
		if err := json.Unmarshal(raw, &saved); err != nil {
			log.Warn("eshop broken watch: corrupt state, starting fresh", "err", err)
		} else {
			b.state = saved
		}
	} else if !os.IsNotExist(err) {
		log.Warn("eshop broken watch: state unreadable, memory only", "err", err)
		b.path = ""
	}
	return b
}

// Report records one sync's broken set, alerting for entries past the grace
// period and noting entries that cleared after alerting. Breaches and
// clears each batch into a single staff post.
func (b *BrokenTracker) Report(broken map[string]string) {
	now := time.Now()
	b.mu.Lock()
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
	b.saveLocked()
	b.mu.Unlock()
	if len(breached) > 0 {
		sort.Strings(breached)
		head := fmt.Sprintf("[Switch - eshop] **%d packages failing to build** (past the grace period, check the sync log):", len(breached))
		b.postStaff(cappedList(head, breached))
	}
	if len(cleared) > 0 {
		sort.Strings(cleared)
		b.postStaff(cappedList("[Switch - eshop] building again:", cleared))
	}
}

// saveLocked persists first-seen state. The caller holds b.mu.
func (b *BrokenTracker) saveLocked() {
	if b.path == "" {
		return
	}
	raw, err := json.Marshal(b.state)
	if err != nil {
		b.log.Warn("eshop broken watch: state unserializable", "err", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		b.log.Warn("eshop broken watch: state dir unwritable", "err", err)
		return
	}
	if err := os.WriteFile(b.path, raw, 0o644); err != nil {
		b.log.Warn("eshop broken watch: state unwritable", "err", err)
	}
}

func (b *BrokenTracker) postStaff(msg string) {
	if b.staffID == "" {
		return
	}
	if err := b.staff.Announce(b.staffID, msg); err != nil {
		b.log.Error("eshop broken watch: staff announce failed", "err", err)
	}
}
