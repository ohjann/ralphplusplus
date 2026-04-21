import { signal, computed } from '@preact/signals';
import { useEffect } from 'preact/hooks';
import { useLocation } from 'preact-iso';
import { apiGet, type RepoSummary, type RunListItem } from '../../lib/api';
import { probeReach } from '../../lib/live';
import { themeMode, setThemeMode, type ThemeMode } from '../../lib/theme';
import { PalettePicker } from './PalettePicker';
import { closeMobileNav } from '../../lib/mobileNav';
import { StartRunModal } from '../StartRunModal';
import ralphPortrait from '../../assets/ralph-portrait.png';

const HEARTBEAT_MS = 15_000;

const repos = signal<RepoSummary[]>([]);
const runsByRepo = signal<Record<string, RunListItem[]>>({});
const expanded = signal<Set<string>>(new Set());
const filterText = signal<string>('');
const loadingRuns = signal<Set<string>>(new Set());
const repoError = signal<string>('');
const reachByFP = signal<Record<string, boolean>>({});
const startRunOpen = signal<boolean>(false);

const filtered = computed(() => {
  const q = filterText.value.trim().toLowerCase();
  if (!q) return repos.value;
  return repos.value.filter(
    (r) => r.path.toLowerCase().includes(q) || r.name.toLowerCase().includes(q),
  );
});

const liveRepos = computed(() =>
  filtered.value.filter((r) => reachByFP.value[r.fp] === true),
);
const otherRepos = computed(() =>
  filtered.value.filter((r) => reachByFP.value[r.fp] !== true),
);

async function loadRepos() {
  try {
    const list = await apiGet<RepoSummary[]>('/api/repos');
    repos.value = list;
    repoError.value = '';
  } catch (e) {
    repoError.value = e instanceof Error ? e.message : String(e);
  }
}

async function probeAllRepos() {
  const list = repos.value;
  if (list.length === 0) return;
  const results = await Promise.all(
    list.map(async (r) => [r.fp, await probeReach(r.fp)] as const),
  );
  const next: Record<string, boolean> = {};
  for (const [fp, ok] of results) next[fp] = ok;
  reachByFP.value = next;
}

async function loadRuns(fp: string) {
  if (runsByRepo.value[fp]) return;
  const next = new Set(loadingRuns.value);
  next.add(fp);
  loadingRuns.value = next;
  try {
    const list = await apiGet<RunListItem[]>(`/api/repos/${fp}/runs`);
    runsByRepo.value = { ...runsByRepo.value, [fp]: list };
  } catch {
    runsByRepo.value = { ...runsByRepo.value, [fp]: [] };
  } finally {
    const rest = new Set(loadingRuns.value);
    rest.delete(fp);
    loadingRuns.value = rest;
  }
}

function toggleRepo(fp: string) {
  const next = new Set(expanded.value);
  if (next.has(fp)) {
    next.delete(fp);
  } else {
    next.add(fp);
    void loadRuns(fp);
  }
  expanded.value = next;
}

function runIdFromPath(path: string): string {
  const m = /^\/repos\/[^/]+\/runs\/([^/]+)/.exec(path);
  return m ? m[1] : '';
}

function fmtRel(iso: string): string {
  try {
    const d = new Date(iso);
    const now = new Date();
    const same = (a: Date, b: Date) =>
      a.getFullYear() === b.getFullYear() &&
      a.getMonth() === b.getMonth() &&
      a.getDate() === b.getDate();
    const yday = new Date(now);
    yday.setDate(yday.getDate() - 1);
    const hhmm = d.toLocaleTimeString([], {
      hour: '2-digit',
      minute: '2-digit',
    });
    if (same(d, now)) return hhmm;
    if (same(d, yday)) return `yday ${hhmm}`;
    return `${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  } catch {
    return iso;
  }
}

const RUN_KIND_LABEL: Record<string, string> = {
  daemon: 'Daemon',
  'ad-hoc': 'Ad-hoc',
  retro: 'Retro',
  'memory-consolidate': 'Memory',
  unknown: 'Other',
};

// Pair-and-hide: a `ralph --web` invocation produces two manifests — the
// daemon (where the work is) and an ad-hoc TUI session (bookkeeping, often
// empty). Showing both in the sidebar makes every run look like a duplicate.
// For any daemon run that has an ad-hoc run starting within 10s for the same
// repo, we hide the ad-hoc half. Solo ad-hoc runs (no nearby daemon) stay
// visible in case the user genuinely interacted with the TUI alone.
const PAIR_WINDOW_MS = 10_000;

function hidePairedAdHoc(runs: RunListItem[]): RunListItem[] {
  const daemonTimes: number[] = runs
    .filter((r) => r.kind === 'daemon')
    .map((r) => Date.parse(r.startTime))
    .filter((t) => !Number.isNaN(t));
  return runs.filter((r) => {
    if (r.kind !== 'ad-hoc') return true;
    const t = Date.parse(r.startTime);
    if (Number.isNaN(t)) return true;
    return !daemonTimes.some((d) => Math.abs(d - t) <= PAIR_WINDOW_MS);
  });
}

function groupByKind(runs: RunListItem[]): Array<[string, RunListItem[]]> {
  const visible = hidePairedAdHoc(runs);
  const groups: Record<string, RunListItem[]> = {};
  for (const r of visible) {
    const k = r.kind || 'unknown';
    (groups[k] ??= []).push(r);
  }
  for (const k of Object.keys(groups)) {
    groups[k].sort((a, b) => b.startTime.localeCompare(a.startTime));
  }
  const order = ['daemon', 'ad-hoc', 'retro', 'memory-consolidate', 'unknown'];
  return order
    .filter((k) => groups[k])
    .map((k) => [k, groups[k]] as [string, RunListItem[]])
    .concat(
      Object.entries(groups).filter(([k]) => !order.includes(k)),
    );
}

export function Sidebar() {
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      await loadRepos();
      if (!cancelled) void probeAllRepos();
    })();
    const id = setInterval(() => {
      if (!cancelled) void probeAllRepos();
    }, HEARTBEAT_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, []);

  return (
    <aside
      style={{
        width: '100%',
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
        background: 'var(--bg-sidebar)',
        color: 'var(--sidebar-fg)',
        minWidth: 0,
        overflow: 'hidden',
        padding: '4px 6px 0 18px',
      }}
    >
      <Brand />
      <StartRunButton />
      <FilterInput />
      {startRunOpen.value && (
        <StartRunModal
          onClose={() => {
            startRunOpen.value = false;
            void loadRepos().then(probeAllRepos);
          }}
        />
      )}
      <nav
        style={{
          flex: 1,
          overflow: 'auto',
          padding: '4px 0 12px',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--section-gap)',
        }}
      >
        <Section label="views">
          <NavRow href="/" label="Home" />
        </Section>

        {liveRepos.value.length > 0 && (
          <Section label="active">
            {liveRepos.value.map((r) => (
              <RepoRow key={r.fp} repo={r} />
            ))}
          </Section>
        )}

        <Section label="all repositories" counter={filtered.value.length}>
          {repoError.value && (
            <div
              style={{
                fontSize: 11,
                padding: '4px 10px',
                color: 'var(--err)',
              }}
            >
              Failed to load: {repoError.value}
            </div>
          )}
          {otherRepos.value.map((r) => (
            <RepoRow key={r.fp} repo={r} />
          ))}
          {filtered.value.length === 0 && !repoError.value && (
            <div
              style={{
                fontStyle: 'italic',
                color: 'var(--sidebar-fg-faint)',
                padding: '6px 10px',
                fontSize: 12,
              }}
            >
              {repos.value.length === 0
                ? 'no repos yet'
                : `no repos match "${filterText.value}"`}
            </div>
          )}
        </Section>
      </nav>
      <Footer />
    </aside>
  );
}

function Brand() {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        padding: '18px 10px 16px 6px',
      }}
    >
      <img
        src={ralphPortrait}
        alt="Ralph"
        style={{
          width: 40,
          height: 40,
          borderRadius: '50%',
          objectFit: 'cover',
          flexShrink: 0,
          border: '1px solid var(--sidebar-border)',
        }}
      />
      <div style={{ minWidth: 0 }}>
        <div
          style={{
            fontWeight: 600,
            fontSize: 13.5,
            letterSpacing: '-0.005em',
            color: 'var(--sidebar-fg)',
          }}
        >
          Ralph Viewer
        </div>
        <div
          style={{
            fontSize: 11.5,
            color: 'var(--sidebar-fg-muted)',
            marginTop: 1,
          }}
        >
          Autonomous agent runner
        </div>
      </div>
    </div>
  );
}

function StartRunButton() {
  return (
    <div style={{ padding: '0 4px 8px 4px' }}>
      <button
        onClick={() => (startRunOpen.value = true)}
        style={{
          width: '100%',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          gap: 6,
          padding: '6px 10px',
          fontSize: 12,
          fontWeight: 600,
          border: '1px solid var(--accent-border)',
          borderRadius: 6,
          background: 'var(--accent-soft)',
          color: 'var(--accent-ink)',
        }}
      >
        <span style={{ fontSize: 13, lineHeight: 1 }}>＋</span>
        Start a run
      </button>
    </div>
  );
}

function FilterInput() {
  return (
    <div
      style={{
        margin: '0 4px 10px 4px',
        padding: '5px 8px',
        display: 'flex',
        alignItems: 'center',
        gap: 7,
        background: 'var(--sidebar-hover)',
        border: '1px solid var(--sidebar-border)',
        borderRadius: 6,
      }}
    >
      <svg
        width="12"
        height="12"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        style={{ color: 'var(--sidebar-fg-ghost)' }}
      >
        <circle cx="7" cy="7" r="4.5" />
        <path d="M10.5 10.5L14 14" strokeLinecap="round" />
      </svg>
      <input
        placeholder="Filter repos…"
        value={filterText.value}
        onInput={(e) =>
          (filterText.value = (e.currentTarget as HTMLInputElement).value)
        }
        style={{
          flex: 1,
          minWidth: 0,
          background: 'transparent',
          border: 0,
          outline: 'none',
          fontSize: 12,
          color: 'var(--sidebar-fg)',
        }}
      />
      {filterText.value && (
        <button
          onClick={() => (filterText.value = '')}
          style={{
            color: 'var(--sidebar-fg-ghost)',
            fontSize: 14,
            lineHeight: 1,
          }}
          aria-label="Clear filter"
        >
          ×
        </button>
      )}
    </div>
  );
}

function Section({
  label,
  counter,
  children,
}: {
  label: string;
  counter?: number;
  children: preact.ComponentChildren;
}) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <div
        style={{
          padding: '0 10px 4px',
          fontSize: 'var(--fs-label)',
          textTransform: 'uppercase',
          letterSpacing: '0.11em',
          color: 'var(--sidebar-fg-faint)',
          fontWeight: 600,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
        }}
      >
        <span>{label}</span>
        {counter != null && (
          <span
            style={{
              fontFamily: 'var(--font-mono)',
              fontSize: 10.5,
              color: 'var(--sidebar-fg-ghost)',
              letterSpacing: 0,
            }}
          >
            {counter}
          </span>
        )}
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
        {children}
      </div>
    </div>
  );
}

function NavRow({ href, label }: { href: string; label: string }) {
  const loc = useLocation();
  const active = loc.path === href;
  return (
    <a
      href={href}
      onClick={closeMobileNav}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 9,
        padding: '6px 10px',
        borderRadius: 6,
        fontSize: 'var(--fs-ui)',
        minHeight: 'var(--row-h)',
        textAlign: 'left',
        color: 'var(--sidebar-fg)',
        textDecoration: 'none',
        background: active ? 'var(--sidebar-active)' : 'transparent',
      }}
    >
      {label}
    </a>
  );
}

function RepoRow({ repo }: { repo: RepoSummary }) {
  const isOpen = expanded.value.has(repo.fp);
  const runs = runsByRepo.value[repo.fp];
  const loading = loadingRuns.value.has(repo.fp);
  const daemonReachable = reachByFP.value[repo.fp] === true;
  const dotClass = daemonReachable ? 'dot ok live' : 'dot';

  return (
    <div>
      <button
        onClick={() => toggleRepo(repo.fp)}
        style={{
          width: '100%',
          display: 'flex',
          alignItems: 'center',
          gap: 9,
          padding: '6px 10px',
          borderRadius: 6,
          fontSize: 'var(--fs-ui)',
          minHeight: 'var(--row-h)',
          textAlign: 'left',
          background: 'transparent',
          transition: 'background 80ms',
        }}
        onMouseEnter={(e) =>
          ((e.currentTarget as HTMLElement).style.background =
            'var(--sidebar-hover)')
        }
        onMouseLeave={(e) =>
          ((e.currentTarget as HTMLElement).style.background = 'transparent')
        }
      >
        <span class="caret" style={{ color: 'var(--sidebar-fg-ghost)' }}>
          {isOpen ? '▾' : '▸'}
        </span>
        <span
          class={dotClass}
          title={daemonReachable ? 'Daemon reachable' : 'Daemon offline'}
          style={{ marginRight: 1 }}
        />
        <span
          style={{
            flex: 1,
            whiteSpace: 'nowrap',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            color: 'var(--sidebar-fg)',
            fontWeight: 500,
          }}
        >
          {repo.name}
        </span>
        <span
          title={`${repo.runCount} total invocations in this repo (may exceed stored runs)`}
          style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 10.5,
            color: 'var(--sidebar-fg-faint)',
            background: 'var(--sidebar-hover)',
            border: '1px solid var(--sidebar-border)',
            borderRadius: 4,
            padding: '0px 5px',
            minWidth: 20,
            textAlign: 'center',
            lineHeight: 1.45,
          }}
        >
          {repo.runCount}
        </span>
      </button>

      {isOpen && (
        <div
          style={{
            marginLeft: 18,
            paddingLeft: 8,
            borderLeft: '1px dashed var(--sidebar-border)',
            marginTop: 2,
            marginBottom: 4,
          }}
        >
          <div
            class="mono"
            style={{
              fontSize: 10.5,
              color: 'var(--sidebar-fg-faint)',
              padding: '2px 6px',
            }}
          >
            {repo.path}
          </div>
          <div
            style={{ display: 'flex', gap: 8, padding: '2px 6px 4px' }}
          >
            <a
              href={`/repos/${repo.fp}/meta`}
              style={{
                fontSize: 10,
                color: 'var(--sidebar-fg-muted)',
                textTransform: 'uppercase',
                letterSpacing: '0.08em',
                textDecoration: 'none',
              }}
            >
              Meta
            </a>
            <a
              href={`/repos/${repo.fp}/settings`}
              style={{
                fontSize: 10,
                color: 'var(--sidebar-fg-muted)',
                textTransform: 'uppercase',
                letterSpacing: '0.08em',
                textDecoration: 'none',
              }}
            >
              Settings
            </a>
          </div>
          {loading && !runs && (
            <div
              style={{
                fontSize: 11,
                color: 'var(--sidebar-fg-faint)',
                padding: '3px 8px',
              }}
            >
              Loading runs…
            </div>
          )}
          {runs && runs.length === 0 && (
            <div
              style={{
                fontStyle: 'italic',
                color: 'var(--sidebar-fg-faint)',
                fontSize: 11,
                padding: '3px 8px',
              }}
            >
              no runs yet
            </div>
          )}
          {runs &&
            runs.length > 0 &&
            groupByKind(runs).map(([kind, items]) => (
              <div key={kind} style={{ marginBottom: 3 }}>
                <div
                  style={{
                    fontSize: 9.5,
                    textTransform: 'uppercase',
                    letterSpacing: '0.1em',
                    color: 'var(--sidebar-fg-ghost)',
                    fontWeight: 600,
                    padding: '4px 8px 2px',
                  }}
                >
                  {RUN_KIND_LABEL[kind] ?? kind}
                </div>
                {items.map((run) => (
                  <RunRow key={run.runId} fp={repo.fp} run={run} />
                ))}
              </div>
            ))}
        </div>
      )}
    </div>
  );
}

function RunRow({ fp, run }: { fp: string; run: RunListItem }) {
  const loc = useLocation();
  const href = `/repos/${fp}/runs/${run.runId}`;
  const active = runIdFromPath(loc.path) === run.runId;
  const claimsRunning = run.status === 'running';
  const daemonReachable = reachByFP.value[fp] === true;
  const isLive = claimsRunning && daemonReachable;
  const dotClass = isLive
    ? 'dot ok live'
    : run.status === 'failed'
      ? 'dot err'
      : run.status === 'interrupted'
        ? 'dot warn'
        : 'dot';
  const dotTitle = isLive
    ? 'Live'
    : claimsRunning
      ? 'Manifest says running, but daemon is unreachable'
      : run.status;
  return (
    <a
      href={href}
      onClick={closeMobileNav}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 7,
        padding: '3px 8px',
        minHeight: 22,
        borderRadius: 4,
        textDecoration: 'none',
        color: 'var(--sidebar-fg)',
        background: active ? 'var(--sidebar-active)' : 'transparent',
        borderLeft: active
          ? '2px solid var(--sidebar-fg)'
          : '2px solid transparent',
      }}
    >
      <span class={dotClass} title={dotTitle} />
      <span
        style={{
          fontSize: 11.5,
          color: 'var(--sidebar-fg)',
          whiteSpace: 'nowrap',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          maxWidth: 110,
        }}
        title={run.runId}
      >
        {run.displayName || run.runId.slice(0, 12)}
      </span>
      <span
        style={{
          flex: 1,
          fontSize: 11,
          color: 'var(--sidebar-fg-faint)',
          whiteSpace: 'nowrap',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
        }}
      >
        {fmtRel(run.startTime)}
      </span>
    </a>
  );
}

function Footer() {
  return (
    <div
      style={{
        padding: '12px 8px 14px',
        borderTop: '1px solid var(--sidebar-border)',
        marginTop: 4,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 8,
      }}
    >
      <PalettePicker />
      <ThemeIcons />
    </div>
  );
}

function ThemeIcons() {
  const current = themeMode.value;
  const Btn = ({
    m,
    label,
    glyph,
  }: {
    m: ThemeMode;
    label: string;
    glyph: string;
  }) => (
    <button
      title={label}
      onClick={() => setThemeMode(m)}
      style={{
        width: 26,
        height: 26,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        borderRadius: 6,
        color:
          current === m
            ? 'var(--sidebar-fg)'
            : 'var(--sidebar-fg-faint)',
        background:
          current === m ? 'var(--sidebar-hover)' : 'transparent',
        fontSize: 13,
      }}
    >
      {glyph}
    </button>
  );
  return (
    <div style={{ display: 'flex', gap: 2 }}>
      <Btn m="light" label="Light" glyph="☀" />
      <Btn m="system" label="Match system" glyph="◐" />
      <Btn m="dark" label="Dark" glyph="☾" />
    </div>
  );
}
