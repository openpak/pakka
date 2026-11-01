package publisher

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OpenVerse is the official-account API of OpenVerse (nn-juxtaposition
// services/official). Every write is repeatable through its event id.
type OpenVerse interface {
	Post(ctx context.Context, p PostRequest) (PostResult, error)
	EnsureCommunity(ctx context.Context, c CommunityRequest) (CommunityResult, error)
	FindCommunity(ctx context.Context, ref CommunityRef) (CommunityResult, bool, error)
	DeletePost(ctx context.Context, id string) error
	Top(ctx context.Context, days, limit int) ([]TopPost, error)
}

// PostRequest is one official post.
type PostRequest struct {
	EventID   string       `json:"eventId"`
	Community CommunityRef `json:"-"`
	Body      string       `json:"body"`
	Image     string       `json:"image,omitempty"` // base64 JPEG
	Discuss   bool         `json:"discuss,omitempty"`
}

// PostResult is the created (or existing) post.
type PostResult struct {
	ID, URL, CommunityID string
	Created              bool
}

// CommunityRequest creates a community.
type CommunityRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	TitleIDs    []string `json:"titleIds,omitempty"` // decimal
}

// CommunityResult is a community as OpenVerse knows it.
type CommunityResult struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	TitleID []string `json:"titleIds"`
	URL     string   `json:"url"`
	Created bool     `json:"created"`
}

// TopPost is one of the week's most Yeah'd posts.
type TopPost struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Author    string `json:"author"`
	Community string `json:"community"`
	Body      string `json:"body"`
	Yeahs     int    `json:"yeahs"`
	Replies   int    `json:"replies"`
}

// APIError is a non-2xx answer.
type APIError struct {
	Service string
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: status %d: %s", e.Service, e.Status, e.Message)
}

// IsStatus reports whether err is an APIError with the status.
func IsStatus(err error, status int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == status
}

type httpJSON struct {
	service, base, key string
	client             *http.Client
}

func (h httpJSON) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.base+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("X-Internal-Key", h.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", h.service, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(raw))
		var doc struct {
			Error  string `json:"error"`
			Detail string `json:"detail"`
		}
		if json.Unmarshal(raw, &doc) == nil && doc.Error != "" {
			msg = doc.Error
			if doc.Detail != "" {
				msg += ": " + doc.Detail
			}
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return &APIError{Service: h.service, Status: resp.StatusCode, Message: msg}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// HTTPOpenVerse talks to the real service.
type HTTPOpenVerse struct{ h httpJSON }

// NewOpenVerse builds the OpenVerse client.
func NewOpenVerse(base, key string, timeout time.Duration) *HTTPOpenVerse {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &HTTPOpenVerse{httpJSON{"openverse", strings.TrimRight(base, "/") + "/internal/v1/official", key, &http.Client{Timeout: timeout}}}
}

// Post creates the post or answers the one this event already made.
func (c *HTTPOpenVerse) Post(ctx context.Context, p PostRequest) (PostResult, error) {
	body := map[string]any{"eventId": p.EventID, "body": p.Body, "community": map[string]string{
		"id": p.Community.ID, "titleId": p.Community.TitleID, "name": p.Community.Name,
	}}
	if p.Image != "" {
		body["image"] = p.Image
	}
	if p.Discuss {
		body["discuss"] = true
	}
	var out struct {
		ID, URL     string
		CommunityID string `json:"communityId"`
		Created     bool
	}
	if err := c.h.do(ctx, http.MethodPost, "/posts", body, &out); err != nil {
		return PostResult{}, err
	}
	return PostResult{ID: out.ID, URL: out.URL, CommunityID: out.CommunityID, Created: out.Created}, nil
}

// EnsureCommunity creates a community or answers the existing one.
func (c *HTTPOpenVerse) EnsureCommunity(ctx context.Context, r CommunityRequest) (CommunityResult, error) {
	var out CommunityResult
	err := c.h.do(ctx, http.MethodPost, "/communities", r, &out)
	return out, err
}

// FindCommunity looks a community up; ok=false when none matches.
func (c *HTTPOpenVerse) FindCommunity(ctx context.Context, ref CommunityRef) (CommunityResult, bool, error) {
	q := url.Values{}
	if ref.ID != "" {
		q.Set("id", ref.ID)
	}
	if ref.TitleID != "" {
		q.Set("titleId", ref.TitleID)
	}
	if ref.Name != "" {
		q.Set("name", ref.Name)
	}
	var out CommunityResult
	if err := c.h.do(ctx, http.MethodGet, "/communities?"+q.Encode(), nil, &out); err != nil {
		if IsStatus(err, http.StatusNotFound) {
			return CommunityResult{}, false, nil
		}
		return CommunityResult{}, false, err
	}
	return out, true, nil
}

// DeletePost removes one of Pakka's own posts.
func (c *HTTPOpenVerse) DeletePost(ctx context.Context, id string) error {
	return c.h.do(ctx, http.MethodDelete, "/posts/"+url.PathEscape(id), nil, nil)
}

// Top lists the most Yeah'd posts of the last days.
func (c *HTTPOpenVerse) Top(ctx context.Context, days, limit int) ([]TopPost, error) {
	var out struct {
		Posts []TopPost `json:"posts"`
	}
	err := c.h.do(ctx, http.MethodGet, fmt.Sprintf("/top?days=%d&limit=%d", days, limit), nil, &out)
	return out.Posts, err
}

// NewsAdmin is the News service's admin API.
type NewsAdmin interface {
	Channels(ctx context.Context) ([]NewsChannel, error)
	Articles(ctx context.Context, topic string) ([]NewsArticle, error)
	PostArticle(ctx context.Context, topic string, a NewsArticleCreate) (uint32, error)
	Publish(ctx context.Context, note string) (int, error)
}

// NewsChannel is the part of a channel Pakka reads.
type NewsChannel struct {
	TopicID        string   `json:"topic_id"`
	Name           string   `json:"name"`
	ApplicationIDs []string `json:"application_ids"`
	Retired        bool     `json:"retired"`
}

// NewsArticle is the part of an article Pakka reads.
type NewsArticle struct {
	TopicID       string `json:"topic_id"`
	NewsID        uint32 `json:"news_id"`
	Title         string `json:"title"`
	Body          string `json:"body"`
	Retired       bool   `json:"retired"`
	EverPublished bool   `json:"ever_published"`
}

// NewsArticleCreate is a new article.
type NewsArticleCreate struct {
	Title              string         `json:"title"`
	Body               string         `json:"body"`
	Language           string         `json:"language"`
	SupportedLanguages []string       `json:"supported_languages"`
	LifetimeDays       int            `json:"lifetime_days"`
	Priority           int            `json:"priority"`
	Art                map[string]any `json:"art"`
	More               map[string]any `json:"more,omitempty"`
}

// HTTPNews talks to the real admin API.
type HTTPNews struct{ h httpJSON }

// NewNews builds the News admin client.
func NewNews(base, key string, timeout time.Duration) *HTTPNews {
	if timeout <= 0 {
		timeout = 25 * time.Minute // publish signs every container (20-40 min under load)
	}
	return &HTTPNews{httpJSON{"news admin", strings.TrimRight(base, "/"), key, &http.Client{Timeout: timeout}}}
}

// Channels lists the channels.
func (c *HTTPNews) Channels(ctx context.Context) ([]NewsChannel, error) {
	var out struct {
		Channels []NewsChannel `json:"channels"`
	}
	err := c.h.do(ctx, http.MethodGet, "/admin/v1/channels", nil, &out)
	return out.Channels, err
}

// Articles lists a channel's articles.
func (c *HTTPNews) Articles(ctx context.Context, topic string) ([]NewsArticle, error) {
	var out struct {
		Articles []NewsArticle `json:"articles"`
	}
	err := c.h.do(ctx, http.MethodGet, "/admin/v1/channels/"+url.PathEscape(topic)+"/articles", nil, &out)
	for i := range out.Articles {
		if out.Articles[i].TopicID == "" {
			out.Articles[i].TopicID = topic
		}
	}
	return out.Articles, err
}

// PostArticle creates an article and returns its id.
func (c *HTTPNews) PostArticle(ctx context.Context, topic string, a NewsArticleCreate) (uint32, error) {
	var out struct {
		NewsID uint32 `json:"news_id"`
	}
	err := c.h.do(ctx, http.MethodPost, "/admin/v1/channels/"+url.PathEscape(topic)+"/articles", a, &out)
	return out.NewsID, err
}

// Publish ships the News source tree to consoles and returns the build.
func (c *HTTPNews) Publish(ctx context.Context, note string) (int, error) {
	var out struct {
		Build int `json:"build"`
	}
	var body any
	if note != "" {
		body = map[string]any{"note": note}
	}
	err := c.h.do(ctx, http.MethodPost, "/admin/v1/publish", body, &out)
	return out.Build, err
}

// maxImage caps an image Pakka will fetch and forward.
const maxImage = 4 << 20

// FetchImage downloads an image from one of our own hosts and returns it
// base64-encoded. Only https, only the allowlisted hosts (and their
// subdomains), no redirect off the list, JPEG only (OpenVerse's screenshot
// pipeline takes nothing else).
func FetchImage(ctx context.Context, raw string, allowed []string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("%w: image must be an https URL", ErrBadAction)
	}
	if !hostAllowed(u.Hostname(), allowed) {
		return "", fmt.Errorf("%w: image host %q is not one of our own assets", ErrBadAction, u.Hostname())
	}
	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 3 || req.URL.Scheme != "https" || !hostAllowed(req.URL.Hostname(), allowed) {
				return errors.New("redirect leaves our own assets")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("image: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImage+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxImage {
		return "", fmt.Errorf("%w: image is larger than %d bytes", ErrBadAction, maxImage)
	}
	if len(data) < 3 || data[0] != 0xFF || data[1] != 0xD8 || data[2] != 0xFF {
		return "", fmt.Errorf("%w: image must be a JPEG", ErrBadAction)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func hostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(host)
	for _, a := range allowed {
		a = strings.ToLower(a)
		if host == a || strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}
