package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pakka/internal/config"
	"pakka/internal/diagnostics"
	"pakka/internal/discord"
	"pakka/internal/evidence"
	"pakka/internal/investigate"
	"pakka/internal/meta"
)

func testServer(model investigate.Model) *Server {
	cfg := config.Config{CheckTimeout: 5 * time.Second, InvestigateSteps: 5}
	return New(Options{
		Runner:       diagnostics.NewRunner(cfg),
		Investigator: investigate.NewInvestigator(cfg),
		Model:        model,
		MaxRuns:      4,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// stubModel replays canned replies for live-mode tests.
type stubModel struct {
	replies []meta.Message
}

func (m *stubModel) Complete(_ context.Context, _ []meta.Message, _ []meta.Tool) (meta.Message, error) {
	if len(m.replies) == 0 {
		return meta.Message{}, errors.New("no more replies")
	}
	r := m.replies[0]
	m.replies = m.replies[1:]
	return r, nil
}

func TestHealthz(t *testing.T) {
	res := httptest.NewRecorder()
	testServer(nil).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", res.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Errorf("body = %v, want status ok", body)
	}
}

func postRun(t *testing.T, payload string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(payload))
	res := httptest.NewRecorder()
	testServer(nil).Handler().ServeHTTP(res, req)
	return res
}

func TestCreateRunFixture(t *testing.T) {
	res := postRun(t, `{"mode":"fixture","scenario":"service-outage"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", res.Code, res.Body.String())
	}
	var run evidence.Run
	if err := json.Unmarshal(res.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.Schema != evidence.Schema {
		t.Errorf("schema = %q", run.Schema)
	}
	if run.Scenario != "service-outage" || run.Mode != "fixture" {
		t.Errorf("run = %s/%s, want service-outage/fixture", run.Scenario, run.Mode)
	}
	if len(run.Results) != len(diagnostics.DefaultSuite) {
		t.Errorf("%d results, want %d", len(run.Results), len(diagnostics.DefaultSuite))
	}
}

func TestCreateRunDefaultScenario(t *testing.T) {
	res := postRun(t, `{"mode":"fixture"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", res.Code, res.Body.String())
	}
	var run evidence.Run
	if err := json.Unmarshal(res.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.Scenario != "all-pass" {
		t.Errorf("scenario = %q, want all-pass", run.Scenario)
	}
}

func TestCreateRunDefaultScenarioLive(t *testing.T) {
	res := postRun(t, `{"mode":"live"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", res.Code, res.Body.String())
	}
	var run evidence.Run
	if err := json.Unmarshal(res.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.Scenario != "live" {
		t.Errorf("scenario = %q, want live", run.Scenario)
	}
}

func TestCreateRunErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"bad json", `{`},
		{"bad mode", `{"mode":"bogus"}`},
		{"unknown scenario", `{"mode":"fixture","scenario":"nope"}`},
	} {
		res := postRun(t, tc.payload)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d, want 400 (%s)", tc.name, res.Code, res.Body.String())
		}
	}
}

func postInvestigation(t *testing.T, srv *Server, payload string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/investigations", strings.NewReader(payload))
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	return res
}

func TestCreateInvestigationFixture(t *testing.T) {
	res := postInvestigation(t, testServer(nil), `{"mode":"fixture","scenario":"local-misconfig","symptoms":"cannot connect"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", res.Code, res.Body.String())
	}
	var body investigationResponse
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Finding.Diagnosis != investigate.DiagnosisLocalFailure {
		t.Errorf("diagnosis = %q", body.Finding.Diagnosis)
	}
	if len(body.Results) != 2 {
		t.Errorf("%d results, want 2", len(body.Results))
	}
}

func TestCreateInvestigationLiveWithoutKey(t *testing.T) {
	res := postInvestigation(t, testServer(nil), `{"mode":"live","symptoms":"x"}`)
	if res.Code != http.StatusServiceUnavailable {
		t.Errorf("code = %d, want 503 (%s)", res.Code, res.Body.String())
	}
}

func TestCreateInvestigationLiveWithModel(t *testing.T) {
	model := &stubModel{replies: []meta.Message{
		{Role: meta.RoleAssistant, ToolCalls: []meta.ToolCall{{ID: "1", Function: meta.FunctionCall{Name: "dns_resolve", Arguments: "{}"}}}},
		{Role: meta.RoleAssistant, Content: `{"diagnosis":"healthy","summary":"ok","evidence":["dns.resolve"],"recommended_next":"none"}`},
	}}
	res := postInvestigation(t, testServer(model), `{"mode":"live","symptoms":"slow"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", res.Code, res.Body.String())
	}
	var body investigationResponse
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Finding.Diagnosis != investigate.DiagnosisHealthy {
		t.Errorf("diagnosis = %q", body.Finding.Diagnosis)
	}
}

func TestCreateInvestigationErrors(t *testing.T) {
	srv := testServer(nil)
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"bad json", `{`},
		{"bad mode", `{"mode":"bogus"}`},
		{"unknown scenario", `{"mode":"fixture","scenario":"nope"}`},
	} {
		res := postInvestigation(t, srv, tc.payload)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d, want 400 (%s)", tc.name, res.Code, res.Body.String())
		}
	}
}

type stubAnnouncer struct {
	posts []string
}

func (s *stubAnnouncer) Announce(channelID, content string) error {
	s.posts = append(s.posts, channelID+":"+content)
	return nil
}

func TestCreateLogEventDisabled(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/log-events", strings.NewReader(`{}`))
	res := httptest.NewRecorder()
	testServer(nil).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Errorf("code = %d, want 503", res.Code)
	}
}

func TestCreateLogEvent(t *testing.T) {
	ann := &stubAnnouncer{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{CheckTimeout: 5 * time.Second, InvestigateSteps: 5}
	srv := New(Options{
		Runner:       diagnostics.NewRunner(cfg),
		Investigator: investigate.NewInvestigator(cfg),
		LogWatcher:   investigateLogWatcher(ann, log),
		MaxRuns:      4,
		Log:          log,
	})
	payload := `{"MESSAGE":"boom","PRIORITY":"3","_SYSTEMD_UNIT":"openpak-x.service","_HOSTNAME":"h"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/log-events", strings.NewReader(payload))
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("code = %d, body = %s", res.Code, res.Body.String())
	}
	if len(ann.posts) != 1 || !strings.HasPrefix(ann.posts[0], "ops1:") {
		t.Errorf("posts = %v", ann.posts)
	}
}

func investigateLogWatcher(ann *stubAnnouncer, log *slog.Logger) *discord.LogWatcher {
	return discord.NewLogWatcher([]string{"openpak-*"}, "mod1", "ops1", ann, time.Hour, log)
}
