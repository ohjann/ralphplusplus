# Configuration

## CLI Reference

```
Usage: ralph [options] [max_iterations]

Options:
  --dir <path>                    Project directory (default: current directory)
  --plan <path>                   Generate prd.json from a plan file, review, then execute
  --workers <n|auto>              Parallel workers, or 'auto' to scale to DAG width (default: 1 = serial)
  --workspace-base <path>         Base directory for worker workspaces (default: /tmp/ralph-workspaces)
  --no-architect                  Skip architect phase for all stories
  --no-simplify                   Skip per-story simplification pass
  --no-fusion                     Disable automatic fusion mode for complex stories
  --fusion-workers <n>            Competing implementations per complex story (default: 2)
  --no-judge                      Disable judge verification (enabled by default)
  --judge-max-rejections <n>      Max rejections before auto-pass (default: 2)
  --no-quality-review             Disable final quality review (enabled by default)
  --quality-workers <n>           Parallel quality reviewers (default: 3)
  --quality-max-iterations <n>    Max review-fix cycles (default: 2)
  --model <name>                  Override model for all roles
  --architect-model <name>        Override model for architect role only
  --implementer-model <name>      Override model for implementer role only
  --utility-model <name>          Override model for utility tasks (default: haiku)
  --story-timeout <minutes>       Max wall clock minutes per story before cancellation (default: 0 = no limit)
  --memory-disable                Disable memory injection
  --web                           Launch the local web viewer (singleton per host) and print its URL
  --notify                        Enable push notifications via ntfy
  --notify-topic <topic>          ntfy topic to publish to (default: RALPH_NOTIFY_TOPIC)
  --ntfy-server <url>             Self-hosted ntfy server URL (default: https://ntfy.sh)
  --enable-monitoring             DEPRECATED alias for --web --notify (prints a warning)
  --no-guy                        Disable sprite mascot overlay
  --idle                          Launch TUI without executing (display only)
  --help, -h                      Show help

Daemon:
  --daemon                        Run as background daemon (no TUI, coordination + API only)
  --kill                          Send SIGTERM to running daemon and exit
  --idle-timeout <duration>       Auto-shutdown after idle (no work + no clients) for duration (default: 5m, 0 = disabled)

Subcommands:
  ralph history                   Show recent run summaries (last 10)
  ralph history --all             Show all run history
  ralph history --stats           Show aggregate statistics across all runs
  ralph history --compare         Compare runs grouped by model
  ralph history --compare --by flags
                                  Compare runs grouped by feature-flag configuration
                                  (groups by --no-architect, --no-fusion, etc.)
  ralph memory stats              Show memory file sizes and entry counts
  ralph memory consolidate        Manually trigger dream consolidation cycle
  ralph memory reset              Clear all memory files

Client Commands (connect to running daemon):
  ralph status                    Show current daemon state
  ralph logs                      Stream daemon events to stdout
  ralph hint <worker-id> "text"   Send a hint to a worker
  ralph pause                     Pause all workers
  ralph resume                    Resume paused workers

Arguments:
  max_iterations                  Max loop iterations (default: 1.5x story count)

Examples:
  ralph                                         Run all stories serially
  ralph 5                                       Run with max 5 iterations
  ralph --plan .claude/plans/my-plan.md         Plan, review, then execute
  ralph --workers 3                             Run up to 3 stories in parallel
  ralph --workers auto                          Scale workers to DAG width (max 5)
  ralph --no-judge                               Run without judge verification
  ralph --no-quality-review                     Run without final quality gate
  ralph --plan plan.md --workers 2              Full pipeline
  ralph --web --notify                          Launch the web viewer and enable push notifications
```

## TUI Keybindings

| Key | Action |
|-----|--------|
| `q` | Quit (press twice during execution) |
| `Ctrl+C` | Force quit (cancels all workers) |
| `Tab` | Switch active panel |
| `j/k` or `↑/↓` | Scroll active panel / select story |
| `PgUp/PgDn` | Page scroll |
| `Enter/l/→` | Expand story details (stories panel) |
| `h/←` | Collapse story details (stories panel) |
| `[/]` | Cycle context panel tabs |
| `1-9` | Switch worker view (parallel mode) |
| `</>` or `,/.` | Cycle previous/next worker tab |
| `t` | Enter task input mode |
| `i` | Inject hint (when stuck bar is showing) |
| `s` | Enter settings mode |
| `p` | Enter interactive sprite mode |
| `y/n` | Resume from checkpoint / start fresh |
| `Enter` | Start execution (review phase) / Submit task / Resume |
| `Esc` | Exit input mode / cancel |

## Monitoring Setup

Ralph can send push notifications (ntfy.sh) and serve a local web viewer
for browsing past runs.

### Push Notifications (ntfy)

Pick a topic name and store it in `.ralph/.env` so you don't have to
pass it every invocation:

```bash
mkdir -p .ralph
cat > .ralph/.env << 'EOF'
RALPH_NOTIFY_TOPIC=ralph-yourname-a8f3
# RALPH_NTFY_SERVER=https://ntfy.my-server.ts.net  # optional, defaults to https://ntfy.sh
EOF
```

Install the ntfy app on your phone ([iOS](https://apps.apple.com/app/ntfy/id1625396347) / [Android](https://play.google.com/store/apps/details?id=io.heckel.ntfy)) and subscribe to the same topic.

Then run:

```bash
ralph --notify
```

Flags take priority over env values: `--notify-topic`, `--ntfy-server`.

### Web Viewer

`--web` launches a singleton web viewer on localhost and prints a URL with
an access token. A second `ralph --web` on the same host reuses the running
viewer and reprints the URL:

```bash
ralph --web
# ✦ Ralph web viewer: http://127.0.0.1:54321/?token=…
```

You can also start the viewer directly and leave it running across runs:

```bash
ralph viewer                        # loopback, auto-opens browser
ralph viewer --port 8123            # pin a specific port
ralph viewer --no-open              # print URL only
```

The loopback URL binds to `127.0.0.1` and is gated by a per-user token at
`<userdata>/ralph/viewer.token` (mode `0600`). The URL printed on start
includes the token as a `?token=...` query parameter.

### Remote access via Tailscale

To reach the viewer from your phone or another machine without typing a
token, add `--tailscale`. The viewer joins your tailnet as a node (named
`ralph` by default) via tsnet, and peers on the tailnet reach it at
`http://ralph/` — Tailscale's mutual auth is the access boundary.

```bash
ralph viewer --tailscale
ralph viewer --tailscale --tailscale-hostname myralph
ralph viewer --tailscale --tailscale-port 8080
```

First launch prints a Tailscale login link to authorize the node; state
persists under `<userdata>/ralph/tsnet/<hostname>/` so subsequent launches
reconnect silently. The loopback listener keeps running in parallel, so
local browsers still use the token-gated URL.

Push notifications (`--notify`) include a `Click` header pointing at the
viewer's `/repos/<fp>` page — tailnet URL when `--tailscale` is on,
loopback URL otherwise. Tapping a notification on your phone opens the
relevant repo directly.

### Putting it together

```bash
ralph --plan .claude/plans/my-feature.md --workers 3 --web --notify
```

Then:
- **Web viewer**: open the printed `http://127.0.0.1:…` URL
- **ntfy app**: push notifications for key events
- **ralph history**: check past runs from the CLI
