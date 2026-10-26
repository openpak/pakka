package diagnostics

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"pakka/internal/config"
	"pakka/internal/evidence"
)

func testRunner() *Runner {
	return NewRunner(config.Config{CheckTimeout: 5 * time.Second})
}

func TestFixtureDeterministic(t *testing.T) {
	r := testRunner()
	ctx := context.Background()
	first, err := r.Run(ctx, ModeFixture, "local-misconfig", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Run(ctx, ModeFixture, "local-misconfig", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Results) != len(second.Results) {
		t.Fatalf("result counts differ: %d vs %d", len(first.Results), len(second.Results))
	}
	// Timestamps and run IDs vary per run; everything else must be stable.
	for i := range first.Results {
		a, b := first.Results[i], second.Results[i]
		if a.CheckID != b.CheckID || a.Outcome != b.Outcome || a.Summary != b.Summary ||
			a.Source != b.Source || a.DurationMs != b.DurationMs ||
			!reflect.DeepEqual(a.Evidence, b.Evidence) {
			t.Errorf("run %d differs:\n%+v\n%+v", i, a, b)
		}
	}
}

func TestFixtureScenarioOutcomes(t *testing.T) {
	want := map[string]map[string]evidence.Outcome{
		"all-pass": {
			"dns.resolve": evidence.OutcomePass,
			"tcp.connect": evidence.OutcomePass,
			"http.health": evidence.OutcomePass,
		},
		"local-misconfig": {
			"dns.resolve": evidence.OutcomeFail,
			"tcp.connect": evidence.OutcomeFail,
			"http.health": evidence.OutcomeSkipped,
		},
		"service-outage": {
			"dns.resolve": evidence.OutcomePass,
			"tcp.connect": evidence.OutcomePass,
			"http.health": evidence.OutcomeFail,
		},
	}
	r := testRunner()
	for scenario, outcomes := range want {
		run, err := r.Run(context.Background(), ModeFixture, scenario, nil)
		if err != nil {
			t.Errorf("%s: %s", scenario, err)
			continue
		}
		if run.Schema != evidence.Schema {
			t.Errorf("%s: schema = %q", scenario, run.Schema)
		}
		got := map[string]evidence.Outcome{}
		for _, res := range run.Results {
			got[res.CheckID] = res.Outcome
		}
		if !reflect.DeepEqual(got, outcomes) {
			t.Errorf("%s: outcomes = %v, want %v", scenario, got, outcomes)
		}
	}
}

func TestFixtureBadScenario(t *testing.T) {
	r := testRunner()
	for _, scenario := range []string{"nope", "", "../x", "a/b", strings.Repeat("a", 65)} {
		if _, err := r.Run(context.Background(), ModeFixture, scenario, nil); err == nil {
			t.Errorf("scenario %q: want error, got nil", scenario)
		}
	}
}

func TestUnknownMode(t *testing.T) {
	if _, err := testRunner().Run(context.Background(), "bogus", "all-pass", nil); err == nil {
		t.Error("want error for unknown mode, got nil")
	}
}

func TestUnknownCheckID(t *testing.T) {
	run, err := testRunner().Run(context.Background(), ModeLive, "live", []string{"nope"})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Results) != 1 || run.Results[0].Outcome != evidence.OutcomeError {
		t.Errorf("results = %+v, want one error result", run.Results)
	}
}

func TestLiveDNSLocalhost(t *testing.T) {
	res := NewDNSCheck("localhost").Run(context.Background())
	if res.Outcome != evidence.OutcomePass {
		t.Errorf("outcome = %q (%s), want pass", res.Outcome, res.Summary)
	}
	if res.Evidence["host"] != "localhost" {
		t.Errorf("evidence = %v, want host key", res.Evidence)
	}
}

func TestLiveTCPConnect(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if res := NewTCPCheck(addr).Run(context.Background()); res.Outcome != evidence.OutcomePass {
		t.Errorf("open port: outcome = %q (%s), want pass", res.Outcome, res.Summary)
	}
	_ = ln.Close()
	if res := NewTCPCheck(addr).Run(context.Background()); res.Outcome != evidence.OutcomeFail {
		t.Errorf("closed port: outcome = %q (%s), want fail", res.Outcome, res.Summary)
	}
}

func TestLiveHTTPHealth(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	if res := NewHTTPCheck(ok.URL).Run(context.Background()); res.Outcome != evidence.OutcomePass {
		t.Errorf("200: outcome = %q (%s), want pass", res.Outcome, res.Summary)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	if res := NewHTTPCheck(bad.URL).Run(context.Background()); res.Outcome != evidence.OutcomeFail {
		t.Errorf("500: outcome = %q (%s), want fail", res.Outcome, res.Summary)
	}
	if res := NewHTTPCheck("://bad").Run(context.Background()); res.Outcome != evidence.OutcomeError {
		t.Errorf("bad URL: outcome = %q (%s), want error", res.Outcome, res.Summary)
	}
}

func TestLiveEmptyTargetsSkipped(t *testing.T) {
	ctx := context.Background()
	for id, res := range map[string]evidence.Result{
		"dns":  NewDNSCheck("").Run(ctx),
		"tcp":  NewTCPCheck("").Run(ctx),
		"http": NewHTTPCheck("").Run(ctx),
	} {
		if res.Outcome != evidence.OutcomeSkipped {
			t.Errorf("%s: outcome = %q (%s), want skipped", id, res.Outcome, res.Summary)
		}
	}
}
