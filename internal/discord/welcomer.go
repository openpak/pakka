package discord

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// MemberLister lists guild members page by page.
type MemberLister interface {
	MembersAfter(after string, limit int) ([]*discordgo.Member, error)
}

// guildLister lists members of one guild via the gateway session.
type guildLister struct {
	session *discordgo.Session
	guildID string
}

// NewGuildLister binds member listing to a guild.
func NewGuildLister(s *discordgo.Session, guildID string) MemberLister {
	return guildLister{session: s, guildID: guildID}
}

// MembersAfter implements MemberLister.
func (l guildLister) MembersAfter(after string, limit int) ([]*discordgo.Member, error) {
	return l.session.GuildMembers(l.guildID, after, limit)
}

// DMOpener opens a DM channel with a user.
type DMOpener interface {
	UserChannelCreate(recipientID string, options ...discordgo.RequestOption) (*discordgo.Channel, error)
}

var _ DMOpener = (*discordgo.Session)(nil)

// Welcomer greets newcomers on an interval. Only members who joined since
// the previous pass are greeted; the first pass only baselines, so
// restarts never re-welcome the whole server. With a DM opener set, each
// newcomer gets setup links by DM and only DM failures fall back to a
// channel tag; without one, everyone is tagged in the welcome channel.
type Welcomer struct {
	lister      MemberLister
	sender      Sender
	channelID   string
	interval    time.Duration
	prefix      string
	dm          DMOpener
	helpChannel string
	lastCheck   time.Time
	log         *slog.Logger
}

// NewWelcomer builds the welcomer.
func NewWelcomer(lister MemberLister, sender Sender, channelID string, interval time.Duration, prefix string, log *slog.Logger) *Welcomer {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	return &Welcomer{
		lister: lister, sender: sender, channelID: channelID,
		interval: interval, prefix: prefix, lastCheck: time.Now().UTC(), log: log,
	}
}

// WithDM greets newcomers by DM (setup links), falling back to channel
// tags when a DM fails. helpChannel links the DM's support pointer.
func (w *Welcomer) WithDM(dm DMOpener, helpChannel string) *Welcomer {
	w.dm = dm
	w.helpChannel = helpChannel
	return w
}

// Run greets newcomers until ctx ends.
func (w *Welcomer) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Pass(ctx)
		}
	}
}

// Pass performs a single welcome pass (used by tests).
func (w *Welcomer) Pass(_ context.Context) {
	start := time.Now().UTC()
	var newcomers []*discordgo.Member
	after := ""
	for {
		page, err := w.lister.MembersAfter(after, 1000)
		if err != nil {
			w.log.Error("welcome member list failed", "err", err)
			return
		}
		if len(page) == 0 {
			break
		}
		for _, m := range page {
			if m == nil || m.User == nil || m.User.Bot {
				continue
			}
			if m.JoinedAt.After(w.lastCheck) {
				newcomers = append(newcomers, m)
			}
		}
		if len(page) < 1000 {
			break
		}
		after = page[len(page)-1].User.ID
	}
	w.lastCheck = start
	if len(newcomers) == 0 {
		return
	}
	sort.Slice(newcomers, func(i, j int) bool { return newcomers[i].JoinedAt.Before(newcomers[j].JoinedAt) })
	pending := newcomers
	if w.dm != nil {
		var fallback []*discordgo.Member
		for _, m := range newcomers {
			if !w.dmOne(m) {
				fallback = append(fallback, m)
			}
		}
		pending = fallback
	}
	if len(pending) == 0 {
		return
	}
	tags := make([]string, 0, len(pending))
	for _, m := range pending {
		tags = append(tags, "<@"+m.User.ID+">")
	}
	msg := fmt.Sprintf("Welcome %s to the server! :wave: If your connection misbehaves, try `%s help`.",
		strings.Join(tags, " "), w.prefix)
	if _, err := w.sender.ChannelMessageSend(w.channelID, truncate(msg)); err != nil {
		w.log.Error("welcome post failed", "err", err)
	}
}

// dmOne sends one newcomer the setup DM; false means tag them in-channel.
func (w *Welcomer) dmOne(m *discordgo.Member) bool {
	dm, err := w.dm.UserChannelCreate(m.User.ID)
	if err != nil {
		w.log.Info("welcome dm unavailable, tagging in channel", "user", m.User.ID)
		return false
	}
	if _, err := w.sender.ChannelMessageSend(dm.ID, truncate(w.dmText())); err != nil {
		w.log.Warn("welcome dm send failed, tagging in channel", "user", m.User.ID, "err", err)
		return false
	}
	return true
}

// dmText renders the setup DM.
func (w *Welcomer) dmText() string {
	help := "#help"
	if w.helpChannel != "" {
		help = "<#" + w.helpChannel + ">"
	}
	return fmt.Sprintf(`Hey, welcome to OpenPak! :wave:
Get online in three steps:
1. Grab your console's patcher: Switch: %s · Wii U: %s · 3DS: %s
2. Follow the instructions on its releases page to patch and connect.
3. Stuck? Ask in %s — and come say hi in your console's platform channel!`,
		switchPatcherURL, wiiuPatcherURL, dsPatcherURL, help)
}
