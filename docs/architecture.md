# Architecture

## Project Structure

```
cmd/ralph/          Entry point
internal/
  config/           CLI flag parsing and configuration
  tui/              Bubbletea TUI (model, views, commands, styles)
  runner/           Claude Code CLI integration and output streaming
  prd/              prd.json loading, saving, story management
  coordinator/      Parallel worker scheduling and state sync
  worker/           Worker goroutine lifecycle
  dag/              Dependency analysis via Claude CLI
  workspace/        jj workspace create/destroy/merge
  fusion/           Competing implementations for complex stories
  roles/            Role-based model configuration
  judge/            Gemini judge integration
  archive/          Run archiving (previous prd.json + progress.md)
  autofix/          Stuck loop detection and fix story generation
  quality/          Final quality review gate (multi-lens reviewers)
  events/           Event log (events.jsonl)
  exec/             Shell command helpers (jj wrappers)
  storystate/       Per-story state persistence (state.json, plan.md, decisions.md)
  checkpoint/       Orchestration checkpoint for crash recovery and resume
  interactive/      Dynamic story creation and session persistence for interactive tasks
  memory/           Markdown-based cross-run memory, dream consolidation, size monitoring
  costs/            Token usage tracking, pricing, run history
  notify/           Push notifications via ntfy.sh
  statuspage/       Remote HTTP status page with SSE live updates
ralph-prompt.md     Prompt template for Claude Code iterations
judge-prompt.md     Review template for Gemini judge
skills/ralph/       Claude Code skill for converting plans to prd.json
```

## Memory Between Iterations

Each iteration is a fresh Claude Code instance. Memory persists via:

- **jj history** -- commits from previous iterations
- **`progress.md`** -- learnings, patterns, and context
- **`prd.json`** -- which stories are done (injected directly into prompt)
- **`CLAUDE.md`** -- Ralph updates these with discovered patterns
- **Story state** -- structured state.json, plan.md, decisions.md per story
- **Markdown memory** -- cross-run learnings and PRD lessons in `.ralph/memory/`, injected into prompts with dream consolidation for maintenance

### Dream Consolidation

A periodic consolidation cycle (every 5 runs by default, or manually via `ralph memory consolidate`) merges duplicates, drops stale entries, and keeps memory files lean. Inspired by Claude Code's Auto Dream.

To disable memory injection entirely:

```bash
ralph --memory-disable
```

## Workspace Lifecycle

### Setup & Teardown

When using parallel workers (`--workers`), each worker runs in an isolated jj workspace. If your project needs custom initialization (e.g. symlinking `node_modules`, copying `.env` files, starting a dev server), you can add scripts to your project's `.ralph/` directory:

- **`.ralph/workspace-setup.sh`** — runs after workspace creation and state copy
- **`.ralph/workspace-teardown.sh`** — runs before workspace destruction (best-effort, errors ignored)

Both scripts receive the `WORKSPACE_DIR` environment variable pointing to the worker's workspace root. The setup script also receives `RALPH_WORKSPACE=1`.

**Example** (Node.js monorepo):

```bash
#!/bin/bash
# .ralph/workspace-setup.sh
set -e
WORKSPACE_DIR="${WORKSPACE_DIR:-$(pwd)}"
MAIN_DIR="$(jj workspace root --name default 2>/dev/null || jj root)"

# Symlink node_modules (instant, shared across workers)
if [ -d "$MAIN_DIR/node_modules" ] && [ ! -e "$WORKSPACE_DIR/node_modules" ]; then
    ln -s "$MAIN_DIR/node_modules" "$WORKSPACE_DIR/node_modules"
fi

# Copy .env files
if [ -f "$MAIN_DIR/.env" ] && [ ! -f "$WORKSPACE_DIR/.env" ]; then
    cp "$MAIN_DIR/.env" "$WORKSPACE_DIR/.env"
fi
```

```bash
#!/bin/bash
# .ralph/workspace-teardown.sh
# Kill any background processes started by setup
WORKSPACE_DIR="${WORKSPACE_DIR:-$(pwd)}"
PID_FILE="$WORKSPACE_DIR/.ralph/dev-server.pid"
if [ -f "$PID_FILE" ]; then
    kill "$(cat "$PID_FILE")" 2>/dev/null || true
    rm -f "$PID_FILE"
fi
```

If no scripts exist, Ralph skips this step silently — they're entirely optional.

### Archiving

When you start a new feature (different `branchName` in prd.json), Ralph automatically archives the previous run's `prd.json` and `progress.md` to `.ralph/archive/YYYY-MM-DD-feature-name/`.

## Key Files

Ralph creates and manages these files in the project directory:

| File | Purpose |
|------|---------|
| `prd.json` | User stories with `passes` status -- the task list |
| `progress.md` | Append-only learnings for future iterations |
| `.ralph/` | Logs, events, judge feedback, stuck detection |
| `.ralph/logs/` | Claude output logs per iteration |
| `.ralph/archive/` | Archived runs from previous features |
| `.ralph/quality/` | Quality review assessments per iteration |
| `.ralph/stories/` | Per-story state (state.json, plan.md, decisions.md) |
| `.ralph/checkpoint.json` | Orchestration checkpoint for resume |
| `.ralph/session-*.json` | Saved interactive task sessions |
| `.ralph/memory/` | Cross-run learnings and PRD lessons (markdown files) |
| `.ralph/run-history.json` | Accumulated run summaries with cost data |
| `.ralph/workspace-setup.sh` | (Optional) Custom worker workspace initialization |
| `.ralph/workspace-teardown.sh` | (Optional) Custom worker workspace cleanup |
