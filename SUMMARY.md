# Phase 5: Learning Loop / Self-Improving System

**Branch:** `ralph/phase5-learning-loop`
**Date:** 2026-03-19

## Overview

Phase 5 adds a learning loop to Ralph that enables cross-run improvement. After a PRD run completes, the system automatically synthesizes lessons from story outcomes (retries, failures, judge rejections), embeds them into ChromaDB for semantic retrieval in future runs, detects recurring anti-patterns from error history, and feeds PRD-quality lessons back into the `/ralph` skill so that generated PRDs improve over time. The system also displays detected anti-patterns in the TUI and injects warnings into agent prompts when stories touch fragile files.

## Stories Completed

| Story | Title | Summary |
|-------|-------|---------|
| P5-001 | Add lesson collections to memory types | Registered `ralph_lessons` (cap 100) and `ralph_prd_lessons` (cap 50) ChromaDB collections in types, hygiene, and retrieval. |
| P5-002 | Lesson types and JSON persistence | Added `Lesson` and `LessonsFile` structs with `SaveLessons`/`LoadLessons` for `.ralph/lessons.json` round-trip. |
| P5-003 | Post-run synthesis prompt and extraction | Created `SynthesizeRunLessons` in `synthesis.go` — builds a prompt from run stats + story states, invokes Gemini, parses response into `[]Lesson`. |
| P5-004 | Embed synthesized lessons into ChromaDB | Created `EmbedLessons` in `pipeline.go` with >0.9 cosine dedup, `times_confirmed` increment, confidence bump (+0.1, capped at 1.0), and collection cap enforcement. |
| P5-005 | Wire post-run synthesis into TUI | Synthesis runs async via `tea.Cmd` at phaseDone, before sidecar shutdown. Results delivered via `synthesisCompleteMsg`. Failures logged, never block the run. |
| P5-006 | Anti-pattern detection | `DetectAntiPatterns` queries `ralph_errors` and `ralph_completions` to find fragile areas, flaky tests, common oversights, and high-friction files (threshold: 3+ occurrences). |
| P5-007 | Inject anti-pattern warnings into prompts | `BuildPromptOpts.AntiPatterns` field + `buildAntiPatternWarnings` helper injects max 3 `KNOWN ISSUE:` warnings into agent prompts when story files match flagged areas. |
| P5-008 | Display anti-patterns in TUI Usage tab | Added "Detected Anti-Patterns" section to the context panel with category icons, occurrence counts, and affected file lists. |
| P5-009 | Extract PRD-quality lessons | Extended synthesis to return `SynthesisResult` with separate PRD lessons (sizing, criteria, ordering, missing_criteria). Stored in `ralph_prd_lessons` collection and `prd_lessons` key in lessons.json. |
| P5-010 | Inject lessons into /ralph skill | Updated `SKILL.md` with a "Step 2: Check for Learned PRD Lessons" section — reads `.ralph/lessons.json`, applies confidence thresholds (>=0.7 = constraint, <0.7 = suggestion), max 10 lessons sorted by confidence. |
| P5-011 | Include lessons in retrieval pipeline | Added `Confidence()` method to `Document`, confidence weighting in ranking formula (`score * recency * confidence_weight`). Lesson collections use confidence metadata; others get neutral 1.0. |
| P5-012 | End-to-end synthesis tests | 7 integration tests covering synthesize→embed pipeline, confidence tracking across runs, decay eviction, anti-pattern detection, and full pipeline flow. |

## Files Changed

### New Files
- `internal/memory/antipatterns.go` — `AntiPattern` struct and `DetectAntiPatterns` function
- `internal/memory/antipatterns_test.go` — 7 test cases with mock ChromaDB server
- `internal/memory/synthesis.go` — `SynthesizeRunLessons`, prompt building, Gemini invocation, JSON parsing
- `internal/memory/synthesis_test.go` — 7 unit tests for synthesis
- `internal/memory/pipeline.go` — `EmbedLessons` with dedup and cap enforcement
- `internal/memory/embed_lessons_test.go` — 4 tests for embed/dedup logic
- `internal/memory/lessons_test.go` — SaveLessons/LoadLessons round-trip tests
- `internal/memory/e2e_synthesis_test.go` — 7 end-to-end integration tests
- `internal/memory/retrieval_test.go` — Confidence weighting tests

### Modified Files
- `internal/memory/types.go` — `Lesson`, `LessonsFile`, `SynthesisResult` structs, `CollectionLessons`, `CollectionPRDLessons`, `Confidence()` method on `Document`
- `internal/memory/hygiene.go` — Added collection caps for new collections
- `internal/memory/retrieval.go` — Confidence weighting in ranking formula, `isLessonCollection` helper
- `internal/memory/client.go` — Added `GetAllDocuments` method
- `internal/runner/runner.go` — `AntiPatterns` field on `BuildPromptOpts`, `buildAntiPatternWarnings`, `collectStoryFiles`, `extractFilePaths`
- `internal/runner/prompt_test.go` — Anti-pattern warning injection tests
- `internal/tui/model.go` — Synthesis async flow, anti-pattern state, `synthesisCompleteMsg` handling
- `internal/tui/commands.go` — `synthesisCmd`, `detectAntiPatternsCmd`
- `internal/tui/messages.go` — `synthesisCompleteMsg`, `antiPatternsMsg`
- `internal/tui/context_panel.go` — Anti-patterns display in Usage tab
- `skills/ralph/SKILL.md` — Learned PRD lessons injection instructions

## Configuration

- **No new environment variables** required
- **No new external dependencies** added
- Lessons persist to `.ralph/lessons.json` in the project directory (auto-created on first synthesis)
- Two new ChromaDB collections (`ralph_lessons`, `ralph_prd_lessons`) are created automatically via `AllCollections()`

## Build & Run

```bash
make build        # Build the ralph binary to build/ralph
make test         # Run all unit tests
make test-e2e TEST=<name>  # Run e2e tests (serial-single, parallel-independent, etc.)
```

## Testing

```bash
# All unit tests
go test ./...

# Memory package tests only (most Phase 5 code lives here)
go test ./internal/memory/...

# Runner prompt tests
go test ./internal/runner/...
```

### New Test Files
| File | Tests | Coverage |
|------|-------|----------|
| `internal/memory/lessons_test.go` | 3 | SaveLessons/LoadLessons round-trip, missing file handling |
| `internal/memory/synthesis_test.go` | 7 | Synthesis prompt building, JSON parsing, empty results, PRD lessons |
| `internal/memory/antipatterns_test.go` | 7 | Pattern detection with mock ChromaDB, threshold enforcement |
| `internal/memory/embed_lessons_test.go` | 4 | Dedup, times_confirmed increment, cap enforcement |
| `internal/memory/retrieval_test.go` | ~3 | Confidence weighting in ranking |
| `internal/memory/e2e_synthesis_test.go` | 7 | Full pipeline integration tests |
| `internal/runner/prompt_test.go` | 18 | Anti-pattern warning injection, file extraction |

## Notes

- **Judge auto-pass warnings**: Most judge verdicts show Gemini 429 errors (capacity unavailable), so stories were auto-passed rather than properly judged. The code compiles and tests pass, but judge feedback was limited.
- **Synthesis requires Gemini**: The `SynthesizeRunLessons` function calls Gemini via the existing `internal/exec/` infrastructure. If Gemini is unavailable, synthesis silently fails and logs the error.
- **Synthesis timing**: Synthesis fires before `stopSidecar()` since it needs ChromaDB running. If ChromaDB is already down, embedding will fail silently.
- **Decay**: Lessons follow the same 0.85 decay as other collections. Low-confidence lessons (below 0.3) are evicted during hygiene cycles.
- **Anti-pattern thresholds**: Files must appear in 3+ error documents to be flagged. This threshold is hardcoded in `DetectAntiPatterns`.
