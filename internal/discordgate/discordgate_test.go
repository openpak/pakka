package discordgate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func b(v bool) *bool { return &v }

type fake struct {
	res   Result
	err   error
	calls int32
}

func (f *fake) Lookup(context.Context, string) (Result, error) {
	atomic.AddInt32(&f.calls, 1)
	return f.res, f.err
}

func gate(f *fake) *Gate {
	return &Gate{Lookup: f, LinkURL: "https://site/link", NoticeURL: "https://site/notice"}
}

const id = "123456789012345678"

func TestDecisions(t *testing.T) {
	adult := Result{Linked: true, Account: Account{ID: "acct-1"}, SupervisedOrMinor: b(false), NoticeAccepted: b(true)}
	cases := []struct {
		name string
		f    *fake
		want Outcome
		msg  string
	}{
		{"not linked", &fake{res: Result{Linked: false}}, NotLinked, "https://site/link"},
		{"child", &fake{res: Result{Linked: true, Account: Account{ID: "a"}, SupervisedOrMinor: b(true), NoticeAccepted: b(true)}}, Child, "can't chat"},
		{"child with no notice is still a child", &fake{res: Result{Linked: true, Account: Account{ID: "a"}, SupervisedOrMinor: b(true), NoticeAccepted: b(false)}}, Child, "can't chat"},
		{"age missing", &fake{res: Result{Linked: true, Account: Account{ID: "a"}, NoticeAccepted: b(true)}}, Child, ""},
		{"no account id", &fake{res: Result{Linked: true, SupervisedOrMinor: b(false), NoticeAccepted: b(true)}}, Child, ""},
		{"notice false", &fake{res: Result{Linked: true, Account: Account{ID: "a"}, SupervisedOrMinor: b(false), NoticeAccepted: b(false)}}, NoticeRequired, "https://site/notice"},
		{"notice unknown (null)", &fake{res: Result{Linked: true, Account: Account{ID: "a"}, SupervisedOrMinor: b(false)}}, NoticeRequired, "Meta"},
		{"lookup error", &fake{err: errors.New("down")}, Unavailable, "try again"},
		{"allowed", &fake{res: adult}, Allowed, ""},
	}
	for _, c := range cases {
		d := gate(c.f).Check(context.Background(), id)
		if d.Outcome != c.want {
			t.Errorf("%s: outcome %v, want %v", c.name, d.Outcome, c.want)
		}
		if !strings.Contains(d.Message, c.msg) {
			t.Errorf("%s: message %q lacks %q", c.name, d.Message, c.msg)
		}
		if d.Allowed() != (c.want == Allowed) {
			t.Errorf("%s: Allowed() = %v", c.name, d.Allowed())
		}
		if c.want == Allowed {
			if d.AccountID != "acct-1" || d.Message != "" {
				t.Errorf("allowed decision: %+v", d)
			}
		} else if d.AccountID != "" || !d.Ephemeral {
			t.Errorf("%s: refusal must carry no account and be ephemeral: %+v", c.name, d)
		}
	}
}

func TestNilGateRefuses(t *testing.T) {
	var g *Gate
	if d := g.Check(context.Background(), id); d.Allowed() || d.Outcome != Unavailable {
		t.Fatalf("nil gate: %+v", d)
	}
	if d := (&Gate{}).Check(context.Background(), id); d.Allowed() {
		t.Fatalf("no lookup: %+v", d)
	}
	if _, ok := g.Func()(id); ok {
		t.Fatal("nil gate Func allowed")
	}
}

func TestCacheOnlyHoldsAllowed(t *testing.T) {
	now := time.Unix(1000, 0)
	f := &fake{res: Result{Linked: true, Account: Account{ID: "acct-1"}, SupervisedOrMinor: b(false), NoticeAccepted: b(true)}}
	g := gate(f)
	g.now = func() time.Time { return now }
	g.Check(context.Background(), id)
	g.Check(context.Background(), id)
	if f.calls != 1 {
		t.Fatalf("allowed not cached: %d calls", f.calls)
	}
	now = now.Add(31 * time.Second)
	// After the TTL the website decides again, and an unlink takes effect.
	f.res = Result{Linked: false}
	if d := g.Check(context.Background(), id); d.Outcome != NotLinked || f.calls != 2 {
		t.Fatalf("after ttl: %+v calls=%d", d, f.calls)
	}
	// Refusals are never cached.
	g.Check(context.Background(), id)
	if f.calls != 3 {
		t.Fatalf("refusal cached: %d", f.calls)
	}
}

func site(t *testing.T, status int, body string) (*Client, *string) {
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	_ = auth
	c := NewClient(srv.URL, "k")
	return c, &path
}

func TestClient(t *testing.T) {
	c, path := site(t, 200, `{"linked":true,"account_id":"acct-1","display_name":"A","supervised_or_minor":false,"pakka_notice_accepted":null}`)
	r, err := c.Lookup(context.Background(), id)
	if err != nil || !r.Linked || r.Account.ID != "acct-1" || r.SupervisedOrMinor == nil || *r.SupervisedOrMinor || r.NoticeAccepted != nil {
		t.Fatalf("%+v %v", r, err)
	}
	if *path != "/internal/discord/"+id {
		t.Fatalf("path %s", *path)
	}
	if r, err := mustLookup(site(t, 200, `{"linked":false}`)); err != nil || r.Linked {
		t.Fatalf("unlinked: %+v %v", r, err)
	}
	for name, tc := range map[string]struct {
		status int
		body   string
	}{"401": {401, `{}`}, "502": {502, `{}`}, "html": {200, `<html>`}, "no linked field": {200, `{"account_id":"x"}`}, "empty": {200, ``}} {
		cl, _ := site(t, tc.status, tc.body)
		if _, err := cl.Lookup(context.Background(), id); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := c.Lookup(context.Background(), "../etc"); err == nil {
		t.Error("path injection accepted")
	}
	if _, err := (&Client{BaseURL: "http://x"}).Lookup(context.Background(), id); err == nil {
		t.Error("no key accepted")
	}
}

func mustLookup(c *Client, _ *string) (Result, error) { return c.Lookup(context.Background(), id) }

func TestEndToEndOverHTTP(t *testing.T) {
	c, _ := site(t, 200, `{"linked":true,"account_id":"acct-1","display_name":"A","supervised_or_minor":false,"pakka_notice_accepted":true}`)
	g := &Gate{Lookup: c}
	if d := g.Check(context.Background(), id); !d.Allowed() || d.AccountID != "acct-1" {
		t.Fatalf("%+v", d)
	}
	c, _ = site(t, 200, `{"linked":true,"account_id":"acct-1","supervised_or_minor":true,"pakka_notice_accepted":true}`)
	if d := (&Gate{Lookup: c}).Check(context.Background(), id); d.Outcome != Child {
		t.Fatalf("%+v", d)
	}
	c, _ = site(t, 500, ``)
	if d := (&Gate{Lookup: c}).Check(context.Background(), id); d.Outcome != Unavailable {
		t.Fatalf("%+v", d)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("PAKKA_DISCORD_GATE_KEY", "")
	if FromEnv() != nil {
		t.Fatal("gate without key")
	}
	t.Setenv("PAKKA_DISCORD_GATE_KEY", "k")
	g := FromEnv()
	if g == nil || g.LinkURL != DefaultLinkURL || g.NoticeURL != DefaultNoticeURL {
		t.Fatalf("%+v", g)
	}
}
