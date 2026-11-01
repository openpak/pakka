package chat

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// websiteTools is the fixed set of account-scoped reads the website serves.
// The website resolves the grant to an account itself; nothing here names one.
var websiteTools = map[string]bool{"status": true, "consoles": true, "friends": true, "rewards": true, "eligibility": true}

// Website calls the OpenPak website's private tool surface.
type Website struct {
	base, key string
	http      *http.Client
}

// NewWebsite builds the client.
func NewWebsite(base, key string) *Website {
	return &Website{base: base, key: key, http: &http.Client{Timeout: 10 * time.Second}}
}

// Tool runs one allowlisted read for the grant's account.
func (w *Website) Tool(ctx context.Context, grant, name string) (string, error) {
	if !websiteTools[name] {
		return "", fmt.Errorf("tool %q is not allowed", name)
	}
	if grant == "" && name != "status" {
		return "", fmt.Errorf("no signed-in context")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.base+"/internal/pakka/tool/"+name, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+w.key)
	req.Header.Set("X-Pakka-Grant", grant)
	resp, err := w.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("website: status %d", resp.StatusCode)
	}
	return string(body), nil
}
