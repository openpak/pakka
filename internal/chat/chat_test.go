package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"pakka/internal/store"
	"strings"
	"sync"
	"testing"
	"time"

	"pakka/internal/meta"
)

const testKey = "service-key"

// fakeSite stands in for the website's private tool surface: it resolves a
// grant to an account and answers only for that account.
type fakeSite struct {
	mu     sync.Mutex
	grants map[string]string // grant -> account
	seen   []string          // grants presented, in order
	data   map[string]map[string]string
	fail   bool
}

func newSite() *fakeSite {
	return &fakeSite{
		grants: map[string]string{"g-a": "acct-a", "g-b": "acct-b", "g-child": "acct-child"},
		data: map[string]map[string]string{
			"acct-a":     {"eligibility": `{"eligible":true}`, "consoles": `{"consoles":["Switch"]}`, "friends": `{"count":4}`, "rewards": `{"balance":250}`, "status": `{"services":"all up"}`},
			"acct-b":     {"eligibility": `{"eligible":true}`, "consoles": `{"consoles":["Wii U","3DS"]}`, "friends": `{"count":9}`, "rewards": `{"balance":7}`, "status": `{"services":"all up"}`},
			"acct-child": {"eligibility": `{"eligible":false}`, "consoles": `{"consoles":["Switch"]}`},
		},
	}
}

func (f *fakeSite) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		http.Error(w, "down", http.StatusBadGateway)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+testKey {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	g := r.Header.Get("X-Pakka-Grant")
	f.seen = append(f.seen, g)
	acct, ok := f.grants[g]
	if !ok {
		http.Error(w, "no grant", http.StatusUnauthorized)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/internal/pakka/tool/")
	body, ok := f.data[acct][name]
	if !ok {
		http.Error(w, "no", http.StatusNotFound)
		return
	}
	_, _ = io.WriteString(w, body)
}

// scriptModel calls the named tools in its first turn (with the given raw
// arguments) and answers with their results in the second.
type scriptModel struct {
	calls []meta.ToolCall
	turn  int
	final func(msgs []meta.Message) string
}

func (m *scriptModel) Complete(_ context.Context, msgs []meta.Message, _ []meta.Tool) (meta.Message, error) {
	m.turn++
	if m.turn == 1 && len(m.calls) > 0 {
		return meta.Message{Role: meta.RoleAssistant, ToolCalls: m.calls}, nil
	}
	out := "hello there from Pakka"
	if m.final != nil {
		out = m.final(msgs)
	}
	return meta.Message{Role: meta.RoleAssistant, Content: out}, nil
}

type rig struct {
	svc  *Service
	ts   *httptest.Server
	site *fakeSite
}

func newRig(t *testing.T, model *scriptModel, mod func(*Config)) *rig {
	t.Helper()
	site := newSite()
	ws := httptest.NewServer(site)
	t.Cleanup(ws.Close)
	cfg := Config{Key: testKey, WebsiteURL: ws.URL, StateDir: t.TempDir(), Retention: time.Hour,
		MaxConversations: 3, BurstPerMinute: 100, DailyCap: 100, DiagnosesPerHour: 1, MaxMessageChars: 200, MaxConcurrent: 4}
	if mod != nil {
		mod(&cfg)
	}
	var m *scriptModel = model
	agent := &Agent{Website: NewWebsite(ws.URL, testKey), Diagnose: func(context.Context, string) (string, error) { return `{"finding":"ok"}`, nil }}
	if m != nil {
		agent.Model = m
	}
	svc, err := New(cfg, agent, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc.Mount(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &rig{svc: svc, ts: ts, site: site}
}

func (r *rig) do(t *testing.T, method, path, key, acct, grant string, body any) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, r.ts.URL+path, rd)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if acct != "" {
		req.Header.Set("X-Account-Id", acct)
	}
	if grant != "" {
		req.Header.Set("X-Pakka-Grant", grant)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (r *rig) as(t *testing.T, acct, grant, method, path string, body any) (int, string) {
	return r.do(t, method, path, testKey, acct, grant, body)
}

func mustConv(t *testing.T, r *rig, acct, grant string) string {
	t.Helper()
	if c, b := r.as(t, acct, grant, "POST", "/v1/chat/consent", nil); c != 200 {
		t.Fatalf("consent: %d %s", c, b)
	}
	c, b := r.as(t, acct, grant, "POST", "/v1/chat/conversations", nil)
	if c != 201 {
		t.Fatalf("create: %d %s", c, b)
	}
	var v struct{ ID string }
	_ = json.Unmarshal([]byte(b), &v)
	return v.ID
}

func TestAuthRejectsSpoofedIdentity(t *testing.T) {
	r := newRig(t, &scriptModel{}, nil)
	if c, _ := r.do(t, "GET", "/v1/chat/conversations", "", "acct-a", "", nil); c != 401 {
		t.Errorf("no key: %d", c)
	}
	if c, _ := r.do(t, "GET", "/v1/chat/conversations", "guess", "acct-a", "", nil); c != 401 {
		t.Errorf("wrong key: %d", c)
	}
	if c, _ := r.do(t, "GET", "/v1/chat/conversations", testKey, "", "", nil); c != 400 {
		t.Errorf("no account: %d", c)
	}
	// A client-side header cannot stand in for the service key.
	req, _ := http.NewRequest("GET", r.ts.URL+"/v1/chat/conversations", nil)
	req.Header.Set("X-Account-Id", "acct-a")
	req.Header.Set("X-Pakka-Key", testKey)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 401 {
		t.Errorf("alt header accepted: %d", resp.StatusCode)
	}
}

func TestChatDisabledWithoutKey(t *testing.T) {
	r := newRig(t, &scriptModel{}, func(c *Config) { c.Key = "" })
	if c, _ := r.do(t, "GET", "/v1/chat/conversations", "", "acct-a", "", nil); c != 503 {
		t.Errorf("got %d", c)
	}
}

func TestConversationsAreIsolatedAndDeletable(t *testing.T) {
	r := newRig(t, &scriptModel{}, nil)
	id := mustConv(t, r, "acct-a", "g-a")
	path := "/v1/chat/conversations/" + id
	if c, _ := r.as(t, "acct-b", "g-b", "GET", path, nil); c != 404 {
		t.Errorf("b reads a: %d", c)
	}
	if c, _ := r.as(t, "acct-b", "g-b", "DELETE", path, nil); c != 404 {
		t.Errorf("b deletes a: %d", c)
	}
	if c, _ := r.as(t, "acct-b", "g-b", "POST", path+"/messages", map[string]string{"text": "hi"}); c != 403 && c != 404 {
		t.Errorf("b sends to a: %d", c)
	}
	if _, b := r.as(t, "acct-b", "g-b", "GET", "/v1/chat/conversations", nil); strings.Contains(b, id) {
		t.Errorf("b lists a's conversation")
	}
	if c, _ := r.as(t, "acct-a", "g-a", "DELETE", path, nil); c != 200 {
		t.Errorf("owner delete: %d", c)
	}
	if c, _ := r.as(t, "acct-a", "g-a", "GET", path, nil); c != 404 {
		t.Errorf("after delete: %d", c)
	}
	if _, b := r.as(t, "acct-a", "g-a", "GET", "/v1/chat/conversations", nil); strings.Contains(b, id) {
		t.Errorf("deleted conversation still listed")
	}
}

func TestDeleteRemovesFile(t *testing.T) {
	r := newRig(t, &scriptModel{}, nil)
	id := mustConv(t, r, "acct-a", "g-a")
	r.as(t, "acct-a", "g-a", "POST", "/v1/chat/conversations/"+id+"/messages", map[string]string{"text": "hi"})
	r.as(t, "acct-a", "g-a", "DELETE", "/v1/chat/conversations/"+id, nil)
	st, err := NewStore(store.MustFiles(r.svc.cfg.StateDir), 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get("acct-a", id); err == nil {
		t.Error("conversation survived a reload after delete")
	}
}

func TestChildAndUnknownAccountsNeverChat(t *testing.T) {
	r := newRig(t, &scriptModel{}, nil)
	for _, p := range []string{"/v1/chat/consent", "/v1/chat/conversations"} {
		if c, _ := r.as(t, "acct-child", "g-child", "POST", p, nil); c != 403 {
			t.Errorf("child %s: %d", p, c)
		}
	}
	if _, b := r.as(t, "acct-child", "g-child", "GET", "/v1/chat/state", nil); !strings.Contains(b, `"eligible":false`) {
		t.Errorf("state: %s", b)
	}
	// No grant: age cannot be determined, so refuse.
	if c, _ := r.as(t, "acct-a", "", "POST", "/v1/chat/consent", nil); c != 403 {
		t.Errorf("no grant: %d", c)
	}
	// Unknown grant, and the website being down, both fail closed.
	if c, _ := r.as(t, "acct-a", "g-nope", "POST", "/v1/chat/consent", nil); c != 403 {
		t.Errorf("unknown grant: %d", c)
	}
	r.site.fail = true
	if c, _ := r.as(t, "acct-a", "g-a", "POST", "/v1/chat/consent", nil); c != 403 {
		t.Errorf("website down: %d", c)
	}
	r.site.fail = false
	// An account that was eligible and chatted, then turns ineligible, is refused on send.
	id := mustConv(t, r, "acct-a", "g-a")
	r.site.mu.Lock()
	r.site.data["acct-a"]["eligibility"] = `{"eligible":false}`
	r.site.mu.Unlock()
	if c, _ := r.as(t, "acct-a", "g-a", "POST", "/v1/chat/conversations/"+id+"/messages", map[string]string{"text": "hi"}); c != 403 {
		t.Errorf("send after becoming ineligible: %d", c)
	}
}

func TestChatBlockedUntilAccepted(t *testing.T) {
	r := newRig(t, &scriptModel{}, nil)
	if c, b := r.as(t, "acct-a", "g-a", "POST", "/v1/chat/conversations", nil); c != 403 || !strings.Contains(b, "consent_required") {
		t.Fatalf("create before consent: %d %s", c, b)
	}
	r.as(t, "acct-a", "g-a", "POST", "/v1/chat/consent", nil)
	id := mustConv(t, r, "acct-a", "g-a")
	r.as(t, "acct-a", "g-a", "DELETE", "/v1/chat/consent", nil)
	if c, b := r.as(t, "acct-a", "g-a", "POST", "/v1/chat/conversations/"+id+"/messages", map[string]string{"text": "hi"}); c != 403 || !strings.Contains(b, "consent_required") {
		t.Errorf("send after withdrawing: %d %s", c, b)
	}
}

func TestSendStreamsAndStores(t *testing.T) {
	r := newRig(t, &scriptModel{}, nil)
	id := mustConv(t, r, "acct-a", "g-a")
	c, b := r.as(t, "acct-a", "g-a", "POST", "/v1/chat/conversations/"+id+"/messages", map[string]string{"text": "hello"})
	if c != 200 || !strings.Contains(b, "event: delta") || !strings.Contains(b, "event: done") {
		t.Fatalf("stream: %d %s", c, b)
	}
	_, b = r.as(t, "acct-a", "g-a", "GET", "/v1/chat/conversations/"+id, nil)
	if !strings.Contains(b, "hello there from Pakka") || !strings.Contains(b, `"title":"hello"`) {
		t.Errorf("not stored: %s", b)
	}
	if c, _ := r.as(t, "acct-a", "g-a", "POST", "/v1/chat/conversations/"+id+"/messages", map[string]string{"text": strings.Repeat("x", 201)}); c != 400 {
		t.Errorf("too long: %d", c)
	}
}

func TestRateLimitsBurstAndDaily(t *testing.T) {
	r := newRig(t, &scriptModel{}, func(c *Config) { c.BurstPerMinute = 2; c.DailyCap = 3 })
	id := mustConv(t, r, "acct-a", "g-a")
	send := func(acct, grant, cid string) int {
		c, _ := r.as(t, acct, grant, "POST", "/v1/chat/conversations/"+cid+"/messages", map[string]string{"text": "hi"})
		return c
	}
	if send("acct-a", "g-a", id) != 200 || send("acct-a", "g-a", id) != 200 {
		t.Fatal("first two should pass")
	}
	if c := send("acct-a", "g-a", id); c != 429 {
		t.Errorf("burst: %d", c)
	}
	// Another account has its own budget.
	idB := mustConv(t, r, "acct-b", "g-b")
	if c := send("acct-b", "g-b", idB); c != 200 {
		t.Errorf("b limited by a: %d", c)
	}
	now := time.Now().Add(2 * time.Minute)
	r.svc.limits.now = func() time.Time { return now }
	if c := send("acct-a", "g-a", id); c != 200 {
		t.Errorf("after window: %d", c)
	}
	now = now.Add(2 * time.Minute)
	if c, b := r.as(t, "acct-a", "g-a", "POST", "/v1/chat/conversations/"+id+"/messages", map[string]string{"text": "hi"}); c != 429 || !strings.Contains(b, "daily_cap") {
		t.Errorf("daily cap: %d %s", c, b)
	}
}

func TestConversationCountLimit(t *testing.T) {
	r := newRig(t, &scriptModel{}, nil)
	mustConv(t, r, "acct-a", "g-a")
	mustConv(t, r, "acct-a", "g-a")
	mustConv(t, r, "acct-a", "g-a")
	if c, _ := r.as(t, "acct-a", "g-a", "POST", "/v1/chat/conversations", nil); c != 409 {
		t.Errorf("limit: %d", c)
	}
}

func TestToolsActOnlyForTheGrantAccount(t *testing.T) {
	// The model tries to name another account in every way it can; the tools
	// take no such argument, so the website only ever sees acct-a's grant.
	var final string
	model := &scriptModel{
		calls: []meta.ToolCall{
			{ID: "1", Function: meta.FunctionCall{Name: "my_consoles", Arguments: `{"account":"acct-b","account_id":"acct-b","grant":"g-b"}`}},
			{ID: "2", Function: meta.FunctionCall{Name: "my_rewards", Arguments: `{"account":"acct-b"}`}},
			{ID: "3", Function: meta.FunctionCall{Name: "my_friends", Arguments: `{"id":"acct-b"}`}},
			{ID: "4", Function: meta.FunctionCall{Name: "my_account_of_someone_else", Arguments: `{}`}},
		},
		final: func(msgs []meta.Message) string {
			var sb strings.Builder
			for _, m := range msgs {
				if m.Role == meta.RoleTool {
					sb.WriteString(m.Content + "|")
				}
			}
			final = sb.String()
			return "done"
		},
	}
	r := newRig(t, model, nil)
	id := mustConv(t, r, "acct-a", "g-a")
	r.site.mu.Lock()
	r.site.seen = nil
	r.site.mu.Unlock()
	if c, b := r.as(t, "acct-a", "g-a", "POST", "/v1/chat/conversations/"+id+"/messages", map[string]string{"text": "what do I have?"}); c != 200 {
		t.Fatalf("%d %s", c, b)
	}
	r.site.mu.Lock()
	defer r.site.mu.Unlock()
	for _, g := range r.site.seen {
		if g != "g-a" {
			t.Errorf("tool presented grant %q", g)
		}
	}
	if !strings.Contains(final, "Switch") || !strings.Contains(final, "250") || strings.Contains(final, "Wii U") || strings.Contains(final, "7}") {
		t.Errorf("tool results: %s", final)
	}
	if !strings.Contains(final, "unknown tool") {
		t.Errorf("unknown tool ran: %s", final)
	}
}

func TestWebsiteToolAllowlist(t *testing.T) {
	w := NewWebsite("http://127.0.0.1:1", testKey)
	for _, n := range []string{"password", "../x", "account", "delete"} {
		if _, err := w.Tool(context.Background(), "g", n); err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("%q: %v", n, err)
		}
	}
}

func TestDiagnosisIsLimited(t *testing.T) {
	var outs []string
	model := &scriptModel{
		calls: []meta.ToolCall{{ID: "1", Function: meta.FunctionCall{Name: "diagnose_connection", Arguments: `{"symptoms":"x"}`}},
			{ID: "2", Function: meta.FunctionCall{Name: "diagnose_connection", Arguments: `{"symptoms":"x"}`}}},
		final: func(msgs []meta.Message) string {
			for _, m := range msgs {
				if m.Role == meta.RoleTool {
					outs = append(outs, m.Content)
				}
			}
			return "ok"
		},
	}
	r := newRig(t, model, nil) // one diagnosis per hour
	id := mustConv(t, r, "acct-a", "g-a")
	r.as(t, "acct-a", "g-a", "POST", "/v1/chat/conversations/"+id+"/messages", map[string]string{"text": "can't connect"})
	if len(outs) != 2 || !strings.Contains(outs[0], "finding") || !strings.Contains(outs[1], "limit reached") {
		t.Errorf("outs: %v", outs)
	}
}

func TestSanitizeAndModelOnlySeesRedactedText(t *testing.T) {
	cases := map[string]string{
		"mail me at jane.doe@example.com":           "[email]",
		"my code is SW-1234-5678-9ABC":              "[friend code]",
		"3ds 1234-5678-9012":                        "[friend code]",
		"from 192.168.1.20 and 2001:db8:0:0:1::1":   "[ip]",
		"acct 3f2b8c1e-aaaa-4bbb-8ccc-1234567890ab": "[id]",
		"serial XAW10012345678":                     "[serial]",
		"call +49 170 1234567":                      "[number]",
		"my password is hunter2":                    "[secret]",
	}
	for in, want := range cases {
		out := Sanitize(in)
		if !strings.Contains(out, want) {
			t.Errorf("%q -> %q, want %s", in, out, want)
		}
		for _, leak := range []string{"jane.doe", "1234-5678", "192.168", "hunter2", "XAW1001", "3f2b8c1e", "170 1234"} {
			if strings.Contains(out, leak) {
				t.Errorf("%q leaked %q: %q", in, leak, out)
			}
		}
	}
	var seen string
	model := &scriptModel{final: func(msgs []meta.Message) string {
		for _, m := range msgs {
			seen += m.Content
		}
		return "ok"
	}}
	r := newRig(t, model, nil)
	id := mustConv(t, r, "acct-a", "g-a")
	r.as(t, "acct-a", "g-a", "POST", "/v1/chat/conversations/"+id+"/messages", map[string]string{"text": "my email is jane.doe@example.com"})
	if strings.Contains(seen, "jane.doe") {
		t.Errorf("model saw the email: %s", seen)
	}
	// The player's own transcript keeps what they typed.
	if _, b := r.as(t, "acct-a", "g-a", "GET", "/v1/chat/conversations/"+id, nil); !strings.Contains(b, "jane.doe") {
		t.Errorf("transcript altered: %s", b)
	}
}

func TestPurgeExpired(t *testing.T) {
	st, _ := NewStore(nil, 5)
	c, _ := st.Create("a")
	now := time.Now().Add(48 * time.Hour)
	st.clock = func() time.Time { return now }
	if n := st.Purge(24 * time.Hour); n != 1 {
		t.Errorf("purged %d", n)
	}
	if _, err := st.Get("a", c.ID); err == nil {
		t.Error("still there")
	}
}

func TestDocsSearch(t *testing.T) {
	idx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"docs":[{"location":"switch/setup/","title":"Switch setup","text":"<p>Install the OpenPak patcher on your Switch &amp; reboot.</p>"},{"location":"x/","title":"Other","text":"nothing"}]}`)
	}))
	defer idx.Close()
	d := NewDocs(idx.URL, "https://docs.example/")
	out := d.Search(context.Background(), "install patcher")
	if !strings.Contains(out, "https://docs.example/switch/setup/") || strings.Contains(out, "<p>") || strings.Contains(out, "Other") {
		t.Errorf("%s", out)
	}
}

func TestToolSchemasSerialiseForTheAPI(t *testing.T) {
	b, err := json.Marshal(tools())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"required":null`) {
		t.Errorf("a tool schema carries required:null, which the model API rejects: %s", b)
	}
}
