package server

import (
	"fmt"
	"io"
	"net/http"
)

// maxLogEventBody caps a single journal line ingest.
const maxLogEventBody = 256 << 10

// createLogEvent ingests one journalctl -o json line from the host-side
// shipper and classifies it for Discord alert channels.
func (s *Server) createLogEvent(w http.ResponseWriter, r *http.Request) {
	if s.logWatcher == nil {
		writeError(w, http.StatusServiceUnavailable, "log watching disabled")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxLogEventBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("bad body: %s", err))
		return
	}
	s.logWatcher.Handle(data)
	w.WriteHeader(http.StatusAccepted)
}
