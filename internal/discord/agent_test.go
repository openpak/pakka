package discord

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"pakka/internal/config"
	"pakka/internal/diagnostics"
	"pakka/internal/meta"
)

// scriptModel replays canned assistant replies for agent tests.
type scriptModel struct {
	replies []meta.Message
	calls   int
}

func (m *scriptModel) Complete(_ context.Context, _ []meta.Message, _ []meta.Tool) (meta.Message, error) {
	m.calls++
	if len(m.replies) == 0 {
		return meta.Message{Role: meta.RoleAssistant, Content: "done"}, nil
	}
	r := m.replies[0]
	m.replies = m.replies[1:]
	return r, nil
}

func toolReply(id, name, args string) meta.Message {
	return meta.Message{Role: meta.RoleAssistant, ToolCalls: []meta.ToolCall{
		{ID: id, Function: meta.FunctionCall{Name: name, Arguments: args}},
	}}
}

func testAgent(model *scriptModel) (*Agent, *fakeAnnouncer, *LogWatcher) {
	cfg := config.Config{DNSHost: "localhost", CheckTimeout: 5 * time.Second, InvestigateSteps: 5}
	ann := &fakeAnnouncer{}
	up := map[string]bool{"a": true, "b": false}
	mon := testMonitor(up, ann)
	mon.RunOnce(context.Background())
	_, w := watchAnnouncer()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Agent{
		model: model, runner: diagnostics.NewRunner(cfg),
		monitor: mon, watcher: w, log: log,
	}, ann, w
}

func TestAgentToolsRegistered(t *testing.T) {
	names := map[string]bool{}
	for _, tl := range AgentTools() {
		names[tl.Function.Name] = true
	}
	for _, want := range []string{"diagnostics_run", "service_status", "recent_alerts", "post_notice"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

func TestAgentAnswersDirectly(t *testing.T) {
	m := &scriptModel{replies: []meta.Message{{Role: meta.RoleAssistant, Content: "All quiet."}}}
	ag, _, _ := testAgent(m)
	got, err := ag.Run(context.Background(), "anything up?")
	if err != nil {
		t.Fatal(err)
	}
	if got != "All quiet." || m.calls != 1 {
		t.Errorf("got %q after %d calls", got, m.calls)
	}
}

func TestAgentUsesStatusTool(t *testing.T) {
	m := &scriptModel{replies: []meta.Message{
		toolReply("c1", "service_status", `{}`),
		{Role: meta.RoleAssistant, Content: "b is down, a is fine."},
	}}
	ag, _, _ := testAgent(m)
	got, err := ag.Run(context.Background(), "service status?")
	if err != nil {
		t.Fatal(err)
	}
	if got != "b is down, a is fine." || m.calls != 2 {
		t.Errorf("got %q after %d calls", got, m.calls)
	}
}

func TestAgentToolStatus(t *testing.T) {
	ag, _, _ := testAgent(&scriptModel{})
	out := ag.executeTool(context.Background(), meta.ToolCall{Function: meta.FunctionCall{Name: "service_status"}})
	if !strings.Contains(out, "a: UP") || !strings.Contains(out, "b: DOWN") {
		t.Errorf("status = %q", out)
	}
	ag.monitor = nil
	if out := ag.executeTool(context.Background(), meta.ToolCall{Function: meta.FunctionCall{Name: "service_status"}}); !strings.Contains(out, "disabled") {
		t.Errorf("nil monitor = %q", out)
	}
}

func TestAgentToolAlerts(t *testing.T) {
	ag, _, w := testAgent(&scriptModel{})
	w.Handle([]byte(`{"MESSAGE":"connection refused","PRIORITY":"3","_SYSTEMD_UNIT":"openpak-x.service","_HOSTNAME":"h1"}`))
	w.Handle([]byte(`{"MESSAGE":"player reported for spam","PRIORITY":"6","_SYSTEMD_UNIT":"openpak-y.service","_HOSTNAME":"h1"}`))
	out := ag.executeTool(context.Background(), meta.ToolCall{Function: meta.FunctionCall{Name: "recent_alerts", Arguments: `{"n":5}`}})
	if !strings.Contains(out, "openpak-x.service") || !strings.Contains(out, "openpak-y.service") {
		t.Errorf("alerts = %q", out)
	}
	out = ag.executeTool(context.Background(), meta.ToolCall{Function: meta.FunctionCall{Name: "recent_alerts", Arguments: `{"unit":"openpak-y"}`}})
	if strings.Contains(out, "openpak-x.service") || !strings.Contains(out, "openpak-y.service") {
		t.Errorf("filtered alerts = %q", out)
	}
	ag.watcher = nil
	if out := ag.executeTool(context.Background(), meta.ToolCall{Function: meta.FunctionCall{Name: "recent_alerts"}}); !strings.Contains(out, "disabled") {
		t.Errorf("nil watcher = %q", out)
	}
}

func TestAgentToolDiagnostics(t *testing.T) {
	ag, _, _ := testAgent(&scriptModel{})
	out := ag.executeTool(context.Background(), meta.ToolCall{Function: meta.FunctionCall{Name: "diagnostics_run"}})
	if !strings.Contains(out, "dns.resolve") || !strings.Contains(out, "outcome") {
		t.Errorf("diagnostics = %q", out)
	}
}

func TestAgentToolPost(t *testing.T) {
	var posted []string
	ag, _, _ := testAgent(&scriptModel{})
	ag.poster = func(channel, message string) error {
		posted = append(posted, channel+":"+message)
		return nil
	}
	out := ag.executeTool(context.Background(), meta.ToolCall{Function: meta.FunctionCall{Name: "post_notice", Arguments: `{"channel":"ops","message":"heads up"}`}})
	if !strings.Contains(out, `"posted":"ops"`) || len(posted) != 1 || posted[0] != "ops:heads up" {
		t.Errorf("post = %q posted=%v", out, posted)
	}
	out = ag.executeTool(context.Background(), meta.ToolCall{Function: meta.FunctionCall{Name: "post_notice", Arguments: `{"channel":"random","message":"x"}`}})
	if !strings.Contains(out, "error") || len(posted) != 1 {
		t.Errorf("bad channel should fail without posting: %q", out)
	}
}

func TestAgentUnknownTool(t *testing.T) {
	ag, _, _ := testAgent(&scriptModel{})
	out := ag.executeTool(context.Background(), meta.ToolCall{Function: meta.FunctionCall{Name: "nuke"}})
	if !strings.Contains(out, "unknown tool") {
		t.Errorf("out = %q", out)
	}
}

func TestRunAgentMentionReplies(t *testing.T) {
	b := testBot()
	b.ownID = "999"
	b.model = &scriptModel{replies: []meta.Message{{Role: meta.RoleAssistant, Content: "The ARMS server is up."}}}
	if posts := handle(t, b, incoming("<@999> is arms up?")); len(posts) != 1 || !strings.Contains(posts[0], "ARMS") {
		t.Errorf("posts = %v", posts)
	}
}

func TestRunAgentReplyRedacted(t *testing.T) {
	b := testBot()
	b.ownID = "999"
	b.model = &scriptModel{replies: []meta.Message{{Role: meta.RoleAssistant, Content: "dial 10.0.0.1 failed"}}}
	posts := handle(t, b, incoming("<@999> what failed?"))
	if len(posts) != 1 || strings.Contains(posts[0], "10.0.0.1") || !strings.Contains(posts[0], "[ip]") {
		t.Errorf("posts = %v", posts)
	}
}

func TestMonitorSnapshot(t *testing.T) {
	up := map[string]bool{"a": true, "b": false}
	m := testMonitor(up, &fakeAnnouncer{})
	for _, s := range m.Snapshot() {
		if s.Seen {
			t.Errorf("pre-poll state seen: %+v", s)
		}
	}
	m.RunOnce(context.Background())
	got := map[string]TargetState{}
	for _, s := range m.Snapshot() {
		got[s.Name] = s
	}
	if !got["a"].Seen || !got["a"].Up || !got["b"].Seen || got["b"].Up {
		t.Errorf("snapshot = %+v", got)
	}
}

func TestLogWatcherRecent(t *testing.T) {
	_, w := watchAnnouncer()
	if recs := w.Recent(5, ""); len(recs) != 0 {
		t.Fatalf("empty watcher returned %d", len(recs))
	}
	w.Handle([]byte(`{"MESSAGE":"first boom","PRIORITY":"3","_SYSTEMD_UNIT":"openpak-x.service","_HOSTNAME":"h1"}`))
	w.Handle([]byte(`{"MESSAGE":"second boom","PRIORITY":"3","_SYSTEMD_UNIT":"openpak-x.service","_HOSTNAME":"h1"}`))
	recs := w.Recent(5, "")
	if len(recs) != 2 || !strings.Contains(recs[0].Message, "second") {
		t.Fatalf("recs = %+v", recs)
	}
	if recs[0].Feed != "ops" || recs[0].Source != "h1/openpak-x.service" {
		t.Errorf("rec = %+v", recs[0])
	}
	if recs := w.Recent(5, "nope"); len(recs) != 0 {
		t.Errorf("filtered recs = %+v", recs)
	}
}
