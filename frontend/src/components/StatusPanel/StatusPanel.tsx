import type { ComponentChildren } from 'preact';
import { signal } from '@preact/signals';
import { useEffect } from 'preact/hooks';
import {
  openLive,
  type DaemonStateEvent,
  type WorkerStatus,
} from '../../lib/live';
import { pushToast } from '../../lib/toast';
import { PauseToggle } from '../Commands/PauseToggle';
import { QuitButton } from '../Commands/QuitButton';
import { HintComposer } from '../Commands/HintComposer';
import { ClarifyPrompt } from '../Commands/ClarifyPrompt';

const stateByFP = signal<Record<string, DaemonStateEvent | null>>({});
const statusByFP = signal<Record<string, 'open' | 'error' | 'closed'>>({});

function fmtUptime(s: string): string {
  // Daemon sends Go duration strings like "2m34s" or "1h5m". Pass through.
  return s || '0s';
}

export function StatusPanel({ fp }: { fp: string }) {
  useEffect(() => {
    const live = openLive(fp);
    const offEvent = live.onEvent((e) => {
      if (e.kind === 'state') {
        stateByFP.value = { ...stateByFP.value, [fp]: e.data };
      } else if (e.kind === 'merge_result') {
        const d = e.data;
        pushToast(
          d.success ? 'success' : 'error',
          d.success
            ? `Merged ${d.story_id}`
            : `Merge failed: ${d.story_id} — ${d.error ?? 'unknown error'}`,
        );
      } else if (e.kind === 'stuck_alert') {
        const d = e.data;
        pushToast(
          'warn',
          `Worker #${d.worker_id} stuck on ${d.story_id}: ${d.stuck_reason}`,
        );
      }
    });
    const offStatus = live.onStatus((s) => {
      statusByFP.value = { ...statusByFP.value, [fp]: s };
    });
    return () => {
      offEvent();
      offStatus();
      live.close();
    };
  }, [fp]);

  const state = stateByFP.value[fp] ?? null;
  const status = statusByFP.value[fp] ?? 'error';

  if (!state && status !== 'open') {
    return (
      <aside style={panel.root}>
        <div style={panel.hd}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span class="dot" />
            <span style={{ fontSize: 12, fontWeight: 600 }}>Daemon offline</span>
          </div>
        </div>
        <div style={{ padding: 16, fontSize: 12, color: 'var(--fg-faint)' }}>
          The repo has no reachable <code>.ralph/daemon.sock</code>. Start
          Ralph in this repo to connect.
        </div>
      </aside>
    );
  }
  if (!state) {
    return (
      <aside style={panel.root}>
        <div style={panel.hd}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span class="dot ok live" />
            <span style={{ fontSize: 12, fontWeight: 600 }}>Connecting…</span>
          </div>
        </div>
      </aside>
    );
  }

  const workers = Object.values(state.workers ?? {});
  const total = state.total_stories || 1;
  const pct = Math.min(100, Math.round((state.completed_count / total) * 100));

  return (
    <aside style={panel.root}>
      <div style={panel.hd}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span class="dot ok live" />
          <span style={{ fontSize: 12, fontWeight: 600 }}>Daemon up</span>
          <span
            class="mono"
            style={{ fontSize: 11, color: 'var(--fg-faint)' }}
          >
            {fmtUptime(state.uptime)}
          </span>
        </div>
      </div>

      <div style={panel.body}>
        {/* Phase */}
        <Row label="Phase">
          <span
            class="pill indigo"
            style={{
              textTransform: 'uppercase',
              letterSpacing: '0.06em',
              fontSize: 10.5,
            }}
          >
            {state.phase || 'idle'}
          </span>
          {state.paused && <span class="pill warn">paused</span>}
        </Row>

        {/* Progress */}
        <div>
          <div style={panel.rowLabel}>Progress</div>
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 8,
              marginTop: 4,
            }}
          >
            <span class="mono" style={{ fontSize: 13, color: 'var(--fg)' }}>
              {state.completed_count}
              <span style={{ color: 'var(--fg-ghost)' }}>/{state.total_stories}</span>
            </span>
            <span style={{ fontSize: 11, color: 'var(--fg-faint)' }}>
              stories
            </span>
            <span style={{ width: 1, height: 10, background: 'var(--border)' }} />
            <span class="mono" style={{ fontSize: 13, color: 'var(--fg)' }}>
              {state.iteration_count}
            </span>
            <span style={{ fontSize: 11, color: 'var(--fg-faint)' }}>
              iters
            </span>
            {state.failed_count > 0 && (
              <>
                <span
                  style={{
                    width: 1,
                    height: 10,
                    background: 'var(--border)',
                  }}
                />
                <span
                  class="mono"
                  style={{ fontSize: 13, color: 'var(--err)' }}
                >
                  {state.failed_count}
                </span>
                <span style={{ fontSize: 11, color: 'var(--err)' }}>
                  failed
                </span>
              </>
            )}
          </div>
          <div style={panel.bar}>
            <div style={{ ...panel.barFill, width: `${pct}%` }} />
          </div>
        </div>

        {/* Cost */}
        <Row label="Cost">
          <span
            class="mono"
            style={{
              fontSize: 14,
              color: 'var(--fg)',
              letterSpacing: '-0.01em',
            }}
          >
            ${state.cost_totals.total_cost.toFixed(2)}
          </span>
          <span style={{ fontSize: 11, color: 'var(--fg-faint)' }}>
            · {state.cost_totals.total_input_tokens.toLocaleString()} in ·{' '}
            {state.cost_totals.total_output_tokens.toLocaleString()} out
          </span>
        </Row>

        {/* Workers */}
        <div>
          <div style={panel.rowLabel}>Workers</div>
          {workers.length === 0 ? (
            <div
              style={{
                marginTop: 6,
                fontSize: 11.5,
                color: 'var(--fg-faint)',
                fontStyle: 'italic',
              }}
            >
              Idle.
            </div>
          ) : (
            <div style={panel.workerTable}>
              {workers
                .sort((a, b) => a.id - b.id)
                .map((w) => (
                  <WorkerRow key={w.id} w={w} />
                ))}
            </div>
          )}
        </div>

        {/* Plan quality */}
        <div>
          <div style={panel.rowLabel}>Plan quality</div>
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 10,
              marginTop: 4,
            }}
          >
            <div
              class="mono"
              style={{
                fontSize: 20,
                color: 'var(--accent-ink)',
                letterSpacing: '-0.01em',
              }}
            >
              {state.plan_quality.score.toFixed(2)}
            </div>
            <span style={{ fontSize: 10.5, color: 'var(--fg-faint)' }}>
              <span class="mono">{state.plan_quality.first_pass_count}</span>{' '}
              first-pass
              <span style={{ color: 'var(--fg-ghost)' }}> · </span>
              <span class="mono">{state.plan_quality.retry_count}</span>{' '}
              retry
              {state.plan_quality.failed_count > 0 && (
                <>
                  <span style={{ color: 'var(--fg-ghost)' }}> · </span>
                  <span
                    class="mono"
                    style={{ color: 'var(--err)' }}
                  >
                    {state.plan_quality.failed_count}
                  </span>{' '}
                  failed
                </>
              )}
            </span>
          </div>
        </div>

        {/* Fusion */}
        {hasFusion(state.fusion_metrics) && (
          <div>
            <div style={panel.rowLabel}>Fusion metrics</div>
            <div style={panel.fusionGrid}>
              {Object.entries(state.fusion_metrics).map(([k, v]) => (
                <FusionCell key={k} label={k} value={fmtFusionVal(v)} />
              ))}
            </div>
          </div>
        )}

        {/* Composer */}
        <div style={panel.composer}>
          <div style={panel.rowLabel}>Commands</div>
          <div style={{ display: 'flex', gap: 6, marginTop: 6 }}>
            <PauseToggle fp={fp} paused={state.paused} />
            <QuitButton fp={fp} />
          </div>
          <div style={{ marginTop: 8 }}>
            <HintComposer fp={fp} workers={workers} />
          </div>
          <div style={{ marginTop: 8 }}>
            <ClarifyPrompt fp={fp} enabled={false} />
          </div>
        </div>
      </div>
    </aside>
  );
}

function Row({
  label,
  children,
}: {
  label: string;
  children: ComponentChildren;
}) {
  return (
    <div>
      <div style={panel.rowLabel}>{label}</div>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          marginTop: 4,
          flexWrap: 'wrap',
        }}
      >
        {children}
      </div>
    </div>
  );
}

function WorkerRow({ w }: { w: WorkerStatus }) {
  const active = w.state !== 'idle' && w.state !== '';
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 8,
        padding: '6px 10px',
        borderBottom: '1px solid var(--border-soft)',
      }}
    >
      <span
        class="mono"
        style={{ fontSize: 11, color: 'var(--fg-muted)', width: 22 }}
      >
        #{w.id}
      </span>
      <span class={`dot ${active ? 'ok live' : ''}`} />
      <span style={{ fontSize: 11.5, color: 'var(--fg)', width: 68 }}>
        {w.state || 'idle'}
      </span>
      <span
        style={{
          fontSize: 11.5,
          color: 'var(--fg-muted)',
          flex: 1,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
      >
        {w.role}
      </span>
      <span
        class="mono"
        style={{
          fontSize: 11,
          color: 'var(--fg-faint)',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
          maxWidth: 80,
        }}
      >
        {w.story_id || '—'}
        {w.iteration > 0 && ` · ${w.iteration}`}
      </span>
    </div>
  );
}

function FusionCell({ label, value }: { label: string; value: string }) {
  return (
    <div
      style={{
        padding: '6px 8px',
        border: '1px solid var(--border)',
        borderRadius: 6,
        background: 'var(--bg-elev)',
      }}
    >
      <div
        class="mono"
        style={{
          fontSize: 15,
          color: 'var(--fg)',
          letterSpacing: '-0.01em',
        }}
      >
        {value}
      </div>
      <div
        style={{
          fontSize: 10,
          color: 'var(--fg-faint)',
          textTransform: 'uppercase',
          letterSpacing: '0.06em',
        }}
      >
        {label}
      </div>
    </div>
  );
}

function hasFusion(fm: unknown): fm is Record<string, unknown> {
  return (
    fm != null &&
    typeof fm === 'object' &&
    Object.keys(fm as Record<string, unknown>).length > 0
  );
}

function fmtFusionVal(v: unknown): string {
  if (typeof v === 'number')
    return Number.isInteger(v) ? v.toString() : v.toFixed(2);
  if (typeof v === 'string') return v;
  return JSON.stringify(v);
}

const panel = {
  root: {
    height: '100%',
    display: 'flex' as const,
    flexDirection: 'column' as const,
    background: 'transparent',
    overflow: 'hidden',
  },
  hd: {
    display: 'flex' as const,
    alignItems: 'center' as const,
    justifyContent: 'space-between' as const,
    padding: '12px 14px',
    borderBottom: '1px solid var(--border-soft)',
  },
  body: {
    flex: 1,
    overflow: 'auto' as const,
    padding: 14,
    display: 'flex' as const,
    flexDirection: 'column' as const,
    gap: 16,
  },
  rowLabel: {
    fontSize: 10.5,
    color: 'var(--fg-faint)',
    textTransform: 'uppercase' as const,
    letterSpacing: '0.08em',
    fontWeight: 600,
  },
  bar: {
    marginTop: 8,
    height: 4,
    background: 'var(--bg-sunken)',
    borderRadius: 2,
    overflow: 'hidden' as const,
  },
  barFill: {
    height: '100%',
    background: 'var(--accent)',
    borderRadius: 2,
    transition: 'width 400ms ease',
  },
  workerTable: {
    marginTop: 6,
    display: 'flex' as const,
    flexDirection: 'column' as const,
    gap: 2,
    border: '1px solid var(--border)',
    borderRadius: 6,
    overflow: 'hidden' as const,
    background: 'var(--bg-elev)',
  },
  fusionGrid: {
    marginTop: 6,
    display: 'grid' as const,
    gridTemplateColumns: 'repeat(auto-fill, minmax(96px, 1fr))',
    gap: 6,
  },
  composer: {
    marginTop: 'auto',
    padding: 12,
    border: '1px solid var(--border)',
    borderRadius: 8,
    background: 'var(--bg-sunken)',
  },
};
