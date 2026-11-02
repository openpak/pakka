// Package releases nudges staff when a watched repo's latest release ages
// past the rollout window: "vX is N days old, confirm it's deployed".
// Pakka cannot see running versions, so this is a time-based reminder, not
// deployment detection.
package releases

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"pakka/internal/eshop"
)

// Lister resolves a repo slug to its latest release.
type Lister interface {
	Latest(ctx context.Context, slug string) (eshop.Release, bool, error)
}

// Announcer posts a message to a channel.
type Announcer interface {
	Announce(channelID, content string) error
}

// ParseRepos parses a comma-separated repo list. Bare names mean openpak/*
// repos; full owner/name slugs pass through.
func ParseRepos(v string) []string {
	var out []string
	for _, name := range strings.Split(v, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !strings.Contains(name, "/") {
			name = "openpak/" + name
		}
		out = append(out, name)
	}
	return out
}

type seen struct {
	Tag     string `json:"tag"`
	Alerted bool   `json:"alerted"`
}

// Watcher polls watched repos' latest releases and pages once a release
// ages past the rollout window. First sight of a tag baselines silently
// (old tags grandfathered); only new releases can nag.
type Watcher struct {
	lister  Lister
	repos   []string
	after   time.Duration
	staff   Announcer
	staffID string
	path    string
	log     *slog.Logger

	mu    sync.Mutex
	state map[string]seen
}

// NewWatcher builds the watcher, restoring persisted state when stateDir
// is set.
func NewWatcher(lister Lister, repos []string, after time.Duration, staff Announcer, staffID, stateDir string, log *slog.Logger) *Watcher {
	if after <= 0 {
		after = 72 * time.Hour
	}
	w := &Watcher{
		lister: lister, repos: repos, after: after,
		staff: staff, staffID: staffID, log: log, state: map[string]seen{},
	}
	if stateDir == "" {
		return w
	}
	w.path = filepath.Join(stateDir, "releases.json")
	if raw, err := os.ReadFile(w.path); err == nil {
		var saved map[string]seen
		if err := json.Unmarshal(raw, &saved); err != nil {
			log.Warn("release watch: corrupt state, starting fresh", "err", err)
		} else {
			w.state = saved
		}
	} else if !os.IsNotExist(err) {
		log.Warn("release watch: state unreadable, memory only", "err", err)
		w.path = ""
	}
	return w
}

// Run checks on an interval until ctx ends.
func (w *Watcher) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Check(ctx)
		}
	}
}

// Check performs a single pass (used by tests).
func (w *Watcher) Check(ctx context.Context) {
	now := time.Now()
	for _, repo := range w.repos {
		rel, ok, err := w.lister.Latest(ctx, repo)
		if err != nil {
			w.log.Warn("release watch: lookup failed", "repo", repo, "err", err)
			continue
		}
		if !ok {
			continue
		}
		pub, err := time.Parse(time.RFC3339, rel.PublishedAt)
		if err != nil {
			w.log.Warn("release watch: bad published_at", "repo", repo, "err", err)
			continue
		}
		w.mu.Lock()
		prev, known := w.state[repo]
		if !known {
			w.state[repo] = seen{Tag: rel.Tag, Alerted: now.Sub(pub) >= w.after}
			w.saveLocked()
			w.mu.Unlock()
			continue
		}
		if prev.Tag != rel.Tag {
			w.state[repo] = seen{Tag: rel.Tag}
			w.saveLocked()
			w.mu.Unlock()
			continue
		}
		if prev.Alerted || now.Sub(pub) < w.after {
			w.mu.Unlock()
			continue
		}
		prev.Alerted = true
		w.state[repo] = prev
		w.saveLocked()
		w.mu.Unlock()
		short, _ := strings.CutPrefix(repo, "openpak/")
		w.postStaff(fmt.Sprintf("[Releases - %s] %s released %s ago — confirm it's deployed.",
			short, rel.Tag, fmtAge(now.Sub(pub))))
	}
}

func (w *Watcher) saveLocked() {
	if w.path == "" {
		return
	}
	raw, err := json.Marshal(w.state)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
		w.log.Warn("release watch: state dir unwritable", "err", err)
		return
	}
	if err := os.WriteFile(w.path, raw, 0o644); err != nil {
		w.log.Warn("release watch: state unwritable", "err", err)
	}
}

func (w *Watcher) postStaff(msg string) {
	if w.staffID == "" {
		return
	}
	if err := w.staff.Announce(w.staffID, msg); err != nil {
		w.log.Error("release watch: staff announce failed", "err", err)
	}
}

// fmtAge renders a release age compactly.
func fmtAge(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 48*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
