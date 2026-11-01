package chat

import (
	"sync"
	"time"
)

// Limiter enforces per-account rates: a sliding burst window, a UTC daily
// cap and an hourly diagnosis budget. State is in memory; a restart resets it.
type Limiter struct {
	mu    sync.Mutex
	burst int
	daily int
	diag  int
	now   func() time.Time
	win   map[string][]time.Time
	day   map[string]dayCount
	dg    map[string][]time.Time
}

type dayCount struct {
	day string
	n   int
}

// Limit reasons.
const (
	LimitBurst = "burst"
	LimitDaily = "daily"
)

// NewLimiter builds a limiter.
func NewLimiter(burstPerMinute, dailyCap, diagnosesPerHour int) *Limiter {
	return &Limiter{burst: burstPerMinute, daily: dailyCap, diag: diagnosesPerHour, now: time.Now,
		win: map[string][]time.Time{}, day: map[string]dayCount{}, dg: map[string][]time.Time{}}
}

// AllowMessage spends one message; reason is "" when allowed, else the limit hit.
func (l *Limiter) AllowMessage(account string) (reason string, retry time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	d := now.UTC().Format("2006-01-02")
	dc := l.day[account]
	if dc.day != d {
		dc = dayCount{day: d}
	}
	if dc.n >= l.daily {
		next := now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
		return LimitDaily, next.Sub(now)
	}
	kept := l.win[account][:0]
	for _, t := range l.win[account] {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.burst {
		l.win[account] = kept
		return LimitBurst, time.Minute - now.Sub(kept[0])
	}
	l.win[account] = append(kept, now)
	dc.n++
	l.day[account] = dc
	return "", 0
}

// AllowDiagnosis spends one connection diagnosis.
func (l *Limiter) AllowDiagnosis(account string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	kept := l.dg[account][:0]
	for _, t := range l.dg[account] {
		if now.Sub(t) < time.Hour {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.diag {
		l.dg[account] = kept
		return false
	}
	l.dg[account] = append(kept, now)
	return true
}

// Sweep drops idle entries so the maps stay bounded.
func (l *Limiter) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	today := now.UTC().Format("2006-01-02")
	for k, v := range l.day {
		if v.day != today {
			delete(l.day, k)
		}
	}
	for k, v := range l.win {
		if len(v) == 0 || now.Sub(v[len(v)-1]) > time.Minute {
			delete(l.win, k)
		}
	}
	for k, v := range l.dg {
		if len(v) == 0 || now.Sub(v[len(v)-1]) > time.Hour {
			delete(l.dg, k)
		}
	}
}
