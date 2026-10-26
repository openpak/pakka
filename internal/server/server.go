// Package server exposes the harness over HTTP: health probes plus a run
// endpoint that executes bounded diagnostic suites.
package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"pakka/internal/diagnostics"
)

// maxBody caps run requests; targets never come from the body.
const maxBody = 64 << 10

// Server is the harness HTTP API.
type Server struct {
	runner *diagnostics.Runner
	sem    chan struct{}
	log    *slog.Logger
}

// New builds the server. maxRuns bounds concurrent diagnostic runs (>=1).
func New(runner *diagnostics.Runner, maxRuns int, log *slog.Logger) *Server {
	if maxRuns < 1 {
		maxRuns = 1
	}
	return &Server{runner: runner, sem: make(chan struct{}, maxRuns), log: log}
}

// Handler returns the routed handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.healthz)
	mux.HandleFunc("POST /v1/runs", s.createRun)
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
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-r.Context().Done():
		writeError(w, http.StatusServiceUnavailable, "server busy")
		return
	}
	run, err := s.runner.Run(r.Context(), mode, scenario, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("run finished", "run_id", run.RunID, "mode", run.Mode, "scenario", run.Scenario)
	writeJSON(w, http.StatusOK, run)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
