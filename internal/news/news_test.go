package news

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeAdmin implements the admin endpoints the lander uses.
type fakeAdmin struct {
	channels map[string]bool
	fail     map[string]int // "METHOD path" -> status
	calls    []string
	articles int
}

func (f *fakeAdmin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + r.URL.Path
	f.calls = append(f.calls, key)
	if code, ok := f.fail[key]; ok {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case key == "POST /admin/v1/publish":
		_, _ = w.Write([]byte(`{"build":12,"revision":3,"changed_topics":["openpak_news"]}`))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/admin/v1/channels/"):
		topic := strings.TrimPrefix(r.URL.Path, "/admin/v1/channels/")
		if f.channels[topic] {
			_, _ = w.Write([]byte(`{"topic_id":"` + topic + `","name":"x"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no such channel"}`))
	case key == "POST /admin/v1/channels":
		var req struct {
			TopicID string `json:"topic_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.channels[req.TopicID] = true
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"topic_id":"` + req.TopicID + `","name":"x"}`))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/articles"):
		f.articles++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"news_id":2609290000,"title":"x"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no such route"}`))
	}
}

type fakeAnnouncer struct {
	posts []string
}

func (f *fakeAnnouncer) Announce(_ string, content string) error {
	f.posts = append(f.posts, content)
	return nil
}

func testLander(srv *httptest.Server) (*Lander, *fakeAnnouncer, *fakeAnnouncer) {
	staff, public := &fakeAnnouncer{}, &fakeAnnouncer{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	l := NewLander(NewClient(srv.URL, "k", time.Second), staff, public, "staff1", "public1", log)
	return l, staff, public
}

func TestLandFullFlow(t *testing.T) {
	admin := &fakeAdmin{channels: map[string]bool{}}
	srv := httptest.NewServer(admin)
	defer srv.Close()
	l, staff, public := testLander(srv)
	res, err := l.Land(context.Background(), Landing{Name: "Astroneer", TitleID: "0100abcdef123400", Blurb: "Build bases together."})
	if err != nil {
		t.Fatal(err)
	}
	if res.TopicID != "openpak_astroneer" || res.Build != 12 || len(res.Articles) != 3 {
		t.Fatalf("result = %+v", res)
	}
	if len(public.posts) != 1 || !strings.Contains(public.posts[0], "Astroneer") ||
		!strings.Contains(public.posts[0], "has landed") {
		t.Fatalf("public posts = %v", public.posts)
	}
	if len(staff.posts) != 1 || !strings.Contains(staff.posts[0], "landed") {
		t.Fatalf("staff posts = %v", staff.posts)
	}
	var sawCreate, sawPublish, games, news, welcome bool
	for _, c := range admin.calls {
		sawCreate = sawCreate || c == "POST /admin/v1/channels"
		sawPublish = sawPublish || c == "POST /admin/v1/publish"
		games = games || strings.Contains(c, gamesTopic)
		news = news || strings.Contains(c, newsTopic)
		welcome = welcome || strings.Contains(c, "openpak_astroneer")
	}
	if !sawCreate || !sawPublish || !games || !news || !welcome {
		t.Errorf("admin calls = %v", admin.calls)
	}
}

func TestLandAlreadyLanded(t *testing.T) {
	admin := &fakeAdmin{channels: map[string]bool{"openpak_astroneer": true}}
	srv := httptest.NewServer(admin)
	defer srv.Close()
	l, staff, public := testLander(srv)
	_, err := l.Land(context.Background(), Landing{Name: "Astroneer"})
	var already *AlreadyError
	if !errors.As(err, &already) {
		t.Fatalf("err = %v, want AlreadyError", err)
	}
	if len(staff.posts) != 0 || len(public.posts) != 0 {
		t.Fatalf("relanding posted: staff=%v public=%v", staff.posts, public.posts)
	}
}

func TestLandValidation(t *testing.T) {
	admin := &fakeAdmin{channels: map[string]bool{}}
	srv := httptest.NewServer(admin)
	defer srv.Close()
	l, _, _ := testLander(srv)
	for _, req := range []Landing{
		{Name: ""},
		{Name: "!!!"},
		{Name: "X", TitleID: "bogus"},
		{Name: "X", Blurb: strings.Repeat("b", 1201)},
	} {
		if _, err := l.Land(context.Background(), req); err == nil {
			t.Errorf("accepted %+v", req)
		}
	}
}

func TestLandPartialFailure(t *testing.T) {
	admin := &fakeAdmin{
		channels: map[string]bool{},
		fail:     map[string]int{"POST /admin/v1/channels/openpak_news/articles": 500},
	}
	srv := httptest.NewServer(admin)
	defer srv.Close()
	l, staff, public := testLander(srv)
	_, err := l.Land(context.Background(), Landing{Name: "Astroneer"})
	var failed *LandError
	if !errors.As(err, &failed) {
		t.Fatalf("err = %v, want LandError", err)
	}
	if failed.Step != "article-openpak_news" || len(failed.Completed) != 2 {
		t.Errorf("failure = %+v", failed)
	}
	if len(public.posts) != 0 {
		t.Errorf("failed landing announced: %v", public.posts)
	}
	if len(staff.posts) != 1 || !strings.Contains(staff.posts[0], "FAILED") {
		t.Fatalf("staff posts = %v", staff.posts)
	}
}

func TestTopicSlug(t *testing.T) {
	for name, want := range map[string]string{
		"Astroneer":         "openpak_astroneer",
		"Crash Team Racing": "openpak_crash_team_racing",
		"  Spaced  Out  ":   "openpak_spaced_out",
		"NO MORE HEROES":    "openpak_no_more_heroes",
	} {
		if got, ok := topicSlug(name); !ok || got != want {
			t.Errorf("topicSlug(%q) = %q,%v want %q", name, got, ok, want)
		}
	}
	if _, ok := topicSlug("!!!"); ok {
		t.Error("punctuation-only name yielded a topic")
	}
	if got, ok := topicSlug(strings.Repeat("a", 40)); !ok || len(got) != 31 {
		t.Errorf("long name slug = %q,%v", got, ok)
	}
}

func TestClientErrors(t *testing.T) {
	admin := &fakeAdmin{channels: map[string]bool{}, fail: map[string]int{}}
	srv := httptest.NewServer(admin)
	defer srv.Close()
	c := NewClient(srv.URL, "k", time.Second)
	ctx := context.Background()
	if _, found, err := c.GetChannel(ctx, "openpak_nope"); err != nil || found {
		t.Errorf("missing channel = %v,%v", found, err)
	}
	admin.fail["POST /admin/v1/publish"] = 503
	if _, err := c.Publish(ctx, "x"); err == nil {
		t.Error("failed publish accepted")
	} else {
		var apiErr *Error
		if !errors.As(err, &apiErr) || apiErr.Status != 503 {
			t.Errorf("err = %v", err)
		}
	}
}
