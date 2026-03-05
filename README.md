# Ralph

![Ralph](ralph.webp)

Ralph is an autonomous AI agent that runs [Claude Code](https://docs.anthropic.com/en/docs/claude-code) in a loop until all user stories in a PRD are complete. Each iteration gets a fresh context window. Memory persists via version control history, `progress.txt`, and `prd.json`.

Based on [Geoffrey Huntley's Ralph pattern](https://ghuntley.com/ralph/).

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
5. Appends learnings to `progress.txt`
6. Repeats until all stories pass or max iterations reached

### Parallel Execution: `--workers`

When stories are independent, Ralph can run them in parallel:

```bash
ralph --workers 3
```

This adds a DAG analysis step where Claude examines the codebase to determine which stories depend on each other, then schedules independent stories across N workers. Each worker runs in an isolated jj workspace.

Use `1-9` keys in the TUI to switch between worker output panels.

### Judge Mode: `--judge`

An independent LLM (Gemini) reviews each story after Claude marks it complete:

```bash
ralph --judge
ralph --judge --judge-max-rejections 3
```

1. Claude implements a story and sets `passes: true`
2. Gemini reviews the diff against acceptance criteria
3. If rejected, `passes` resets to `false` and feedback is written for the next iteration
4. After N rejections (default: 2), the story is auto-passed with `[HUMAN REVIEW NEEDED]`

The judge is advisory -- if Gemini crashes or times out, Ralph treats it as a pass and continues.

### Quality Review: `--quality-review`

After all stories pass, an optional final quality gate runs multiple independent Claude Code reviewers, each focused on a single concern:

```bash
ralph --quality-review
ralph --quality-review --quality-workers 5 --quality-max-iterations 3
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
  --judge                         Enable Gemini judge verification
  --judge-max-rejections <n>      Max rejections before auto-pass (default: 2)
  --workspace-base <path>         Base directory for worker workspaces (default: /tmp/ralph-workspaces)
  --quality-review                Enable final quality review after all stories pass
  --quality-workers <n>           Parallel quality reviewers (default: 3)
  --quality-max-iterations <n>    Max review-fix cycles (default: 2)
  --idle                          Launch TUI without executing (display only)
  --help, -h                      Show help

Arguments:
  max_iterations                  Max loop iterations (default: 1.5x story count)

Examples:
  ralph                                         Run all stories serially
  ralph 5                                       Run with max 5 iterations
  ralph --plan .claude/plans/my-plan.md         Plan, review, then execute
  ralph --workers 3                             Run up to 3 stories in parallel
  ralph --judge                                 Run with Gemini judge verification
  ralph --quality-review                        Run with final quality gate
  ralph --plan plan.md --workers 2 --judge      Full pipeline
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
  archive/          Run archiving (previous prd.json + progress.txt)
  autofix/          Stuck loop detection and fix story generation
  quality/          Final quality review gate (multi-lens reviewers)
  events/           Event log (events.jsonl)
  exec/             Shell command helpers (jj wrappers)
ralph-prompt.md     Prompt template for Claude Code iterations
judge-prompt.md     Review template for Gemini judge
skills/ralph/       Claude Code skill for converting plans to prd.json
```

## Key Files (In Your Project)

Ralph creates and manages these files in the project directory:

| File | Purpose |
|------|---------|
| `prd.json` | User stories with `passes` status -- the task list |
| `progress.txt` | Append-only learnings for future iterations |
| `.ralph/` | Logs, events, judge feedback, stuck detection |
| `.ralph/logs/` | Claude output logs per iteration |
| `.ralph/archive/` | Archived runs from previous features |
| `.ralph/quality/` | Quality review assessments per iteration |

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

Each iteration is a fresh Claude Code instance. The only memory is:

- **jj history** -- commits from previous iterations
- **`progress.txt`** -- learnings, patterns, and context
- **`prd.json`** -- which stories are done
- **`CLAUDE.md`** -- Ralph updates these with discovered patterns

### Stuck Detection

If Claude gets stuck in a loop (repeatedly running the same command or editing the same file), Ralph:

1. Detects the pattern via tool call analysis
2. Cancels the current Claude process
3. Generates a targeted "fix story" and inserts it before the stuck story
4. Continues with the fix story in the next iteration

### Archiving

When you start a new feature (different `branchName` in prd.json), Ralph automatically archives the previous run's `prd.json` and `progress.txt` to `.ralph/archive/YYYY-MM-DD-feature-name/`.

## References

- [Geoffrey Huntley's Ralph article](https://ghuntley.com/ralph/)
- [Claude Code documentation](https://docs.anthropic.com/en/docs/claude-code)
- [Jujutsu (jj) documentation](https://martinvonz.github.io/jj/)
