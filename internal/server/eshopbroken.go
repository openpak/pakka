package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// brokenReport is one sync's broken set: package name to failure context
// (the 4TU date or gh:tag it failed at).
type brokenReport struct {
	Broken map[string]string `json:"broken"`
}

// createEshopBroken ingests the sync's broken-package set from the
// workstation shipper for the eShop broken watch.
func (s *Server) createEshopBroken(w http.ResponseWriter, r *http.Request) {
	if s.broken == nil {
		writeError(w, http.StatusServiceUnavailable, "eshop broken watch disabled")
		return
	}
	var req brokenReport
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("bad request: %s", err))
		return
	}
	if req.Broken == nil {
		req.Broken = map[string]string{}
	}
	s.broken.Report(req.Broken)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
