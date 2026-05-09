// Package item defines the canonical shape of a news item flowing through
// briefer's pipeline. Every fetcher returns []Item; every later stage
// (filter, dedupe, output) speaks the same shape.
package item

import (
	"encoding/json"
	"io"
	"strings"
	"time"
)

// Item is a single piece of fetched content. Fields are best-effort:
// missing values are zero-value rather than fabricated.
type Item struct {
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Source      string    `json:"source"`             // "hn" | "arxiv" | "techcrunch" | "anthropic"
	PublishedAt time.Time `json:"published_at,omitempty"`
	Snippet     string    `json:"snippet,omitempty"`
	Score       int       `json:"score,omitempty"`    // optional (HN: points, arXiv: -)
}

// Batch wraps a slice for predictable JSON encoding/decoding at file boundaries.
type Batch struct {
	Items []Item `json:"items"`
}

// WriteJSON encodes the batch to w as pretty-printed JSON.
func (b Batch) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(b)
}

// ReadBatch reads a Batch from r.
func ReadBatch(r io.Reader) (Batch, error) {
	var b Batch
	if err := json.NewDecoder(r).Decode(&b); err != nil {
		return Batch{}, err
	}
	return b, nil
}

// NormalizedTitle strips and lowercases the title for fuzzy comparisons (used by dedupe).
func (i Item) NormalizedTitle() string {
	t := strings.ToLower(strings.TrimSpace(i.Title))
	// Collapse whitespace.
	return strings.Join(strings.Fields(t), " ")
}
