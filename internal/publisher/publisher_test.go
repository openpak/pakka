package publisher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"pakka/internal/store"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type fakeOV struct {
	mu          sync.Mutex
	posts       map[string]PostRequest // by event id
	calls       int
	communities map[string]CommunityResult
	top         []TopPost
	failNext    error
}

func newFakeOV() *fakeOV {
	return &fakeOV{posts: map[string]PostRequest{}, communities: map[string]CommunityResult{}}
}

func (f *fakeOV) Post(_ context.Context, p PostRequest) (PostResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return PostResult{}, err
	}
	_, seen := f.posts[p.EventID]
	if !seen {
		f.posts[p.EventID] = p
	}
	return PostResult{ID: "pk" + p.EventID, URL: "https://verse.test/posts/" + p.EventID, CommunityID: "c1", Created: !seen}, nil
}

func (f *fakeOV) EnsureCommunity(_ context.Context, c CommunityRequest) (CommunityResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.communities[c.Name]; ok {
		r.Created = false
		return r, nil
	}
	r := CommunityResult{ID: "id-" + c.Name, Name: c.Name, Created: true}
	f.communities[c.Name] = r
	return r, nil
}

func (f *fakeOV) FindCommunity(_ context.Context, ref CommunityRef) (CommunityResult, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.communities {
		for _, t := range c.TitleID {
			if ref.TitleID != "" && t == ref.TitleID {
				return c, true, nil
			}
		}
	}
	return CommunityResult{}, false, nil
}

func (f *fakeOV) DeletePost(context.Context, string) error { return nil }
func (f *fakeOV) Top(context.Context, int, int) ([]TopPost, error) {
	return f.top, nil
}
func (f *fakeOV) postCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.posts) }

type fakeNews struct {
	mu       sync.Mutex
	channels []NewsChannel
	articles map[string][]NewsArticle
	created  []string
	builds   int
	nextID   uint32
}

func newFakeNews() *fakeNews {
	return &fakeNews{
		channels: []NewsChannel{
			{TopicID: "openpak_news", Name: "OpenPak News"},
			{TopicID: "openpak_games", Name: "Games"},
			{TopicID: "openpak_kirby", Name: "Kirby", ApplicationIDs: []string{"01007ef00011e000"}},
		},
		articles: map[string][]NewsArticle{},
		nextID:   2611010001,
	}
}

func (f *fakeNews) Channels(context.Context) ([]NewsChannel, error) { return f.channels, nil }
func (f *fakeNews) Articles(_ context.Context, t string) ([]NewsArticle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]NewsArticle(nil), f.articles[t]...), nil
}
func (f *fakeNews) PostArticle(_ context.Context, t string, a NewsArticleCreate) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextID
	f.nextID++
	f.created = append(f.created, fmt.Sprintf("%s:%d:%s", t, id, a.Title))
	return id, nil
}
func (f *fakeNews) PutArticleImage(_ context.Context, t string, id uint32, jpeg []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, fmt.Sprintf("image:%s:%d:%d", t, id, len(jpeg)))
	return nil
}
func (f *fakeNews) Publish(context.Context, string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.builds++
	return f.builds, nil
}
func (f *fakeNews) publish(t string, id uint32, title string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.articles[t] = append(f.articles[t], NewsArticle{TopicID: t, NewsID: id, Title: title, Body: "Body of " + title, EverPublished: true})
}

type fakeWriter struct {
	out string
	err error
}

func (w fakeWriter) Write(context.Context, string, string) (string, error) { return w.out, w.err }

func newSvc(t *testing.T, cats ...string) (*Service, *fakeOV, *fakeNews) {
	t.Helper()
	cfg := Config{Categories: map[string]bool{}, OpenVerseURL: "http://ov", OpenVerseKey: "k", StateDir: t.TempDir(), ImageHosts: []string{"openpak.org"}}
	for _, c := range cats {
		cfg.Categories[c] = true
	}
	ov, news := newFakeOV(), newFakeNews()
	s, err := New(cfg, ov, news, quiet)
	if err != nil {
		t.Fatal(err)
	}
	return s, ov, news
}

func splatfest() Action {
	return Action{Tool: ToolPost, Category: CatSplatfest, Template: "result", EventID: "splatfest:sf-1",
		Community: CommunityRef{Name: "Switch"}, Facts: Facts{"event": "Pizza vs Pasta", "winner": "Team Pizza", "score": "52%"}}
}

func free() Action {
	return Action{Tool: ToolPost, Category: CatAnnounce, Body: "Maintenance tonight", Community: CommunityRef{Name: "Announcements"}}
}

// --- scopes -------------------------------------------------------------

func TestSystemAutoPublishesTemplatedFacts(t *testing.T) {
	s, ov, _ := newSvc(t, CatSplatfest)
	out, err := s.Submit(context.Background(), System("events"), splatfest(), false)
	if err != nil || out.Mode != "published" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if ov.postCount() != 1 {
		t.Fatalf("posts=%d", ov.postCount())
	}
	if got := ov.posts["splatfest:sf-1"].Body; !strings.Contains(got, "Team Pizza won the Pizza vs Pasta Splatfest") {
		t.Fatalf("body %q", got)
	}
}

func TestFreeTextNeverAutoPublishes(t *testing.T) {
	s, ov, _ := newSvc(t, CatAnnounce, CatSplatfest, CatNewsMirror)
	// System with free text in an auto category: still a draft.
	a := splatfest()
	a.Template, a.Facts, a.Body = "", nil, "Winner is whoever I say"
	out, err := s.Submit(context.Background(), System("events"), a, false)
	if err != nil || out.Mode != "queued" {
		t.Fatalf("free text in auto category: out=%+v err=%v", out, err)
	}
	// System with free text in a draft category: queued.
	out, err = s.Submit(context.Background(), System("x"), free(), false)
	if err != nil || out.Mode != "queued" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if ov.postCount() != 0 {
		t.Fatal("nothing may be published without a reviewer")
	}
}

func TestAutoRuleCannotBeWidenedByCategoryOrSideEffects(t *testing.T) {
	s, ov, _ := newSvc(t, CatSplatfest, CatCommunity, CatNews, CatBoss)
	ctx := context.Background()
	// templated + send_as_news is not auto
	a := splatfest()
	a.SendAsNews = true
	if out, err := s.Submit(ctx, System("events"), a, false); err != nil || out.Mode != "queued" {
		t.Fatalf("send_as_news must queue: %+v %v", out, err)
	}
	// non-post tools are never auto, even for the system actor
	for _, tool := range []Action{
		{Tool: ToolCommunity, Category: CatCommunity, Title: "Kirby", Description: "d"},
		{Tool: ToolNews, Category: CatNews, Title: "t", Body: "b"},
		{Tool: ToolBoss, Category: CatBoss, Title: "t", Body: "b"},
	} {
		out, err := s.Submit(ctx, System("x"), tool, false)
		if err == nil && out.Mode != "queued" {
			t.Fatalf("%s must not auto-publish: %+v", tool.Tool, out)
		}
	}
	if ov.postCount() != 0 || len(ov.communities) != 0 {
		t.Fatal("something was published")
	}
}

func TestModelCanOnlyDraft(t *testing.T) {
	s, ov, _ := newSvc(t, CatAnnounce, CatSplatfest)
	ctx := context.Background()
	out, err := s.Submit(ctx, Model(), free(), false)
	if err != nil || out.Mode != "queued" || ov.postCount() != 0 {
		t.Fatalf("model post: %+v %v", out, err)
	}
	// even a templated auto category is a draft when the model asks
	out, err = s.Submit(ctx, Model(), splatfest(), false)
	if err != nil || out.Mode != "queued" || ov.postCount() != 0 {
		t.Fatalf("model templated: %+v %v", out, err)
	}
	// and "now" does nothing for a non-staff actor
	out, err = s.Submit(ctx, Model(), free(), true)
	if err != nil || out.Mode != "queued" {
		t.Fatalf("model now: %+v %v", out, err)
	}
	if _, err := s.Approve(ctx, Model(), out.DraftID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("model approve: %v", err)
	}
	if _, err := s.Reject(Model(), out.DraftID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("model reject: %v", err)
	}
}

func TestStaffScopesAreChecked(t *testing.T) {
	s, ov, _ := newSvc(t, CatAnnounce)
	ctx := context.Background()
	noPost := Staff("a")
	delete(noPost.Scopes, ScopeOpenVersePost)
	if _, err := s.Submit(ctx, noPost, free(), true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing tool scope: %v", err)
	}
	noApprove := Staff("b")
	delete(noApprove.Scopes, ScopeApprove)
	if _, err := s.Submit(ctx, noApprove, free(), true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing approve scope: %v", err)
	}
	noDraft := Actor{Name: "x", Kind: KindStaff, Scopes: scopes()}
	if _, err := s.Submit(ctx, noDraft, free(), false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing draft scope: %v", err)
	}
	if _, err := s.Submit(ctx, Staff("ok"), free(), true); err != nil {
		t.Fatal(err)
	}
	if ov.postCount() != 1 {
		t.Fatalf("posts=%d", ov.postCount())
	}
}

func TestSystemLacksOtherToolScopes(t *testing.T) {
	sys := System("x")
	for _, sc := range []Scope{ScopeOpenVerseCommunity, ScopeNewsPublish, ScopeBossQueue, ScopeApprove} {
		if sys.Scopes[sc] {
			t.Fatalf("system must not hold %s", sc)
		}
	}
	m := Model()
	if len(m.Scopes) != 1 || !m.Scopes[ScopeDraft] {
		t.Fatalf("model scopes: %v", m.Scopes)
	}
}

func TestApproverNeedsTheToolScopeToo(t *testing.T) {
	s, _, _ := newSvc(t, CatCommunity)
	out, _ := s.Submit(context.Background(), Staff("a"), Action{Tool: ToolCommunity, Category: CatCommunity, Title: "Kirby", Description: "d"}, false)
	r := Staff("r")
	delete(r.Scopes, ScopeOpenVerseCommunity)
	if _, err := s.Approve(context.Background(), r, out.DraftID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("got %v", err)
	}
}

// --- approval queue -----------------------------------------------------

func TestDraftIsNotPublishedUntilApproved(t *testing.T) {
	s, ov, _ := newSvc(t, CatHighlights)
	ctx := context.Background()
	a := Action{Tool: ToolPost, Category: CatHighlights, Body: "Best of OpenVerse\nGreat week", Community: CommunityRef{Name: "Announcements"}}
	out, err := s.Submit(ctx, System("highlights"), a, false)
	if err != nil || out.Mode != "queued" {
		t.Fatal(out, err)
	}
	if ov.postCount() != 0 {
		t.Fatal("published before approval")
	}
	d, err := s.Approve(ctx, Staff("rev"), out.DraftID)
	if err != nil || d.Status != StatusPublished || d.Reviewer != "staff:rev" {
		t.Fatalf("%+v %v", d, err)
	}
	if ov.postCount() != 1 {
		t.Fatalf("posts=%d", ov.postCount())
	}
}

func TestApprovingTwiceCannotPublishTwice(t *testing.T) {
	s, ov, _ := newSvc(t, CatAnnounce)
	ctx := context.Background()
	out, _ := s.Submit(ctx, Model(), free(), false)
	if _, err := s.Approve(ctx, Staff("a"), out.DraftID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, Staff("b"), out.DraftID); !errors.Is(err, ErrNotPending) {
		t.Fatalf("second approve: %v", err)
	}
	if ov.postCount() != 1 {
		t.Fatalf("posts=%d", ov.postCount())
	}
}

func TestConcurrentApprovalsPublishOnce(t *testing.T) {
	s, ov, _ := newSvc(t, CatAnnounce)
	ctx := context.Background()
	out, _ := s.Submit(ctx, Model(), free(), false)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Approve(ctx, Staff("x"), out.DraftID)
		}()
	}
	wg.Wait()
	if ov.calls != 1 {
		t.Fatalf("publish calls=%d", ov.calls)
	}
}

func TestRejectedDraftCannotBeApproved(t *testing.T) {
	s, ov, _ := newSvc(t, CatAnnounce)
	ctx := context.Background()
	out, _ := s.Submit(ctx, Model(), free(), false)
	if _, err := s.Reject(Staff("a"), out.DraftID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, Staff("a"), out.DraftID); !errors.Is(err, ErrNotPending) {
		t.Fatalf("got %v", err)
	}
	if ov.postCount() != 0 {
		t.Fatal("published a rejected draft")
	}
}

func TestFailedPublishStaysRetryable(t *testing.T) {
	s, ov, _ := newSvc(t, CatAnnounce)
	ctx := context.Background()
	out, _ := s.Submit(ctx, Model(), free(), false)
	ov.failNext = &APIError{Service: "openverse", Status: 502, Message: "down"}
	d, err := s.Approve(ctx, Staff("a"), out.DraftID)
	if err == nil || d.Status != StatusFailed {
		t.Fatalf("%+v %v", d, err)
	}
	d, err = s.Approve(ctx, Staff("a"), out.DraftID)
	if err != nil || d.Status != StatusPublished || ov.postCount() != 1 {
		t.Fatalf("retry: %+v %v posts=%d", d, err, ov.postCount())
	}
}

func TestQueueSurvivesRestartAndInterruptedApprovalRetries(t *testing.T) {
	dir := t.TempDir()
	q, _ := NewQueue(store.MustFiles(dir))
	d, _ := q.Add(free(), "p", "model")
	if _, err := q.Begin(d.ID, "r"); err != nil {
		t.Fatal(err)
	}
	q2, err := NewQueue(store.MustFiles(dir))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := q2.Get(d.ID)
	if !ok || got.Status != StatusFailed {
		t.Fatalf("%+v %v", got, ok)
	}
	if _, err := q2.Begin(d.ID, "r"); err != nil {
		t.Fatalf("retry after interruption: %v", err)
	}
}

func TestSameEventQueuesOnce(t *testing.T) {
	q, _ := NewQueue(nil)
	a := free()
	a.EventID = "highlights:2026-W44"
	d1, _ := q.Add(a, "p", "m")
	d2, _ := q.Add(a, "p", "m")
	if d1.ID != d2.ID || len(q.List("")) != 1 {
		t.Fatal("duplicate draft for one event")
	}
}

func TestDraftApprovalNotifiesStaff(t *testing.T) {
	s, _, _ := newSvc(t, CatAnnounce)
	var got string
	s.Notify = func(m string) { got = m }
	out, _ := s.Submit(context.Background(), Model(), free(), false)
	if !strings.Contains(got, out.DraftID) || !strings.Contains(got, "waiting for approval") {
		t.Fatalf("notice %q", got)
	}
}

// --- idempotency --------------------------------------------------------

func TestSameEventPublishesOnce(t *testing.T) {
	s, ov, _ := newSvc(t, CatSplatfest)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := s.Submit(ctx, System("events"), splatfest(), false); err != nil {
			t.Fatal(err)
		}
	}
	if ov.postCount() != 1 {
		t.Fatalf("posts=%d", ov.postCount())
	}
}

func TestMirrorBaselinesThenMirrorsOnceAndSkipsOwnArticles(t *testing.T) {
	s, ov, news := newSvc(t, CatNewsMirror)
	ctx := context.Background()
	news.publish("openpak_news", 1, "Old news")
	n, err := s.MirrorPass(ctx)
	if err != nil || n != 0 || ov.postCount() != 0 {
		t.Fatalf("baseline must not flood: n=%d err=%v posts=%d", n, err, ov.postCount())
	}
	news.publish("openpak_news", 2, "Fresh news")
	news.publish("openpak_kirby", 3, "Kirby update")
	for i := 0; i < 3; i++ {
		if _, err := s.MirrorPass(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if ov.postCount() != 2 {
		t.Fatalf("posts=%d, want one per new article", ov.postCount())
	}
	p := ov.posts["news:openpak_news:2"]
	if !p.Discuss || !strings.Contains(p.Body, "Fresh news") || p.Community.Name != "Announcements" {
		t.Fatalf("mirror post %+v", p)
	}
	if k := ov.posts["news:openpak_kirby:3"]; k.Community.Name != "Switch" {
		t.Fatalf("game without a community falls back to Switch: %+v", k.Community)
	}
	// an article Pakka wrote itself is never mirrored back
	s.markMirrored("openpak_news", 9)
	news.publish("openpak_news", 9, "Pakka's own")
	_, _ = s.MirrorPass(ctx)
	if _, ok := ov.posts["news:openpak_news:9"]; ok {
		t.Fatal("mirrored an article Pakka authored")
	}
}

func TestMirrorUsesGameCommunityWhenItExists(t *testing.T) {
	s, ov, news := newSvc(t, CatNewsMirror)
	ov.communities["Kirby"] = CommunityResult{ID: "kirby-c", Name: "Kirby", TitleID: []string{"01007ef00011e000"}}
	_, _ = s.MirrorPass(context.Background()) // baseline
	news.publish("openpak_kirby", 5, "Patch")
	_, _ = s.MirrorPass(context.Background())
	if got := ov.posts["news:openpak_kirby:5"].Community; got.ID != "kirby-c" {
		t.Fatalf("community %+v", got)
	}
}

func TestMirrorStateSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Categories: map[string]bool{CatNewsMirror: true}, OpenVerseURL: "x", OpenVerseKey: "k", StateDir: dir}
	ov, news := newFakeOV(), newFakeNews()
	s1, _ := New(cfg, ov, news, quiet)
	_, _ = s1.MirrorPass(context.Background())
	news.publish("openpak_news", 2, "New")
	_, _ = s1.MirrorPass(context.Background())
	s2, _ := New(cfg, ov, news, quiet)
	_, _ = s2.MirrorPass(context.Background())
	if ov.postCount() != 1 {
		t.Fatalf("posts=%d", ov.postCount())
	}
}

func TestMirrorRespectsKillAndCategory(t *testing.T) {
	s, ov, news := newSvc(t) // no categories on
	_, _ = s.MirrorPass(context.Background())
	news.publish("openpak_news", 2, "New")
	if _, err := s.MirrorPass(context.Background()); !errors.Is(err, ErrCategoryDisabled) {
		t.Fatalf("got %v", err)
	}
	s.cfg.Categories[CatNewsMirror] = true
	s.Kill("test")
	if _, err := s.MirrorPass(context.Background()); !errors.Is(err, ErrKilled) {
		t.Fatalf("got %v", err)
	}
	if ov.postCount() != 0 {
		t.Fatal("mirrored while off")
	}
}

func TestNewsPublishIsIdempotent(t *testing.T) {
	s, _, news := newSvc(t, CatNews)
	ctx := context.Background()
	a := Action{Tool: ToolNews, Category: CatNews, Title: "Maintenance", Body: "Line one\nline two", EventID: "news:evt-1"}
	for i := 0; i < 2; i++ {
		if _, err := s.Submit(ctx, Staff("a"), a, true); err != nil {
			t.Fatal(err)
		}
	}
	if len(news.created) != 1 || news.builds != 1 {
		t.Fatalf("created=%v builds=%d", news.created, news.builds)
	}
}

// --- news channels ------------------------------------------------------

func TestChannelsForFitsGameAndGeneral(t *testing.T) {
	n := newFakeNews()
	got, err := channelsFor(Action{TitleIDs: []string{"01007EF00011E000"}}, n.channels)
	if err != nil || strings.Join(got, ",") != "openpak_kirby,openpak_news" {
		t.Fatalf("%v %v", got, err)
	}
	got, _ = channelsFor(Action{}, n.channels)
	if strings.Join(got, ",") != "openpak_news" {
		t.Fatalf("%v", got)
	}
	if _, err := channelsFor(Action{Channels: []string{"openpak_nope"}}, n.channels); !errors.Is(err, ErrBadAction) {
		t.Fatalf("%v", err)
	}
	n.channels[2].Retired = true
	got, _ = channelsFor(Action{TitleIDs: []string{"01007ef00011e000"}}, n.channels)
	if strings.Join(got, ",") != "openpak_news" {
		t.Fatalf("retired channel used: %v", got)
	}
}

func TestSendAsNewsPublishesPostAndArticles(t *testing.T) {
	s, ov, news := newSvc(t, CatAnnounce)
	ctx := context.Background()
	a := free()
	a.SendAsNews = true
	a.TitleIDs = []string{"01007ef00011e000"}
	out, err := s.Submit(ctx, Model(), a, false)
	if err != nil || out.Mode != "queued" {
		t.Fatal(out, err)
	}
	if len(news.created) != 0 || ov.postCount() != 0 {
		t.Fatal("must wait for approval")
	}
	d, err := s.Approve(ctx, Staff("a"), out.DraftID)
	if err != nil || ov.postCount() != 1 || len(news.created) != 2 {
		t.Fatalf("%+v %v posts=%d news=%v", d, err, ov.postCount(), news.created)
	}
	// the mirror does not echo them back
	news.publish("openpak_news", 1, "x")
	for _, c := range news.created {
		parts := strings.SplitN(c, ":", 3)
		if !s.mir.has(parts[0] + ":" + parts[1]) {
			t.Fatalf("own article %s not marked", c)
		}
	}
}

// --- kill switch, categories, audit ------------------------------------

func TestKillSwitchStopsEverything(t *testing.T) {
	s, ov, _ := newSvc(t, CatAnnounce, CatSplatfest)
	ctx := context.Background()
	out, _ := s.Submit(ctx, Model(), free(), false)
	s.Kill("tester")
	if _, err := s.Submit(ctx, System("e"), splatfest(), false); !errors.Is(err, ErrKilled) {
		t.Fatalf("auto: %v", err)
	}
	if _, err := s.Submit(ctx, Staff("a"), free(), true); !errors.Is(err, ErrKilled) {
		t.Fatalf("staff now: %v", err)
	}
	if _, err := s.Approve(ctx, Staff("a"), out.DraftID); !errors.Is(err, ErrKilled) {
		t.Fatalf("approve: %v", err)
	}
	if d, _ := s.Queue().Get(out.DraftID); d.Status != StatusPending {
		t.Fatalf("draft must stay pending, is %s", d.Status)
	}
	if ov.postCount() != 0 {
		t.Fatal("published while killed")
	}
}

func TestKillFromConfig(t *testing.T) {
	cfg := LoadConfig(func(k string) string {
		return map[string]string{"PAKKA_PUBLISH_KILL": "1", "PAKKA_PUBLISH_CATEGORIES": "splatfest, news_mirror ,", "PAKKA_OPENVERSE_URL": "http://x/", "PAKKA_OPENVERSE_KEY": "k"}[k]
	})
	if !cfg.Kill || !cfg.Categories[CatSplatfest] || !cfg.Categories[CatNewsMirror] || len(cfg.Categories) != 2 || cfg.OpenVerseURL != "http://x" || !cfg.Enabled() {
		t.Fatalf("%+v", cfg)
	}
	s, _ := New(cfg, newFakeOV(), nil, quiet)
	if _, err := s.Submit(context.Background(), System("e"), splatfest(), false); !errors.Is(err, ErrKilled) {
		t.Fatalf("got %v", err)
	}
}

func TestCategoryToggle(t *testing.T) {
	s, ov, _ := newSvc(t, CatSplatfest) // tournament and announcement are off
	ctx := context.Background()
	a := Action{Tool: ToolPost, Category: CatTournament, Template: "winner", Community: CommunityRef{Name: "Switch"}, Facts: Facts{"tournament": "Cup", "winner": "Ann"}}
	if _, err := s.Submit(ctx, System("e"), a, false); !errors.Is(err, ErrCategoryDisabled) {
		t.Fatalf("auto category off: %v", err)
	}
	if _, err := s.Submit(ctx, Model(), free(), false); !errors.Is(err, ErrCategoryDisabled) {
		t.Fatalf("draft category off: %v", err)
	}
	if _, err := s.Submit(ctx, System("e"), splatfest(), false); err != nil {
		t.Fatalf("category on: %v", err)
	}
	if ov.postCount() != 1 {
		t.Fatalf("posts=%d", ov.postCount())
	}
	// switching a category off blocks approval of drafts already queued
	s.cfg.Categories[CatAnnounce] = true
	out, _ := s.Submit(ctx, Model(), free(), false)
	delete(s.cfg.Categories, CatAnnounce)
	if _, err := s.Approve(ctx, Staff("a"), out.DraftID); !errors.Is(err, ErrCategoryDisabled) {
		t.Fatalf("approve with category off: %v", err)
	}
}

func TestEveryPublishIsAudited(t *testing.T) {
	s, _, _ := newSvc(t, CatSplatfest, CatAnnounce)
	ctx := context.Background()
	_, _ = s.Submit(ctx, System("events"), splatfest(), false)
	out, _ := s.Submit(ctx, Model(), free(), false)
	_, _ = s.Approve(ctx, Staff("rev"), out.DraftID)
	var kinds []string
	for _, e := range s.Audit().Recent(0) {
		kinds = append(kinds, e.Actor+"/"+e.Event)
	}
	got := strings.Join(kinds, " ")
	for _, want := range []string{"system:events/attempt", "system:events/published", "model/queued", "staff:rev/approved", "staff:rev/attempt", "staff:rev/published"} {
		if !strings.Contains(got, want) {
			t.Fatalf("audit lacks %s:\n%s", want, got)
		}
	}
	raw, err := os.ReadFile(filepath.Join(s.cfg.StateDir, "publisher-audit.jsonl"))
	if err != nil || strings.Count(string(raw), "\n") != len(kinds) {
		t.Fatalf("audit file lines mismatch: %v", err)
	}
}

func TestDenialsAreAudited(t *testing.T) {
	s, _, _ := newSvc(t)
	_, _ = s.Submit(context.Background(), System("e"), splatfest(), false)
	e := s.Audit().Recent(1)[0]
	if e.Event != "denied" || !strings.Contains(e.Detail, "switched off") {
		t.Fatalf("%+v", e)
	}
}

func TestUnwritableAuditRefusesToPublish(t *testing.T) {
	s, ov, _ := newSvc(t, CatSplatfest)
	if err := os.Mkdir(filepath.Join(s.cfg.StateDir, "publisher-audit.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(context.Background(), System("e"), splatfest(), false); err == nil || !strings.Contains(err.Error(), "refusing to publish") {
		t.Fatalf("got %v", err)
	}
	if ov.postCount() != 0 {
		t.Fatal("published without an audit record")
	}
}

// --- templates, boss, images, highlights --------------------------------

func TestTemplateFactsAreChecked(t *testing.T) {
	ctx := context.Background()
	s, ov, _ := newSvc(t, CatSplatfest, CatCourse)
	bad := []Facts{
		{"event": "x", "winner": "see https://evil.example"},
		{"event": "x", "winner": "@everyone"},
		{"event": "x", "winner": "a\x00b"},
		{"event": "x"},
		{"event": strings.Repeat("a", 200), "winner": "w"},
	}
	for _, f := range bad {
		a := splatfest()
		a.Facts = f
		if _, err := s.Submit(ctx, System("e"), a, false); !errors.Is(err, ErrBadAction) {
			t.Fatalf("facts %v: %v", f, err)
		}
	}
	a := splatfest()
	a.Body = "free text next to a template"
	if _, err := s.Submit(ctx, System("e"), a, false); !errors.Is(err, ErrBadAction) {
		t.Fatalf("template plus body: %v", err)
	}
	a = splatfest()
	a.Template = "nope"
	if _, err := s.Submit(ctx, System("e"), a, false); !errors.Is(err, ErrBadAction) {
		t.Fatalf("unknown template: %v", err)
	}
	if ov.postCount() != 0 {
		t.Fatal("a bad action published")
	}
}

func TestBossIsBlockedAndStaysRetryable(t *testing.T) {
	s, _, _ := newSvc(t, CatBoss)
	ctx := context.Background()
	a := Action{Tool: ToolBoss, Category: CatBoss, Title: "t", Body: "b"}
	out, err := s.Submit(ctx, Staff("a"), a, true)
	if err != nil || out.Result.Status != "blocked" || out.Result.Detail != "blocked: no BOSS keys" {
		t.Fatalf("%+v %v", out, err)
	}
	q, _ := s.Submit(ctx, Model(), a, false)
	d, err := s.Approve(ctx, Staff("a"), q.DraftID)
	if err == nil || d.Status != StatusFailed || !strings.Contains(d.Error, "no BOSS keys") {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestImageMustBeOurOwn(t *testing.T) {
	ctx := context.Background()
	for _, u := range []string{"http://openpak.org/a.jpg", "https://evil.example/a.jpg", "https://openpak.org.evil.example/a.jpg", "https://user@openpak.org/a.jpg", "ftp://openpak.org/a.jpg", "%%%"} {
		if _, err := FetchImage(ctx, u, []string{"openpak.org"}); !errors.Is(err, ErrBadAction) {
			t.Fatalf("%s: %v", u, err)
		}
	}
	if !hostAllowed("rewards.openpak.org", []string{"openpak.org"}) || hostAllowed("notopenpak.org", []string{"openpak.org"}) {
		t.Fatal("host matching")
	}
}

func TestPostWithImageUsesFetcher(t *testing.T) {
	s, ov, _ := newSvc(t, CatAnnounce)
	s.FetchImage = func(context.Context, string) (string, error) { return "QUJD", nil }
	a := free()
	a.ImageURL = "https://openpak.org/x.jpg"
	if _, err := s.Submit(context.Background(), Staff("a"), a, true); err != nil {
		t.Fatal(err)
	}
	for _, p := range ov.posts {
		if p.Image != "QUJD" {
			t.Fatalf("image %q", p.Image)
		}
	}
	a2 := Action{Tool: ToolBoss, Category: CatBoss, Title: "t", Body: "b", ImageURL: "https://openpak.org/x.jpg"}
	if _, err := s.Submit(context.Background(), Staff("a"), a2, true); !errors.Is(err, ErrBadAction) {
		t.Fatalf("image on a tool without images: %v", err)
	}
}

func TestNewsWithImageUploadsBanner(t *testing.T) {
	s, _, news := newSvc(t, CatNews)
	s.FetchImage = func(context.Context, string) (string, error) { return "QUJD", nil }
	a := Action{Tool: ToolNews, Category: CatNews, Title: "t", Body: "b", ImageURL: "https://openpak.org/x.jpg"}
	if _, err := s.Submit(context.Background(), Staff("a"), a, true); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range news.created {
		if strings.HasPrefix(c, "image:") && strings.HasSuffix(c, ":3") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no banner upload: %v", news.created)
	}
}

func TestHighlightsAreDraftedNotPublished(t *testing.T) {
	s, ov, _ := newSvc(t, CatHighlights)
	ov.top = []TopPost{{ID: "p1", Author: "Ann", Community: "Drawing", Body: "my drawing", Yeahs: 9}}
	ctx := context.Background()
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	d, err := s.DraftHighlights(ctx, fakeWriter{out: "A lovely week of drawings."}, now)
	if err != nil || d.Status != StatusPending || ov.postCount() != 0 {
		t.Fatalf("%+v %v posts=%d", d, err, ov.postCount())
	}
	if !strings.Contains(d.Action.Body, "A lovely week") || d.Action.EventID != "highlights:2026-W45" {
		t.Fatalf("%+v", d.Action)
	}
	d2, _ := s.DraftHighlights(ctx, fakeWriter{out: "Different text"}, now)
	if d2.ID != d.ID {
		t.Fatal("a second draft for the same week")
	}
	if _, err := s.Approve(ctx, Staff("rev"), d.ID); err != nil || ov.postCount() != 1 {
		t.Fatalf("approve: %v posts=%d", err, ov.postCount())
	}
}

func TestHighlightsStripLinksAndSurviveWriterFailure(t *testing.T) {
	s, ov, _ := newSvc(t, CatHighlights)
	ov.top = []TopPost{{ID: "p1", Body: "x", Yeahs: 1}}
	d, err := s.DraftHighlights(context.Background(), fakeWriter{out: "Visit https://spam.example now @everyone"}, time.Now())
	if err != nil || strings.Contains(d.Action.Body, "http") || strings.Contains(d.Action.Body, "@") {
		t.Fatalf("%+v %v", d, err)
	}
	s2, ov2, _ := newSvc(t, CatHighlights)
	ov2.top = ov.top
	d, err = s2.DraftHighlights(context.Background(), fakeWriter{err: errors.New("model down")}, time.Now())
	if err != nil || !strings.Contains(d.Action.Body, "Best of OpenVerse") {
		t.Fatalf("%+v %v", d, err)
	}
	ov2.top = nil
	if _, err := s2.DraftHighlights(context.Background(), nil, time.Now().AddDate(0, 0, 7)); !errors.Is(err, ErrNothingToHighlight) {
		t.Fatalf("got %v", err)
	}
}

func TestModelToolsOnlyQueue(t *testing.T) {
	s, ov, news := newSvc(t, CatAnnounce, CatCommunity, CatNews)
	ctx := context.Background()
	for name, args := range map[string]string{
		"propose_post":      `{"community":"Announcements","text":"Hi all","send_as_news":true}`,
		"propose_community": `{"name":"Kirby","description":"All about Kirby"}`,
		"propose_news":      `{"title":"T","text":"B"}`,
	} {
		out := s.ExecModelTool(ctx, name, args)
		if !strings.Contains(out, `"queued"`) {
			t.Fatalf("%s: %s", name, out)
		}
	}
	if ov.postCount() != 0 || len(ov.communities) != 0 || len(news.created) != 0 {
		t.Fatal("a model tool published")
	}
	if len(s.Queue().List(StatusPending)) != 3 {
		t.Fatal("expected three drafts")
	}
	if out := s.ExecModelTool(ctx, "post_everything", "{}"); !strings.Contains(out, "unknown tool") {
		t.Fatal(out)
	}
	if out := s.ExecModelTool(ctx, "propose_post", "{"); !strings.Contains(out, "bad arguments") {
		t.Fatal(out)
	}
}

func TestRegistryListsEveryToolWithItsScope(t *testing.T) {
	seen := map[string]Scope{}
	for _, r := range Registry() {
		seen[r.Name] = r.Scope
	}
	for tool, sc := range toolScope {
		if seen[tool] != sc {
			t.Fatalf("%s: registry scope %q, enforced %q", tool, seen[tool], sc)
		}
	}
}

func TestPlainHighlightsAreGrammaticalAndFree(t *testing.T) {
	one := plainHighlights([]TopPost{{Community: "Drawing", Body: "see https://evil.example"}})
	if !strings.Contains(one, "1 post earned") || !strings.Contains(one, "Drawing") || strings.Contains(one, "evil") {
		t.Fatal(one)
	}
	if many := plainHighlights([]TopPost{{}, {}}); !strings.Contains(many, "2 posts earned") {
		t.Fatal(many)
	}
}

func TestDeletePostIsStaffOnlyAndAudited(t *testing.T) {
	s, _, _ := newSvc(t, CatAnnounce)
	ctx := context.Background()
	if err := s.DeletePost(ctx, Model(), "pk1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("model delete: %v", err)
	}
	if err := s.DeletePost(ctx, System("x"), "pk1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("system delete: %v", err)
	}
	if err := s.DeletePost(ctx, Staff("a"), "pk1"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range s.Audit().Recent(0) {
		got = append(got, e.Event)
	}
	if !strings.Contains(strings.Join(got, ","), "deleted") {
		t.Fatalf("not audited: %v", got)
	}
}

// --- staff review: edit, reject with reason, history ----------------------

func TestEditedApprovalPublishesTheEditAndKeepsTheOriginal(t *testing.T) {
	s, ov, _ := newSvc(t, CatHighlights)
	ctx := context.Background()
	a := Action{Tool: ToolPost, Category: CatHighlights, Body: "Best of OpenVerse\nGreat week", Community: CommunityRef{Name: "Announcements"}}
	out, _ := s.Submit(ctx, Model(), a, false)
	d, err := s.ApproveEdited(ctx, Staff("rev"), out.DraftID, Edit{Body: "Best of OpenVerse\nA calmer week"})
	if err != nil || d.Status != StatusPublished {
		t.Fatalf("%+v %v", d, err)
	}
	if d.Original == nil || d.Original.Body != a.Body || d.EditedBy != "staff:rev" || d.Action.Body != "Best of OpenVerse\nA calmer week" {
		t.Fatalf("edit not recorded: %+v", d)
	}
	ov.mu.Lock()
	var got string
	for _, p := range ov.posts {
		got = p.Body
	}
	ov.mu.Unlock()
	if got != "Best of OpenVerse\nA calmer week" {
		t.Fatalf("published %q", got)
	}
	hist, err := s.DraftHistory(out.DraftID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range hist {
		seen[e.Event] = true
	}
	for _, want := range []string{"queued", "edited", "approved", "attempt", "published"} {
		if !seen[want] {
			t.Errorf("history lacks %q: %+v", want, hist)
		}
	}
}

func TestInvalidEditChangesNothingAndTemplatesCannotBeEdited(t *testing.T) {
	s, ov, _ := newSvc(t, CatHighlights, CatSplatfest)
	ctx := context.Background()
	a := Action{Tool: ToolPost, Category: CatHighlights, Body: "Hello", Community: CommunityRef{Name: "Announcements"}}
	o2, err := s.Submit(ctx, Model(), a, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveEdited(ctx, Model(), o2.DraftID, Edit{Body: "x"}); err == nil {
		t.Fatal("model approved")
	}
	d, _ := s.Queue().Get(o2.DraftID)
	if d.Status != StatusPending || d.Action.Body != "Hello" || ov.postCount() != 0 {
		t.Fatalf("changed: %+v", d)
	}
	if _, err := s.Queue().Edit(o2.DraftID, "x", Edit{Body: "y"}, s.preview); err != nil {
		t.Fatal(err)
	}
	tmpl := splatfest()
	tmpl.Template = "result"
	q := s.Queue()
	td, _ := q.Add(tmpl, "p", "system:events")
	if _, err := q.Edit(td.ID, "rev", Edit{Body: "free text"}, s.preview); !errors.Is(err, ErrBadAction) {
		t.Fatalf("template edit: %v", err)
	}
}

func TestRejectKeepsTheReasonInDraftAndAudit(t *testing.T) {
	s, ov, _ := newSvc(t, CatHighlights)
	ctx := context.Background()
	out, _ := s.Submit(ctx, Model(), Action{Tool: ToolPost, Category: CatHighlights, Body: "Hello", Community: CommunityRef{Name: "Announcements"}}, false)
	d, err := s.RejectWithReason(Staff("rev"), out.DraftID, "  too chatty  ")
	if err != nil || d.Status != StatusRejected || d.RejectReason != "too chatty" {
		t.Fatalf("%+v %v", d, err)
	}
	hist, _ := s.DraftHistory(out.DraftID)
	last := hist[len(hist)-1]
	if last.Event != "rejected" || last.Detail != "too chatty" || ov.postCount() != 0 {
		t.Fatalf("%+v", hist)
	}
	if _, err := s.Approve(ctx, Staff("rev"), out.DraftID); !errors.Is(err, ErrNotPending) {
		t.Fatalf("approved a rejected draft: %v", err)
	}
}
