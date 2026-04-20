import { useEffect } from 'preact/hooks';
import { signal, computed } from '@preact/signals';
import { streamNdjson } from '../../lib/ndjson';
import { pairToolResults } from '../../lib/pair-tool-results';
import type { Turn as TurnT } from '../../lib/turn-types';
import { Turn } from './Turn';

const turnsByKey = signal<Record<string, TurnT[]>>({});
const loadingKey = signal<string>('');
const errorByKey = signal<Record<string, string>>({});

function storageKey(fp: string, runId: string, story: string, iter: string) {
  return `${fp}/${runId}/${story}/${iter}`;
}

async function load(
  fp: string,
  runId: string,
  story: string,
  iter: string,
) {
  const key = storageKey(fp, runId, story, iter);
  loadingKey.value = key;
  turnsByKey.value = { ...turnsByKey.value, [key]: [] };
  errorByKey.value = { ...errorByKey.value, [key]: '' };
  const url =
    `/api/repos/${fp}/runs/${runId}/transcript/` +
    `${encodeURIComponent(story)}/${encodeURIComponent(iter)}`;
  try {
    // Reconnect-safe de-dup by Turn.index — follow=true reconnects would
    // replay from 0, so we keep the highest Index we've seen and skip
    // duplicates.
    const seen = new Map<number, true>();
    for await (const t of streamNdjson<TurnT>(url, {
      headers: { 'X-Ralph-Token': sessionStorage.getItem('ralph.token') ?? '' },
    })) {
      if (loadingKey.value !== key) return;
      if (seen.has(t.index)) continue;
      seen.set(t.index, true);
      const prev = turnsByKey.value[key] ?? [];
      turnsByKey.value = { ...turnsByKey.value, [key]: [...prev, t] };
    }
  } catch (e) {
    errorByKey.value = {
      ...errorByKey.value,
      [key]: e instanceof Error ? e.message : String(e),
    };
  }
}

export function ChatView({
  fp,
  runId,
  story,
  iter,
}: {
  fp: string;
  runId: string;
  story: string;
  iter: string;
}) {
  const key = storageKey(fp, runId, story, iter);
  const turns = computed(() => turnsByKey.value[key] ?? []);
  const err = computed(() => errorByKey.value[key] ?? '');
  const pairs = computed(() => pairToolResults(turns.value));

  useEffect(() => {
    void load(fp, runId, story, iter);
  }, [fp, runId, story, iter]);

  return (
    <div class="p-6 max-w-4xl">
      <header class="mb-4">
        <div class="text-xs text-neutral-500 font-mono">
          {story} · iter {iter}
        </div>
        <div class="text-[11px] text-neutral-600">
          run {runId.slice(0, 12)}
        </div>
      </header>
      {err.value && (
        <div class="p-3 mb-3 rounded bg-red-500/10 border border-red-500/30 text-red-300 text-sm">
          {err.value}
        </div>
      )}
      {turns.value.length === 0 && !err.value && (
        <div class="text-sm text-neutral-500 italic">Loading transcript…</div>
      )}
      {turns.value.map((t) => (
        <Turn key={t.index} turn={t} toolResults={pairs.value} />
      ))}
    </div>
  );
}
