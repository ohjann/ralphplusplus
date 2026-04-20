import { signal, computed } from '@preact/signals';
import { useEffect } from 'preact/hooks';
import { useLocation } from 'preact-iso';
import { apiGet, type RepoSummary, type RunListItem } from '../../lib/api';

const repos = signal<RepoSummary[]>([]);
const runsByRepo = signal<Record<string, RunListItem[]>>({});
const expanded = signal<Set<string>>(new Set());
const filterText = signal<string>('');
const loadingRuns = signal<Set<string>>(new Set());
const repoError = signal<string>('');

const filtered = computed(() => {
  const q = filterText.value.trim().toLowerCase();
  if (!q) return repos.value;
  return repos.value.filter(
    (r) => r.path.toLowerCase().includes(q) || r.name.toLowerCase().includes(q),
  );
});

async function loadRepos() {
  try {
    const list = await apiGet<RepoSummary[]>('/api/repos');
    repos.value = list;
    repoError.value = '';
  } catch (e) {
    repoError.value = e instanceof Error ? e.message : String(e);
  }
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

function toggle(fp: string) {
  const next = new Set(expanded.value);
  if (next.has(fp)) {
    next.delete(fp);
  } else {
    next.add(fp);
    void loadRuns(fp);
  }
  expanded.value = next;
}

function shortHash(s: string): string {
  return s.slice(0, 8);
}

function fmtTime(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

function groupByKind(runs: RunListItem[]): Array<[string, RunListItem[]]> {
  const groups: Record<string, RunListItem[]> = {};
  for (const r of runs) {
    const k = r.kind || 'unknown';
    (groups[k] ??= []).push(r);
  }
  for (const k of Object.keys(groups)) {
    groups[k].sort((a, b) => b.startTime.localeCompare(a.startTime));
  }
  return Object.entries(groups).sort(([a], [b]) => a.localeCompare(b));
}

function runIdFromPath(path: string): string {
  const m = /^\/repos\/[^/]+\/runs\/([^/]+)/.exec(path);
  return m ? m[1] : '';
}

export function Sidebar() {
  useEffect(() => {
    void loadRepos();
  }, []);

  return (
    <aside class="w-80 h-screen overflow-y-auto bg-neutral-900 border-r border-neutral-800 text-neutral-100 text-sm">
      <div class="sticky top-0 z-10 p-3 bg-neutral-900 border-b border-neutral-800">
        <input
          type="text"
          placeholder="Filter repos (path prefix or name)"
          value={filterText.value}
          onInput={(e) =>
            (filterText.value = (e.currentTarget as HTMLInputElement).value)
          }
          class="w-full bg-neutral-800 border border-neutral-700 rounded px-2 py-1 placeholder:text-neutral-500 focus:outline-none focus:border-neutral-500"
        />
      </div>
      {repoError.value && (
        <div class="px-3 py-2 text-xs text-red-400">
          Failed to load repos: {repoError.value}
        </div>
      )}
      {filtered.value.length === 0 && !repoError.value && (
        <div class="px-3 py-6 text-xs text-neutral-500">
          {repos.value.length === 0 ? 'Loading…' : 'No repos match filter.'}
        </div>
      )}
      <ul class="py-1">
        {filtered.value.map((repo) => (
          <li key={repo.fp}>
            <RepoRow repo={repo} />
          </li>
        ))}
      </ul>
    </aside>
  );
}

function RepoRow({ repo }: { repo: RepoSummary }) {
  const isOpen = expanded.value.has(repo.fp);
  const runs = runsByRepo.value[repo.fp];
  const loading = loadingRuns.value.has(repo.fp);
  const anyRunning = (runs ?? []).some((r) => r.status === 'running');

  return (
    <div>
      <button
        type="button"
        onClick={() => toggle(repo.fp)}
        class="w-full text-left px-3 py-2 hover:bg-neutral-800 flex items-center gap-2 border-b border-neutral-900"
      >
        <span class="text-neutral-500 w-3 inline-block">{isOpen ? '▾' : '▸'}</span>
        <span class="flex-1 truncate">
          <span class="font-medium">{repo.name || repo.path}</span>
          <span class="block text-[11px] text-neutral-500 truncate">{repo.path}</span>
        </span>
        {anyRunning && <LiveDot />}
        <span class="text-[11px] text-neutral-500">{repo.runCount}</span>
      </button>
      {isOpen && (
        <div class="pl-5 pr-3 py-1 bg-neutral-925">
          {loading && !runs && (
            <div class="py-1 text-[11px] text-neutral-500">Loading runs…</div>
          )}
          {runs && runs.length === 0 && (
            <div class="py-1 text-[11px] text-neutral-500">No runs yet.</div>
          )}
          {runs && runs.length > 0 && <RunGroups fp={repo.fp} runs={runs} />}
        </div>
      )}
    </div>
  );
}

function RunGroups({ fp, runs }: { fp: string; runs: RunListItem[] }) {
  const groups = groupByKind(runs);
  return (
    <div class="flex flex-col gap-2 py-1">
      {groups.map(([kind, items]) => (
        <div key={kind}>
          <div class="text-[10px] uppercase tracking-wider text-neutral-500 px-1 pb-1">
            {kind}
          </div>
          <ul class="flex flex-col">
            {items.map((run) => (
              <li key={run.runId}>
                <RunRow fp={fp} run={run} />
              </li>
            ))}
          </ul>
        </div>
      ))}
    </div>
  );
}

function RunRow({ fp, run }: { fp: string; run: RunListItem }) {
  const href = `/repos/${fp}/runs/${run.runId}`;
  const isRunning = run.status === 'running';
  const loc = useLocation();
  const isActive = runIdFromPath(loc.path) === run.runId;
  return (
    <a
      href={href}
      class={
        'flex items-center gap-2 px-1 py-1 rounded text-xs hover:bg-neutral-800 ' +
        (isActive ? 'bg-neutral-800' : '')
      }
    >
      {isRunning ? (
        <LiveDot />
      ) : (
        <span class="w-2 h-2 inline-block rounded-full bg-neutral-700" />
      )}
      <span class="font-mono text-neutral-300">{shortHash(run.runId)}</span>
      <span class="text-neutral-500 truncate">{fmtTime(run.startTime)}</span>
    </a>
  );
}

function LiveDot() {
  return (
    <span
      class="w-2 h-2 inline-block rounded-full bg-emerald-500 animate-pulse"
      title="Running"
    />
  );
}
