package discord

import (
	"strings"
	"testing"

	"pakka/internal/config"
)

func TestRedact(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  string
		extra []string
	}{
		{"ipv4", "dial tcp 10.212.63.175:24800 refused", "dial tcp [ip]:24800 refused", nil},
		{"ipv6", "from fe80::1 port 5", "from [ip] port 5", nil},
		{"ipv6 full", "addr 2001:db8:0:0:0:0:2:1 down", "addr [ip] down", nil},
		{"time kept", "took 12:34:56 to fail", "took 12:34:56 to fail", nil},
		{"secret assign", "token=abc123 leaked", "token=[redacted] leaked", nil},
		{"secret colon", "Password: s3cr3t here", "Password=[redacted] here", nil},
		{"api key", "api_key: ZZ9 end", "api_key=[redacted] end", nil},
		{"discord token", "x " + "MTExOTExOTExOTExOTExOTEx" + "." + "GrHAw6" + "." + "abcdefghijklmnopqrstuvwxyz0" + " z", "x [redacted-token] z", nil},
		{"github token", "used " + "ghp_" + "abcdefghijklmnopqr", "used [redacted-token]", nil},
		{"home path", "open /home/ubuntu/x failed", "open [path] failed", nil},
		{"etc path", "read /etc/passwd denied", "read [path] denied", nil},
		{"extra host", "on draughts-server ok", "on [internal] ok", []string{"draughts-server"}},
		{"mention kept", "hi <@u1> see <#c1>", "hi <@u1> see <#c1>", nil},
		{"command kept", "`!pakka run live` then", "`!pakka run live` then", nil},
		{"public url kept", "see https://openpak.org/status", "see https://openpak.org/status", nil},
		{"plain question", "where do I get the discord token?", "where do I get the discord token?", nil},
	}
	for _, tc := range cases {
		if got := RedactWith(tc.in, tc.extra); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestRedactExtras(t *testing.T) {
	cfg := config.Config{DNSHost: "localhost", TCPAddr: "db.internal:5432", HealthURL: "http://mon.internal:8080/hz"}
	extras := RedactExtras(cfg)
	joined := strings.Join(extras, " ")
	for _, want := range []string{"db.internal:5432", "db.internal", "mon.internal:8080"} {
		if !strings.Contains(joined, want) {
			t.Errorf("extras %q missing %q", extras, want)
		}
	}
	if strings.Contains(joined, "localhost") {
		t.Errorf("extras should skip localhost: %q", extras)
	}
}

func TestRedactingSender(t *testing.T) {
	inner := &fakeSender{}
	s := Redacting(inner, "secretbox")
	if _, err := s.ChannelMessageSend("c1", "from 10.0.0.1 on secretbox", nil...); err != nil {
		t.Fatal(err)
	}
	if len(inner.posts) != 1 || inner.posts[0] != "from [ip] on [internal]" {
		t.Errorf("posts = %v", inner.posts)
	}
}

func TestReplyRedactsInfra(t *testing.T) {
	b := testBot()
	b.redactExtra = []string{"secretbox"}
	sender := &fakeSender{}
	b.reply(sender, "c1", "dial 10.0.0.1 token=abc /home/u/x on secretbox")
	if len(sender.posts) != 1 {
		t.Fatalf("posts = %v", sender.posts)
	}
	got := sender.posts[0]
	for _, leak := range []string{"10.0.0.1", "token=abc", "/home/u/x", "secretbox"} {
		if strings.Contains(got, leak) {
			t.Errorf("reply leaked %q: %q", leak, got)
		}
	}
}

func TestRoutingIndependentOfCommandChannels(t *testing.T) {
	b := testBot()
	b.channels["hackathon"] = true
	b.router = NewRouter(keywordClassifier{}, "setup1", "conn1", "bug1", 0, nil)
	mismatch := supportMsg("setup1", "u1", "cannot connect to lobby")
	if posts := handle(t, b, mismatch); len(posts) != 1 || !strings.Contains(posts[0], "<#conn1>") {
		t.Errorf("support routing broke under channel gating: %v", posts)
	}
	cmd := supportMsg("setup1", "u1", "!pakka help")
	if posts := handle(t, b, cmd); len(posts) != 0 {
		t.Errorf("command in support channel got %d replies", len(posts))
	}
}

func TestMention(t *testing.T) {
	b := testBot()
	b.ownID = "999"
	if posts := handle(t, b, incoming("<@999> help")); len(posts) != 1 || !strings.Contains(posts[0], "diagnostics") {
		t.Errorf("mention help: %v", posts)
	}
	if posts := handle(t, b, incoming("<@!999>")); len(posts) != 1 || !strings.Contains(posts[0], "diagnostics") {
		t.Errorf("bare mention: %v", posts)
	}
	// Free text becomes a live investigation (no key here, so the key error).
	if posts := handle(t, b, incoming("<@999> cannot connect")); len(posts) != 1 || !strings.Contains(posts[0], "API key") {
		t.Errorf("mention question: %v", posts)
	}
	// Unknown IDs are not mentions.
	if posts := handle(t, b, incoming("<@123> help")); len(posts) != 0 {
		t.Errorf("foreign mention got %d replies", len(posts))
	}
	// Channel gating applies to mentions too.
	b.channels["hackathon"] = true
	if posts := handle(t, b, incoming("<@999> help")); len(posts) != 0 {
		t.Errorf("gated mention got %d replies", len(posts))
	}
	dev := incoming("<@999> help")
	dev.ChannelID = "hackathon"
	if posts := handle(t, b, dev); len(posts) != 1 {
		t.Errorf("dev-channel mention got %d replies", len(posts))
	}
}
