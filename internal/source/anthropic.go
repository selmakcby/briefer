package source

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/selmakcby/briefer/internal/item"
)

const (
	anthropicSitemapURL = "https://www.anthropic.com/sitemap.xml"
	// Anthropic's sitemap returns lastmod (last edit), not published date.
	// We fetch each article page and extract the real publishedOn from the
	// embedded Next.js JSON, then drop anything older than this window.
	anthropicMaxAge = 14 * 24 * time.Hour
	// Bound the number of articles we resolve to keep run time predictable.
	anthropicMaxResolve = 25
	// Cap concurrency on per-article fetches to be polite to the host.
	anthropicConcurrency = 12
)

type anthropicSource struct {
	client *http.Client
}

func (a *anthropicSource) Name() string { return "anthropic" }

type anthropicSitemap struct {
	URLs []anthropicSitemapEntry `xml:"url"`
}

type anthropicSitemapEntry struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod"`
}

// publishedOnRE matches Anthropic's embedded Next.js field, e.g.
//   "publishedOn":"2024-11-25T15:50:00.000Z"
// or with escaped quotes inside JSON-in-JSON:
//   publishedOn\":\"2024-11-25T15:50:00.000Z\"
var publishedOnRE = regexp.MustCompile(`publishedOn\\?":\\?"([0-9T:.\-Z]+)\\?"`)

func (a *anthropicSource) Fetch(ctx context.Context) ([]item.Item, error) {
	urls, err := a.fetchSitemapURLs(ctx)
	if err != nil {
		return nil, err
	}

	// Resolve real publishedOn for each candidate, in parallel and bounded.
	type resolved struct {
		url       string
		title     string
		published time.Time
	}

	candidates := urls
	if len(candidates) > anthropicMaxResolve {
		candidates = candidates[:anthropicMaxResolve]
	}
	// fetchSitemapURLs already pre-filtered to a small window, but cap again
	// to be defensive (don't burn requests on long-stale URLs).
	if len(candidates) > 12 {
		candidates = candidates[:12]
	}

	out := make([]resolved, 0, len(candidates))
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, anthropicConcurrency)
	)

	for _, u := range candidates {
		wg.Add(1)
		sem <- struct{}{}
		go func(u string) {
			defer wg.Done()
			defer func() { <-sem }()

			pub, err := a.resolvePublishedOn(ctx, u)
			if err != nil || pub.IsZero() {
				return // skip articles we can't date
			}
			slug := strings.TrimPrefix(u, "https://www.anthropic.com/news/")
			slug = strings.TrimSuffix(slug, "/")
			mu.Lock()
			out = append(out, resolved{
				url:       u,
				title:     titleFromSlug(slug),
				published: pub,
			})
			mu.Unlock()
		}(u)
	}
	wg.Wait()

	// Drop items older than the max-age window.
	cutoff := time.Now().Add(-anthropicMaxAge)
	fresh := out[:0]
	for _, r := range out {
		if r.published.After(cutoff) {
			fresh = append(fresh, r)
		}
	}

	// Newest first.
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].published.After(fresh[j].published) })

	items := make([]item.Item, 0, len(fresh))
	for _, r := range fresh {
		items = append(items, item.Item{
			Title:       r.title,
			URL:         r.url,
			Source:      "anthropic",
			PublishedAt: r.published,
		})
	}
	return items, nil
}

// fetchSitemapURLs returns /news/* URLs from sitemap.xml, sorted by lastmod
// descending (we use lastmod as a coarse pre-filter only, real publishedOn is
// resolved in resolvePublishedOn).
func (a *anthropicSource) fetchSitemapURLs(ctx context.Context) ([]string, error) {
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

	var set anthropicSitemap
	if err := xml.NewDecoder(resp.Body).Decode(&set); err != nil {
		return nil, fmt.Errorf("decode sitemap: %w", err)
	}

	// Pre-filter: an article whose lastmod is older than 30 days won't have
	// a fresh publishedOn either, so skip the per-URL fetch entirely.
	cutoff := time.Now().Add(-30 * 24 * time.Hour)

	type entry struct {
		url string
		mod time.Time
	}
	var entries []entry
	for _, u := range set.URLs {
		if !strings.HasPrefix(u.Loc, "https://www.anthropic.com/news/") {
			continue
		}
		mod, err := time.Parse(time.RFC3339, u.LastMod)
		if err != nil || mod.Before(cutoff) {
			continue
		}
		entries = append(entries, entry{url: u.Loc, mod: mod})
	}
	// Newest lastmod first so the resolver hits likely-fresh articles first.
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.After(entries[j].mod) })

	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.url
	}
	return out, nil
}

// resolvePublishedOn fetches an article page and extracts the embedded
// publishedOn timestamp. Returns the zero time if the page or field is missing.
func (a *anthropicSource) resolvePublishedOn(ctx context.Context, url string) (time.Time, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return time.Time{}, err
	}
	req.Header.Set("User-Agent", hnUserAgent)

	resp, err := a.client.Do(req)
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Read enough of the body to find the field. Anthropic pages are large;
	// the publishedOn field appears in the head/early body, so 256 KB is plenty.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return time.Time{}, err
	}
	match := publishedOnRE.FindSubmatch(body)
	if len(match) < 2 {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, string(match[1]))
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// titleFromSlug renders "claude-opus-4-7" → "Claude Opus 4 7".
// Imperfect but readable; the downstream summarizer can polish.
func titleFromSlug(slug string) string {
	parts := strings.Split(slug, "-")
	for i, p := range parts {
		if p == "" || isNumeric(p) {
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
