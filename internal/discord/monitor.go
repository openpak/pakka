package discord

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Target is one monitored endpoint: an https:// URL or tcp://host:port.
type Target struct {
	Name string
	URL  string
}

// ParseTargets parses "name=url,name=url". Entries with empty names,
// unsupported schemes, or commas inside are skipped.
func ParseTargets(v string) []Target {
	var out []Target
	for _, entry := range strings.Split(v, ",") {
		name, url, ok := strings.Cut(entry, "=")
		name, url = strings.TrimSpace(name), strings.TrimSpace(url)
		if !ok || name == "" || url == "" {
			continue
		}
		if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "tcp://") {
			continue
		}
		out = append(out, Target{Name: name, URL: url})
	}
	return out
}

// Prober checks one target: up plus a short detail string.
type Prober interface {
	Probe(ctx context.Context, t Target) (up bool, detail string)
}

// netProber is the live prober: 2xx is up for HTTPS, connect is up for TCP.
type netProber struct {
	timeout time.Duration
}

// NewNetProber builds the live prober.
func NewNetProber(timeout time.Duration) Prober {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return netProber{timeout: timeout}
}

func (p netProber) Probe(ctx context.Context, t Target) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	if addr, ok := strings.CutPrefix(t.URL, "tcp://"); ok {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return false, err.Error()
		}
		_ = conn.Close()
		return true, "connected"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		return false, err.Error()
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return false, fmt.Sprintf("status %d", resp.StatusCode)
	}
	return true, fmt.Sprintf("status %d", resp.StatusCode)
}

// Announcer posts status transitions to the status channel.
type Announcer interface {
	Announce(channelID, content string) error
}

// SenderAnnouncer adapts a Sender to Announcer.
type SenderAnnouncer struct {
	Sender Sender
}

// Announce implements Announcer.
func (a SenderAnnouncer) Announce(channelID, content string) error {
	_, err := a.Sender.ChannelMessageSend(channelID, content)
	return err
}

// Monitor polls targets on an interval and announces up/down transitions.
// The first poll only baselines state, so restarts never flood the channel.
type Monitor struct {
	targets   []Target
	interval  time.Duration
	prober    Prober
	announce  Announcer
	channelID string
	log       *slog.Logger
	mu        sync.RWMutex
	states    map[string]bool
	baselined bool
}

// TargetState is one target's last known state.
type TargetState struct {
	Name string
	Up   bool
	Seen bool // false before the first poll covers it
}

// Snapshot returns the last known state per target for the mod agent.
// Empty before the first poll; safe for concurrent use.
func (m *Monitor) Snapshot() []TargetState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]TargetState, 0, len(m.targets))
	for _, t := range m.targets {
		up, seen := m.states[t.Name]
		out = append(out, TargetState{Name: t.Name, Up: up, Seen: seen})
	}
	return out
}

// NewMonitor builds the monitor; targets must be pre-parsed and non-empty.
func NewMonitor(targets []Target, interval time.Duration, prober Prober, announce Announcer, channelID string, log *slog.Logger) *Monitor {
	if interval <= 0 {
		interval = time.Minute
	}
	return &Monitor{
		targets: targets, interval: interval, prober: prober,
		announce: announce, channelID: channelID, log: log,
		states: map[string]bool{},
	}
}

// Run polls until ctx ends.
func (m *Monitor) Run(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	m.check(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.check(ctx)
		}
	}
}

// RunOnce performs a single poll (used by tests).
func (m *Monitor) RunOnce(ctx context.Context) {
	m.check(ctx)
}

func (m *Monitor) check(ctx context.Context) {
	for _, t := range m.targets {
		up, detail := m.prober.Probe(ctx, t)
		m.mu.Lock()
		prev, seen := m.states[t.Name]
		m.states[t.Name] = up
		baselined := m.baselined
		m.mu.Unlock()
		if !baselined || !seen {
			continue
		}
		if up == prev {
			continue
		}
		var msg string
		if up {
			msg = fmt.Sprintf(":green_circle: **%s** is back UP — %s", t.Name, detail)
		} else {
			msg = fmt.Sprintf(":red_circle: **%s** is DOWN — %s\n`%s`", t.Name, detail, t.URL)
		}
		if err := m.announce.Announce(m.channelID, msg); err != nil {
			m.log.Error("status announce failed", "target", t.Name, "err", err)
		}
	}
	m.mu.Lock()
	m.baselined = true
	m.mu.Unlock()
}
