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
  --notify <topic>                Send push notifications via ntfy.sh to given topic
  --ntfy-server <url>             Self-hosted ntfy server URL (default: https://ntfy.sh)
  --status-port <port>            Start remote status page on given port (disabled by default)
  --enable-monitoring             Enable ntfy + status page using .ralph/.env config
  --no-guy                        Disable sprite mascot overlay
  --idle                          Launch TUI without executing (display only)
  --help, -h                      Show help

Subcommands:
  ralph history                   Show recent run summaries (last 10)
  ralph history --all             Show all run history
  ralph history --stats           Show aggregate statistics across all runs
  ralph history --compare         Compare runs grouped by model
  ralph memory stats              Show memory file sizes and entry counts
  ralph memory consolidate        Manually trigger dream consolidation cycle
  ralph memory reset              Clear all memory files

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
  ralph --enable-monitoring                     Use .ralph/.env for ntfy + status page
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
| `m` | Toggle monitoring on/off |
| `p` | Enter interactive sprite mode |
| `y/n` | Resume from checkpoint / start fresh |
| `Enter` | Start execution (review phase) / Submit task / Resume |
| `Esc` | Exit input mode / cancel |

## Monitoring Setup

Ralph can send push notifications (ntfy.sh) and serve a status page.

### Quick Setup (`.ralph/.env`)

Configure once, then use `--enable-monitoring` to activate both:

```bash
mkdir -p .ralph
cat > .ralph/.env << 'EOF'
RALPH_NOTIFY_TOPIC=ralph-yourname-a8f3
RALPH_STATUS_PORT=8080
# RALPH_NTFY_SERVER=https://ntfy.my-server.ts.net  # optional, defaults to https://ntfy.sh
EOF
```

Install the ntfy app on your phone ([iOS](https://apps.apple.com/app/ntfy/id1625396347) / [Android](https://play.google.com/store/apps/details?id=io.heckel.ntfy)) and subscribe to the same topic.

Then just run:

```bash
ralph --enable-monitoring
```

Ralph prints the active monitoring config at startup:

```
Monitoring:
  Notifications: https://ntfy.sh/ralph-yourname-a8f3
  Status page:   http://localhost:8080
```

You can also set these values as OS environment variables (`RALPH_NOTIFY_TOPIC`, `RALPH_NTFY_SERVER`, `RALPH_STATUS_PORT`) or use the explicit flags (`--notify`, `--ntfy-server`, `--status-port`) which always take priority.

### Remote Access with Tailscale

Combined with [Tailscale](https://tailscale.com), the status page is accessible from your phone without port forwarding:

1. Install [Tailscale](https://tailscale.com/download) on your laptop and phone
2. Find your laptop's Tailscale IP: `tailscale ip -4` (e.g., `100.64.1.42`)
3. Open `http://100.64.1.42:8080` on your phone

The status page shows PRD name, current phase, run duration, story list with status/cost, and total cost. Updates live via SSE. JSON API at `/api/status`.

### Putting it together

```bash
# On your laptop (connected to Tailscale)
ralph --plan .claude/plans/my-feature.md --workers 3 --enable-monitoring
```

Then on your phone:
- **Status page**: `http://<tailscale-ip>:8080` for live progress
- **ntfy app**: push notifications for key events
- **ralph history**: check past runs when you're back at your laptop
