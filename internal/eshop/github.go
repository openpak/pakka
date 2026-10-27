package eshop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// GitHubClient resolves repo slugs to their latest release.
type GitHubClient struct {
	token   string
	client  *http.Client
	backoff []time.Duration // sleeps on 403/429 before giving up
}

// NewGitHubClient builds the releases client. The token (GITHUB_TOKEN)
// lifts the rate limit from 60 to 5000 requests an hour.
func NewGitHubClient(token string, timeout time.Duration) *GitHubClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &GitHubClient{
		token:   token,
		client:  &http.Client{Timeout: timeout},
		backoff: []time.Duration{30 * time.Second, 90 * time.Second, 240 * time.Second},
	}
}

type releaseJSON struct {
	TagName     string `json:"tag_name"`
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
	Body        string `json:"body"`
}

// Latest implements Releases.
func (c *GitHubClient) Latest(ctx context.Context, slug string) (Release, bool, error) {
	url := "https://api.github.com/repos/" + slug + "/releases/latest"
	var lastErr error
	for attempt := 0; ; attempt++ {
		rel, ok, retry, err := c.fetch(ctx, url)
		if err == nil {
			return rel, ok, nil
		}
		lastErr = err
		if !retry || attempt >= len(c.backoff) {
			return Release{}, false, lastErr
		}
		timer := time.NewTimer(c.backoff[attempt])
		select {
		case <-ctx.Done():
			timer.Stop()
			return Release{}, false, ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *GitHubClient) fetch(ctx context.Context, url string) (rel Release, ok bool, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, false, false, err
	}
	req.Header.Set("User-Agent", "openpak-pakka")
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return Release{}, false, false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return Release{}, false, false, nil // no releases published
	case http.StatusForbidden, http.StatusTooManyRequests:
		return Release{}, false, true, fmt.Errorf("github %s: status %d", url, resp.StatusCode)
	default:
		return Release{}, false, false, fmt.Errorf("github %s: status %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Release{}, false, false, err
	}
	var doc releaseJSON
	if err := json.Unmarshal(b, &doc); err != nil {
		return Release{}, false, false, err
	}
	if doc.TagName == "" {
		return Release{}, false, false, nil
	}
	return Release{Tag: doc.TagName, PublishedAt: doc.PublishedAt, HTMLURL: doc.HTMLURL, Body: doc.Body}, true, false, nil
}
