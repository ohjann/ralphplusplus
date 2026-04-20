import type { ComponentChildren } from 'preact';
import { signal } from '@preact/signals';
import { useEffect } from 'preact/hooks';
import {
  openLive,
  type DaemonStateEvent,
  type WorkerStatus,
} from '../../lib/live';

// Per-fp live state, shared across any StatusPanel mounts for the same repo.
const stateByFP = signal<Record<string, DaemonStateEvent | null>>({});
const statusByFP = signal<Record<string, 'open' | 'error' | 'closed'>>({});

export function StatusPanel({ fp }: { fp: string }) {
  useEffect(() => {
    const live = openLive(fp);
    const offEvent = live.onEvent((e) => {
      if (e.kind === 'state') {
        stateByFP.value = { ...stateByFP.value, [fp]: e.data };
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
      <aside class="border-l border-neutral-800 bg-neutral-900/30 p-4 text-sm">
        <div class="text-xs uppercase tracking-wider text-neutral-500 mb-2">
          Daemon
        </div>
        <div class="text-amber-400 text-xs">Daemon offline</div>
      </aside>
    );
  }
  if (!state) {
    return (
      <aside class="border-l border-neutral-800 bg-neutral-900/30 p-4 text-sm">
        <div class="text-xs text-neutral-500">Connecting…</div>
      </aside>
    );
  }

  const workers = Object.values(state.workers ?? {});

  return (
    <aside class="border-l border-neutral-800 bg-neutral-900/30 p-4 text-sm min-w-[18rem] max-w-xs">
      <div class="flex items-center gap-2 mb-3">
        <span class="text-xs uppercase tracking-wider text-neutral-500">
          Daemon
        </span>
        <span class="w-2 h-2 rounded-full bg-emerald-500 animate-pulse" />
        <span class="text-[10px] text-neutral-500">up {state.uptime}</span>
      </div>

      <Row
        label="Phase"
        value={
          <span class="px-2 py-0.5 rounded border border-neutral-700 text-xs font-mono">
            {state.phase || 'idle'}
            {state.paused && (
              <span class="ml-1 text-amber-400">(paused)</span>
            )}
          </span>
        }
      />
      <Row
        label="Progress"
        value={
          <span class="text-neutral-300 text-xs">
            {state.completed_count}/{state.total_stories}
            {state.failed_count > 0 && (
              <span class="text-red-400 ml-1">
                · {state.failed_count} failed
              </span>
            )}
            <span class="ml-2 text-neutral-500">
              iter {state.iteration_count}
            </span>
          </span>
        }
      />
      <Row
        label="Cost"
        value={
          <span class="font-mono text-xs">
            ${state.cost_totals.total_cost.toFixed(2)}
          </span>
        }
      />

      <section class="mt-4">
        <div class="text-[10px] uppercase tracking-wider text-neutral-500 mb-1">
          Workers
        </div>
        {workers.length === 0 ? (
          <div class="text-xs text-neutral-500 italic">Idle.</div>
        ) : (
          <ul class="flex flex-col gap-1">
            {workers
              .sort((a, b) => a.id - b.id)
              .map((w) => (
                <li key={w.id}>
                  <WorkerRow w={w} />
                </li>
              ))}
          </ul>
        )}
      </section>

      <section class="mt-4">
        <div class="text-[10px] uppercase tracking-wider text-neutral-500 mb-1">
          Plan quality
        </div>
        <div class="text-xs text-neutral-300">
          <span class="font-mono">
            {Math.round(state.plan_quality.score * 100)}%
          </span>
          <span class="text-neutral-500 ml-2">
            {state.plan_quality.first_pass_count} first-pass ·{' '}
            {state.plan_quality.retry_count} retries
            {state.plan_quality.failed_count > 0 && (
              <> · {state.plan_quality.failed_count} failed</>
            )}
          </span>
        </div>
      </section>

      {hasFusion(state.fusion_metrics) && (
        <section class="mt-4">
          <div class="text-[10px] uppercase tracking-wider text-neutral-500 mb-1">
            Fusion metrics
          </div>
          <dl class="grid grid-cols-2 gap-x-3 gap-y-1 text-[11px] text-neutral-300">
            {Object.entries(state.fusion_metrics).map(([k, v]) => (
              <div key={k} class="contents">
                <dt class="text-neutral-500">{k}</dt>
                <dd class="font-mono text-right">{fmtFusionVal(v)}</dd>
              </div>
            ))}
          </dl>
        </section>
      )}
    </aside>
  );
}

function Row({
  label,
  value,
}: {
  label: string;
  value: ComponentChildren;
}) {
  return (
    <div class="flex items-center gap-2 py-1">
      <span class="text-[10px] uppercase tracking-wider text-neutral-500 w-16">
        {label}
      </span>
      <span class="flex-1">{value}</span>
    </div>
  );
}

function WorkerRow({ w }: { w: WorkerStatus }) {
  const active = w.state !== 'idle' && w.state !== '';
  return (
    <div class="flex items-center gap-2 text-xs px-2 py-1 rounded bg-neutral-900 border border-neutral-800">
      <span class="font-mono text-neutral-500 w-6">#{w.id}</span>
      <span
        class={`w-1.5 h-1.5 rounded-full ${active ? 'bg-emerald-500' : 'bg-neutral-600'}`}
      />
      <span class="text-neutral-400 text-[10px] uppercase tracking-wider">
        {w.role}
      </span>
      <span class="text-neutral-300 truncate flex-1" title={w.story_title}>
        {w.story_id || '—'}
      </span>
      {w.iteration > 0 && (
        <span class="text-neutral-500 text-[10px]">i{w.iteration}</span>
      )}
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
  if (typeof v === 'number') {
    return Number.isInteger(v) ? v.toString() : v.toFixed(2);
  }
  if (typeof v === 'string') return v;
  return JSON.stringify(v);
}
