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
	"pakka/internal/eshop"
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
		channels:     map[string]bool{},
		ops:          map[string]bool{},
		modRoles:     map[string]bool{},
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

func TestHandleFAQ(t *testing.T) {
	newRouted := func() *Bot {
		b := testBot()
		b.router = NewRouter(keywordClassifier{}, "help1", "", "bug1", time.Hour, b.log)
		b.faq = NewFAQ("match1", time.Hour, b.log)
		return b
	}
	in := incoming("how do I install the patcher on my sd card")
	in.ChannelID = "help1"
	posts := handle(t, newRouted(), in)
	if len(posts) != 1 || !strings.Contains(posts[0], "switch-patcher/releases") {
		t.Errorf("in-scope faq posts = %v", posts)
	}
	// Out-of-scope redirects win over FAQ answers.
	out := incoming("how do I install the patcher on my sd card")
	out.ChannelID = "bug1"
	posts = handle(t, newRouted(), out)
	if len(posts) != 1 || !strings.Contains(posts[0], "faster help") {
		t.Errorf("out-of-scope posts = %v", posts)
	}
	// Prefixed text never triggers FAQ.
	cmd := incoming("!pakka install the patcher on my sd card")
	cmd.ChannelID = "help1"
	posts = handle(t, newRouted(), cmd)
	if len(posts) != 1 || !strings.Contains(posts[0], "unknown command") {
		t.Errorf("prefixed posts = %v", posts)
	}
}

func TestAckCommands(t *testing.T) {
	newAcked := func() *Bot {
		b := testBot()
		targets := ParseTargets("Core/accounts=https://x.invalid/")
		prober := funcProber(func(_ context.Context, _ Target) (bool, string) { return true, "ok" })
		b.agentMonitor = NewMonitor(targets, time.Minute, prober, &fakeAnnouncer{}, "status1", b.log)
		return b
	}
	posts := handle(t, newAcked(), incoming("!pakka ack"))
	if len(posts) != 1 || !strings.Contains(posts[0], "nothing acked") {
		t.Errorf("bare ack = %v", posts)
	}
	b := newAcked()
	posts = handle(t, b, incoming("!pakka ack accounts 30m"))
	if len(posts) != 1 || !strings.Contains(posts[0], "acked **Core/accounts** for 30m") {
		t.Errorf("ack = %v", posts)
	}
	posts = handle(t, b, incoming("!pakka ack"))
	if len(posts) != 1 || !strings.Contains(posts[0], "Core/accounts") {
		t.Errorf("ack list = %v", posts)
	}
	posts = handle(t, b, incoming("!pakka ack nope"))
	if len(posts) != 1 || !strings.Contains(posts[0], "unknown target") {
		t.Errorf("bad ack = %v", posts)
	}
	posts = handle(t, b, incoming("!pakka unack accounts"))
	if len(posts) != 1 || !strings.Contains(posts[0], "unacked") {
		t.Errorf("unack = %v", posts)
	}
	posts = handle(t, testBot(), incoming("!pakka ack a"))
	if len(posts) != 1 || !strings.Contains(posts[0], "not configured") {
		t.Errorf("ack without monitor = %v", posts)
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

func TestListenChannels(t *testing.T) {
	b := testBot()
	b.channels["c1"] = true
	if posts := handle(t, b, incoming("!pakka help")); len(posts) != 1 {
		t.Errorf("listed channel got %d replies", len(posts))
	}
	other := incoming("!pakka help")
	other.ChannelID = "c2"
	if posts := handle(t, b, other); len(posts) != 0 {
		t.Errorf("unlisted channel got %d replies", len(posts))
	}
}

func TestOpsRestriction(t *testing.T) {
	b := testBot()
	b.ops["ops1"] = true
	if posts := handle(t, b, incoming("!pakka investigate live x")); len(posts) != 1 || !strings.Contains(posts[0], "ops channels") {
		t.Errorf("non-ops investigate: %v", posts)
	}
	ops := incoming("!pakka investigate fixture all-pass x")
	ops.ChannelID = "ops1"
	if posts := handle(t, b, ops); len(posts) != 1 || !strings.Contains(posts[0], "healthy") {
		t.Errorf("ops investigate: %v", posts)
	}
	if posts := handle(t, b, incoming("!pakka run all-pass")); len(posts) != 1 || !strings.Contains(posts[0], "**run**") {
		t.Errorf("run should stay public: %v", posts)
	}
}

func TestRouterWiring(t *testing.T) {
	b := testBot()
	b.router = NewRouter(keywordClassifier{}, "setup1", "conn1", "bug1", time.Hour, nil)
	msg := incoming("cannot connect to lobby at all")
	msg.ChannelID = "setup1"
	if posts := handle(t, b, msg); len(posts) != 1 || !strings.Contains(posts[0], "<#conn1>") {
		t.Errorf("support redirect: %v", posts)
	}
	cmd := incoming("!pakka run all-pass")
	cmd.ChannelID = "setup1"
	if posts := handle(t, b, cmd); len(posts) != 1 || !strings.Contains(posts[0], "**run**") {
		t.Errorf("command in support channel: %v", posts)
	}
}

func TestMentionModeratorsOnly(t *testing.T) {
	b := testBot()
	b.ownID = "999"
	b.modRoles["mod1"] = true
	pleb := incoming("<@999> help")
	if posts := handle(t, b, pleb); len(posts) != 0 {
		t.Errorf("non-mod mention got %d replies", len(posts))
	}
	mod := incoming("<@999> help")
	mod.Roles = []string{"other", "mod1"}
	if posts := handle(t, b, mod); len(posts) != 1 {
		t.Errorf("mod mention got %d replies", len(posts))
	}
	// Prefixed commands are unaffected by the moderator gate.
	if posts := handle(t, b, plebCmd()); len(posts) != 1 {
		t.Errorf("non-mod command got %d replies", len(posts))
	}
}

func plebCmd() Incoming {
	return incoming("!pakka help")
}

func TestPrefixedQuestionRoutesInSupportChannel(t *testing.T) {
	b := testBot()
	b.channels["hackathon"] = true
	b.router = NewRouter(keywordClassifier{}, "setup1", "conn1", "bug1", time.Hour, nil)
	msg := supportMsg("setup1", "u1", "!pakka I am getting 2618-0502 when trying to connect to the ARMS server")
	if posts := handle(t, b, msg); len(posts) != 1 || !strings.Contains(posts[0], "<#conn1>") {
		t.Errorf("prefixed support question not redirected: %v", posts)
	}
	// Known commands in support channels stay gated (silent outside hackathon).
	cmd := supportMsg("setup1", "u2", "!pakka help")
	if posts := handle(t, b, cmd); len(posts) != 0 {
		t.Errorf("gated command got %d replies", len(posts))
	}
	// Unroutable unknown commands still report unknown where allowed.
	b2 := testBot()
	if posts := handle(t, b2, incoming("!pakka frobnicate")); len(posts) != 1 || !strings.Contains(posts[0], "unknown command") {
		t.Errorf("unknown command: %v", posts)
	}
}

func TestNewSurfacesGatewayWarnings(t *testing.T) {
	cfg := config.Config{DiscordPrefix: "!pakka", DiscordToken: "test-token"}
	b, err := New(cfg, diagnostics.NewRunner(cfg), investigate.NewInvestigator(cfg), nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if b.session.LogLevel != discordgo.LogWarning {
		t.Errorf("LogLevel = %d, want LogWarning", b.session.LogLevel)
	}
}

func TestMentionOwnRole(t *testing.T) {
	b := testBot()
	b.ownID = "999"
	b.roleLookup = func(guildID string) ([]string, error) { return []string{"pakkarole"}, nil }
	if posts := handle(t, b, incoming("<@&pakkarole> help")); len(posts) != 1 {
		t.Errorf("own-role mention got %d replies", len(posts))
	}
	if posts := handle(t, b, incoming("<@&otherrole> help")); len(posts) != 0 {
		t.Errorf("foreign-role mention got %d replies", len(posts))
	}
	// Without a lookup, role mentions never match.
	b2 := testBot()
	b2.ownID = "999"
	if posts := handle(t, b2, incoming("<@&pakkarole> help")); len(posts) != 0 {
		t.Errorf("unverifiable role mention got %d replies", len(posts))
	}
}

func TestEshopCommand(t *testing.T) {
	b := testBot()
	if posts := handle(t, b, incoming("!pakka eshop")); len(posts) != 1 || !strings.Contains(posts[0], "not configured") {
		t.Fatalf("unconfigured eshop -> %v", posts)
	}
	var cat eshop.Catalog
	var rel eshop.Releases
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b.SetEshopWatcher(eshop.New(cat, rel, nil, nil, nil, "", "", nil, log))
	posts := handle(t, b, incoming("!pakka eshop"))
	if len(posts) != 1 || !strings.Contains(posts[0], "eShop watch") || !strings.Contains(posts[0], "sync: not configured") {
		t.Fatalf("eshop status -> %v", posts)
	}
}

func TestMentionOwnerBypass(t *testing.T) {
	b := testBot()
	b.ownID = "999"
	b.modRoles["mod1"] = true
	b.ownerLookup = func(guildID string) (string, error) { return "owner1", nil }
	owner := incoming("<@999> help")
	owner.AuthorID = "owner1"
	if posts := handle(t, b, owner); len(posts) != 1 {
		t.Errorf("owner mention got %d replies", len(posts))
	}
	pleb := incoming("<@999> help")
	pleb.AuthorID = "u9"
	if posts := handle(t, b, pleb); len(posts) != 0 {
		t.Errorf("non-owner mention got %d replies", len(posts))
	}
}

func TestMentionAccountGateRefuses(t *testing.T) {
	b := testBot()
	b.ownID = "999"
	b.SetGate(func(string) (string, bool) { return "link your account", false })
	posts := handle(t, b, incoming("<@999> why is my connection slow"))
	if len(posts) != 1 || !strings.Contains(posts[0], "link your account") || !strings.Contains(posts[0], "<@u1>") {
		t.Errorf("gated free text got %q", posts)
	}
	// Commands are not model answers and stay open.
	if posts := handle(t, b, incoming("<@999> help")); len(posts) != 1 || strings.Contains(posts[0], "link your account") {
		t.Errorf("command was gated: %q", posts)
	}
}
