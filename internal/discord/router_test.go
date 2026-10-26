package discord

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pakka/internal/meta"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		text string
		want Intent
	}{
		{"how do I install the patch on my sd card", IntentSetup},
		{"first time setup guide for atmosphere", IntentSetup},
		{"cannot connect to lobby, timeout on every join", IntentConnection},
		{"error code 2124-0041 when going online", IntentConnection},
		{"connection issue", IntentConnection},
		{"game crashes on startup with black screen", IntentBug},
		{"found a visual glitch, textures broken", IntentBug},
		{"hello everyone", ""},
		{"update broke my game", ""}, // setup/bug tie stays silent
	}
	for _, tc := range cases {
		got, err := keywordClassifier{}.Classify(tc.text)
		if err != nil {
			t.Errorf("%q: unexpected error %s", tc.text, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.text, got, tc.want)
		}
	}
}

func scriptedClassifier(t *testing.T, reply string, status int) ModelClassifier {
	t.Helper()
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(stub.Close)
	return ModelClassifier{Client: meta.NewClient(stub.URL, "k", "m", 5*time.Second)}
}

func TestModelClassifier(t *testing.T) {
	m := scriptedClassifier(t,
		`{"choices":[{"message":{"role":"assistant","content":"{\"intent\":\"bug\"}"},"finish_reason":"stop"}]}`,
		http.StatusOK)
	intent, err := m.Classify("it crashes")
	if err != nil || intent != IntentBug {
		t.Errorf("intent = %q, err = %v", intent, err)
	}
}

func TestModelClassifierInvalid(t *testing.T) {
	for name, reply := range map[string]string{
		"prose":   `{"choices":[{"message":{"role":"assistant","content":"looks like a bug"},"finish_reason":"stop"}]}`,
		"unknown": `{"choices":[{"message":{"role":"assistant","content":"{\"intent\":\"unknown\"}"},"finish_reason":"stop"}]}`,
		"bogus":   `{"choices":[{"message":{"role":"assistant","content":"{\"intent\":\"nope\"}"},"finish_reason":"stop"}]}`,
	} {
		m := scriptedClassifier(t, reply, http.StatusOK)
		intent, err := m.Classify("x")
		if err != nil || intent != "" {
			t.Errorf("%s: intent = %q, err = %v", name, intent, err)
		}
	}
}

func TestModelClassifierError(t *testing.T) {
	m := scriptedClassifier(t, `{"error":"bad"}`, http.StatusUnauthorized)
	if _, err := m.Classify("x"); err == nil {
		t.Error("want error for 401, got nil")
	}
}

type stubClassifier struct {
	intent Intent
	err    error
}

func (s stubClassifier) Classify(_ string) (Intent, error) { return s.intent, s.err }

func TestChainClassifier(t *testing.T) {
	// Primary error falls back to keywords.
	chain := chainClassifier{
		primary:  stubClassifier{err: errors.New("boom")},
		fallback: stubClassifier{intent: IntentSetup},
	}
	intent, err := chain.Classify("x")
	if err != nil || intent != IntentSetup {
		t.Errorf("fallback: intent = %q, err = %v", intent, err)
	}
	// Confident unknown is respected, not retried.
	chain = chainClassifier{
		primary:  stubClassifier{intent: ""},
		fallback: stubClassifier{intent: IntentBug},
	}
	intent, err = chain.Classify("x")
	if err != nil || intent != "" {
		t.Errorf("unknown: intent = %q, err = %v", intent, err)
	}
}

func testRouter() *Router {
	return NewRouter(keywordClassifier{}, "setup1", "conn1", "bug1", time.Hour)
}

func supportMsg(channelID, authorID, content string) Incoming {
	return Incoming{AuthorID: authorID, GuildID: "g1", ChannelID: channelID, Content: content}
}

func TestRouteMismatch(t *testing.T) {
	msg, ok := testRouter().Route(supportMsg("setup1", "u1", "cannot connect to lobby"))
	if !ok {
		t.Fatal("want redirect, got silent")
	}
	if !strings.Contains(msg, "<#conn1>") || !strings.Contains(msg, "<@u1>") {
		t.Errorf("msg = %q", msg)
	}
}

func TestRouteMatchSilent(t *testing.T) {
	if msg, ok := testRouter().Route(supportMsg("conn1", "u1", "cannot connect to lobby")); ok {
		t.Errorf("in-scope message redirected: %q", msg)
	}
}

func TestRouteUnknownSilent(t *testing.T) {
	if msg, ok := testRouter().Route(supportMsg("setup1", "u1", "hello everyone")); ok {
		t.Errorf("unclear message redirected: %q", msg)
	}
}

func TestRouteNonSupportSilent(t *testing.T) {
	if msg, ok := testRouter().Route(supportMsg("general", "u1", "cannot connect to lobby")); ok {
		t.Errorf("non-support channel redirected: %q", msg)
	}
}

func TestRouteCooldown(t *testing.T) {
	r := testRouter()
	in := supportMsg("setup1", "u1", "cannot connect to lobby")
	if _, ok := r.Route(in); !ok {
		t.Fatal("first redirect missing")
	}
	if _, ok := r.Route(in); ok {
		t.Error("second redirect inside cooldown window")
	}
	other := supportMsg("setup1", "u2", "cannot connect to lobby")
	if _, ok := r.Route(other); !ok {
		t.Error("different user suppressed by cooldown")
	}
}

func TestRouterNeedsAllChannels(t *testing.T) {
	if r := NewRouter(keywordClassifier{}, "setup1", "", "bug1", time.Hour); r != nil {
		t.Error("partial channels built a router")
	}
}
