package investigate

import (
	"context"
	"fmt"

	"pakka/internal/meta"
)

// ScriptedModel replays canned assistant replies for fixture investigations.
// Tool calls execute the real (suite-configured) checks; the final reply
// carries the fixture finding.
type ScriptedModel struct {
	replies []meta.Message
	calls   int
}

// Complete implements Model.
func (m *ScriptedModel) Complete(_ context.Context, _ []meta.Message, _ []meta.Tool) (meta.Message, error) {
	if m.calls >= len(m.replies) {
		return meta.Message{}, fmt.Errorf("script exhausted after %d calls", m.calls)
	}
	reply := m.replies[m.calls]
	m.calls++
	return reply, nil
}

func toolReply(id, name string) meta.Message {
	return meta.Message{
		Role: meta.RoleAssistant,
		ToolCalls: []meta.ToolCall{{
			ID:       id,
			Function: meta.FunctionCall{Name: name, Arguments: "{}"},
		}},
	}
}

func finalReply(finding string) meta.Message {
	return meta.Message{Role: meta.RoleAssistant, Content: finding}
}

// ScriptFor returns the canned model script for a fixture scenario.
func ScriptFor(scenario string) (*ScriptedModel, bool) {
	switch scenario {
	case "local-misconfig":
		return &ScriptedModel{replies: []meta.Message{
			toolReply("call-1", "dns_resolve"),
			toolReply("call-2", "tcp_connect"),
			finalReply(`{"diagnosis":"local-failure","summary":"synthetic: local environment misconfigured","evidence":["dns.resolve","tcp.connect"],"recommended_next":"synthetic: re-run Switch Setup and retest"}`),
		}}, true
	case "service-outage":
		return &ScriptedModel{replies: []meta.Message{
			toolReply("call-1", "dns_resolve"),
			toolReply("call-2", "tcp_connect"),
			toolReply("call-3", "http_health"),
			finalReply(`{"diagnosis":"service-outage","summary":"synthetic: service unhealthy, local side fine","evidence":["dns.resolve","tcp.connect","http.health"],"recommended_next":"synthetic: check the status page; change nothing locally"}`),
		}}, true
	case "all-pass":
		return &ScriptedModel{replies: []meta.Message{
			toolReply("call-1", "dns_resolve"),
			toolReply("call-2", "tcp_connect"),
			toolReply("call-3", "http_health"),
			finalReply(`{"diagnosis":"healthy","summary":"synthetic: all checks pass","evidence":["dns.resolve","tcp.connect","http.health"],"recommended_next":"synthetic: none"}`),
		}}, true
	}
	return nil, false
}
