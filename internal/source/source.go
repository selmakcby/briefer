// Package source defines the Source interface and a small registry so the CLI
// can look up fetchers by name. Each source lives in its own file
// (hn.go, arxiv.go, rss.go) and registers itself in init().
package source

import (
	"context"
	"fmt"
	"sync"

	"github.com/selmakcby/briefer/internal/item"
)

// Source is anything that can fetch items from a single origin.
type Source interface {
	// Name is the lowercase identifier used on the CLI (e.g. "hn", "arxiv").
	Name() string
	// Fetch returns up to ~30 items, sorted newest-first when possible.
	// Returning an empty slice with no error is fine (e.g. the source had no fresh content).
	Fetch(ctx context.Context) ([]item.Item, error)
}

var (
	registry   = map[string]Source{}
	registryMu sync.RWMutex
)

// Register adds s to the global registry. Call from init().
func Register(s Source) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[s.Name()] = s
}

// Get returns the source registered under name.
func Get(name string) (Source, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	s, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown source %q (have: %v)", name, names())
	}
	return s, nil
}

// All returns the names of every registered source, sorted for stable output.
func All() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return names()
}

func names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	// Stable order.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// FetchAll runs every registered source concurrently and returns the merged item slice.
// Per-source errors are returned as a multi-error; partial results are still returned.
func FetchAll(ctx context.Context) ([]item.Item, error) {
	registryMu.RLock()
	sources := make([]Source, 0, len(registry))
	for _, s := range registry {
		sources = append(sources, s)
	}
	registryMu.RUnlock()

	type result struct {
		items []item.Item
		err   error
		name  string
	}

	resultsCh := make(chan result, len(sources))
	for _, s := range sources {
		go func(s Source) {
			items, err := s.Fetch(ctx)
			resultsCh <- result{items: items, err: err, name: s.Name()}
		}(s)
	}

	var (
		merged []item.Item
		errs   []error
	)
	for i := 0; i < len(sources); i++ {
		r := <-resultsCh
		if r.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.name, r.err))
			continue
		}
		merged = append(merged, r.items...)
	}

	if len(errs) > 0 {
		return merged, joinErrors(errs)
	}
	return merged, nil
}

func joinErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return fmt.Errorf("multiple source errors: %s", joinStrings(msgs, "; "))
}

func joinStrings(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}
