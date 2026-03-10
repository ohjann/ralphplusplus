#!/bin/bash
# Ralph - Long-running AI agent loop
# Usage: ralph [--dir <path>] [--judge] [max_iterations]

set -e

# --- Defaults ---
MAX_ITERATIONS=10
PROJECT_DIR=""
JUDGE_ENABLED=false
JUDGE_MAX_REJECTIONS=2

# --- Help ---
usage() {
  cat <<'EOF'
Usage: ralph [options] [max_iterations]

Run the Ralph autonomous agent loop against a prd.json in the current directory.

Options:
  --dir <path>                    Project directory containing prd.json (default: current directory)
  --judge                         Enable LLM-as-Judge verification (requires gemini CLI)
  --judge-max-rejections <n>      Max judge rejections per story before auto-passing (default: 2)
  --help, -h                      Show this help message

Arguments:
  max_iterations                  Maximum loop iterations (default: 10)

Examples:
  ralph                           Run with 10 iterations, prd.json in current dir
  ralph 5                         Run with 5 iterations
  ralph --dir ~/myapp             Run against prd.json in ~/myapp
  ralph --judge                   Run with Gemini judge verification
  ralph --judge --judge-max-rejections 3   Allow up to 3 rejections per story
EOF
  exit 0
}

# --- Parse arguments ---
while [[ $# -gt 0 ]]; do
  case $1 in
    --help|-h)
      usage
      ;;
    --dir)
      PROJECT_DIR="$2"
      shift 2
      ;;
    --dir=*)
      PROJECT_DIR="${1#*=}"
      shift
      ;;
    --judge)
      JUDGE_ENABLED=true
      shift
      ;;
    --judge-max-rejections)
      JUDGE_MAX_REJECTIONS="$2"
      shift 2
      ;;
    --judge-max-rejections=*)
      JUDGE_MAX_REJECTIONS="${1#*=}"
      shift
      ;;
    *)
      if [[ "$1" =~ ^[0-9]+$ ]]; then
        MAX_ITERATIONS="$1"
      else
        echo "Error: Unknown argument '$1'. Use --help for usage."
        exit 1
      fi
      shift
      ;;
  esac
done

# RALPH_HOME is where the prompts live (the ralph repo)
RALPH_HOME="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# PROJECT_DIR is where prd.json and working files live (default: cwd)
PROJECT_DIR="${PROJECT_DIR:-$(pwd)}"
PROJECT_DIR="$(cd "$PROJECT_DIR" && pwd)"  # resolve to absolute path

PRD_FILE="$PROJECT_DIR/prd.json"
PROGRESS_FILE="$PROJECT_DIR/progress.md"
ARCHIVE_DIR="$PROJECT_DIR/.ralph/archive"
LAST_BRANCH_FILE="$PROJECT_DIR/.ralph/.last-branch"
LOG_DIR="$PROJECT_DIR/.ralph/logs"

# Check prd.json exists
if [ ! -f "$PRD_FILE" ]; then
  echo "Error: No prd.json found in $PROJECT_DIR"
  echo "Use the /ralph skill in Claude Code to create one from a PRD."
  exit 1
fi

# Ensure .ralph directory exists
mkdir -p "$PROJECT_DIR/.ralph"
mkdir -p "$LOG_DIR"

# --- Judge pre-flight checks ---
if [ "$JUDGE_ENABLED" = true ]; then
  if ! command -v gemini &>/dev/null; then
    echo "Error: --judge requires the 'gemini' CLI to be installed."
    echo "Install it from: https://github.com/google-gemini/gemini-cli"
    exit 1
  fi
  if [ ! -f "$RALPH_HOME/judge-prompt.md" ]; then
    echo "Error: judge-prompt.md not found at $RALPH_HOME/judge-prompt.md"
    exit 1
  fi
  echo "Judge enabled (max $JUDGE_MAX_REJECTIONS rejections per story)"
fi

# --- Judge helper functions ---

get_rejection_count() {
  local story_id="$1"
  local count_file="$PROJECT_DIR/.ralph/judge-rejections-${story_id}.count"
  if [ -f "$count_file" ]; then
    cat "$count_file"
  else
    echo "0"
  fi
}

increment_rejection_count() {
  local story_id="$1"
  local count_file="$PROJECT_DIR/.ralph/judge-rejections-${story_id}.count"
  local current
  current=$(get_rejection_count "$story_id")
  echo $((current + 1)) > "$count_file"
}

clear_rejection_count() {
  local story_id="$1"
  rm -f "$PROJECT_DIR/.ralph/judge-rejections-${story_id}.count"
  rm -f "$PROJECT_DIR/.ralph/judge-feedback-${story_id}.md"
}

# --- run_judge function ---
# Returns 0 on PASS (or error fallthrough), 1 on FAIL
run_judge() {
  local story_id="$1"
  local pre_rev="$2"

  echo "  [Judge] Reviewing story $story_id..."

  # Extract story details from prd.json
  local story_json
  story_json=$(jq -r --arg id "$story_id" '.userStories[] | select(.id == $id)' "$PRD_FILE" 2>/dev/null || echo "")
  if [ -z "$story_json" ]; then
    echo "  [Judge] Warning: Could not find story $story_id in prd.json. Treating as PASS."
    return 0
  fi

  local story_title story_description acceptance_criteria
  story_title=$(echo "$story_json" | jq -r '.title // ""')
  story_description=$(echo "$story_json" | jq -r '.description // ""')
  acceptance_criteria=$(echo "$story_json" | jq -r '
    if .acceptanceCriteria then
      .acceptanceCriteria | to_entries | map("- " + .value) | join("\n")
    else
      "No acceptance criteria specified"
    end
  ')

  # Get diff
  local diff_output
  if [ -n "$pre_rev" ]; then
    diff_output=$(cd "$PROJECT_DIR" && jj diff --from "$pre_rev" --to @ --git 2>/dev/null || echo "")
  fi
  if [ -z "$diff_output" ]; then
    # Fallback: try git diff
    diff_output=$(cd "$PROJECT_DIR" && git diff HEAD~1 2>/dev/null || echo "")
  fi
  if [ -z "$diff_output" ]; then
    echo "  [Judge] Warning: No diff available. Treating as PASS."
    return 0
  fi

  # Build prompt from template
  local template
  template=$(cat "$RALPH_HOME/judge-prompt.md")
  local prompt
  prompt="${template//\{\{STORY_ID\}\}/$story_id}"
  prompt="${prompt//\{\{STORY_TITLE\}\}/$story_title}"
  prompt="${prompt//\{\{STORY_DESCRIPTION\}\}/$story_description}"
  prompt="${prompt//\{\{ACCEPTANCE_CRITERIA\}\}/$acceptance_criteria}"
  prompt="${prompt//\{\{DIFF\}\}/$diff_output}"

  # Run gemini
  local judge_output
  judge_output=$(gemini -p "$prompt" -o text 2>/dev/null || echo "")
  if [ -z "$judge_output" ]; then
    echo "  [Judge] Warning: Gemini returned empty output. Treating as PASS."
    return 0
  fi

  # Strip markdown fences if present
  judge_output=$(echo "$judge_output" | sed 's/^```json//; s/^```//; s/```$//' | tr -d '\r')
  # Trim leading/trailing whitespace
  judge_output=$(echo "$judge_output" | sed '/^[[:space:]]*$/d' | head -1)

  # Try to extract JSON - find the first { to last }
  local json_block
  json_block=$(echo "$judge_output" | grep -o '{.*}' | head -1 || echo "")
  if [ -z "$json_block" ]; then
    echo "  [Judge] Warning: Could not parse JSON from judge output. Treating as PASS."
    echo "  [Judge] Raw output: $judge_output"
    return 0
  fi

  # Parse verdict
  local verdict
  verdict=$(echo "$json_block" | jq -r '.verdict // ""' 2>/dev/null || echo "")
  if [ -z "$verdict" ]; then
    echo "  [Judge] Warning: No verdict in judge response. Treating as PASS."
    return 0
  fi

  if [ "$verdict" = "PASS" ]; then
    local reason
    reason=$(echo "$json_block" | jq -r '.reason // ""' 2>/dev/null || echo "")
    echo "  [Judge] PASS: $reason"
    return 0
  elif [ "$verdict" = "FAIL" ]; then
    local reason suggestion criteria_failed
    reason=$(echo "$json_block" | jq -r '.reason // ""' 2>/dev/null || echo "")
    suggestion=$(echo "$json_block" | jq -r '.suggestion // ""' 2>/dev/null || echo "")
    criteria_failed=$(echo "$json_block" | jq -r '.criteria_failed // [] | join(", ")' 2>/dev/null || echo "")

    echo "  [Judge] FAIL: $reason"
    echo "  [Judge] Failed criteria: $criteria_failed"

    # Write feedback file for Claude to read
    cat > "$PROJECT_DIR/.ralph/judge-feedback-${story_id}.md" <<FEEDBACK
# Judge Feedback for $story_id

## Verdict: FAIL

## Reason
$reason

## Failed Criteria
$criteria_failed

## Suggestion
$suggestion

## Instructions
Address the failed criteria above. Do not repeat the same approach that was rejected.
FEEDBACK

    # Revert passes to false in prd.json
    local tmp_prd
    tmp_prd=$(jq --arg id "$story_id" '
      .userStories = [.userStories[] | if .id == $id then .passes = false else . end]
    ' "$PRD_FILE")
    echo "$tmp_prd" > "$PRD_FILE"

    return 1
  else
    echo "  [Judge] Warning: Unknown verdict '$verdict'. Treating as PASS."
    return 0
  fi
}

# --- Archive previous run if branch changed ---
if [ -f "$LAST_BRANCH_FILE" ]; then
  CURRENT_BRANCH=$(jq -r '.branchName // empty' "$PRD_FILE" 2>/dev/null || echo "")
  LAST_BRANCH=$(cat "$LAST_BRANCH_FILE" 2>/dev/null || echo "")

  if [ -n "$CURRENT_BRANCH" ] && [ -n "$LAST_BRANCH" ] && [ "$CURRENT_BRANCH" != "$LAST_BRANCH" ]; then
    DATE=$(date +%Y-%m-%d)
    FOLDER_NAME=$(echo "$LAST_BRANCH" | sed 's|^ralph/||')
    ARCHIVE_FOLDER="$ARCHIVE_DIR/$DATE-$FOLDER_NAME"

    echo "Archiving previous run: $LAST_BRANCH"
    mkdir -p "$ARCHIVE_FOLDER"
    [ -f "$PRD_FILE" ] && cp "$PRD_FILE" "$ARCHIVE_FOLDER/"
    [ -f "$PROGRESS_FILE" ] && cp "$PROGRESS_FILE" "$ARCHIVE_FOLDER/"
    echo "   Archived to: $ARCHIVE_FOLDER"

    echo "# Ralph Progress Log" > "$PROGRESS_FILE"
    echo "Started: $(date)" >> "$PROGRESS_FILE"
    echo "---" >> "$PROGRESS_FILE"
  fi
fi

# Track current branch
CURRENT_BRANCH=$(jq -r '.branchName // empty' "$PRD_FILE" 2>/dev/null || echo "")
if [ -n "$CURRENT_BRANCH" ]; then
  echo "$CURRENT_BRANCH" > "$LAST_BRANCH_FILE"
fi

# Initialize progress file if it doesn't exist
if [ ! -f "$PROGRESS_FILE" ]; then
  echo "# Ralph Progress Log" > "$PROGRESS_FILE"
  echo "Started: $(date)" >> "$PROGRESS_FILE"
  echo "---" >> "$PROGRESS_FILE"
fi

echo "Starting Ralph - Max iterations: $MAX_ITERATIONS"
echo "  Project: $PROJECT_DIR"

for i in $(seq 1 $MAX_ITERATIONS); do
  echo ""
  echo "==============================================================="
  echo "  Ralph Iteration $i of $MAX_ITERATIONS"
  echo "==============================================================="

  # Find the next incomplete story
  NEXT_STORY=$(jq -r '[.userStories[] | select(.passes == false)] | sort_by(.priority) | .[0].id // empty' "$PRD_FILE")

  if [ -z "$NEXT_STORY" ]; then
    echo "All stories already complete!"
    exit 0
  fi

  echo "  Target story: $NEXT_STORY"

  # Capture jj revision before Claude runs (for judge diff baseline)
  PRE_REV=""
  if [ "$JUDGE_ENABLED" = true ]; then
    PRE_REV=$(cd "$PROJECT_DIR" && jj log -r @ --no-graph -T 'change_id' 2>/dev/null || echo "")
  fi

  # Capture progress file state before iteration
  PRE_LINES=$(wc -l < "$PROGRESS_FILE" 2>/dev/null | tr -d ' ')

  # Build prompt with story constraint injected
  BASE_PROMPT=$(cat "$RALPH_HOME/ralph-prompt.md")

  PROMPT="$BASE_PROMPT

---
## THIS ITERATION
You MUST only work on story **$NEXT_STORY**. Do NOT implement any other story. After completing $NEXT_STORY, stop immediately.
If progress.md contains a [CONTEXT EXHAUSTED] entry for $NEXT_STORY, continue from where it left off."

  # Run Claude Code with the dynamic prompt
  LOG_FILE="$LOG_DIR/iteration-$i.log"

  cd "$PROJECT_DIR" && echo "$PROMPT" | claude --dangerously-skip-permissions --print 2>&1 | tee "$LOG_FILE" || true

  OUTPUT=$(cat "$LOG_FILE")

  # Show what was added to progress.md this iteration
  POST_LINES=$(wc -l < "$PROGRESS_FILE" 2>/dev/null | tr -d ' ')
  if [ "$POST_LINES" -gt "$PRE_LINES" ]; then
    echo ""
    echo "--- Progress from iteration $i ---"
    tail -n +$((PRE_LINES + 1)) "$PROGRESS_FILE"
    echo "---"
  fi

  # --- Judge step (after Claude, before completion check) ---
  if [ "$JUDGE_ENABLED" = true ]; then
    # Check if target story now has passes: true
    STORY_PASSES=$(jq -r --arg id "$NEXT_STORY" '.userStories[] | select(.id == $id) | .passes' "$PRD_FILE" 2>/dev/null || echo "false")

    if [ "$STORY_PASSES" = "true" ]; then
      REJECTION_COUNT=$(get_rejection_count "$NEXT_STORY")

      if [ "$REJECTION_COUNT" -ge "$JUDGE_MAX_REJECTIONS" ]; then
        echo "  [Judge] Strike limit reached ($REJECTION_COUNT/$JUDGE_MAX_REJECTIONS). Auto-passing story $NEXT_STORY."
        echo "" >> "$PROGRESS_FILE"
        echo "## [Judge] $NEXT_STORY auto-passed after $REJECTION_COUNT rejections [HUMAN REVIEW NEEDED]" >> "$PROGRESS_FILE"
        echo "---" >> "$PROGRESS_FILE"
        clear_rejection_count "$NEXT_STORY"
      else
        if run_judge "$NEXT_STORY" "$PRE_REV"; then
          # PASS - clean up
          clear_rejection_count "$NEXT_STORY"
        else
          # FAIL - increment and re-loop
          increment_rejection_count "$NEXT_STORY"
          REJECTION_COUNT=$(get_rejection_count "$NEXT_STORY")
          echo "  [Judge] Rejection $REJECTION_COUNT of $JUDGE_MAX_REJECTIONS for story $NEXT_STORY"
          continue
        fi
      fi
    fi
  fi

  # Check for completion signal
  if echo "$OUTPUT" | grep -q "<promise>COMPLETE</promise>"; then
    echo ""
    echo "Ralph completed all tasks!"
    echo "Completed at iteration $i of $MAX_ITERATIONS"
    exit 0
  fi

  echo "Iteration $i complete. Continuing..."
  sleep 2
done

echo ""
echo "Ralph reached max iterations ($MAX_ITERATIONS) without completing all tasks."
echo "Check $PROGRESS_FILE for status."
exit 1
