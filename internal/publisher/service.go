package publisher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// Service is the publisher: it checks scopes, categories and the kill
// switch, queues what needs a reviewer, runs what may go out, and audits
// all of it.
type Service struct {
	cfg    Config
	ov     OpenVerse
	news   NewsAdmin
	queue  *Queue
	audit  *Audit
	done   *doneStore
	mir    *mirrorState
	killed atomic.Bool
	log    *slog.Logger
	// Notify tells staff about a new draft or a failed publish; may be nil.
	Notify func(msg string)
	// FetchImage loads an image from our own assets; replaceable in tests.
	FetchImage func(ctx context.Context, url string) (string, error)
	newsMu     sync.Mutex
}

// New builds the service. ov and news may be nil when unconfigured; actions
// that need them then fail with ErrNotConfigured.
func New(cfg Config, ov OpenVerse, news NewsAdmin, log *slog.Logger) (*Service, error) {
	q, err := NewQueue(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	a, err := NewAudit(cfg.StateDir, log)
	if err != nil {
		return nil, err
	}
	d, err := newDoneStore(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	m, err := newMirrorState(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	s := &Service{cfg: cfg, ov: ov, news: news, queue: q, audit: a, done: d, mir: m, log: log}
	s.killed.Store(cfg.Kill)
	s.FetchImage = func(ctx context.Context, u string) (string, error) { return FetchImage(ctx, u, cfg.ImageHosts) }
	return s, nil
}

// Queue exposes the review queue.
func (s *Service) Queue() *Queue { return s.queue }

// Audit exposes the audit log.
func (s *Service) Audit() *Audit { return s.audit }

// Config returns the configuration.
func (s *Service) Config() Config { return s.cfg }

// Killed reports the kill switch.
func (s *Service) Killed() bool { return s.killed.Load() }

// Kill engages the kill switch at runtime. There is deliberately no way to
// release it except changing the environment and restarting.
func (s *Service) Kill(by string) {
	s.killed.Store(true)
	_ = s.audit.Record(AuditEntry{Actor: by, Event: "killed", Detail: "kill switch engaged at runtime"})
}

var toolScope = map[string]Scope{
	ToolPost: ScopeOpenVersePost, ToolCommunity: ScopeOpenVerseCommunity, ToolNews: ScopeNewsPublish, ToolBoss: ScopeBossQueue,
}

// validate normalises and checks an action's shape (not its permissions).
func (s *Service) validate(a *Action) error {
	if _, ok := toolScope[a.Tool]; !ok {
		return fmt.Errorf("%w: unknown tool %q", ErrBadAction, a.Tool)
	}
	known := false
	for _, c := range AllCategories() {
		known = known || c == a.Category
	}
	if !known {
		return fmt.Errorf("%w: unknown category %q", ErrBadAction, a.Category)
	}
	if a.Template != "" && a.Body != "" {
		return fmt.Errorf("%w: a templated action takes facts, not free text", ErrBadAction)
	}
	switch a.Tool {
	case ToolPost:
		if a.Template == "" && strings.TrimSpace(a.Body) == "" {
			return fmt.Errorf("%w: a post needs text or a template", ErrBadAction)
		}
		if a.Community == (CommunityRef{}) {
			return fmt.Errorf("%w: a post needs a community", ErrBadAction)
		}
		if a.Template != "" {
			if _, _, err := RenderTemplate(a.Category, a.Template, a.Facts); err != nil {
				return err
			}
		}
	case ToolCommunity:
		if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Description) == "" {
			return fmt.Errorf("%w: a community needs a title and a description", ErrBadAction)
		}
	case ToolNews:
		if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Body) == "" {
			return fmt.Errorf("%w: a News article needs a title and text", ErrBadAction)
		}
	}
	if a.ImageURL != "" && a.Tool != ToolPost {
		return fmt.Errorf("%w: only posts take an image", ErrBadAction)
	}
	return nil
}

// autoEligible is the whole auto-publish rule: Pakka's own job, a post, one
// of the factual categories, text from a template (no free text), and no
// News side effect. Nothing else ever publishes without a reviewer.
func autoEligible(actor Actor, a Action) bool {
	return actor.Kind == KindSystem && a.Tool == ToolPost && IsAutoCategory(a.Category) &&
		a.Template != "" && a.Body == "" && !a.SendAsNews
}

func (s *Service) deny(actor Actor, a Action, err error) error {
	_ = s.audit.Record(AuditEntry{Actor: actor.Name, Event: "denied", Tool: a.Tool, Category: a.Category, EventID: a.EventID, Detail: err.Error()})
	return err
}

// Submit is the one entry point for an action. now=true lets a staff actor
// publish immediately (it is their own approval); everything else either
// auto-publishes under the rule above or is queued.
func (s *Service) Submit(ctx context.Context, actor Actor, a Action, now bool) (Outcome, error) {
	if err := s.validate(&a); err != nil {
		return Outcome{}, s.deny(actor, a, err)
	}
	if !s.cfg.Categories[a.Category] {
		return Outcome{}, s.deny(actor, a, fmt.Errorf("%w: %s", ErrCategoryDisabled, a.Category))
	}
	switch {
	case autoEligible(actor, a):
		if !actor.Scopes[toolScope[a.Tool]] {
			return Outcome{}, s.deny(actor, a, ErrForbidden)
		}
		res, err := s.run(ctx, actor.Name, a)
		if err != nil {
			return Outcome{}, err
		}
		return Outcome{Mode: "published", Result: &res}, nil
	case now && actor.Kind == KindStaff:
		if !actor.Scopes[toolScope[a.Tool]] || !actor.Scopes[ScopeApprove] {
			return Outcome{}, s.deny(actor, a, ErrForbidden)
		}
		if a.EventID == "" {
			a.EventID = "staff:" + newID()
		}
		res, err := s.run(ctx, actor.Name, a)
		if err != nil {
			return Outcome{}, err
		}
		return Outcome{Mode: "published", Result: &res}, nil
	default:
		if !actor.Scopes[ScopeDraft] {
			return Outcome{}, s.deny(actor, a, ErrForbidden)
		}
		d, err := s.queue.Add(a, s.preview(a), actor.Name)
		if err != nil {
			return Outcome{}, err
		}
		_ = s.audit.Record(AuditEntry{Actor: actor.Name, Event: "queued", Tool: a.Tool, Category: a.Category, EventID: d.Action.EventID, DraftID: d.ID, Detail: d.Preview})
		if s.Notify != nil {
			s.Notify(fmt.Sprintf("[Pakka - publisher] draft #%s waiting for approval (%s/%s): %s", d.ID, a.Tool, a.Category, truncRunes(d.Preview, 300)))
		}
		return Outcome{Mode: "queued", DraftID: d.ID}, nil
	}
}

// Approve releases a draft: the reviewer needs the approve scope and the
// tool's scope, and the kill switch and category must allow it. Approving
// twice cannot publish twice (the queue lets one approval through and the
// event id repeats safely on the far side).
func (s *Service) Approve(ctx context.Context, reviewer Actor, id string) (Draft, error) {
	d, ok := s.queue.Get(id)
	if !ok {
		return Draft{}, ErrNoDraft
	}
	a := d.Action
	if !reviewer.Scopes[ScopeApprove] || !reviewer.Scopes[toolScope[a.Tool]] {
		return Draft{}, s.deny(reviewer, a, ErrForbidden)
	}
	if !s.cfg.Categories[a.Category] {
		return Draft{}, s.deny(reviewer, a, fmt.Errorf("%w: %s", ErrCategoryDisabled, a.Category))
	}
	if s.Killed() {
		return Draft{}, s.deny(reviewer, a, ErrKilled)
	}
	if _, err := s.queue.Begin(id, reviewer.Name); err != nil {
		return Draft{}, err
	}
	_ = s.audit.Record(AuditEntry{Actor: reviewer.Name, Event: "approved", Tool: a.Tool, Category: a.Category, EventID: a.EventID, DraftID: id})
	res, err := s.run(ctx, reviewer.Name, a)
	if err == nil && res.Status == "blocked" {
		err = errors.New(res.Detail)
	}
	if err != nil {
		s.queue.Finish(id, nil, err)
	} else {
		s.queue.Finish(id, &res, nil)
	}
	d, _ = s.queue.Get(id)
	return d, err
}

// Reject discards a draft.
func (s *Service) Reject(reviewer Actor, id string) (Draft, error) {
	if !reviewer.Scopes[ScopeApprove] {
		return Draft{}, s.deny(reviewer, Action{}, ErrForbidden)
	}
	d, err := s.queue.Reject(id, reviewer.Name)
	if err != nil {
		return Draft{}, err
	}
	_ = s.audit.Record(AuditEntry{Actor: reviewer.Name, Event: "rejected", Tool: d.Action.Tool, Category: d.Action.Category, EventID: d.Action.EventID, DraftID: id})
	return d, nil
}

// run executes one action: kill switch, configuration, audit-before-publish
// (refused when the audit log cannot be written), then the tool.
func (s *Service) run(ctx context.Context, by string, a Action) (Result, error) {
	if s.Killed() {
		_ = s.audit.Record(AuditEntry{Actor: by, Event: "denied", Tool: a.Tool, Category: a.Category, EventID: a.EventID, Detail: ErrKilled.Error()})
		return Result{}, ErrKilled
	}
	if err := s.audit.Record(AuditEntry{Actor: by, Event: "attempt", Tool: a.Tool, Category: a.Category, EventID: a.EventID, Detail: s.preview(a)}); err != nil {
		return Result{}, fmt.Errorf("refusing to publish: %w", err)
	}
	res, err := s.execute(ctx, a)
	ev := AuditEntry{Actor: by, Tool: a.Tool, Category: a.Category, EventID: a.EventID}
	switch {
	case err != nil:
		ev.Event, ev.Detail = "failed", err.Error()
	case res.Status == "blocked":
		ev.Event, ev.Detail = "blocked", res.Detail
	default:
		ev.Event, ev.Detail = "published", res.Status+" "+refsString(res.Refs)
	}
	_ = s.audit.Record(ev)
	return res, err
}

func refsString(m map[string]string) string {
	var parts []string
	for _, k := range []string{"post", "url", "community", "articles", "build"} {
		if v := m[k]; v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	return strings.Join(parts, " ")
}

func (s *Service) execute(ctx context.Context, a Action) (Result, error) {
	switch a.Tool {
	case ToolPost:
		return s.execPost(ctx, a)
	case ToolCommunity:
		return s.execCommunity(ctx, a)
	case ToolNews:
		return s.execNews(ctx, a)
	case ToolBoss:
		// No BOSS sender yet: nn-boss is not wired for Pakka and no BOSS
		// keys are provisioned to it. Say so rather than pretend.
		return Result{Status: "blocked", Detail: "blocked: no BOSS keys"}, nil
	}
	return Result{}, fmt.Errorf("%w: %s", ErrBadAction, a.Tool)
}

// postText is the text of a post from its template or free text.
func postText(a Action) (string, error) {
	if a.Template == "" {
		return strings.TrimSpace(a.Body), nil
	}
	title, body, err := RenderTemplate(a.Category, a.Template, a.Facts)
	if err != nil {
		return "", err
	}
	if a.Category == CatNewsMirror && title != "" {
		return title + "\n" + body, nil
	}
	return body, nil
}

func (s *Service) execPost(ctx context.Context, a Action) (Result, error) {
	if s.ov == nil {
		return Result{}, ErrNotConfigured
	}
	text, err := postText(a)
	if err != nil {
		return Result{}, err
	}
	var image string
	if a.ImageURL != "" {
		if image, err = s.FetchImage(ctx, a.ImageURL); err != nil {
			return Result{}, err
		}
	}
	pr, err := s.ov.Post(ctx, PostRequest{EventID: a.EventID, Community: a.Community, Body: text, Image: image, Discuss: a.Discuss})
	if err != nil {
		return Result{}, err
	}
	res := Result{Status: "published", Refs: map[string]string{"post": pr.ID, "url": pr.URL, "community": pr.CommunityID}}
	if !pr.Created {
		res.Status = "exists"
	}
	if a.SendAsNews {
		na := a
		na.Tool = ToolNews
		if na.Title == "" {
			na.Title = firstLine(text)
		}
		na.Body = text
		nres, err := s.execNews(ctx, na)
		if err != nil {
			res.Detail = "post published, News failed: " + err.Error()
			return res, fmt.Errorf("post published (%s) but News failed: %w", pr.URL, err)
		}
		res.Refs["articles"] = nres.Refs["articles"]
		res.Refs["build"] = nres.Refs["build"]
	}
	return res, nil
}

func (s *Service) execCommunity(ctx context.Context, a Action) (Result, error) {
	if s.ov == nil {
		return Result{}, ErrNotConfigured
	}
	cr, err := s.ov.EnsureCommunity(ctx, CommunityRequest{
		Name: strings.TrimSpace(a.Title), Description: strings.TrimSpace(a.Description), TitleIDs: a.TitleIDs,
	})
	if err != nil {
		return Result{}, err
	}
	st := "published"
	if !cr.Created {
		st = "exists"
	}
	return Result{Status: st, Refs: map[string]string{"community": cr.ID, "url": cr.URL}}, nil
}

func (s *Service) preview(a Action) string {
	switch a.Tool {
	case ToolPost:
		text, err := postText(a)
		if err != nil {
			text = "(invalid: " + err.Error() + ")"
		}
		extra := ""
		if a.ImageURL != "" {
			extra += " +image"
		}
		if a.Discuss {
			extra += " +discuss"
		}
		if a.SendAsNews {
			extra += " +News"
		}
		return fmt.Sprintf("post to %s%s: %s", refName(a.Community), extra, truncRunes(text, 280))
	case ToolCommunity:
		return fmt.Sprintf("create community %q: %s", a.Title, truncRunes(a.Description, 200))
	case ToolNews:
		return fmt.Sprintf("News %q to %s: %s", a.Title, channelsLabel(a), truncRunes(a.Body, 200))
	case ToolBoss:
		return "BOSS notification: " + truncRunes(a.Title+" "+a.Body, 200)
	}
	return a.Tool
}

func channelsLabel(a Action) string {
	if len(a.Channels) > 0 {
		return strings.Join(a.Channels, ",")
	}
	return "the fitting channels"
}

func refName(r CommunityRef) string {
	switch {
	case r.Name != "":
		return r.Name
	case r.TitleID != "":
		return "title " + r.TitleID
	}
	return "community " + r.ID
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return truncRunes(line, 100)
}

func truncRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func validUTF8(s string) bool { return utf8.ValidString(s) }

// DeletePost removes one of Pakka's own OpenVerse posts (rollback, test
// cleanup). Staff only, audited; OpenVerse only ever deletes Pakka's posts.
func (s *Service) DeletePost(ctx context.Context, by Actor, id string) error {
	if s.ov == nil {
		return ErrNotConfigured
	}
	a := Action{Tool: ToolPost, EventID: id}
	if !by.Scopes[ScopeOpenVersePost] || !by.Scopes[ScopeApprove] {
		return s.deny(by, a, ErrForbidden)
	}
	if err := s.audit.Record(AuditEntry{Actor: by.Name, Event: "attempt", Tool: ToolPost, EventID: id, Detail: "delete post"}); err != nil {
		return fmt.Errorf("refusing to delete: %w", err)
	}
	err := s.ov.DeletePost(ctx, id)
	ev := AuditEntry{Actor: by.Name, Tool: ToolPost, EventID: id, Event: "deleted"}
	if err != nil {
		ev.Event, ev.Detail = "failed", err.Error()
	}
	_ = s.audit.Record(ev)
	return err
}
