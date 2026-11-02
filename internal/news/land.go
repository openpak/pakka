package news

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"
)

// Lineup channels every landing writes to.
const (
	gamesTopic = "openpak_games"
	newsTopic  = "openpak_news"
)

// Announcer posts a message to a channel.
type Announcer interface {
	Announce(channelID, content string) error
}

// Landing is one game landing request.
type Landing struct {
	Name    string // display name, e.g. "Astroneer"
	TitleID string // optional 16-hex title id (auto-subscribe + game link)
	Blurb   string // optional one-paragraph body; a default is used when empty
}

// LandingResult is a completed landing.
type LandingResult struct {
	TopicID   string   `json:"topic_id"`
	Articles  []uint32 `json:"articles"`
	Build     int      `json:"build"`
	MessageID string   `json:"message_id"`
}

// LandError is a failed landing: the step that failed plus what completed
// before it, so staff can finish or repair by hand.
type LandError struct {
	Step      string
	Completed []string
	Err       error
}

func (e *LandError) Error() string {
	return fmt.Sprintf("landing failed at %s: %s (completed: %s)", e.Step, e.Err, strings.Join(e.Completed, ", "))
}

// AlreadyError means the game's channel already exists: no partial state,
// nothing was posted.
type AlreadyError struct {
	TopicID string
}

func (e *AlreadyError) Error() string {
	return fmt.Sprintf("%s already landed (%s exists)", e.TopicID, e.TopicID)
}

// Lander runs the game-landing flow: a news channel for the game, launch
// articles in OpenPak Games, OpenPak News and the new channel, publish to
// consoles, a Discord announcement, and a staff report.
type Lander struct {
	news              *Client
	staff, public     Announcer
	staffID, publicID string
	log               *slog.Logger
}

// NewLander builds the landing flow.
func NewLander(news *Client, staff, public Announcer, staffID, publicID string, log *slog.Logger) *Lander {
	return &Lander{news: news, staff: staff, public: public, staffID: staffID, publicID: publicID, log: log}
}

// Land runs one landing end to end.
func (l *Lander) Land(ctx context.Context, req Landing) (*LandingResult, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	topic, ok := topicSlug(name)
	if !ok {
		return nil, fmt.Errorf("name %q yields no usable topic id", name)
	}
	tid := strings.TrimSpace(req.TitleID)
	if tid != "" && !validTitleID(tid) {
		return nil, fmt.Errorf("title id %q must be 16 lowercase hex digits", tid)
	}
	blurb := strings.Join(strings.Fields(req.Blurb), " ")
	if blurb == "" {
		blurb = fmt.Sprintf("%s is now available on OpenPak! Connect and play.", name)
	}
	if len(blurb) > 1200 {
		return nil, fmt.Errorf("blurb is %d bytes, at most 1200", len(blurb))
	}
	_, found, err := l.news.GetChannel(ctx, topic)
	if err != nil {
		return nil, &LandError{Step: "lookup", Err: err}
	}
	if found {
		return nil, &AlreadyError{TopicID: topic}
	}
	res := &LandingResult{TopicID: topic}
	var completed []string
	fail := func(step string, err error) (*LandingResult, error) {
		le := &LandError{Step: step, Completed: append([]string{}, completed...), Err: err}
		l.report(fmt.Sprintf("[Games - %s] landing FAILED at %s: %s (completed: %s).",
			name, step, err, noneList(completed)))
		return nil, le
	}
	desc := truncBytes(fmt.Sprintf("News and updates for %s on OpenPak.", name), 120)
	create := ChannelCreate{
		TopicID: topic, Name: truncBytes(name, 100), Publisher: "OpenPak",
		Description: desc, IconMotif: "mark-pad",
	}
	if tid != "" {
		create.ApplicationIDs = []string{tid}
	}
	if _, err := l.news.CreateChannel(ctx, create); err != nil {
		return fail("create-channel", err)
	}
	completed = append(completed, "channel")
	var link *ArticleLink
	if tid != "" {
		link = &ArticleLink{Type: "game", ApplicationIDs: []string{tid}}
	}
	articles := []struct {
		topic, title, body, motif string
		link                      *ArticleLink
	}{
		{gamesTopic, truncBytes(fmt.Sprintf("%s is now available on OpenPak", name), 100), blurb, "pad", link},
		{newsTopic, truncBytes(fmt.Sprintf("Now on OpenPak: %s", name), 100),
			fmt.Sprintf("%s is now available on the OpenPak network. See the OpenPak Games channel for details.", name), "globe", nil},
		{topic, truncBytes(fmt.Sprintf("Welcome to %s on OpenPak", name), 100),
			fmt.Sprintf("This channel carries news and updates for %s on OpenPak.", name), "flag", link},
	}
	for _, a := range articles {
		art, err := l.news.PostArticle(ctx, a.topic, ArticleCreate{
			Title: a.title, Body: a.body, Language: "en-GB",
			SupportedLanguages: []string{"en-GB", "en-US"}, LifetimeDays: 30,
			Priority: 100, ArtMotif: a.motif, Link: a.link,
		})
		if err != nil {
			return fail("article-"+a.topic, err)
		}
		res.Articles = append(res.Articles, art.NewsID)
		completed = append(completed, "article-"+a.topic)
	}
	pub, err := l.news.Publish(ctx, truncBytes("land "+name, 200))
	if err != nil {
		return fail("publish", err)
	}
	res.Build = pub.Build
	completed = append(completed, "publish")
	announce := fmt.Sprintf(":tada: **%s** has landed on OpenPak!\n%s", name, blurb)
	if tid != "" {
		announce += "\nLaunching the game subscribes your console to its news channel."
	} else {
		announce += fmt.Sprintf("\nSubscribe to the %s channel in News to follow updates.", name)
	}
	if err := l.public.Announce(l.publicID, announce); err != nil {
		return fail("announce", err)
	}
	completed = append(completed, "announce")
	l.report(fmt.Sprintf("[Games - %s] landed: channel %s, %d articles, consoles build %d.",
		name, topic, len(res.Articles), res.Build))
	return res, nil
}

func (l *Lander) report(msg string) {
	if l.staffID == "" {
		return
	}
	if err := l.staff.Announce(l.staffID, msg); err != nil {
		l.log.Error("landing staff report failed", "err", err)
	}
}

func noneList(done []string) string {
	if len(done) == 0 {
		return "nothing"
	}
	return strings.Join(done, ", ")
}

// topicSlug derives the channel topic id from a display name.
func topicSlug(name string) (string, bool) {
	var sb strings.Builder
	prevUnderscore := true // trim leading separators
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
			prevUnderscore = false
		default:
			if !prevUnderscore {
				sb.WriteRune('_')
				prevUnderscore = true
			}
		}
	}
	s := strings.Trim(sb.String(), "_")
	if len(s) > 23 {
		s = strings.Trim(s[:23], "_")
	}
	if s == "" {
		return "", false
	}
	return "openpak_" + s, true
}

func validTitleID(tid string) bool {
	if len(tid) != 16 {
		return false
	}
	for _, r := range tid {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// truncBytes cuts s to at most n bytes on a rune boundary.
func truncBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
