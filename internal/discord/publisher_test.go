package discord

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"pakka/internal/publisher"
)

type discordOV struct{ posts int }

func (d *discordOV) Post(context.Context, publisher.PostRequest) (publisher.PostResult, error) {
	d.posts++
	return publisher.PostResult{ID: "p", URL: "https://verse.test/posts/p", Created: true}, nil
}
func (d *discordOV) EnsureCommunity(context.Context, publisher.CommunityRequest) (publisher.CommunityResult, error) {
	return publisher.CommunityResult{}, nil
}
func (d *discordOV) FindCommunity(context.Context, publisher.CommunityRef) (publisher.CommunityResult, bool, error) {
	return publisher.CommunityResult{}, false, nil
}
func (d *discordOV) DeletePost(context.Context, string) error { return nil }
func (d *discordOV) Top(context.Context, int, int) ([]publisher.TopPost, error) {
	return nil, nil
}

func pubBot(t *testing.T) (*Bot, *discordOV, string) {
	t.Helper()
	ov := &discordOV{}
	svc, err := publisher.New(publisher.Config{Categories: map[string]bool{publisher.CatAnnounce: true}, OpenVerseURL: "x", OpenVerseKey: "k", StateDir: t.TempDir()},
		ov, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	out, err := svc.Submit(context.Background(), publisher.Model(), publisher.Action{Tool: publisher.ToolPost, Category: publisher.CatAnnounce, Body: "Hi", Community: publisher.CommunityRef{Name: "Announcements"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	b := testBot()
	b.modRoles = map[string]bool{"staff": true}
	b.SetPublisher(svc)
	return b, ov, out.DraftID
}

func TestPublishCommandsAreStaffOnly(t *testing.T) {
	b, ov, id := pubBot(t)
	for name, in := range map[string]Incoming{
		"no role":  {AuthorID: "u1", GuildID: "g", ChannelID: "c", Roles: []string{"member"}},
		"in a DM":  {AuthorID: "u1", ChannelID: "c", Roles: []string{"staff"}},
		"no roles": {AuthorID: "u1", GuildID: "g", ChannelID: "c"},
	} {
		in.Content = "!pakka publish approve " + id
		got := strings.Join(handle(t, b, in), "\n")
		if !strings.Contains(got, "staff only") {
			t.Fatalf("%s: %q", name, got)
		}
	}
	if ov.posts != 0 {
		t.Fatal("a non-staff member published")
	}
}

func TestPublishListApproveRejectAsStaff(t *testing.T) {
	b, ov, id := pubBot(t)
	staff := Incoming{AuthorID: "u2", GuildID: "g", ChannelID: "c", Roles: []string{"staff"}}
	staff.Content = "!pakka publish list"
	if got := strings.Join(handle(t, b, staff), ""); !strings.Contains(got, id) {
		t.Fatalf("list: %q", got)
	}
	staff.Content = "!pakka publish approve " + id
	if got := strings.Join(handle(t, b, staff), ""); !strings.Contains(got, "published") || ov.posts != 1 {
		t.Fatalf("approve: %q posts=%d", got, ov.posts)
	}
	staff.Content = "!pakka publish approve " + id
	if got := strings.Join(handle(t, b, staff), ""); !strings.Contains(got, "not published") || ov.posts != 1 {
		t.Fatalf("second approve: %q posts=%d", got, ov.posts)
	}
	staff.Content = "!pakka publish reject nope"
	if got := strings.Join(handle(t, b, staff), ""); !strings.Contains(got, "not rejected") {
		t.Fatalf("reject: %q", got)
	}
}

func TestPublishUnconfigured(t *testing.T) {
	b := testBot()
	b.modRoles = map[string]bool{"staff": true}
	got := strings.Join(handle(t, b, Incoming{AuthorID: "u", GuildID: "g", ChannelID: "c", Roles: []string{"staff"}, Content: "!pakka publish list"}), "")
	if !strings.Contains(got, "not configured") {
		t.Fatal(got)
	}
}
