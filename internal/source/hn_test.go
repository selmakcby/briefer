package source

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTrimSnippet(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"", 10, ""},
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is longer than ten", 10, "this is lo…"},
	}
	for _, tc := range cases {
		if got := trimSnippet(tc.in, tc.max); got != tc.want {
			t.Errorf("trimSnippet(%q,%d) = %q; want %q", tc.in, tc.max, got, tc.want)
		}
	}
}

// roundTripFunc lets us hand the hackerNews client a canned response without
// exposing the URL as a configurable field — keeps production code untouched.
type roundTripFunc func(*http.Request) *http.Response

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req), nil
}

func TestHackerNews_Fetch_ParsesAlgoliaPayload(t *testing.T) {
	body := `{
	  "hits": [
	    {"title": "Story with URL", "url": "https://example.com/a",
	     "points": 42, "objectID": "1", "created_at_i": 1700000000, "story_text": ""},
	    {"title": "Ask HN with no URL", "url": "",
	     "points": 17, "objectID": "2", "created_at_i": 1700000100,
	     "story_text": "What's the best way to ..."},
	    {"title": "", "url": "https://example.com/empty",
	     "points": 3, "objectID": "3", "created_at_i": 1700000200, "story_text": ""}
	  ]
	}`
	hn := &hackerNews{client: &http.Client{
		Transport: roundTripFunc(func(*http.Request) *http.Response {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}
		}),
		Timeout: 5 * time.Second,
	}}

	items, err := hn.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	// Empty-title item dropped → 2 items.
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d: %+v", len(items), items)
	}
	if items[0].Title != "Story with URL" || items[0].URL != "https://example.com/a" || items[0].Source != "hn" {
		t.Errorf("first item not parsed correctly: %+v", items[0])
	}
	if items[0].Score != 42 {
		t.Errorf("expected score 42, got %d", items[0].Score)
	}
	// Ask-HN with no URL should fall back to the discussion link.
	if items[1].URL != "https://news.ycombinator.com/item?id=2" {
		t.Errorf("ask-HN URL fallback failed: %q", items[1].URL)
	}
}

func TestHackerNews_Fetch_HTTPError(t *testing.T) {
	hn := &hackerNews{client: &http.Client{
		Transport: roundTripFunc(func(*http.Request) *http.Response {
			return &http.Response{
				StatusCode: 500,
				Body:       io.NopCloser(strings.NewReader("upstream broke")),
				Header:     make(http.Header),
			}
		}),
		Timeout: 5 * time.Second,
	}}

	_, err := hn.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error on HTTP 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status code: %v", err)
	}
}
