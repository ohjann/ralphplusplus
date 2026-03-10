#!/usr/bin/env bash
#
# Ralph E2E Test Runner
#
# Sets up a temporary jj repo from the sample project, copies a test fixture,
# and runs ralph against it. Validates results after completion.
#
# Usage:
#   ./testdata/run-test.sh <test-name> [ralph-flags...]
#
# Test names:
#   serial-single      Single story, fastest possible test (~1 iteration)
#   serial-basic       Two sequential stories (~2 iterations)
#   parallel-independent  Three independent stories with --workers 3
#   parallel-deps      Three stories with dependency chain, --workers 2
#   plan               Test --plan mode with plan-test.md
#   quality            Single story + --quality-review
#   judge              Two stories + --judge
#   idle               Just test TUI rendering (no Claude, instant)
#
# Examples:
#   ./testdata/run-test.sh serial-single
#   ./testdata/run-test.sh parallel-independent --workers 2
#   ./testdata/run-test.sh serial-basic --judge
#   ./testdata/run-test.sh quality --quality-workers 5

set -eo pipefail

RALPH_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RALPH_BIN="${RALPH_ROOT}/build/ralph"
SAMPLE_DIR="${RALPH_ROOT}/testdata/sample-project"
FIXTURES_DIR="${RALPH_ROOT}/testdata/fixtures"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info()  { echo -e "${BLUE}[INFO]${NC} $*"; }
ok()    { echo -e "${GREEN}[OK]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
fail()  { echo -e "${RED}[FAIL]${NC} $*"; }

usage() {
    echo "Usage: $0 <test-name> [ralph-flags...]"
    echo ""
    echo "Test names:"
    echo "  serial-single         Single story (~1 iteration)"
    echo "  serial-basic          Two sequential stories (~2 iterations)"
    echo "  parallel-independent  Three independent stories (uses --workers 3)"
    echo "  parallel-deps         Three stories with deps (uses --workers 2)"
    echo "  plan                  Test --plan mode"
    echo "  quality               Single story + --quality-review"
    echo "  judge                 Two stories + --judge"
    echo "  idle                  TUI rendering only (instant, no Claude)"
    echo ""
    echo "Extra ralph flags can be appended after the test name."
    exit 1
}

if [[ $# -lt 1 ]]; then
    usage
fi

TEST_NAME="$1"
shift
EXTRA_FLAGS=("$@")

# Build ralph if needed
if [[ ! -f "$RALPH_BIN" ]] || [[ "$RALPH_ROOT/cmd/ralph/main.go" -nt "$RALPH_BIN" ]]; then
    info "Building ralph..."
    (cd "$RALPH_ROOT" && make build)
fi

# Create temp directory for test
TEST_DIR=$(mktemp -d /tmp/ralph-test-XXXXXX)
info "Test directory: ${TEST_DIR}"

cleanup() {
    if [[ "${KEEP_TEST_DIR:-}" == "1" ]]; then
        warn "Keeping test dir: ${TEST_DIR}"
    else
        rm -rf "$TEST_DIR"
        info "Cleaned up ${TEST_DIR}"
    fi
}
trap cleanup EXIT

# Copy sample project (including hidden files like .gitignore)
cp -r "$SAMPLE_DIR"/* "$SAMPLE_DIR"/.* "$TEST_DIR"/ 2>/dev/null || true

# Initialize jj repo
info "Initializing jj repo..."
(cd "$TEST_DIR" && jj git init && jj commit -m "initial commit")

# Determine fixture and default flags based on test name
RALPH_FLAGS=()
case "$TEST_NAME" in
    serial-single)
        cp "$FIXTURES_DIR/serial-single.json" "$TEST_DIR/prd.json"
        info "Test: Serial single story"
        ;;
    serial-basic)
        cp "$FIXTURES_DIR/serial-basic.json" "$TEST_DIR/prd.json"
        info "Test: Serial basic (2 stories)"
        ;;
    parallel-independent)
        cp "$FIXTURES_DIR/parallel-independent.json" "$TEST_DIR/prd.json"
        RALPH_FLAGS+=(--workers 3)
        info "Test: Parallel independent (3 stories, 3 workers)"
        ;;
    parallel-deps)
        cp "$FIXTURES_DIR/parallel-deps.json" "$TEST_DIR/prd.json"
        RALPH_FLAGS+=(--workers 2)
        info "Test: Parallel with dependencies (3 stories, 2 workers)"
        ;;
    plan)
        cp "$FIXTURES_DIR/plan-test.md" "$TEST_DIR/plan.md"
        RALPH_FLAGS+=(--plan plan.md)
        info "Test: Plan mode (generates prd.json from plan)"
        ;;
    quality)
        cp "$FIXTURES_DIR/serial-single.json" "$TEST_DIR/prd.json"
        RALPH_FLAGS+=(--quality-review)
        info "Test: Quality review (1 story + quality gate)"
        ;;
    judge)
        cp "$FIXTURES_DIR/serial-basic.json" "$TEST_DIR/prd.json"
        RALPH_FLAGS+=(--judge)
        info "Test: Judge mode (2 stories + Gemini judge)"
        ;;
    idle)
        cp "$FIXTURES_DIR/serial-basic.json" "$TEST_DIR/prd.json"
        RALPH_FLAGS+=(--idle)
        info "Test: Idle mode (TUI only, no execution)"
        ;;
    *)
        fail "Unknown test: ${TEST_NAME}"
        usage
        ;;
esac

# Add extra flags
if [[ ${#EXTRA_FLAGS[@]} -gt 0 ]]; then
    RALPH_FLAGS+=("${EXTRA_FLAGS[@]}")
fi

# Create progress.md
touch "$TEST_DIR/progress.md"

# Show what we're running
info "Running: ralph --dir ${TEST_DIR} ${RALPH_FLAGS[*]}"
echo ""

# Run ralph
START_TIME=$(date +%s)
"$RALPH_BIN" --dir "$TEST_DIR" "${RALPH_FLAGS[@]}" || true
END_TIME=$(date +%s)
ELAPSED=$((END_TIME - START_TIME))

echo ""
info "Completed in ${ELAPSED}s"

# Validate results
echo ""
info "=== Validation ==="

PASS_COUNT=0
FAIL_COUNT=0

check() {
    local desc="$1"
    local result="$2"
    if [[ "$result" == "true" ]]; then
        ok "$desc"
        ((PASS_COUNT++))
    else
        fail "$desc"
        ((FAIL_COUNT++))
    fi
}

# Skip validation for idle mode
if [[ "$TEST_NAME" == "idle" ]]; then
    ok "Idle mode exited cleanly"
    exit 0
fi

# Check prd.json exists
if [[ -f "$TEST_DIR/prd.json" ]]; then
    check "prd.json exists" "true"

    # Check story completion
    TOTAL=$(python3 -c "import json; d=json.load(open('$TEST_DIR/prd.json')); print(len(d['userStories']))")
    PASSED=$(python3 -c "import json; d=json.load(open('$TEST_DIR/prd.json')); print(sum(1 for s in d['userStories'] if s['passes']))")
    check "Stories passed: ${PASSED}/${TOTAL}" "$([[ "$PASSED" == "$TOTAL" ]] && echo true || echo false)"

    # Show story details
    python3 -c "
import json
d = json.load(open('$TEST_DIR/prd.json'))
for s in d['userStories']:
    status = 'PASS' if s['passes'] else 'FAIL'
    print(f'  {s[\"id\"]}: {s[\"title\"]} [{status}]')
    if s.get('notes'):
        print(f'        notes: {s[\"notes\"]}')
"
else
    check "prd.json exists" "false"
fi

# Check progress.md has content
if [[ -s "$TEST_DIR/progress.md" ]]; then
    check "progress.md has content" "true"
    LINES=$(wc -l < "$TEST_DIR/progress.md" | tr -d ' ')
    info "  progress.md: ${LINES} lines"
else
    check "progress.md has content" "false"
fi

# Check .ralph directory
if [[ -d "$TEST_DIR/.ralph" ]]; then
    check ".ralph directory exists" "true"
    LOG_COUNT=$(find "$TEST_DIR/.ralph/logs" -name "*.log" 2>/dev/null | wc -l | tr -d ' ')
    info "  Log files: ${LOG_COUNT}"
else
    check ".ralph directory exists" "false"
fi

# Check Go project still builds
if (cd "$TEST_DIR" && go build ./... 2>/dev/null); then
    check "go build ./... passes" "true"
else
    check "go build ./... passes" "false"
fi

# Check Go tests pass (if test files exist)
if ls "$TEST_DIR"/*_test.go &>/dev/null; then
    if (cd "$TEST_DIR" && go test ./... 2>/dev/null); then
        check "go test ./... passes" "true"
    else
        check "go test ./... passes" "false"
    fi
fi

# Test-specific validation
case "$TEST_NAME" in
    plan)
        # Verify prd.json was generated (not just copied)
        if python3 -c "import json; d=json.load(open('$TEST_DIR/prd.json')); assert len(d['userStories']) > 0" 2>/dev/null; then
            check "Plan generated valid prd.json with stories" "true"
        else
            check "Plan generated valid prd.json with stories" "false"
        fi
        ;;
    quality)
        if ls "$TEST_DIR/.ralph/quality/"*.json &>/dev/null; then
            check "Quality assessment files generated" "true"
        else
            check "Quality assessment files generated" "false"
        fi
        ;;
esac

# Check jj history
if (cd "$TEST_DIR" && jj log --no-graph -r 'all()' -T 'description ++ "\n"' 2>/dev/null | grep -q .); then
    check "jj has commit history" "true"
    info "  Recent commits:"
    (cd "$TEST_DIR" && jj log --no-graph -r 'all()' -T '"    " ++ description' 2>/dev/null | head -5)
fi

echo ""
if [[ $FAIL_COUNT -eq 0 ]]; then
    ok "All ${PASS_COUNT} checks passed"
else
    fail "${FAIL_COUNT} checks failed, ${PASS_COUNT} passed"
fi

# Offer to keep test dir for inspection
if [[ $FAIL_COUNT -gt 0 ]]; then
    warn "Set KEEP_TEST_DIR=1 to preserve test directory for debugging"
fi
