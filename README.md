# Ralph

![Ralph](ralph.webp)

Ralph is an autonomous AI agent that runs [Claude Code](https://docs.anthropic.com/en/docs/claude-code) in a loop until all user stories in a PRD are complete. Each iteration gets a fresh context window. Memory persists via version control history, `progress.md`, `prd.json`, and semantic vector retrieval.

Based on [Geoffrey Huntley's Ralph pattern](https://ghuntley.com/ralph/).

## Features

- **Per-story work state** — each story gets its own `.ralph/stories/{id}/` directory with `state.json`, `plan.md`, and `decisions.md`, persisted across iterations
- **PRD context injection** — the current story is injected in full + one-line summaries of other stories directly into the prompt (~200 tokens vs ~3K)
- **Crash-resilient checkpoints** — orchestration state saved to `.ralph/checkpoint.json` after every story event; on restart Ralph detects the checkpoint and offers to resume
- **Parallel execution** — DAG analysis determines story dependencies; independent stories run across N workers in isolated jj workspaces
- **Gemini judge** — an independent LLM reviews each story after Claude marks it complete, rejecting subpar implementations
- **Quality review gate** — five parallel "lens" reviewers (security, efficiency, DRY, error handling, testing) examine the full changeset after all stories pass
- **Semantic memory** — ChromaDB vector database with Voyage AI embeddings stores patterns, errors, decisions, and codebase signatures across runs
- **Confidence decay** — unconfirmed memories decay by 0.85x per run; confirmed memories get boosted
- **Real-time cost tracking** — token usage parsed from Claude and Gemini streaming output, aggregated per-story and per-run
- **TUI costs tab** — per-story cost breakdown, total run cost, token counts, cache hit rate
- **Run history** — `ralph history` shows recent runs with date, stories, cost, and duration
- **Push notifications** — ntfy.sh notifications on story complete/fail/stuck and run done; zero accounts needed
- **Remote status page** — mobile-friendly HTTP status page with SSE live updates; JSON API at `/api/status`
- **Stuck detection** — detects tool-call loops, cancels the process, and inserts a targeted fix story
- **Automatic archiving** — previous runs archived to `.ralph/archive/` when you start a new feature

## Prerequisites

- **Go 1.25+** (for building from source)
- **[Claude Code](https://docs.anthropic.com/en/docs/claude-code)** installed and authenticated (`npm install -g @anthropic-ai/claude-code`)
- **[jj (Jujutsu)](https://martinvonz.github.io/jj/)** for version control (Ralph uses jj, not git)
- (Optional) **[Gemini CLI](https://github.com/google-gemini/gemini-cli)** for `--judge` mode

## Building

```bash
make build
```

This produces `build/ralph` with the current git version baked in. Add it to your PATH or create an alias:

```bash
alias ralph='/path/to/ralph/build/ralph'
```

## Quick Start

```bash
# 1. Create a plan using Claude Code's built-in /plan command
#    (this saves to .claude/plans/)

# 2. Generate prd.json from the plan and execute
ralph --plan .claude/plans/my-plan.md

# Or if you already have a prd.json:
ralph
```

## Workflow

### Planning: `--plan`

The recommended flow uses Claude Code's built-in `/plan` command to create a plan, then Ralph converts it to `prd.json` and executes.

```bash
ralph --plan .claude/plans/my-plan.md
```

This launches the TUI in a planning phase:

1. Claude reads the plan file and explores the codebase
2. Generates `prd.json` in the project root (you can watch progress in the TUI)
3. **Pauses for review** -- the TUI shows "Review prd.json -- press Enter to execute"
4. Open `prd.json` in another terminal, review and edit if needed
5. Press `Enter` to start execution, or `q` to quit

You can also generate `prd.json` manually using the `/ralph` skill in Claude Code, or write it by hand.

### Execution

Once `prd.json` exists, Ralph loops:

1. Picks the highest-priority story where `passes: false`
2. Spawns a fresh Claude Code instance to implement that story
3. Claude runs quality checks (typecheck, tests, etc.)
4. If checks pass, commits with jj and marks the story `passes: true`
5. Appends learnings to `progress.md`
6. Repeats until all stories pass or max iterations reached

### Parallel Execution: `--workers`

When stories are independent, Ralph can run them in parallel:

```bash
ralph --workers 3
```

This adds a DAG analysis step where Claude examines the codebase to determine which stories depend on each other, then schedules independent stories across N workers. Each worker runs in an isolated jj workspace.

Use `1-9` keys in the TUI to switch between worker output panels.

### Judge Mode (enabled by default)

An independent LLM (Gemini) reviews each story after Claude marks it complete. Disable with `--no-judge`.

```bash
ralph --no-judge                    # disable judge
ralph --judge-max-rejections 3      # allow up to 3 rejections
```

1. Claude implements a story and sets `passes: true`
2. Gemini reviews the diff against acceptance criteria
3. If rejected, `passes` resets to `false` and feedback is written for the next iteration
4. After N rejections (default: 2), the story is auto-passed with `[HUMAN REVIEW NEEDED]`

The judge is advisory -- if Gemini crashes or times out, Ralph treats it as a pass and continues.

### Quality Review (enabled by default)

After all stories pass, a final quality gate runs multiple independent Claude Code reviewers, each focused on a single concern. Disable with `--no-quality-review`.

```bash
ralph --no-quality-review                              # disable quality review
ralph --quality-workers 5 --quality-max-iterations 3   # tune parameters
```

1. Five "lens" reviewers run in parallel, each examining the full changeset:
   - **Security** — injection, auth, secrets, OWASP top 10
   - **Efficiency** — unnecessary allocations, N+1 queries, algorithmic issues
   - **DRY-ness** — duplicated logic, reimplemented existing utilities (searches the full codebase)
   - **Error Handling** — unchecked errors, nil dereference, edge cases, race conditions
   - **Testing** — untested code paths, missing edge case tests
2. Findings are merged into an assessment (`.ralph/quality/assessment-N.json`)
3. A Claude Code instance reads the assessment and fixes issues (critical first)
4. Re-review — the lenses run again to verify fixes and catch new issues
5. After max iterations (default: 2), the TUI prompts: **Enter** to continue fixing, **q** to finish

Each reviewer is an interactive Claude Code agent — it doesn't just read a diff paste, it explores files on demand using Read/Grep/Glob. This means it scales to any changeset size and the DRY reviewer can search the existing codebase for patterns the new code should reuse.

## CLI Reference

```
Usage: ralph [options] [max_iterations]

Options:
  --dir <path>                    Project directory (default: current directory)
  --plan <path>                   Generate prd.json from a plan file, review, then execute
  --workers <n>                   Parallel workers (default: 1 = serial)
  --no-judge                      Disable Gemini judge verification (enabled by default)
  --judge-max-rejections <n>      Max rejections before auto-pass (default: 2)
  --workspace-base <path>         Base directory for worker workspaces (default: /tmp/ralph-workspaces)
  --no-quality-review             Disable final quality review (enabled by default)
  --quality-workers <n>           Parallel quality reviewers (default: 3)
  --quality-max-iterations <n>    Max review-fix cycles (default: 2)
  --notify <topic>                Send push notifications via ntfy.sh to given topic
  --ntfy-server <url>             Self-hosted ntfy server URL (default: https://ntfy.sh)
  --status-port <port>            Start remote status page on given port (disabled by default)
  --enable-monitoring             Enable ntfy + status page using .ralph/.env config
  --memory-max-tokens <n>         Max tokens for injected memory context (default: 2000)
  --memory-top-k <n>              Results per memory collection (default: 5)
  --memory-min-score <float>      Memory similarity threshold (default: 0.7)
  --memory-disable                Skip ChromaDB startup
  --memory-port <port>            ChromaDB sidecar port (default: 9876)
  --idle                          Launch TUI without executing (display only)
  --help, -h                      Show help

Subcommands:
  ralph history                   Show recent run summaries (last 10)
  ralph history --all             Show all run history
  ralph memory stats              Show memory collection statistics
  ralph memory search <query>     Test semantic retrieval
  ralph memory prune              Force memory confidence decay
  ralph memory reset              Clear all memory collections

Arguments:
  max_iterations                  Max loop iterations (default: 1.5x story count)

Examples:
  ralph                                         Run all stories serially
  ralph 5                                       Run with max 5 iterations
  ralph --plan .claude/plans/my-plan.md         Plan, review, then execute
  ralph --workers 3                             Run up to 3 stories in parallel
  ralph --no-judge                               Run without Gemini judge
  ralph --no-quality-review                     Run without final quality gate
  ralph --plan plan.md --workers 2              Full pipeline
  ralph --enable-monitoring                     Use .ralph/.env for ntfy + status page
```

## TUI Keybindings

| Key | Action |
|-----|--------|
| `q` | Quit (press twice during execution) |
| `Ctrl+C` | Force quit (cancels all workers) |
| `Tab` | Switch active panel |
| `j/k` | Scroll active panel |
| `PgUp/PgDn` | Page scroll |
| `1-9` | Switch worker view (parallel mode) |
| `Enter` | Start execution (review phase only) |

## Configuration

### Monitoring Setup (`.ralph/.env`)

Ralph supports push notifications (ntfy.sh) and a remote status page. Configure once in `.ralph/.env`, then use `--enable-monitoring` to activate both:

**One-time setup:**

```bash
mkdir -p .ralph
cat > .ralph/.env << 'EOF'
RALPH_NOTIFY_TOPIC=ralph-yourname-a8f3
RALPH_STATUS_PORT=8080
# RALPH_NTFY_SERVER=https://ntfy.my-server.ts.net  # optional, defaults to https://ntfy.sh
EOF
```

Install the ntfy app on your phone ([iOS](https://apps.apple.com/app/ntfy/id1625396347) / [Android](https://play.google.com/store/apps/details?id=io.heckel.ntfy)) and subscribe to the same topic.

**Then just run:**

```bash
ralph --enable-monitoring
```

Ralph prints the active monitoring config at startup as a reminder:

```
Monitoring:
  Notifications: https://ntfy.sh/ralph-yourname-a8f3
  Status page:   http://localhost:8080
```

You can also set these values as OS environment variables (`RALPH_NOTIFY_TOPIC`, `RALPH_NTFY_SERVER`, `RALPH_STATUS_PORT`) or use the explicit flags (`--notify`, `--ntfy-server`, `--status-port`) which always take priority.

### Remote Status Page + Tailscale

Combined with [Tailscale](https://tailscale.com), the status page is accessible from your phone without port forwarding:

1. Install [Tailscale](https://tailscale.com/download) on your laptop and phone
2. Find your laptop's Tailscale IP: `tailscale ip -4` (e.g., `100.64.1.42`)
3. Open `http://100.64.1.42:8080` on your phone

The status page shows: PRD name, current phase, run duration, story list with status/cost, and total run cost — all updating in real-time via SSE. JSON API available at `/api/status`.

### Semantic Memory (ChromaDB)

Ralph uses a ChromaDB vector database for semantic memory across runs. It requires a Python environment with `chromadb` installed.

**Setup:**

1. Install conda (or ensure pip is available)
2. Ralph will automatically create a conda environment and install chromadb on first run
3. Memory data persists in `.ralph/memory/chroma/`

To use Voyage AI embeddings (recommended), set your API key:

```bash
export VOYAGE_API_KEY=your-key    # or uses ANTHROPIC_API_KEY as fallback
```

To disable semantic memory entirely:

```bash
ralph --memory-disable
```

### Full Remote Monitoring Stack

For the complete phone monitoring experience:

```bash
# On your laptop (connected to Tailscale)
ralph --plan .claude/plans/my-feature.md --workers 3 --enable-monitoring
```

Then on your phone:
- **Status page**: `http://<tailscale-ip>:8080` for live progress
- **ntfy app**: push notifications for key events
- **ralph history**: check past runs when you're back at your laptop

## prd.json Format

```json
{
  "project": "MyApp",
  "branchName": "ralph/feature-name",
  "description": "Short description of the work",
  "userStories": [
    {
      "id": "AB-001",
      "title": "Add status field to tasks table",
      "description": "As a developer, I need to store task status.",
      "acceptanceCriteria": [
        "Add status column with default 'pending'",
        "Migration runs successfully",
        "Typecheck passes"
      ],
      "priority": 1,
      "passes": false,
      "notes": ""
    }
  ]
}
```

### Story Sizing

Each story must be completable in **one context window**. If Claude runs out of context mid-story, it produces broken code.

**Right-sized:** add a DB column, add a UI component, update server logic, add a filter
**Too big (split these):** "build the entire dashboard", "add authentication", "refactor the API"

### Story Ordering

Stories execute in priority order. Earlier stories must not depend on later ones:

1. Schema/database changes
2. Backend/API logic
3. UI components
4. Dashboards/aggregation views

## How It Works

### Memory Between Iterations

Each iteration is a fresh Claude Code instance. Memory persists via:

- **jj history** -- commits from previous iterations
- **`progress.md`** -- learnings, patterns, and context
- **`prd.json`** -- which stories are done (injected directly into prompt)
- **`CLAUDE.md`** -- Ralph updates these with discovered patterns
- **Story state** -- structured state.json, plan.md, decisions.md per story
- **Semantic memory** -- ChromaDB vector retrieval of relevant patterns, errors, and decisions from past runs

### Stuck Detection

If Claude gets stuck in a loop (repeatedly running the same command or editing the same file), Ralph:

1. Detects the pattern via tool call analysis
2. Cancels the current Claude process
3. Generates a targeted "fix story" and inserts it before the stuck story
4. Continues with the fix story in the next iteration

### Archiving

When you start a new feature (different `branchName` in prd.json), Ralph automatically archives the previous run's `prd.json` and `progress.md` to `.ralph/archive/YYYY-MM-DD-feature-name/`.

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
  judge/            Gemini judge integration
  archive/          Run archiving (previous prd.json + progress.md)
  autofix/          Stuck loop detection and fix story generation
  quality/          Final quality review gate (multi-lens reviewers)
  events/           Event log (events.jsonl)
  exec/             Shell command helpers (jj wrappers)
  storystate/       Per-story state persistence (state.json, plan.md, decisions.md)
  checkpoint/       Orchestration checkpoint for crash recovery and resume
  memory/           ChromaDB sidecar, embedding pipeline, semantic retrieval
  costs/            Token usage tracking, pricing, run history
  notify/           Push notifications via ntfy.sh
  statuspage/       Remote HTTP status page with SSE live updates
ralph-prompt.md     Prompt template for Claude Code iterations
judge-prompt.md     Review template for Gemini judge
skills/ralph/       Claude Code skill for converting plans to prd.json
```

## Key Files (In Your Project)

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
| `.ralph/memory/` | ChromaDB vector database storage |
| `.ralph/run-history.json` | Accumulated run summaries with cost data |

## References

- [Geoffrey Huntley's Ralph article](https://ghuntley.com/ralph/)
- [Claude Code documentation](https://docs.anthropic.com/en/docs/claude-code)
- [Jujutsu (jj) documentation](https://martinvonz.github.io/jj/)
