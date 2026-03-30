# PRD Format

## prd.json Schema

```json
{
  "project": "MyApp",
  "branchName": "ralph/feature-name",
  "description": "Short description of the work",
  "buildCommand": "make build",
  "constraints": [
    "Use existing Drizzle ORM patterns for all migrations"
  ],
  "userStories": [
    {
      "id": "AB-001",
      "title": "Add status field to tasks table",
      "description": "As a developer, I need to store task status.",
      "acceptanceCriteria": [
        "Add status column with default 'pending'",
        "Migration runs successfully",
        "Typecheck passes"
      ],
      "priority": 1,
      "passes": false,
      "notes": "",
      "dependsOn": [],
      "approach": "Add column to existing tasks schema in db/schema.ts"
    },
    {
      "id": "AB-002",
      "title": "Display status badge on task cards",
      "description": "As a user, I want to see task status at a glance.",
      "acceptanceCriteria": [
        "Each task card shows colored status badge",
        "Typecheck passes"
      ],
      "priority": 2,
      "passes": false,
      "notes": "",
      "dependsOn": ["AB-001"],
      "approach": "Create a StatusBadge component, use in existing TaskCard"
    }
  ]
}
```

## Field Reference

| Field | Level | Purpose |
|-------|-------|---------|
| `buildCommand` | PRD | Custom build/compile command used in pre-judge compilation gate (default: `make build`) |
| `constraints` | PRD | Cross-cutting architectural decisions injected into every agent prompt |
| `dependsOn` | Story | Explicit dependency graph — skips the Claude DAG analysis call when present |
| `approach` | Story | Implementation strategy hint — guides the architect/implementer agents |

All fields are optional. When `dependsOn` is provided on any story, Ralph uses it directly for parallel scheduling instead of running a Claude analysis pass.

## Story Sizing

Each story must be completable in **one context window**. If Claude runs out of context mid-story, it produces broken code.

**Right-sized:** add a DB column, add a UI component, update server logic, add a filter

**Too big (split these):** "build the entire dashboard", "add authentication", "refactor the API"

## Story Ordering

Stories execute in priority order. Earlier stories must not depend on later ones:

1. Schema/database changes
2. Backend/API logic
3. UI components
4. Dashboards/aggregation views
