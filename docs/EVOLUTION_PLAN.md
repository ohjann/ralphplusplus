# Ralph v2 Evolution Plan: Phased Rollout

> Architectural proposal for evolving Ralph from a capable agent loop into a
> best-in-class autonomous coding framework. Codename: **Ralph v2**. The binary,
> directories, and CLI remain `ralph` — v2 marks the capability leap, not a
> rewrite.
>
> Each phase is designed to be self-contained and implementable from a fresh
> Claude context using this document as input.

## Standing Requirements (Apply to Every Phase)

> **Documentation:** Update or create relevant docs for any new CLI flags,
> subcommands, configuration options, or architectural changes introduced in
> the phase. Keep CLAUDE.md and any user-facing help text in sync.
>
> **Testing:** Write unit tests for new packages and integration tests for
> new workflows. Aim for meaningful coverage of core logic — not busywork
> tests, but tests that catch real regressions. If an existing e2e test
> (`testdata/run-test.sh`) is affected by changes, update it.
>
> **Both should be included in the phase's build plan, not treated as an
> afterthought.**

## Current State (Baseline)

Ralph is a BubbleTea TUI app (Go) that orchestrates Claude Code instances to
implement user stories from a PRD. Key existing capabilities:

- **Serial & parallel execution** with DAG-based dependency analysis
- **Jujutsu (jj) version control** with workspace isolation per worker
- **Judge system** (Gemini) for independent story verification
- **Quality review** with 5 parallel lens reviewers (security, efficiency, DRY, error handling, testing)
- **Stuck detection** with auto-fix story generation
- **Memory**: `progress.md` (append-only text log) and `events.jsonl` (structured event log)
- **Context injection**: recent events formatted into next iteration's prompt
- **Archive system**: preserves history across branch/feature changes

### Key Limitations to Address

1. ~~Memory is flat — no semantic retrieval, all recent context dumped into prompt~~ → **Addressed in Phase 2** (semantic memory with vector DB)
2. ~~Context exhaustion recovery is lossy — relies on text markers in progress.md~~ → **Addressed in Phase 1** (structured per-story state)
3. ~~No structured per-story work state — agent must reconstruct intent from history~~ → **Addressed in Phase 1** (storystate package)
4. No cost visibility — token usage is logged but not surfaced
5. All Claude invocations use the same generalist prompt
6. ~~No crash recovery — killing ralph mid-run loses orchestration state~~ → **Partially addressed in Phase 1** (checkpoint package — detection and prompt work, but resume logic is a stub)
7. ~~No cross-run learning — each PRD run starts from zero institutional knowledge~~ → **Addressed in Phase 2** (semantic memory with vector DB)

---

## Phase 1: Per-Story Work State & Checkpoint/Resume ✅ COMPLETE

**Impact: High | Complexity: Low | Dependencies: None**

> **Status: Implemented** (completed 2026-03-11 on branch `ralph/phase1-state-checkpoint`).
> Phase 1 has been fully implemented. Key deliverables:
> - `internal/storystate/` package — per-story state files (state.json, plan.md, decisions.md) with full CRUD
> - `internal/checkpoint/` package — orchestration checkpoint persistence (.ralph/checkpoint.json)
> - `BuildPrompt()` injects current story context and one-line summaries of other stories directly into the prompt
> - `ralph-prompt.md` updated to instruct agents to maintain story state and no longer read prd.json
> - Story state is copied to worker workspaces and synced back after completion
> - Checkpoint is written after story events and deleted on clean completion
>
> **P1-013 (resolved):** Checkpoint resume now fully restores state. Serial mode
> restores the iteration counter. Parallel mode reconstructs the DAG via
> `dag.FromCheckpoint()`, pre-seeds the coordinator with completed/failed stories
> via `coordinator.NewFromCheckpoint()`, and resumes scheduling from where it left off.

### Goal

Make agent sessions truly resumable by persisting structured work state per
story, and make ralph itself crash-resilient by checkpointing orchestration
state.

### Context for Builder

Ralph currently tracks story progress through two mechanisms:
- `progress.md`: human-readable append-only log with timestamps, files changed,
  and learnings. Located at the path from `config.ProgressFile`.
- `events.jsonl`: structured events (story_complete, stuck, judge_result, etc.)
  in `internal/events/events.go`.

When context exhaustion occurs (`[CONTEXT EXHAUSTED]` marker in progress.md),
the next iteration has to reconstruct what was happening from this text log.
This is lossy — subtask granularity, the agent's plan, and decision rationale
are lost.

### What to Build

#### 1a. Per-Story State Files

Create `internal/storystate/` package:

```
.ralph/stories/{story-id}/
├── state.json        # structured checkpoint
├── plan.md           # agent's implementation plan (written by agent)
└── decisions.md      # key decisions and rationale (written by agent)
```

`state.json` schema:
```json
{
  "story_id": "STORY-1",
  "status": "in_progress|blocked|context_exhausted|complete|failed",
  "iteration_count": 3,
  "files_touched": ["internal/foo/bar.go", "internal/foo/bar_test.go"],
  "subtasks": [
    {"description": "Create handler", "done": true},
    {"description": "Add tests", "done": false},
    {"description": "Wire up routes", "done": false}
  ],
  "errors_encountered": [
    {"error": "test timeout in TestFoo", "resolution": "increased deadline to 5s"}
  ],
  "judge_feedback": ["Missing error handling on line 42"],
  "last_updated": "2026-03-11T10:30:00Z"
}
```

**Integration points:**
- `internal/runner/runner.go` `BuildPrompt()`: inject the story's state.json
  and plan.md into the prompt when starting an iteration for that story
- `ralph-prompt.md`: add instructions for the agent to update
  `.ralph/stories/{id}/state.json` at the end of each iteration, and to write
  `plan.md` at the start of a story's first iteration
- `internal/workspace/workspace.go` `CopyState()`: include `.ralph/stories/`
  in the state copied to worker workspaces
- `internal/coordinator/coordinator.go` `MergeAndSync()`: sync story state
  back from workspace to main after completion

**Key design decision:** The agent itself writes these files (plan.md,
decisions.md, updates to state.json). Ralph reads them to inject context.
This keeps the agent in control of its own memory while ralph provides the
persistence infrastructure.

#### 1b. PRD Prompt Injection (Stop Claude Reading prd.json)

Currently, `ralph-prompt.md` instructs Claude to read prd.json from disk
every iteration. For a 46-story / 888-line prd.json, that's ~3K wasted
tokens per iteration on stories the agent will never touch. This doesn't
scale.

**Change:** `BuildPrompt()` should inject PRD context directly into the
prompt so Claude never opens prd.json itself:

1. Parse prd.json in `BuildPrompt()` (ralph already loads it in the TUI layer)
2. Inject the **current story in full** — title, description, acceptance
   criteria, notes
3. Inject a **one-line summary** of all other stories — just ID, title,
   and status (pass/fail). This gives the agent project awareness without
   the token cost.
4. Inject project-level context — project name, description, branch name

Example of what gets injected:
```
## YOUR STORY
ID: P1-003
Title: Inject story state into BuildPrompt
Description: As a developer, I want BuildPrompt() to inject...
Acceptance Criteria:
- Modify runner.BuildPrompt() to call storystate.Load()...
- ...
Notes: See internal/runner/runner.go BuildPrompt()...

## PROJECT CONTEXT
Project: Ralph | Branch: ralph/phase1-state-checkpoint
Progress: 2/13 stories complete

## OTHER STORIES (context only — do NOT work on these)
P1-001: Create storystate package with types and CRUD ✅
P1-002: Unit tests for storystate package ✅
P1-004: Update ralph-prompt.md for story state instructions (queued)
P1-005: Copy story state to worker workspaces (queued)
...
```

**Integration points:**
- `internal/runner/runner.go` `BuildPrompt()`: accept a `*prd.PRD` parameter
  (or the prd file path) and the story ID. Format the current story in full
  and the rest as a summary list.
- `ralph-prompt.md`: **remove** the instruction to read prd.json. The agent
  no longer needs to — all relevant PRD info is already in the prompt.
- This change keeps prompt size roughly constant regardless of PRD size
  (~200 tokens for a 50-story summary vs ~3K+ for the raw file).

#### 1c. Orchestration Checkpoint

Create `internal/checkpoint/` package:

```json
// .ralph/checkpoint.json
{
  "prd_hash": "sha256 of prd.json at start",
  "phase": "parallel",
  "completed_stories": ["STORY-1", "STORY-3"],
  "failed_stories": {"STORY-2": {"retries": 2, "last_error": "..."}},
  "in_progress": [],
  "dag": {"STORY-4": ["STORY-1"], "STORY-5": ["STORY-3"]},
  "iteration_count": 7,
  "timestamp": "2026-03-11T10:30:00Z"
}
```

**Integration points:**
- `internal/tui/model.go`: on startup (phaseInit), check for checkpoint.json.
  If found and prd_hash matches, offer resume via a new `phaseResumePrompt`.
- `internal/coordinator/coordinator.go`: write checkpoint after each story
  completion/failure.
- Serial mode (`phaseIterating`): write checkpoint after each iteration.
- On clean completion (`phaseDone`): delete checkpoint.json.

### Acceptance Criteria

- [x] Story state files are created and updated across iterations
- [x] Context exhaustion recovery uses structured state instead of text parsing
- [x] BuildPrompt() injects current story in full + one-line summaries of others
- [x] Claude no longer reads prd.json from disk (instruction removed from ralph-prompt.md)
- [x] Prompt size stays roughly constant regardless of PRD story count
- [x] Ralph can be killed mid-run and resumed from checkpoint
- [x] Parallel mode resumes with correct DAG state and completed story tracking
- [x] Story state is correctly synced across jj workspaces in parallel mode

### Estimated Scope

~600-800 lines of new Go code across 2 new packages + modifications to runner,
coordinator, workspace, and TUI.

---

## Phase 2: Semantic Memory with Vector Database ✅ COMPLETE

**Impact: Transformative | Complexity: Medium | Dependencies: Phase 1 (story state provides richer content to embed)**

> **Status: Complete** (completed 2026-03-12).
> Phase 2 has been implemented with the core pipeline working end-to-end. Key deliverables:
> - `internal/memory/` package — ChromaDB sidecar lifecycle, client, embedder (Voyage AI), pipeline, retrieval, hygiene, maintenance
> - ChromaDB starts/stops with ralph lifecycle; data persists to disk
> - All 5 collections created (patterns, completions, errors, decisions, codebase)
> - Story completions trigger embedding pipeline automatically (serial and parallel modes)
> - `BuildPrompt()` injects semantically retrieved context with token budget enforcement
> - Confidence decay runs at end of each PRD; collection caps enforced after each embed
> - `ralph memory` subcommands implemented (stats, search, prune, reset)
> - Codebase indexing via Go AST scanning on first run
> - Parallel workers receive semantic memory context (via Coordinator.SetMemory)
> - Pipeline uses DeduplicateInsertBatch with cosine similarity >0.9 dedup
> - ChromaDB data directory unified between TUI and CLI subcommands

### Goal

Replace flat context injection with semantic retrieval. The agent should get
precisely the memories most relevant to the current story, not a chronological
dump of recent events.

### Context for Builder

Currently, `internal/events/events.go` has `FormatContextSection()` which
formats recent events into a text block injected into the prompt via
`internal/runner/runner.go` `BuildPrompt()`. This includes patterns, story
completion summaries, and failure info — but it's recency-biased, not
relevance-biased.

Progress.md has a "Codebase Patterns" section at the top that gets
consolidated, but it grows unbounded and the agent has to read all of it.

### What to Build

#### 2a. ChromaDB Sidecar

Ralph manages a ChromaDB instance as a subprocess:
- Start on ralph launch, stop on ralph exit
- Persist data to `.ralph/memory/chroma/`
- Communicate via ChromaDB's HTTP API (localhost)
- Create `internal/memory/` package with client wrapper

Collections:
```
ralph_patterns      — codebase patterns and conventions
ralph_completions   — story completion summaries with context
ralph_errors        — errors encountered and their resolutions
ralph_decisions     — architectural decisions and rationale
ralph_codebase      — module/file summaries and signatures
```

#### 2b. Embedding Pipeline

On story completion (or context exhaustion):
- Extract structured data from story state (Phase 1), progress.md entry,
  and events
- Chunk into semantic units (one pattern = one document, one error = one
  document, etc.)
- **Deduplicate before insert**: query for existing documents with cosine
  similarity > 0.9. If a near-duplicate exists, update it (refresh timestamp,
  merge content) rather than inserting. This prevents accumulation of 15
  documents all saying "the config module uses viper."
- Embed and upsert into appropriate collection
- Metadata: story_id, timestamp, files_involved, tags, last_confirmed,
  relevance_score (initial: 1.0)

On PRD start (or periodically):
- Scan codebase for key files (go files, configs, READMEs)
- Generate summaries via a fast model (Haiku-class) or extract signatures
- Embed into `ralph_codebase` collection
- This gives the agent a semantic map of the project

#### 2c. Retrieval Integration

Modify `internal/runner/runner.go` `BuildPrompt()`:
- Take the current story's title, description, and acceptance criteria
- Query each collection with this as the search text (top-k per collection)
- Format results into a "Relevant Context" section in the prompt
- Replace the current flat `FormatContextSection()` approach
- Include relevance scores so the agent knows confidence levels

**Tuning parameters** (configurable in ralph CLI flags):
- `--memory-top-k` (default: 5 per collection)
- `--memory-min-score` (default: 0.7 similarity threshold)
- `--memory-max-tokens` (default: 2000) — hard cap on total injected context
  tokens regardless of result count. Rank by relevance_score × recency,
  take top N that fit within budget. The vector DB can hold thousands of
  documents — what matters is only injecting 5-8 of them.
- `--memory-disable` (bypass for debugging)

#### 2d. Memory Hygiene & Anti-Bloat

The vector DB must behave like a **working memory with active curation**, not
an append-only archive. Without these guards, it degrades the same way
progress.md does — just with fancier technology.

**Deduplication on write (non-negotiable from day one):**
- Before inserting, query for cosine similarity > 0.9 matches
- If found, update existing document (refresh timestamp, merge content)
- Prevents redundant accumulation

**Collection caps (implement from the start):**
- `ralph_patterns`: max 100 documents
- `ralph_completions`: max 200 documents
- `ralph_errors`: max 100 documents
- `ralph_decisions`: max 100 documents
- `ralph_codebase`: max 300 documents
- When cap is hit, evict documents with lowest `relevance_score`

**Confidence decay (post-run maintenance):**
- Every document has `relevance_score` (float, initial: 1.0) and
  `last_confirmed` (timestamp)
- When a memory is retrieved and the story succeeds, bump `relevance_score`
  by 0.1 (capped at 2.0) and update `last_confirmed`
- At the end of each PRD run, decay all documents not confirmed in the
  current run: `relevance_score *= 0.85`
- Documents that drop below 0.3 are evicted
- This means a memory that's never retrieved again is gone after ~6 runs

**Codebase-aware invalidation (deferred to Phase 8):**
- When the knowledge graph (Phase 8) detects significant file changes,
  flag all memories referencing those files for re-validation
- A fast model checks "is this memory still accurate given current code?"
- Until Phase 8, this is handled implicitly by confidence decay

#### 2e. Cross-Run Persistence

The ChromaDB data persists in `.ralph/memory/chroma/` across PRD runs.
When the archive system detects a branch change, memory is NOT archived —
it accumulates across features. This is the key differentiator: patterns
learned on feature-A inform feature-B.

Add a `ralph memory` subcommand for management:
- `ralph memory stats` — collection sizes, total documents, avg relevance scores
- `ralph memory search "query"` — test retrieval manually
- `ralph memory prune` — force a decay cycle and evict low-scoring documents
- `ralph memory reset` — nuclear option, clear all collections

### Technical Decisions

**Why ChromaDB over SQLite-vec:**
- Built-in collection management and metadata filtering
- More mature semantic search with multiple distance metrics
- The Python dependency is acceptable for this project

**API embeddings over local sentence-transformers:**
- Use an API embedding service (OpenAI `text-embedding-3-small`, Voyage, or
  similar) instead of local sentence-transformers
- Fewer moving parts: no local model downloads, no MLX/CUDA concerns, no
  version pinning for ML dependencies — just one HTTP request
- Embeddings are not on a hot path (a few documents on story completion,
  codebase indexing once per run) — 200ms API latency is invisible
- Cost is negligible: embedding API pricing is orders of magnitude cheaper
  than LLM calls. An entire PRD run's embeddings cost less than one Claude
  iteration.
- Configure ChromaDB to use a custom embedding function that calls the API
  instead of the default local model
- Configurable via `--embedding-provider` flag (default: openai) with API
  key from environment variable (e.g., `OPENAI_API_KEY`)

**ChromaDB setup:**
- Options are to use `pip install chromadb` or bundle in a virtualenv at `.ralph/memory/venv/` but we want to use `conda` here for virtual env
- Ralph auto-creates the venv on first run if not present
- `chroma run --path .ralph/memory/chroma/ --port 9876` as managed subprocess

### Acceptance Criteria

- [x] ChromaDB sidecar starts/stops cleanly with ralph lifecycle
- [x] Story completions, patterns, errors, and decisions are embedded automatically
- [x] Prompt injection uses semantic retrieval instead of recency-based context
- [x] Retrieval budget hard-caps injected context to `--memory-max-tokens`
- [x] Deduplication prevents near-duplicate documents (>0.9 similarity)
- [x] Collection caps are enforced, with lowest-scored documents evicted
- [x] Confidence decay runs at end of each PRD, evicting stale memories
- [x] Memory persists across PRD runs and accumulates cross-feature knowledge
- [x] `ralph memory` subcommands work (stats, search, prune, reset)
- [x] Codebase summaries are generated and embedded on first run
- [x] Retrieval quality is visibly better than flat context (test with 10+ story PRD) — **verified via TestRetrievalQuality_SemanticVsFlat and TestRetrievalQuality_TokenBudgetPreventsOverload**
- [x] Memory does NOT degrade after 5+ PRD runs (verify with stats/search) — **verified via TestMemoryNoDegradation_10Runs and TestDecayMath_ConvergenceProperties**

### Estimated Scope

~900-1100 lines of Go code in `internal/memory/` + modifications to runner
and TUI. Plus ChromaDB setup/management scripts. The hygiene mechanisms add
~200 lines over a naive implementation but are critical for long-term viability.

---

## Phase 3: Cost Tracking, TUI Analytics & Remote Monitoring

**Impact: High (visibility) | Complexity: Low | Dependencies: None (can be built in parallel with Phase 1 or 2)**

### Goal

Surface token usage, cost estimates, and run analytics directly in the TUI.
Enable remote monitoring of ralph runs from a phone via a lightweight status
page and push notifications (for when you leave your laptop running and walk
away). Critical for understanding spend and demonstrating sophistication.

### Context for Builder

Claude's streaming JSON output (parsed in `internal/runner/runner.go`
`streamProcessor`) includes `message_start` events with `usage` fields
containing `input_tokens` and `output_tokens`. These are currently logged
to the raw JSON file but not tracked.

Gemini usage (judge, autofix) is invoked via `internal/exec/` command
wrappers and output is not currently parsed for token counts.

### What to Build

#### 3a. Cost Tracking Package

Create `internal/costs/` package:

```go
type TokenUsage struct {
    InputTokens  int
    OutputTokens int
    CacheRead    int  // if available from streaming
    CacheWrite   int
    Model        string
    Provider     string // "claude" | "gemini"
}

type StoryCosting struct {
    StoryID     string
    Iterations  []IterationCost
    JudgeCosts  []TokenUsage
    TotalCost   float64
}

type RunCosting struct {
    Stories     map[string]*StoryCosting
    QualityCost TokenUsage
    DAGCost     TokenUsage
    PlanCost    TokenUsage
    TotalCost   float64
    StartTime   time.Time
}
```

Pricing table (configurable, with sensible defaults for current Claude/Gemini
pricing). Persist to `.ralph/costs.json` per run.

#### 3b. Stream Parser Integration

Modify `internal/runner/runner.go` `streamProcessor`:
- Parse `usage` from `message_start` and `message_delta` events
- Accumulate per-iteration totals
- Send a new `CostUpdateMsg` to the TUI

#### 3c. TUI Integration

Add a new context tab: `contextCosts` (alongside progress, worktree, judge,
quality):

```
╭─ Costs ──────────────────────────────────────╮
│ Current Run          $12.47                  │
│ ─────────────────────────────────────────    │
│ STORY-1 (done)       $2.31  (2 iterations)  │
│ STORY-2 (done)       $1.87  (1 iteration)   │
│ STORY-3 (running)    $3.12  (3 iterations)  │
│   └─ Judge (2x)      $0.42                  │
│ STORY-4 (queued)     —                       │
│ ─────────────────────────────────────────    │
│ Quality review        $2.18                  │
│ DAG analysis          $0.15                  │
│ ─────────────────────────────────────────    │
│ Tokens: 1.2M in / 340K out                  │
│ Cache hit rate: 67%                          │
╰──────────────────────────────────────────────╯
```

Also add a cost summary to the header bar: `$12.47` next to the iteration
count and progress bar.

#### 3d. Run History

Append completed run summaries to `.ralph/run-history.json`:
```json
{
  "runs": [
    {
      "prd": "feature-auth",
      "date": "2026-03-11",
      "stories": 8,
      "completed": 7,
      "failed": 1,
      "total_cost": 24.50,
      "duration_minutes": 45,
      "total_iterations": 12,
      "avg_iterations_per_story": 1.5,
      "stuck_count": 1,
      "judge_rejection_rate": 0.125
    }
  ]
}
```

Show a summary of recent runs on ralph startup (or via `ralph history`
subcommand).

#### 3e. Push Notifications

Create `internal/notify/` package using [ntfy.sh](https://ntfy.sh) — a free,
open-source push notification service. Zero accounts, zero API keys.

Send notifications for key events:
- Story completed / failed
- Stuck detected
- Judge rejection
- Run complete (all stories done)
- Run crashed / unexpected error

Implementation is trivial — one HTTP POST per notification:
```go
func Notify(topic, title, message string, priority int) error {
    req, _ := http.NewRequest("POST", "https://ntfy.sh/"+topic,
        strings.NewReader(message))
    req.Header.Set("Title", title)
    req.Header.Set("Priority", strconv.Itoa(priority))
    req.Header.Set("Tags", "robot")
    _, err := http.DefaultClient.Do(req)
    return err
}
```

**Integration points:**
- `internal/tui/model.go`: send notifications on phase transitions and
  story state changes
- `internal/coordinator/coordinator.go`: send on worker completion/failure
  in parallel mode

**Configuration:**
- `--notify <topic>` flag — enables notifications to the given ntfy topic.
  If omitted, notifications are disabled.
- Topic should be a user-chosen secret string (e.g., `ralph-eoghan-a8f3`)
  to prevent others from subscribing
- User installs the ntfy app on their phone and subscribes to their topic
- Optionally: `--ntfy-server <url>` for self-hosted ntfy instances (e.g.,
  running on your Tailscale network for full privacy)

#### 3f. Remote Status Page

Create `internal/statuspage/` package — a minimal HTTP server that exposes
ralph's current state as a mobile-friendly web page.

**Server:**
- Starts on `--status-port <port>` (default: disabled, e.g., `--status-port 19876`)
- Serves a single HTML page with auto-refresh via Server-Sent Events (SSE)
- Also exposes `GET /api/status` returning JSON for programmatic access

**Status page content:**
```
Ralph — feature-auth
━━━━━━━━━━━━━━━━━━━━
Phase: parallel | Running 47m
Stories: 7/10 complete | 1 failed | 2 in progress
Cost: $18.42

STORY-1  ✅  done       $2.31
STORY-2  ✅  done       $1.87
STORY-3  ✅  done       $3.12
STORY-4  ❌  failed     $4.50  (stuck: repeated bash)
STORY-5  ✅  done       $1.20
STORY-6  ⚙️  running    $2.80  (iteration 2, implementer)
STORY-7  ✅  done       $1.44
STORY-8  ⚙️  running    $1.18  (iteration 1, architect)
STORY-9  ⏳  queued     —      (depends on STORY-6)
STORY-10 ✅  done       $0.00
```

**Remote access via Tailscale:**
- With Tailscale installed on laptop and phone, the status page is
  accessible at `http://<laptop-tailscale-ip>:19876` from anywhere
- No port forwarding, no public exposure, encrypted via WireGuard
- Document the Tailscale setup in a `ralph status --help` message

**Design constraints:**
- Single HTML file, inlined CSS, no JS framework — it's a status page,
  not an app. Must render well on mobile Safari/Chrome.
- SSE connection for live updates (push from ralph's event system)
- The status page reads from the same `RunCosting` and coordinator state
  that the TUI uses — no duplication of data sources

### Acceptance Criteria

- [ ] Token usage is parsed from Claude streaming output per iteration
- [ ] Cost estimates are calculated using configurable pricing table
- [ ] New "Costs" context tab in TUI shows per-story and total costs
- [ ] Header bar shows running cost total
- [ ] Gemini token usage is tracked for judge and autofix invocations
- [ ] Run history is persisted and viewable across runs
- [ ] Cost data is included in checkpoint (Phase 1) for resume accuracy
- [ ] Push notifications fire for story completion, failure, stuck, and run done
- [ ] Notifications are disabled by default, enabled via `--notify <topic>`
- [ ] Status page serves mobile-friendly HTML with live SSE updates
- [ ] Status page is accessible from phone via Tailscale
- [ ] JSON API endpoint available for programmatic access

### Estimated Scope

~700-800 lines of new Go code. Cost tracking (~400), notifications (~50),
status page (~250). Mostly parsing, TUI rendering, and stdlib `net/http`.

---

## Phase 4: Agent Specialization

**Impact: High | Complexity: Medium | Dependencies: Phase 1 (story state for plan handoff), Phase 2 (vector memory for architect context)**

### Goal

Replace the one-size-fits-all Claude invocation with role-specific agents that
are better at their specific task. The key split: an architect agent that plans,
and an implementer agent that executes the plan.

### Context for Builder

Currently, every Claude invocation uses the same prompt template
(`ralph-prompt.md`) assembled in `internal/runner/runner.go` `BuildPrompt()`.
The prompt tells Claude to pick a story, implement it, test it, and commit.

The runner is invoked from:
- Serial mode: `internal/tui/commands.go` via `runClaudeCmd()`
- Parallel mode: `internal/worker/worker.go` via `Run()`
- Quality fix: `internal/quality/quality.go`

All use `runner.RunClaude()` with the same basic flow.

### What to Build

#### 4a. Agent Role System

Create `internal/roles/` package:

```go
type Role string

const (
    RoleArchitect   Role = "architect"
    RoleImplementer Role = "implementer"
    RoleDebugger    Role = "debugger"
    RoleReviewer    Role = "reviewer"
)

type AgentConfig struct {
    Role        Role
    PromptFile  string   // path to role-specific prompt template
    Model       string   // can vary by role (future: Phase 7)
    MaxTokens   int      // output token budget
    Tools       []string // tool restrictions (future consideration)
}
```

#### 4b. Role-Specific Prompts

Create prompt templates:

- `prompts/architect.md`: "You are a software architect. Read the codebase,
  understand the story requirements, and produce a detailed implementation
  plan. Write the plan to `.ralph/stories/{id}/plan.md`. Include: files to
  create/modify, approach for each file, risks and edge cases, testing
  strategy. Do NOT write code."

- `prompts/implementer.md`: "You are an implementer. Read the plan at
  `.ralph/stories/{id}/plan.md` and execute it precisely. Focus on writing
  clean, working code that satisfies the acceptance criteria. Update
  state.json as you complete subtasks."

- `prompts/debugger.md`: "You are a debugger specializing in fixing stuck
  implementations. You have context about what went wrong (see stuck info
  and error history). Focus on diagnosing root cause and applying minimal,
  targeted fixes."

#### 4c. Orchestration Flow

Modify the story execution flow:

```
Story Start
  ├─ Architect agent runs (reads codebase + vector memory, writes plan)
  ├─ Plan stored in .ralph/stories/{id}/plan.md
  ├─ Implementer agent runs (reads plan, writes code)
  ├─ [If stuck] → Debugger agent runs (specialized stuck context)
  ├─ [If judge rejects] → Implementer re-runs with feedback
  └─ Story complete
```

**When to skip the architect:**
- FIX-stories (already have clear scope from autofix generation)
- Stories with very short descriptions (< 50 words) — likely simple enough
- Flag: `--no-architect` to disable globally

**Integration points:**
- `internal/runner/runner.go`: accept `AgentConfig` parameter, use
  role-specific prompt template
- `internal/worker/worker.go`: run architect phase before implementer phase
- `internal/tui/model.go`: show current agent role in header/status
  (e.g., "STORY-3: architect → planning...")
- `internal/tui/logpanel.go`: differentiate architect vs implementer output
  visually (different header colors or labels)

#### 4d. Plan Quality Gate

After the architect runs, before launching the implementer:
- Validate that `plan.md` exists and is non-empty
- Optionally: quick validation via a fast model ("does this plan address
  all acceptance criteria?")
- If plan is insufficient, re-run architect with feedback

### Acceptance Criteria

- [ ] Architect agent produces implementation plans before coding begins
- [ ] Implementer agent follows the plan and updates story state
- [ ] Debugger agent is used for FIX-stories and stuck recovery
- [ ] TUI shows current agent role during execution
- [ ] Architect phase can be skipped for simple stories or via flag
- [ ] Plan quality is validated before implementation begins
- [ ] Story success rate improves measurably vs baseline (track in run history)

### Estimated Scope

~600-800 lines of new Go code. New prompt templates. Modifications to runner,
worker, coordinator, and TUI.

---

## Phase 5: Learning Loop / Self-Improving System

**Impact: Medium-High | Complexity: Medium | Dependencies: Phase 2 (vector DB for semantic storage), Phase 3 (run history for analysis input)**

### Goal

Ralph should get measurably better at each successive PRD run by automatically
extracting and applying lessons from past runs.

### Context for Builder

Currently, `ralph-prompt.md` is a static template. Patterns are consolidated
manually in the "Codebase Patterns" section of progress.md. There's no
mechanism for ralph to learn that, for example, "stories involving the auth
module always need 2+ iterations" or "tests in module X are flaky and need
retries."

Run history (Phase 3) provides quantitative data. Vector memory (Phase 2)
provides qualitative data. This phase connects them.

### What to Build

#### 5a. Post-Run Analysis

Create `internal/learning/` package:

After a PRD run completes, automatically invoke a fast model to analyze:
- Which stories required retries and why?
- Which stories got stuck and what was the pattern?
- Which stories were rejected by judge and what was missing?
- What codebase-specific patterns emerged?

Output: structured lessons stored in vector DB (`ralph_lessons` collection)
and in `.ralph/lessons.json` for human review.

```json
{
  "lessons": [
    {
      "id": "lesson-001",
      "category": "testing",
      "pattern": "Integration tests in internal/api/ require the test database to be running",
      "evidence": "STORY-3 and STORY-7 both got stuck on 'connection refused' errors",
      "recommendation": "Include 'ensure test DB is running' in implementation plans for API stories",
      "confidence": 0.9,
      "times_confirmed": 2
    }
  ]
}
```

#### 5b. Dynamic Prompt Augmentation

**Critical design rule: `ralph-prompt.md` stays static and hand-tuned.
Lessons are NEVER appended to it.** Instead, lessons are injected dynamically
at prompt build time via the same vector retrieval pipeline as Phase 2.

At the start of each iteration, `BuildPrompt()` queries `ralph_lessons`
collection with the current story's context. Relevant lessons are injected
into a bounded "Lessons from Previous Runs" section — subject to the same
`--memory-max-tokens` budget that governs all vector DB context injection.

Lessons gain confidence as they're confirmed across runs. Low-confidence
lessons (single occurrence) are injected with lower priority. The retrieval
budget ensures the lesson section cannot grow unbounded regardless of how
many lessons accumulate in the vector DB.

#### 5c. Anti-Pattern Detection

Track recurring failure modes:
- Same file causing stuck detection repeatedly → flag as "fragile area"
- Same test failing across stories → flag as "flaky test"
- Same judge rejection reason → flag as "common oversight"

Surface these in the TUI (new section in costs/analytics tab) and
automatically inject relevant anti-patterns into prompts for related stories.

#### 5d. Prompt Refinement (Not Growth)

After N runs (configurable, default 5), offer to generate a **refined**
`ralph-prompt.md`. This is NOT about appending lessons — it's about
tightening the base prompt:
- Remove instructions that are redundant with learned behaviour
- Sharpen instructions that aren't producing good results
- Simplify wording where the agent consistently understands intent

The prompt should get *sharper and shorter* over time, not longer. Lessons
stay in the vector DB where they're retrieved dynamically. The base prompt
is the stable core.

This is always human-reviewed — ralph suggests a diff, the user approves
or rejects. The current prompt is backed up to
`.ralph/archive/ralph-prompt-{date}.md` before any changes.

#### 5e. Skill Feedback Loop

The `/ralph` skill (`skills/ralph/SKILL.md`) generates prd.json files that
ralph then executes. Currently, the skill has no awareness of what works
well in practice — it uses static heuristics for story sizing, ordering,
and acceptance criteria. This creates a broken feedback loop: ralph learns
from execution, but the skill that creates the *input* never improves.

**Close the loop:**

After post-run analysis (5a), extract lessons specifically relevant to PRD
quality and store them in a `ralph_prd_lessons` vector DB collection:

- **Story sizing lessons**: "Stories touching `internal/api/` routinely cause
  context exhaustion when they span 4+ files — split more aggressively"
- **Criteria quality lessons**: "Judge rejects stories with 'handles errors
  gracefully' — use specific error scenarios instead"
- **Ordering lessons**: "UI stories that depend on server actions fail when
  the DAG marks them as independent — enforce stricter ordering"
- **Missing criteria patterns**: "Schema stories in this codebase should
  always include 'database migrations run cleanly' as a criterion"

**Skill integration:**

The skill itself (`SKILL.md`) stays static — same principle as
`ralph-prompt.md`. When the skill runs, it queries `ralph_prd_lessons` for
the current project and injects relevant lessons as additional constraints
before generating the PRD. The skill's output gets shaped by learned lessons
without the skill template growing.

**Example injection into skill context:**
```
## Learned PRD Constraints (from previous ralph runs)
- Stories modifying internal/auth/ should be split to ≤3 files per story
  (confidence: 0.9, confirmed 3 times)
- Always include "Tests pass" for stories touching internal/db/
  (confidence: 0.85, confirmed 2 times)
- Stories with >5 acceptance criteria tend to cause context exhaustion —
  split into two stories instead (confidence: 0.75, confirmed 2 times)
```

### Acceptance Criteria

- [ ] Post-run analysis generates structured lessons automatically
- [ ] Lessons are embedded in vector DB and persisted in JSON
- [ ] Relevant lessons are injected into prompts for new stories
- [ ] Confidence scoring tracks lesson reliability across runs
- [ ] Anti-patterns are detected and surfaced in TUI
- [ ] Prompt refinement suggestions make the prompt shorter/sharper, not longer
- [ ] Base prompt is backed up before any refinement changes
- [ ] PRD-quality lessons are extracted and stored in `ralph_prd_lessons` collection
- [ ] `/ralph` skill queries learned lessons before generating prd.json
- [ ] Generated PRDs reflect learned sizing, ordering, and criteria patterns

### Estimated Scope

~600-800 lines of Go code. One new analysis prompt template. Modifications
to runner, TUI, and skill invocation pipeline.

---

## Phase 6: Multi-Model Orchestration

**Impact: Medium | Complexity: Low | Dependencies: Phase 3 (cost tracking to measure savings), Phase 4 (agent roles to assign models)**

### Goal

Use the right model for each task — expensive models for complex work,
fast/cheap models for mechanical tasks. Reduce cost without sacrificing
quality.

### Context for Builder

Currently, Claude is invoked via `internal/runner/runner.go` which calls the
`claude` CLI. Gemini is invoked via `internal/exec/gemini.go` for judge and
autofix. The model is not configurable per-invocation.

### What to Build

#### 6a. Model Configuration

Extend `internal/config/` and `internal/roles/`:

```go
type ModelTier string

const (
    TierHigh   ModelTier = "high"   // Opus — complex stories, architecture
    TierMedium ModelTier = "medium" // Sonnet — standard implementation
    TierFast   ModelTier = "fast"   // Haiku — mechanical tasks, analysis
)

// Default mapping (configurable via CLI flags)
var DefaultModels = map[ModelTier]string{
    TierHigh:   "opus",
    TierMedium: "sonnet",
    TierFast:   "haiku",
}
```

#### 6b. Automatic Tier Assignment

Assign model tiers based on:
- **Agent role**: Architect → High, Implementer → Medium, Debugger → High
- **Story complexity heuristic**: description length × acceptance criteria
  count × dependency depth
- **Override**: `--model-tier high` forces all invocations to use the high tier
- Simple stories (short description, few criteria, no deps) → Medium tier
- FIX-stories → Medium tier (scoped, mechanical)

#### 6c. Fast Model for Utility Tasks

Use the fast tier (Haiku-class) for:
- DAG analysis (`internal/dag/`)
- Stuck detection analysis
- Memory retrieval re-ranking
- Post-run lesson extraction
- Commit message generation
- Plan validation (Phase 4)

These are currently using full Claude which is overkill.

#### 6d. Runner Integration

Modify `internal/runner/runner.go`:
- Accept model parameter: `RunClaude(ctx, opts RunOptions)` where RunOptions
  includes `Model string`
- Pass `--model` flag to the `claude` CLI invocation
- Track model used per iteration in cost tracking (Phase 3)

### Acceptance Criteria

- [ ] Model tier is configurable per agent role
- [ ] Automatic complexity-based tier assignment works correctly
- [ ] Utility tasks (DAG, stuck analysis, etc.) use fast tier
- [ ] Cost savings are measurable via Phase 3 tracking (target: 30-50% reduction)
- [ ] Quality does not regress (judge pass rate stays stable or improves)
- [ ] `--model-tier` override works for forcing a specific tier

### Estimated Scope

~300-400 lines of Go code. Modifications to runner, config, roles, dag,
and utility invocation sites.

---

## Phase 7: MCP Tool Server

**Impact: Medium-High | Complexity: Medium | Dependencies: Phase 2 (vector memory to expose), Phase 1 (story state to expose)**

### Goal

Create a bidirectional communication channel between ralph and its child
Claude instances. Instead of fire-and-forget, agents can query ralph for
memory, check sibling story status, and coordinate.

### Context for Builder

MCP (Model Context Protocol) allows Claude Code to connect to external tool
servers that provide additional capabilities. Claude Code supports MCP servers
via its configuration. If ralph runs an MCP server, each Claude instance
launched by ralph can be configured to connect to it.

Currently, Claude instances are launched by `internal/runner/runner.go` via
the `claude` CLI with `--output-format stream-json --verbose`. MCP servers
can be configured via Claude Code's settings or CLI flags.

### What to Build

#### 7a. MCP Server

Create `internal/mcp/` package implementing an MCP tool server:

**Tools to expose:**

```
ralph_query_memory(query: string, collection: string, top_k: int)
  → Returns semantically relevant memories from the vector DB

ralph_get_story_status(story_id: string)
  → Returns current status, files touched, completion state of any story

ralph_get_story_plan(story_id: string)
  → Returns the architect's plan for a story

ralph_list_stories()
  → Returns all stories with their current status

ralph_report_pattern(pattern: string, category: string)
  → Agent proactively reports a discovered pattern (embedded immediately)

ralph_report_blocker(description: string, related_story: string)
  → Agent signals it's blocked on another story's output

ralph_get_codebase_map(module: string)
  → Returns knowledge graph data for a module (Phase 9 prerequisite)
```

#### 7b. Server Lifecycle

- Ralph starts the MCP server on a random localhost port at startup
- Port is passed to each Claude invocation via MCP configuration
- Server shuts down cleanly on ralph exit
- Server handles concurrent requests (multiple parallel workers)

#### 7c. Claude Integration

Modify `internal/runner/runner.go`:
- Add MCP server configuration to Claude CLI invocation
- This likely means writing a temporary `.claude/settings.json` or using
  CLI flags for MCP server connection

Modify prompt templates:
- Inform agents about available MCP tools
- Encourage use of `ralph_query_memory` for context retrieval
- Encourage use of `ralph_report_pattern` for knowledge sharing

#### 7d. Blocker Coordination

When an agent reports a blocker via `ralph_report_blocker`:
- Coordinator checks if the blocking story is in progress
- If so, the blocked worker is paused (not killed) and resumed when the
  blocking story completes
- This enables finer-grained parallel coordination than the DAG alone

### Acceptance Criteria

- [ ] MCP server starts and stops with ralph lifecycle
- [ ] Claude instances can query vector memory via MCP tools
- [ ] Story status and plan retrieval works across parallel workers
- [ ] Pattern reporting from agents flows into vector DB in real-time
- [ ] Blocker coordination enables dynamic dependency resolution
- [ ] MCP server handles concurrent requests from parallel workers safely

### Estimated Scope

~800-1000 lines of Go code. MCP protocol implementation (or use an existing
Go MCP SDK). Modifications to runner and coordinator.

---

## Phase 8: Codebase Knowledge Graph

**Impact: Medium | Complexity: Medium-High | Dependencies: Phase 2 (vector DB as storage layer), Phase 4 (architect agent as primary consumer)**

### Goal

Build a lightweight knowledge graph of the codebase that captures structural
relationships — not just semantic similarity. This enables the architect agent
to understand ripple effects and plan modifications with awareness of the
dependency chain.

### Context for Builder

Currently, the architect agent (Phase 4) reads the codebase directly via
Claude Code's file tools. This works but is expensive (token-wise) and the
agent may miss non-obvious relationships. A knowledge graph provides a
pre-computed structural map.

### What to Build

#### 8a. Graph Schema

Store in SQLite (`.ralph/knowledge.db`):

```sql
-- Nodes
CREATE TABLE entities (
    id TEXT PRIMARY KEY,
    kind TEXT,        -- 'package', 'file', 'function', 'type', 'interface'
    name TEXT,
    file_path TEXT,
    summary TEXT,     -- one-line AI-generated summary
    signature TEXT    -- for functions/types
);

-- Edges
CREATE TABLE relationships (
    source_id TEXT,
    target_id TEXT,
    kind TEXT,        -- 'imports', 'calls', 'implements', 'depends_on',
                      -- 'modified_by_story', 'tests'
    FOREIGN KEY (source_id) REFERENCES entities(id),
    FOREIGN KEY (target_id) REFERENCES entities(id)
);
```

#### 8b. Graph Builder

Create `internal/knowledge/` package:

- **Static analysis pass**: Parse Go AST to extract packages, files, functions,
  types, imports, and call relationships. This is deterministic and fast.
- **AI enrichment pass**: Use a fast model to generate one-line summaries for
  each entity. This is expensive but only needed on first run + incremental
  updates.
- **Story tracking**: When a story completes, add `modified_by_story` edges
  from the story to all files it touched.

Run the builder:
- On first `ralph` launch for a project (full build)
- Incrementally after each story completion (only re-analyze changed files)
- Via `ralph knowledge rebuild` for manual refresh

#### 8c. Query Interface

Expose via MCP (Phase 7) and use in prompt building:

```
ralph_get_codebase_map(module: string) →
  Returns the module's entities, their relationships, and recent story
  modifications. The architect sees: "This module has 5 functions, imports
  3 other modules, was last modified by STORY-2, and implements interface X."

ralph_get_impact_analysis(files: []string) →
  Given a set of files to modify, returns all entities that depend on them
  (reverse dependency walk). Helps the architect anticipate ripple effects.
```

#### 8d. Graph-Enhanced Retrieval

Combine with vector search (Phase 2):
- When retrieving memories for a story, also pull in knowledge graph context
  for the relevant modules
- This gives the agent both semantic context (similar past work) and
  structural context (what this code connects to)

### Acceptance Criteria

- [ ] Knowledge graph is built from Go AST analysis on first run
- [ ] AI-generated summaries are stored for all entities
- [ ] Graph updates incrementally after story completions
- [ ] MCP tools expose graph queries to agents
- [ ] Architect agent uses graph for impact analysis in plans
- [ ] Graph + vector retrieval produces richer context than either alone

### Estimated Scope

~1000-1200 lines of Go code. Go AST parsing, SQLite schema, MCP tool
additions. New prompt sections for architect.

---

## Phase 9: Speculative Parallel Execution

**Impact: Medium | Complexity: High | Dependencies: Robust parallel infrastructure (existing), Phase 1 (checkpoint for rollback)**

### Goal

Increase throughput on large PRDs by executing stories with soft dependencies
in parallel, speculatively, and handling conflicts on merge.

### Context for Builder

Currently, `internal/dag/dag.go` builds a strict dependency graph — a story
only executes when ALL its dependencies are complete. The DAG is built by
asking Claude to analyze story relationships, which tends to be conservative
(over-specifying dependencies).

`internal/coordinator/coordinator.go` already handles merge conflicts via
Claude-assisted resolution. This infrastructure can be extended.

### What to Build

#### 9a. Dependency Classification

Modify DAG analysis to classify dependencies:

```json
{
  "id": "STORY-5",
  "hard_depends_on": ["STORY-1"],
  "soft_depends_on": ["STORY-3"],
  "reason": "STORY-3 modifies the same config file but changes are independent"
}
```

- **Hard dependency**: Must complete first (shared interface, data model change)
- **Soft dependency**: Likely to touch related code but could work independently

#### 9b. Speculative Scheduler

Modify `internal/coordinator/coordinator.go`:

- Stories with only soft dependencies on in-progress stories can be scheduled
  speculatively
- Track speculative vs confirmed execution status
- On merge: if speculative story conflicts with its soft dependency, mark
  it for re-verification (re-run implementer with merged state)
- Budget: limit speculative slots to `workers / 2` to avoid waste

#### 9c. Conflict Recovery

When a speculatively-executed story conflicts:
1. Rebase the speculative work onto the now-completed dependency
2. If conflicts are minor (auto-resolvable), keep the work
3. If conflicts are major, re-run the implementer phase only (not architect)
4. Track speculative success rate in run history (Phase 3)

#### 9d. TUI Integration

Show speculative status in the stories panel:
- `⚡ STORY-5 (speculative)` — running ahead of soft dependency
- `🔄 STORY-5 (rebasing)` — soft dependency completed, merging
- Track and display speculative hit rate in analytics

### Acceptance Criteria

- [ ] DAG analysis distinguishes hard vs soft dependencies
- [ ] Speculative execution launches stories with only soft dependencies pending
- [ ] Conflict recovery handles rebase/re-verification automatically
- [ ] Speculative execution improves wall-clock time for 10+ story PRDs
- [ ] Speculative success rate is tracked and displayed
- [ ] No correctness regressions — hard dependencies are still strictly enforced

### Estimated Scope

~600-800 lines of Go code. Modifications to dag, coordinator, worker, and TUI.

---

## Phase 10: Web Dashboard (Optional / Stretch)

**Impact: Low-Medium | Complexity: Medium | Dependencies: Phase 3 (cost data), Phase 5 (learning data)**

### Goal

A localhost web UI for historical analytics, run comparison, and team
visibility. This is a nice-to-have that becomes valuable if ralph is
shared with the team.

### What to Build

- Lightweight Go HTTP server (stdlib `net/http`) serving a single-page app
- Websocket for real-time updates during active runs
- Pages: run history, cost trends, story success rates, learning insights,
  DAG visualization (using D3.js or similar)
- Launch via `ralph dashboard` subcommand
- Read from `.ralph/run-history.json`, `.ralph/lessons.json`, cost data

### Note

This is explicitly the lowest priority. The TUI with cost tracking (Phase 3)
covers 90% of the need. Only pursue this if ralph goes to the team and they
want a shared view.

---

## Phase Dependency Graph

```
Phase 1 (Story State + Checkpoint)
  │
  ├──→ Phase 2 (Vector Memory)
  │      │
  │      ├──→ Phase 5 (Learning Loop)
  │      ├──→ Phase 7 (MCP Server)
  │      │      │
  │      │      └──→ Phase 8 (Knowledge Graph)
  │      │
  │      └──→ Phase 4 (Agent Specialization)
  │
  ├──→ Phase 9 (Speculative Parallel)
  │
  Phase 3 (Cost Tracking) ← independent, can run in parallel with 1 & 2
  │
  └──→ Phase 6 (Multi-Model) ← benefits from Phase 3 + 4
                │
                └──→ Phase 10 (Web Dashboard) ← optional stretch
```

## Recommended Execution Order

| Order | Phase | Est. Effort | Cumulative Value |
|-------|-------|-------------|------------------|
| 1st   | Phase 1: Story State + Checkpoint ✅ | ~3-4 days | Foundation for everything |
| 2nd   | Phase 3: Cost Tracking | ~2 days | Quick win, high visibility |
| 3rd   | Phase 2: Vector Memory ✅ | ~4-5 days | Transformative capability |
| 4th   | Phase 4: Agent Specialization | ~3-4 days | Quality step-change |
| 5th   | Phase 6: Multi-Model | ~2 days | Cost optimization |
| 6th   | Phase 5: Learning Loop | ~3-4 days | Compounding returns |
| 7th   | Phase 7: MCP Server | ~4-5 days | Agent coordination leap |
| 8th   | Phase 8: Knowledge Graph | ~5-6 days | Structural intelligence |
| 9th   | Phase 9: Speculative Parallel | ~4-5 days | Throughput optimization |
| 10th  | Phase 10: Web Dashboard | ~3-4 days | Team visibility (if needed) |

**Total estimated effort: ~33-39 days of focused work**

Note: Phases 1 and 3 have no dependency on each other and can be built
concurrently. Similarly, Phase 6 is relatively independent and could be
pulled forward if cost is a pressing concern.

---

## Success Metrics

After full rollout, ralph should demonstrate:

- **Context efficiency**: Agent prompts contain only relevant context, not
  everything (measurable via token reduction per iteration)
- **Story success rate**: >90% first-attempt pass rate (up from current baseline)
- **Cost per story**: 30-50% reduction via multi-model + better context
- **Crash resilience**: Any interruption recoverable via checkpoint + resume
- **Cross-run learning**: Measurable improvement in success rate across
  successive PRD runs on the same codebase
- **Throughput**: Large PRDs (20+ stories) complete faster via speculative
  parallel execution
- **Visibility**: Full cost and performance analytics in TUI
