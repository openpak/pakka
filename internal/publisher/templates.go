package publisher

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A template turns checked facts into text. Auto-published posts can only
// use these: the wording is fixed here, the facts are validated, so no
// free text (and no model output) reaches a player unreviewed.
type template struct {
	required []string
	render   func(f Facts) (title, body string)
}

// maxFact bounds one fact value.
const maxFact = 80

var templates = map[string]map[string]template{
	CatNewsMirror: {"article": {
		required: []string{"title", "body"},
		render:   func(f Facts) (string, string) { return f["title"], f["body"] },
	}},
	CatSplatfest: {"result": {
		required: []string{"event", "winner"},
		render: func(f Facts) (string, string) {
			body := fmt.Sprintf("%s won the %s Splatfest!", f["winner"], f["event"])
			if s := f["score"]; s != "" {
				body += " Final score: " + s + "."
			}
			return "Splatfest results", body
		},
	}},
	CatTournament: {"winner": {
		required: []string{"tournament", "winner"},
		render: func(f Facts) (string, string) {
			body := fmt.Sprintf("%s won %s!", f["winner"], f["tournament"])
			if g := f["game"]; g != "" {
				body += " Game: " + g + "."
			}
			return "Tournament winner", body
		},
	}},
	CatCourse: {"featured": {
		required: []string{"course", "creator"},
		render: func(f Facts) (string, string) {
			body := fmt.Sprintf("Featured course: %s by %s.", f["course"], f["creator"])
			if g := f["game"]; g != "" {
				body += " Game: " + g + "."
			}
			return "Featured course", body
		},
	}},
}

// cleanFact normalises one fact and refuses control characters, links and
// mentions: a template takes names, never anything that could be a link.
func cleanFact(key, v string) (string, error) {
	v = strings.Join(strings.Fields(v), " ")
	for _, r := range v {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("fact %q has control characters", key)
		}
	}
	lower := strings.ToLower(v)
	if strings.Contains(lower, "http") || strings.Contains(lower, "www.") || strings.Contains(v, "@") {
		return "", fmt.Errorf("fact %q contains a link or mention", key)
	}
	return v, nil
}

// RenderTemplate validates the facts and fills the template.
func RenderTemplate(category, name string, facts Facts) (title, body string, err error) {
	t, ok := templates[category][name]
	if !ok {
		return "", "", fmt.Errorf("%w: no template %q for category %q", ErrBadAction, name, category)
	}
	clean := Facts{}
	// News mirror text is already published News copy, which may carry
	// links; the other templates take short player-supplied names.
	for k, v := range facts {
		if category == CatNewsMirror {
			clean[k] = strings.Join(strings.Fields(v), " ")
			continue
		}
		cv, err := cleanFact(k, v)
		if err != nil {
			return "", "", fmt.Errorf("%w: %v", ErrBadAction, err)
		}
		if utf8.RuneCountInString(cv) > maxFact {
			return "", "", fmt.Errorf("%w: fact %q is longer than %d", ErrBadAction, k, maxFact)
		}
		clean[k] = cv
	}
	for _, k := range t.required {
		if clean[k] == "" {
			return "", "", fmt.Errorf("%w: template %s/%s needs fact %q", ErrBadAction, category, name, k)
		}
	}
	title, body = t.render(clean)
	return title, body, nil
}
