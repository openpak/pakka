package evidence

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRunJSONShape(t *testing.T) {
	run := Run{
		Schema:   Schema,
		RunID:    "abc123",
		Scenario: "all-pass",
		Mode:     "fixture",
		Started:  time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC),
		Finished: time.Date(2026, 10, 26, 18, 0, 1, 0, time.UTC),
		Results: []Result{{
			CheckID:    "dns.resolve",
			Timestamp:  time.Date(2026, 10, 26, 18, 0, 0, 0, time.UTC),
			Source:     "fixture",
			Outcome:    OutcomePass,
			Summary:    "synthetic: resolve ok",
			Evidence:   map[string]any{"host": "example.invalid"},
			DurationMs: 2,
		}},
	}
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema", "run_id", "scenario", "mode", "started", "finished", "results"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing run key %q in %s", key, data)
		}
	}
	if got["schema"] != Schema {
		t.Errorf("schema = %v, want %q", got["schema"], Schema)
	}
	results, ok := got["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("results = %v, want 1 entry", got["results"])
	}
	res, ok := results[0].(map[string]any)
	if !ok {
		t.Fatalf("result = %T, want object", results[0])
	}
	for _, key := range []string{"check_id", "ts", "source", "outcome", "summary", "evidence", "duration_ms"} {
		if _, ok := res[key]; !ok {
			t.Errorf("missing result key %q in %s", key, data)
		}
	}
}
