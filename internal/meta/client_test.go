package meta

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChatRoundTrip(t *testing.T) {
	var gotAuth, gotPath string
	var gotReq struct {
		Model       string    `json:"model"`
		Messages    []Message `json:"messages"`
		Tools       []Tool    `json:"tools"`
		Temperature float64   `json:"temperature"`
	}
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("decode request: %s", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call-1","function":{"name":"dns_resolve","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
	}))
	defer stub.Close()

	c := NewClient(stub.URL, "test-key", "muse-spark-1.3-contributor", 5*time.Second)
	msg, err := c.Chat(context.Background(),
		[]Message{{Role: RoleUser, Content: "help"}},
		[]Tool{{Type: "function", Function: FunctionDef{Name: "dns_resolve"}}})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/chat/completions" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotReq.Model != "muse-spark-1.3-contributor" || len(gotReq.Tools) != 1 || len(gotReq.Messages) != 1 {
		t.Errorf("request = %+v", gotReq)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "dns_resolve" {
		t.Errorf("tool calls = %+v", msg.ToolCalls)
	}
}

func TestChatTrimsBaseSlash(t *testing.T) {
	var gotPath string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{}"},"finish_reason":"stop"}]}`))
	}))
	defer stub.Close()

	c := NewClient(stub.URL+"/", "k", "m", time.Second)
	if _, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/chat/completions" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestChatErrorStatus(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer stub.Close()

	c := NewClient(stub.URL, "bad", "m", time.Second)
	_, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want 401", err)
	}
}

func TestChatNoChoices(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer stub.Close()

	c := NewClient(stub.URL, "k", "m", time.Second)
	if _, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil); err == nil {
		t.Error("want error for empty choices, got nil")
	}
}
