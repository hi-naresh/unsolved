package partials

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hi-naresh/unsolved/internal/views"
)

var stepMarker = regexp.MustCompile(`^\s*(?:\d{1,2}\s*[.)\-:]|[-*•–])\s*`)

// Steps turns free text into a list of short steps: one per line, with any
// "1." / "-" / "•" markers stripped. A single paragraph with several sentences
// becomes one step per sentence, so every process renders as a flow.
func Steps(text string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(stepMarker.ReplaceAllString(line, ""))
		if line != "" {
			out = append(out, line)
		}
	}
	if len(out) == 1 {
		if s := sentences(out[0]); len(s) > 1 {
			return s
		}
	}
	return out
}

func sentences(p string) []string {
	var out []string
	start := 0
	for i := 0; i < len(p)-1; i++ {
		if (p[i] == '.' || p[i] == '?' || p[i] == '!') && p[i+1] == ' ' {
			if s := strings.TrimSpace(p[start : i+1]); s != "" {
				out = append(out, s)
			}
			start = i + 1
		}
	}
	if s := strings.TrimSpace(p[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// StepLabel shortens a step to a few words for the compact process map:
// the first clause, cut to about 42 characters on a word boundary.
func StepLabel(step string) string {
	s := step
	for _, sep := range []string{"; ", ": ", " — ", " - ", ", then ", ". "} {
		if i := strings.Index(s, sep); i >= 18 {
			s = s[:i]
		}
	}
	s = strings.TrimRight(s, ".")
	return Excerpt(s, 72)
}

// Excerpt returns the first sentence or line of text, cut to max runes on a
// word boundary with an ellipsis.
func Excerpt(text string, max int) string {
	text = strings.Join(strings.Fields(stepMarker.ReplaceAllString(text, "")), " ")
	if i := strings.Index(text, ". "); i > 40 && i < max {
		return text[:i+1]
	}
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	r := []rune(text)[:max]
	cut := string(r)
	if i := strings.LastIndex(cut, " "); i > max/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:-") + "…"
}

// AuthorText is authorship as plain text, for places a link can't go (inside
// a card that is itself a link). It follows the same anonymity rules as
// views.Author.
func AuthorText(a views.AuthorRef) string {
	var s string
	switch {
	case a.Anonymous:
		s = "Anonymous"
	case a.Deleted:
		return "deleted user"
	default:
		s = "@" + a.Handle
	}
	if a.Tier != "" && (a.Tier != "member" || a.Anonymous) {
		s += " · " + views.TierLabel(a.Tier)
	}
	return s
}

// Ago is a compact relative time ("3d", "5mo").
func Ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return strconv.Itoa(max(1, int(d.Minutes()))) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	case d < 30*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	case d < 365*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/24/30)) + "mo"
	}
	return strconv.Itoa(int(d.Hours()/24/365)) + "y"
}

// StateChip is the chip class for a problem state.
func StateChip(state string) string {
	switch state {
	case "solved":
		return "chip chip-solved"
	case "invalid":
		return "chip chip-invalid"
	}
	return "chip chip-open"
}
