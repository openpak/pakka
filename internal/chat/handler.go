package chat

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"pakka/internal/store"
)

// Service is the chat HTTP API.
type Service struct {
	cfg      Config
	store    *Store
	consents *Consents
	limits   *Limiter
	agent    *Agent
	sem      chan struct{}
	busy     sync.Map // conversation id -> struct{}
	log      *slog.Logger
}

// New builds the service. agent.Model nil means replies report unavailable.
func New(cfg Config, agent *Agent, log *slog.Logger) (*Service, error) {
	cfg = cfg.withDefaults()
	ds := cfg.Store
	if ds == nil && cfg.StateDir != "" {
		var err error
		if ds, err = store.OpenFiles(cfg.StateDir); err != nil {
			return nil, err
		}
	}
	st, err := NewStore(ds, cfg.MaxConversations)
	if err != nil {
		return nil, err
	}
	lim := NewLimiter(cfg.BurstPerMinute, cfg.DailyCap, cfg.DiagnosesPerHour)
	agent.Limits = lim
	return &Service{cfg: cfg, store: st, consents: NewConsents(ds), limits: lim, agent: agent,
		sem: make(chan struct{}, cfg.MaxConcurrent), log: log}, nil
}

// Run purges expired conversations and idle limiter state until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n := s.store.Purge(s.cfg.Retention); n > 0 {
			s.log.Info("chat: purged expired conversations", "count", n)
		}
		s.limits.Sweep()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Enabled reports whether a service key is configured.
func (s *Service) Enabled() bool { return s.cfg.Key != "" }

// Mount registers the routes on mux.
func (s *Service) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/chat/state", s.auth(s.state))
	mux.HandleFunc("POST /v1/chat/consent", s.auth(s.accept))
	mux.HandleFunc("DELETE /v1/chat/consent", s.auth(s.withdraw))
	mux.HandleFunc("DELETE /v1/chat/account", s.auth(s.eraseAccount))
	mux.HandleFunc("GET /v1/chat/conversations", s.auth(s.list))
	mux.HandleFunc("POST /v1/chat/conversations", s.auth(s.create))
	mux.HandleFunc("GET /v1/chat/conversations/{id}", s.auth(s.get))
	mux.HandleFunc("DELETE /v1/chat/conversations/{id}", s.auth(s.remove))
	mux.HandleFunc("POST /v1/chat/conversations/{id}/messages", s.auth(s.send))
}

type reqCtx struct {
	account string
	grant   string
}

var accountRe = regexp.MustCompile(`^[A-Za-z0-9._:\-]{1,128}$`)

// auth admits only the website: the service key must match, and the account
// is whatever the website resolved. Nothing else is read as identity.
func (s *Service) auth(next func(http.ResponseWriter, *http.Request, reqCtx)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Key == "" {
			writeErr(w, http.StatusServiceUnavailable, "chat_disabled", "chat is not enabled")
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Key)) != 1 {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
			return
		}
		acct := r.Header.Get("X-Account-Id")
		if !accountRe.MatchString(acct) {
			writeErr(w, http.StatusBadRequest, "bad_request", "missing account")
			return
		}
		next(w, r, reqCtx{account: acct, grant: r.Header.Get("X-Pakka-Grant")})
	}
}

// gate enforces the age and consent rules for anything that sends text to the model.
func (s *Service) gate(w http.ResponseWriter, r *http.Request, rc reqCtx) bool {
	if !s.eligible(r.Context(), rc) {
		writeErr(w, http.StatusForbidden, "not_available", "Pakka isn't available on this account")
		return false
	}
	if !s.consents.Accepted(rc.account) {
		writeErr(w, http.StatusForbidden, "consent_required", "accept the data-use notice first")
		return false
	}
	return true
}

// eligible asks the website, which owns the account's age and supervision
// status, whether this account may use Pakka. Child, supervised and
// unknown-age accounts never may; any failure to determine it refuses.
func (s *Service) eligible(ctx context.Context, rc reqCtx) bool {
	if s.agent.Website == nil || rc.grant == "" {
		return false
	}
	out, err := s.agent.Website.Tool(ctx, rc.grant, "eligibility")
	if err != nil {
		return false
	}
	var v struct {
		Eligible bool `json:"eligible"`
	}
	return json.Unmarshal([]byte(out), &v) == nil && v.Eligible
}

func (s *Service) state(w http.ResponseWriter, r *http.Request, rc reqCtx) {
	ok := s.eligible(r.Context(), rc)
	writeJSON(w, http.StatusOK, map[string]any{
		"available": s.agent.Model != nil, "eligible": ok, "consent": ok && s.consents.Accepted(rc.account),
		"notice_version": NoticeVersion, "retention_days": int(s.cfg.Retention.Hours() / 24),
		"daily_cap": s.cfg.DailyCap, "max_chars": s.cfg.MaxMessageChars,
	})
}

func (s *Service) accept(w http.ResponseWriter, r *http.Request, rc reqCtx) {
	if !s.eligible(r.Context(), rc) {
		writeErr(w, http.StatusForbidden, "not_available", "Pakka isn't available on this account")
		return
	}
	s.consents.Accept(rc.account)
	writeJSON(w, http.StatusOK, map[string]any{"consent": true, "notice_version": NoticeVersion})
}

func (s *Service) withdraw(w http.ResponseWriter, _ *http.Request, rc reqCtx) {
	s.consents.Withdraw(rc.account)
	writeJSON(w, http.StatusOK, map[string]any{"consent": false})
}

// eraseAccount is the website's erasure step: all of the account's
// conversations and its consent record go. Safe to repeat.
func (s *Service) eraseAccount(w http.ResponseWriter, _ *http.Request, rc reqCtx) {
	n := s.store.EraseAccount(rc.account)
	s.consents.Withdraw(rc.account)
	s.limits.Forget(rc.account)
	writeJSON(w, http.StatusOK, map[string]any{"erased": true, "conversations": n})
}

func view(c Conversation) map[string]any {
	return map[string]any{"id": c.ID, "title": c.Title, "created": c.Created, "updated": c.Updated}
}

func (s *Service) list(w http.ResponseWriter, _ *http.Request, rc reqCtx) {
	out := []map[string]any{}
	for _, c := range s.store.List(rc.account) {
		out = append(out, view(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": out})
}

func (s *Service) create(w http.ResponseWriter, r *http.Request, rc reqCtx) {
	if !s.gate(w, r, rc) {
		return
	}
	c, err := s.store.Create(rc.account)
	if errors.Is(err, ErrLimit) {
		writeErr(w, http.StatusConflict, "conversation_limit", "delete an old conversation first")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, view(c))
}

func (s *Service) get(w http.ResponseWriter, r *http.Request, rc reqCtx) {
	c, err := s.store.Get(rc.account, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "conversation not found")
		return
	}
	v := view(c)
	v["messages"] = c.Messages
	writeJSON(w, http.StatusOK, v)
}

func (s *Service) remove(w http.ResponseWriter, r *http.Request, rc reqCtx) {
	if err := s.store.Delete(rc.account, r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "conversation not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// send stores the user's message and streams the reply as server-sent events:
// "tool" (a lookup started), "delta" (reply text), "done", or "error".
func (s *Service) send(w http.ResponseWriter, r *http.Request, rc reqCtx) {
	if !s.gate(w, r, rc) {
		return
	}
	id := r.PathValue("id")
	if _, err := s.store.Get(rc.account, id); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "conversation not found")
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "bad request")
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" || utf8.RuneCountInString(text) > s.cfg.MaxMessageChars {
		writeErr(w, http.StatusBadRequest, "bad_message", "message is empty or too long (max "+strconv.Itoa(s.cfg.MaxMessageChars)+" characters)")
		return
	}
	if s.agent.Model == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "Pakka is not available right now")
		return
	}
	if _, loaded := s.busy.LoadOrStore(id, struct{}{}); loaded {
		writeErr(w, http.StatusConflict, "busy", "Pakka is still answering the previous message")
		return
	}
	defer s.busy.Delete(id)
	if reason, retry := s.limits.AllowMessage(rc.account); reason != "" {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		code := "rate_limited"
		if reason == LimitDaily {
			code = "daily_cap"
		}
		writeErr(w, http.StatusTooManyRequests, code, "message limit reached")
		return
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		writeErr(w, http.StatusServiceUnavailable, "busy", "Pakka is busy; try again in a moment")
		return
	}
	if err := s.store.Append(rc.account, id, Message{Role: RoleUser, Text: text}); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "conversation not found")
		return
	}
	conv, _ := s.store.Get(rc.account, id)

	rcn := http.NewResponseController(w)
	_ = rcn.SetWriteDeadline(time.Now().Add(2 * time.Minute))
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	emit := func(event string, v any) {
		data, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		_ = rcn.Flush()
	}
	emit("start", map[string]string{"conversation": id})

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	reply, err := s.agent.Reply(ctx, Scope{Account: rc.account, Grant: rc.grant}, conv.Messages, func(tool string) {
		emit("tool", map[string]string{"name": tool})
	})
	if err != nil {
		s.log.Warn("chat: reply failed", "acct", accountKey(rc.account), "err", err)
		emit("error", map[string]string{"code": "model_error"})
		return
	}
	for _, chunk := range chunks(reply, 48) {
		emit("delta", map[string]string{"text": chunk})
		time.Sleep(15 * time.Millisecond)
	}
	m := Message{ID: newID(), Role: RoleAssistant, Text: reply}
	_ = s.store.Append(rc.account, id, m)
	emit("done", map[string]string{"message_id": m.ID})
}

// chunks splits text into pieces of about n runes at word boundaries.
func chunks(s string, n int) []string {
	var out []string
	cur := ""
	for _, f := range strings.SplitAfter(s, " ") {
		if utf8.RuneCountInString(cur)+utf8.RuneCountInString(f) > n && cur != "" {
			out = append(out, cur)
			cur = ""
		}
		cur += f
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}
