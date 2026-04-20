import { useEffect } from 'preact/hooks';
import { signal } from '@preact/signals';
import { useRoute } from 'preact-iso';
import { apiGet, type SettingsResponse } from '../lib/api';
import type { DaemonStateEvent } from '../lib/live';

const loading = signal<boolean>(false);
const error = signal<string>('');
const resp = signal<SettingsResponse | null>(null);
const currentFP = signal<string>('');

async function load(fp: string) {
  if (currentFP.value === fp && resp.value) return;
  currentFP.value = fp;
  loading.value = true;
  error.value = '';
  resp.value = null;
  try {
    const r = await apiGet<SettingsResponse>(
      `/api/live/${encodeURIComponent(fp)}/settings`,
    );
    if (currentFP.value !== fp) return;
    resp.value = r;
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

export function SettingsRoute() {
  const { params } = useRoute();
  const fp = params.fp;
  useEffect(() => {
    if (fp) void load(fp);
  }, [fp]);

  if (!fp) return null;
  if (loading.value && !resp.value)
    return (
      <div style={{ padding: 32, color: 'var(--fg-faint)' }}>
        Loading settings…
      </div>
    );
  if (error.value)
    return (
      <div style={{ padding: 32, color: 'var(--err)' }}>
        Failed to load: {error.value}
      </div>
    );
  if (!resp.value) return null;

  return (
    <div style={{ padding: '22px 28px 80px', minHeight: '100%' }}>
      <div style={{ maxWidth: 860, margin: '0 auto' }}>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 6,
            fontSize: 12,
            color: 'var(--fg-faint)',
            fontFamily: 'var(--font-mono)',
            marginBottom: 12,
          }}
        >
          <span>system</span>
          <span style={{ color: 'var(--fg-ghost)' }}>/</span>
          <span style={{ color: 'var(--fg)' }}>settings</span>
        </div>
        <h2
          style={{
            fontSize: 18,
            fontWeight: 600,
            letterSpacing: '-0.01em',
            margin: '0 0 14px',
            color: 'var(--fg)',
          }}
        >
          Daemon configuration
          <span
            style={{
              marginLeft: 10,
              fontSize: 10,
              color: 'var(--warn)',
              textTransform: 'uppercase',
              letterSpacing: '0.08em',
              verticalAlign: 'middle',
            }}
          >
            Read-only
          </span>
        </h2>

        {resp.value.source === 'daemon' ? <DaemonBanner /> : <FileBanner />}

        {resp.value.source === 'file' ? (
          <FileConfig config={resp.value.config ?? {}} />
        ) : (
          <DaemonStateSummary state={resp.value.state as DaemonStateEvent} />
        )}
      </div>
    </div>
  );
}

function DaemonBanner() {
  return (
    <div
      style={{
        padding: '10px 14px',
        background: 'var(--ok-soft)',
        color: 'var(--ok)',
        border: '1px solid var(--ok)',
        borderRadius: 6,
        fontSize: 13,
        marginBottom: 16,
      }}
    >
      <strong style={{ fontWeight: 600 }}>Daemon reachable.</strong>{' '}
      Showing live runtime state. Persisted configuration lives in{' '}
      <code class="mono">.ralph/config.toml</code>.
    </div>
  );
}

function FileBanner() {
  return (
    <div
      style={{
        padding: '10px 14px',
        background: 'var(--warn-soft)',
        color: 'var(--warn)',
        border: '1px solid var(--warn)',
        borderRadius: 6,
        fontSize: 13,
        marginBottom: 16,
      }}
    >
      <strong style={{ fontWeight: 600 }}>Daemon offline.</strong>{' '}
      Showing persisted config from{' '}
      <code class="mono">.ralph/config.toml</code>.
    </div>
  );
}

function FileConfig({ config }: { config: Record<string, unknown> }) {
  const keys = Object.keys(config).sort();
  if (keys.length === 0) {
    return (
      <div
        style={{
          fontSize: 13,
          color: 'var(--fg-faint)',
          fontStyle: 'italic',
        }}
      >
        No persisted configuration (config.toml is missing or empty).
      </div>
    );
  }
  return (
    <dl
      style={{
        border: '1px solid var(--border)',
        borderRadius: 8,
        overflow: 'hidden',
        background: 'var(--bg-elev)',
      }}
    >
      {keys.map((k, i) => (
        <div
          key={k}
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 16,
            padding: '8px 14px',
            borderTop: i === 0 ? 'none' : '1px solid var(--border-soft)',
          }}
        >
          <dt
            class="mono"
            style={{
              fontSize: 12,
              color: 'var(--fg-muted)',
              width: 220,
              flexShrink: 0,
            }}
          >
            {k}
          </dt>
          <dd
            class="mono"
            style={{
              fontSize: 12,
              color: 'var(--fg)',
              margin: 0,
              wordBreak: 'break-all',
            }}
          >
            {fmtValue(config[k])}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function DaemonStateSummary({ state }: { state: DaemonStateEvent }) {
  if (!state)
    return (
      <div
        style={{
          fontSize: 13,
          color: 'var(--fg-faint)',
          fontStyle: 'italic',
        }}
      >
        Daemon returned no state snapshot.
      </div>
    );
  const rows: Array<[string, string]> = [
    ['phase', state.phase || 'idle'],
    ['paused', state.paused ? 'true' : 'false'],
    ['uptime', state.uptime],
    ['workers', String(Object.keys(state.workers ?? {}).length)],
    [
      'progress',
      `${state.completed_count}/${state.total_stories}` +
        (state.failed_count > 0 ? ` · ${state.failed_count} failed` : ''),
    ],
    ['iterations', String(state.iteration_count)],
    ['total_cost', `$${state.cost_totals.total_cost.toFixed(2)}`],
    [
      'tokens',
      `in ${state.cost_totals.total_input_tokens.toLocaleString()} · out ${state.cost_totals.total_output_tokens.toLocaleString()}`,
    ],
  ];
  return (
    <>
      <dl
        style={{
          border: '1px solid var(--border)',
          borderRadius: 8,
          overflow: 'hidden',
          background: 'var(--bg-elev)',
        }}
      >
        {rows.map(([k, v], i) => (
          <div
            key={k}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 16,
              padding: '8px 14px',
              borderTop: i === 0 ? 'none' : '1px solid var(--border-soft)',
            }}
          >
            <dt
              class="mono"
              style={{
                fontSize: 12,
                color: 'var(--fg-muted)',
                width: 140,
                flexShrink: 0,
              }}
            >
              {k}
            </dt>
            <dd
              class="mono"
              style={{ fontSize: 12, color: 'var(--fg)', margin: 0 }}
            >
              {v}
            </dd>
          </div>
        ))}
      </dl>
      <p
        style={{
          fontSize: 11.5,
          color: 'var(--fg-faint)',
          marginTop: 10,
        }}
      >
        The daemon currently exposes runtime state, not its tunable config.
        To see persisted configuration, stop the daemon and reload this
        page.
      </p>
    </>
  );
}

function fmtValue(v: unknown): string {
  if (v === null) return 'null';
  if (typeof v === 'string') return v;
  if (typeof v === 'boolean' || typeof v === 'number') return String(v);
  try {
    return JSON.stringify(v);
  } catch {
    return String(v);
  }
}
