# Ralph

[![CI](https://github.com/ohjann/ralph/actions/workflows/ci.yml/badge.svg)](https://github.com/ohjann/ralph/actions/workflows/ci.yml)

![Ralph](ralph.webp)

Ralph is an autonomous AI agent that runs [Claude Code](https://docs.anthropic.com/en/docs/claude-code) in a loop until all user stories in a PRD are complete. Each iteration gets a fresh context window. Memory persists via version control history, `progress.md`, `prd.json`, and markdown-based cross-run memory.

Based on [Geoffrey Huntley's Ralph pattern](https://ghuntley.com/ralph/).

## Why Ralph?

Claude Code is excellent at implementing a single, well-scoped task. But a real feature is 5–15 tasks with ordering constraints, and Claude's context window resets between sessions. Ralph solves this: you describe the work as user stories, and Ralph handles sequencing, memory, verification, and retry — so you can walk away and come back to a working feature.

## How It Works

```
prd.json (stories) ──► Ralph picks highest-priority incomplete story
                            │
                            ▼
                       Spawns fresh Claude Code instance
                            │
                            ▼
                       Claude implements + runs checks
                            │
                            ▼
                       Commits with jj, marks story done
                            │
                            ▼
                       Repeats until all stories pass
```

Memory between iterations comes from jj history, `progress.md`, `prd.json` status, per-story state files, and markdown memory in `.ralph/memory/`.

## Prerequisites

- **Go 1.25+** (for building from source)
- **[Claude Code](https://docs.anthropic.com/en/docs/claude-code)** installed and authenticated (`npm install -g @anthropic-ai/claude-code`)
- **[jj (Jujutsu)](https://martinvonz.github.io/jj/)** for version control — Ralph uses jj, not git. This is a deliberate choice for its workspace isolation and mutable commit model, but it means you'll need jj installed and initialized in your project (`jj git init --colocate` in an existing git repo)
- (Optional) **[Gemini CLI](https://github.com/google-gemini/gemini-cli)** for judge mode

No external ML/vector infrastructure required — memory runs on plain markdown files.

## Quick Start

```bash
# Build
make build

# Option 1: Plan-driven (recommended)
# Create a plan using Claude Code's /plan command, then:
ralph --plan .claude/plans/my-plan.md

# Option 2: Existing prd.json
ralph

# Option 3: Interactive mode (no prd.json needed)
# Just run ralph without a prd.json — it auto-detects and presents an input bar
ralph
```

## Key Features

- **Parallel execution** — DAG analysis determines story dependencies; independent stories run across N workers in isolated jj workspaces (`--workers 3` or `--workers auto`)
- **Gemini judge** — an independent LLM reviews each story after Claude marks it complete, rejecting subpar implementations (enabled by default, `--no-judge` to disable)
- **Fusion mode** — complex stories automatically spawn competing implementations in parallel; the judge selects the best passing result
- **Quality review gate** — parallel "lens" reviewers (security, efficiency, DRY, error handling, testing) examine the full changeset after all stories pass
- **Stuck detection + hint injection** — detects tool-call loops, notifies you, and lets you inject a hint for the next iteration
- **Per-story simplification pass** — fast code quality review between implementation and judge verification
- **Markdown memory** — cross-run learnings stored in `.ralph/memory/`, injected into worker prompts with periodic dream consolidation
- **Interactive task mode** — submit ad-hoc tasks through a TUI input bar without needing a prd.json
- **Multi-model orchestration** — Opus for architect/debugger, Sonnet for implementer/reviewer, Haiku for utility tasks; configurable per role
- **Crash-resilient checkpoints** — orchestration state saved after every story event; resume on restart

## Documentation

| Document | Contents |
|----------|----------|
| [Workflow & Modes](docs/workflow.md) | Planning, execution, parallel workers, judge, fusion, simplification, quality review, interactive mode |
| [Configuration](docs/configuration.md) | CLI reference, TUI keybindings, monitoring setup (ntfy.sh, status page, Tailscale) |
| [PRD Format](docs/prd-format.md) | `prd.json` schema, field reference, story sizing and ordering guidance |
| [Architecture](docs/architecture.md) | Project structure, memory system, workspace lifecycle, key files |

## Contributing

Ralph is a personal project — it works for my workflow but comes with no guarantees. It uses [Jujutsu (jj)](https://martinvonz.github.io/jj/) for version control, not git, which is a deliberate choice but limits portability.

If you find Ralph useful, **fork it and make it your own**. Bug reports, PRs, and feature requests are welcome but may not be accepted if they don't align with the project's direction. No hard feelings either way.

## Acknowledgements

This project started as a fork of [snarktank/ralph](https://github.com/snarktank/ralph) — thanks to Ryan Carson for the initial foundations. The core concept comes from [Geoffrey Huntley's Ralph pattern](https://ghuntley.com/ralph/).

## References

- [Geoffrey Huntley's Ralph article](https://ghuntley.com/ralph/)
- [Claude Code documentation](https://docs.anthropic.com/en/docs/claude-code)
- [Jujutsu (jj) documentation](https://martinvonz.github.io/jj/)
