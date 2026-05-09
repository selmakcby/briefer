// Package dedupe drops items that already appeared in recent daily briefings.
//
// History format: a directory of YYYY-MM-DD.md files. The deduper scans the
// most recent N files (default 7) and looks for URL or normalized-title overlap.
package dedupe

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/selmakcby/briefer/internal/item"
)

// Apply removes items already covered by any of the most-recent `lookback`
// files in `historyDir`. If historyDir doesn't exist, every item passes.
func Apply(items []item.Item, historyDir string, lookback int) ([]item.Item, error) {
	seenURLs, seenTitles, err := loadHistorySets(historyDir, lookback)
	if err != nil {
		return nil, err
	}
	if len(seenURLs) == 0 && len(seenTitles) == 0 {
		return items, nil
	}
	out := items[:0:0]
	for _, it := range items {
		if it.URL != "" && seenURLs[strings.ToLower(it.URL)] {
			continue
		}
		if seenTitles[it.NormalizedTitle()] {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

// loadHistorySets walks historyDir and returns sets of seen URLs and titles
// drawn from the `lookback` most recent .md files.
func loadHistorySets(historyDir string, lookback int) (map[string]bool, map[string]bool, error) {
	urls := map[string]bool{}
	titles := map[string]bool{}

	if historyDir == "" {
		return urls, titles, nil
	}
	if _, err := os.Stat(historyDir); os.IsNotExist(err) {
		return urls, titles, nil
	}

	entries, err := os.ReadDir(historyDir)
	if err != nil {
		return nil, nil, err
	}
	mdFiles := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		mdFiles = append(mdFiles, e.Name())
	}
	// YYYY-MM-DD filenames sort lexicographically by date — newest last.
	sort.Strings(mdFiles)
	if lookback > 0 && len(mdFiles) > lookback {
		mdFiles = mdFiles[len(mdFiles)-lookback:]
	}

	for _, name := range mdFiles {
		f, err := os.Open(filepath.Join(historyDir, name))
		if err != nil {
			return nil, nil, err
		}
		extractRefs(f, urls, titles)
		f.Close()
	}
	return urls, titles, nil
}

// extractRefs scans a markdown file for URLs (anything beginning with http://
// or https://) and bold-titled lines (e.g. "**Title** — source"). Both go into
// the seen-sets so future runs can suppress them.
func extractRefs(r io.Reader, urls, titles map[string]bool) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return
	}
	text := string(buf)

	// URL extraction.
	for _, prefix := range []string{"https://", "http://"} {
		i := 0
		for {
			idx := strings.Index(text[i:], prefix)
			if idx < 0 {
				break
			}
			start := i + idx
			end := start
			for end < len(text) && !isURLDelim(text[end]) {
				end++
			}
			urls[strings.ToLower(text[start:end])] = true
			i = end
		}
	}

	// Bold title extraction: lines like "**Title** — Source".
	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "**") {
			continue
		}
		end := strings.Index(l[2:], "**")
		if end < 0 {
			continue
		}
		title := strings.TrimSpace(l[2 : 2+end])
		if title == "" {
			continue
		}
		titles[normalize(title)] = true
	}
}

func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func isURLDelim(b byte) bool {
	switch b {
	case ' ', '\n', '\t', '\r', ')', ']', '"', '\'', '<', '>', ',':
		return true
	}
	return false
}
