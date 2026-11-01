package chat

import "regexp"

// Everything sent to the model passes through Sanitize first: Meta's terms
// for the contributor tier ask that personal information is not submitted, so
// identifiers are removed from user text and tool results before they leave.
var sanitizers = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)+`), "[email]"},
	{regexp.MustCompile(`(?i)\bSW-(?:[0-9A-F]{4}-?){3,4}\b`), "[friend code]"},
	{regexp.MustCompile(`\b\d{4}-\d{4}-\d{4}\b`), "[friend code]"},
	{regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`), "[ip]"},
	{regexp.MustCompile(`(?i)\b(?:[0-9a-f]{0,4}:){3,}[0-9a-f:.]*\b`), "[ip]"},
	{regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), "[id]"},
	{regexp.MustCompile(`\b[0-9a-fA-F]{16,}\b`), "[id]"},
	// Console serials: Switch XAW/XKW/XAJ..., Wii U/3DS letters plus digits.
	{regexp.MustCompile(`\bX[A-Z]{2}[0-9A-Z]{9,}\b`), "[serial]"},
	{regexp.MustCompile(`\b(?:[A-Z]{2,3}\d{8,11})\b`), "[serial]"},
	{regexp.MustCompile(`\b(?:\+?\d[\d\s().\-]{8,}\d)\b`), "[number]"},
	{regexp.MustCompile(`(?i)\b(?:password|passwort|contraseña|mot de passe|senha)\s*(?:is|ist|es|est|é|:|=)\s*\S+`), "[secret]"},
	{regexp.MustCompile(`\bopk_[A-Za-z0-9_\-]{10,}\b`), "[token]"},
}

// Sanitize strips emails, friend codes, IPs, ids, serials, numbers and
// password statements from s.
func Sanitize(s string) string {
	for _, x := range sanitizers {
		s = x.re.ReplaceAllString(s, x.with)
	}
	return s
}
