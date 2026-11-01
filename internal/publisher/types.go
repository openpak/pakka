package publisher

import (
	"errors"
	"fmt"
	"time"
)

// Scope is one permission an actor can hold.
type Scope string

// The scopes. Tool scopes let an actor run a tool; Draft lets it queue
// proposals; Approve lets it release or reject the queue.
const (
	ScopeOpenVersePost      Scope = "openverse:post"
	ScopeOpenVerseCommunity Scope = "openverse:community"
	ScopeNewsPublish        Scope = "news:publish"
	ScopeBossQueue          Scope = "boss:queue"
	ScopeDraft              Scope = "queue:draft"
	ScopeApprove            Scope = "queue:approve"
)

// ActorKind separates who is asking.
type ActorKind string

// The kinds of actor.
const (
	KindSystem ActorKind = "system" // Pakka's own scheduled jobs and event intake
	KindModel  ActorKind = "model"  // anything the language model asked for
	KindStaff  ActorKind = "staff"  // a person (Discord staff role or the admin key)
)

// Actor is the caller of a tool.
type Actor struct {
	Name   string
	Kind   ActorKind
	Scopes map[Scope]bool
}

func scopes(s ...Scope) map[Scope]bool {
	m := make(map[Scope]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}

// System is Pakka's own jobs: they may post and queue, nothing else.
func System(job string) Actor {
	return Actor{Name: "system:" + job, Kind: KindSystem, Scopes: scopes(ScopeOpenVersePost, ScopeDraft)}
}

// Model is the language model: it can only propose.
func Model() Actor {
	return Actor{Name: "model", Kind: KindModel, Scopes: scopes(ScopeDraft)}
}

// Staff is a person with the staff role (or the admin key).
func Staff(name string) Actor {
	return Actor{Name: "staff:" + name, Kind: KindStaff, Scopes: scopes(
		ScopeOpenVersePost, ScopeOpenVerseCommunity, ScopeNewsPublish, ScopeBossQueue, ScopeDraft, ScopeApprove)}
}

// Tool names.
const (
	ToolPost      = "openverse.post"
	ToolCommunity = "openverse.community"
	ToolNews      = "news.publish"
	ToolBoss      = "boss.notify"
)

// Categories. The first four may auto-publish when templated; the rest
// always wait for a staff member.
const (
	CatNewsMirror = "news_mirror"
	CatSplatfest  = "splatfest"
	CatTournament = "tournament"
	CatCourse     = "course"
	CatHighlights = "highlights"
	CatAnnounce   = "announcement"
	CatCommunity  = "community"
	CatNews       = "news"
	CatBoss       = "boss"
)

// autoCategories are the only categories that may publish without a
// reviewer, and only with a template. Fixed in code on purpose.
var autoCategories = map[string]bool{CatNewsMirror: true, CatSplatfest: true, CatTournament: true, CatCourse: true}

// AllCategories lists every category.
func AllCategories() []string {
	return []string{CatNewsMirror, CatSplatfest, CatTournament, CatCourse, CatHighlights, CatAnnounce, CatCommunity, CatNews, CatBoss}
}

// IsAutoCategory reports whether a category may publish unreviewed (when templated).
func IsAutoCategory(c string) bool { return autoCategories[c] }

// CommunityRef names an OpenVerse community; the first field that matches wins.
type CommunityRef struct {
	ID      string `json:"id,omitempty"`
	TitleID string `json:"title_id,omitempty"`
	Name    string `json:"name,omitempty"`
}

// Action is one thing Pakka may do. It is also what the queue stores.
type Action struct {
	Tool     string `json:"tool"`
	Category string `json:"category"`
	// EventID makes the action idempotent end to end. Empty = assigned.
	EventID string `json:"event_id,omitempty"`

	// openverse.post
	Community CommunityRef `json:"community,omitempty"`
	Body      string       `json:"body,omitempty"`     // free text: always needs approval
	Template  string       `json:"template,omitempty"` // templated text: may auto-publish
	Facts     Facts        `json:"facts,omitempty"`
	ImageURL  string       `json:"image_url,omitempty"` // our own assets only
	Discuss   bool         `json:"discuss,omitempty"`   // add a "Discuss" link to the post
	// SendAsNews also publishes the post to the News channels that fit.
	SendAsNews bool `json:"send_as_news,omitempty"`

	// openverse.community, news.publish
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	TitleIDs    []string `json:"title_ids,omitempty"` // hex (news) or decimal (community)
	Channels    []string `json:"channels,omitempty"`  // explicit News topics
}

// Facts are the checked values a template fills in.
type Facts map[string]string

// Result is what a tool reports.
type Result struct {
	// Status is "published", "exists" (repeat, nothing new) or "blocked".
	Status string            `json:"status"`
	Detail string            `json:"detail,omitempty"`
	Refs   map[string]string `json:"refs,omitempty"`
}

// Outcome is the answer to a submission.
type Outcome struct {
	// Mode is "published" (ran now) or "queued" (waits for approval).
	Mode    string  `json:"mode"`
	DraftID string  `json:"draft_id,omitempty"`
	Result  *Result `json:"result,omitempty"`
}

// Errors callers can match.
var (
	ErrKilled           = errors.New("publishing is switched off (kill switch)")
	ErrForbidden        = errors.New("actor lacks the scope for this tool")
	ErrCategoryDisabled = errors.New("category is switched off")
	ErrNotConfigured    = errors.New("publishing is not configured")
	ErrBadAction        = errors.New("invalid action")
)

// Draft statuses.
const (
	StatusPending   = "pending"
	StatusApproving = "approving"
	StatusPublished = "published"
	StatusRejected  = "rejected"
	StatusFailed    = "failed"
)

// Draft is a queued action waiting for a staff decision.
type Draft struct {
	ID        string     `json:"id"`
	Action    Action     `json:"action"`
	Preview   string     `json:"preview"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	Status    string     `json:"status"`
	Reviewer  string     `json:"reviewer,omitempty"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
	Result    *Result    `json:"result,omitempty"`
	Error     string     `json:"error,omitempty"`
}

func (d Draft) String() string {
	return fmt.Sprintf("#%s [%s/%s] %s", d.ID, d.Action.Tool, d.Action.Category, d.Preview)
}
