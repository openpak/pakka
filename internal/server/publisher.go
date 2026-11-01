package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pakka/internal/publisher"
)

// registerPublisher mounts the publisher admin API. Every route needs the
// admin key (PAKKA_PUBLISH_ADMIN_KEY); with no key set they answer 404.
// Like the rest of the harness API it is meant for localhost and the
// private network only.
func (s *Server) registerPublisher(mux *http.ServeMux) {
	if s.publisher == nil {
		return
	}
	h := func(f http.HandlerFunc) http.HandlerFunc { return s.pubAuth(f) }
	mux.HandleFunc("GET /v1/publisher/status", h(s.pubStatus))
	mux.HandleFunc("GET /v1/publisher/drafts", h(s.pubDrafts))
	mux.HandleFunc("POST /v1/publisher/drafts", h(s.pubDraftCreate))
	mux.HandleFunc("POST /v1/publisher/drafts/{id}/approve", h(s.pubApprove))
	mux.HandleFunc("POST /v1/publisher/drafts/{id}/reject", h(s.pubReject))
	mux.HandleFunc("POST /v1/publisher/run", h(s.pubRun))
	mux.HandleFunc("POST /v1/publisher/events", h(s.pubEvent))
	mux.HandleFunc("POST /v1/publisher/mirror", h(s.pubMirror))
	mux.HandleFunc("POST /v1/publisher/mirror/run", h(s.pubMirrorRun))
	mux.HandleFunc("POST /v1/publisher/highlights/run", h(s.pubHighlights))
	mux.HandleFunc("POST /v1/publisher/kill", h(s.pubKill))
	mux.HandleFunc("GET /v1/publisher/audit", h(s.pubAudit))
	mux.HandleFunc("DELETE /v1/publisher/posts/{id}", h(s.pubDeletePost))
}

func keyEqual(a, b string) bool {
	x, y := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}

func (s *Server) pubAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := s.publisher.Config().AdminKey
		if key == "" {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		if !keyEqual(r.Header.Get("X-Admin-Key"), key) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

// approver is the person behind the admin key, for the audit log.
func approver(r *http.Request) publisher.Actor {
	name := strings.Map(func(c rune) rune {
		if c < 0x20 || c == 0x7f {
			return -1
		}
		return c
	}, strings.TrimSpace(r.Header.Get("X-Approver")))
	if name == "" {
		name = "admin"
	}
	if len(name) > 60 {
		name = name[:60]
	}
	return publisher.Staff(name)
}

func pubStatusCode(err error) int {
	var api *publisher.APIError
	switch {
	case errors.Is(err, publisher.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, publisher.ErrCategoryDisabled), errors.Is(err, publisher.ErrNotPending):
		return http.StatusConflict
	case errors.Is(err, publisher.ErrKilled), errors.Is(err, publisher.ErrNotConfigured):
		return http.StatusServiceUnavailable
	case errors.Is(err, publisher.ErrBadAction):
		return http.StatusBadRequest
	case errors.Is(err, publisher.ErrNoDraft):
		return http.StatusNotFound
	case errors.As(err, &api):
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}

func pubError(w http.ResponseWriter, err error) { writeError(w, pubStatusCode(err), err.Error()) }

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad request: "+err.Error())
		return false
	}
	return true
}

func (s *Server) pubStatus(w http.ResponseWriter, _ *http.Request) {
	cfg := s.publisher.Config()
	var on []string
	for _, c := range publisher.AllCategories() {
		if cfg.Categories[c] {
			on = append(on, c)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"killed": s.publisher.Killed(), "configured": cfg.Enabled(), "categories_on": on,
		"auto_categories": []string{publisher.CatNewsMirror, publisher.CatSplatfest, publisher.CatTournament, publisher.CatCourse},
		"queue":           s.publisher.Queue().Counts(), "tools": publisher.Registry(),
	})
}

func (s *Server) pubDrafts(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = publisher.StatusPending
	}
	if status == "all" {
		status = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"drafts": s.publisher.Queue().List(status)})
}

// pubDraftCreate queues an action for review as staff (it never publishes).
func (s *Server) pubDraftCreate(w http.ResponseWriter, r *http.Request) {
	var a publisher.Action
	if !decode(w, r, &a) {
		return
	}
	out, err := s.publisher.Submit(r.Context(), approver(r), a, false)
	if err != nil {
		pubError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}

// pubRun publishes an action now, as the staff member named in X-Approver.
func (s *Server) pubRun(w http.ResponseWriter, r *http.Request) {
	var a publisher.Action
	if !decode(w, r, &a) {
		return
	}
	out, err := s.publisher.Submit(r.Context(), approver(r), a, true)
	if err != nil {
		pubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) pubApprove(w http.ResponseWriter, r *http.Request) {
	d, err := s.publisher.Approve(r.Context(), approver(r), r.PathValue("id"))
	if err != nil {
		if d.ID != "" {
			writeJSON(w, pubStatusCode(err), map[string]any{"error": err.Error(), "draft": d})
			return
		}
		pubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) pubReject(w http.ResponseWriter, r *http.Request) {
	d, err := s.publisher.Reject(approver(r), r.PathValue("id"))
	if err != nil {
		pubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// pubEvent is the intake for game servers: a fact and a template, never
// free text. It may auto-publish (Splatfest results, tournament winners,
// featured courses) when the category is on.
func (s *Server) pubEvent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Category  string                 `json:"category"`
		Template  string                 `json:"template"`
		EventID   string                 `json:"event_id"`
		Community publisher.CommunityRef `json:"community"`
		Facts     publisher.Facts        `json:"facts"`
		ImageURL  string                 `json:"image_url"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.EventID == "" {
		writeError(w, http.StatusBadRequest, "event_id is required")
		return
	}
	a := publisher.Action{Tool: publisher.ToolPost, Category: req.Category, Template: req.Template,
		EventID: req.Category + ":" + req.EventID, Community: req.Community, Facts: req.Facts, ImageURL: req.ImageURL}
	out, err := s.publisher.Submit(r.Context(), publisher.System("events"), a, false)
	if err != nil {
		pubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) pubMirror(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Topic  string `json:"topic"`
		NewsID uint32 `json:"news_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	out, err := s.publisher.MirrorOne(r.Context(), req.Topic, req.NewsID)
	if err != nil {
		pubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) pubMirrorRun(w http.ResponseWriter, r *http.Request) {
	n, err := s.publisher.MirrorPass(r.Context())
	if err != nil {
		pubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"posted": n})
}

func (s *Server) pubHighlights(w http.ResponseWriter, r *http.Request) {
	d, err := s.publisher.DraftHighlights(r.Context(), s.writer, time.Now())
	if err != nil {
		if errors.Is(err, publisher.ErrNothingToHighlight) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "nothing to highlight"})
			return
		}
		pubError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, d)
}

func (s *Server) pubKill(w http.ResponseWriter, r *http.Request) {
	s.publisher.Kill(approver(r).Name)
	writeJSON(w, http.StatusOK, map[string]bool{"killed": true})
}

func (s *Server) pubAudit(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 || n > 200 {
		n = 50
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": s.publisher.Audit().Recent(n)})
}

// pubDeletePost removes one of Pakka's own OpenVerse posts (rollback).
func (s *Server) pubDeletePost(w http.ResponseWriter, r *http.Request) {
	if err := s.publisher.DeletePost(r.Context(), approver(r), r.PathValue("id")); err != nil {
		if publisher.IsStatus(err, http.StatusNotFound) {
			writeError(w, http.StatusNotFound, "no such post")
			return
		}
		pubError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
