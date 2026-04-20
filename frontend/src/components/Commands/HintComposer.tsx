import { useState } from 'preact/hooks';
import { sendCommand } from '../../lib/commands';
import type { WorkerStatus } from '../../lib/live';

export function HintComposer({
  fp,
  workers,
}: {
  fp: string;
  workers: WorkerStatus[];
}) {
  const [workerId, setWorkerId] = useState<number | ''>(
    workers.length > 0 ? workers[0].id : '',
  );
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(e: Event) {
    e.preventDefault();
    if (busy || workerId === '' || text.trim() === '') return;
    setBusy(true);
    try {
      await sendCommand(
        fp,
        'hint',
        { worker_id: workerId, text },
        {
          success: `Hint sent to worker #${workerId}`,
          errorPrefix: 'Hint failed',
        },
      );
      setText('');
    } catch {
      /* toast fired */
    } finally {
      setBusy(false);
    }
  }

  if (workers.length === 0) {
    return (
      <div
        style={{
          fontSize: 11,
          color: 'var(--fg-faint)',
          fontStyle: 'italic',
        }}
      >
        No active workers — hints target a specific worker.
      </div>
    );
  }

  return (
    <form
      onSubmit={submit}
      style={{ display: 'flex', flexDirection: 'column', gap: 5 }}
    >
      <select
        value={workerId === '' ? '' : String(workerId)}
        onChange={(e) => {
          const v = (e.currentTarget as HTMLSelectElement).value;
          setWorkerId(v === '' ? '' : Number(v));
        }}
        style={{
          padding: '5px 8px',
          fontSize: 12,
          background: 'var(--bg-elev)',
          border: '1px solid var(--border)',
          borderRadius: 5,
          color: 'var(--fg)',
        }}
      >
        {workers.map((w) => (
          <option key={w.id} value={String(w.id)}>
            #{w.id} · {w.role} · {w.story_id || '—'}
          </option>
        ))}
      </select>
      <textarea
        value={text}
        onInput={(e) => setText((e.currentTarget as HTMLTextAreaElement).value)}
        placeholder="Hint to prepend to the next iteration's prompt…"
        rows={2}
        style={{
          padding: '6px 8px',
          background: 'var(--bg-elev)',
          border: '1px solid var(--border)',
          borderRadius: 5,
          fontSize: 12,
          color: 'var(--fg)',
          resize: 'vertical',
          fontFamily: 'var(--font-sans)',
        }}
      />
      <button
        type="submit"
        disabled={busy || workerId === '' || text.trim() === ''}
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          gap: 6,
          padding: '5px 10px',
          fontSize: 12,
          border: '1px solid var(--accent-border)',
          borderRadius: 5,
          background: 'var(--accent-soft)',
          color: 'var(--accent-ink)',
          opacity:
            busy || workerId === '' || text.trim() === '' ? 0.5 : 1,
        }}
      >
        {busy ? 'sending…' : '→ send hint'}
      </button>
    </form>
  );
}
