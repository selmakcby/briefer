// briefer — token-efficient morning briefing CLI.
//
// Subcommands:
//   fetch <source>       fetch one source (hn, arxiv, techcrunch, anthropic) → JSON on stdout
//   fetch-all            fetch every registered source in parallel → merged JSON
//   filter               read JSON from stdin, drop off-topic items by interests.md
//   dedupe               read JSON from stdin, drop items already seen in history dir
//   pipeline             fetch-all | filter | dedupe in one shot (deterministic stages)
//   sources              list registered sources
//   version              print version
//
// All commands write JSON to stdout (suitable for piping). Errors go to stderr.
//
// Example — full deterministic prep, ready for an LLM summarizer:
//   briefer pipeline \
//     --interests vault/interests.md \
//     --history vault/daily \
//     > /tmp/survivors.json
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/selmakcby/briefer/internal/dedupe"
	"github.com/selmakcby/briefer/internal/filter"
	"github.com/selmakcby/briefer/internal/item"
	"github.com/selmakcby/briefer/internal/source"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "fetch":
		err = cmdFetch(ctx, args)
	case "fetch-all":
		err = cmdFetchAll(ctx, args)
	case "filter":
		err = cmdFilter(args)
	case "dedupe":
		err = cmdDedupe(args)
	case "pipeline":
		err = cmdPipeline(ctx, args)
	case "sources":
		fmt.Println(joinNames(source.All()))
	case "version", "--version", "-v":
		fmt.Println("briefer", version)
	case "help", "--help", "-h":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage(os.Stderr)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func cmdFetch(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("fetch: missing source name (have: %v)", source.All())
	}
	s, err := source.Get(args[0])
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	items, err := s.Fetch(cctx)
	if err != nil {
		return err
	}
	return item.Batch{Items: items}.WriteJSON(os.Stdout)
}

func cmdFetchAll(ctx context.Context, _ []string) error {
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	items, err := source.FetchAll(cctx)
	if err != nil {
		// Don't fail hard — partial results are still useful. Log to stderr.
		fmt.Fprintln(os.Stderr, "warn:", err)
	}
	sortByPublished(items)
	return item.Batch{Items: items}.WriteJSON(os.Stdout)
}

func cmdFilter(args []string) error {
	fs := flag.NewFlagSet("filter", flag.ExitOnError)
	interestsPath := fs.String("interests", "vault/interests.md", "path to interests file")
	inputPath := fs.String("in", "-", "input json file (- for stdin)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	in, closer, err := openInput(*inputPath)
	if err != nil {
		return err
	}
	defer closer()

	batch, err := item.ReadBatch(in)
	if err != nil {
		return fmt.Errorf("read input: %w", err)
	}

	interests, err := filter.LoadInterests(*interestsPath)
	if err != nil {
		return fmt.Errorf("load interests: %w", err)
	}

	out := filter.Apply(batch.Items, interests)
	return item.Batch{Items: out}.WriteJSON(os.Stdout)
}

func cmdDedupe(args []string) error {
	fs := flag.NewFlagSet("dedupe", flag.ExitOnError)
	historyDir := fs.String("history", "vault/daily", "directory of past daily briefings")
	lookback := fs.Int("lookback", 7, "number of recent files to scan")
	inputPath := fs.String("in", "-", "input json file (- for stdin)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	in, closer, err := openInput(*inputPath)
	if err != nil {
		return err
	}
	defer closer()

	batch, err := item.ReadBatch(in)
	if err != nil {
		return fmt.Errorf("read input: %w", err)
	}

	out, err := dedupe.Apply(batch.Items, *historyDir, *lookback)
	if err != nil {
		return err
	}
	return item.Batch{Items: out}.WriteJSON(os.Stdout)
}

func cmdPipeline(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("pipeline", flag.ExitOnError)
	interestsPath := fs.String("interests", "vault/interests.md", "path to interests file")
	historyDir := fs.String("history", "vault/daily", "directory of past daily briefings")
	lookback := fs.Int("lookback", 7, "number of recent files to scan")
	maxAge := fs.Duration("max-age", 24*time.Hour, "drop items older than this (e.g. 24h, 12h, 48h)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	items, err := source.FetchAll(cctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warn:", err)
	}
	sortByPublished(items)
	fmt.Fprintf(os.Stderr, "fetched: %d items\n", len(items))

	if *maxAge > 0 {
		items = filterByAge(items, *maxAge)
		fmt.Fprintf(os.Stderr, "after max-age (%s): %d items\n", *maxAge, len(items))
	}

	interests, err := filter.LoadInterests(*interestsPath)
	if err != nil {
		return fmt.Errorf("load interests: %w", err)
	}
	items = filter.Apply(items, interests)
	fmt.Fprintf(os.Stderr, "after filter: %d items\n", len(items))

	items, err = dedupe.Apply(items, *historyDir, *lookback)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "after dedupe: %d items\n", len(items))

	return item.Batch{Items: items}.WriteJSON(os.Stdout)
}

// filterByAge drops items whose PublishedAt is older than maxAge ago.
// Items with a zero PublishedAt are kept (we don't penalize unparseable dates).
func filterByAge(items []item.Item, maxAge time.Duration) []item.Item {
	cutoff := time.Now().Add(-maxAge)
	out := items[:0:0]
	for _, it := range items {
		if it.PublishedAt.IsZero() {
			out = append(out, it)
			continue
		}
		if it.PublishedAt.After(cutoff) {
			out = append(out, it)
		}
	}
	return out
}

func sortByPublished(items []item.Item) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].PublishedAt.After(items[j].PublishedAt)
	})
}

func openInput(path string) (io.Reader, func(), error) {
	if path == "" || path == "-" {
		return os.Stdin, func() {}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { f.Close() }, nil
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += "\n"
		}
		out += n
	}
	return out
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `briefer %s — token-efficient morning briefing pipeline.

Usage:
  briefer fetch <source>             fetch one source (json on stdout)
  briefer fetch-all                  fetch every source in parallel
  briefer filter --interests FILE    drop off-topic items (reads stdin)
  briefer dedupe --history DIR       drop items already in history (reads stdin)
  briefer pipeline                   fetch-all | filter | dedupe in one shot
  briefer sources                    list registered sources
  briefer version                    print version

Sources: %s

Example (deterministic prep for an LLM):
  briefer pipeline \
    --interests vault/interests.md \
    --history vault/daily \
    > /tmp/survivors.json

`, version, joinSpaces(source.All()))
}

func joinSpaces(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += " "
		}
		out += n
	}
	return out
}
