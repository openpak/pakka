// Package discord connects a Discord bot to the shared harness core:
// !pakka run and !pakka investigate execute the same in-process runner
// and investigator the HTTP API uses.
package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"pakka/internal/config"
	"pakka/internal/diagnostics"
	"pakka/internal/investigate"
	"pakka/internal/meta"
)

// maxMessage is Discord's message length limit.
const maxMessage = 2000

// Sender posts replies; *discordgo.Session satisfies it.
type Sender interface {
	ChannelMessageSend(channelID, content string, options ...discordgo.RequestOption) (*discordgo.Message, error)
}

// Incoming is the handler input, decoupled from the gateway event.
type Incoming struct {
	AuthorID  string
	AuthorBot bool
	GuildID   string
	ChannelID string
	Roles     []string
	Content   string
}

// Bot serves Pakka commands on Discord.
type Bot struct {
	session      *discordgo.Session
	runner       *diagnostics.Runner
	investigator *investigate.Investigator
	model        investigate.Model // nil without API key; live investigate unavailable
	prefix       string
	guilds       map[string]bool // empty = all guilds
	channels     map[string]bool // empty = all channels
	ops          map[string]bool // empty = investigate unrestricted
	modRoles     map[string]bool // empty = mentions unrestricted
	router       *Router         // nil = support routing disabled
	ownID        string
	redactExtra  []string // internal endpoints/hostnames, never posted to chat
	log          *slog.Logger
}

// New builds the bot (no network yet). metaClient may be nil; support
// routing then classifies by keywords alone.
func New(cfg config.Config, runner *diagnostics.Runner, inv *investigate.Investigator, model investigate.Model, metaClient *meta.Client, log *slog.Logger) (*Bot, error) {
	s, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, err
	}
	s.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsMessageContent | discordgo.IntentsGuildMembers
	b := &Bot{
		session: s, runner: runner, investigator: inv, model: model,
		prefix: cfg.DiscordPrefix, guilds: toSet(cfg.DiscordGuilds),
		channels: toSet(cfg.DiscordChannels), ops: toSet(cfg.DiscordOpsChannels),
		modRoles:    toSet(cfg.DiscordModRoles),
		router:      NewRouterFromConfig(cfg, metaClient),
		redactExtra: RedactExtras(cfg),
		log:         log,
	}
	s.AddHandler(b.onMessage)
	return b, nil
}

func toSet(ids []string) map[string]bool {
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func hasAnyRole(allowed map[string]bool, roles []string) bool {
	for _, r := range roles {
		if allowed[r] {
			return true
		}
	}
	return false
}

// Session exposes the gateway session for the monitor and welcomer senders.
func (b *Bot) Session() *discordgo.Session { return b.session }

// Start opens the gateway connection.
func (b *Bot) Start() error {
	if err := b.session.Open(); err != nil {
		return err
	}
	if u := b.session.State.User; u != nil {
		b.ownID = u.ID
	}
	return nil
}

// Stop closes the gateway connection.
func (b *Bot) Stop() error {
	if b.session == nil {
		return nil
	}
	return b.session.Close()
}

func (b *Bot) onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	b.Handle(context.Background(), s, Incoming{
		AuthorID:  m.Author.ID,
		AuthorBot: m.Author.Bot,
		GuildID:   m.GuildID,
		ChannelID: m.ChannelID,
		Roles:     memberRoles(m.Member),
		Content:   m.Content,
	})
}

// memberRoles reads guild role IDs off the gateway event (nil-safe: DMs
// carry no member, and can never satisfy a moderator gate).
func memberRoles(m *discordgo.Member) []string {
	if m == nil {
		return nil
	}
	return m.Roles
}

// Handle parses one message and replies. Pure logic over Sender: unit-testable.
func (b *Bot) Handle(ctx context.Context, sender Sender, in Incoming) {
	if in.AuthorBot {
		return
	}
	if b.ownID != "" && in.AuthorID == b.ownID {
		return
	}
	if len(b.guilds) > 0 && !b.guilds[in.GuildID] {
		return
	}
	content := strings.TrimSpace(in.Content)
	if rest, ok := b.stripMention(content); ok {
		b.handleMention(ctx, sender, in, rest)
		return
	}
	if msg, ok := b.route(in, content); ok {
		b.reply(sender, in.ChannelID, msg)
		return
	}
	if !strings.HasPrefix(content, b.prefix) {
		return
	}
	// Command channels gate prefixed commands only; support routing above
	// stays independent so it keeps working in its own channels.
	if len(b.channels) > 0 && !b.channels[in.ChannelID] {
		return
	}
	args := strings.Fields(strings.TrimSpace(strings.TrimPrefix(content, b.prefix)))
	if len(args) == 0 {
		b.reply(sender, in.ChannelID, usage(b.prefix))
		return
	}
	b.runCommand(ctx, sender, in, args)
}

// stripMention removes a leading @pakka mention; ok=false when absent.
func (b *Bot) stripMention(content string) (rest string, ok bool) {
	if b.ownID == "" {
		return "", false
	}
	for _, prefix := range []string{"<@" + b.ownID + ">", "<@!" + b.ownID + ">"} {
		if r, found := strings.CutPrefix(content, prefix); found {
			return strings.TrimSpace(r), true
		}
	}
	return "", false
}

// handleMention answers a direct @pakka address: known commands run as-is,
// anything else becomes a live investigation of the message text.
func (b *Bot) handleMention(ctx context.Context, sender Sender, in Incoming, rest string) {
	if len(b.channels) > 0 && !b.channels[in.ChannelID] {
		return
	}
	// Mentions are the privileged path: moderators only when roles are set.
	if len(b.modRoles) > 0 && !hasAnyRole(b.modRoles, in.Roles) {
		return
	}
	args := strings.Fields(rest)
	if len(args) == 0 {
		b.reply(sender, in.ChannelID, usage(b.prefix))
		return
	}
	switch args[0] {
	case "help", "run", "investigate":
		b.runCommand(ctx, sender, in, args)
	default:
		b.runCommand(ctx, sender, in, append([]string{"investigate", "live"}, args...))
	}
}

// runCommand dispatches one parsed command and replies.
func (b *Bot) runCommand(ctx context.Context, sender Sender, in Incoming, args []string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	var out string
	switch args[0] {
	case "help":
		out = usage(b.prefix)
	case "run":
		out = b.cmdRun(ctx, args[1:])
	case "investigate":
		if len(b.ops) > 0 && !b.ops[in.ChannelID] {
			out = "investigate is only available in ops channels"
		} else {
			out = b.cmdInvestigate(ctx, args[1:])
		}
	default:
		out = fmt.Sprintf("unknown command %q (try `%s help`)", args[0], b.prefix)
	}
	b.reply(sender, in.ChannelID, out)
}

// route redirects out-of-scope support questions; false = handle normally.
func (b *Bot) route(in Incoming, content string) (string, bool) {
	if b.router == nil || strings.HasPrefix(content, b.prefix) {
		return "", false
	}
	return b.router.Route(in)
}

func usage(prefix string) string {
	return fmt.Sprintf("**Pakka** — connection diagnostics\n"+
		"`%s run [scenario] [fixture|live]` — run checks (default: all-pass fixture)\n"+
		"`%s investigate [fixture [scenario]|live] <symptoms>` — diagnose a problem (ops channels only)",
		prefix, prefix)
}

// cmdRun executes a diagnostic suite: `run [scenario] [fixture|live]`.
func (b *Bot) cmdRun(ctx context.Context, args []string) string {
	scenario, mode := "all-pass", diagnostics.ModeFixture
	for _, a := range args {
		if a == string(diagnostics.ModeFixture) || a == string(diagnostics.ModeLive) {
			mode = diagnostics.Mode(a)
			continue
		}
		scenario = a
	}
	run, err := b.runner.Run(ctx, mode, scenario, nil)
	if err != nil {
		return "run failed: " + err.Error()
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "**run** %s/%s\n", run.Mode, run.Scenario)
	for _, r := range run.Results {
		fmt.Fprintf(&sb, "`%s` %s — %s\n", r.CheckID, r.Outcome, r.Summary)
	}
	return sb.String()
}

// cmdInvestigate diagnoses symptoms: `investigate [fixture [scenario]|live] <symptoms>`.
func (b *Bot) cmdInvestigate(ctx context.Context, args []string) string {
	mode, scenario, rest := diagnostics.ModeFixture, "all-pass", args
	if len(rest) > 0 && rest[0] == string(diagnostics.ModeLive) {
		mode, rest = diagnostics.ModeLive, rest[1:]
	} else if len(rest) > 0 && rest[0] == string(diagnostics.ModeFixture) {
		rest = rest[1:]
		if len(rest) > 0 {
			if _, ok := investigate.ScriptFor(rest[0]); ok {
				scenario, rest = rest[0], rest[1:]
			}
		}
	}
	symptoms := strings.Join(rest, " ")
	if symptoms == "" {
		symptoms = "unspecified connection problem"
	}
	var model investigate.Model
	switch mode {
	case diagnostics.ModeFixture:
		script, ok := investigate.ScriptFor(scenario)
		if !ok {
			return fmt.Sprintf("unknown scenario %q", scenario)
		}
		model = script
	case diagnostics.ModeLive:
		if b.model == nil {
			return "live investigations need a Meta API key"
		}
		model = b.model
	}
	finding, results, err := b.investigator.Run(ctx, model, symptoms)
	if err != nil {
		return "investigation failed: " + err.Error()
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "**%s** — %s\n", finding.Diagnosis, finding.Summary)
	if len(finding.Evidence) > 0 {
		fmt.Fprintf(&sb, "evidence: `%s`\n", strings.Join(finding.Evidence, "`, `"))
	}
	if finding.RecommendedNext != "" {
		fmt.Fprintf(&sb, "next: %s\n", finding.RecommendedNext)
	}
	for _, r := range results {
		fmt.Fprintf(&sb, "`%s` %s\n", r.CheckID, r.Outcome)
	}
	return sb.String()
}

func (b *Bot) reply(sender Sender, channelID, content string) {
	content = truncate(RedactWith(content, b.redactExtra))
	if _, err := sender.ChannelMessageSend(channelID, content, discordgo.WithContext(context.Background())); err != nil {
		b.log.Error("discord reply failed", "err", err)
	}
}

// RedactingSender wraps the session so monitor, welcomer, and log-watcher
// announcements are redacted before send.
func (b *Bot) RedactingSender() Sender {
	return Redacting(b.session, b.redactExtra...)
}

// truncate caps output at Discord's message limit (rune-safe).
func truncate(s string) string {
	if len(s) <= maxMessage {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxMessage {
		return s
	}
	return string(runes[:maxMessage-14]) + "\n…(truncated)"
}
