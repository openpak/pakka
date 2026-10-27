package eshop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStoreClientTitles(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Internal-Key")
		if r.URL.Path != "/internal/store/titles" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"count":2,"titles":[
{"title_id":"01fef84c9c71f000","published":true,"version":65536,"version_label":"3.2.1",
 "revised_at":1790000000,"changelog":"fixes","distribution":{"source":"https://github.com/mtheall/ftpd/releases/tag/v3.2.1"},
 "localized":{"en-US":{"name":"ftpd"}},"default_lang":"en-US"},
{"title_id":"01fed0479e03e000","published":false,"version":0,"version_label":"",
 "distribution":{"source":""},"localized":{"ja":{"name":"x"}},"default_lang":"ja"}]}`))
	}))
	defer srv.Close()
	c := NewStoreClient(srv.URL, "sekret", time.Second)
	titles, err := c.Titles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "sekret" {
		t.Errorf("internal key = %q", gotKey)
	}
	if len(titles) != 2 || titles[0].Name != "ftpd" || !titles[0].Published || titles[1].Published {
		t.Fatalf("titles = %+v", titles)
	}
	if titles[1].Name != "x" {
		t.Errorf("fallback name = %q", titles[1].Name)
	}
}

func TestStoreClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := NewStoreClient(srv.URL, "bad", time.Second).Titles(context.Background()); err == nil {
		t.Error("401 accepted")
	}
}

func TestGitHubClientLatest(t *testing.T) {
	var gotAuth, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotUA = r.Header.Get("Authorization"), r.Header.Get("User-Agent")
		switch r.URL.Path {
		case "/repos/o/r/releases/latest":
			w.Write([]byte(`{"tag_name":"v2","published_at":"2026-10-20T00:00:00Z","html_url":"https://github.com/o/r/releases/tag/v2","body":"notes"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := NewGitHubClient("tok", time.Second)
	// Point at the test server by swapping the client's transport target is
	// overkill; instead exercise fetch directly against the test URL shape.
	rel, ok, _, err := c.fetch(context.Background(), srv.URL+"/repos/o/r/releases/latest")
	if err != nil || !ok || rel.Tag != "v2" || rel.Body != "notes" {
		t.Fatalf("rel=%+v ok=%v err=%v", rel, ok, err)
	}
	if gotAuth != "Bearer tok" || gotUA == "" {
		t.Errorf("auth=%q ua=%q", gotAuth, gotUA)
	}
	_, ok, _, err = c.fetch(context.Background(), srv.URL+"/repos/o/none/releases/latest")
	if err != nil || ok {
		t.Errorf("404 = ok=%v err=%v, want silent no-release", ok, err)
	}
}

func TestCommandTriggerSingleFlight(t *testing.T) {
	trig := NewCommandTrigger("sleep 5", time.Minute, time.Hour)
	var posts []string
	post := func(m string) { posts = append(posts, m) }
	if started, _ := trig.Request("first", post); !started {
		t.Fatal("first request refused")
	}
	if started, note := trig.Request("second", post); started || note == "" {
		t.Fatalf("overlap started=%v note=%q", started, note)
	}
	if !trig.Status().Running {
		t.Error("status not running")
	}
}

func TestCommandTriggerRunsAndPosts(t *testing.T) {
	trig := NewCommandTrigger("echo hello", 10*time.Second, 0)
	done := make(chan string, 1)
	started, _ := trig.Request("test release", func(m string) { done <- m })
	if !started {
		t.Fatal("request refused")
	}
	select {
	case msg := <-done:
		if !strings.Contains(msg, "finished") || !strings.Contains(msg, "test release") {
			t.Errorf("finish post = %q", msg)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no finish post")
	}
	st := trig.Status()
	if st.Running || !st.LastOK {
		t.Errorf("status = %+v", st)
	}
}

func TestCommandTriggerFailurePostsTail(t *testing.T) {
	trig := NewCommandTrigger("echo boom; exit 3", 10*time.Second, 0)
	done := make(chan string, 1)
	trig.Request("bad release", func(m string) { done <- m })
	select {
	case msg := <-done:
		if !strings.Contains(msg, "failed") || !strings.Contains(msg, "boom") {
			t.Errorf("failure post = %q", msg)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no failure post")
	}
	if trig.Status().LastOK {
		t.Error("failed run marked ok")
	}
}
