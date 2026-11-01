package publisher

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// mirrorState remembers which News articles were already mirrored (or
// deliberately skipped), so a restart never reposts. The first pass of a
// fresh state only baselines: the existing archive is not flooded into
// OpenVerse.
type mirrorState struct {
	mu   sync.Mutex
	file jsonFile
	data struct {
		Baselined bool            `json:"baselined"`
		Seen      map[string]bool `json:"seen"`
	}
}

func newMirrorState(dir string) (*mirrorState, error) {
	f, err := newJSONFile(dir, "publisher-mirror.json")
	if err != nil {
		return nil, err
	}
	m := &mirrorState{file: f}
	m.data.Seen = map[string]bool{}
	if err := f.load(&m.data); err != nil {
		return nil, fmt.Errorf("mirror state: %w", err)
	}
	if m.data.Seen == nil {
		m.data.Seen = map[string]bool{}
	}
	return m, nil
}

func (m *mirrorState) has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.Seen[key]
}

func (m *mirrorState) add(keys ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		m.data.Seen[k] = true
	}
	_ = m.file.save(m.data)
}

func (m *mirrorState) baselined() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data.Baselined
}

func (m *mirrorState) setBaselined(keys []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		m.data.Seen[k] = true
	}
	m.data.Baselined = true
	_ = m.file.save(m.data)
}

func mirrorKey(topic string, id uint32) string {
	return topic + ":" + strconv.FormatUint(uint64(id), 10)
}

// markMirrored records an article Pakka itself just wrote, so the mirror
// never turns a "send as News" post back into a second post.
func (s *Service) markMirrored(topic string, id uint32) { s.mir.add(mirrorKey(topic, id)) }

// generalCommunity is where a channel's articles go when its game has no
// OpenVerse community of its own.
var generalCommunity = map[string]string{
	"openpak_news": "Announcements", "openpak_workshop": "Announcements",
	"openpak_games": "Switch", "openpak_help": "Help",
}

// communityFor finds the OpenVerse community for a News channel: the
// community of one of its games when there is one, else a general room.
func (s *Service) communityFor(ctx context.Context, ch NewsChannel) CommunityRef {
	for _, app := range ch.ApplicationIDs {
		if c, ok, err := s.ov.FindCommunity(ctx, CommunityRef{TitleID: app}); err == nil && ok {
			return CommunityRef{ID: c.ID}
		}
	}
	name := generalCommunity[ch.TopicID]
	if name == "" {
		name = "Switch"
	}
	return CommunityRef{Name: name}
}

func (s *Service) mirrorAction(ch NewsChannel, art NewsArticle, ref CommunityRef) Action {
	return Action{
		Tool: ToolPost, Category: CatNewsMirror, Template: "article",
		EventID:   "news:" + mirrorKey(art.TopicID, art.NewsID),
		Community: ref,
		Facts:     Facts{"title": art.Title, "body": art.Body, "topic": ch.TopicID},
		Discuss:   true,
	}
}

// MirrorPass mirrors the articles that appeared since the last pass. It
// returns how many posts it made.
func (s *Service) MirrorPass(ctx context.Context) (int, error) {
	if s.news == nil || s.ov == nil {
		return 0, ErrNotConfigured
	}
	chans, err := s.news.Channels(ctx)
	if err != nil {
		return 0, err
	}
	type item struct {
		ch  NewsChannel
		art NewsArticle
	}
	var fresh []item
	var all []string
	for _, ch := range chans {
		if ch.Retired {
			continue
		}
		arts, err := s.news.Articles(ctx, ch.TopicID)
		if err != nil {
			return 0, err
		}
		for _, a := range arts {
			if a.Retired || !a.EverPublished {
				continue
			}
			k := mirrorKey(ch.TopicID, a.NewsID)
			all = append(all, k)
			if !s.mir.has(k) {
				a.TopicID = ch.TopicID
				fresh = append(fresh, item{ch, a})
			}
		}
	}
	if !s.mir.baselined() {
		s.mir.setBaselined(all)
		s.log.Info("news mirror baselined", "articles", len(all))
		return 0, nil
	}
	posted := 0
	for _, it := range fresh {
		if posted >= 10 {
			break
		}
		ref := s.communityFor(ctx, it.ch)
		out, err := s.Submit(ctx, System("news-mirror"), s.mirrorAction(it.ch, it.art, ref), false)
		if err != nil {
			s.log.Warn("news mirror post failed", "article", mirrorKey(it.art.TopicID, it.art.NewsID), "err", err)
			if errors.Is(err, ErrKilled) || isCategoryOff(err) {
				return posted, err
			}
			continue
		}
		if out.Mode == "published" {
			s.mir.add(mirrorKey(it.art.TopicID, it.art.NewsID))
			if out.Result != nil && out.Result.Status == "published" {
				posted++
			}
		}
	}
	return posted, nil
}

func isCategoryOff(err error) bool { return errors.Is(err, ErrCategoryDisabled) }

// MirrorOne mirrors one article on request (staff tool and live checks),
// whether or not it was seen before. The post is still idempotent.
func (s *Service) MirrorOne(ctx context.Context, topic string, id uint32) (Outcome, error) {
	if s.news == nil || s.ov == nil {
		return Outcome{}, ErrNotConfigured
	}
	chans, err := s.news.Channels(ctx)
	if err != nil {
		return Outcome{}, err
	}
	for _, ch := range chans {
		if ch.TopicID != topic {
			continue
		}
		arts, err := s.news.Articles(ctx, topic)
		if err != nil {
			return Outcome{}, err
		}
		for _, a := range arts {
			if a.NewsID == id {
				a.TopicID = topic
				out, err := s.Submit(ctx, System("news-mirror"), s.mirrorAction(ch, a, s.communityFor(ctx, ch)), false)
				if err == nil {
					s.mir.add(mirrorKey(topic, id))
				}
				return out, err
			}
		}
		return Outcome{}, fmt.Errorf("%w: article %d not in %s", ErrBadAction, id, topic)
	}
	return Outcome{}, fmt.Errorf("%w: no channel %q", ErrBadAction, topic)
}

// RunMirror polls on an interval until ctx ends.
func (s *Service) RunMirror(ctx context.Context, every time.Duration) {
	if every <= 0 {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		pass := func() {
			c, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			if n, err := s.MirrorPass(c); err != nil {
				s.log.Warn("news mirror pass", "err", err)
			} else if n > 0 {
				s.log.Info("news mirror posted", "posts", n)
			}
		}
		pass()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
