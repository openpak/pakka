// Package shopcore is Pakka's client for shop-core, the console-neutral shop catalog (card
// openpak/prds#124). Pakka keeps the durable broken-package state there and reads catalog,
// run and pause state for the `shop` staff commands.
package shopcore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// BrokenEntry mirrors shop-core's shop_broken row.
type BrokenEntry struct {
	Since   time.Time `json:"since"`
	Alerted bool      `json:"alerted"`
	Context string    `json:"context"`
}

// Console is one console's summary.
type Console struct {
	Console   string `json:"console"`
	Name      string `json:"name"`
	ReadOnly  bool   `json:"read_only"`
	Paused    bool   `json:"paused"`
	Titles    int    `json:"titles"`
	Published int    `json:"published"`
	Retired   int    `json:"retired"`
	Broken    int    `json:"broken"`
}

// Run is one sync-run ledger row.
type Run struct {
	ID      int64      `json:"id"`
	Step    string     `json:"step"`
	State   string     `json:"state"`
	Started time.Time  `json:"started"`
	Ended   *time.Time `json:"ended"`
	Summary string     `json:"summary"`
}

// Title is the subset of a catalog row the staff commands print.
type Title struct {
	TitleID   string     `json:"title_id"`
	Name      string     `json:"name"`
	Published bool       `json:"published"`
	RetiredAt *time.Time `json:"retired_at"`
}

// Client talks to shop-core's internal API.
type Client struct {
	base string
	key  string
	hc   *http.Client
}

// New returns a client; base is e.g. http://openpak-shop-core:20240.
func New(base, key string) *Client {
	return &Client{base: base, key: key, hc: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) do(ctx context.Context, actor, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Internal-Key", c.key)
	if actor != "" {
		req.Header.Set("X-Shop-Actor", actor)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode/100 != 2 {
		var e struct{ Error, Message string }
		_ = json.Unmarshal(raw, &e)
		if e.Message != "" {
			return fmt.Errorf("shop-core: %s", e.Message)
		}
		return fmt.Errorf("shop-core: status %d", resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Broken returns a console's tracked broken packages.
func (c *Client) Broken(ctx context.Context, console string) (map[string]BrokenEntry, error) {
	var out struct {
		Broken map[string]BrokenEntry `json:"broken"`
	}
	if err := c.do(ctx, "", http.MethodGet, "/v1/shop/"+console+"/broken", nil, &out); err != nil {
		return nil, err
	}
	return out.Broken, nil
}

// SetBroken replaces a console's tracked set.
func (c *Client) SetBroken(ctx context.Context, console string, set map[string]BrokenEntry) error {
	if set == nil {
		set = map[string]BrokenEntry{}
	}
	return c.do(ctx, "", http.MethodPut, "/v1/shop/"+console+"/broken", map[string]any{"broken": set}, nil)
}

// Consoles lists every console with counts.
func (c *Client) Consoles(ctx context.Context) ([]Console, error) {
	var out struct {
		Consoles []Console `json:"consoles"`
	}
	err := c.do(ctx, "", http.MethodGet, "/v1/shop/consoles", nil, &out)
	return out.Consoles, err
}

// Runs lists a console's latest sync runs.
func (c *Client) Runs(ctx context.Context, console string, n int) ([]Run, error) {
	var out struct {
		Runs []Run `json:"runs"`
	}
	err := c.do(ctx, "", http.MethodGet, fmt.Sprintf("/v1/shop/%s/runs?limit=%d", console, n), nil, &out)
	return out.Runs, err
}

// Titles lists a console's titles; retiredOnly narrows to retired ones.
func (c *Client) Titles(ctx context.Context, console string, retiredOnly bool) ([]Title, error) {
	q := url.Values{}
	if retiredOnly {
		q.Set("retired", "true")
	}
	var out struct {
		Titles []Title `json:"titles"`
	}
	err := c.do(ctx, "", http.MethodGet, "/v1/shop/"+console+"/titles?"+q.Encode(), nil, &out)
	return out.Titles, err
}

// Pause stops (or with false, resumes) a console's sync runs.
func (c *Client) Pause(ctx context.Context, actor, console string, paused bool) error {
	action := "resume"
	if paused {
		action = "pause"
	}
	return c.do(ctx, actor, http.MethodPost, "/v1/shop/"+console+"/"+action, nil, nil)
}

// Retire hides a title from new buyers; owners keep their download.
func (c *Client) Retire(ctx context.Context, actor, console, titleID, reason string) error {
	return c.do(ctx, actor, http.MethodPost, "/v1/shop/"+console+"/titles/"+titleID+"/retire", map[string]string{"reason": reason}, nil)
}

// Unretire reverses Retire.
func (c *Client) Unretire(ctx context.Context, actor, console, titleID string) error {
	return c.do(ctx, actor, http.MethodPost, "/v1/shop/"+console+"/titles/"+titleID+"/unretire", nil, nil)
}
