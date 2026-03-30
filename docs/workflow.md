# Workflow & Modes

## Planning: `--plan`

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

## Execution

Once `prd.json` exists, Ralph loops:

1. Picks the highest-priority story where `passes: false`
2. Spawns a fresh Claude Code instance to implement that story
3. Claude runs quality checks (typecheck, tests, etc.)
4. If checks pass, commits with jj and marks the story `passes: true`
5. Appends learnings to `progress.md`
6. Repeats until all stories pass or max iterations reached

## Parallel Execution: `--workers`

When stories are independent, Ralph can run them in parallel:

```bash
ralph --workers 3
ralph --workers auto    # scale to DAG width (max 5)
```

If stories have `dependsOn` fields in prd.json, Ralph uses them directly for scheduling. Otherwise, it runs a DAG analysis step where Claude examines the codebase to determine dependencies. Independent stories are scheduled across N workers, each in an isolated jj workspace.

Use `1-9` keys in the TUI to switch between worker output panels, or `<`/`>` to cycle through them.

## Simplification Pass (enabled by default)

After each story is implemented but before judge verification, a fast simplification pass scans the diff for duplicated logic, unnecessary complexity, dead code, and missed reuse opportunities — then fixes issues without changing behavior or expanding scope. Disable with `--no-simplify`.

## Fusion Mode (enabled by default)

For complex stories (architectural decisions with multiple valid approaches, many files, design trade-offs), Ralph automatically spawns competing implementations in parallel. A lightweight LLM call assesses story complexity, and complex stories get multiple independent workers that each implement the story differently. The judge then selects the best passing result.

```bash
ralph --no-fusion                   # disable fusion mode
ralph --fusion-workers 3            # 3 competing implementations (default: 2)
```

## Judge Mode (enabled by default)

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

## Interactive Mode

When no `prd.json` is present, Ralph starts in interactive mode with a task input bar. You can also submit tasks alongside a running PRD.

1. Type a task description and press `Enter`
2. A lightweight Claude call (Sonnet) assesses clarity — if ambiguous, up to 3 clarifying questions are shown
3. Answer any questions inline, then the task is dispatched as a `T-001`, `T-002`, etc. story
4. Tasks execute through the full worker pipeline (workspace isolation, memory, judge, merge)

Interactive tasks are included in checkpoints for crash recovery. On clean exit, sessions are saved to `.ralph/session-{timestamp}.json`.

## Quality Review (enabled by default)

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

## Stuck Detection + Hint Injection

If Claude gets stuck in a loop (repeatedly running the same command or editing the same file), Ralph:

1. Detects the pattern via tool call analysis
2. Shows a red status bar in the TUI with details (story, pattern, offending command)
3. Sends a macOS/terminal notification so you know even when not watching the TUI
4. Cancels the current Claude process
5. Generates a targeted "fix story" and inserts it before the stuck story
6. Continues with the fix story in the next iteration

When the stuck bar is showing, press `i` to inject a hint — a one-liner that gets included in the next iteration's prompt. This lets you nudge Claude in the right direction when you can see what it's doing wrong (e.g. "use the existing `fetchUser` helper, don't create a new one"). The hint is consumed after one use.
