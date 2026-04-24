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

async function load(fp: string, runId: string, story: string, iter: string) {
  const key = storageKey(fp, runId, story, iter);
  loadingKey.value = key;
  turnsByKey.value = { ...turnsByKey.value, [key]: [] };
  errorByKey.value = { ...errorByKey.value, [key]: '' };
  // follow=true replays existing turns then tails the jsonl for live writes.
  // The backend closes immediately when the manifest is terminal, so this is
  // safe for archived iterations too.
  const url =
    `/api/repos/${fp}/runs/${runId}/transcript/` +
    `${encodeURIComponent(story)}/${encodeURIComponent(iter)}?follow=true`;
  try {
    const seen = new Map<number, true>();
    for await (const t of streamNdjson<TurnT>(url, {
      headers: {
        'X-Ralph-Token': sessionStorage.getItem('ralph.token') ?? '',
      },
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

// ChatStream renders the turns for a single iteration and (by default) tails
// the transcript jsonl for live updates. Shared between the full-page
// IterRoute and the embedded live panel on the Run page.
export function ChatStream({
  fp,
  runId,
  story,
  iter,
  maxWidth,
}: {
  fp: string;
  runId: string;
  story: string;
  iter: string;
  maxWidth?: number;
}) {
  const key = storageKey(fp, runId, story, iter);
  const turns = computed(() => turnsByKey.value[key] ?? []);
  const err = computed(() => errorByKey.value[key] ?? '');
  const pairs = computed(() => pairToolResults(turns.value));

  useEffect(() => {
    void load(fp, runId, story, iter);
  }, [fp, runId, story, iter]);

  return (
    <>
      {err.value && (
        <div
          style={{
            padding: '10px 12px',
            marginBottom: 14,
            borderRadius: 6,
            background: 'var(--err-soft)',
            border: '1px solid var(--err)',
            color: 'var(--err)',
            fontSize: 13,
          }}
        >
          {err.value}
        </div>
      )}
      {turns.value.length === 0 && !err.value && (
        <div
          style={{
            fontSize: 13,
            color: 'var(--fg-faint)',
            fontStyle: 'italic',
          }}
        >
          Waiting for turns…
        </div>
      )}

      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          gap: 14,
          maxWidth: maxWidth ?? 820,
        }}
      >
        {turns.value.map((t) => (
          <Turn key={t.index} turn={t} toolResults={pairs.value} />
        ))}
      </div>
    </>
  );
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

  return (
    <div style={{ padding: '22px 28px 80px', minHeight: '100%' }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 6,
          fontSize: 12,
          color: 'var(--fg-faint)',
          fontFamily: 'var(--font-mono)',
          marginBottom: 12,
          flexWrap: 'wrap',
        }}
      >
        <a
          href={`/repos/${fp}/runs/${runId}`}
          style={{ color: 'var(--fg-faint)' }}
        >
          ← back to run
        </a>
        <span style={{ color: 'var(--fg-ghost)' }}>/</span>
        <span>{story}</span>
        <span style={{ color: 'var(--fg-ghost)' }}>/</span>
        <span style={{ color: 'var(--fg)' }}>iter {iter}</span>
      </div>

      <h1
        style={{
          fontSize: 20,
          fontWeight: 600,
          letterSpacing: '-0.01em',
          margin: '0 0 4px',
          color: 'var(--fg)',
        }}
      >
        Iteration transcript
      </h1>
      <div
        style={{
          fontSize: 12.5,
          color: 'var(--fg-faint)',
          marginBottom: 20,
        }}
      >
        Streaming NDJSON · {turns.value.length} turns
      </div>

      <ChatStream fp={fp} runId={runId} story={story} iter={iter} />
    </div>
  );
}
