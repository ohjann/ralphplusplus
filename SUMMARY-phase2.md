# Ralph Phase 2 Gaps — Implementation Summary

## Overview

Fixed three semantic memory gaps in Ralph's Phase 2 implementation: (1) wired semantic memory context into parallel workers so they receive the same retrieval quality as serial mode, (2) replaced raw `UpsertDocuments` calls with `DeduplicateInsertBatch` in the embedding pipeline to prevent near-duplicate document accumulation using cosine similarity (>0.9 threshold), and (3) unified the ChromaDB data directory between TUI and CLI so both operate on the same database. Added test coverage for the new behavior and updated the evolution plan to mark Phase 2 as fully complete.

## Stories Completed

| ID | Title | Summary |
|----|-------|---------|
| **P2G-001** | Wire semantic memory into parallel workers | Added `ChromaClient` and `Embedder` fields to the Worker struct. `worker.Run()` now constructs `BuildPromptOpts` with memory retrieval (matching the TUI serial pattern). Coordinator threads memory deps to workers via `SetMemory()`. |
| **P2G-002** | Replace UpsertDocuments with DeduplicateInsertBatch | Replaced all 4 `UpsertDocuments` call sites in `pipeline.go` (`embedPatterns`, `embedCompletion`, `embedErrors`, `embedDecisions`) with `DeduplicateInsertBatch`, which applies cosine similarity dedup (distance < 0.1) and enforces collection caps. |
| **P2G-003** | Unify ChromaDB data directory | Changed CLI memory subcommands in `main.go` to use `cfg.RalphHome/memory/` (same as TUI) instead of `cfg.ProjectDir/.ralph/memory/`. One-line fix. |
| **P2G-004** | Add tests for pipeline dedup and worker memory | Added `pipeline_test.go` (4 tests verifying DeduplicateInsertBatch usage and near-duplicate merge behavior) and `worker_test.go` (3 tests verifying BuildPromptOpts construction with/without memory). |
| **FIX-P2G-002** | Fix build error from unused import | Removed unused `prd` import in `cmd/ralph/main.go` that was preventing compilation. |
| **P2G-005** | Update EVOLUTION_PLAN.md | Marked Phase 2 as fully "COMPLETE" (removed "with known gaps"), removed the known gaps section, checked off dedup and parallel worker acceptance criteria, removed "serial mode only" caveats. |

## Files Changed

| File | Change |
|------|--------|
| `internal/worker/worker.go` | Added optional `ChromaClient`/`Embedder` fields; `Run()` builds `BuildPromptOpts` from them |
| `internal/coordinator/coordinator.go` | Added `SetMemory()` method; threads memory deps to workers in `ScheduleReady()` |
| `internal/tui/model.go` | Calls `SetMemory()` on coordinator at both creation sites (New and NewFromCheckpoint) |
| `internal/memory/pipeline.go` | Replaced 4x `UpsertDocuments` → `DeduplicateInsertBatch` |
| `cmd/ralph/main.go` | Fixed memory data dir path; removed unused `prd` import |
| `internal/config/config.go` | Minor update for memory config access |
| `internal/memory/pipeline_test.go` | **New** — 4 tests for pipeline dedup behavior |
| `internal/worker/worker_test.go` | **New** — 3 tests for worker memory integration |
| `docs/EVOLUTION_PLAN.md` | Updated Phase 2 status to fully COMPLETE |

## Configuration

No new configuration or environment variables were introduced. Existing memory configuration flags continue to work:

- `--memory-disable` — disables semantic memory for both serial and parallel modes
- ChromaDB data is stored at `<RalphHome>/memory/` (typically `.ralph/memory/`)
- ChromaDB sidecar is managed automatically by the TUI

## Build & Run

```bash
# Build
make build          # outputs to build/ralph

# Run TUI (main usage)
./build/ralph

# CLI memory commands (now use same DB as TUI)
./build/ralph memory stats
./build/ralph memory search <query>
./build/ralph memory prune
./build/ralph memory reset
```

## Testing

```bash
# Unit tests
go test ./...

# Specific packages with new tests
go test ./internal/memory/...    # includes pipeline_test.go
go test ./internal/worker/...    # includes worker_test.go

# E2E tests
make test-e2e TEST=serial-single
make test-e2e TEST=parallel-independent
```

New test files:
- `internal/memory/pipeline_test.go` — verifies dedup behavior across all embed functions
- `internal/worker/worker_test.go` — verifies memory opts propagation and nil-safety

## Notes

- Two acceptance criteria in the evolution plan remain unchecked (retrieval quality measurement and memory degradation over time) — these require manual verification with real PRD runs and are Phase 3 concerns.
- The `docs/EVOLUTION_PLAN.md` file has local uncommitted modifications — review and commit as needed.
- The mock ChromaDB server in tests must handle UUID-based routes (`/api/v1/collections/{uuid}/count`), not just name-based routes, because `DeduplicateInsertBatch` chains through `getCollectionID`.
