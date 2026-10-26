package investigate

import (
	"context"
	"errors"
	"testing"
	"time"

	"pakka/internal/diagnostics"
	"pakka/internal/evidence"
	"pakka/internal/meta"
)

type stubCheck struct {
	id  string
	out evidence.Outcome
}

func (s stubCheck) ID() string { return s.id }

func (s stubCheck) Run(_ context.Context) evidence.Result {
	return evidence.Result{
		CheckID: s.id, Source: "stub", Outcome: s.out,
		Summary: "stub", Evidence: map[string]any{},
	}
}

func stubInvestigator(steps int) *Investigator {
	return &Investigator{
		checks: map[string]diagnostics.Check{
			"dns.resolve": stubCheck{id: "dns.resolve", out: evidence.OutcomePass},
			"tcp.connect": stubCheck{id: "tcp.connect", out: evidence.OutcomeFail},
			"http.health": stubCheck{id: "http.health", out: evidence.OutcomePass},
		},
		maxSteps: steps,
		timeout:  time.Second,
	}
}

type fakeModel struct {
	replies []meta.Message
}

func (f *fakeModel) Complete(_ context.Context, _ []meta.Message, _ []meta.Tool) (meta.Message, error) {
	if len(f.replies) == 0 {
		return meta.Message{}, errors.New("no more replies")
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	return r, nil
}

func TestRunToolThenFinding(t *testing.T) {
	in := stubInvestigator(5)
	model := &fakeModel{replies: []meta.Message{
		toolReply("1", "dns_resolve"),
		finalReply(`{"diagnosis":"local-failure","summary":"dns broken","evidence":["dns.resolve"],"recommended_next":"fix dns"}`),
	}}
	finding, results, err := in.Run(context.Background(), model, "cannot connect")
	if err != nil {
		t.Fatal(err)
	}
	if finding.Diagnosis != DiagnosisLocalFailure {
		t.Errorf("diagnosis = %q", finding.Diagnosis)
	}
	if finding.RecommendedNext != "fix dns" {
		t.Errorf("recommended_next = %q", finding.RecommendedNext)
	}
	if len(results) != 1 || results[0].CheckID != "dns.resolve" {
		t.Errorf("results = %+v", results)
	}
}

func TestRunUnknownTool(t *testing.T) {
	in := stubInvestigator(5)
	model := &fakeModel{replies: []meta.Message{
		toolReply("1", "bogus"),
		finalReply(`{"diagnosis":"insufficient-evidence","summary":"no tools ran","evidence":[]}`),
	}}
	finding, results, err := in.Run(context.Background(), model, "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("results = %+v, want none", results)
	}
	if finding.Diagnosis != DiagnosisInsufficient {
		t.Errorf("diagnosis = %q", finding.Diagnosis)
	}
}

func TestRunBadCitationDowngrades(t *testing.T) {
	in := stubInvestigator(5)
	model := &fakeModel{replies: []meta.Message{
		toolReply("1", "dns_resolve"),
		finalReply(`{"diagnosis":"service-outage","summary":"outage","evidence":["http.health"]}`),
	}}
	finding, _, err := in.Run(context.Background(), model, "x")
	if err != nil {
		t.Fatal(err)
	}
	if finding.Diagnosis != DiagnosisInsufficient {
		t.Errorf("diagnosis = %q, want insufficient-evidence", finding.Diagnosis)
	}
}

func TestRunGarbageFinalDowngrades(t *testing.T) {
	in := stubInvestigator(5)
	model := &fakeModel{replies: []meta.Message{
		toolReply("1", "dns_resolve"),
		{Role: meta.RoleAssistant, Content: "not json"},
	}}
	finding, _, err := in.Run(context.Background(), model, "x")
	if err != nil {
		t.Fatal(err)
	}
	if finding.Diagnosis != DiagnosisInsufficient {
		t.Errorf("diagnosis = %q, want insufficient-evidence", finding.Diagnosis)
	}
}

func TestRunExhaustion(t *testing.T) {
	in := stubInvestigator(2)
	model := &fakeModel{replies: []meta.Message{
		toolReply("1", "dns_resolve"),
		toolReply("2", "tcp_connect"),
		toolReply("3", "http_health"),
	}}
	finding, results, err := in.Run(context.Background(), model, "x")
	if err != nil {
		t.Fatal(err)
	}
	if finding.Diagnosis != DiagnosisInsufficient {
		t.Errorf("diagnosis = %q, want insufficient-evidence", finding.Diagnosis)
	}
	if len(results) != 2 {
		t.Errorf("%d results, want 2 (step bound)", len(results))
	}
}

func TestScripts(t *testing.T) {
	want := map[string]Diagnosis{
		"local-misconfig": DiagnosisLocalFailure,
		"service-outage":  DiagnosisOutage,
		"all-pass":        DiagnosisHealthy,
	}
	for scenario, diagnosis := range want {
		script, ok := ScriptFor(scenario)
		if !ok {
			t.Errorf("%s: no script", scenario)
			continue
		}
		finding, results, err := stubInvestigator(5).Run(context.Background(), script, "fixture")
		if err != nil {
			t.Errorf("%s: %s", scenario, err)
			continue
		}
		if finding.Diagnosis != diagnosis {
			t.Errorf("%s: diagnosis = %q, want %q", scenario, finding.Diagnosis, diagnosis)
		}
		if len(results) == 0 || len(finding.Evidence) == 0 {
			t.Errorf("%s: missing results/evidence", scenario)
		}
	}
	if _, ok := ScriptFor("nope"); ok {
		t.Error("ScriptFor(nope) = true, want false")
	}
}
