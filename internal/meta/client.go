// Package meta is a minimal client for the Meta Model API
// (https://api.meta.ai/v1), using the OpenAI-compatible chat completions
// endpoint with function tools.
package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is the Meta Model API base.
const DefaultBaseURL = "https://api.meta.ai/v1"

// DefaultModel is the default investigation model.
const DefaultModel = "muse-spark-1.3-contributor"

// Message roles.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message is one chat turn.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a request to invoke one function tool.
type ToolCall struct {
	ID       string       `json:"id"`
	Function FunctionCall `json:"function"`
}

// FunctionCall names the tool and its JSON arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool is a function tool definition.
type Tool struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

// FunctionDef describes one callable function.
type FunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	Temperature float64   `json:"temperature"`
}

type chatResponse struct {
	Choices []choice `json:"choices"`
}

type choice struct {
	Message Message `json:"message"`
}

// Client calls the Meta Model API. The API key stays in memory and outbound
// requests only; it is never logged.
type Client struct {
	base  string
	key   string
	model string
	http  *http.Client
}

// NewClient builds a client. timeout bounds each API call.
func NewClient(baseURL, apiKey, model string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Client{
		base:  strings.TrimSuffix(baseURL, "/"),
		key:   apiKey,
		model: model,
		http:  &http.Client{Timeout: timeout},
	}
}

// Chat sends one chat completions request with tools and returns the
// assistant message.
func (c *Client) Chat(ctx context.Context, messages []Message, tools []Tool) (Message, error) {
	var zero Message
	body, err := json.Marshal(chatRequest{
		Model:       c.model,
		Messages:    messages,
		Tools:       tools,
		Temperature: 0.2,
	})
	if err != nil {
		return zero, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return zero, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.key)
	resp, err := c.http.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return zero, err
	}
	if resp.StatusCode/100 != 2 {
		return zero, fmt.Errorf("meta api: status %d: %s", resp.StatusCode, truncate(string(data), 1000))
	}
	var parsed chatResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return zero, fmt.Errorf("meta api: bad response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return zero, fmt.Errorf("meta api: no choices in response")
	}
	return parsed.Choices[0].Message, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
