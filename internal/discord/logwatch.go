package discord

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// JournalEvent is the subset of journalctl -o json we classify.
type JournalEvent struct {
	Message    string
	Priority   int // syslog priority, -1 when absent/unparseable
	Unit       string
	Identifier string
	Hostname   string
}

// ParseJournalEvent decodes one journalctl JSON line. MESSAGE may be a
// string or an array of strings.
func ParseJournalEvent(data []byte) (JournalEvent, error) {
	var raw struct {
		Message    any    `json:"MESSAGE"`
		Priority   string `json:"PRIORITY"`
		Unit       string `json:"_SYSTEMD_UNIT"`
		Identifier string `json:"SYSLOG_IDENTIFIER"`
		Hostname   string `json:"_HOSTNAME"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return JournalEvent{}, err
	}
	ev := JournalEvent{Priority: -1, Unit: raw.Unit, Identifier: raw.Identifier, Hostname: raw.Hostname}
	switch m := raw.Message.(type) {
	case string:
		ev.Message = m
	case []any:
		parts := make([]string, 0, len(m))
		for _, p := range m {
			if s, ok := p.(string); ok {
				parts = append(parts, s)
			}
		}
		ev.Message = strings.Join(parts, "\n")
	}
	if raw.Priority != "" {
		if p, err := strconv.Atoi(raw.Priority); err == nil {
			ev.Priority = p
		}
	}
	return ev, nil
}

var moderationWords = []string{
	"report", "abuse", "abusive", "spam", "spammer", "ban", "banned",
	"cheat", "cheater", "harass", "moderation", "inappropriate",
	"profile_status", "passport",
}

// LogWatcher classifies journal lines and announces errors/warnings and
// moderation signals to their Discord channels.
type LogWatcher struct {
	units      []string
	modChannel string
	opsChannel string
	announce   Announcer
	cooldown   time.Duration
	mu         sync.Mutex
	last       map[string]time.Time
	log        *slog.Logger
}

// NewLogWatcher builds the watcher. Either channel may be empty to disable
// that feed; units uses glob patterns ("openpak-*", "*", exact).
func NewLogWatcher(units []string, modChannel, opsChannel string, announce Announcer, cooldown time.Duration, log *slog.Logger) *LogWatcher {
	if cooldown <= 0 {
		cooldown = 30 * time.Minute
	}
	return &LogWatcher{
		units: units, modChannel: modChannel, opsChannel: opsChannel,
		announce: announce, cooldown: cooldown,
		last: map[string]time.Time{}, log: log,
	}
}

// Handle classifies one journal line and announces matches.
func (w *LogWatcher) Handle(data []byte) {
	ev, err := ParseJournalEvent(data)
	if err != nil || ev.Message == "" {
		return
	}
	if !matchAnyUnit(w.units, ev.Unit) {
		return
	}
	first := firstLine(ev.Message)
	source := ev.Unit
	if ev.Hostname != "" {
		source = ev.Hostname + "/" + ev.Unit
	}
	if w.opsChannel != "" && ev.Priority >= 0 && ev.Priority <= 4 {
		w.post(w.opsChannel, source, fmt.Sprintf(":warning: **%s** `%s` — %s", levelName(ev.Priority), source, first))
	}
	if w.modChannel != "" && containsWord(strings.ToLower(ev.Message), moderationWords) {
		w.post(w.modChannel, source, fmt.Sprintf(":shield: possible moderation signal in `%s` — %s", source, first))
	}
}

func (w *LogWatcher) post(channel, source, msg string) {
	key := channel + "\x00" + source + "\x00" + truncateKey(msg)
	w.mu.Lock()
	if len(w.last) > 1000 {
		w.last = map[string]time.Time{}
	}
	if last, seen := w.last[key]; seen && time.Since(last) < w.cooldown {
		w.mu.Unlock()
		return
	}
	w.last[key] = time.Now()
	w.mu.Unlock()
	if err := w.announce.Announce(channel, truncate(msg)); err != nil {
		w.log.Error("log announce failed", "channel", channel, "err", err)
	}
}

func truncateKey(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

func matchAnyUnit(patterns []string, unit string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if matchUnit(p, unit) {
			return true
		}
	}
	return false
}

func matchUnit(pattern, unit string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(unit, strings.TrimSuffix(pattern, "*"))
	}
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(unit, strings.TrimPrefix(pattern, "*"))
	}
	return pattern == unit
}

func levelName(p int) string {
	if p <= 3 {
		return "error"
	}
	return "warning"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func containsWord(lower string, words []string) bool {
	for _, w := range words {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}
