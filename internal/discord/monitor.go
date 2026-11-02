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
	alerts    Announcer // nil = no staff-alerts mirror
	alertsID  string
	log       *slog.Logger
	downAfter int                 // consecutive failures before paging
	depends   map[string][]string // child target -> parent targets
	mu        sync.RWMutex
	states    map[string]*targetState
	acks      map[string]time.Time // target -> ack expiry
	baselined bool
}

// targetState is one target's tracked outage state.
type targetState struct {
	up        bool
	seen      bool
	fails     int       // consecutive failures in the current streak
	posted    bool      // DOWN alert posted for the current streak
	downSince time.Time // first failure of the current streak
	lastErr   string
}

// SetAlertsMirror also posts tagged transitions to the staff alerts
// channel; targets named "Area/service" render as [Area - service].
func (m *Monitor) SetAlertsMirror(a Announcer, channelID string) {
	m.alerts, m.alertsID = a, channelID
}

// AlertTag renders a target's alerts-channel tag: "Area/service" names
// keep their taxonomy, plain names fall under Status.
func AlertTag(name string) string {
	if area, service, ok := strings.Cut(name, "/"); ok {
		area, service = strings.TrimSpace(area), strings.TrimSpace(service)
		if area != "" && service != "" {
			return "[" + area + " - " + service + "]"
		}
	}
	return "[Status - " + name + "]"
}

// shortName strips the area off an "Area/service" target for the message
// body; the tag already carries it.
func shortName(name string) string {
	if _, service, ok := strings.Cut(name, "/"); ok {
		if service = strings.TrimSpace(service); service != "" {
			return service
		}
	}
	return name
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
		st := m.states[t.Name]
		if st == nil {
			out = append(out, TargetState{Name: t.Name})
			continue
		}
		out = append(out, TargetState{Name: t.Name, Up: st.up, Seen: st.seen})
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
		downAfter: 3,
		states:    map[string]*targetState{},
		acks:      map[string]time.Time{},
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
		statusMsg, alertMsg := m.transition(t, up, detail)
		if statusMsg != "" {
			if err := m.announce.Announce(m.channelID, statusMsg); err != nil {
				m.log.Error("status announce failed", "target", t.Name, "err", err)
			}
		}
		if alertMsg != "" && m.alerts != nil && m.alertsID != "" {
			if err := m.alerts.Announce(m.alertsID, alertMsg); err != nil {
				m.log.Error("alerts mirror failed", "target", t.Name, "err", err)
			}
		}
	}
	m.mu.Lock()
	m.baselined = true
	m.mu.Unlock()
}

// transition folds one probe result into state and returns the status and
// alerts messages to post ("" = silent). DOWN pages only after downAfter
// consecutive failures, and stays silent while acked or while a declared
// dependency is posted-down; recovery announces with the outage length,
// but only when the outage was announced.
func (m *Monitor) transition(t Target, up bool, detail string) (string, string) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.states[t.Name]
	if st == nil {
		st = &targetState{}
		m.states[t.Name] = st
	}
	if !m.baselined || !st.seen {
		st.seen, st.up = true, up
		if !up {
			st.fails, st.downSince, st.lastErr = 1, now, detail
		}
		return "", ""
	}
	status := func(state string) string {
		return fmt.Sprintf(":red_circle: **%s** %s\n`%s`", t.Name, state, t.URL)
	}
	if up {
		posted, downFor := st.posted, now.Sub(st.downSince)
		st.up, st.fails, st.posted, st.lastErr = true, 0, false, ""
		delete(m.acks, t.Name)
		if !posted {
			return "", ""
		}
		body := fmt.Sprintf("is back UP — %s (was down %s)", detail, fmtDowntime(downFor))
		return fmt.Sprintf(":green_circle: **%s** %s", t.Name, body),
			AlertTag(t.Name) + " " + shortName(t.Name) + " " + body
	}
	if st.fails == 0 {
		st.downSince = now
	}
	st.up, st.lastErr = false, detail
	st.fails++
	if st.posted || st.fails < m.downAfter {
		return "", ""
	}
	if exp, ok := m.acks[t.Name]; ok {
		if now.Before(exp) {
			m.log.Info("status down suppressed by ack", "target", t.Name)
			return "", ""
		}
		delete(m.acks, t.Name)
	}
	for _, parent := range m.depends[t.Name] {
		if ps, ok := m.states[parent]; ok && ps.posted {
			m.log.Info("status down suppressed by dependency", "target", t.Name, "parent", parent)
			return "", ""
		}
	}
	st.posted = true
	polls := fmt.Sprintf("%d bad polls", st.fails)
	if st.fails == 1 {
		polls = "1 bad poll"
	}
	body := fmt.Sprintf("is DOWN — %s (failing %s, %s)", detail, fmtDowntime(now.Sub(st.downSince)), polls)
	return status(body), AlertTag(t.Name) + " " + shortName(t.Name) + " " + body
}

// fmtDowntime renders an outage length compactly.
func fmtDowntime(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if m := int(d.Minutes()) % 60; m > 0 {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), m)
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

// SetDownAfter sets how many consecutive failures page (default 3).
func (m *Monitor) SetDownAfter(n int) {
	if n < 1 {
		n = 1
	}
	m.downAfter = n
}

// ParseDepends parses "child=parent,child=parent" dependency pairs. A
// child failing while a parent is posted-down stays silent; it pages once
// the parent recovers if it is still failing.
func ParseDepends(v string) map[string][]string {
	var out map[string][]string
	for _, entry := range strings.Split(v, ",") {
		child, parent, ok := strings.Cut(entry, "=")
		child, parent = strings.TrimSpace(child), strings.TrimSpace(parent)
		if !ok || child == "" || parent == "" {
			continue
		}
		if out == nil {
			out = map[string][]string{}
		}
		out[child] = append(out[child], parent)
	}
	return out
}

// SetDepends installs the dependency map (nil disables suppression).
func (m *Monitor) SetDepends(d map[string][]string) {
	m.depends = d
}

// matchLocked resolves a target name case-insensitively: exact first,
// then unique substring.
func (m *Monitor) matchLocked(name string) (string, bool) {
	for _, t := range m.targets {
		if strings.EqualFold(t.Name, name) {
			return t.Name, true
		}
	}
	var found string
	n := 0
	for _, t := range m.targets {
		if strings.Contains(strings.ToLower(t.Name), strings.ToLower(name)) {
			found, n = t.Name, n+1
		}
	}
	return found, n == 1
}

// Ack silences name's DOWN alerts for ttl (default 1h), returning the
// matched target. Recovery clears the ack early.
func (m *Monitor) Ack(name string, ttl time.Duration) (string, bool) {
	if ttl <= 0 {
		ttl = time.Hour
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	matched, ok := m.matchLocked(name)
	if !ok {
		return "", false
	}
	if m.acks == nil {
		m.acks = map[string]time.Time{}
	}
	m.acks[matched] = time.Now().Add(ttl)
	return matched, true
}

// Unack lifts name's ack; false means no such target or no ack.
func (m *Monitor) Unack(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	matched, ok := m.matchLocked(name)
	if !ok {
		return false
	}
	if _, ok := m.acks[matched]; !ok {
		return false
	}
	delete(m.acks, matched)
	return true
}

// Acks returns the active (unexpired) ack expiries by target.
func (m *Monitor) Acks() map[string]time.Time {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]time.Time{}
	for name, exp := range m.acks {
		if now.Before(exp) {
			out[name] = exp
			continue
		}
		delete(m.acks, name)
	}
	return out
}
