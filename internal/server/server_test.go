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
	"pakka/internal/eshop"
	"pakka/internal/evidence"
	"pakka/internal/investigate"
	"pakka/internal/meta"
	"pakka/internal/news"

	"github.com/bwmarrin/discordgo"
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

func TestEshopBrokenDisabled(t *testing.T) {
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/eshop-broken", strings.NewReader(`{"broken":{}}`))
	testServer(nil).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", res.Code)
	}
}

func TestEshopBrokenIngest(t *testing.T) {
	ann := &stubAnnouncer{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(Options{
		Shops:   map[string]*eshop.BrokenTracker{"switch": eshop.NewBrokenTracker(ann, "staff1", time.Hour, nil, log)},
		MaxRuns: 4,
		Log:     log,
	})
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/eshop-broken", strings.NewReader(`{"broken":{"ChunkIntercept":"27/10/2026"}}`))
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", res.Code, res.Body.String())
	}
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/eshop-broken", strings.NewReader(`oops`))
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", res.Code)
	}
}

func TestShopBrokenPerConsole(t *testing.T) {
	sw, wu := &stubAnnouncer{}, &stubAnnouncer{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(Options{
		Shops: map[string]*eshop.BrokenTracker{
			"switch": eshop.NewConsoleBrokenTracker("switch", eshop.ConsoleLabel("switch", ""), sw, "staff1", time.Hour, nil, log),
			"wiiu":   eshop.NewConsoleBrokenTracker("wiiu", eshop.ConsoleLabel("wiiu", "Wii U"), wu, "staff1", time.Hour, nil, log),
		},
		MaxRuns: 4, Log: log,
	})
	post := func(path, body string) int {
		res := httptest.NewRecorder()
		srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return res.Code
	}
	if c := post("/v1/shop/broken", `{"console":"wiiu","broken":{"A":"x"}}`); c != http.StatusOK {
		t.Fatalf("wiiu ingest = %d", c)
	}
	if c := post("/v1/shop/broken", `{"console":"ps9","broken":{}}`); c != http.StatusNotFound {
		t.Fatalf("unknown console = %d, want 404", c)
	}
	if c := post("/v1/shop/broken", `{"broken":{}}`); c != http.StatusNotFound {
		t.Fatalf("missing console = %d, want 404", c)
	}
	// The legacy path always means the Switch, whatever the body says.
	if c := post("/v1/eshop-broken", `{"console":"wiiu","broken":{"B":"y"}}`); c != http.StatusOK {
		t.Fatalf("legacy alias = %d", c)
	}
	if got := srv.shops["switch"].Snapshot(); len(got) != 1 || got["B"].IsZero() {
		t.Fatalf("switch tracker after alias = %v", got)
	}
	if got := srv.shops["wiiu"].Snapshot(); len(got) != 1 || got["A"].IsZero() {
		t.Fatalf("wiiu tracker = %v", got)
	}
}

func TestLandDisabled(t *testing.T) {
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/land", strings.NewReader(`{"name":"X"}`))
	testServer(nil).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", res.Code)
	}
}

func TestLandConflict(t *testing.T) {
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"topic_id":"openpak_x","name":"X"}`))
	}))
	defer admin.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ann := &stubAnnouncer{}
	srv := New(Options{
		Lander:  news.NewLander(news.NewClient(admin.URL, "k", time.Second), ann, ann, "staff1", "public1", log),
		MaxRuns: 4,
		Log:     log,
	})
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/land", strings.NewReader(`{"name":"X"}`))
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("code = %d, body = %s", res.Code, res.Body.String())
	}
	res = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/land", strings.NewReader(`oops`))
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", res.Code)
	}
}

type stubSender struct {
	posts []string
}

func (s *stubSender) ChannelMessageSend(_ string, content string, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	s.posts = append(s.posts, content)
	return &discordgo.Message{Content: content}, nil
}

func TestAnnounceRelay(t *testing.T) {
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/announce", strings.NewReader(`{"channel":"1","text":"hi"}`))
	testServer(nil).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", res.Code)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sender := &stubSender{}
	srv := New(Options{Relay: sender, MaxRuns: 4, Log: log})
	for body, want := range map[string]int{
		`{"channel":"123","text":"deployed v1, healthy"}`:              http.StatusOK,
		`{"channel":"abc","text":"hi"}`:                                http.StatusBadRequest,
		`{"channel":"123","text":""}`:                                  http.StatusBadRequest,
		`{"channel":"123","text":"` + strings.Repeat("x", 2001) + `"}`: http.StatusBadRequest,
		`oops`: http.StatusBadRequest,
	} {
		res := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/announce", strings.NewReader(body))
		srv.Handler().ServeHTTP(res, req)
		if res.Code != want {
			t.Errorf("body %q: code = %d, want %d", body[:min(40, len(body))], res.Code, want)
		}
	}
	if len(sender.posts) != 1 || !strings.Contains(sender.posts[0], "healthy") {
		t.Errorf("relayed = %v", sender.posts)
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
