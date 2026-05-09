<p align="center">
  <img src="./banner.svg" alt="briefer — token-efficient news pipeline" width="100%">
</p>

# briefer

A token-efficient morning briefing pipeline. Built for [Claude Code routines](https://code.claude.com/docs/en/routines), but works as a standalone CLI.

The premise: in a routine that fetches news, filters it, deduplicates, then summarizes — only the summarization should burn LLM tokens. Everything else is deterministic work that a small Go binary does in ~30 ms.

## What it does

```
sources           | filter           | dedupe            | LLM (later)
------------------|------------------|-------------------|------------------
hn                | interests.md     | vault/daily/*.md  | summarize
arxiv             | --skip negatives | last N days       | extract todos
techcrunch        |                  |                   | post to Discord
anthropic         |                  |                   |
```

Stages 1–3 run in `briefer`. Only the LLM stage talks to Claude.

## Why

A naive routine implements the four fetchers as Haiku agents. Each agent burns ~14 K tokens just to call `WebFetch` and parse JSON. Across four sources plus filter and dedupe agents, the deterministic parts of the pipeline use ~85 K tokens before the model has done a single thought.

Replacing those stages with a Go CLI brings that to ~0 tokens. The summarizer agent still runs (that's the actual LLM work), but it gets clean structured input instead of having to drive the fetch.

Approximate savings on a 5-source pipeline: **~70% fewer tokens per run** with the same final output.

## Install

### Local terminal (Go installed)

```bash
go install github.com/selmakcby/briefer/cmd/briefer@latest
# binary lands in $(go env GOPATH)/bin/briefer — make sure that's on your PATH
```

Or build from source:

```bash
git clone https://github.com/selmakcby/briefer
cd briefer
go build -o briefer ./cmd/briefer
./briefer pipeline --interests vault/interests.md --history vault/daily
```

### Claude Code cloud routine (no Go)

Cloud routines run in a sandboxed Linux env with **no Go toolchain**, so `go install` won't work there. Instead, cross-compile locally and ship the binaries inside your routine repo:

```bash
# from a clone of this repo
GOOS=linux GOARCH=amd64 go build -o bin/briefer-linux-amd64 ./cmd/briefer
GOOS=linux GOARCH=arm64 go build -o bin/briefer-linux-arm64 ./cmd/briefer
```

Copy `bin/briefer-linux-{amd64,arm64}` into your routine's git repo (anywhere — `bin/` is a clean default). Commit, push. The routine prompt then picks the right binary at runtime based on `uname -m` — see "Routine integration" below for the snippet.

End-to-end reference implementation: **[selmakcby/sabah-asistani](https://github.com/selmakcby/sabah-asistani)** — a private morning-briefing routine that bundles these binaries, calls `briefer pipeline`, hands the survivors to two LLM agents (`summarizer`, `todo-maker`), and posts to Discord. The repo's `routine-prompt.md`, `DEPLOY.md`, and `bin/` show the full deploy pattern.

## Usage

### Single source

```bash
briefer fetch hn          # → JSON on stdout
briefer fetch arxiv
briefer fetch techcrunch
briefer fetch anthropic
```

### All sources, in parallel

```bash
briefer fetch-all > items.json
```

### Filter against your interests

`vault/interests.md` is a markdown bullet list:

```markdown
## Core focus

- Claude (Anthropic) — releases, model updates, Claude Code, MCP, Skills, Agents
- AI agents — multi-agent systems, agent frameworks
- Model Context Protocol (MCP)

## Skip

- Crypto, web3, NFT
- AI hype pieces
```

Each bullet is exploded into individual keywords (`Claude`, `Anthropic`, `Claude Code`, `MCP`, …), and an item is kept if its title or snippet contains any positive keyword and no negative keyword.

```bash
briefer filter --interests vault/interests.md < items.json > filtered.json
```

### Dedupe against past briefings

```bash
briefer dedupe --history vault/daily --lookback 7 < filtered.json > new.json
```

The deduper scans the most recent N daily files and drops any item whose URL or title appears in them.

### Full pipeline

The three deterministic stages combined:

```bash
briefer pipeline \
  --interests vault/interests.md \
  --history vault/daily \
  > /tmp/survivors.json
```

This is what you call from a Claude Code routine. The output is ready for an LLM summarizer to read.

## Routine integration

Replace the deterministic stages of your routine prompt with a single shell call. The fetcher / filterer / dedupe LLM agents can be deleted entirely — `briefer` does it in zero tokens. The remaining LLM work (summarize, extract todos) keeps its agents. Permissions stay tight: `briefer` only needs `Bash`.

### Step 1 — pick the binary at runtime

Bundle both Linux binaries in `bin/` (see "Install → Claude Code cloud routine" above), then in the routine prompt:

```bash
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  BRIEFER=./bin/briefer-linux-amd64 ;;
  aarch64) BRIEFER=./bin/briefer-linux-arm64 ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac
chmod +x "$BRIEFER"
```

### Step 2 — run the pipeline

```bash
$BRIEFER pipeline \
  --interests vault/interests.md \
  --history vault/daily \
  --max-age 24h \
  > /tmp/items.json
```

`--max-age 24h` is the default; pass a different duration (e.g. `12h`, `48h`) to widen or narrow the window.

### Step 3 — hand off to the LLM

Read `/tmp/items.json`, pass the `items` array to your summarizer agent. The summarizer no longer drives the fetch — it just reads structured input and writes a briefing.

### Step 4 — network allowlist

Cloud routines run with a network allowlist. The default "Trusted" preset blocks some sources. Use **Custom network** in the routine's environment settings and allow:

```
news.ycombinator.com    hn.algolia.com
export.arxiv.org        arxiv.org
techcrunch.com
www.anthropic.com
discord.com             github.com
```

For a fully working routine prompt you can copy and adapt, see [`sabah-asistani/routine-prompt.md`](https://github.com/selmakcby/sabah-asistani/blob/main/routine-prompt.md).

## Sources

| Source | Endpoint | Format |
|---|---|---|
| `hn` | `https://hn.algolia.com/api/v1/search?tags=front_page` | JSON |
| `arxiv` | `https://export.arxiv.org/api/query?search_query=cat:cs.AI` | Atom |
| `techcrunch` | `https://techcrunch.com/feed/` | RSS 2.0 |
| `anthropic` | `https://www.anthropic.com/sitemap.xml` (no public RSS) | Sitemap XML |

To add a new source, drop a file in `internal/source/` that registers itself in `init()` and implements the two-method `Source` interface. See `internal/source/hn.go` for the simplest example.

## Output shape

Every command emits the same JSON envelope:

```json
{
  "items": [
    {
      "title": "Teaching Claude Why",
      "url": "https://www.anthropic.com/research/teaching-claude-why",
      "source": "anthropic",
      "published_at": "2026-05-08T00:00:00Z",
      "snippet": "...",
      "score": 0
    }
  ]
}
```

Predictable, pipeable, machine-readable.

## License

MIT.

## Related

Built as the V7 follow-up to [@selma.builds](https://www.youtube.com/@selma.builds)' V6 video on the morning-assistant routine. The V6 system used five LLM agents for what `briefer` does in zero — that's the lesson.
