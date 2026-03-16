# Phase 3.1: 1M Context Recalibration — Summary

**Branch:** `ralph/phase3.1-1m-context-recalibration`
**Date:** 2026-03-16
**Stories:** 8/8 completed, all passed judge review

## Overview

Recalibrated Ralph's parameters and heuristics for 1M token context windows. Loosened context-scarcity constraints across memory retrieval defaults, agent prompts, and skill guidance. Added TUI improvements: iteration counts on running stories, expandable story details, memory retrieval display, and full judge rejection reasoning.

## Stories Completed

| ID | Title | Summary |
|----|-------|---------|
| P31-001 | Update memory retrieval defaults | TopK 5→15, MinScore 0.7→0.6, MaxTokens 2000→8000 |
| P31-002 | Rewrite /ralph skill story-sizing | Replaced context-window sizing guidance with scope-quality focus |
| P31-003 | Update ralph-prompt.md | Removed context-scarcity language, encouraged broader codebase reading |
| P31-004 | Increase activity log cap | 64KB → 256KB in ReadActivityContent() |
| P31-005 | Show iteration count in stories panel | Running stories display "(iter N)" between title and worker badge |
| P31-006 | Show retrieved memories in memory tab | "Retrieved for {story-id}" section with scores, collections, content previews |
| P31-007 | Expandable story details | Enter/arrow to expand inline details (files, subtasks, judge feedback); cursor navigation |
| P31-008 | Full judge rejection reasoning | Failed criteria show indented Reason and Suggestion with visual guides |

## Files Changed

**Config & Defaults:**
- `internal/config/config.go` — Memory retrieval default values and help text
- `README.md` — Updated CLI flag documentation

**Agent Prompts & Skills:**
- `ralph-prompt.md` — Removed context-scarcity language, added broad reading guidance
- `skills/ralph/SKILL.md` — Rewrote story-sizing section for scope-quality

**Runner:**
- `internal/runner/runner.go` — Activity log cap increase; BuildPrompt now returns RetrievalResult

**Memory:**
- `internal/memory/retrieval.go` — Added Score/Content to DocRef, TotalFound/MaxTokens to RetrievalResult

**Judge:**
- `internal/judge/judge.go` — FormatResult shows full rejection reasoning with indented feedback

**TUI:**
- `internal/tui/model.go` — Iteration count, story cursor/expansion state, memory retrieval state
- `internal/tui/stories_panel.go` — IterationCount display, cursor navigation, expandable details
- `internal/tui/context_panel.go` — Memory retrieval rendering
- `internal/tui/messages.go` — MemoryRetrievalMsg type
- `internal/tui/commands.go` — Capture retrieval data from BuildPrompt

## Configuration

No new environment variables or configuration files. Existing CLI flags retain the same names with updated defaults:
- `--memory-top-k` default: 15 (was 5)
- `--memory-min-score` default: 0.6 (was 0.7)
- `--memory-max-tokens` default: 8000 (was 2000)

## Build & Run

```bash
make build        # Build binary to build/ralph
make test         # Run unit tests (go test ./...)
make test-e2e TEST=<name>  # Run E2E tests (serial-single, parallel-independent, etc.)
```

## Testing

All existing tests pass (`go test ./...`). No new test files were added — changes were to defaults, prompts, and TUI rendering. E2E tests can verify end-to-end behavior:

```bash
make test-e2e TEST=serial-single
make test-e2e TEST=parallel-independent
```

## Notes

- Judge verdict JSON parsing warnings appeared during automated judging but all stories passed
- The `[CONTEXT EXHAUSTED]` marker in ralph-prompt.md is referenced as a string literal in runner.go — do not rename without updating both
- Story expansion in the stories panel calls `storystate.Load()` at render time — could be cached if performance becomes an issue
- Memory retrieval display relies on BuildPrompt's second return value (`RetrievalResult`) — callers discarding it with `_` are unaffected
