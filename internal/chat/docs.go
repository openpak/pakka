package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Docs answers help questions from the public docs site's search index. The
// index URL comes from configuration; the model never supplies a URL.
type Docs struct {
	indexURL string
	baseURL  string
	http     *http.Client

	mu      sync.Mutex
	entries []docEntry
	fetched time.Time
}

type docEntry struct {
	Location string `json:"location"`
	Title    string `json:"title"`
	Text     string `json:"text"`
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// NewDocs builds the docs searcher.
func NewDocs(indexURL, baseURL string) *Docs {
	return &Docs{indexURL: indexURL, baseURL: strings.TrimSuffix(baseURL, "/") + "/", http: &http.Client{Timeout: 10 * time.Second}}
}

func (d *Docs) load(ctx context.Context) ([]docEntry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.entries) > 0 && time.Since(d.fetched) < time.Hour {
		return d.entries, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.indexURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return d.entries, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return d.entries, fmt.Errorf("docs index: status %d", resp.StatusCode)
	}
	var doc struct {
		Docs []docEntry `json:"docs"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&doc); err != nil {
		return d.entries, err
	}
	for i := range doc.Docs {
		doc.Docs[i].Text = strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(doc.Docs[i].Text, " "))), " ")
	}
	d.entries, d.fetched = doc.Docs, time.Now()
	return d.entries, nil
}

// Search returns the best matches for query as JSON for the model.
func (d *Docs) Search(ctx context.Context, query string) string {
	entries, err := d.load(ctx)
	if len(entries) == 0 {
		_ = err
		return `{"error":"the documentation is not reachable right now"}`
	}
	terms := []string{}
	for _, t := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127)
	}) {
		if len(t) >= 3 {
			terms = append(terms, t)
		}
	}
	if len(terms) == 0 {
		return `{"error":"give a few words to search for"}`
	}
	type scored struct {
		e docEntry
		s int
	}
	var hits []scored
	for _, e := range entries {
		lt, lx := strings.ToLower(e.Title), strings.ToLower(e.Text)
		s := 0
		for _, t := range terms {
			if strings.Contains(lt, t) {
				s += 6
			}
			s += min(strings.Count(lx, t), 5)
		}
		if s > 0 {
			hits = append(hits, scored{e, s})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].s > hits[j].s })
	type out struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Excerpt string `json:"excerpt"`
	}
	res := []out{}
	for _, h := range hits {
		if len(res) == 4 {
			break
		}
		res = append(res, out{Title: h.e.Title, URL: d.baseURL + strings.TrimPrefix(h.e.Location, "/"), Excerpt: excerpt(h.e.Text, terms, 500)})
	}
	b, _ := json.Marshal(map[string]any{"results": res})
	return string(b)
}

func excerpt(text string, terms []string, n int) string {
	lower := strings.ToLower(text)
	at := 0
	for _, t := range terms {
		if i := strings.Index(lower, t); i >= 0 {
			at = i
			break
		}
	}
	r := []rune(text)
	// Byte index to rune index approximation is fine for an excerpt window.
	start := max(0, min(len([]rune(text[:at]))-80, len(r)))
	end := min(len(r), start+n)
	return strings.TrimSpace(string(r[start:end]))
}
