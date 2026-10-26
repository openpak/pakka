// Package investigate runs bounded model-guided diagnostic loops: the model
// may call allowlisted read-only checks, then must return an evidence-backed
// finding. See docs/plan.md Milestone 2.
package investigate

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"pakka/internal/config"
	"pakka/internal/diagnostics"
	"pakka/internal/evidence"
	"pakka/internal/meta"
)

// Diagnosis is the investigation outcome.
type Diagnosis string

// Known diagnoses.
const (
	DiagnosisLocalFailure Diagnosis = "local-failure"
	DiagnosisOutage       Diagnosis = "service-outage"
	DiagnosisHealthy      Diagnosis = "healthy"
	DiagnosisInsufficient Diagnosis = "insufficient-evidence"
)

func (d Diagnosis) valid() bool {
	switch d {
	case DiagnosisLocalFailure, DiagnosisOutage, DiagnosisHealthy, DiagnosisInsufficient:
		return true
	}
	return false
}

// Finding is the validated investigation result. Evidence lists only check
// IDs that actually ran; RecommendedNext is advisory text, never executed.
type Finding struct {
	Diagnosis       Diagnosis `json:"diagnosis"`
	Summary         string    `json:"summary"`
	Evidence        []string  `json:"evidence"`
	RecommendedNext string    `json:"recommended_next"`
}

// Model answers one loop turn: tool calls and/or a final message.
type Model interface {
	Complete(ctx context.Context, messages []meta.Message, tools []meta.Tool) (meta.Message, error)
}

// ClientModel adapts a Meta API client to Model.
type ClientModel struct {
	Client *meta.Client
}

// Complete implements Model.
func (m ClientModel) Complete(ctx context.Context, messages []meta.Message, tools []meta.Tool) (meta.Message, error) {
	return m.Client.Chat(ctx, messages, tools)
}

const systemPrompt = `You are Pakka, a network diagnostician. Use ONLY the provided tools to gather evidence, then give a final finding as JSON: {"diagnosis":"local-failure|service-outage|healthy|insufficient-evidence","summary":"...","evidence":["check-id",...],"recommended_next":"..."}. Cite only checks you actually ran. If the evidence is inconclusive, return insufficient-evidence. No prose outside tool calls and the final JSON.`

// ToolDef binds a model-callable tool name to a diagnostics check. Tools
// take no arguments: targets come from server configuration (approved
// destinations), never from the model.
type ToolDef struct {
	Name        string
	Description string
	CheckID     string
}

var registry = []ToolDef{
	{Name: "dns_resolve", Description: "Resolve the configured host; fails when local DNS or environment is wrong.", CheckID: "dns.resolve"},
	{Name: "tcp_connect", Description: "Dial the configured address; fails when the endpoint is unreachable.", CheckID: "tcp.connect"},
	{Name: "http_health", Description: "Fetch the configured health URL; fails when the service is down.", CheckID: "http.health"},
}

// Tools returns the model tool definitions.
func Tools() []meta.Tool {
	out := make([]meta.Tool, 0, len(registry))
	for _, td := range registry {
		out = append(out, meta.Tool{
			Type: "function",
			Function: meta.FunctionDef{
				Name:        td.Name,
				Description: td.Description,
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		})
	}
	return out
}

func checkForTool(name string) (string, bool) {
	for _, td := range registry {
		if td.Name == name {
			return td.CheckID, true
		}
	}
	return "", false
}

// Investigator executes bounded investigation loops.
type Investigator struct {
	checks   map[string]diagnostics.Check
	maxSteps int
	timeout  time.Duration
}

// NewInvestigator binds checks to configured targets.
func NewInvestigator(cfg config.Config) *Investigator {
	steps := cfg.InvestigateSteps
	if steps < 1 {
		steps = 5
	}
	timeout := cfg.CheckTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Investigator{
		checks: map[string]diagnostics.Check{
			"dns.resolve": diagnostics.NewDNSCheck(cfg.DNSHost),
			"tcp.connect": diagnostics.NewTCPCheck(cfg.TCPAddr),
			"http.health": diagnostics.NewHTTPCheck(cfg.HealthURL),
		},
		maxSteps: steps,
		timeout:  timeout,
	}
}

// Run investigates symptoms with model, returning the validated finding plus
// every collected check result.
func (in *Investigator) Run(ctx context.Context, model Model, symptoms string) (Finding, []evidence.Result, error) {
	messages := []meta.Message{
		{Role: meta.RoleSystem, Content: systemPrompt},
		{Role: meta.RoleUser, Content: symptoms},
	}
	var results []evidence.Result
	executed := map[string]bool{}
	for range in.maxSteps {
		reply, err := model.Complete(ctx, messages, Tools())
		if err != nil {
			return Finding{}, results, err
		}
		messages = append(messages, reply)
		if len(reply.ToolCalls) == 0 {
			return validateFinding(reply.Content, executed), results, nil
		}
		for _, tc := range reply.ToolCalls {
			content, res := in.executeTool(ctx, tc)
			if res != nil {
				results = append(results, *res)
				executed[res.CheckID] = true
			}
			messages = append(messages, meta.Message{Role: meta.RoleTool, ToolCallID: tc.ID, Content: content})
		}
	}
	return Finding{
		Diagnosis: DiagnosisInsufficient,
		Summary:   "step budget exhausted before a finding; see collected evidence",
		Evidence:  executedIDs(executed),
	}, results, nil
}

func (in *Investigator) executeTool(ctx context.Context, tc meta.ToolCall) (string, *evidence.Result) {
	checkID, ok := checkForTool(tc.Function.Name)
	if !ok {
		return `{"error":"unknown tool"}`, nil
	}
	check := in.checks[checkID]
	if check == nil {
		return `{"error":"check unavailable"}`, nil
	}
	tctx, cancel := context.WithTimeout(ctx, in.timeout)
	defer cancel()
	res := check.Run(tctx)
	data, err := json.Marshal(res)
	if err != nil {
		return `{"error":"encode failed"}`, &res
	}
	return string(data), &res
}

// validateFinding parses the final message and enforces the evidence rule:
// every cited check ID must have actually run, else the finding downgrades
// to insufficient-evidence.
func validateFinding(content string, executed map[string]bool) Finding {
	insufficient := func(summary string) Finding {
		return Finding{Diagnosis: DiagnosisInsufficient, Summary: summary, Evidence: executedIDs(executed)}
	}
	text := strings.TrimSpace(content)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(strings.TrimSpace(text), "```")
	var f Finding
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &f); err != nil {
		return insufficient("model finding was not valid JSON; treating as insufficient evidence")
	}
	if !f.Diagnosis.valid() {
		return insufficient(fmt.Sprintf("unknown diagnosis %q; treating as insufficient evidence", f.Diagnosis))
	}
	for _, id := range f.Evidence {
		if !executed[id] {
			return insufficient(fmt.Sprintf("finding cites unexecuted check %q; treating as insufficient evidence", id))
		}
	}
	return f
}

func executedIDs(executed map[string]bool) []string {
	ids := make([]string, 0, len(executed))
	for id := range executed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
