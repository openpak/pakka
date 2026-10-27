// Agent turns @pakka mentions into a moderator assistant: the model answers
// in plain chat using read tools (diagnostics, service status, recent
// alerts) plus one narrow action, post_notice, limited to staff channels.
package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"pakka/internal/diagnostics"
	"pakka/internal/investigate"
	"pakka/internal/meta"
)

// Agent steps bound one mention turn.
const defaultAgentSteps = 6

// Agent answers one moderator question per Run.
type Agent struct {
	model   investigate.Model
	runner  *diagnostics.Runner
	monitor *Monitor    // nil = service_status unavailable
	watcher *LogWatcher // nil = recent_alerts unavailable
	// poster sends a notice to a staff channel ("modlog", "ops", "status", "alerts").
	poster func(channel, message string) error
	steps  int
	log    *slog.Logger
}

const agentSystem = `You are Pakka, a moderator assistant for the OpenPak Discord. Answer the moderator's question briefly in chat (a few lines, no JSON).

Rules:
- Use tools for facts; never invent service states, errors, or diagnostics.
- service_status shows the monitored services; recent_alerts shows what the log watcher caught; diagnostics_run runs live checks.
- post_notice posts to a staff channel (modlog, ops, status, alerts) — ONLY when explicitly asked to post, announce, or notify, and say what you posted.
- Never reveal infrastructure details (IPs, tokens, paths); the channel redacts them anyway.`

// AgentTools returns the agent's tool definitions.
func AgentTools() []meta.Tool {
	obj := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required}
	}
	return []meta.Tool{
		{Type: "function", Function: meta.FunctionDef{
			Name:        "diagnostics_run",
			Description: "Run live diagnostic checks (dns, tcp, http) and return outcomes.",
			Parameters:  obj(map[string]any{}),
		}},
		{Type: "function", Function: meta.FunctionDef{
			Name:        "service_status",
			Description: "Current up/down state of every monitored service.",
			Parameters:  obj(map[string]any{}),
		}},
		{Type: "function", Function: meta.FunctionDef{
			Name:        "recent_alerts",
			Description: "Newest log-watcher alerts, optionally filtered by unit substring.",
			Parameters: obj(map[string]any{
				"n":    map[string]any{"type": "integer", "description": "max alerts, default 5"},
				"unit": map[string]any{"type": "string", "description": "only sources containing this"},
			}),
		}},
		{Type: "function", Function: meta.FunctionDef{
			Name:        "post_notice",
			Description: "Post a message to a staff channel; only when asked to post or announce.",
			Parameters: obj(map[string]any{
				"channel": map[string]any{"type": "string", "description": "one of: modlog, ops, status, alerts"},
				"message": map[string]any{"type": "string", "description": "notice text"},
			}, "channel", "message"),
		}},
	}
}

// Run answers one question, looping over tool calls within the step budget.
func (a *Agent) Run(ctx context.Context, question string) (string, error) {
	steps := a.steps
	if steps <= 0 {
		steps = defaultAgentSteps
	}
	messages := []meta.Message{
		{Role: meta.RoleSystem, Content: agentSystem},
		{Role: meta.RoleUser, Content: question},
	}
	lastText := ""
	for range steps {
		reply, err := a.model.Complete(ctx, messages, AgentTools())
		if err != nil {
			return "", err
		}
		messages = append(messages, reply)
		if len(reply.ToolCalls) == 0 {
			if strings.TrimSpace(reply.Content) == "" {
				return "I don't have an answer for that — try rephrasing the question.", nil
			}
			return reply.Content, nil
		}
		lastText = reply.Content
		for _, tc := range reply.ToolCalls {
			out := a.executeTool(ctx, tc)
			messages = append(messages, meta.Message{Role: meta.RoleTool, ToolCallID: tc.ID, Content: out})
		}
	}
	if strings.TrimSpace(lastText) != "" {
		return lastText, nil
	}
	return "I ran out of steps on that — try narrowing the question.", nil
}

func (a *Agent) executeTool(ctx context.Context, tc meta.ToolCall) string {
	var args map[string]any
	if tc.Function.Arguments != "" {
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return `{"error":"bad arguments"}`
		}
	}
	switch tc.Function.Name {
	case "diagnostics_run":
		return a.toolDiagnostics(ctx)
	case "service_status":
		return a.toolStatus()
	case "recent_alerts":
		n := 5
		if v, ok := args["n"].(float64); ok && v > 0 {
			n = int(v)
			if n > 20 {
				n = 20
			}
		}
		unit, _ := args["unit"].(string)
		return a.toolAlerts(n, unit)
	case "post_notice":
		channel, _ := args["channel"].(string)
		message, _ := args["message"].(string)
		return a.toolPost(channel, message)
	default:
		return `{"error":"unknown tool"}`
	}
}

func (a *Agent) toolDiagnostics(ctx context.Context) string {
	if a.runner == nil {
		return `{"error":"diagnostics unavailable"}`
	}
	run, err := a.runner.Run(ctx, diagnostics.ModeLive, "live", nil)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	type row struct {
		Check   string `json:"check"`
		Outcome string `json:"outcome"`
		Summary string `json:"summary"`
	}
	rows := make([]row, 0, len(run.Results))
	for _, r := range run.Results {
		rows = append(rows, row{Check: r.CheckID, Outcome: string(r.Outcome), Summary: r.Summary})
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return `{"error":"encode failed"}`
	}
	return string(data)
}

func (a *Agent) toolStatus() string {
	if a.monitor == nil {
		return `{"status":"monitor disabled"}` + "\n"
	}
	var sb strings.Builder
	for _, s := range a.monitor.Snapshot() {
		state := "unknown"
		if s.Seen {
			state = "DOWN"
			if s.Up {
				state = "UP"
			}
		}
		fmt.Fprintf(&sb, "%s: %s\n", s.Name, state)
	}
	return sb.String()
}

func (a *Agent) toolAlerts(n int, unit string) string {
	if a.watcher == nil {
		return "log watcher disabled"
	}
	recs := a.watcher.Recent(n, unit)
	if len(recs) == 0 {
		return "no recent alerts"
	}
	var sb strings.Builder
	for _, r := range recs {
		fmt.Fprintf(&sb, "[%s] %s %s — %s\n", r.Time.UTC().Format("15:04"), r.Feed, r.Source, r.Message)
	}
	return sb.String()
}

var staffNames = map[string]bool{"modlog": true, "ops": true, "status": true, "alerts": true}

func (a *Agent) toolPost(channel, message string) string {
	if a.poster == nil {
		return `{"error":"posting unavailable"}`
	}
	if !staffNames[channel] {
		return `{"error":"unknown staff channel (want modlog|ops|status|alerts)"}`
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return `{"error":"empty message"}`
	}
	if len(message) > 1500 {
		return `{"error":"message too long (1500 max)"}`
	}
	if err := a.poster(channel, message); err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return fmt.Sprintf(`{"posted":%q}`, channel)
}
