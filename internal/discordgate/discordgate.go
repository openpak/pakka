// Package discordgate decides whether a Discord user may talk to Pakka.
//
// A Discord identity is not an OpenPak account until the person linked it on
// openpak.org (Account > Linked platforms). The gate asks the website's
// internal lookup (GET /internal/discord/{discord_id}) and applies the same
// rules as the web: the Meta data-use notice must be accepted, child accounts
// are never answered, and anything that cannot be verified is refused. It
// fails closed on every error, malformed answer and missing field.
//
// The package is transport-neutral and imports nothing from internal/discord;
// see docs/discord-gate.patch for the hook.
package discordgate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Outcome is the gate's verdict.
type Outcome int

const (
	// Unavailable: the link could not be checked. Refused (fail closed).
	Unavailable Outcome = iota
	// NotLinked: no OpenPak account is linked to this Discord user.
	NotLinked
	// Child: the account is supervised or under 18 (or its age is unknown).
	Child
	// NoticeRequired: linked adult who has not accepted the Meta data-use notice.
	NoticeRequired
	// Allowed: may use Pakka. Decision.AccountID is set.
	Allowed
)

func (o Outcome) String() string {
	return [...]string{"unavailable", "not_linked", "child", "notice_required", "allowed"}[o]
}

// Decision is what the caller acts on.
type Decision struct {
	Outcome Outcome
	// AccountID is set only when Allowed: use it for rate limits and tool scoping.
	AccountID string
	// Message is the reply for a refusal. Empty when Allowed.
	Message string
	// Ephemeral asks the transport to show Message only to the user when it
	// can (slash commands and buttons); plain channel messages cannot.
	Ephemeral bool
}

// Allowed reports whether Pakka may answer.
func (d Decision) Allowed() bool { return d.Outcome == Allowed && d.AccountID != "" }

// Account is the website's answer for a linked Discord user.
type Account struct {
	ID          string
	DisplayName string
}

// Looker resolves a Discord user id through the website.
type Looker interface {
	Lookup(ctx context.Context, discordID string) (Result, error)
}

// Result is the lookup answer, decoded strictly.
type Result struct {
	Linked bool
	Account
	// SupervisedOrMinor and NoticeAccepted are pointers so that a missing
	// field is distinguishable from false; missing means refuse.
	SupervisedOrMinor *bool
	NoticeAccepted    *bool
}

var snowflake = regexp.MustCompile(`^[0-9]{5,25}$`)

// Client calls the website's lookup.
type Client struct {
	BaseURL string // e.g. http://openpak-website:20010
	Key     string // WEBSITE_INTERNAL_KEY
	HTTP    *http.Client
}

// NewClient returns a client with a short timeout: the gate sits in front of
// every Discord reply.
func NewClient(baseURL, key string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Key: key, HTTP: &http.Client{Timeout: 5 * time.Second}}
}

// Lookup asks the website who discordID is.
func (c *Client) Lookup(ctx context.Context, discordID string) (Result, error) {
	if !snowflake.MatchString(discordID) {
		return Result{}, errors.New("discordgate: not a Discord user id")
	}
	if c.BaseURL == "" || c.Key == "" {
		return Result{}, errors.New("discordgate: lookup not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/internal/discord/"+url.PathEscape(discordID), nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("discordgate: lookup: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("discordgate: lookup: status %d", resp.StatusCode)
	}
	var raw struct {
		Linked            *bool  `json:"linked"`
		AccountID         string `json:"account_id"`
		DisplayName       string `json:"display_name"`
		SupervisedOrMinor *bool  `json:"supervised_or_minor"`
		NoticeAccepted    *bool  `json:"pakka_notice_accepted"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || raw.Linked == nil {
		return Result{}, errors.New("discordgate: lookup: malformed answer")
	}
	return Result{Linked: *raw.Linked, Account: Account{ID: raw.AccountID, DisplayName: raw.DisplayName},
		SupervisedOrMinor: raw.SupervisedOrMinor, NoticeAccepted: raw.NoticeAccepted}, nil
}

// Gate applies the rules.
type Gate struct {
	Lookup Looker
	// LinkURL is where a person links Discord; NoticeURL is where they accept the notice.
	LinkURL, NoticeURL string
	// TTL caches Allowed decisions only, so an unlink, a ban or a withdrawn
	// notice takes effect within this time. 0 means 30 seconds; negative disables.
	TTL time.Duration

	now   func() time.Time
	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	account string
	until   time.Time
}

// Defaults for the URLs the messages point at.
const (
	DefaultLinkURL   = "https://openpak.org/account#devices"
	DefaultNoticeURL = "https://openpak.org/pakka"
)

// FromEnv builds a gate from PAKKA_DISCORD_GATE_URL (default
// http://openpak-website:20010), PAKKA_DISCORD_GATE_KEY (required, the
// website's internal key), PAKKA_DISCORD_LINK_URL and PAKKA_DISCORD_NOTICE_URL.
// It returns nil when the key is not set, so the caller can leave the gate off
// and log it; a nil *Gate refuses everyone.
func FromEnv() *Gate {
	key := strings.TrimSpace(os.Getenv("PAKKA_DISCORD_GATE_KEY"))
	if key == "" {
		return nil
	}
	return &Gate{
		Lookup:    NewClient(envOr("PAKKA_DISCORD_GATE_URL", "http://openpak-website:20010"), key),
		LinkURL:   envOr("PAKKA_DISCORD_LINK_URL", DefaultLinkURL),
		NoticeURL: envOr("PAKKA_DISCORD_NOTICE_URL", DefaultNoticeURL),
	}
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

// NoticeText is the Meta data-use notice shown before the first answer. The
// website's copy is authoritative; keep the substance the same.
const NoticeText = "Pakka runs on a Meta model. What you write to Pakka may be used by Meta to train and improve its models. " +
	"OpenPak does not train on your chats. Do not share passwords or personal details."

// Check decides for one Discord user. A nil Gate, a nil Lookup, a bad id or
// any lookup failure is Unavailable.
func (g *Gate) Check(ctx context.Context, discordID string) Decision {
	if g == nil || g.Lookup == nil {
		return g.unavailable()
	}
	if acct, ok := g.fromCache(discordID); ok {
		return Decision{Outcome: Allowed, AccountID: acct}
	}
	res, err := g.Lookup.Lookup(ctx, discordID)
	if err != nil {
		return g.unavailable()
	}
	switch {
	case !res.Linked:
		return Decision{Outcome: NotLinked, Ephemeral: true, Message: fmt.Sprintf(
			"To talk to Pakka here, link your Discord account to your OpenPak account first: %s "+
				"It only takes a minute, and it lets Pakka follow the same rules as the website.", g.linkURL())}
	case res.Account.ID == "" || res.SupervisedOrMinor == nil || *res.SupervisedOrMinor:
		// Unknown age or missing field is treated as a child: never answer.
		return Decision{Outcome: Child, Ephemeral: true, Message: "Sorry, Pakka can't chat with this account. " +
			"Thanks for understanding. The guides on docs.openpak.org and the community channels are still here for you."}
	case res.NoticeAccepted == nil || !*res.NoticeAccepted:
		return Decision{Outcome: NoticeRequired, Ephemeral: true, Message: fmt.Sprintf(
			"Before Pakka answers, please read and accept this notice: %s\nAccept it on the website: %s", NoticeText, g.noticeURL())}
	}
	g.store(discordID, res.Account.ID)
	return Decision{Outcome: Allowed, AccountID: res.Account.ID}
}

// Func adapts the gate to a plain callback for transports that should not
// import this package's types: it returns the refusal message and false, or
// "" and true when allowed. It uses a 6 second budget per check.
func (g *Gate) Func() func(discordID string) (string, bool) {
	return func(discordID string) (string, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		d := g.Check(ctx, discordID)
		return d.Message, d.Allowed()
	}
}

func (g *Gate) unavailable() Decision {
	return Decision{Outcome: Unavailable, Ephemeral: true,
		Message: "Pakka can't check your OpenPak account right now, so it can't answer. Please try again in a few minutes."}
}

func (g *Gate) linkURL() string {
	if g.LinkURL != "" {
		return g.LinkURL
	}
	return DefaultLinkURL
}

func (g *Gate) noticeURL() string {
	if g.NoticeURL != "" {
		return g.NoticeURL
	}
	return DefaultNoticeURL
}

func (g *Gate) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

func (g *Gate) ttl() time.Duration {
	if g.TTL == 0 {
		return 30 * time.Second
	}
	return g.TTL
}

func (g *Gate) fromCache(id string) (string, bool) {
	if g.ttl() < 0 {
		return "", false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.cache[id]
	if !ok || !g.clock().Before(c.until) {
		delete(g.cache, id)
		return "", false
	}
	return c.account, true
}

func (g *Gate) store(id, account string) {
	if g.ttl() < 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cache == nil {
		g.cache = map[string]cached{}
	}
	g.cache[id] = cached{account: account, until: g.clock().Add(g.ttl())}
}
