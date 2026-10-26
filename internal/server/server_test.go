package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pakka/internal/config"
	"pakka/internal/diagnostics"
	"pakka/internal/evidence"
)

func testServer() *Server {
	r := diagnostics.NewRunner(config.Config{CheckTimeout: 5 * time.Second})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(r, 4, log)
}

func TestHealthz(t *testing.T) {
	res := httptest.NewRecorder()
	testServer().Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/healthz", nil))
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
	testServer().Handler().ServeHTTP(res, req)
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
