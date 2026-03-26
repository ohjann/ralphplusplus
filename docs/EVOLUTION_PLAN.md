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
- **Stuck detection** with auto-fix story generation and tool-call loop detection
- **Memory**: `progress.md` (append-only text log) and `events.jsonl` (structured event log)
- **Context injection**: recent events formatted into next iteration's prompt
- **Archive system**: preserves history across branch/feature changes
- **Terminal & macOS notifications** for story events

### Key Limitations to Address

1. ~~Memory is flat — no semantic retrieval, all recent context dumped into prompt~~ → **Addressed in Phase 2/2.1** (vector DB → markdown memory with dream consolidation)
2. ~~Context exhaustion recovery is lossy — relies on text markers in progress.md~~ → **Addressed in Phase 1** (structured per-story state)
3. ~~No structured per-story work state — agent must reconstruct intent from history~~ → **Addressed in Phase 1** (storystate package)
4. ~~No cost visibility — token usage is logged but not surfaced~~ → **Addressed in Phase 3** (usage tracking tab, run history, remote status page)
5. ~~All Claude invocations use the same generalist prompt~~ → **Addressed in Phase 4** (role-specific agents: architect, implementer, debugger) **+ Phase 6** (per-role model selection: Opus/Sonnet/Haiku)
6. ~~No crash recovery — killing ralph mid-run loses orchestration state~~ → **Partially addressed in Phase 1** (checkpoint package — detection and prompt work, but resume logic is a stub)
7. ~~No cross-run learning — each PRD run starts from zero institutional knowledge~~ → **Addressed in Phase 2/2.1** (markdown memory with dream consolidation)

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

## Phase 2: Semantic Memory with Vector Database ✅ COMPLETE → ⚠️ SUPERSEDED BY PHASE 2.1

**Impact: Transformative | Complexity: Medium | Dependencies: Phase 1 (story state provides richer content to embed)**

> **Status: Superseded** (completed 2026-03-12, superseded by Phase 5.8 on 2026-03-26).
> Phase 2 was implemented and worked end-to-end, but with 1M context windows
> now standard, the vector DB infrastructure (ChromaDB, Voyage AI, Python env,
> ~2600 lines of Go) solves a problem that no longer exists — context scarcity.
> An LLM reading markdown files in-context performs better relevance filtering
> than cosine similarity and requires zero external dependencies. Phase 5.8
> replaces this with markdown-based memory and dream consolidation.
>
> **Original deliverables (to be removed by Phase 5.8):**
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

## Phase 3: Usage Tracking, TUI Analytics & Remote Monitoring ✅ COMPLETE

**Impact: High (visibility) | Complexity: Low | Dependencies: None (can be built in parallel with Phase 1 or 2)**

> **Status: Complete** (completed 2026-03-13).
> Phase 3 has been fully implemented. Key deliverables:
> - `internal/costs/` package — token usage parsing, per-story and per-run tracking
> - Usage context tab in TUI with per-story breakdown, judge usage, token counts, cache hit rate
> - When token data is unavailable (e.g., Claude Max subscription), displays iteration turns and duration instead
> - Header bar shows running usage total
> - Run history persisted to `.ralph/run-history.json`, viewable via `ralph history`
> - Push notifications via ntfy.sh (`--notify <topic>`) and macOS Notification Center
> - Remote status page with SSE live updates (`--status-port <port>`)
> - JSON API endpoint at `/api/status`
>
> **Post-completion update:** "Costs" tab renamed to "Usage" tab to better reflect
> that users on subscription plans (Claude Max) don't have per-token costs. The tab
> now gracefully falls back to showing iteration turns and duration when token
> data is unavailable.

### Goal

Surface token usage and run analytics directly in the TUI. Enable remote
monitoring of ralph runs from a phone via a lightweight status page and push
notifications (for when you leave your laptop running and walk away).

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

- [x] Token usage is parsed from Claude streaming output per iteration
- [x] Usage estimates tracked (falls back to turns/duration when tokens unavailable)
- [x] New "Usage" context tab in TUI shows per-story and total usage
- [x] Header bar shows running usage total
- [x] Gemini token usage is tracked for judge and autofix invocations
- [x] Run history is persisted and viewable across runs
- [x] Cost data is included in checkpoint (Phase 1) for resume accuracy
- [x] Push notifications fire for story completion, failure, stuck, and run done
- [x] Notifications are disabled by default, enabled via `--notify <topic>`
- [x] Status page serves mobile-friendly HTML with live SSE updates
- [x] Status page is accessible from phone via Tailscale
- [x] JSON API endpoint available for programmatic access

### Estimated Scope

~700-800 lines of new Go code. Cost tracking (~400), notifications (~50),
status page (~250). Mostly parsing, TUI rendering, and stdlib `net/http`.

---

## Phase 3.1: 1M Context Recalibration ✅ COMPLETE

**Impact: Medium-High | Complexity: Low-Medium | Dependencies: Phases 1, 2, 3 (all complete)**

> **Status: Complete** (completed 2026-03-16).
> Phase 3.1 has been fully implemented. Key deliverables:
> - Memory retrieval defaults recalibrated (top-k=15, max-tokens=8000, min-score=0.6)
> - `/ralph` skill story-sizing guidance rewritten for scope-quality over context-size
> - `ralph-prompt.md` updated to remove context-scarcity language, encourage broad exploration
> - Activity log cap increased from 64KB to 256KB
> - Iteration count displayed next to running stories in stories panel
> - Memory tab shows retrieved memories with relevance scores for current story
> - Stories panel supports expand/collapse to show files, subtasks, judge feedback
> - Judge tab shows full rejection reasoning per failed criterion
>
> Claude now supports 1M token context. Many Phase 1-2 design decisions were
> driven by context scarcity (128K-200K windows). This phase recalibrated
> existing parameters and heuristics to take advantage of the larger window
> without rearchitecting anything.

### Goal

Loosen context-scarcity constraints across Phases 1-3 so that Ralph produces
fewer, larger stories and injects richer memory context per iteration. The
agent loop, orchestration, and quality systems remain unchanged — they solve
execution control, not context management.

### What to Change

#### 3.1a. Relax Memory Retrieval Defaults

**File: `internal/config/config.go` (`DefaultMemoryConfig`)**

| Parameter | Current | New | Rationale |
|-----------|---------|-----|-----------|
| `--memory-top-k` | 5 | 15 | More relevant memories per collection without context pressure |
| `--memory-max-tokens` | 2000 | 8000 | 4× budget — still <1% of 1M context |
| `--memory-min-score` | 0.7 | 0.6 | Include slightly less confident memories; more context is cheap now |

Also update the help text in `config.go` and `README.md` to reflect new defaults.

#### 3.1b. Relax Story Sizing in `/ralph` Skill

**File: `skills/ralph/SKILL.md`**

Replace the "completable in ONE context window" framing with a scope-quality
framing. The constraint is no longer context size — it's coherent execution.

New guidance:
- **Right-sized stories**: Coherent, self-contained changes that can be
  implemented and tested in one pass. Can span 5-15 files if the changes
  are logically related.
- **Too big**: Changes that require fundamentally different expertise
  (e.g., schema + middleware + UI + tests for a new feature). Split by
  *concern*, not by *file count*.
- **Rule of thumb**: If the acceptance criteria require unrelated types of
  work, split. If they're all facets of the same change, keep together.

Remove references to "context window" and "runs out of context" as the
primary splitting rationale.

#### 3.1c. Update `ralph-prompt.md` Context Guidance

**File: `ralph-prompt.md`**

The agent prompt should reflect that context is abundant:
- Remove any language suggesting the agent should be conservative about
  reading files or exploring the codebase
- Encourage the agent to read more broadly when planning (the full module,
  not just the target file)
- Keep the instruction to update story state — that's for orchestration,
  not context recovery

#### 3.1d. Increase Activity Log Cap

**File: `internal/runner/runner.go` (`ReadActivityContent`)**

| Parameter | Current | New | Rationale |
|-----------|---------|-----|-----------|
| MaxSize | 64KB | 256KB | Longer iterations produce more output; 64KB truncates early context that explains the agent's approach |

The TUI already tail-trims, so this doesn't affect memory usage meaningfully.
With bigger stories and 10-15 file changes per iteration, users lose the
planning/exploration phase of the output under the current cap.

#### 3.1e. Show Iteration Count in Stories Panel

**File: `internal/tui/stories_panel.go`**

Display the current iteration number next to running stories:

```
⠋ P3-003: Implement auth middleware       (iter 3) W2  1m 42s
```

The iteration count is already tracked in story state (`iteration_count`)
and internally in the TUI model. Just not rendered. With bigger stories
taking more iterations, this is important signal for the user to gauge
progress and spot stuck stories early.

#### 3.1f. Show Retrieved Memories in Memory Tab

**File: `internal/tui/context_panel.go` (memory tab rendering)**

Currently the memory tab shows only capacity bars per collection:

```
ralph_patterns   [████░░░░] 42/100
ralph_completions [██░░░░░░] 18/200
```

Add a section below showing the memories that were actually injected into
the current story's prompt — titles, collection source, and relevance
scores. The retrieval results are already formatted by
`internal/memory/retrieval.go` `RetrieveContext()` — capture the individual
results before formatting and surface them in the TUI.

```
╭─ Retrieved for P3-003 ─────────────────────╮
│ 0.92  patterns    Config uses viper with... │
│ 0.87  completions P2-001: Added auth mid... │
│ 0.81  errors      TestDB connection refus.. │
│ 0.74  decisions   Chose JWT over session... │
│        ... 11 more (8,000 token budget)     │
╰─────────────────────────────────────────────╯
```

This requires `RetrieveContext()` to return structured results alongside the
formatted string (or expose a separate method). The TUI receives these via
a new message type and renders them in the memory tab.

#### 3.1g. Expandable Story Details in Stories Panel

**File: `internal/tui/stories_panel.go`**

Allow pressing Enter or right-arrow on a story to expand inline details:

```
✓ P3-001: Create storystate package          ✓ passed
  ├─ Files: internal/storystate/state.go, state_test.go (+2 more)
  ├─ Iterations: 2 | Judge: passed (1 attempt)
  └─ Subtasks: 4/4 complete

⠋ P3-003: Implement auth middleware          (iter 3) W2  1m 42s
  ├─ Files: internal/auth/middleware.go, handler.go
  ├─ Judge: rejected (1x) — "missing error handling on line 42"
  └─ Subtasks: 2/5 complete
```

All this data exists in story state files (`state.json`): `files_touched`,
`subtasks`, `judge_feedback`, `errors_encountered`, `iteration_count`. For
completed stories, read from `.ralph/stories/{id}/state.json`. For running
stories in parallel mode, read from the worker's synced state.

Press Enter or right-arrow to expand, again to collapse. Only one story
expanded at a time.

#### 3.1h. Show Judge Feedback in Judge Tab

**File: `internal/tui/context_panel.go` (judge tab rendering)**

Currently the judge tab shows the formatted verdict (pass/fail per
criterion). Add the judge's textual feedback below each failed criterion
so the user can see exactly why without digging into logs. With bigger
stories, judge rejections are costlier — immediate visibility matters.

The feedback is already available from `judge.FormatResult()` — ensure
the full rejection reasoning is included, not just the pass/fail icon.

### What NOT to Change

- **The agent loop** — still needed for execution control, test feedback,
  judge verification
- **Curated memory over raw dumps** — even with abundant context, structured
  learnings are better signal than dumping everything. Phase 5.8's dream
  consolidation keeps memory files curated and relevant.
- **Memory hygiene** — noise reduction matters regardless of window size.
  Dream consolidation prunes stale entries and merges duplicates.
- **Checkpoint/resume** — orthogonal to context size
- **Phase 1b (PRD injection)** — already shipped, still saves a tool call.
  Not worth reverting, just not worth optimising further.

### Acceptance Criteria

- [x] Memory retrieval defaults updated (top-k=15, max-tokens=8000, min-score=0.6)
- [x] `/ralph` skill story-sizing guidance rewritten for scope-quality, not context-size
- [x] `ralph-prompt.md` removes context-scarcity language
- [x] All help text and README updated to match new defaults
- [x] Activity log cap increased from 64KB to 256KB
- [x] Iteration count shown next to running stories in stories panel
- [x] Memory tab shows retrieved memories with scores for current story
- [x] Stories panel supports expand/collapse to show files, subtasks, judge feedback
- [x] Judge tab shows full rejection reasoning per failed criterion
- [x] Existing tests pass with new defaults

### Estimated Scope

~400-600 lines of Go code. Config/doc changes (~50 lines), activity cap
(~5 lines), iteration display (~20 lines), memory retrieval display (~100
lines including new message type), expandable stories (~150 lines), judge
feedback (~50 lines). No new packages.

---

## Phase 4: Agent Specialization ✅ COMPLETE

**Impact: High | Complexity: Medium | Dependencies: Phase 1 (story state for plan handoff), Phase 5.8 (markdown memory for architect context)**

> **Status: Complete** (completed 2026-03-16).
> Phase 4 has been fully implemented. Key deliverables:
> - `internal/roles/` package — Role type, AgentConfig, and role registry for agent specialization
> - Role-specific prompt templates (`prompts/architect.md`, `prompts/implementer.md`, `prompts/debugger.md`)
> - `BuildPrompt()` and `RunClaude()` accept agent role parameter for role-specific prompt selection
> - Architect-then-implementer flow in both serial and parallel modes
> - Debugger role integrated with stuck detection for FIX-stories and stuck recovery
> - TUI header and stories panel show current agent role during execution
> - `--no-architect` CLI flag to skip architect phase globally
> - Plan quality gate validates architect output before launching implementer

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
  ├─ Architect agent runs (reads codebase + learned context, writes plan)
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

- [x] Architect agent produces implementation plans before coding begins
- [x] Implementer agent follows the plan and updates story state
- [x] Debugger agent is used for FIX-stories and stuck recovery
- [x] TUI shows current agent role during execution
- [x] Architect phase can be skipped for simple stories or via flag
- [x] Plan quality is validated before implementation begins
- [x] Story success rate improves measurably vs baseline (track in run history)

### Estimated Scope

~600-800 lines of new Go code. New prompt templates. Modifications to runner,
worker, coordinator, and TUI.

---

## Phase 5: Learning Loop / Self-Improving System (Revised) ✅ COMPLETE → ⚠️ PARTIALLY SUPERSEDED BY PHASE 2.1

**Impact: Medium-High | Complexity: Medium | Dependencies: Phase 5.8 (markdown memory — supersedes Phase 2), Phase 3 (run history — already complete)**

> **Status: Implemented** (completed 2026-03-23), **partially superseded** by
> Phase 5.8 (2026-03-26). The memory storage and retrieval portions of Phase 5
> (ChromaDB collections, Gemini synthesis, confidence-weighted ranking) are
> replaced by Phase 5.8's markdown memory and Claude-based dream consolidation.
> The conceptual contributions survive: post-run synthesis, anti-pattern
> detection, and the `/ralph` skill feedback loop are preserved in Phase 5.8's
> design — just backed by markdown files and Claude instead of ChromaDB and
> Gemini.
>
> **Original deliverables (storage replaced by Phase 5.8, concepts preserved):**
> - ~~`ralph_lessons` and `ralph_prd_lessons` ChromaDB collections~~ → `learnings.md` and `prd-learnings.md`
> - ~~`SynthesizeRunLessons()` via Gemini~~ → Post-run synthesis via Claude (Phase 5.8b)
> - ~~`EmbedLessons()` with ChromaDB dedup~~ → Markdown append with dream consolidation (Phase 5.8d)
> - `DetectAntiPatterns()` — concept preserved, implementation updated to read from markdown
> - Anti-pattern warnings injected into `BuildPrompt()` — preserved
> - Anti-patterns displayed in TUI Usage tab — preserved
> - `/ralph` skill reads `prd-learnings.md` (was `.ralph/lessons.json`) — preserved
> - ~~Confidence-weighted ranking in retrieval pipeline~~ → LLM-native relevance filtering

> **Revision note (2026-03-19):** The original Phase 5 was written before
> Phase 2 was implemented. The memory pipeline already provides cross-run
> learning — patterns, completions, errors, and decisions are embedded on
> story completion and retrieved semantically in future runs. This revision
> focuses on the **gaps** that remain: holistic post-run synthesis,
> anti-pattern detection, and closing the skill feedback loop. Prompt
> refinement (original 5d) has been cut — the base prompt is hand-tuned
> and working; auto-suggesting edits is a footgun.
>
> **Revision note (2026-03-26):** Phase 5.8 supersedes the storage and
> retrieval portions of this phase. Post-run synthesis now writes to
> markdown files instead of ChromaDB collections, uses Claude instead of
> Gemini, and relies on dream consolidation instead of numeric confidence
> decay. Anti-pattern detection and skill feedback loop concepts are
> preserved but their backing store changes from vector DB to markdown.

### Goal

Extract higher-level cross-story insights that the per-story memory pipeline
misses, detect recurring failure patterns, and close the feedback loop so
the `/ralph` skill generates better PRDs over time.

### Context for Builder

The memory pipeline (previously `internal/memory/pipeline.go` with ChromaDB,
now replaced by Phase 5.8's markdown memory) captures patterns, completions,
errors, and decisions. Per-story knowledge is now handled by Claude's
built-in auto-memory. Cross-story orchestration knowledge is written to
`.ralph/memory/` markdown files and injected by `BuildPrompt()`.

What's missing:
1. **Holistic analysis** — no step synthesizes cross-story insights after a
   full PRD run completes (e.g., "stories touching module X averaged 3.2
   iterations vs 1.4 elsewhere")
2. **Anti-pattern aggregation** — recurring failures are stored individually
   in `ralph_errors` but never grouped or surfaced as systemic issues
3. **Skill feedback** — the `/ralph` skill generates prd.json with static
   heuristics, never learning from execution outcomes

Run history (`internal/costs/`) provides quantitative data. Vector memory
(`internal/memory/`) provides qualitative data. This phase connects them.

### What to Build

#### 5a. Post-Run Synthesis

Add a post-run analysis step to the existing memory pipeline (extend
`internal/memory/pipeline.go` — no new package needed):

After a PRD run completes, invoke a fast model to analyze the full run
holistically:
- Which stories required retries and why?
- Which stories got stuck and what was the pattern?
- Which stories were rejected by judge and what was missing?
- What cross-story patterns emerged that individual story embeddings miss?

Output: synthesized lessons appended to `.ralph/memory/learnings.md`
(high-signal, cross-story insights only) and persisted for human review.
**Note (2026-03-26):** Phase 5.8 replaces this with Claude-based synthesis
writing to markdown files. The `ralph_lessons` ChromaDB collection and
Gemini dependency are removed.

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

**Key design rule: `ralph-prompt.md` stays static and hand-tuned. Lessons
are NEVER appended to it.** Lessons are injected dynamically at prompt build
time by reading `.ralph/memory/learnings.md` directly into the prompt. The
LLM performs relevance filtering natively — no vector search or token budget
needed with 1M context. Lessons gain confirmation counts as confirmed across
runs. The dream consolidation cycle (Phase 5.8d) handles dedup and pruning.

**Hygiene:** Dream consolidation (Phase 5.8d) replaces numeric decay.
Entries with zero confirmations are dropped after N runs. Duplicate entries
are merged. Files are kept under 50 entries each.

#### 5b. Anti-Pattern Detection

Analyse run history and memory files to detect recurring failure modes
across runs (previously queried `ralph_errors` and `ralph_completions`
ChromaDB collections — now reads from run history and learnings):
- Same file causing stuck detection repeatedly → flag as **fragile area**
- Same test failing across stories → flag as **flaky test**
- Same judge rejection reason → flag as **common oversight**
- Module-level iteration averages significantly above baseline → flag as
  **high-friction area**

**Surface in the TUI:** New section in the Usage tab showing detected
anti-patterns with occurrence counts and affected stories.

**Inject into prompts:** When a story touches files/modules flagged as
anti-patterns, include a targeted warning in `BuildPrompt()`:
```
⚠️ KNOWN ISSUE: internal/api/handler.go has caused stuck detection in 3
of the last 5 stories that modified it. Common root cause: test database
not running. Ensure test DB is available before running integration tests.
```

Anti-pattern data is derived from run history (`run-history.json`) and
memory files — no embedding or vector queries needed.

#### 5c. Skill Feedback Loop

The `/ralph` skill (`skills/ralph/SKILL.md`) generates prd.json files that
ralph then executes. Currently, the skill has no awareness of what works
well in practice — it uses static heuristics for story sizing, ordering,
and acceptance criteria.

**Close the loop:**

After post-run synthesis (5a), extract lessons specifically relevant to PRD
quality and append them to `.ralph/memory/prd-learnings.md` (previously
stored in `ralph_prd_lessons` ChromaDB collection):

- **Story sizing lessons**: "Stories touching `internal/api/` fail when they
  mix schema changes with endpoint logic — split by concern, not file count"
- **Criteria quality lessons**: "Judge rejects stories with 'handles errors
  gracefully' — use specific error scenarios instead"
- **Ordering lessons**: "UI stories that depend on server actions fail when
  the DAG marks them as independent — enforce stricter ordering"
- **Missing criteria patterns**: "Schema stories in this codebase should
  always include 'database migrations run cleanly' as a criterion"

**Skill integration:**

The skill itself (`SKILL.md`) stays static — same principle as
`ralph-prompt.md`. When the skill runs, it reads
`.ralph/memory/prd-learnings.md` and injects relevant lessons as
additional constraints before generating the PRD.

**Example injection into skill context:**
```
## Learned PRD Constraints (from previous ralph runs)
- Stories modifying internal/auth/ should split schema vs middleware vs UI
  concerns (confirmed 3 times)
- Always include "Tests pass" for stories touching internal/db/
  (confirmed 2 times)
- Stories mixing unrelated acceptance criteria (e.g., UI + API + schema)
  tend to produce lower quality — split by concern instead
  (confirmed 2 times)
```

**Hygiene:** Dream consolidation (Phase 5.8d) keeps `prd-learnings.md`
under 50 entries. These lessons accumulate slowly (one batch per PRD run)
so a smaller cap is appropriate.

### Acceptance Criteria

- [ ] Post-run synthesis generates cross-story lessons automatically after PRD completion
- [ ] Lessons are stored in `.ralph/memory/learnings.md` (was `ralph_lessons` ChromaDB collection)
- [ ] Relevant lessons are injected into prompts by reading markdown files directly
- [ ] Confirmation counts track lesson reliability across runs (dream consolidation prunes stale entries)
- [ ] Anti-patterns are detected from run history and memory files, surfaced in TUI Usage tab
- [ ] Anti-pattern warnings are injected into prompts for stories touching flagged areas
- [ ] PRD-quality lessons are stored in `.ralph/memory/prd-learnings.md` (was `ralph_prd_lessons` collection)
- [ ] `/ralph` skill reads `prd-learnings.md` before generating prd.json
- [ ] Generated PRDs reflect learned sizing, ordering, and criteria patterns

### Estimated Scope

~300-400 lines of Go code (reduced from original estimate — markdown
files are simpler than ChromaDB collections). One new analysis prompt
template. Modifications to runner (prompt injection), TUI (anti-pattern
display), and skill invocation. Two new markdown files in `.ralph/memory/`.

---

## Phase 5.5: Interactive Task Mode ✅ COMPLETE

**Impact: High | Complexity: Medium | Dependencies: Phase 4 (agent roles — already complete), Phase 1 (story state — already complete), Phase 5 (learning loop — already complete, learned context used for interactive tasks)**

> **Status: Complete** (completed 2026-03-23).
> All 11 stories passed (P55-001 through P55-011). Key deliverables:
> - `internal/interactive/` package — `StoryCreator` with atomic ID counter, `CreateStory`, `CreateAndAppend`, `SaveSession()`
> - `internal/tui/clarify.go` — lightweight Claude (Sonnet) clarification step: returns "READY" or up to 3 questions
> - Task input bar (textarea) visible in `phaseInteractive` and `phaseParallel`; Enter submits, Esc clears, Tab toggles focus
> - `phaseInteractive` phase constant added to TUI phase system; coexists with running workers
> - Clarification Q&A display and inline answer collection in TUI
> - `Coordinator.AddStory()` and `DAG.AddNode()` for dynamic story injection into the scheduling pipeline
> - Stories panel renders interactive tasks with lightning bolt marker and status labels (clarifying/queued/running/done/failed)
> - Interactive tasks included in checkpoint writes automatically; session files saved to `.ralph/session-{timestamp}.json` on exit
> - Config validation no longer fatals on missing `prd.json`; sets `NoPRD=true` and enters `phaseInteractive` directly
> - Task input works during `phaseParallel` with full clarification flow
>
> **P55-010 initially failed judge review** because `phaseParallel` task submission
> skipped clarification — fixed by wiring the same clarification flow for both phases.
>
> **Added 2026-03-23.** Ralph currently requires a pre-defined prd.json to
> operate. This phase makes prd.json optional and adds a persistent input
> bar to the TUI so users can inject tasks on the fly. Each task is
> independent (no DAG dependencies) and dispatched to the next available
> worker. This transforms Ralph from a batch executor into an interactive
> coding assistant that still leverages the full orchestration stack
> (workspace isolation, memory, judge, cost tracking).

### Goal

Allow users to dynamically add tasks to Ralph at any time — whether
starting from an empty session or injecting ad-hoc work alongside a
running PRD. Tasks entered interactively bypass the DAG and are dispatched
immediately to available workers.

### Context for Builder

The TUI already has an input mechanism — the "hint" field lets users send
guidance to stuck workers. The coordinator's `ScheduleReady()` polls for
stories with satisfied dependencies each tick. A story with no `dependsOn`
is immediately ready. The worker, memory, merge, and workspace systems are
all story-agnostic — they don't care whether a `UserStory` came from
prd.json or was created at runtime.

Key existing touch points:
- `cmd/ralph/main.go`: validates prd.json exists at startup
- `internal/tui/model.go`: TUI model, phase state machine
- `internal/tui/commands.go`: TUI command handlers
- `internal/coordinator/coordinator.go`: worker scheduling
- `internal/prd/prd.go`: PRD struct and story management

### What to Build

#### 5.5a. Make prd.json Optional

Relax validation in `cmd/ralph/main.go`:
- If no prd.json is provided, start with an empty `PRD` struct (project
  name defaults to the current directory name, empty story list)
- Skip DAG analysis phase when starting without a PRD
- Skip archive phase when there's nothing to archive
- The TUI starts in an "interactive" state with the input bar focused

When a prd.json IS provided, behavior is unchanged — stories load and
execute as normal, but the input bar is still available.

#### 5.5b. Task Input Bar

Add a persistent text input to the TUI (below the stories panel or as a
dedicated input area):
- Always visible, always accepting input regardless of current phase
- User types a task description and hits Enter
- Input is cleared and the task enters the clarification flow (5.5c)
- The hint input field remains separate — hints go to running workers,
  tasks go to the coordinator

Key bindings:
- `Tab` or a dedicated key to toggle focus between task input and hint input
- `Enter` submits the task
- `Esc` clears the input without submitting

#### 5.5c. Clarification Step

Before dispatching, run a lightweight Claude call to assess whether the
task description is sufficient:

1. Send the user's one-liner to Claude with a prompt like:
   "Given this task and the current codebase, do you have any clarifying
   questions? If the task is clear enough to proceed, respond with READY.
   Otherwise, list up to 3 specific questions."
2. If Claude responds with READY, proceed directly to dispatch (5.5d)
3. If Claude returns questions, display them in the TUI below the input bar
4. User types answers inline (each answer submitted with Enter)
5. Once all questions are answered, bundle the original task + Q&A into
   the story description and dispatch

The clarification prompt should include:
- The user's task description
- A brief codebase summary (project name, key directories — reuse what
  `BuildPrompt()` already assembles for project context)
- Any currently running/completed stories (for awareness of in-flight work)

This is similar to the architect phase but lighter weight and interactive.
Use Sonnet for speed.

#### 5.5d. Dynamic Story Creation & Dispatch

On submission (after clarification):
1. Mint a new `UserStory` with auto-incrementing ID (e.g., `T-001`,
   `T-002` — "T" prefix to distinguish from PRD story IDs)
2. Title: first ~80 chars of the user's input (or a Claude-generated
   summary from the clarification step)
3. Description: full input + any Q&A from clarification
4. `DependsOn`: empty (no DAG edges)
5. `Priority`: 0 (immediately ready)
6. Append to `prd.UserStories`
7. `ScheduleReady()` picks it up on the next tick — no DAG edges means
   immediately schedulable

The story flows through the existing worker pipeline: workspace creation,
`BuildPrompt()` with memory context, implementer execution, optional
judge, merge back.

#### 5.5e. TUI Updates

**Stories panel**: grows dynamically as tasks are added. Interactive tasks
show with a distinct marker:

```
⚡ T-001: fix login button not responding on mobile    ⠋ running
⚡ T-002: add rate limiting to /api/upload endpoint    ⏳ clarifying
✅ P5-001: Implement auth middleware                   (from PRD)
✅ P5-002: Add user settings page                      (from PRD)
⚡ T-003: refactor database connection pooling         ✅ done
```

**Clarification display**: when a task is in the clarification step, show
the questions and answer input in the context panel or a dedicated area.

**Phase handling**: the TUI needs a phase that supports "idle but accepting
input." Currently the phase machine is linear (init → ... → done). Add
an `phaseInteractive` that runs alongside other phases — it's not a
sequential step but a persistent capability.

#### 5.5f. Session Persistence

- Interactive tasks are appended to `prd.UserStories`, so they benefit
  from the existing checkpoint system
- If Ralph is interrupted and resumed, in-progress interactive tasks
  are restored from the checkpoint like any other story
- On clean completion of all tasks, optionally save the session's stories
  to a file for reference (e.g., `.ralph/session-{timestamp}.json`)

### Acceptance Criteria

- [x] Ralph starts successfully without a prd.json, presenting an empty TUI with input bar
- [x] Tasks can be typed and submitted via the input bar at any time
- [x] Clarification step fires before dispatch, asking questions if the task is ambiguous
- [x] User can answer clarification questions inline in the TUI
- [x] Submitted tasks are created as `UserStory` structs and scheduled immediately
- [x] Tasks execute through the full worker pipeline (workspace, Claude, merge)
- [x] Stories panel updates dynamically as tasks are added and progress
- [x] Interactive tasks work alongside PRD stories when prd.json is provided
- [x] Interactive tasks are included in the checkpoint for crash recovery
- [x] Multiple tasks can run in parallel when `--workers N > 1`
- [x] Memory system works with interactive tasks (learned context injected into prompts)

### Estimated Scope

~400-600 lines of Go code. Modifications to main.go (optional PRD),
model.go (input bar, phase handling), commands.go (clarification flow,
story creation), coordinator.go (minor — scheduling already handles this).
One new prompt template for the clarification step. No new packages needed.

---

## Phase 5.7: Run History Observability

**Impact: Medium | Complexity: Low | Dependencies: Phase 3 (usage tracking — already complete), Phase 5 (learning loop — already complete)**

> **Added 2026-03-25.** Run history (`run-history.json`) was missing critical
> metrics needed to compare model performance across runs. TotalCost was
> hardcoded to zero, first-pass rate was computed at runtime but never
> persisted, model names were tracked in TokenUsage but not surfaced, and
> per-story iteration counts were lost (only the average survived). This
> phase fixes all of these gaps as a **prerequisite for Phase 6** — we need
> baseline metrics on Opus before switching roles to Sonnet/Haiku.

### Goal

Persist runtime metrics that are already computed but not saved to
`run-history.json`, and surface them in the `ralph history` CLI. This
establishes a baseline for comparing model performance before and after
Phase 6 (Multi-Model Orchestration).

### Context for Builder

The `RunCosting` struct (`internal/costs/costs.go`) already tracks per-story
token usage, model names, and costs at runtime. The `PlanQuality` struct
in the coordinator already computes first-pass rate. None of this data
was making it into `RunSummary` in `internal/costs/history.go` — the
`persistRunHistory()` function in `model.go` was constructing summaries
with `TotalCost: 0` and missing fields.

### What to Build

#### 5.7a. Extend RunSummary

Add to `internal/costs/history.go`:
- `StorySummary` struct: StoryID, Title, Iterations, Passed, JudgeRejects, Model
- New fields on `RunSummary`: FirstPassRate, ModelsUsed, TotalInputTokens,
  TotalOutputTokens, CacheHitRate, StoryDetails

#### 5.7b. Wire persistRunHistory()

Fix `internal/tui/model.go` `persistRunHistory()` to populate:
- TotalCost from `m.runCosting.GetTotalCost()`
- Token counts and cache hit rate from RunCosting snapshot
- ModelsUsed from distinct models across all iterations
- FirstPassRate from coordinator's PlanQuality (parallel) or events (serial)
- Per-story details from PRD + RunCosting + event judge data

#### 5.7c. Enhance CLI Display

Update `cmd/ralph/main.go` `printHistory()` to show two new columns:
- **1ST PASS**: FirstPassRate as percentage
- **MODEL**: Primary model name (short form) or "mixed" if multiple

### Acceptance Criteria

- [x] RunSummary includes FirstPassRate, ModelsUsed, token counts, CacheHitRate, StoryDetails
- [x] persistRunHistory() populates all new fields from runtime data
- [x] `ralph history` shows 1ST PASS and MODEL columns
- [x] Old run-history.json files (without new fields) load without error
- [x] Unit tests cover new fields round-trip and backward compatibility

### Estimated Scope

~100 lines of Go code. Modifications to history.go (struct), model.go
(persistence), main.go (display). One new test file update.

---

## Phase 5.8: Markdown Memory — Replace Vector DB with Dream-Based Consolidation ✅ COMPLETE

**Impact: High (simplification) | Complexity: Medium | Dependencies: Phase 2 (supersedes), Phase 5 (supersedes memory portions)**

> **Status: Implemented** (completed 2026-03-26).
> Phase 5.8 has been fully implemented. The entire ChromaDB/Voyage AI vector
> memory system has been replaced with a lightweight markdown-based memory
> system (~300 lines replacing ~2600). Key deliverables:
> - `internal/memory/` reduced to `files.go`, `synthesis.go`, `dream.go`, `size.go`, `types.go`
> - Post-run synthesis writes cross-story lessons to `.ralph/memory/learnings.md`
> - PRD quality lessons written to `.ralph/memory/prd-learnings.md`
> - `BuildPrompt()` injects memory file contents into worker prompts
> - Dream consolidation runs automatically every N runs (default: 5) or manually via `ralph memory consolidate`
> - Size monitoring warns when memory files exceed thresholds (50k/150k tokens)
> - `ralph memory` subcommands updated: `stats`, `consolidate`, `reset`
> - ChromaDB, Voyage AI, and Python/conda dependencies fully removed
> - All vector DB code (`client.go`, `embedder.go`, `retrieval.go`, `sidecar.go`, `scanner.go`, `maintenance.go`, `setup.go`, `pipeline.go`) deleted
>
> **Key insight:** Ralph's workers ARE Claude Code instances. They already
> have auto-memory natively. The only knowledge that requires custom handling
> is orchestration-level cross-story insights that no single worker sees —
> specifically post-run synthesis and PRD quality lessons. Everything else
> (per-story patterns, error/resolution pairs, codebase navigation) is
> handled by the workers' built-in auto-memory.

### Goal

Replace the ChromaDB/Voyage AI vector memory system with a lightweight
markdown-based memory that leverages LLM-native comprehension for relevance
filtering and a periodic dream/consolidation cycle for maintenance.

### Context for Builder

The current memory system (`internal/memory/`) consists of:
- `client.go` (529 lines) — ChromaDB REST API wrapper
- `embedder.go` (209 lines) — Voyage AI embedding client
- `retrieval.go` (311 lines) — Vector search with ranking
- `pipeline.go` (416 lines) — Extraction and embedding pipeline
- `scanner.go` (379 lines) — AST-based codebase indexing
- `sidecar.go` (181 lines) — ChromaDB subprocess management
- `maintenance.go` (144 lines) — Numeric confidence decay
- `synthesis.go` — Post-run lesson generation via Gemini
- `setup.go` — Python/conda environment setup
- `types.go` (180 lines) — Data structures and 7 collections
- `commands.go` (254 lines) — CLI commands

This system manages 7 ChromaDB collections (patterns, completions, errors,
decisions, codebase, lessons, prd_lessons) with a total cap of ~950
documents. It requires a ChromaDB sidecar process on port 9876, a Python
environment (conda or venv), and Voyage AI API access for embeddings.

Key integration points:
- `internal/worker/worker.go`: Workers receive `ChromaClient` and `Embedder`
  from coordinator, build memory-augmented prompts
- `internal/runner/runner.go`: `BuildPrompt()` accepts `Memory` retriever
  and `MemoryOpts` for semantic injection
- `internal/coordinator/coordinator.go`: `SetMemory()` configures memory
  for all workers
- `internal/config/config.go`: `MemoryConfig` with TopK, MinScore,
  MaxTokens, Disabled, Port

### What to Build

#### 5.8a. Markdown Memory Files

Replace 7 ChromaDB collections with 2 structured markdown files in
`.ralph/memory/`:

```
.ralph/memory/
├── learnings.md       # cross-story lessons from post-run synthesis
└── prd-learnings.md   # PRD quality lessons (sizing, ordering, criteria)
```

**Why only 2 files, not 7:**
- `ralph_patterns`, `ralph_errors`, `ralph_completions`, `ralph_decisions`
  — Per-story knowledge that Claude's built-in auto-memory captures
  natively. Each worker is a Claude Code instance; it saves relevant
  patterns, errors, and decisions to its own auto-memory during execution.
- `ralph_codebase` — Redundant. Workers have the codebase right there and
  can read files directly. Embedding Go AST summaries so an LLM can search
  them is unnecessary when the LLM can just `Read` the files.
- `ralph_lessons` → `learnings.md` — Cross-story insights that only the
  orchestration layer sees. This is the one thing auto-memory can't capture
  because no single Claude instance has the full-run view.
- `ralph_prd_lessons` → `prd-learnings.md` — PRD quality feedback for the
  `/ralph` skill.

**Entry format** (structured markdown with metadata headers):

```markdown
### lesson-2026-03-26-auth-retry
- **Run:** feature-auth (2026-03-26)
- **Stories:** P55-003, P55-007
- **Confirmed:** 2 times
- **Category:** testing

When auth tokens expire mid-request, retry with backoff rather than
failing the story. The token refresh endpoint has a 2s cold-start delay.
Always include `ensure auth token is fresh` in implementation plans for
stories touching internal/auth/.
```

#### 5.8b. Post-Run Synthesis (Write Path)

After a PRD run completes, Ralph spawns a Claude instance (not Gemini —
removing that dependency) to analyse the full run and write structured
entries to the memory files.

The synthesis prompt receives:
- Summary of all stories: ID, title, status, iteration count, judge results
- Events from the run (stuck detections, errors, judge rejections)
- Existing contents of `learnings.md` and `prd-learnings.md`

The synthesis Claude call:
1. Analyses cross-story patterns the individual workers couldn't see
2. Appends new entries to `learnings.md` (general lessons) and
   `prd-learnings.md` (PRD quality lessons)
3. Updates confirmation counts on existing entries that were re-confirmed
4. Does NOT modify existing entries beyond confirmation bumps — that's
   the dream cycle's job

**Implementation:** Create `internal/memory/synthesis.go` (~100 lines).
Reuse the existing `runner.RunClaude()` infrastructure with a synthesis
prompt template at `prompts/synthesis.md`.

#### 5.8c. Prompt Injection (Read Path)

Modify `BuildPrompt()` in `internal/runner/runner.go`:
- Read `.ralph/memory/learnings.md` and `.ralph/memory/prd-learnings.md`
- Inject contents as a `## Learned Context` section in the prompt
- No embedding, no query, no scoring — the LLM reads the full files and
  determines relevance itself

With consolidation keeping files pruned (5.8d), both files should stay
well under 50k tokens combined — trivial within a 1M context window.

#### 5.8d. Dream Consolidation Cycle

Every N runs (configurable, default 5), Ralph spawns a Claude instance
to consolidate the memory files. Adapted from Claude Code's Auto Dream
system prompt:

```
# Dream: Memory Consolidation

You are performing a dream — a reflective pass over Ralph's memory files.
Consolidate accumulated learnings into durable, well-organized memories
so future runs benefit from cleaner, more relevant context.

Memory directory: {.ralph/memory/}

## Phase 1 — Orient

- Read learnings.md and prd-learnings.md
- Understand current entries, categories, and confirmation counts

## Phase 2 — Gather Signal

- Review the last {N} run summaries from .ralph/run-history.json
- Identify which existing learnings were confirmed or contradicted
- Note any new patterns not yet captured

## Phase 3 — Consolidate

For each memory file, produce an updated version that:
- Merges duplicate or overlapping lessons into single entries
- Removes lessons contradicted by more recent evidence
- Drops lessons with zero confirmations older than {M} runs (default: 10)
- Updates confirmation counts based on recent run evidence
- Converts any relative dates to absolute dates
- Preserves high-confidence, repeatedly-confirmed lessons

## Phase 4 — Prune

- Keep each file under {max_entries} entries (default: 50)
- If over limit, drop lowest-confirmation entries first
- Ensure entries remain well-categorised and clearly written

Return a brief summary of what you consolidated, updated, or pruned.
```

**Implementation:** Create `internal/memory/dream.go` (~100 lines).
Track run count in `.ralph/run-meta.json` (increment after each run,
reset after consolidation). The dream runs as a Claude invocation via
`runner.RunClaude()`.

**Manual trigger:** `ralph memory consolidate` command.

#### 5.8e. Size Warning System

Track memory file sizes and warn when they grow too large:

```go
const (
    WarnTokenThreshold = 50_000  // ~200KB — consolidation should prevent this
    CritTokenThreshold = 150_000 // ~600KB — something is wrong
)
```

On each run start, check combined size of `.ralph/memory/` files:
- **Above warn:** Log warning in TUI — "Memory files are large ({X}
  tokens). Run `ralph memory consolidate` or check dream cycle."
- **Above crit:** Log error — "Memory files exceed {X} tokens. This may
  degrade worker quality. Run `ralph memory consolidate` or
  `ralph memory reset`."

This prevents silent quality degradation — if memory grows unchecked,
you'll see it immediately rather than debugging mysterious regressions
months later.

**Implementation:** Create `internal/memory/size.go` (~50 lines). Check
runs at TUI startup and before `BuildPrompt()`.

#### 5.8f. Delete Vector DB Infrastructure

Remove the following files entirely:
- `internal/memory/client.go` — ChromaDB REST wrapper
- `internal/memory/embedder.go` — Voyage AI client
- `internal/memory/retrieval.go` — Vector search + ranking
- `internal/memory/sidecar.go` — ChromaDB subprocess management
- `internal/memory/scanner.go` — AST-based codebase indexing
- `internal/memory/maintenance.go` — Numeric decay cycle
- `internal/memory/setup.go` — Python/conda environment setup
- `internal/memory/pipeline.go` — Extraction and embedding pipeline
- `memory/chroma/` — HNSW vector index data
- `memory/conda-env/` or `memory/.venv/` — Python environment

Update the following:
- `internal/memory/types.go` — Remove `Document`, `Collection`,
  `QueryResult` types and all collection definitions. Replace with
  simple `LearningEntry` struct.
- `internal/memory/commands.go` — Replace `stats`/`search`/`prune`/`reset`
  with `consolidate`/`stats`/`reset` commands.
- `internal/worker/worker.go` — Remove `ChromaClient` and `Embedder`
  fields from Worker struct. Remove memory retriever construction.
- `internal/runner/runner.go` — Replace `Memory` retriever interface in
  `BuildPromptOpts` with direct file reading.
- `internal/coordinator/coordinator.go` — Remove `SetMemory()` method.
- `internal/config/config.go` — Replace `MemoryConfig` (TopK, MinScore,
  MaxTokens, Port) with simplified config (Disabled, DreamEveryNRuns,
  MaxEntries, WarnTokenThreshold).
- `go.mod` / `go.sum` — Remove any ChromaDB or Voyage AI dependencies.

#### 5.8g. CLI Commands

Update `ralph memory` subcommand:

- `ralph memory stats` — Show file sizes, entry counts, last
  consolidation date, runs since last consolidation
- `ralph memory consolidate` — Manually trigger dream cycle
- `ralph memory reset` — Clear all memory files (with confirmation)

### Technical Decisions

**Why markdown over a simpler JSON file:**
- Human-readable and editable — you can manually review and curate
  learnings by opening the file
- Naturally structured with headers and metadata
- Claude comprehends markdown natively — no parsing layer needed
- Diffs are meaningful in version control (even though `.ralph/` is
  gitignored, the format supports it if desired)

**Why Claude for synthesis instead of Gemini:**
- Removes the Gemini CLI dependency, which has been unreliable
- Ralph already spawns Claude instances — no new infrastructure
- Claude's comprehension of its own memory format is naturally better
- Consistent model family across the entire pipeline

**Why no index file:**
- With only 2 topic files and consolidation keeping them under 50 entries
  each, an index adds complexity without value
- If files grow beyond expectations, the size warning system (5.8e) will
  surface it before quality degrades
- An index can be added later if needed — it's additive, not architectural

**What about per-worker auto-memory:**
- Ralph's workers are Claude Code instances with built-in auto-memory
- Per-story patterns, errors, and decisions are captured natively by
  each worker during execution
- Workers sharing the same project directory share auto-memory files
- Auto-dream consolidates worker memories periodically
- This phase focuses only on orchestration-level knowledge that no single
  worker has — the gap auto-memory can't fill

### Acceptance Criteria

- [x] ChromaDB, Voyage AI, and Python environment dependencies fully removed
- [x] `internal/memory/` reduced from ~2600 lines to ~300 lines
- [x] Post-run synthesis writes cross-story lessons to `.ralph/memory/learnings.md`
- [x] PRD quality lessons written to `.ralph/memory/prd-learnings.md`
- [x] `BuildPrompt()` injects memory file contents into worker prompts
- [x] Dream consolidation runs automatically every N runs (default: 5)
- [x] Dream cycle merges duplicates, drops stale entries, updates confirmations
- [x] `ralph memory consolidate` manually triggers dream cycle
- [x] `ralph memory stats` shows file sizes, entry counts, consolidation status
- [x] Size warning emitted when memory files exceed warn threshold (50k tokens)
- [x] Size error emitted when memory files exceed critical threshold (150k tokens)
- [x] `/ralph` skill continues to read learned PRD lessons from `prd-learnings.md`
- [x] Worker struct no longer carries ChromaClient or Embedder
- [x] Existing tests updated or replaced to cover new memory system
- [x] `make build` succeeds with all vector DB code removed

### Estimated Scope

~300 lines of new Go code across 4 files (`files.go`, `synthesis.go`,
`dream.go`, `size.go`). ~2600 lines deleted. One new prompt template
(`prompts/synthesis.md`). Modifications to worker, runner, coordinator,
config, and TUI. Net reduction of ~2300 lines and 3 external dependencies.

---

## Phase 6: Multi-Model Orchestration (Revised) ✅ COMPLETE

**Impact: Medium | Complexity: Low | Dependencies: Phase 4 (agent roles — already complete), Phase 5.7 (run history observability — baseline metrics)**

> **Status: Implemented** (completed 2026-03-26 on branch `ralph/phase6-multi-model`).
> Phase 6 has been fully implemented. Key deliverables:
> - `RunClaudeOpts.Model` wired through to `--model` flag on claude CLI invocation
> - Default models set per role: Opus for Architect/Debugger, Sonnet for Implementer/Reviewer
> - CLI override flags: `--model` (all roles), `--architect-model`, `--implementer-model`, `--utility-model`
> - Worker resolves model per role with precedence: role-specific flag > global override > role default
> - DAG analysis and utility tasks use Haiku by default
> - Per-iteration model tracked in run history and displayed in TUI and `ralph history`
> - DAG tree visualization added to TUI stories panel with box-drawing connectors
>
> **Revision note (2026-03-19):** Original framing was cost optimization
> ("target: 30-50% reduction"). The user is on Claude Max subscription, so
> per-token cost is irrelevant. Reframed as speed + quality allocation:
> Opus for planning/debugging (quality), Sonnet for implementation (speed),
> Haiku for utility tasks (speed). Also note that multi-model is partially
> in place already — judge uses Gemini via `internal/exec/gemini.go`. The
> complexity heuristic (6b original) has been dropped — just map roles to
> tiers directly. Simpler, easier to reason about, and avoidable if it
> doesn't measurably help.

### Goal

Use the right model for each agent role — high-quality models for planning
and debugging, fast models for implementation and utility tasks. Optimize
for speed and output quality, not cost.

### Context for Builder

Currently, Claude is invoked via `internal/runner/runner.go` which calls the
`claude` CLI. The model is not configurable per-invocation — all Claude
calls use the same default model. Gemini is already used for judge and
autofix via `internal/exec/gemini.go`, so multi-model is partially in place.

The roles system (`internal/roles/roles.go`) already defines `AgentConfig`
per role with `Model string` field — it just isn't wired through to the
CLI invocation yet.

### What to Build

#### 6a. Role-to-Model Mapping

Extend `internal/roles/roles.go` default configs:

| Role | Default Model | Rationale |
|------|--------------|-----------|
| Architect | `opus` | Planning benefits from deeper reasoning |
| Implementer | `sonnet` | Fast execution of a known plan |
| Debugger | `opus` | Root cause analysis needs depth |
| Reviewer | `sonnet` | Mechanical lens-based review |

Add to `internal/config/`:
- `--model <name>` — override model for all roles (existing behavior)
- `--architect-model <name>` — override for architect only
- `--implementer-model <name>` — override for implementer only

#### 6b. Fast Model for Utility Tasks

Use Haiku-class models for tasks that don't need full Claude:
- DAG analysis (`internal/dag/`)
- Post-run lesson extraction (Phase 5)
- Plan validation (Phase 4)

DAG analysis currently runs full Claude CLI to analyze story dependencies.
This is a structured output task (JSON array) that Haiku handles well.

#### 6c. Runner Integration

Modify `internal/runner/runner.go`:
- `RunClaude()` already accepts options — add `Model string` to the options
- Pass `--model` flag to the `claude` CLI invocation when set
- Track model used per iteration in usage tracking (Phase 3)
- The `AgentConfig.Model` field in roles already exists — wire it through

#### 6d. DAG Tree Visualization

Add tree-based story visualization to the TUI stories panel:
- Render stories as a directory-tree structure using box-drawing connectors (│, ├─, └─)
- Root nodes are stories with no dependencies; dependents nest under parents
- Preserves all existing display info (status icons, role badge, iteration, worker, elapsed time)
- Falls back to flat chain for serial mode

### Acceptance Criteria

- [x] Model is configurable per agent role via roles.go defaults
- [x] `--model`, `--architect-model`, `--implementer-model` CLI flags work
- [x] Utility tasks (DAG analysis, plan validation) use fast tier
- [x] Model used is tracked per iteration in usage data
- [x] Quality does not regress (judge pass rate stays stable or improves)
- [x] Speed improvement is measurable for implementation iterations
- [x] TUI stories panel renders DAG as tree with box-drawing connectors

### Estimated Scope

~200-300 lines of Go code. Modifications to runner, config, and roles.
Straightforward wiring — the role system already has the Model field.

---

## Phase 7: MCP Tool Server (Revised — Scoped Down)

**Impact: Medium | Complexity: Medium | Dependencies: Phase 5.8 (markdown memory), Phase 1 (story state — already complete)**

> **Revision note (2026-03-19):** With 1M context, `BuildPrompt()` already
> injects memory, story state, PRD context, and events effectively. The
> original justification — letting agents pull context on-demand because
> context was scarce — no longer applies. This revision scopes MCP to the
> capabilities that static prompt injection **cannot** provide: real-time
> sibling status, blocker signaling, and live pattern reporting. Tools like
> `ralph_query_memory` and `ralph_get_story_plan` are redundant with what
> `BuildPrompt()` already injects and have been cut.

### Goal

Enable real-time coordination between ralph and its child Claude instances
during parallel execution. Agents can check live sibling status, signal
blockers, and push pattern discoveries without waiting for story completion.

### Context for Builder

MCP (Model Context Protocol) allows Claude Code to connect to external tool
servers. Claude Code supports MCP servers via its configuration or CLI flags.
If ralph runs an MCP server, each Claude instance can connect to it.

Currently, Claude instances are launched by `internal/runner/runner.go` via
the `claude` CLI. All context is injected at prompt build time — agents
have no way to check what happened *after* their prompt was assembled.

### What to Build

#### 7a. MCP Server

Create `internal/mcp/` package implementing an MCP tool server:

**Tools to expose (focused set — 3 tools, not 7):**

```
ralph_get_sibling_status(story_id?: string)
  → Returns live status of all stories (or a specific one): status,
    files touched, current iteration, completion state. This is the
    key capability that prompt injection can't provide — the status
    may have changed since the prompt was built.

ralph_report_blocker(description: string, blocked_by_story: string)
  → Agent signals it's blocked on another story's output. Coordinator
    pauses the worker and resumes when the blocking story completes.
    Enables dynamic dependency resolution beyond the static DAG.

ralph_report_pattern(pattern: string, category: string)
  → Agent proactively pushes a discovered pattern for immediate
    append to `.ralph/memory/learnings.md`. Currently patterns are
    only extracted on story *completion* — this enables real-time
    sharing between parallel workers mid-execution. Other workers
    pick up new entries on their next `BuildPrompt()` cycle.
```

#### 7b. Server Lifecycle

- Ralph starts the MCP server on a random localhost port at startup
- Port is passed to each Claude invocation via MCP configuration
- Server shuts down cleanly on ralph exit
- Server handles concurrent requests (multiple parallel workers)
- Use a Go MCP SDK (e.g., `github.com/mark3labs/mcp-go`) to avoid
  implementing the protocol from scratch

#### 7c. Claude Integration

Modify `internal/runner/runner.go`:
- Write a temporary MCP config file per worker that points to ralph's
  MCP server, or pass via `--mcp-config` CLI flag
- MCP config includes the server's localhost URL and available tools

Modify prompt templates (minimal additions):
- Inform agents that `ralph_get_sibling_status` is available for checking
  if a dependency has completed mid-execution
- Inform agents that `ralph_report_blocker` is available if they discover
  an unexpected dependency at runtime

#### 7d. Blocker Coordination

When an agent reports a blocker via `ralph_report_blocker`:
- Coordinator checks if the blocking story is in progress
- If so, the blocked worker is paused (not killed) and resumed when the
  blocking story completes
- This enables finer-grained parallel coordination than the DAG alone
- If the blocking story is already complete, return immediately with its
  status so the agent can proceed

### Acceptance Criteria

- [ ] MCP server starts and stops cleanly with ralph lifecycle
- [ ] Claude instances can check live sibling story status via MCP
- [ ] Blocker coordination pauses/resumes workers dynamically
- [ ] Pattern reporting from agents appends to memory files in real-time
- [ ] MCP server handles concurrent requests from parallel workers safely
- [ ] Agents use MCP tools appropriately (not excessively)

### Estimated Scope

~500-700 lines of Go code. Use an existing Go MCP SDK for protocol handling.
Modifications to runner (MCP config injection) and coordinator (blocker
handling).

---

## Phase 8: Codebase Knowledge Graph (Revised — Evidence-Gated)

**Impact: Medium | Complexity: Medium-High | Dependencies: Phase 7 (MCP for exposing graph queries)**

> **Note:** Phase 7.5 (Activate `ralph_codebase`) was added on 2026-03-19
> but removed the same day after audit confirmed that `ralph_codebase` was
> fully operational. **Update (2026-03-26):** The `ralph_codebase` ChromaDB
> collection is removed by Phase 5.8. With 1M context, workers can read
> codebase files directly — semantic search over AST summaries is redundant.
> The knowledge graph's value proposition is now purely **structural
> relationships** (imports, calls, implements) that neither file reading
> nor markdown memory can provide.

> **Revision note (2026-03-19/2026-03-26):** The original justification
> referenced the `ralph_codebase` vector collection. With Phase 5.8
> removing all vector DB infrastructure, the gating question changes: do
> architect agents need structural relationship data (import chains, call
> graphs, interface implementations) that they can't get by reading the
> codebase directly? Gate on evidence from actual runs — if architects are
> missing ripple effects, this phase fills the gap.

### Goal

Build a lightweight structural graph of the codebase that captures
relationships vector search can't represent — import chains, call graphs,
interface implementations, and reverse dependencies. The architect agent
uses this for impact analysis.

### Context for Builder

With 1M context, workers can read codebase files directly, and Claude's
auto-memory captures codebase patterns natively. But neither can answer
structural queries like "what depends on this interface?" or "if I change
this function's signature, what breaks?" The knowledge graph fills this gap.

### What to Build

#### 8a. Graph Schema

Store in SQLite (`.ralph/knowledge.db`):

```sql
CREATE TABLE entities (
    id TEXT PRIMARY KEY,
    kind TEXT,        -- 'package', 'file', 'function', 'type', 'interface'
    name TEXT,
    file_path TEXT,
    signature TEXT    -- for functions/types
);

CREATE TABLE relationships (
    source_id TEXT,
    target_id TEXT,
    kind TEXT,        -- 'imports', 'calls', 'implements', 'depends_on',
                      -- 'modified_by_story', 'tests'
    FOREIGN KEY (source_id) REFERENCES entities(id),
    FOREIGN KEY (target_id) REFERENCES entities(id)
);
```

Note: With Phase 5.8, the `ralph_codebase` ChromaDB collection no longer
exists. The graph stores purely structural data — relationships that
can't be derived from reading individual files.

#### 8b. Graph Builder

Create `internal/knowledge/` package:

- **Static analysis pass**: Parse Go AST to extract packages, files,
  functions, types, imports, and call relationships. Deterministic and fast.
- **Story tracking**: When a story completes, add `modified_by_story` edges
  from the story to all files it touched.
- Run on first launch (full build), incrementally after story completion,
  and via `ralph knowledge rebuild` for manual refresh.

#### 8c. Query Interface

Expose via MCP (Phase 7) as an additional tool:

```
ralph_get_impact_analysis(files: []string)
  → Given files to modify, returns all entities that depend on them
    (reverse dependency walk). Helps architect anticipate ripple effects.
```

Also inject structural context into `BuildPrompt()` for architect role:
compact summary of the target module's imports, dependents, and interfaces.

### Acceptance Criteria

- [ ] Knowledge graph is built from Go AST analysis on first run
- [ ] Graph updates incrementally after story completions
- [ ] Impact analysis query returns reverse dependencies correctly
- [ ] Architect agent receives structural context in prompts
- [ ] Architect plans show measurably better awareness of ripple effects

### Estimated Scope

~800-1000 lines of Go code. Go AST parsing, SQLite schema, MCP tool
addition. The graph handles only structural relationships — codebase
comprehension is handled natively by workers reading files with 1M context.

---

## Phase 8.5: Deep Code Review Mode

**Impact: Medium-High | Complexity: Medium | Dependencies: Phase 8 (knowledge graph for structural context), Phase 4 (agent roles — already complete)**

> **Added 2026-03-24.** Ralph currently only operates in "implementation mode"
> — stories describe what to build, and the pipeline is architect → implementer
> → judge → quality review. This phase adds a read-only **deep code review**
> mode that reuses the same loop infrastructure to perform meticulous,
> file-by-file and architectural reviews of existing code without making
> any changes.
>
> This is distinct from the existing quality review gate (which runs
> post-implementation with narrow lens-focused checks). Deep review is a
> first-class operating mode where each PRD story defines a **review focus
> area** and the output is a structured review document.
>
> **Why after Phase 8:** The knowledge graph provides structural codebase
> awareness (dependency graphs, import chains, module boundaries) that makes
> architectural reviews significantly more useful. Without it, the reviewer
> is limited to what it can discover via Read/Grep/Glob in a single Claude
> session. Phase 8's impact analysis queries enable the reviewer to trace
> ripple effects and coupling that would otherwise be invisible.

### Goal

Add a `--deep-review` CLI mode that transforms the Ralph pipeline into a
read-only code review system. Stories define areas to review instead of
features to build. The agent explores code deeply, analyses architecture,
and produces structured review documents with severity-tagged findings.

### Context for Builder

The existing role system (`internal/roles/roles.go`) already supports
multiple agent specialisations. The architect role demonstrates the pattern
for a read-only agent — it explores the codebase and produces a plan without
writing code. Deep review follows the same pattern but produces review
findings instead of implementation plans.

The quality review system (`internal/quality/quality.go`) demonstrates
structured findings output — JSON findings with severity, file, line,
description, and suggestion fields parsed from `<findings>` tags. Deep
review follows a similar output pattern but uses `<review>` tags with
richer markdown content.

### What to Build

#### 8.5a. Config Flag and Role

Add `DeepReview bool` to `internal/config/config.go`. When set:
- Auto-disable: `JudgeEnabled = false`, `QualityReview = false`, `NoArchitect = true`
- Require prd.json to exist (no plan-file mode)
- Mutually exclusive with `--idle`

Add `RoleDeepReviewer Role = "deep-reviewer"` to `internal/roles/roles.go`:
```go
case RoleDeepReviewer:
    return AgentConfig{
        Role:       RoleDeepReviewer,
        PromptFile: "prompts/deep-reviewer.md",
        MaxTokens:  32000,
    }
```

#### 8.5b. Deep Reviewer Prompt (`prompts/deep-reviewer.md`)

Read-only reviewer agent instructions:
- **Tools allowed:** Read, Grep, Glob only. MUST NOT use Write, Edit, or Bash.
- **Process:** Read story to understand review scope → explore all relevant
  files broadly → analyse architecture, code quality, potential bugs, tech
  debt, security, performance, testing gaps, coupling → produce structured
  review in `<review>` tags.
- **Knowledge graph integration:** When Phase 8's knowledge graph is
  available, query it for dependency/import context, reverse dependencies,
  and interface implementations to inform architectural analysis.
- **Output format (inside `<review>` tags):**
  ```markdown
  # Deep Review: [Story ID] - [Title]

  ## Executive Summary
  One paragraph overview of the area reviewed and key findings.

  ## File-by-File Analysis
  ### `path/to/file.go`
  - [critical] Description of critical issue (line X)
  - [warning] Description of warning (line Y)
  - [info] Observation (line Z)

  ## Architectural Observations
  - Pattern analysis, coupling, cohesion, dependency concerns.

  ## Recommendations
  1. [critical] Highest priority items
  2. [warning] Medium priority items
  3. [info] Nice-to-haves
  ```
- **Story state:** Write state.json with status "complete", update
  progress.md with review summary.

#### 8.5c. Review Worker (`internal/worker/review.go`)

Simplified worker function `RunReview(w *Worker, cfg *config.Config, updateCh chan<- WorkerUpdate)`:

1. Send `WorkerSetup` state
2. **No workspace creation** — run directly in `cfg.ProjectDir` (reviews
   are read-only, no merge conflicts possible)
3. Load PRD, build prompt with `RoleDeepReviewer` via `runner.BuildPrompt()`
4. Append parallel-mode stop instruction via `appendParallelMode()`
5. Call `runner.RunClaude()` against the project directory
6. Parse `<review>` tags from activity log
7. Write parsed content to `.ralph/stories/{id}/review.md`
8. Mark story passed in PRD (reviews always pass)
9. Send `WorkerDone` with `Passed: true`, empty `ChangeID` (no commit)
10. No judge, no workspace cleanup

#### 8.5d. Coordinator Routing

In `coordinator.ScheduleReady()`, dispatch `worker.RunReview` instead
of `worker.Run` when `cfg.DeepReview` is true.

#### 8.5e. TUI Integration

- **Serial mode:** `nextStoryMsg` dispatches `runDeepReviewCmd` instead of
  `runClaudeCmd`. Skip judge check in `claudeDoneMsg` handler.
- **Parallel mode:** Workers run `RunReview`. `WorkerDone` handler skips
  `mergeBackCmd` (no ChangeID, no workspace to merge).
- **Completion:** `transitionToComplete()` skips quality review. Instead,
  runs consolidation — reads all `.ralph/stories/*/review.md` files and
  writes a consolidated `REVIEW.md` at the project root with a table
  of contents.
- **Display:** Show "Deep Review" instead of "Running" in phase labels.

#### 8.5f. PRD Format for Reviews

Stories define review targets rather than features:
```json
{
  "project": "MyProject",
  "description": "Deep code review of the authentication system",
  "userStories": [
    {
      "id": "R-001",
      "title": "Review auth middleware",
      "description": "Deep review of internal/auth/ — security, error handling, edge cases, architectural patterns",
      "acceptanceCriteria": ["Identify security vulnerabilities", "Check error handling completeness", "Assess test coverage"],
      "priority": 1
    }
  ]
}
```

### Acceptance Criteria

- [ ] `--deep-review` flag activates review mode, auto-disables judge/quality/architect
- [ ] New `RoleDeepReviewer` role with dedicated prompt
- [ ] `RunReview` worker runs Claude in read-only mode (no workspace, no commit)
- [ ] Per-story review written to `.ralph/stories/{id}/review.md`
- [ ] Consolidated `REVIEW.md` generated at project root after all stories
- [ ] Works in both serial and parallel (`--workers N`) modes
- [ ] After a deep-review run, `jj diff` shows no code changes (only .ralph/ state and REVIEW.md)
- [ ] Knowledge graph context injected into reviewer prompt when available (Phase 8)

### Estimated Scope

~400-500 lines of Go code + ~100 lines of prompt. New files: `internal/worker/review.go`,
`prompts/deep-reviewer.md`. Modifications to: config, roles, coordinator, TUI model,
TUI commands, TUI messages. The infrastructure is largely reused from the existing
worker/coordinator pipeline.

---

## Phase 9: Improved DAG Accuracy (Revised — Replaces Speculative Parallel)

**Impact: Medium | Complexity: Low-Medium | Dependencies: Phase 8 (knowledge graph for structural awareness, optional)**

> **Revision note (2026-03-19):** The original Phase 9 (Speculative Parallel
> Execution) has been dropped. With 1M context enabling bigger stories, PRDs
> have fewer, larger stories — less parallelism opportunity and less value
> from speculation. The rebase-on-conflict machinery is complex and
> error-prone with jj workspaces. Instead, this phase focuses on the root
> cause: the DAG analysis over-specifies dependencies, leaving parallelism
> on the table. Better DAG accuracy unlocks more parallelism without
> speculation.

### Goal

Improve DAG dependency analysis accuracy so fewer false dependencies block
parallel execution. The current Claude-based analysis tends to be
conservative — stories that could run in parallel are serialized due to
over-specified dependencies.

### What to Build

#### 9a. Smarter DAG Analysis Prompt

Improve the DAG analysis prompt in `internal/dag/dag.go`:
- Provide structural codebase context (from Phase 8 knowledge graph if
  available) so the analysis has actual import/dependency
  information rather than guessing
- Explicitly ask the model to distinguish "touches the same module" from
  "actually depends on the other story's output"
- Include examples of over-specification and correct classification

#### 9b. DAG Validation with Codebase Awareness

After DAG generation, validate dependencies against actual file-level
relationships:
- If story A and story B are marked as dependent but touch completely
  different files and packages, flag as likely over-specified
- Optionally auto-remove clearly false dependencies (with logging)

#### 9c. DAG Quality Tracking

Track DAG accuracy in run history:
- How many stories were serialized vs could have been parallel?
- Did any merge conflicts occur between stories marked as independent?
- Feed DAG quality metrics into Phase 5 learning loop

### Acceptance Criteria

- [ ] DAG analysis produces fewer false dependencies
- [ ] Parallel utilization improves (more stories scheduled concurrently)
- [ ] No increase in merge conflicts from relaxed dependencies
- [ ] DAG quality metrics tracked in run history
- [ ] Analysis uses structural codebase context when available (Phase 8)

### Estimated Scope

~200-300 lines of Go code. Prompt improvements, validation logic, metrics
tracking.

---

## Phase 10: Auto-Splitting Stuck Stories (New — Replaces Web Dashboard)

**Impact: Medium-High | Complexity: Medium | Dependencies: Phase 5 (anti-pattern detection), Phase 4 (architect role)**

> **Added 2026-03-19.** The original Phase 10 (Web Dashboard) has been moved
> to a stretch goal. This phase addresses a real pain point: stories that
> are stuck after 3+ iterations waste significant execution time. Rather
> than continuing to throw iterations at a stuck story, ralph should
> automatically propose splitting it into smaller sub-stories.

### Goal

When a story is repeatedly stuck or failing, automatically generate a split
proposal — smaller sub-stories that break the stuck story into more tractable
pieces — and re-queue them.

### What to Build

#### 10a. Stuck Story Analysis

When a story hits the stuck threshold (3+ iterations with no progress, or
2+ judge rejections on different criteria):
- Analyze the story's state.json, plan.md, errors_encountered, and judge
  feedback to identify which aspects are causing difficulty
- Determine if the story is "too big" (multiple unrelated concerns) or
  "too hard" (single concern but complex)

#### 10b. Split Proposal Generation

For "too big" stories:
- Use the architect role to generate 2-3 sub-stories that decompose the
  original story by concern
- Each sub-story inherits relevant acceptance criteria from the parent
- Dependencies between sub-stories are declared
- The parent story is marked as "split" (not failed)

For "too hard" stories:
- Generate a targeted FIX-story that addresses the specific blocker
- Re-queue the original story to run after the FIX-story completes

#### 10c. Re-Queuing

- Sub-stories are injected into the DAG dynamically
- In parallel mode, the coordinator adds them to the pending queue
- In serial mode, they're inserted at the current position
- The original story's completed subtasks are preserved — sub-stories
  only cover what remains

#### 10d. TUI Integration

Show split status in stories panel:
```
✂ P5-003: Implement auth middleware  → split into 3 sub-stories
  ├─ P5-003a: Auth middleware core    ⠋ running
  ├─ P5-003b: Auth error handling     ⏳ queued (depends on P5-003a)
  └─ P5-003c: Auth test coverage      ⏳ queued (depends on P5-003a)
```

### Acceptance Criteria

- [ ] Stuck stories are detected after configurable threshold
- [ ] Split proposals are generated automatically via architect role
- [ ] Sub-stories are injected into the DAG with correct dependencies
- [ ] Parent story state (completed subtasks, files touched) is preserved
- [ ] TUI shows split status and sub-story progress
- [ ] Split reduces overall failure rate (tracked in run history)

### Estimated Scope

~400-600 lines of Go code. Modifications to coordinator, dag, storystate,
and TUI.

---

## Stretch: Web Dashboard (Deferred)

**Impact: Low-Medium | Complexity: Medium | Dependencies: Phase 3 (cost data), Phase 5 (learning data)**

A localhost web UI for historical analytics, run comparison, and team
visibility. The TUI with usage tracking (Phase 3) and the remote status
page covers 90% of the need. Only pursue this if ralph goes to a team
and they want a shared view. Not on the critical path.

---

## Phase Dependency Graph

```
Phase 1 (Story State + Checkpoint) ✅
  │
  ├──→ Phase 2 (Vector Memory) ✅ → SUPERSEDED
  │      │
  │      ├──→ Phase 5.8 (Markdown Memory — replaces vector DB) ✅
  │      │
  │      ├──→ Phase 4 (Agent Specialization) ✅
  │      │
  │      ├──→ Phase 5 (Learning Loop) ✅ [partially superseded by 5.8]
  │      │      │
  │      │      ├──→ Phase 5.5 (Interactive Task Mode) ✅
  │      │      │
  │      │      └──→ Phase 10 (Auto-Split Stuck Stories)
  │      │
  │      ├──→ Phase 8 (Knowledge Graph — evidence-gated)
  │      │      │
  │      │      ├──→ Phase 8.5 (Deep Code Review Mode)
  │      │      │
  │      │      └──→ Phase 9 (Improved DAG Accuracy)
  │      │
  │      └──→ Phase 7 (MCP Server — scoped) ← depends on Phase 5.8 ✅
  │
  Phase 3 (Usage Tracking) ✅
  │
  ├──→ Phase 3.1 (1M Context Recalibration) ✅
  │
  ├──→ Phase 5.7 (Run History Observability) ✅
  │      │
  └──→ Phase 6 (Multi-Model — revised) ✅ ← benefits from Phase 4 ✅, depends on Phase 5.7 ✅
```

## Recommended Execution Order

| Order | Phase | Scope | Cumulative Value |
|-------|-------|-------|------------------|
| 1st   | Phase 1: Story State + Checkpoint ✅ | Done | Foundation for everything |
| 2nd   | Phase 3: Usage Tracking ✅ | Done | Quick win, high visibility |
| 3rd   | Phase 2: Vector Memory ✅ (superseded) | Done | Was transformative, now replaced |
| 4th   | Phase 3.1: 1M Context Recalibration ✅ | Done | Recalibrate defaults + TUI improvements |
| 5th   | Phase 4: Agent Specialization ✅ | Done | Quality step-change |
| 6th   | Phase 5: Learning Loop (revised) ✅ | Done | Compounding cross-run improvement, anti-patterns, skill feedback |
| 7th   | Phase 5.5: Interactive Task Mode ✅ | Done | On-the-fly task dispatch, no PRD required |
| 8th   | Phase 5.7: Run History Observability ✅ | Done | Baseline metrics for model comparison |
| 9th   | Phase 5.8: Markdown Memory ✅ | Done | Deleted ~2600 lines, removed 3 dependencies, simpler + better memory |
| 10th  | Phase 6: Multi-Model (revised) ✅ | Done | Per-role model selection + DAG tree visualization in TUI |
| **11th** | **Phase 7: MCP Server (scoped)** | ~6-8 stories | Real-time parallel coordination |
| **12th** | **Phase 8: Knowledge Graph (if needed)** | ~8-10 stories | Structural intelligence — gate on evidence |
| **13th** | **Phase 8.5: Deep Code Review Mode** | ~4-5 stories | Read-only code review via Ralph loop |
| **14th** | **Phase 9: Improved DAG Accuracy** | ~3-4 stories | Better parallelism without speculation |
| **15th** | **Phase 10: Auto-Split Stuck Stories** | ~5-7 stories | Reduce wasted iterations on stuck stories |
| Stretch | Web Dashboard | — | Team visibility (if needed) |

Phase 6 is now complete — per-role model selection (Opus for planning/debugging,
Sonnet for implementation/review, Haiku for utility tasks) is wired through
the full stack from CLI flags to runner invocation. The TUI stories panel
now renders a DAG tree visualization with box-drawing connectors. Phase 7
(MCP Server) is the recommended next phase — it enables real-time parallel
coordination that static prompt injection cannot provide. Phase 8 is
explicitly evidence-gated — only build if architects are missing structural
dependency information that they can't get by reading codebase files directly.

---

## Success Metrics

After full rollout, ralph should demonstrate:

- **Context efficiency**: Agent prompts contain curated learned context
  via markdown memory, not raw dumps. Dream consolidation keeps memory
  files lean. ✅ (Phases 1-2, completed by Phase 5.8)
- **Story success rate**: >90% first-attempt pass rate (up from current baseline)
- **Speed allocation**: Architect uses Opus, implementer uses Sonnet — right
  model for each role (replaces cost reduction metric — user is on Claude Max) ✅ (Phase 6)
- **Crash resilience**: Any interruption recoverable via checkpoint + resume ✅ (Phase 1)
- **Cross-run learning**: Measurable improvement in success rate across
  successive PRD runs on the same codebase ✅ (Phase 5, completed by Phase 5.8)
- **Minimal dependencies**: No external ML/vector infrastructure required.
  Memory system runs on markdown files + LLM comprehension ✅ (Phase 5.8)
- **Parallel efficiency**: DAG analysis produces fewer false dependencies,
  enabling more concurrent story execution (Phase 9)
- **Stuck recovery**: Stuck stories are automatically split rather than
  wasting iterations (Phase 10)
- **Deep review**: Ralph can perform meticulous read-only code reviews
  using the same loop infrastructure, producing structured findings with
  file-by-file analysis and architectural observations (Phase 8.5)
- **Interactive mode**: Tasks can be dispatched on the fly without a PRD,
  with clarification step ensuring quality input ✅ (Phase 5.5)
- **Visibility**: Full usage and performance analytics in TUI ✅ (Phase 3)
