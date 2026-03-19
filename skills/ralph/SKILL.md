---
name: ralph
description: "Convert plans to prd.json format for the Ralph autonomous agent system. Use when converting a Claude Code plan, pasted plan text, or PRD markdown into Ralph's JSON format. Triggers on: convert this plan, ralph prd, create prd.json, turn this into ralph format, ralph json."
user-invocable: true
---

# Ralph PRD Converter

Converts Claude Code plans, pasted plan text, or PRD markdown into the `prd.json` format that Ralph uses for autonomous execution.

---

## Step 1: Find the Input

Check for input in this order:

1. **Claude Code plans** -- Look in `.claude/plans/` for markdown files. If any exist, list them with timestamps and offer the most recent one as the default. The user may pick a different one.
2. **Pasted text** -- The user may have pasted plan text directly in the conversation. Use that.
3. **PRD markdown file** -- The user may reference a `.md` file by path. Read it.

If none of the above are available, ask the user to provide a plan.

---

## Step 2: Where to Write prd.json

**CRITICAL: Always write `prd.json` to the PROJECT ROOT directory (the current working directory).**

- Correct: `./prd.json` (project root)
- WRONG: `.claude/prd.json`
- WRONG: `.claude/plans/prd.json`
- WRONG: `skills/ralph/prd.json`
- WRONG: Any subdirectory unless the user explicitly asks

Before writing, confirm the project root by checking for markers like `package.json`, `go.mod`, `.git`, etc. The file goes next to those, at the top level.

---

## Output Format

```json
{
  "project": "[Project Name]",
  "branchName": "ralph/[feature-name-kebab-case]",
  "description": "[Feature description from plan title/intro]",
  "repos": ["../other-repo"],
  "constraints": [
    "Cross-cutting constraint or architectural decision that applies to all stories",
    "e.g. 'Use the existing EventBus pattern for all new event handling'"
  ],
  "userStories": [
    {
      "id": "AB-001",
      "title": "[Story title]",
      "description": "As a [user], I want [feature] so that [benefit]",
      "acceptanceCriteria": [
        "Criterion 1",
        "Criterion 2",
        "Typecheck passes"
      ],
      "priority": 1,
      "passes": false,
      "notes": "",
      "dependsOn": [],
      "approach": "Brief implementation strategy hint for the builder agent"
    }
  ]
}
```

---

## Story Size: Coherent, Self-Contained Changes

**Each story should represent one coherent, logically related change.**

A well-sized story touches all the files needed to deliver a single concern end-to-end. Stories can span 5–15 files if the changes are all facets of the same logical change — what matters is that the change is self-contained and independently verifiable.

### Right-sized stories:
- Add a database column, update the server action that uses it, and adjust the UI that displays it (one feature, multiple layers)
- Refactor all API endpoints to use a new middleware pattern (one concern across many files)
- Add a UI component to an existing page with its supporting queries
- Update a server action with new logic and its tests

### Too big — split by concern:
- "Build the entire dashboard" — Split by concern: schema/migrations, data queries, UI components, filtering logic
- "Add authentication" — Split by concern: schema, middleware, login UI, session handling
- "Refactor the API" — Split if it mixes unrelated concerns (e.g., error handling + pagination + auth)

### Rule of thumb:
If a story's acceptance criteria require **unrelated types of work**, split it. If all criteria are **facets of the same change**, keep them together.

**Split** a story when it mixes concerns like schema changes AND unrelated UI work AND unrelated middleware — these are independent changes that should be verified separately.

**Keep together** a story that adds a new field to the schema, updates the query, and shows it in the UI — these are tightly coupled parts of one feature.

---

## Story Ordering & Dependencies

Stories execute in priority order. Earlier stories must not depend on later ones.

**Use the `dependsOn` field** to explicitly declare dependencies between stories. This lets Ralph skip an expensive Claude analysis step and run stories in parallel when they have no shared dependencies.

- `dependsOn` is an array of story IDs that must complete before this story can start
- Stories with no dependencies use an empty array: `"dependsOn": []`
- Every story referenced in `dependsOn` must exist in the PRD

**Correct order:**
1. Schema/database changes (migrations)
2. Server actions / backend logic
3. UI components that use the backend
4. Dashboard/summary views that aggregate data

**Wrong order:**
1. UI component (depends on schema that does not exist yet)
2. Schema change

---

## Acceptance Criteria: Must Be Verifiable

Each criterion must be something Ralph can CHECK, not something vague.

### Good criteria (verifiable):
- "Add `status` column to tasks table with default 'pending'"
- "Filter dropdown has options: All, Active, Completed"
- "Clicking delete shows confirmation dialog"
- "Typecheck passes"
- "Tests pass"

### Bad criteria (vague):
- "Works correctly"
- "User can do X easily"
- "Good UX"
- "Handles edge cases"

### Always include as final criterion:
```
"Typecheck passes"
```

For stories with testable logic, also include:
```
"Tests pass"
```

### For stories that change UI, also include:
```
"Verify in browser using rodney"
```

Frontend stories are NOT complete until visually verified. Ralph will use rodney to navigate to the page, interact with the UI, and confirm changes work.

---

## Conversion Rules

1. **Each user story becomes one JSON entry**
2. **IDs**: Sequential (AB-001, AB-002, etc.)
3. **Priority**: Based on dependency order, then document order
4. **All stories**: `passes: false` and empty `notes`
5. **branchName**: Derive from feature name, kebab-case, prefixed with `ralph/`
6. **Always add**: "Typecheck passes" to every story's acceptance criteria
7. **Cross-repo work**: If the plan mentions changes in multiple repositories, add a `repos` array with relative paths to the additional repos (the primary project dir is always included implicitly)
8. **dependsOn**: Set explicitly for every story. Stories with no dependencies get `"dependsOn": []`. This enables parallel execution and skips an expensive analysis step.
9. **approach**: If the plan specifies how to implement a story (e.g., "extend the middleware chain" or "use the existing EventBus"), capture it in the `approach` field. This guides the architect and implementer agents, reducing wasted exploration.
10. **constraints**: Extract cross-cutting architectural decisions or constraints from the plan intro/notes into the top-level `constraints` array. These are injected into every agent prompt.

---

## Translating Claude Code Plans

Claude Code plans (from `/plan`) often use a different structure than traditional PRDs. Here is how to translate them:

- **Plan steps/phases** become individual user stories, split further if any step is too large for one iteration.
- **Numbered task lists** within a step usually map to acceptance criteria for that story.
- **"Consider" or "open question" notes** in the plan are informational -- incorporate the decisions into acceptance criteria, do not leave them ambiguous.
- **File paths mentioned in the plan** are valuable context -- include them in the story description or notes so Ralph knows where to work.
- **Implementation strategy notes** (e.g., "use pattern X", "extend module Y") become the `approach` field for the relevant story.
- **Cross-cutting decisions** (e.g., "all new endpoints use middleware Z", "prefer approach A over B because of constraint C") go into the top-level `constraints` array.
- **Step dependencies** (e.g., "step 3 requires step 1") become `dependsOn` references between story IDs.

---

## Splitting Large Plans

If a plan has big features, split them:

**Original:**
> "Add user notification system"

**Split into:**
1. AB-001: Add notifications table to database
2. AB-002: Create notification service for sending notifications
3. AB-003: Add notification bell icon to header
4. AB-004: Create notification dropdown panel
5. AB-005: Add mark-as-read functionality
6. AB-006: Add notification preferences page

Each is one focused change that can be completed and verified independently.

---

## Example

**Input (Claude Code plan from `.claude/plans/task-status.md`):**
```markdown
# Task Status Feature

## Steps
1. Add status column to tasks table (pending/in_progress/done)
2. Show status badge on each task card with color coding
3. Add toggle to change status inline from the task list
4. Add filter dropdown to filter tasks by status
```

**Output `prd.json` (written to project root):**
```json
{
  "project": "TaskApp",
  "branchName": "ralph/task-status",
  "description": "Task Status Feature - Track task progress with status indicators",
  "constraints": [
    "Use existing Drizzle ORM patterns for migrations",
    "Status values must be a union type, not an enum, for type safety"
  ],
  "userStories": [
    {
      "id": "AB-001",
      "title": "Add status field to tasks table",
      "description": "As a developer, I need to store task status in the database.",
      "acceptanceCriteria": [
        "Add status column: 'pending' | 'in_progress' | 'done' (default 'pending')",
        "Generate and run migration successfully",
        "Typecheck passes"
      ],
      "priority": 1,
      "passes": false,
      "notes": "",
      "dependsOn": [],
      "approach": "Add column to existing tasks schema in db/schema.ts, generate migration with drizzle-kit"
    },
    {
      "id": "AB-002",
      "title": "Display status badge on task cards",
      "description": "As a user, I want to see task status at a glance.",
      "acceptanceCriteria": [
        "Each task card shows colored status badge",
        "Badge colors: gray=pending, blue=in_progress, green=done",
        "Typecheck passes",
        "Verify in browser using rodney"
      ],
      "priority": 2,
      "passes": false,
      "notes": "",
      "dependsOn": ["AB-001"],
      "approach": "Create a StatusBadge component, use it in the existing TaskCard component"
    },
    {
      "id": "AB-003",
      "title": "Add status toggle to task list rows",
      "description": "As a user, I want to change task status directly from the list.",
      "acceptanceCriteria": [
        "Each row has status dropdown or toggle",
        "Changing status saves immediately",
        "UI updates without page refresh",
        "Typecheck passes",
        "Verify in browser using rodney"
      ],
      "priority": 3,
      "passes": false,
      "notes": "",
      "dependsOn": ["AB-001"],
      "approach": "Add a server action for status updates, use optimistic updates in the UI"
    },
    {
      "id": "AB-004",
      "title": "Filter tasks by status",
      "description": "As a user, I want to filter the list to see only certain statuses.",
      "acceptanceCriteria": [
        "Filter dropdown: All | Pending | In Progress | Done",
        "Filter persists in URL params",
        "Typecheck passes",
        "Verify in browser using rodney"
      ],
      "priority": 4,
      "passes": false,
      "notes": "",
      "dependsOn": ["AB-002", "AB-003"],
      "approach": "Use URL search params for filter state, add WHERE clause to existing task query"
    }
  ]
}
```

---

## Archiving Previous Runs

**Before writing a new prd.json, check if there is an existing one from a different feature:**

1. Read the current `prd.json` (at project root) if it exists
2. Check if `branchName` differs from the new feature's branch name
3. If different AND `progress.txt` has content beyond the header:
   - Create archive folder: `archive/YYYY-MM-DD-feature-name/`
   - Copy current `prd.json` and `progress.txt` to archive
   - Reset `progress.txt` with fresh header

**The ralph.sh script handles this automatically** when you run it, but if you are manually updating prd.json between runs, archive first.

---

## Checklist Before Saving

Before writing prd.json, verify:

- [ ] **Output path is project root** (not `.claude/`, not `skills/`, not any subdirectory)
- [ ] **Previous run archived** (if prd.json exists with different branchName, archive it first)
- [ ] Each story is a coherent, self-contained change (one concern end-to-end)
- [ ] Stories are ordered by dependency (schema to backend to UI)
- [ ] Every story has "Typecheck passes" as criterion
- [ ] UI stories have "Verify in browser using rodney" as criterion
- [ ] Acceptance criteria are verifiable (not vague)
- [ ] No story depends on a later story
- [ ] Every story has `dependsOn` set (empty array `[]` if no dependencies)
- [ ] Cross-cutting decisions are captured in top-level `constraints`
- [ ] Implementation strategies from the plan are captured in `approach` fields
