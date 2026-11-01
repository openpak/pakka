package discord

import (
	"context"
	"fmt"
	"strings"

	"pakka/internal/publisher"
)

// PublisherAdmin is what the staff commands need from the publisher.
type PublisherAdmin interface {
	Queue() *publisher.Queue
	Approve(ctx context.Context, reviewer publisher.Actor, id string) (publisher.Draft, error)
	Reject(reviewer publisher.Actor, id string) (publisher.Draft, error)
	Killed() bool
	ExecModelTool(ctx context.Context, name, arguments string) string
}

// SetPublisher turns on the `publish` staff commands.
func (b *Bot) SetPublisher(p PublisherAdmin) { b.publisher = p }

// isStaff: a member with one of the moderator roles, or the guild owner. With
// no moderator role configured only the owner passes; publishing is never open.
func (b *Bot) isStaff(in Incoming) bool {
	if in.GuildID == "" || in.AuthorID == "" {
		return false
	}
	return hasAnyRole(b.modRoles, in.Roles) || b.isOwner(in.GuildID, in.AuthorID)
}

// cmdPublish serves `publish list|show|approve|reject`. Staff only; the
// reply never contains more than the draft's own preview text.
func (b *Bot) cmdPublish(ctx context.Context, in Incoming, args []string) string {
	if b.publisher == nil {
		return "publishing is not configured"
	}
	if !b.isStaff(in) {
		return "publish commands are for staff only"
	}
	if len(args) == 0 {
		args = []string{"list"}
	}
	reviewer := publisher.Staff("discord:" + in.AuthorID)
	q := b.publisher.Queue()
	switch args[0] {
	case "list":
		drafts := q.List(publisher.StatusPending)
		if len(drafts) == 0 {
			return "nothing is waiting for approval"
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "**%d draft(s) waiting**", len(drafts))
		if b.publisher.Killed() {
			sb.WriteString(" (kill switch is ON: approvals are refused)")
		}
		sb.WriteString("\n")
		for _, d := range drafts {
			fmt.Fprintf(&sb, "`%s` %s\n", d.ID, truncate(d.Preview))
		}
		return sb.String()
	case "show":
		if len(args) < 2 {
			return "usage: `publish show <id>`"
		}
		d, ok := q.Get(args[1])
		if !ok {
			return "no such draft"
		}
		return fmt.Sprintf("`%s` %s by %s, status **%s**\n%s", d.ID, d.Action.Tool, d.CreatedBy, d.Status, truncate(d.Preview))
	case "approve":
		if len(args) < 2 {
			return "usage: `publish approve <id>`"
		}
		d, err := b.publisher.Approve(ctx, reviewer, args[1])
		if err != nil {
			return "not published: " + err.Error()
		}
		out := fmt.Sprintf("published `%s`", d.ID)
		if d.Result != nil {
			out += ": " + d.Result.Status
			if u := d.Result.Refs["url"]; u != "" {
				out += " " + u
			}
		}
		return out
	case "reject":
		if len(args) < 2 {
			return "usage: `publish reject <id>`"
		}
		if _, err := b.publisher.Reject(reviewer, args[1]); err != nil {
			return "not rejected: " + err.Error()
		}
		return "rejected `" + args[1] + "`"
	}
	return "usage: `publish list | show <id> | approve <id> | reject <id>`"
}
