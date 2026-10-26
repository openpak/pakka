package diagnostics

import (
	"embed"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"pakka/internal/evidence"
)

// FixtureSchema versions the embedded synthetic fixtures.
const FixtureSchema = "pakka.fixture/v0"

//go:embed fixtures/*.json
var fixtureFS embed.FS

type fixtureFile struct {
	Schema   string          `json:"schema"`
	Scenario string          `json:"scenario"`
	Results  []fixtureResult `json:"results"`
}

type fixtureResult struct {
	CheckID    string           `json:"check_id"`
	Outcome    evidence.Outcome `json:"outcome"`
	Summary    string           `json:"summary"`
	Evidence   map[string]any   `json:"evidence"`
	DurationMs int64            `json:"duration_ms"`
}

var scenarioPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

func loadFixture(scenario string) (fixtureFile, error) {
	var fix fixtureFile
	if !scenarioPattern.MatchString(scenario) {
		return fix, fmt.Errorf("invalid scenario %q", scenario)
	}
	data, err := fixtureFS.ReadFile("fixtures/" + scenario + ".json")
	if err != nil {
		return fix, fmt.Errorf("unknown scenario %q", scenario)
	}
	if err := json.Unmarshal(data, &fix); err != nil {
		return fix, fmt.Errorf("bad fixture %q: %w", scenario, err)
	}
	if fix.Schema != FixtureSchema {
		return fix, fmt.Errorf("fixture %q has schema %q, want %q", scenario, fix.Schema, FixtureSchema)
	}
	return fix, nil
}

// runFixture renders one fixture result, stamped with the collection time.
// IDs absent from the fixture report skipped so fixtures stay minimal.
func runFixture(fix fixtureFile, id string) evidence.Result {
	now := time.Now().UTC()
	for _, fr := range fix.Results {
		if fr.CheckID != id {
			continue
		}
		ev := fr.Evidence
		if ev == nil {
			ev = map[string]any{}
		}
		return evidence.Result{
			CheckID:    id,
			Timestamp:  now,
			Source:     "fixture",
			Outcome:    fr.Outcome,
			Summary:    fr.Summary,
			Evidence:   ev,
			DurationMs: fr.DurationMs,
		}
	}
	return evidence.Result{
		CheckID:   id,
		Timestamp: now,
		Source:    "fixture",
		Outcome:   evidence.OutcomeSkipped,
		Summary:   fmt.Sprintf("check %q not in fixture %q", id, fix.Scenario),
		Evidence:  map[string]any{},
	}
}
