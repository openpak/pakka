// Package diagnostics runs bounded, typed checks and returns structured
// evidence (schema pakka.evidence/v0). Checks are read-only; fixtures are
// synthetic and embedded. See docs/m1-proposal.md.
package diagnostics

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"pakka/internal/config"
	"pakka/internal/evidence"
)

// Mode selects fixture playback or live checks.
type Mode string

// Run modes.
const (
	ModeFixture Mode = "fixture"
	ModeLive    Mode = "live"
)

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool { return m == ModeFixture || m == ModeLive }

// Check is one diagnostic. Implementations must honor ctx (bounded),
// stay read-only, and return pre-redacted evidence.
type Check interface {
	ID() string
	Run(ctx context.Context) evidence.Result
}

// DefaultSuite is the ordered check set for a diagnostic run.
var DefaultSuite = []string{"dns.resolve", "tcp.connect", "http.health"}

// Runner executes suites in fixture or live mode.
type Runner struct {
	timeout time.Duration
	live    map[string]func() Check
}

// NewRunner binds live checks to configured targets.
func NewRunner(cfg config.Config) *Runner {
	timeout := cfg.CheckTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Runner{
		timeout: timeout,
		live: map[string]func() Check{
			"dns.resolve": func() Check { return NewDNSCheck(cfg.DNSHost) },
			"tcp.connect": func() Check { return NewTCPCheck(cfg.TCPAddr) },
			"http.health": func() Check { return NewHTTPCheck(cfg.HealthURL) },
		},
	}
}

// Run executes suite (DefaultSuite when empty) and returns the run. Unknown
// scenarios fail the run; unknown check IDs report per-check errors so the
// run still completes deterministically.
func (r *Runner) Run(ctx context.Context, mode Mode, scenario string, suite []string) (evidence.Run, error) {
	if !mode.Valid() {
		return evidence.Run{}, fmt.Errorf("unknown mode %q", mode)
	}
	if len(suite) == 0 {
		suite = DefaultSuite
	}
	var fix fixtureFile
	if mode == ModeFixture {
		var err error
		fix, err = loadFixture(scenario)
		if err != nil {
			return evidence.Run{}, err
		}
	}
	started := time.Now().UTC()
	results := make([]evidence.Result, 0, len(suite))
	for _, id := range suite {
		if mode == ModeFixture {
			results = append(results, runFixture(fix, id))
			continue
		}
		checkCtx, cancel := context.WithTimeout(ctx, r.timeout)
		results = append(results, r.runLive(checkCtx, id))
		cancel()
	}
	return evidence.Run{
		Schema:   evidence.Schema,
		RunID:    newRunID(),
		Scenario: scenario,
		Mode:     string(mode),
		Started:  started,
		Finished: time.Now().UTC(),
		Results:  results,
	}, nil
}

func (r *Runner) runLive(ctx context.Context, id string) evidence.Result {
	mk, ok := r.live[id]
	if !ok {
		return evidence.Result{
			CheckID:   id,
			Timestamp: time.Now().UTC(),
			Source:    checkSource,
			Outcome:   evidence.OutcomeError,
			Summary:   fmt.Sprintf("unknown check %q", id),
			Evidence:  map[string]any{},
		}
	}
	return mk().Run(ctx)
}

func newRunID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("ts-%d", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
