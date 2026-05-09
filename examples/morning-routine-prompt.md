# Example: Morning briefing routine

A complete Claude Code routine prompt that uses `briefer` for fetch + filter + dedupe, then hands the survivors to two LLM agents (summarizer, todo-maker), commits the daily briefing to git, and POSTs to Discord. Drop into `/schedule` after adapting the paths and the Discord webhook.

This is the prompt powering [`selmakcby/sabah-asistani`](https://github.com/selmakcby/sabah-asistani) — a personal morning-news assistant. Cron `0 11 * * *` (07:00 America/New_York during EDT).

## Prerequisites

In your routine repo:

- `bin/briefer-linux-amd64`, `bin/briefer-linux-arm64` — cross-compiled briefer binaries (see briefer README "Install → Claude Code cloud routine")
- `vault/interests.md` — keyword list (see [`examples/interests.md`](./interests.md))
- `vault/daily/` — directory where briefings get written and dedupe history reads from
- `.claude/agents/summarizer.md` — agent that takes the JSON survivors and writes a markdown briefing
- `.claude/agents/todo-maker.md` — agent that takes the briefing and writes today's `vault/daily/YYYY-MM-DD.md`
- `.claude/skills/notify-discord.py` — Python script that POSTs the daily file to Discord (use a `User-Agent` header — Discord 403s without one)
- Routine secret: `DISCORD_WEBHOOK_URL`
- Routine network: Custom allowlist with `news.ycombinator.com`, `hn.algolia.com`, `export.arxiv.org`, `arxiv.org`, `techcrunch.com`, `www.anthropic.com`, `discord.com`, `github.com`
- Routine permissions: enable "Allow unrestricted branch pushes" if you want to push to `main` (default behavior pushes to a `claude/<name>` branch)

## Prompt to paste into `/schedule`

```
You are the orchestrator of a morning briefing routine. Two worker agents are defined under `.claude/agents/`: `summarizer` and `todo-maker`. Your job is to fire them in the correct order, then write today's briefing and notify Discord.

Today's date is in UTC. Use it for the daily filename.

Step 0 — Pre-flight (HARD GATES — refuse if any fail)

Before doing any other work, verify the runtime is healthy. If any check fails, write a single-line failure note to `vault/daily/YYYY-MM-DD.md` ("# Morning Briefing — <date>\n\nRefused at pre-flight: <reason>") and EXIT.

  - DISCORD_WEBHOOK_URL is set: `bash -c 'test -n "$DISCORD_WEBHOOK_URL"'`
    → fail = "missing webhook"
  - Network reachable: `bash -c 'curl -sI -o /dev/null -w "%{http_code}" https://news.ycombinator.com/'` returns 2xx or 3xx
    → fail = "network unreachable"
  - Vault writable: `bash -c 'mkdir -p vault/daily && touch vault/daily/.preflight && rm vault/daily/.preflight'`
    → fail = "vault not writable"

Refusing here is a feature, not a bug.

Step 1 — Pick the briefer binary for this architecture

  ARCH=$(uname -m)
  case "$ARCH" in
    x86_64)  BRIEFER=./bin/briefer-linux-amd64 ;;
    aarch64) BRIEFER=./bin/briefer-linux-arm64 ;;
    *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
  esac
  chmod +x "$BRIEFER"

If neither binary works, fall back to building from source if Go is available:
  go install github.com/selmakcby/briefer/cmd/briefer@latest && BRIEFER="$HOME/go/bin/briefer"

Step 2 — Fetch + filter + dedupe (single shell call, zero LLM tokens)

  $BRIEFER pipeline \
    --interests vault/interests.md \
    --history vault/daily \
    --max-age 24h \
    > /tmp/items.json

This call replaces what would otherwise be four fetcher agents plus a filterer and a dedupe agent. Typical run time: 200–400ms. Output: { "items": [...] }.

REFUSE GATE: if /tmp/items.json contains zero items AND briefer's stderr says "fetched: 0 items" (systemic network failure), write a "no fetch today" briefing and skip to Step 7. If the fetch worked but filter/dedupe produced zero items (a quiet news day), continue and let the summarizer return NO_BRIEFING_TODAY.

Step 3 — Summarize

Read /tmp/items.json. Call the `summarizer` agent with the items array. It returns the markdown briefing.

Step 4 — Write today's briefing

Call the `todo-maker` agent with the summarizer's output. It writes vault/daily/YYYY-MM-DD.md.

Step 5 — Commit and push

  git add vault/daily/
  git -c user.name="morning-assistant" -c user.email="bot@example.com" commit -m "briefing: $(date -u +%Y-%m-%d)"
  git push origin HEAD:main

If push fails (network, auth), log the failure and continue to Step 6 — the file is on disk and the next run's dedupe will see it.

Step 6 — Notify Discord

  python3 .claude/skills/notify-discord.py

The script reads vault/daily/$(date -u +%Y-%m-%d).md, extracts "Top stories" and "Today's todos" sections, and POSTs them to Discord as a rich embed. It reads DISCORD_WEBHOOK_URL from the environment.

If the script exits non-zero, log it but don't fail the run — the briefing is already on disk.

Done.

Constraints:
- Webhook URL is read from the environment at runtime, never inlined.
- The summarizer and todo-maker agents are restricted by their YAML tools lists — don't loosen them.
- Don't reintroduce fetcher/filterer/dedupe agents — briefer replaces them.
- Refuse loud, fail safe.
```

## Cost comparison (per run)

| Stage | Naive (5-agent design) | With briefer |
|---|---|---|
| Fetch ×4 sources | ~56K tokens (4 Haiku agents) | 0 tokens |
| Filter against interests | ~15K tokens (Haiku) | 0 tokens |
| Dedupe against history | ~15K tokens (Haiku) | 0 tokens |
| Summarize | ~25K tokens (Sonnet) | ~25K tokens (Sonnet) |
| Todo extraction | ~5K tokens (Haiku) | ~5K tokens (Haiku) |
| **Total** | **~115K tokens** (~$0.22) | **~30K tokens** (~$0.06) |

~74% reduction with the same final briefing.
