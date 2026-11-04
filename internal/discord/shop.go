package discord

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"pakka/internal/eshop"
	"pakka/internal/shopcore"
)

// ShopAdmin wires the `shop` commands: one console-neutral surface for every shop Pakka
// manages. Core is shop-core (catalog, runs, pause); Watcher is the Switch update watcher (the
// only console with a sync trigger so far); Trackers are the per-console broken trackers.
type ShopAdmin struct {
	Core     *shopcore.Client
	Watcher  *eshop.Watcher
	Trackers map[string]*eshop.BrokenTracker
	Consoles []string
}

// SetShopAdmin turns on the `shop` commands.
func (b *Bot) SetShopAdmin(a ShopAdmin) { b.shop = &a }

func (a *ShopAdmin) known(console string) bool {
	for _, c := range a.Consoles {
		if c == console {
			return true
		}
	}
	return false
}

const shopUsage = "`shop [console]` status; staff: `shop sync|pause|resume <console>`, " +
	"`shop retire <console> <title> [reason]`, `shop unretire <console> <title>`, " +
	"`shop broken [console]`, `shop runs [console]`"

// cmdShop serves `shop ...`. Status is open to anyone who can run the bot's commands; every
// action and the broken and run listings are staff only.
func (b *Bot) cmdShop(ctx context.Context, in Incoming, args []string) string {
	if b.shop == nil {
		return "shop management is not configured"
	}
	a := b.shop
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	switch sub {
	case "", "status":
		var console string
		if len(args) > 1 {
			console = strings.ToLower(args[1])
		}
		return a.status(ctx, b, console)
	}
	if a.known(sub) { // `shop switch` is `shop status switch`
		return a.status(ctx, b, sub)
	}
	if !b.isStaff(in) {
		return "shop commands other than status are for staff only"
	}
	rest := args[1:]
	console := ""
	if len(rest) > 0 {
		console = strings.ToLower(rest[0])
	}
	needConsole := func() (string, bool) {
		if console == "" {
			return fmt.Sprintf("name a console (%s)", strings.Join(a.Consoles, ", ")), false
		}
		if !a.known(console) {
			return fmt.Sprintf("unknown console %q (managed: %s)", console, strings.Join(a.Consoles, ", ")), false
		}
		return "", true
	}
	actor := "discord:" + in.AuthorID
	switch sub {
	case "sync":
		if msg, ok := needConsole(); !ok {
			return msg
		}
		if console != "switch" || a.Watcher == nil {
			return fmt.Sprintf("no sync is wired for %s on this Pakka instance (the %s runner arrives with its phase P5)", console, consoleName(console))
		}
		started, note := a.Watcher.RequestSync("staff request by "+in.AuthorID, func(msg string) { b.reply(b.RedactingSender(), in.ChannelID, msg) })
		if started {
			return "[Switch - eshop] sync started"
		}
		return "[Switch - eshop] sync not started: " + note
	case "pause", "resume":
		if msg, ok := needConsole(); !ok {
			return msg
		}
		if a.Core == nil {
			return "shop-core is not configured"
		}
		if err := a.Core.Pause(ctx, actor, console, sub == "pause"); err != nil {
			return "failed: " + err.Error()
		}
		return fmt.Sprintf("%s syncs %sd", console, sub)
	case "retire", "unretire":
		if msg, ok := needConsole(); !ok {
			return msg
		}
		if a.Core == nil {
			return "shop-core is not configured"
		}
		if len(rest) < 2 {
			return fmt.Sprintf("usage: `shop %s <console> <title id or name>%s`", sub, map[bool]string{true: " [reason]"}[sub == "retire"])
		}
		id, msg := a.resolveTitle(ctx, console, rest[1])
		if id == "" {
			return msg
		}
		if sub == "retire" {
			reason := strings.TrimSpace(strings.Join(rest[2:], " "))
			if reason == "" {
				reason = "retired by staff " + in.AuthorID
			}
			if err := a.Core.Retire(ctx, actor, console, id, reason); err != nil {
				return "failed: " + err.Error()
			}
			return fmt.Sprintf("retired `%s` on %s: hidden from new buyers, current owners keep their download", id, console)
		}
		if err := a.Core.Unretire(ctx, actor, console, id); err != nil {
			return "failed: " + err.Error()
		}
		return fmt.Sprintf("unretired `%s` on %s (still unpublished until published again)", id, console)
	case "broken":
		return a.broken(ctx, console)
	case "runs":
		return a.runs(ctx, console)
	}
	return fmt.Sprintf("unknown shop command %q — %s", sub, shopUsage)
}

// resolveTitle turns a 16-hex id or a unique name fragment into a title id.
func (a *ShopAdmin) resolveTitle(ctx context.Context, console, q string) (string, string) {
	ts, err := a.Core.Titles(ctx, console, false)
	if err != nil {
		return "", "failed: " + err.Error()
	}
	q = strings.ToLower(q)
	var hits []shopcore.Title
	for _, t := range ts {
		if t.TitleID == q {
			return t.TitleID, ""
		}
		if strings.Contains(strings.ToLower(t.Name), q) {
			hits = append(hits, t)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0].TitleID, ""
	case 0:
		return "", fmt.Sprintf("no %s title matches %q", console, q)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d titles match %q, use the id:", len(hits), q)
	for i, t := range hits {
		if i == 8 {
			sb.WriteString("\n…")
			break
		}
		fmt.Fprintf(&sb, "\n`%s` %s", t.TitleID, t.Name)
	}
	return "", sb.String()
}

func (a *ShopAdmin) status(ctx context.Context, b *Bot, only string) string {
	if only != "" && !a.known(only) {
		return fmt.Sprintf("unknown console %q (managed: %s)", only, strings.Join(a.Consoles, ", "))
	}
	var sb strings.Builder
	var cs []shopcore.Console
	var err error
	if a.Core != nil {
		cs, err = a.Core.Consoles(ctx)
	}
	for _, console := range a.Consoles {
		if only != "" && console != only {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		if console == "switch" {
			sb.WriteString(b.cmdEshop())
		} else {
			fmt.Fprintf(&sb, "**%s**", eshop.ConsoleLabel(console, consoleName(console)))
		}
		for _, c := range cs {
			if c.Console != console {
				continue
			}
			fmt.Fprintf(&sb, "\ncatalog: %d titles, %d published, %d retired, %d broken", c.Titles, c.Published, c.Retired, c.Broken)
			if c.Paused {
				sb.WriteString("; **syncs paused**")
			}
		}
		if t := a.Trackers[console]; t != nil {
			if snap := t.Snapshot(); len(snap) > 0 {
				fmt.Fprintf(&sb, "\nbroken watch: %d package(s) failing", len(snap))
			}
		}
	}
	if err != nil {
		fmt.Fprintf(&sb, "\nshop-core unreachable: %v", err)
	}
	return sb.String()
}

func (a *ShopAdmin) broken(ctx context.Context, only string) string {
	var sb strings.Builder
	for _, console := range a.Consoles {
		if only != "" && console != only {
			continue
		}
		t := a.Trackers[console]
		if t == nil {
			continue
		}
		snap := t.Snapshot()
		fmt.Fprintf(&sb, "**%s** — %d failing\n", consoleName(console), len(snap))
		names := make([]string, 0, len(snap))
		for p := range snap {
			names = append(names, p)
		}
		sort.Strings(names)
		for i, p := range names {
			if i == 15 {
				fmt.Fprintf(&sb, "… and %d more\n", len(names)-15)
				break
			}
			fmt.Fprintf(&sb, "`%s` since %s\n", p, snap[p].UTC().Format("2006-01-02"))
		}
	}
	if sb.Len() == 0 {
		return "no matching console"
	}
	return strings.TrimSpace(sb.String())
}

func (a *ShopAdmin) runs(ctx context.Context, only string) string {
	if a.Core == nil {
		return "shop-core is not configured"
	}
	var sb strings.Builder
	for _, console := range a.Consoles {
		if only != "" && console != only {
			continue
		}
		rs, err := a.Core.Runs(ctx, console, 5)
		if err != nil {
			fmt.Fprintf(&sb, "**%s** — failed: %v\n", console, err)
			continue
		}
		fmt.Fprintf(&sb, "**%s** — last %d run(s)\n", console, len(rs))
		for _, r := range rs {
			fmt.Fprintf(&sb, "`%d` %s %s %s", r.ID, r.Step, r.State, r.Started.UTC().Format(time.RFC3339))
			if r.Summary != "" {
				fmt.Fprintf(&sb, " — %s", truncateLine(r.Summary, 120))
			}
			sb.WriteString("\n")
		}
	}
	if sb.Len() == 0 {
		return "no matching console"
	}
	return strings.TrimSpace(sb.String())
}

// consoleName is a console's display name.
func consoleName(console string) string {
	switch console {
	case "switch":
		return "Switch"
	case "wiiu":
		return "Wii U"
	case "ps3":
		return "PS3"
	}
	return console
}

func truncateLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
