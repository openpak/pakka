package discord

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pakka/internal/eshop"
	"pakka/internal/shopcore"
)

type noAnn struct{}

func (noAnn) Announce(string, string) error { return nil }

func shopBot(t *testing.T, calls *[]string) *Bot {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.Method+" "+r.URL.Path+" actor="+r.Header.Get("X-Shop-Actor"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/shop/consoles":
			_, _ = w.Write([]byte(`{"consoles":[{"console":"wiiu","name":"Wii U","titles":3,"published":2,"retired":1,"broken":0,"paused":true}]}`))
		case r.URL.Path == "/v1/shop/wiiu/titles":
			_, _ = w.Write([]byte(`{"titles":[{"title_id":"00050000fe000001","name":"Gamepad Tester"},{"title_id":"00050000fe000002","name":"Eject Disc"}]}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(ts.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := testBot()
	b.modRoles = map[string]bool{"staff": true}
	b.SetShopAdmin(ShopAdmin{
		Core:     shopcore.New(ts.URL, "k"),
		Consoles: []string{"switch", "wiiu"},
		Trackers: map[string]*eshop.BrokenTracker{
			"wiiu": eshop.NewConsoleBrokenTracker("wiiu", "Wii U - shop", noAnn{}, "s", time.Hour, nil, log),
		},
	})
	return b
}

func TestShopStatusIsOpenActionsAreStaffOnly(t *testing.T) {
	var calls []string
	b := shopBot(t, &calls)
	member := Incoming{AuthorID: "u1", GuildID: "g", ChannelID: "c", Roles: []string{"member"}}
	member.Content = "!pakka shop wiiu"
	got := strings.Join(handle(t, b, member), "\n")
	if !strings.Contains(got, "Wii U - shop") || !strings.Contains(got, "3 titles, 2 published, 1 retired") || !strings.Contains(got, "syncs paused") {
		t.Fatalf("status: %q", got)
	}
	for _, cmd := range []string{"shop pause wiiu", "shop retire wiiu gamepad", "shop broken", "shop runs wiiu", "shop sync wiiu"} {
		member.Content = "!pakka " + cmd
		if got := strings.Join(handle(t, b, member), "\n"); !strings.Contains(got, "staff only") {
			t.Fatalf("%s: %q", cmd, got)
		}
	}
	for _, c := range calls {
		if strings.Contains(c, "POST") || strings.Contains(c, "PUT") {
			t.Fatalf("a non-staff command wrote: %s", c)
		}
	}
}

func TestShopStaffRetireResolvesTitleAndAudits(t *testing.T) {
	var calls []string
	b := shopBot(t, &calls)
	staff := Incoming{AuthorID: "u9", GuildID: "g", ChannelID: "c", Roles: []string{"staff"}}
	staff.Content = "!pakka shop retire wiiu gamepad upstream gone"
	got := strings.Join(handle(t, b, staff), "\n")
	if !strings.Contains(got, "retired `00050000fe000001` on wiiu") || !strings.Contains(got, "owners keep") {
		t.Fatalf("retire: %q", got)
	}
	if last := calls[len(calls)-1]; last != "POST /v1/shop/wiiu/titles/00050000fe000001/retire actor=discord:u9" {
		t.Fatalf("call: %s", last)
	}
	staff.Content = "!pakka shop retire wiiu e"
	if got := strings.Join(handle(t, b, staff), "\n"); !strings.Contains(got, "2 titles match") {
		t.Fatalf("ambiguous: %q", got)
	}
	staff.Content = "!pakka shop pause ps9"
	if got := strings.Join(handle(t, b, staff), "\n"); !strings.Contains(got, "unknown console") {
		t.Fatalf("unknown console: %q", got)
	}
	staff.Content = "!pakka shop sync wiiu"
	if got := strings.Join(handle(t, b, staff), "\n"); !strings.Contains(got, "no sync is wired") {
		t.Fatalf("sync wiiu: %q", got)
	}
}

func TestShopNotConfigured(t *testing.T) {
	b := testBot()
	in := Incoming{AuthorID: "u", GuildID: "g", ChannelID: "c", Content: "!pakka shop"}
	if got := strings.Join(handle(t, b, in), "\n"); !strings.Contains(got, "not configured") {
		t.Fatalf("got %q", got)
	}
}
