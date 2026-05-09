// Package filter drops items that don't match the user's interests.
//
// Interests file format:
//   - Bullet list of phrases (lines beginning with "-" or "*")
//   - One topic per line
//   - "## Skip" sections are honored: anything under them counts as a NEGATIVE keyword
//     and an item matching a skip term is dropped even if it matches a positive term
//
// Matching is case-insensitive substring against title + snippet.
package filter

import (
	"bufio"
	"io"
	"os"
	"strings"

	"github.com/selmakcby/briefer/internal/item"
)

// Interests holds the parsed wantlist + skiplist.
type Interests struct {
	Want []string
	Skip []string
}

// LoadInterests reads and parses an interests.md file from disk.
func LoadInterests(path string) (Interests, error) {
	f, err := os.Open(path)
	if err != nil {
		return Interests{}, err
	}
	defer f.Close()
	return ParseInterests(f)
}

// ParseInterests reads the interests file from r.
func ParseInterests(r io.Reader) (Interests, error) {
	var (
		out    Interests
		mode   = "want"
		sc     = bufio.NewScanner(r)
	)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// Mode switching on H2 headings.
		if strings.HasPrefix(line, "##") {
			lower := strings.ToLower(line)
			switch {
			case strings.Contains(lower, "skip") || strings.Contains(lower, "ignore") || strings.Contains(lower, "exclude"):
				mode = "skip"
			default:
				mode = "want"
			}
			continue
		}
		// Bullet lines. Each line yields multiple keywords because the
		// human-friendly format packs several phrases into a single line:
		//   - Claude (Anthropic) — releases, model updates, Claude Code, MCP
		// We want all of: "Claude", "Anthropic", "releases", "Claude Code", "MCP"
		// to be valid match terms.
		if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") {
			body := strings.TrimSpace(strings.TrimLeft(line, "-* "))
			if body == "" {
				continue
			}
			for _, term := range explodeKeywords(body) {
				if mode == "skip" {
					out.Skip = append(out.Skip, term)
				} else {
					out.Want = append(out.Want, term)
				}
			}
		}
	}
	return out, sc.Err()
}

// explodeKeywords splits a single bullet line into individual match terms by
// breaking on commas, slashes, parentheses, em-dashes, and similar separators.
// Common stopwords are dropped so we don't match against every article that
// contains the word "and".
func explodeKeywords(s string) []string {
	// Replace structural separators with commas, then split on comma.
	repl := strings.NewReplacer(
		" — ", ",",
		" – ", ",",
		" - ", ",",
		"/", ",",
		"(", ",",
		")", ",",
		":", ",",
	)
	s = repl.Replace(s)

	stop := map[string]bool{
		"":         true,
		"and":      true,
		"or":       true,
		"the":      true,
		"a":        true,
		"an":       true,
		"of":       true,
		"for":      true,
		"new":      true,
		"updates":  true,
		"update":   true,
		"releases": true,
		"release":  true,
		"with":     true,
		"to":       true,
		"in":       true,
		"on":       true,
		"only if comparable to claude work": true,
	}

	var terms []string
	for _, raw := range strings.Split(s, ",") {
		t := strings.ToLower(strings.TrimSpace(raw))
		if t == "" || stop[t] {
			continue
		}
		// Drop tiny tokens (1-2 chars) — too noisy.
		if len(t) <= 2 {
			continue
		}
		// Strip enclosing quotes.
		t = strings.Trim(t, `"'`)
		terms = append(terms, t)
	}
	return terms
}

// Apply returns only the items that pass the interest filter.
func Apply(items []item.Item, want Interests) []item.Item {
	if len(want.Want) == 0 {
		// No positive terms: pass everything (skip-only filter).
		out := items[:0:0]
		for _, it := range items {
			if !matchesAny(it, want.Skip) {
				out = append(out, it)
			}
		}
		return out
	}
	out := items[:0:0]
	for _, it := range items {
		if matchesAny(it, want.Skip) {
			continue
		}
		if matchesAny(it, want.Want) {
			out = append(out, it)
		}
	}
	return out
}

func matchesAny(it item.Item, terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	hay := strings.ToLower(it.Title + " " + it.Snippet)
	for _, t := range terms {
		if t == "" {
			continue
		}
		if strings.Contains(hay, t) {
			return true
		}
	}
	return false
}
