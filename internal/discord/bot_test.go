package discord

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"pakka/internal/config"
	"pakka/internal/diagnostics"
	"pakka/internal/investigate"
)

type fakeSender struct {
	posts []string
}

func (f *fakeSender) ChannelMessageSend(channelID, content string, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	f.posts = append(f.posts, content)
	return &discordgo.Message{ChannelID: channelID, Content: content}, nil
}

func testBot() *Bot {
	cfg := config.Config{CheckTimeout: 5 * time.Second, InvestigateSteps: 5, DiscordPrefix: "!pakka"}
	return &Bot{
		runner:       diagnostics.NewRunner(cfg),
		investigator: investigate.NewInvestigator(cfg),
		model:        nil,
		prefix:       "!pakka",
		guilds:       map[string]bool{},
		log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func handle(t *testing.T, b *Bot, in Incoming) []string {
	t.Helper()
	sender := &fakeSender{}
	b.Handle(context.Background(), sender, in)
	return sender.posts
}

func incoming(content string) Incoming {
	return Incoming{AuthorID: "u1", GuildID: "g1", ChannelID: "c1", Content: content}
}

func TestIgnoresNonCommands(t *testing.T) {
	b := testBot()
	if posts := handle(t, b, incoming("hello")); len(posts) != 0 {
		t.Errorf("plain chat got %d replies", len(posts))
	}
	botMsg := incoming("!pakka help")
	botMsg.AuthorBot = true
	if posts := handle(t, b, botMsg); len(posts) != 0 {
		t.Errorf("bot message got %d replies", len(posts))
	}
}

func TestHelp(t *testing.T) {
	for _, content := range []string{"!pakka help", "!pakka", "!pakka   "} {
		posts := handle(t, testBot(), incoming(content))
		if len(posts) != 1 || !strings.Contains(posts[0], "diagnostics") {
			t.Errorf("%q -> %v", content, posts)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	posts := handle(t, testBot(), incoming("!pakka frobnicate"))
	if len(posts) != 1 || !strings.Contains(posts[0], "unknown command") {
		t.Errorf("posts = %v", posts)
	}
}

func TestGuildAllowlist(t *testing.T) {
	b := testBot()
	b.guilds["g1"] = true
	if posts := handle(t, b, incoming("!pakka help")); len(posts) != 1 {
		t.Errorf("allowed guild got %d replies", len(posts))
	}
	other := incoming("!pakka help")
	other.GuildID = "g2"
	if posts := handle(t, b, other); len(posts) != 0 {
		t.Errorf("blocked guild got %d replies", len(posts))
	}
}

func TestRunFixture(t *testing.T) {
	posts := handle(t, testBot(), incoming("!pakka run local-misconfig"))
	if len(posts) != 1 {
		t.Fatalf("posts = %v", posts)
	}
	if !strings.Contains(posts[0], "local-misconfig") || !strings.Contains(posts[0], "dns.resolve") {
		t.Errorf("posts = %v", posts)
	}
}

func TestRunUnknownScenario(t *testing.T) {
	posts := handle(t, testBot(), incoming("!pakka run nope"))
	if len(posts) != 1 || !strings.Contains(posts[0], "run failed") {
		t.Errorf("posts = %v", posts)
	}
}

func TestInvestigateFixture(t *testing.T) {
	posts := handle(t, testBot(), incoming("!pakka investigate fixture service-outage cannot connect"))
	if len(posts) != 1 {
		t.Fatalf("posts = %v", posts)
	}
	if !strings.Contains(posts[0], "service-outage") {
		t.Errorf("posts = %v", posts)
	}
}

func TestInvestigateLiveWithoutKey(t *testing.T) {
	posts := handle(t, testBot(), incoming("!pakka investigate live cannot connect"))
	if len(posts) != 1 || !strings.Contains(posts[0], "API key") {
		t.Errorf("posts = %v", posts)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short"); got != "short" {
		t.Errorf("short changed: %q", got)
	}
	long := truncate(strings.Repeat("x", 3000))
	if n := len([]rune(long)); n > 2000 {
		t.Errorf("truncated to %d runes", n)
	}
	if !strings.Contains(long, "truncated") {
		t.Errorf("missing marker: %q", long[len(long)-20:])
	}
}
