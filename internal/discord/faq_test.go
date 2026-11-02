package discord

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func testFAQ() *FAQ {
	return NewFAQ("match1", time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestFAQAnswers(t *testing.T) {
	faq := testFAQ()
	i := 0
	for text, want := range map[string]string{
		"how do I install the patcher on my sd card":       "switch-patcher/releases",
		"where do homebrew updates come from on the eshop": "OpenPak eShop",
		"error code 2124-8007 cant connect":                "can't reach the network",
		"anyone playing mario kart, join me":               "find players",
		"when is ps5 supported":                            "fully live",
	} {
		i++
		msg, ok := faq.Answer(Incoming{AuthorID: fmt.Sprintf("u%d", i), ChannelID: "help1", Content: text})
		if !ok {
			t.Errorf("no answer for %q", text)
			continue
		}
		if !strings.Contains(msg, want) || !strings.Contains(msg, "Still stuck?") {
			t.Errorf("answer for %q = %q, want %q plus escalation", text, msg, want)
		}
	}
}

func TestFAQSilent(t *testing.T) {
	faq := testFAQ()
	for _, text := range []string{
		"hello everyone",                // no keywords
		"when will the update land",     // single generic hit
		"2124 error, anyone playing?",   // tie across entries
		"my game crashes on install",    // bug, not a setup how-to
		"can you add a new feature plz", // unknown
	} {
		if msg, ok := faq.Answer(Incoming{AuthorID: "u1", ChannelID: "help1", Content: text}); ok {
			t.Errorf("answered %q: %q", text, msg)
		}
	}
}

func TestFAQCooldown(t *testing.T) {
	faq := testFAQ()
	in := Incoming{AuthorID: "u1", ChannelID: "help1", Content: "how do I install the patcher on my sd card"}
	if _, ok := faq.Answer(in); !ok {
		t.Fatal("first answer missing")
	}
	if msg, ok := faq.Answer(in); ok {
		t.Fatalf("cooldown ignored: %q", msg)
	}
	in.AuthorID = "u2"
	if _, ok := faq.Answer(in); !ok {
		t.Error("cooldown leaked across authors")
	}
}
