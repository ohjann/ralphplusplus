import { useEffect } from 'preact/hooks';
import { signal } from '@preact/signals';
import { useRoute } from 'preact-iso';
import {
  apiGet,
  apiPost,
  ApiError,
  type RepoMetaResponse,
  type SpawnRetroResponse,
} from '../lib/api';
import { pushToast } from '../lib/toast';
import { refreshRepoRuns } from '../components/Sidebar/Sidebar';

const loading = signal<boolean>(false);
const error = signal<string>('');
const resp = signal<RepoMetaResponse | null>(null);
const currentFP = signal<string>('');
const retroBusy = signal<boolean>(false);

async function triggerRetro(fp: string): Promise<void> {
  if (retroBusy.value) return;
  retroBusy.value = true;
  try {
    const out = await apiPost<SpawnRetroResponse>(
      `/api/spawn/retro/${encodeURIComponent(fp)}`,
    );
    const tail = out.runId
      ? `run ${out.runId.slice(0, 12)}`
      : `pid ${out.pid}`;
    pushToast('success', `Retro started — ${tail}`);
    refreshRepoRuns(fp);
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) {
      const body = e.body as { runId?: string } | undefined;
      const suffix = body?.runId ? ` (${body.runId.slice(0, 8)}…)` : '';
      pushToast('warn', `A retro is already running for this repo${suffix}.`);
      return;
    }
    const msg = e instanceof Error ? e.message : String(e);
    pushToast('error', `Retro failed to start: ${msg}`);
  } finally {
    retroBusy.value = false;
  }
}

async function load(fp: string) {
  if (currentFP.value === fp && resp.value) return;
  currentFP.value = fp;
  loading.value = true;
  error.value = '';
  resp.value = null;
  try {
    const r = await apiGet<RepoMetaResponse>(
      `/api/repos/${encodeURIComponent(fp)}/meta`,
    );
    if (currentFP.value !== fp) return;
    resp.value = r;
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

function fmtTime(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

export function RepoMetaRoute() {
  const { params } = useRoute();
  const fp = params.fp;

  useEffect(() => {
    if (fp) void load(fp);
  }, [fp]);

  if (!fp) return null;
  if (loading.value && !resp.value) {
    return <div class="p-8 text-sm text-neutral-500">Loading repo meta…</div>;
  }
  if (error.value) {
    return (
      <div class="p-8 text-sm text-red-400">
        Failed to load: {error.value}
      </div>
    );
  }
  if (!resp.value) return null;

  const { meta, aggCosts, runCountsByKind } = resp.value;
  const kinds = Object.entries(runCountsByKind).sort(([a], [b]) =>
    a.localeCompare(b),
  );
  const totalByKind = kinds.reduce((s, [, n]) => s + n, 0);

  return (
    <div class="p-6 max-w-3xl">
      <header class="mb-6 flex items-start justify-between gap-4">
        <div class="min-w-0">
          <h1 class="text-xl font-semibold">{meta.name || meta.path}</h1>
          <div class="text-xs text-neutral-500 font-mono break-all">
            {meta.path}
          </div>
        </div>
        <button
          type="button"
          disabled={retroBusy.value}
          onClick={() => void triggerRetro(fp)}
          class="shrink-0 px-3 py-1.5 text-xs font-semibold rounded border border-neutral-700 bg-neutral-800 text-neutral-100 hover:bg-neutral-700 disabled:opacity-60 disabled:cursor-not-allowed"
        >
          {retroBusy.value ? 'Starting…' : 'Run retrospective'}
        </button>
      </header>

      <section class="mb-6">
        <h2 class="text-xs uppercase tracking-wider text-neutral-500 mb-2">
          Identity
        </h2>
        <dl class="border border-neutral-800 rounded divide-y divide-neutral-800">
          <Row label="Fingerprint" value={fp} mono />
          <Row
            label="First git SHA"
            value={meta.git_first_sha ? meta.git_first_sha.slice(0, 16) : '—'}
            mono
          />
          <Row label="First seen" value={fmtTime(meta.first_seen)} />
          <Row label="Last seen" value={fmtTime(meta.last_seen)} />
          <Row
            label="Last run"
            value={
              meta.last_run_id ? meta.last_run_id.slice(0, 16) : '—'
            }
            mono
          />
          <Row label="Total invocations" value={String(meta.run_count)} mono />
        </dl>
      </section>

      <section class="mb-6">
        <h2 class="text-xs uppercase tracking-wider text-neutral-500 mb-2">
          Aggregate stats
        </h2>
        <div class="grid grid-cols-2 md:grid-cols-4 gap-3">
          <Card label="Stored runs" value={String(aggCosts.runs)} />
          <Card
            label="Total cost"
            value={`$${aggCosts.totalCost.toFixed(2)}`}
          />
          <Card
            label="Duration"
            value={fmtDuration(aggCosts.durationMinutes)}
          />
          <Card
            label="Iterations"
            value={String(aggCosts.totalIterations)}
          />
          <Card label="Stories total" value={String(aggCosts.storiesTotal)} />
          <Card
            label="Stories done"
            value={String(aggCosts.storiesCompleted)}
          />
          <Card
            label="Stories failed"
            value={String(aggCosts.storiesFailed)}
            tone={aggCosts.storiesFailed > 0 ? 'warn' : undefined}
          />
          {aggCosts.storiesTotal > 0 && (
            <Card
              label="Completion rate"
              value={`${Math.round(
                (aggCosts.storiesCompleted / aggCosts.storiesTotal) * 100,
              )}%`}
            />
          )}
        </div>
      </section>

      <section>
        <h2 class="text-xs uppercase tracking-wider text-neutral-500 mb-2">
          Runs by kind
        </h2>
        {kinds.length === 0 ? (
          <div class="text-sm text-neutral-500 italic">
            No stored manifests.
          </div>
        ) : (
          <table class="w-full text-xs border border-neutral-800 rounded">
            <thead>
              <tr class="text-left text-neutral-500 border-b border-neutral-800">
                <th class="px-3 py-2 font-normal">Kind</th>
                <th class="px-3 py-2 font-normal">Count</th>
                <th class="px-3 py-2 font-normal">Share</th>
              </tr>
            </thead>
            <tbody>
              {kinds.map(([k, n]) => (
                <tr key={k} class="border-t border-neutral-800">
                  <td class="px-3 py-2 text-neutral-200 font-mono">{k}</td>
                  <td class="px-3 py-2 text-neutral-200 font-mono">{n}</td>
                  <td class="px-3 py-2 text-neutral-500 font-mono">
                    {totalByKind > 0
                      ? `${Math.round((n / totalByKind) * 100)}%`
                      : '—'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}

function Row({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div class="flex items-center gap-4 px-3 py-2">
      <dt class="text-xs text-neutral-400 w-40 shrink-0">{label}</dt>
      <dd
        class={
          'text-xs text-neutral-200 break-all ' +
          (mono ? 'font-mono' : '')
        }
      >
        {value}
      </dd>
    </div>
  );
}

function Card({
  label,
  value,
  tone,
}: {
  label: string;
  value: string;
  tone?: 'warn';
}) {
  const valueClass =
    tone === 'warn'
      ? 'text-amber-300'
      : 'text-neutral-100';
  return (
    <div class="bg-neutral-900 border border-neutral-800 rounded px-3 py-2">
      <div class="text-[10px] uppercase tracking-wider text-neutral-500">
        {label}
      </div>
      <div class={`font-mono text-sm ${valueClass}`}>{value}</div>
    </div>
  );
}

function fmtDuration(minutes: number): string {
  if (minutes < 1) return `${Math.round(minutes * 60)}s`;
  if (minutes < 60) return `${minutes.toFixed(1)}m`;
  const h = Math.floor(minutes / 60);
  const m = Math.round(minutes % 60);
  return `${h}h ${m}m`;
}
