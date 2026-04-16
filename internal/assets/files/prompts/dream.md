# Dream: Memory Consolidation

You are performing a dream — a reflective pass over Ralph's memory files. Consolidate accumulated learnings into durable, well-organized memories so future runs benefit from cleaner, more relevant context.

## Current Learnings (learnings.md)

{{LEARNINGS}}

## Current PRD Learnings (prd-learnings.md)

{{PRD_LEARNINGS}}

## Recent Run Summaries

{{RUN_SUMMARIES}}

## Instructions

### Phase 1 — Orient

- Read the current learnings and PRD learnings above
- Understand current entries, categories, and confirmation counts

### Phase 2 — Gather Signal

- Review the run summaries above
- Identify which existing learnings were confirmed or contradicted
- Note any new patterns not yet captured

### Phase 3 — Consolidate

For each memory file, produce an updated version that:
- Merges duplicate or overlapping lessons into single entries
- Removes lessons contradicted by more recent evidence
- Drops lessons with zero confirmations older than 10 runs
- Updates confirmation counts based on recent run evidence
- Converts any relative dates to absolute dates
- Preserves high-confidence, repeatedly-confirmed lessons

### Phase 4 — Prune

- Keep each file under {{MAX_ENTRIES}} entries
- If over limit, drop lowest-confirmation entries first
- Ensure entries remain well-categorised and clearly written

## Entry Format

Learnings must follow this format:

```
### L-{NNN}
- **Run:** {project_name}
- **Stories:** {comma-separated story IDs}
- **Confirmed:** {N} times
- **Category:** {testing, architecture, sizing, ordering, tooling, debugging, patterns}

{Description}
```

PRD learnings are **global** (shared across projects) and must be general principles, not tied to specific stories or projects. Strip out any project-specific references (story IDs, file names, project names) during consolidation.

```
### P-{NNN}
- **Confirmed:** {N} times
- **Category:** {sizing, ordering, criteria, constraints, dependencies, scope}

{A general, reusable lesson about PRD structure.}
```

## Rules

- Write COMPLETE replacement files — not patches or diffs
- Preserve the header line (# Learnings or # PRD Learnings) at the top of each file
- Re-number entries sequentially (L-001, L-002, ... and P-001, P-002, ...)
- If there are no entries to write, write just the header line
- Do NOT invent new learnings during consolidation — only merge, prune, and update existing ones
- After writing the files, return a brief summary of what you consolidated, updated, or pruned
