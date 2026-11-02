package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// announceRequest is one staff/operator post to Discord: numeric channel
// id plus text. Deploy scripts use this to confirm rollouts in #alerts.
type announceRequest struct {
	Channel string `json:"channel"`
	Text    string `json:"text"`
}

// createAnnounce relays one operator post to Discord. The endpoint is
// localhost-only like the rest of the API; redaction still applies, and
// oversize text is rejected rather than truncated.
func (s *Server) createAnnounce(w http.ResponseWriter, r *http.Request) {
	if s.relay == nil {
		writeError(w, http.StatusServiceUnavailable, "announce relay disabled")
		return
	}
	var req announceRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("bad request: %s", err))
		return
	}
	req.Channel = strings.TrimSpace(req.Channel)
	if req.Channel == "" || !isSnowflake(req.Channel) {
		writeError(w, http.StatusBadRequest, "channel must be a numeric channel id")
		return
	}
	if strings.TrimSpace(req.Text) == "" || len(req.Text) > 2000 {
		writeError(w, http.StatusBadRequest, "text must be 1..2000 chars")
		return
	}
	if _, err := s.relay.ChannelMessageSend(req.Channel, req.Text); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("discord send failed: %s", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func isSnowflake(id string) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
