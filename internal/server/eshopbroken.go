package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// brokenReport is one sync's broken set: package name to failure context
// (the 4TU date or gh:tag it failed at). Console names the shop; the
// legacy /v1/eshop-broken path carries none and means the Switch.
type brokenReport struct {
	Console string            `json:"console"`
	Broken  map[string]string `json:"broken"`
}

// createEshopBroken is the legacy path: POST /v1/eshop-broken is an alias for
// POST /v1/shop/broken with console=switch, so run-hb.sh keeps working.
func (s *Server) createEshopBroken(w http.ResponseWriter, r *http.Request) {
	s.ingestBroken(w, r, "switch")
}

// createShopBroken ingests a console's broken-package set: {"console": "...", "broken": {...}}.
func (s *Server) createShopBroken(w http.ResponseWriter, r *http.Request) {
	s.ingestBroken(w, r, "")
}

// ingestBroken ingests the sync's broken-package set from a shipper for a console's broken
// watch. force names the console when the path implies one; otherwise the body's console decides.
func (s *Server) ingestBroken(w http.ResponseWriter, r *http.Request, force string) {
	var req brokenReport
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(&req); err != nil {
		if len(s.shops) == 0 {
			writeError(w, http.StatusServiceUnavailable, "shop broken watch disabled")
			return
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("bad request: %s", err))
		return
	}
	console := force
	if console == "" {
		console = req.Console
	}
	if len(s.shops) == 0 {
		writeError(w, http.StatusServiceUnavailable, "shop broken watch disabled")
		return
	}
	t, ok := s.shops[console]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown shop console %q", console))
		return
	}
	if req.Broken == nil {
		req.Broken = map[string]string{}
	}
	t.Report(req.Broken)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "console": console})
}
