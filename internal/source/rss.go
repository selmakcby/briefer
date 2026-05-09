package source

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/selmakcby/briefer/internal/item"
)

const (
	techCrunchRSSURL = "https://techcrunch.com/feed/"
)

// Anthropic doesn't expose an RSS feed; see anthropic.go for the sitemap-based fetcher.
func init() {
	Register(&rssSource{name: "techcrunch", url: techCrunchRSSURL, client: rssClient()})
	Register(&anthropicSource{client: rssClient()})
}

func rssClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second}
}

type rssSource struct {
	name   string
	url    string
	client *http.Client
}

func (r *rssSource) Name() string { return r.name }

// rss2 mirrors the standard RSS 2.0 channel/item shape.
type rss2 struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}

var htmlTagRE = regexp.MustCompile(`<[^>]*>`)

func (r *rssSource) Fetch(ctx context.Context) ([]item.Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", hnUserAgent)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch: HTTP %d", resp.StatusCode)
	}

	var feed rss2
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	items := make([]item.Item, 0, len(feed.Channel.Items))
	for _, e := range feed.Channel.Items {
		title := strings.TrimSpace(e.Title)
		url := strings.TrimSpace(e.Link)
		if title == "" || url == "" {
			continue
		}
		// pubDate format varies; try a couple common ones.
		published, _ := parseRSSDate(e.PubDate)
		// Strip HTML tags from description for a clean snippet.
		snippet := htmlTagRE.ReplaceAllString(e.Description, " ")
		snippet = strings.Join(strings.Fields(snippet), " ")
		items = append(items, item.Item{
			Title:       title,
			URL:         url,
			Source:      r.name,
			PublishedAt: published.UTC(),
			Snippet:     trimSnippet(snippet, 320),
		})
	}
	if len(items) > 30 {
		items = items[:30]
	}
	return items, nil
}

func parseRSSDate(s string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"2006-01-02T15:04:05Z07:00",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date %q", s)
}
