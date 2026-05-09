package source

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/selmakcby/briefer/internal/item"
)

const anthropicSitemapURL = "https://www.anthropic.com/sitemap.xml"

// Anthropic publishes a sitemap.xml but no RSS feed. We extract /news/* entries
// and synthesize titles from the slug — good enough as a feed substitute, and
// no LLM is needed to re-extract titles later.
type anthropicSource struct {
	client *http.Client
}

func (a *anthropicSource) Name() string { return "anthropic" }

type sitemapURLSet struct {
	URLs []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod"`
}

func (a *anthropicSource) Fetch(ctx context.Context) ([]item.Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, anthropicSitemapURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", hnUserAgent)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch sitemap: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch sitemap: HTTP %d", resp.StatusCode)
	}

	var set sitemapURLSet
	if err := xml.NewDecoder(resp.Body).Decode(&set); err != nil {
		return nil, fmt.Errorf("decode sitemap: %w", err)
	}

	type entry struct {
		url     string
		title   string
		modTime time.Time
	}
	var entries []entry
	for _, u := range set.URLs {
		if !strings.HasPrefix(u.Loc, "https://www.anthropic.com/news/") {
			continue
		}
		slug := strings.TrimPrefix(u.Loc, "https://www.anthropic.com/news/")
		slug = strings.TrimSuffix(slug, "/")
		if slug == "" {
			continue
		}
		mod, _ := time.Parse(time.RFC3339, u.LastMod)
		entries = append(entries, entry{
			url:     u.Loc,
			title:   titleFromSlug(slug),
			modTime: mod.UTC(),
		})
	}

	// Newest first, cap at 30.
	sort.Slice(entries, func(i, j int) bool { return entries[i].modTime.After(entries[j].modTime) })
	if len(entries) > 30 {
		entries = entries[:30]
	}

	out := make([]item.Item, 0, len(entries))
	for _, e := range entries {
		out = append(out, item.Item{
			Title:       e.title,
			URL:         e.url,
			Source:      "anthropic",
			PublishedAt: e.modTime,
		})
	}
	return out, nil
}

// titleFromSlug renders "claude-opus-4-7" → "Claude Opus 4 7".
// Imperfect but readable, and downstream summarizer can polish.
func titleFromSlug(slug string) string {
	parts := strings.Split(slug, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		// Don't TitleCase numbers.
		if isNumeric(p) {
			parts[i] = p
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
