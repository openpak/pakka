// Package evidence defines Pakka's evidence schema v0: one structured result
// per diagnostic check plus a run envelope. See docs/m1-proposal.md.
package evidence

import "time"

// Schema versions the evidence wire format.
const Schema = "pakka.evidence/v0"

// Outcome is the result of a single check.
type Outcome string

// Check outcomes.
const (
	OutcomePass    Outcome = "pass"
	OutcomeFail    Outcome = "fail"
	OutcomeError   Outcome = "error"
	OutcomeSkipped Outcome = "skipped"
)

// Result is one check's structured outcome. Evidence must already be
// redacted at collection; see docs/architecture.md boundaries.
type Result struct {
	CheckID    string         `json:"check_id"`
	Timestamp  time.Time      `json:"ts"`
	Source     string         `json:"source"`
	Outcome    Outcome        `json:"outcome"`
	Summary    string         `json:"summary"`
	Evidence   map[string]any `json:"evidence"`
	DurationMs int64          `json:"duration_ms"`
}

// Run is one diagnostic run: a scenario label plus ordered results.
type Run struct {
	Schema   string    `json:"schema"`
	RunID    string    `json:"run_id"`
	Scenario string    `json:"scenario"`
	Mode     string    `json:"mode"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Results  []Result  `json:"results"`
}
