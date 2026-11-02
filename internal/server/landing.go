package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"pakka/internal/news"
)

// landRequest is one game landing: display name, optional 16-hex title id
// (auto-subscribe + game link), optional one-paragraph blurb.
type landRequest struct {
	Name    string `json:"name"`
	TitleID string `json:"title_id"`
	Blurb   string `json:"blurb"`
}

// createLanding runs the game-landing flow: news channel + articles,
// publish to consoles, Discord announcement, staff report.
func (s *Server) createLanding(w http.ResponseWriter, r *http.Request) {
	if s.lander == nil {
		writeError(w, http.StatusServiceUnavailable, "game landing disabled")
		return
	}
	var req landRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("bad request: %s", err))
		return
	}
	res, err := s.lander.Land(r.Context(), news.Landing{Name: req.Name, TitleID: req.TitleID, Blurb: req.Blurb})
	if err != nil {
		var already *news.AlreadyError
		if errors.As(err, &already) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		var failed *news.LandError
		if errors.As(err, &failed) {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error": err.Error(), "step": failed.Step, "completed": failed.Completed,
			})
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
