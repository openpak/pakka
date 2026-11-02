// Package news talks to the news service's internal admin API: channels,
// articles, and publish-to-consoles. See platforms/core/news/docs/admin-api.md.
package news

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Error is a non-2xx admin API response.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("news admin: status %d: %s", e.Status, e.Message)
}

// Client reads and authors news channels and articles.
type Client struct {
	base   string
	key    string
	client *http.Client
}

// NewClient builds the admin client; base is the API root URL.
func NewClient(base, key string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	return &Client{base: base, key: key, client: &http.Client{Timeout: timeout}}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return err
	}
	if c.key != "" {
		req.Header.Set("X-Internal-Key", c.key)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var doc struct {
			Err string `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &doc) == nil && doc.Err != "" {
			msg = doc.Err
		}
		return &Error{Status: resp.StatusCode, Message: msg}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Channel is the subset of a news channel the landing flow reads back.
type Channel struct {
	TopicID string `json:"topic_id"`
	Name    string `json:"name"`
}

// ChannelCreate is a new game channel.
type ChannelCreate struct {
	TopicID        string   `json:"topic_id"`
	Name           string   `json:"name"`
	Publisher      string   `json:"publisher"`
	Description    string   `json:"description"`
	IconMotif      string   `json:"-"`
	ApplicationIDs []string `json:"application_ids,omitempty"`
}

func (c ChannelCreate) marshal() map[string]any {
	m := map[string]any{
		"topic_id": c.TopicID, "name": c.Name,
		"publisher": c.Publisher, "description": c.Description,
	}
	if c.IconMotif != "" {
		m["icon"] = map[string]any{"motif": c.IconMotif}
	}
	if len(c.ApplicationIDs) > 0 {
		m["application_ids"] = c.ApplicationIDs
	}
	return m
}

// GetChannel fetches one channel; found=false when the topic is unknown.
func (c *Client) GetChannel(ctx context.Context, topic string) (*Channel, bool, error) {
	var ch Channel
	err := c.do(ctx, http.MethodGet, "/admin/v1/channels/"+topic, nil, &ch)
	if err != nil {
		var apiErr *Error
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &ch, true, nil
}

// CreateChannel creates a game channel; a used topic id answers 409.
func (c *Client) CreateChannel(ctx context.Context, req ChannelCreate) (*Channel, error) {
	var ch Channel
	if err := c.do(ctx, http.MethodPost, "/admin/v1/channels", req.marshal(), &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// ArticleLink is an article's link button.
type ArticleLink struct {
	Type           string   `json:"type"`
	ApplicationIDs []string `json:"application_ids,omitempty"`
	Query          string   `json:"query,omitempty"`
	URL            string   `json:"url,omitempty"`
}

// ArticleCreate is a new article.
type ArticleCreate struct {
	Title              string       `json:"title"`
	Body               string       `json:"body"`
	Language           string       `json:"language"`
	SupportedLanguages []string     `json:"supported_languages"`
	LifetimeDays       int          `json:"lifetime_days"`
	Priority           int          `json:"priority,omitempty"`
	ArtMotif           string       `json:"-"`
	Link               *ArticleLink `json:"more,omitempty"`
}

func (a ArticleCreate) marshal() map[string]any {
	m := map[string]any{
		"title": a.Title, "body": a.Body, "language": a.Language,
		"supported_languages": a.SupportedLanguages, "lifetime_days": a.LifetimeDays,
	}
	if a.Priority != 0 {
		m["priority"] = a.Priority
	}
	if a.ArtMotif != "" {
		m["art"] = map[string]any{"motif": a.ArtMotif}
	}
	if a.Link != nil {
		m["more"] = a.Link
	}
	return m
}

// Article is the created article's identity.
type Article struct {
	NewsID uint32 `json:"news_id"`
	Title  string `json:"title"`
}

// PostArticle publishes one article to a channel.
func (c *Client) PostArticle(ctx context.Context, topic string, req ArticleCreate) (*Article, error) {
	var a Article
	if err := c.do(ctx, http.MethodPost, "/admin/v1/channels/"+topic+"/articles", req.marshal(), &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// PublishResult is a successful publish-to-consoles.
type PublishResult struct {
	Build         int      `json:"build"`
	Revision      int      `json:"revision"`
	ChangedTopics []string `json:"changed_topics"`
}

// Publish ships the source tree to consoles.
func (c *Client) Publish(ctx context.Context, note string) (*PublishResult, error) {
	var body any
	if note != "" {
		body = map[string]any{"note": note}
	}
	var res PublishResult
	if err := c.do(ctx, http.MethodPost, "/admin/v1/publish", body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
