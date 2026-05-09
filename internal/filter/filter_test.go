package filter

import (
	"strings"
	"testing"

	"github.com/selmakcby/briefer/internal/item"
)

func TestExplodeKeywords(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "comma list",
			input: "Claude Code, MCP, Skills, Agents",
			want:  []string{"claude code", "mcp", "skills", "agents"},
		},
		{
			name:  "em-dash and parens",
			input: "Claude (Anthropic) — releases, Claude Code, MCP",
			want:  []string{"claude", "anthropic", "claude code", "mcp"},
		},
		{
			name:  "drops stopwords",
			input: "Claude and Anthropic for Agents",
			want:  []string{"claude and anthropic for agents"}, // single term — not split because no separators
		},
		{
			name:  "drops short tokens",
			input: "AI, ML, GPT, MCP",
			want:  []string{"gpt", "mcp"}, // ai and ml are <=2 chars, dropped
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := explodeKeywords(tc.input)
			if !equalStringSlices(got, tc.want) {
				t.Errorf("explodeKeywords(%q) = %v; want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseInterests_WantAndSkip(t *testing.T) {
	src := `# Interests

## Core focus

- Claude Code, MCP

## Skip

- crypto, web3
`
	got, err := ParseInterests(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(got.Want, "claude code") || !contains(got.Want, "mcp") {
		t.Errorf("want list missing expected terms: %v", got.Want)
	}
	if !contains(got.Skip, "crypto") || !contains(got.Skip, "web3") {
		t.Errorf("skip list missing expected terms: %v", got.Skip)
	}
}

func TestApply_KeepsMatching(t *testing.T) {
	items := []item.Item{
		{Title: "New Claude Code release", Snippet: "Anthropic ships agent skills"},
		{Title: "Bitcoin hits all-time high", Snippet: "Crypto market"},
		{Title: "MCP server for Postgres", Snippet: "Model Context Protocol"},
	}
	interests := Interests{
		Want: []string{"claude code", "mcp"},
		Skip: []string{"crypto"},
	}
	out := Apply(items, interests)
	if len(out) != 2 {
		t.Fatalf("expected 2 items, got %d: %+v", len(out), out)
	}
	if out[0].Title != "New Claude Code release" || out[1].Title != "MCP server for Postgres" {
		t.Errorf("unexpected items kept: %+v", out)
	}
}

func TestApply_SkipOverridesWant(t *testing.T) {
	items := []item.Item{
		{Title: "Claude Code crypto integration", Snippet: ""},
	}
	interests := Interests{
		Want: []string{"claude code"},
		Skip: []string{"crypto"},
	}
	out := Apply(items, interests)
	if len(out) != 0 {
		t.Errorf("skip term should override want; got %+v", out)
	}
}

func TestApply_NoWantPassesEverythingExceptSkip(t *testing.T) {
	items := []item.Item{
		{Title: "Anything goes"},
		{Title: "But not crypto news"},
	}
	interests := Interests{
		Skip: []string{"crypto"},
	}
	out := Apply(items, interests)
	if len(out) != 1 || out[0].Title != "Anything goes" {
		t.Errorf("expected only the non-crypto item; got %+v", out)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
