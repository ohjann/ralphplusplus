# Ralph E2E Test Plan

Automated test harness for verifying the Ralph loop end-to-end. Uses a tiny Go project with trivial stories so each test runs fast and cheap.

## Quick Start

```bash
# Build + run a test
make test-e2e TEST=serial-single

# Or directly
./testdata/run-test.sh serial-single

# Keep test dir on failure for debugging
KEEP_TEST_DIR=1 ./testdata/run-test.sh serial-basic
```

## Test Fixtures

All fixtures live in `testdata/fixtures/`. The sample project (`testdata/sample-project/`) is a tiny Go module with `main.go` and `math.go`.

| Fixture | Stories | What it tests |
|---------|---------|---------------|
| `serial-single.json` | 1 | Minimal loop: one story, one iteration |
| `serial-basic.json` | 2 | Sequential stories with dependency (add function, then test it) |
| `parallel-independent.json` | 3 | Three independent stories (different files) |
| `parallel-deps.json` | 3 | DAG analysis: story 3 depends on stories 1+2 |
| `plan-test.md` | - | `--plan` mode: generate prd.json from a plan file |

## Test Scenarios

### 1. Smoke Test (fastest, ~1 min)

```bash
make test-e2e TEST=serial-single
```

Verifies the core loop works: pick story, run Claude, commit, mark passes.

**Expected:**
- TS-001 passes
- `math.go` has a `Multiply` function
- `go build ./...` passes
- progress.md has content
- jj has commit history

### 2. Serial Multi-Story (~2-3 min)

```bash
make test-e2e TEST=serial-basic
```

Verifies multiple iterations with story dependencies.

**Expected:**
- TS-001 passes (adds Multiply function)
- TS-002 passes (adds math_test.go testing all functions including Multiply)
- `go test ./...` passes
- Two iterations in logs

### 3. Parallel Independent (~1-2 min)

```bash
make test-e2e TEST=parallel-independent
```

Verifies parallel execution with independent stories.

**Expected:**
- DAG analysis runs and identifies all 3 stories as independent
- All 3 workers run concurrently
- All stories pass
- jj workspaces created and cleaned up
- `go build ./...` passes

### 4. Parallel with Dependencies (~2-3 min)

```bash
make test-e2e TEST=parallel-deps
```

Verifies DAG scheduling respects dependencies.

**Expected:**
- DAG analysis identifies TD-003 depends on TD-001 and TD-002
- TD-001 and TD-002 run in parallel
- TD-003 waits until both complete, then runs
- All stories pass
- `go test ./...` passes

### 5. Plan Mode (~2-3 min)

```bash
make test-e2e TEST=plan
```

Verifies prd.json generation from a plan file.

**Expected:**
- TUI shows "Planning" phase
- Claude generates prd.json with valid stories
- TUI pauses for review
- After Enter, stories execute normally
- All generated stories pass

### 6. Quality Review (~3-5 min)

```bash
make test-e2e TEST=quality
```

Verifies the quality review gate after story completion.

**Expected:**
- TS-001 passes
- Quality review phase starts (5 lens reviewers run)
- Assessment file generated in `.ralph/quality/`
- Quality fix phase runs if issues found
- Project still builds cleanly

### 7. Judge Mode (~3-5 min)

```bash
make test-e2e TEST=judge
```

Verifies Gemini judge integration. Requires `gemini` CLI installed.

**Expected:**
- Each story is reviewed by Gemini after Claude marks it done
- If rejected, story re-runs with feedback
- All stories eventually pass
- Judge feedback files appear in `.ralph/`

### 8. Idle Mode (instant)

```bash
make test-e2e TEST=idle
```

Verifies TUI renders without executing anything.

**Expected:**
- TUI displays header, panels, footer
- Shows "Idle mode" in header
- Exits cleanly on `q`

## Combining Flags

Extra flags can be appended to any test:

```bash
# Serial with judge
./testdata/run-test.sh serial-basic --judge

# Parallel with quality review
./testdata/run-test.sh parallel-independent --workers 2 --quality-review

# Quality with more workers
./testdata/run-test.sh quality --quality-workers 5 --quality-max-iterations 3
```

## Validation Checks

The test runner automatically validates:

1. **prd.json** — exists and all stories pass
2. **progress.md** — has content (learnings written)
3. **.ralph/** — directory and log files exist
4. **go build** — project still compiles
5. **go test** — tests pass (if test files exist)
6. **jj history** — commits were made
7. **Test-specific** — quality assessments, generated prd.json, etc.

## Debugging Failures

```bash
# Keep the test directory for inspection
KEEP_TEST_DIR=1 ./testdata/run-test.sh serial-single

# Check Claude logs
cat /tmp/ralph-test-*/\.ralph/logs/iteration-*-activity.log

# Check prd.json state
cat /tmp/ralph-test-*/prd.json | python3 -m json.tool

# Check jj history
cd /tmp/ralph-test-* && jj log
```

## Adding New Fixtures

1. Create a new JSON file in `testdata/fixtures/`
2. Follow the prd.json schema (see README.md)
3. Keep stories trivial — each should complete in <30s of Claude time
4. Add a case in `testdata/run-test.sh` with appropriate default flags
5. Document the test scenario above

## Design Principles

- **Trivial stories**: Each story is a single function or file — fast and cheap
- **Self-contained**: No external deps, databases, or network calls
- **Deterministic**: Stories are unambiguous so Claude produces consistent results
- **Composable**: Tests can be combined with extra flags for coverage
- **Automatic validation**: Script checks results, not just "did it run"
