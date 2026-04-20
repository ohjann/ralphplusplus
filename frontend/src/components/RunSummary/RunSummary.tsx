import type { ComponentChildren } from 'preact';
import { useEffect } from 'preact/hooks';
import { signal } from '@preact/signals';
import {
  apiGet,
  type RunDetail,
  type PRDResponse,
  type StoryRecord,
} from '../../lib/api';
import { StatusPanel } from '../StatusPanel/StatusPanel';

const loading = signal<boolean>(false);
const error = signal<string>('');
const detail = signal<RunDetail | null>(null);
const prd = signal<PRDResponse | null>(null);
const currentKey = signal<string>('');

async function load(fp: string, runId: string) {
  const key = `${fp}/${runId}`;
  if (currentKey.value === key && detail.value) return;
  currentKey.value = key;
  loading.value = true;
  error.value = '';
  detail.value = null;
  prd.value = null;
  try {
    const [d, p] = await Promise.all([
      apiGet<RunDetail>(`/api/repos/${fp}/runs/${runId}`),
      apiGet<PRDResponse>(
        `/api/repos/${fp}/prd?run_id=${encodeURIComponent(runId)}`,
      ).catch(() => null),
    ]);
    if (currentKey.value !== key) return;
    detail.value = d;
    prd.value = p;
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

function fmtTime(iso?: string): string {
  if (!iso) return '—';
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

function fmtNum(n: number): string {
  return n.toLocaleString();
}

function fmtCost(n: number): string {
  return `$${n.toFixed(2)}`;
}

function fmtDuration(minutes: number): string {
  if (minutes < 1) return `${Math.round(minutes * 60)}s`;
  if (minutes < 60) return `${minutes.toFixed(1)}m`;
  const h = Math.floor(minutes / 60);
  const m = Math.round(minutes % 60);
  return `${h}h ${m}m`;
}

function shortHash(s: string, n = 8): string {
  return s.slice(0, n);
}

export function RunSummary({ fp, runId }: { fp: string; runId: string }) {
  useEffect(() => {
    void load(fp, runId);
  }, [fp, runId]);

  if (loading.value && !detail.value) {
    return <div class="p-8 text-sm text-neutral-500">Loading run…</div>;
  }
  if (error.value) {
    return (
      <div class="p-8 text-sm text-red-400">Failed to load: {error.value}</div>
    );
  }
  if (!detail.value) {
    return null;
  }

  const m = detail.value.manifest;
  const s = detail.value.summary;
  const p = prd.value;
  const statusTone =
    m.status === 'running'
      ? 'bg-emerald-500/10 text-emerald-300 border-emerald-500/40'
      : m.status === 'complete'
        ? 'bg-neutral-700/30 text-neutral-200 border-neutral-600'
        : 'bg-amber-500/10 text-amber-300 border-amber-500/40';

  const isLive = m.status === 'running';

  return (
    <div class="flex items-stretch">
      <div class="flex-1 p-6 max-w-4xl">
      <header class="mb-6">
        <div class="flex items-center gap-2 mb-1">
          <h1 class="text-xl font-semibold">{m.repo_name || m.repo_path}</h1>
          <span
            class={`ml-1 text-[10px] uppercase tracking-wider px-2 py-0.5 rounded border ${statusTone}`}
          >
            {m.status}
          </span>
          <span class="text-[10px] uppercase tracking-wider px-2 py-0.5 rounded border border-neutral-700 text-neutral-400">
            {m.kind}
          </span>
        </div>
        <div class="text-xs text-neutral-500 font-mono">{m.repo_path}</div>
        <dl class="grid grid-cols-2 md:grid-cols-4 gap-x-6 gap-y-2 mt-4 text-xs">
          <Field label="Branch" value={m.git_branch || '—'} mono />
          <Field label="HEAD" value={m.git_head_sha ? shortHash(m.git_head_sha, 10) : '—'} mono />
          <Field label="Started" value={fmtTime(m.start_time)} />
          <Field label="Ended" value={fmtTime(m.end_time)} />
          <Field label="Run ID" value={shortHash(m.run_id)} mono />
          <Field label="Ralph" value={m.ralph_version} mono />
        </dl>
      </header>

      {(m.flags?.length || m.claude_models) && (
        <section class="mb-6">
          <h2 class="text-xs uppercase tracking-wider text-neutral-500 mb-2">
            Configuration
          </h2>
          <div class="flex flex-wrap gap-1.5">
            {m.flags?.map((f) => (
              <Chip key={f} tone="neutral">
                {f}
              </Chip>
            ))}
            {m.claude_models &&
              Object.entries(m.claude_models).map(([role, model]) => (
                <Chip key={role} tone="indigo">
                  <span class="text-indigo-400">{role}</span>
                  <span class="text-neutral-500 mx-1">→</span>
                  <span class="text-neutral-100">{model}</span>
                </Chip>
              ))}
          </div>
        </section>
      )}

      <section class="mb-6">
        <h2 class="text-xs uppercase tracking-wider text-neutral-500 mb-2">
          Totals
        </h2>
        <div class="grid grid-cols-2 md:grid-cols-5 gap-3 text-sm">
          <Metric label="Input tokens" value={fmtNum(m.totals.input_tokens)} />
          <Metric label="Output tokens" value={fmtNum(m.totals.output_tokens)} />
          <Metric label="Cache read" value={fmtNum(m.totals.cache_read)} />
          <Metric label="Cache write" value={fmtNum(m.totals.cache_write)} />
          <Metric label="Iterations" value={fmtNum(m.totals.iterations)} />
          {s && <Metric label="Cost" value={fmtCost(s.total_cost)} />}
          {s && (
            <Metric label="Duration" value={fmtDuration(s.duration_minutes)} />
          )}
          {s && (
            <Metric
              label="First-pass rate"
              value={`${Math.round(s.first_pass_rate * 100)}%`}
            />
          )}
        </div>
      </section>

      <section class="mb-6">
        <h2 class="text-xs uppercase tracking-wider text-neutral-500 mb-2">
          PRD
        </h2>
        <PRDRow fp={fp} prd={p} />
      </section>

      <section>
        <h2 class="text-xs uppercase tracking-wider text-neutral-500 mb-2">
          Stories
        </h2>
        <StoriesList fp={fp} runId={runId} stories={m.stories ?? []} />
      </section>
      </div>
      {isLive && <StatusPanel fp={fp} />}
    </div>
  );
}

function Field({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div>
      <dt class="text-[10px] uppercase tracking-wider text-neutral-500">
        {label}
      </dt>
      <dd class={'text-neutral-200 ' + (mono ? 'font-mono text-xs' : '')}>
        {value}
      </dd>
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div class="bg-neutral-900 border border-neutral-800 rounded px-3 py-2">
      <div class="text-[10px] uppercase tracking-wider text-neutral-500">
        {label}
      </div>
      <div class="text-neutral-100 font-mono text-sm">{value}</div>
    </div>
  );
}

function Chip({
  children,
  tone = 'neutral',
}: {
  children: ComponentChildren;
  tone?: 'neutral' | 'indigo';
}) {
  const cls =
    tone === 'indigo'
      ? 'bg-indigo-500/10 border-indigo-500/30 text-indigo-200'
      : 'bg-neutral-800 border-neutral-700 text-neutral-300';
  return (
    <span
      class={`text-xs px-2 py-0.5 rounded border ${cls} inline-flex items-center`}
    >
      {children}
    </span>
  );
}

function PRDRow({ fp, prd }: { fp: string; prd: PRDResponse | null }) {
  if (!prd) {
    return (
      <div class="text-sm text-neutral-500 italic">
        No prd.json on disk for this repo.
      </div>
    );
  }
  const unchanged = prd.matchesRunSnapshot === true;
  const changed = prd.matchesRunSnapshot === false;
  return (
    <div class="flex items-center gap-3 text-sm">
      <span class="font-mono text-xs text-neutral-400">
        sha256 {shortHash(prd.hash, 12)}
      </span>
      {unchanged && (
        <span class="text-emerald-400 text-xs">
          PRD unchanged since this run
        </span>
      )}
      {changed && (
        <>
          <span class="text-amber-400 text-xs">PRD changed</span>
          <a
            href={`/repos/${fp}/prd`}
            class="text-xs text-indigo-300 hover:underline"
          >
            view current →
          </a>
        </>
      )}
      {prd.matchesRunSnapshot === undefined && (
        <span class="text-xs text-neutral-500">
          (run has no PRD snapshot)
        </span>
      )}
    </div>
  );
}

function StoriesList({
  fp,
  runId,
  stories,
}: {
  fp: string;
  runId: string;
  stories: StoryRecord[];
}) {
  if (stories.length === 0) {
    return (
      <div class="text-sm text-neutral-500 italic">
        No stories recorded for this run.
      </div>
    );
  }
  return (
    <ul class="divide-y divide-neutral-800 border border-neutral-800 rounded">
      {stories.map((st) => (
        <li key={st.story_id} class="p-3">
          <div class="flex items-center gap-2 mb-1">
            <span class="font-mono text-xs text-neutral-400">
              {st.story_id}
            </span>
            {st.final_status && (
              <Chip tone="neutral">{st.final_status}</Chip>
            )}
            {st.title && (
              <span class="text-sm text-neutral-200">{st.title}</span>
            )}
          </div>
          {st.iterations && st.iterations.length > 0 && (
            <ul class="flex flex-wrap gap-1.5 mt-1">
              {st.iterations.map((iter) => (
                <li key={iter.index}>
                  <a
                    href={`/repos/${fp}/runs/${runId}/iter/${encodeURIComponent(st.story_id)}/${iter.index}`}
                    class="text-xs font-mono px-2 py-0.5 rounded border border-neutral-700 hover:border-neutral-500 hover:bg-neutral-800 text-neutral-300"
                    title={`${iter.role} · ${iter.model ?? '—'}`}
                  >
                    #{iter.index} {iter.role}
                  </a>
                </li>
              ))}
            </ul>
          )}
        </li>
      ))}
    </ul>
  );
}
