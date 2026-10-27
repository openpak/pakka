package eshop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// StoreClient reads the eShop catalog through the store admin API
// (GET /internal/store/titles) on nx-baas's game-API listener.
type StoreClient struct {
	base    string
	key     string
	client  *http.Client
	timeout time.Duration
}

// NewStoreClient builds the catalog client; base is the API root URL.
func NewStoreClient(base, key string, timeout time.Duration) *StoreClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &StoreClient{
		base:    base,
		key:     key,
		client:  &http.Client{Timeout: timeout},
		timeout: timeout,
	}
}

type storeTitleJSON struct {
	TitleID      string `json:"title_id"`
	Published    bool   `json:"published"`
	Version      uint32 `json:"version"`
	VersionLabel string `json:"version_label"`
	RevisedAt    int64  `json:"revised_at"`
	Changelog    string `json:"changelog"`
	Distribution struct {
		Source string `json:"source"`
	} `json:"distribution"`
	Localized map[string]struct {
		Name string `json:"name"`
	} `json:"localized"`
	DefaultLang string `json:"default_lang"`
}

// Titles implements Catalog.
func (c *StoreClient) Titles(ctx context.Context) ([]Title, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/internal/store/titles", nil)
	if err != nil {
		return nil, err
	}
	if c.key != "" {
		req.Header.Set("X-Internal-Key", c.key)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("store admin %s: status %d", c.base, resp.StatusCode)
	}
	var doc struct {
		Titles []storeTitleJSON `json:"titles"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	out := make([]Title, 0, len(doc.Titles))
	for _, t := range doc.Titles {
		out = append(out, Title{
			TitleID: t.TitleID, Name: displayName(t), Version: t.Version,
			VersionLabel: t.VersionLabel, RevisedAt: t.RevisedAt,
			Changelog: t.Changelog, Source: t.Distribution.Source,
			Published: t.Published,
		})
	}
	return out, nil
}

// displayName applies the catalog language fallback: en-US, then the
// entry default, then any named language, else the title id.
func displayName(t storeTitleJSON) string {
	if v, ok := t.Localized["en-US"]; ok && v.Name != "" {
		return v.Name
	}
	if t.DefaultLang != "" {
		if v, ok := t.Localized[t.DefaultLang]; ok && v.Name != "" {
			return v.Name
		}
	}
	for _, v := range t.Localized {
		if v.Name != "" {
			return v.Name
		}
	}
	return t.TitleID
}
