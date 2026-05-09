package source

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/selmakcby/briefer/internal/item"
)

const (
	arxivURL = "http://export.arxiv.org/api/query?search_query=cat:cs.AI&sortBy=submittedDate&sortOrder=descending&max_results=20"
)

func init() {
	Register(&arxiv{client: &http.Client{Timeout: 20 * time.Second}})
}

type arxiv struct {
	client *http.Client
}

func (a *arxiv) Name() string { return "arxiv" }

// arxivFeed mirrors the Atom shape returned by export.arxiv.org.
type arxivFeed struct {
	Entries []arxivEntry `xml:"entry"`
}

type arxivEntry struct {
	Title     string       `xml:"title"`
	Summary   string       `xml:"summary"`
	Published string       `xml:"published"`
	ID        string       `xml:"id"`
	Links     []arxivLink  `xml:"link"`
}

type arxivLink struct {
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
	Href string `xml:"href,attr"`
}

func (a *arxiv) Fetch(ctx context.Context) ([]item.Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, arxivURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", hnUserAgent)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch: HTTP %d", resp.StatusCode)
	}

	var feed arxivFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	items := make([]item.Item, 0, len(feed.Entries))
	for _, e := range feed.Entries {
		title := strings.Join(strings.Fields(e.Title), " ")
		if title == "" {
			continue
		}
		// Prefer the "alternate" HTML link; fall back to ID.
		url := e.ID
		for _, l := range e.Links {
			if l.Rel == "alternate" && l.Href != "" {
				url = l.Href
				break
			}
		}
		published, _ := time.Parse(time.RFC3339, e.Published)
		items = append(items, item.Item{
			Title:       title,
			URL:         url,
			Source:      "arxiv",
			PublishedAt: published.UTC(),
			Snippet:     trimSnippet(strings.Join(strings.Fields(e.Summary), " "), 320),
		})
	}
	return items, nil
}
