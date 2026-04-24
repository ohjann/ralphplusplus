import { useEffect } from 'preact/hooks';
import { signal } from '@preact/signals';
import { apiGet } from '../lib/api';

interface GlobalStatsTotals {
  repos: number;
  runs: number;
  totalCost: number;
  durationMinutes: number;
  totalIterations: number;
  storiesTotal: number;
  storiesCompleted: number;
  storiesFailed: number;
  firstPassRate: number;
}

interface ActivityPoint {
  date: string;
  runs: number;
  cost: number;
}

interface RepoStatsSummary {
  fp: string;
  name: string;
  path: string;
  runs: number;
  totalCost: number;
  storiesCompleted: number;
  storiesFailed: number;
  lastSeen: string;
}

interface GlobalStats {
  totals: GlobalStatsTotals;
  runsByKind: Record<string, number>;
  activityByDay: ActivityPoint[];
  byRepo: RepoStatsSummary[];
}

const stats = signal<GlobalStats | null>(null);
const loading = signal<boolean>(false);
const error = signal<string>('');

async function load() {
  loading.value = true;
  error.value = '';
  try {
    stats.value = await apiGet<GlobalStats>('/api/stats/global');
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

export function Home() {
  useEffect(() => {
    void load();
  }, []);

  if (loading.value && !stats.value) {
    return (
      <div style={{ padding: 32, fontSize: 13, color: 'var(--fg-faint)' }}>
        Loading stats…
      </div>
    );
  }
  if (error.value) {
    return (
      <div style={{ padding: 32, fontSize: 13, color: 'var(--err)' }}>
        Failed to load: {error.value}
      </div>
    );
  }
  const s = stats.value;
  if (!s) return null;

  return (
    <div style={{ padding: '22px 28px 80px', minHeight: '100%' }}>
      <div style={{ maxWidth: 1080, margin: '0 auto' }}>
        <Header totals={s.totals} />
        <TotalsGrid totals={s.totals} />
        <ActivityChart points={s.activityByDay} />
        <KindsAndRepos s={s} />
      </div>
    </div>
  );
}

function Header({ totals }: { totals: GlobalStatsTotals }) {
  return (
    <header style={{ marginBottom: 22 }}>
      <div
        style={{
          fontSize: 12,
          color: 'var(--fg-faint)',
          fontFamily: 'var(--font-mono)',
          letterSpacing: '0.04em',
          textTransform: 'uppercase',
          marginBottom: 8,
        }}
      >
        Ralph Viewer · localhost
      </div>
      <h1
        style={{
          fontSize: 26,
          fontWeight: 600,
          letterSpacing: '-0.02em',
          lineHeight: 1.2,
          margin: '0 0 6px',
          color: 'var(--fg)',
        }}
      >
        {totals.repos > 0 ? 'Overview' : 'No runs yet'}
      </h1>
      <p
        style={{
          fontSize: 14,
          color: 'var(--fg-muted)',
          margin: 0,
          lineHeight: 1.5,
        }}
      >
        {totals.repos > 0
          ? `${totals.runs.toLocaleString()} runs across ${totals.repos} ${totals.repos === 1 ? 'repo' : 'repos'}. Pick one from the sidebar to dig in.`
          : 'Start a run to see aggregated stats here.'}
      </p>
    </header>
  );
}

function TotalsGrid({ totals }: { totals: GlobalStatsTotals }) {
  const completionRate =
    totals.storiesTotal > 0
      ? totals.storiesCompleted / totals.storiesTotal
      : 0;
  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))',
        gap: 8,
        marginBottom: 24,
      }}
    >
      <StatCard label="Repos" value={fmtInt(totals.repos)} />
      <StatCard label="Runs" value={fmtInt(totals.runs)} />
      <StatCard label="Total cost" value={`$${totals.totalCost.toFixed(2)}`} accent />
      <StatCard label="Wall clock" value={fmtDuration(totals.durationMinutes)} />
      <StatCard label="Iterations" value={fmtInt(totals.totalIterations)} />
      <StatCard label="Stories done" value={fmtInt(totals.storiesCompleted)} />
      <StatCard
        label="Stories failed"
        value={fmtInt(totals.storiesFailed)}
        tone={totals.storiesFailed > 0 ? 'warn' : undefined}
      />
      <StatCard
        label="Completion rate"
        value={totals.storiesTotal > 0 ? `${Math.round(completionRate * 100)}%` : '—'}
      />
      <StatCard
        label="First-pass rate"
        value={totals.storiesTotal > 0 ? `${Math.round(totals.firstPassRate * 100)}%` : '—'}
      />
    </div>
  );
}

function StatCard({
  label,
  value,
  tone,
  accent,
}: {
  label: string;
  value: string;
  tone?: 'warn';
  accent?: boolean;
}) {
  const color =
    tone === 'warn' ? 'var(--warn)' : accent ? 'var(--accent-ink)' : 'var(--fg)';
  return (
    <div
      style={{
        border: '1px solid var(--border)',
        borderRadius: 8,
        padding: '10px 12px',
        background: 'var(--bg-elev)',
        minWidth: 0,
      }}
    >
      <div
        style={{
          fontSize: 10.5,
          color: 'var(--fg-faint)',
          textTransform: 'uppercase',
          letterSpacing: '0.07em',
          fontWeight: 600,
        }}
      >
        {label}
      </div>
      <div
        class="mono"
        style={{
          fontSize: 18,
          fontWeight: 500,
          marginTop: 2,
          color,
          letterSpacing: '-0.01em',
        }}
      >
        {value}
      </div>
    </div>
  );
}

function ActivityChart({ points }: { points: ActivityPoint[] }) {
  const max = points.reduce((m, p) => (p.runs > m ? p.runs : m), 0);
  const totalInWindow = points.reduce((s, p) => s + p.runs, 0);
  const chartHeight = 80;
  const barWidth = 100 / Math.max(points.length, 1);
  return (
    <section
      style={{
        border: '1px solid var(--border)',
        borderRadius: 8,
        padding: 14,
        marginBottom: 24,
        background: 'var(--bg-elev)',
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'baseline',
          justifyContent: 'space-between',
          marginBottom: 10,
        }}
      >
        <h2
          style={{
            fontSize: 12.5,
            fontWeight: 600,
            margin: 0,
            color: 'var(--fg-muted)',
            textTransform: 'uppercase',
            letterSpacing: '0.07em',
          }}
        >
          Activity — last {points.length} days
        </h2>
        <span style={{ fontSize: 11.5, color: 'var(--fg-faint)' }}>
          {totalInWindow} runs
        </span>
      </div>
      <svg
        viewBox={`0 0 100 ${chartHeight}`}
        preserveAspectRatio="none"
        style={{
          display: 'block',
          width: '100%',
          height: chartHeight,
        }}
      >
        {points.map((p, i) => {
          const h = max > 0 ? (p.runs / max) * (chartHeight - 4) : 0;
          return (
            <g key={p.date}>
              <title>{`${p.date} — ${p.runs} runs · $${p.cost.toFixed(2)}`}</title>
              <rect
                x={i * barWidth + barWidth * 0.15}
                y={chartHeight - h}
                width={barWidth * 0.7}
                height={h}
                fill={p.runs > 0 ? 'var(--accent-ink)' : 'transparent'}
                opacity={p.runs > 0 ? 0.9 : 1}
                rx={0.8}
              />
            </g>
          );
        })}
      </svg>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          fontSize: 10,
          color: 'var(--fg-faint)',
          marginTop: 6,
          fontFamily: 'var(--font-mono)',
        }}
      >
        <span>{points[0]?.date ?? ''}</span>
        <span>{points[points.length - 1]?.date ?? ''}</span>
      </div>
    </section>
  );
}

function KindsAndRepos({ s }: { s: GlobalStats }) {
  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: 'minmax(220px, 1fr) minmax(0, 2fr)',
        gap: 16,
        alignItems: 'start',
      }}
    >
      <KindsCard kinds={s.runsByKind} />
      <RepoList repos={s.byRepo} />
    </div>
  );
}

function KindsCard({ kinds }: { kinds: Record<string, number> }) {
  const entries = Object.entries(kinds).sort(([, a], [, b]) => b - a);
  const total = entries.reduce((sum, [, n]) => sum + n, 0);
  return (
    <section
      style={{
        border: '1px solid var(--border)',
        borderRadius: 8,
        padding: 14,
        background: 'var(--bg-elev)',
      }}
    >
      <h2
        style={{
          fontSize: 12.5,
          fontWeight: 600,
          margin: '0 0 10px',
          color: 'var(--fg-muted)',
          textTransform: 'uppercase',
          letterSpacing: '0.07em',
        }}
      >
        Runs by kind
      </h2>
      {entries.length === 0 ? (
        <div style={{ fontSize: 13, color: 'var(--fg-faint)', fontStyle: 'italic' }}>
          No runs yet.
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {entries.map(([kind, n]) => {
            const pct = total > 0 ? (n / total) * 100 : 0;
            return (
              <div key={kind}>
                <div
                  style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    fontSize: 12,
                    color: 'var(--fg)',
                    marginBottom: 3,
                  }}
                >
                  <span class="mono">{kind}</span>
                  <span
                    class="mono"
                    style={{ color: 'var(--fg-muted)' }}
                  >
                    {n} · {Math.round(pct)}%
                  </span>
                </div>
                <div
                  style={{
                    height: 6,
                    background: 'var(--bg-sunken)',
                    borderRadius: 3,
                    overflow: 'hidden',
                  }}
                >
                  <div
                    style={{
                      width: `${pct}%`,
                      height: '100%',
                      background: 'var(--accent-ink)',
                      opacity: 0.85,
                    }}
                  />
                </div>
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}

function RepoList({ repos }: { repos: RepoStatsSummary[] }) {
  return (
    <section
      style={{
        border: '1px solid var(--border)',
        borderRadius: 8,
        background: 'var(--bg-elev)',
        overflow: 'hidden',
      }}
    >
      <h2
        style={{
          fontSize: 12.5,
          fontWeight: 600,
          margin: 0,
          color: 'var(--fg-muted)',
          textTransform: 'uppercase',
          letterSpacing: '0.07em',
          padding: '12px 14px',
          borderBottom: '1px solid var(--border-soft)',
        }}
      >
        Repositories
      </h2>
      {repos.length === 0 ? (
        <div style={{ fontSize: 13, color: 'var(--fg-faint)', fontStyle: 'italic', padding: 14 }}>
          No repos yet.
        </div>
      ) : (
        <div>
          {repos.map((r, i) => (
            <a
              key={r.fp}
              href={`/repos/${r.fp}/meta`}
              style={{
                display: 'grid',
                gridTemplateColumns: '1fr 80px 100px 110px',
                alignItems: 'center',
                gap: 12,
                padding: '10px 14px',
                borderTop: i === 0 ? 'none' : '1px solid var(--border-soft)',
                textDecoration: 'none',
                color: 'var(--fg)',
              }}
            >
              <div style={{ minWidth: 0 }}>
                <div style={{ fontSize: 13.5, fontWeight: 600, color: 'var(--fg)' }}>
                  {r.name || r.path}
                </div>
                <div
                  class="mono"
                  style={{
                    fontSize: 10.5,
                    color: 'var(--fg-faint)',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                  }}
                >
                  {r.path}
                </div>
              </div>
              <div class="mono" style={{ fontSize: 12, color: 'var(--fg-muted)' }}>
                {r.runs} runs
              </div>
              <div class="mono" style={{ fontSize: 12, color: 'var(--accent-ink)' }}>
                ${r.totalCost.toFixed(2)}
              </div>
              <div class="mono" style={{ fontSize: 11, color: 'var(--fg-faint)' }}>
                {fmtRelative(r.lastSeen)}
              </div>
            </a>
          ))}
        </div>
      )}
    </section>
  );
}

function fmtInt(n: number): string {
  return n.toLocaleString();
}

function fmtDuration(minutes: number): string {
  if (!Number.isFinite(minutes) || minutes <= 0) return '—';
  if (minutes < 1) return `${Math.round(minutes * 60)}s`;
  if (minutes < 60) return `${minutes.toFixed(0)}m`;
  const h = Math.floor(minutes / 60);
  const m = Math.round(minutes % 60);
  if (h >= 24) {
    const d = Math.floor(h / 24);
    return `${d}d ${h % 24}h`;
  }
  return `${h}h ${m}m`;
}

function fmtRelative(iso: string): string {
  try {
    const t = new Date(iso).getTime();
    if (!Number.isFinite(t)) return '';
    const diff = Date.now() - t;
    const mins = Math.round(diff / 60_000);
    if (mins < 1) return 'just now';
    if (mins < 60) return `${mins}m ago`;
    const hours = Math.round(mins / 60);
    if (hours < 24) return `${hours}h ago`;
    const days = Math.round(hours / 24);
    if (days < 30) return `${days}d ago`;
    return new Date(iso).toLocaleDateString();
  } catch {
    return '';
  }
}
