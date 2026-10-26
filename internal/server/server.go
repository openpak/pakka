// Package server exposes the harness over HTTP: health probes, diagnostic
// runs, model-guided investigations, and journal log intake.
package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"pakka/internal/diagnostics"
	"pakka/internal/discord"
	"pakka/internal/investigate"
)

// maxBody caps run requests; targets never come from the body.
const maxBody = 64 << 10

// Options wires the server dependencies. Model may be nil when no API key
// is configured; live investigations then report unavailable. LogWatcher
// may be nil; log intake then reports disabled.
type Options struct {
	Runner       *diagnostics.Runner
	Investigator *investigate.Investigator
	Model        investigate.Model
	LogWatcher   *discord.LogWatcher
	MaxRuns      int
	Log          *slog.Logger
}

// Server is the harness HTTP API.
type Server struct {
	runner       *diagnostics.Runner
	investigator *investigate.Investigator
	model        investigate.Model
	logWatcher   *discord.LogWatcher
	sem          chan struct{}
	log          *slog.Logger
}

// New builds the server.
func New(opts Options) *Server {
	maxRuns := opts.MaxRuns
	if maxRuns < 1 {
		maxRuns = 1
	}
	return &Server{
		runner:       opts.Runner,
		investigator: opts.Investigator,
		model:        opts.Model,
		logWatcher:   opts.LogWatcher,
		sem:          make(chan struct{}, maxRuns),
		log:          opts.Log,
	}
}

// Handler returns the routed handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.healthz)
	mux.HandleFunc("POST /v1/runs", s.createRun)
	mux.HandleFunc("POST /v1/investigations", s.createInvestigation)
	mux.HandleFunc("POST /v1/log-events", s.createLogEvent)
	return mux
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type runRequest struct {
	Mode     string `json:"mode"`
	Scenario string `json:"scenario"`
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	var req runRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("bad request: %s", err))
		return
	}
	mode := diagnostics.Mode(req.Mode)
	if !mode.Valid() {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown mode %q (want fixture|live)", req.Mode))
		return
	}
	scenario := req.Scenario
	if scenario == "" {
		scenario = "all-pass"
	}
	if !s.acquire(r) {
		writeError(w, http.StatusServiceUnavailable, "server busy")
		return
	}
	defer s.release()
	run, err := s.runner.Run(r.Context(), mode, scenario, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("run finished", "run_id", run.RunID, "mode", run.Mode, "scenario", run.Scenario)
	writeJSON(w, http.StatusOK, run)
}

type investigationRequest struct {
	Mode     string `json:"mode"`
	Scenario string `json:"scenario"`
	Symptoms string `json:"symptoms"`
}

type investigationResponse struct {
	Finding investigate.Finding `json:"finding"`
	Results []resultView        `json:"results"`
}

type resultView struct {
	CheckID string `json:"check_id"`
	Outcome string `json:"outcome"`
	Summary string `json:"summary"`
}

func (s *Server) createInvestigation(w http.ResponseWriter, r *http.Request) {
	var req investigationRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("bad request: %s", err))
		return
	}
	mode := diagnostics.Mode(req.Mode)
	if !mode.Valid() {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown mode %q (want fixture|live)", req.Mode))
		return
	}
	symptoms := req.Symptoms
	if symptoms == "" {
		symptoms = "unspecified connection problem"
	}
	var model investigate.Model
	switch mode {
	case diagnostics.ModeFixture:
		scenario := req.Scenario
		if scenario == "" {
			scenario = "all-pass"
		}
		script, ok := investigate.ScriptFor(scenario)
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown scenario %q", scenario))
			return
		}
		model = script
	case diagnostics.ModeLive:
		if s.model == nil {
			writeError(w, http.StatusServiceUnavailable, "meta API key not configured")
			return
		}
		model = s.model
	}
	if !s.acquire(r) {
		writeError(w, http.StatusServiceUnavailable, "server busy")
		return
	}
	defer s.release()
	finding, results, err := s.investigator.Run(r.Context(), model, symptoms)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("investigation failed: %s", err))
		return
	}
	s.log.Info("investigation finished", "diagnosis", finding.Diagnosis, "mode", req.Mode)
	resp := investigationResponse{Finding: finding}
	for _, res := range results {
		resp.Results = append(resp.Results, resultView{CheckID: res.CheckID, Outcome: string(res.Outcome), Summary: res.Summary})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) acquire(r *http.Request) bool {
	select {
	case s.sem <- struct{}{}:
		return true
	case <-r.Context().Done():
		return false
	}
}

func (s *Server) release() { <-s.sem }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
