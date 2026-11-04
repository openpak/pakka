package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"pakka/internal/publisher"
)

type memOV struct {
	mu    sync.Mutex
	posts map[string]publisher.PostRequest
}

func (m *memOV) Post(_ context.Context, p publisher.PostRequest) (publisher.PostResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, seen := m.posts[p.EventID]
	m.posts[p.EventID] = p
	return publisher.PostResult{ID: "p", URL: "https://verse.test/posts/p", Created: !seen}, nil
}
func (m *memOV) EnsureCommunity(context.Context, publisher.CommunityRequest) (publisher.CommunityResult, error) {
	return publisher.CommunityResult{}, nil
}
func (m *memOV) FindCommunity(context.Context, publisher.CommunityRef) (publisher.CommunityResult, bool, error) {
	return publisher.CommunityResult{}, false, nil
}
func (m *memOV) DeletePost(context.Context, string) error { return nil }
func (m *memOV) Top(context.Context, int, int) ([]publisher.TopPost, error) {
	return nil, nil
}

func pubServer(t *testing.T, adminKey string, cats ...string) (http.Handler, *memOV) {
	t.Helper()
	cfg := publisher.Config{Categories: map[string]bool{}, OpenVerseURL: "x", OpenVerseKey: "k", AdminKey: adminKey, StateDir: t.TempDir()}
	for _, c := range cats {
		cfg.Categories[c] = true
	}
	ov := &memOV{posts: map[string]publisher.PostRequest{}}
	svc, err := publisher.New(cfg, ov, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := testServer(nil)
	s.publisher = svc
	return s.Handler(), ov
}

func call(h http.Handler, method, path, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("X-Admin-Key", key)
	}
	req.Header.Set("X-Approver", "tester")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPublisherAPIIsOffWithoutAKeyAndGuardedWithOne(t *testing.T) {
	h, _ := pubServer(t, "")
	if rec := call(h, "GET", "/v1/publisher/status", "anything", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("no key configured: %d", rec.Code)
	}
	h, _ = pubServer(t, "s3cret")
	if rec := call(h, "GET", "/v1/publisher/status", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing key: %d", rec.Code)
	}
	if rec := call(h, "GET", "/v1/publisher/drafts", "wrong", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", rec.Code)
	}
	if rec := call(h, "GET", "/v1/publisher/status", "s3cret", ""); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}
}

func TestEventIntakeAutoPublishesFactsOnly(t *testing.T) {
	h, ov := pubServer(t, "k", publisher.CatSplatfest)
	body := `{"category":"splatfest","template":"result","event_id":"sf-9","community":{"name":"Switch"},"facts":{"event":"A vs B","winner":"A"}}`
	rec := call(h, "POST", "/v1/publisher/events", "k", body)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"published"`) || len(ov.posts) != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	// free text through the event door is not a thing
	bad := `{"category":"splatfest","template":"result","event_id":"sf-10","community":{"name":"Switch"},"facts":{"event":"x","winner":"see https://x.example"}}`
	if rec := call(h, "POST", "/v1/publisher/events", "k", bad); rec.Code != http.StatusBadRequest {
		t.Fatalf("link in facts: %d", rec.Code)
	}
	// a category that is off refuses
	off := `{"category":"tournament","template":"winner","event_id":"t1","community":{"name":"Switch"},"facts":{"tournament":"Cup","winner":"A"}}`
	if rec := call(h, "POST", "/v1/publisher/events", "k", off); rec.Code != http.StatusConflict {
		t.Fatalf("category off: %d", rec.Code)
	}
}

func TestDraftApproveFlowOverHTTP(t *testing.T) {
	h, ov := pubServer(t, "k", publisher.CatAnnounce)
	rec := call(h, "POST", "/v1/publisher/drafts", "k", `{"tool":"openverse.post","category":"announcement","body":"Hello","community":{"name":"Announcements"}}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out struct {
		DraftID string `json:"draft_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(ov.posts) != 0 {
		t.Fatal("published before approval")
	}
	if rec := call(h, "GET", "/v1/publisher/drafts", "k", ""); !strings.Contains(rec.Body.String(), out.DraftID) {
		t.Fatalf("not listed: %s", rec.Body)
	}
	if rec := call(h, "POST", "/v1/publisher/drafts/"+out.DraftID+"/approve", "k", ""); rec.Code != http.StatusOK || len(ov.posts) != 1 {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body)
	}
	if rec := call(h, "POST", "/v1/publisher/drafts/"+out.DraftID+"/approve", "k", ""); rec.Code != http.StatusConflict {
		t.Fatalf("second approve: %d", rec.Code)
	}
	if rec := call(h, "POST", "/v1/publisher/drafts/nope/reject", "k", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown draft: %d", rec.Code)
	}
	if rec := call(h, "GET", "/v1/publisher/audit?n=3", "k", ""); !strings.Contains(rec.Body.String(), "staff:tester") {
		t.Fatalf("audit lacks the approver: %s", rec.Body)
	}
}

func TestKillEndpointStopsPublishing(t *testing.T) {
	h, ov := pubServer(t, "k", publisher.CatSplatfest)
	if rec := call(h, "POST", "/v1/publisher/kill", "k", ""); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	body := `{"category":"splatfest","template":"result","event_id":"sf-1","community":{"name":"Switch"},"facts":{"event":"A","winner":"B"}}`
	if rec := call(h, "POST", "/v1/publisher/events", "k", body); rec.Code != http.StatusServiceUnavailable || len(ov.posts) != 0 {
		t.Fatalf("%d posts=%d", rec.Code, len(ov.posts))
	}
}

func TestDraftEditApproveRejectAndHistoryOverHTTP(t *testing.T) {
	h, ov := pubServer(t, "k", publisher.CatAnnounce)
	mk := func(body string) string {
		rec := call(h, "POST", "/v1/publisher/drafts", "k", `{"tool":"openverse.post","category":"announcement","body":"`+body+`","community":{"name":"Announcements"}}`)
		var out struct {
			DraftID string `json:"draft_id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.DraftID
	}
	a, b := mk("first"), mk("second")
	rec := call(h, "POST", "/v1/publisher/drafts/"+a+"/approve", "k", `{"body":"first, edited"}`)
	if rec.Code != http.StatusOK || len(ov.posts) != 1 {
		t.Fatalf("edited approve: %d %s", rec.Code, rec.Body)
	}
	for _, p := range ov.posts {
		if p.Body != "first, edited" {
			t.Fatalf("published %q", p.Body)
		}
	}
	rec = call(h, "GET", "/v1/publisher/drafts/"+a, "k", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"edited"`) || !strings.Contains(rec.Body.String(), `"original"`) {
		t.Fatalf("history: %d %s", rec.Code, rec.Body)
	}
	if rec := call(h, "POST", "/v1/publisher/drafts/"+b+"/reject", "k", `{"reason":"off topic"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "off topic") {
		t.Fatalf("reject: %d %s", rec.Code, rec.Body)
	}
	if rec := call(h, "GET", "/v1/publisher/drafts/nope", "k", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", rec.Code)
	}
	if rec := call(h, "GET", "/v1/publisher/drafts/"+a, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", rec.Code)
	}
}
