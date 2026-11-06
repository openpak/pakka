package publisher

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	// newsTopic is the Switch-only general News channel every console
	// subscribes to.
	newsTopic = "openpak_news"
	maxTitle  = 100
	maxBody   = 1200
)

// channelsFor picks the News channels an action goes to: the explicit list
// when given, else the channel of each title id plus the general channel.
func channelsFor(a Action, chans []NewsChannel) ([]string, error) {
	live := map[string]NewsChannel{}
	for _, c := range chans {
		if !c.Retired {
			live[c.TopicID] = c
		}
	}
	set := map[string]bool{}
	if len(a.Channels) > 0 {
		for _, t := range a.Channels {
			if _, ok := live[t]; !ok {
				return nil, fmt.Errorf("%w: news channel %q does not exist", ErrBadAction, t)
			}
			set[t] = true
		}
	} else {
		if _, ok := live[newsTopic]; ok {
			set[newsTopic] = true
		}
		for _, tid := range a.TitleIDs {
			tid = strings.ToLower(strings.TrimSpace(tid))
			for _, c := range live {
				for _, app := range c.ApplicationIDs {
					if strings.ToLower(app) == tid {
						set[c.TopicID] = true
					}
				}
			}
		}
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("%w: no News channel fits", ErrBadAction)
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

// oneParagraph squeezes text into the single paragraph News bodies allow.
func oneParagraph(s string) string { return strings.Join(strings.Fields(s), " ") }

func (s *Service) execNews(ctx context.Context, a Action) (Result, error) {
	if s.news == nil {
		return Result{}, ErrNotConfigured
	}
	key := "news:" + a.EventID
	if r, ok := s.done.get(key); ok {
		r.Status = "exists"
		return r, nil
	}
	// One publish at a time: it signs every container and takes a while.
	s.newsMu.Lock()
	defer s.newsMu.Unlock()
	chans, err := s.news.Channels(ctx)
	if err != nil {
		return Result{}, err
	}
	topics, err := channelsFor(a, chans)
	if err != nil {
		return Result{}, err
	}
	art := NewsArticleCreate{
		Title:              truncBytes(strings.TrimSpace(a.Title), maxTitle),
		Body:               truncBytes(oneParagraph(a.Body), maxBody),
		Language:           "en-GB",
		SupportedLanguages: []string{"en-GB", "en-US"},
		LifetimeDays:       14,
		Priority:           100,
		Art:                map[string]any{"motif": "globe"},
	}
	var apps []string
	for _, t := range a.TitleIDs {
		if len(t) == 16 {
			apps = append(apps, strings.ToLower(t))
		}
	}
	if len(apps) > 0 {
		art.More = map[string]any{"type": "game", "application_ids": apps}
	}
	var banner []byte
	if a.ImageURL != "" {
		b64, err := s.FetchImage(ctx, a.ImageURL)
		if err != nil {
			return Result{}, err
		}
		if banner, err = base64.StdEncoding.DecodeString(b64); err != nil {
			return Result{}, fmt.Errorf("%w: image: %v", ErrBadAction, err)
		}
	}
	var ids []string
	for _, t := range topics {
		id, err := s.news.PostArticle(ctx, t, art)
		if err == nil && banner != nil {
			err = s.news.PutArticleImage(ctx, t, id, banner)
		}
		if err != nil {
			// Articles already created stay in the source tree unpublished;
			// say which, so staff can finish by hand.
			return Result{}, fmt.Errorf("news article in %s failed (created before: %s): %w", t, orNone(ids), err)
		}
		ids = append(ids, t+":"+strconv.FormatUint(uint64(id), 10))
		s.markMirrored(t, id)
	}
	build, err := s.news.Publish(ctx, truncBytes("pakka "+a.EventID, 200))
	if err != nil {
		return Result{}, fmt.Errorf("news articles created (%s) but the publish failed: %w", strings.Join(ids, ","), err)
	}
	res := Result{Status: "published", Refs: map[string]string{"articles": strings.Join(ids, ","), "build": strconv.Itoa(build)}}
	_ = s.done.put(key, res)
	return res, nil
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "nothing"
	}
	return strings.Join(s, ",")
}

// truncBytes cuts s to at most n bytes on a rune boundary.
func truncBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !validUTF8(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
