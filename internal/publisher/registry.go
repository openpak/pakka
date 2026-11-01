package publisher

import (
	"context"
	"encoding/json"
	"fmt"

	"pakka/internal/meta"
)

// ToolSpec describes one registered publisher tool.
type ToolSpec struct {
	Name        string `json:"name"`
	Scope       Scope  `json:"scope"`
	Description string `json:"description"`
	// Mode is "auto when templated, else approval" or "approval".
	Mode string `json:"mode"`
}

// Registry lists the publisher tools and what each needs.
func Registry() []ToolSpec {
	return []ToolSpec{
		{ToolPost, ScopeOpenVersePost, "Post as the official Pakka account in an OpenVerse community or announcement, optionally with an image from our own assets and a Discuss link; may also go out as News.", "auto for templated news_mirror/splatfest/tournament/course; approval for free text"},
		{ToolCommunity, ScopeOpenVerseCommunity, "Create an OpenVerse community.", "approval"},
		{ToolNews, ScopeNewsPublish, "Publish a News article to the Switch News channels that fit.", "approval"},
		{ToolBoss, ScopeBossQueue, "Queue a Wii U/3DS BOSS notification (blocked until BOSS keys are provisioned).", "approval"},
	}
}

// ModelTools are the tools the language model may call. They all only
// propose: the model holds the draft scope and nothing else.
func ModelTools() []meta.Tool {
	obj := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required}
	}
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	return []meta.Tool{
		{Type: "function", Function: meta.FunctionDef{
			Name:        "propose_post",
			Description: "Draft an OpenVerse post as the official Pakka account. It is NOT published: a staff member must approve it first.",
			Parameters: obj(map[string]any{
				"community":    str("name of the OpenVerse community, e.g. Announcements"),
				"text":         str("the post text, at most 280 characters"),
				"send_as_news": map[string]any{"type": "boolean", "description": "also propose it as News"},
				"category":     str("announcement or highlights"),
			}, "community", "text"),
		}},
		{Type: "function", Function: meta.FunctionDef{
			Name:        "propose_community",
			Description: "Propose a new OpenVerse community. A staff member must approve it.",
			Parameters:  obj(map[string]any{"name": str("community name"), "description": str("one sentence")}, "name", "description"),
		}},
		{Type: "function", Function: meta.FunctionDef{
			Name:        "propose_news",
			Description: "Draft a News article for the Switch News channels. A staff member must approve it.",
			Parameters:  obj(map[string]any{"title": str("title, at most 100 characters"), "text": str("one paragraph")}, "title", "text"),
		}},
	}
}

// ExecModelTool runs a model tool call as the model actor and returns the
// JSON result for the model. The model can only queue drafts.
func (s *Service) ExecModelTool(ctx context.Context, name, arguments string) string {
	var args map[string]any
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return `{"error":"bad arguments"}`
		}
	}
	str := func(k string) string { v, _ := args[k].(string); return v }
	var a Action
	switch name {
	case "propose_post":
		cat := CatAnnounce
		if str("category") == CatHighlights {
			cat = CatHighlights
		}
		send, _ := args["send_as_news"].(bool)
		a = Action{Tool: ToolPost, Category: cat, Body: str("text"), Community: CommunityRef{Name: str("community")}, Discuss: true, SendAsNews: send}
		if send {
			a.Title = firstLine(a.Body)
		}
	case "propose_community":
		a = Action{Tool: ToolCommunity, Category: CatCommunity, Title: str("name"), Description: str("description")}
	case "propose_news":
		a = Action{Tool: ToolNews, Category: CatNews, Title: str("title"), Body: str("text")}
	default:
		return `{"error":"unknown tool"}`
	}
	out, err := s.Submit(ctx, Model(), a, false)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	b, _ := json.Marshal(map[string]string{"status": out.Mode, "draft_id": out.DraftID, "note": "waiting for staff approval; nothing was published"})
	return string(b)
}
