package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/selmakcby/briefer/internal/item"
)

const (
	hnAlgoliaURL = "https://hn.algolia.com/api/v1/search?tags=front_page&hitsPerPage=30"
	hnUserAgent  = "briefer/0.1 (+https://github.com/selmakcby/briefer)"
)

func init() {
	Register(&hackerNews{client: &http.Client{Timeout: 15 * time.Second}})
}

type hackerNews struct {
	client *http.Client
}

func (h *hackerNews) Name() string { return "hn" }

func (h *hackerNews) Fetch(ctx context.Context) ([]item.Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hnAlgoliaURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", hnUserAgent)

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch: HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Hits []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Points      int    `json:"points"`
			ObjectID    string `json:"objectID"`
			CreatedAtI  int64  `json:"created_at_i"`
			StoryText   string `json:"story_text"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	items := make([]item.Item, 0, len(payload.Hits))
	for _, hit := range payload.Hits {
		if hit.Title == "" {
			continue
		}
		// Some HN posts ("Ask HN") have no URL — fall back to the discussion link.
		url := hit.URL
		if url == "" && hit.ObjectID != "" {
			url = "https://news.ycombinator.com/item?id=" + hit.ObjectID
		}
		if url == "" {
			continue
		}
		items = append(items, item.Item{
			Title:       hit.Title,
			URL:         url,
			Source:      "hn",
			PublishedAt: time.Unix(hit.CreatedAtI, 0).UTC(),
			Snippet:     trimSnippet(hit.StoryText, 280),
			Score:       hit.Points,
		})
	}
	return items, nil
}

func trimSnippet(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
