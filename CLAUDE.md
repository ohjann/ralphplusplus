# Ralph Repo Notes

## Build
- `make build` — Go binary at `build/ralph`
- `pnpm --filter ./frontend build` — frontend bundle into
  `internal/viewer/dist/` (committed; RV-018 CI staleness guard rejects
  stale diffs)

Always run `make build` after verifications.

## Verification fixtures

**UI stories** (verifying the Ralph viewer itself):
- Start the viewer: `./build/ralph viewer --web`
- Extract URL + token from stdout
- Point the `rodney` skill at the URL

**Daemon-dependent stories** (live socket, live-badge, command proxies):
- Create a throwaway repo: `make scratch-repo`
- Start an idle daemon there (live socket, no API calls):
  `./build/ralph --dir /tmp/ralph-scratch --idle --daemon &`
- Verify against the daemon's socket / viewer proxy routes
- Stop it: `pkill -f 'ralph.*ralph-scratch'`

## Prompts

Implementer, judge, and architect prompts live in
`internal/assets/files/prompts/` and `internal/assets/files/`. Changes
affect all Ralph runs on all projects — keep them domain-neutral.
Project-specific fixtures (the section above) belong here in CLAUDE.md.
