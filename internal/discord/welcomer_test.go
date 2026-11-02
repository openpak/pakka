package discord

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

type fakeLister struct {
	members []*discordgo.Member
	err     error
}

func (f fakeLister) MembersAfter(_ string, _ int) ([]*discordgo.Member, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.members, nil
}

func member(id string, joined time.Time, bot bool) *discordgo.Member {
	return &discordgo.Member{User: &discordgo.User{ID: id, Bot: bot}, JoinedAt: joined}
}

func TestWelcomeNewcomers(t *testing.T) {
	now := time.Now().UTC()
	lister := fakeLister{members: []*discordgo.Member{
		member("old", now.Add(-2*time.Hour), false),
		member("new1", now.Add(-time.Minute), false),
		member("newbot", now.Add(-time.Minute), true),
	}}
	sender := &fakeSender{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWelcomer(lister, sender, "welcome1", time.Minute, "!pakka", log)
	w.lastCheck = now.Add(-30 * time.Minute)
	w.Pass(context.Background())
	if len(sender.posts) != 1 {
		t.Fatalf("posts = %v", sender.posts)
	}
	post := sender.posts[0]
	if !strings.Contains(post, "<@new1>") {
		t.Errorf("newcomer not tagged: %q", post)
	}
	if strings.Contains(post, "old") || strings.Contains(post, "newbot") {
		t.Errorf("wrong members greeted: %q", post)
	}
}

func TestWelcomeNone(t *testing.T) {
	now := time.Now().UTC()
	lister := fakeLister{members: []*discordgo.Member{member("old", now.Add(-2*time.Hour), false)}}
	sender := &fakeSender{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWelcomer(lister, sender, "welcome1", time.Minute, "!pakka", log)
	w.lastCheck = now.Add(-30 * time.Minute)
	w.Pass(context.Background())
	if len(sender.posts) != 0 {
		t.Errorf("posts = %v", sender.posts)
	}
}

func TestWelcomeListError(t *testing.T) {
	lister := fakeLister{err: errors.New("boom")}
	sender := &fakeSender{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWelcomer(lister, sender, "welcome1", time.Minute, "!pakka", log)
	w.Pass(context.Background())
	if len(sender.posts) != 0 {
		t.Errorf("posts = %v", sender.posts)
	}
}

type channelPost struct {
	channel, content string
}

type channelSender struct {
	posts []channelPost
}

func (s *channelSender) ChannelMessageSend(channelID, content string, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	s.posts = append(s.posts, channelPost{channelID, content})
	return &discordgo.Message{ChannelID: channelID, Content: content}, nil
}

type fakeDM struct {
	fail map[string]bool
}

func (f fakeDM) UserChannelCreate(id string, _ ...discordgo.RequestOption) (*discordgo.Channel, error) {
	if f.fail[id] {
		return nil, errors.New("dms closed")
	}
	return &discordgo.Channel{ID: "dm-" + id}, nil
}

func TestWelcomeDM(t *testing.T) {
	now := time.Now().UTC()
	lister := fakeLister{members: []*discordgo.Member{member("new1", now.Add(-time.Minute), false)}}
	sender := &channelSender{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWelcomer(lister, sender, "welcome1", time.Minute, "!pakka", log).WithDM(fakeDM{}, "help9")
	w.lastCheck = now.Add(-30 * time.Minute)
	w.Pass(context.Background())
	if len(sender.posts) != 1 {
		t.Fatalf("posts = %v", sender.posts)
	}
	got := sender.posts[0]
	if got.channel != "dm-new1" {
		t.Errorf("dm went to %q, want dm-new1", got.channel)
	}
	if !strings.Contains(got.content, "switch-patcher/releases") || !strings.Contains(got.content, "<#help9>") {
		t.Errorf("dm missing setup links: %q", got.content)
	}
}

func TestWelcomeDMFallback(t *testing.T) {
	now := time.Now().UTC()
	lister := fakeLister{members: []*discordgo.Member{
		member("dmok", now.Add(-time.Minute), false),
		member("dmclosed", now.Add(-time.Minute), false),
	}}
	sender := &channelSender{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := NewWelcomer(lister, sender, "welcome1", time.Minute, "!pakka", log).
		WithDM(fakeDM{fail: map[string]bool{"dmclosed": true}}, "help9")
	w.lastCheck = now.Add(-30 * time.Minute)
	w.Pass(context.Background())
	if len(sender.posts) != 2 {
		t.Fatalf("posts = %v", sender.posts)
	}
	var dm, tag bool
	for _, p := range sender.posts {
		dm = dm || p.channel == "dm-dmok"
		tag = tag || (p.channel == "welcome1" && strings.Contains(p.content, "<@dmclosed>"))
	}
	if !dm || !tag {
		t.Errorf("want one dm plus one fallback tag: %v", sender.posts)
	}
}
