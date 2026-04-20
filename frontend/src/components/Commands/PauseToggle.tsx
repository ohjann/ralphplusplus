import { useState } from 'preact/hooks';
import { sendCommand } from '../../lib/commands';

export function PauseToggle({
  fp,
  paused,
}: {
  fp: string;
  paused: boolean;
}) {
  const [busy, setBusy] = useState(false);

  async function handleClick() {
    if (busy) return;
    setBusy(true);
    try {
      await sendCommand(
        fp,
        paused ? 'resume' : 'pause',
        undefined,
        paused
          ? { success: 'Resumed', errorPrefix: 'Resume failed' }
          : { success: 'Paused', errorPrefix: 'Pause failed' },
      );
    } catch {
      /* toast fired */
    } finally {
      setBusy(false);
    }
  }

  const warn = paused;
  return (
    <button
      type="button"
      onClick={handleClick}
      disabled={busy}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 6,
        padding: '5px 10px',
        fontSize: 12,
        border: '1px solid',
        borderColor: warn ? 'var(--warn)' : 'var(--border)',
        borderRadius: 5,
        background: warn ? 'var(--warn-soft)' : 'var(--bg-elev)',
        color: warn ? 'var(--warn)' : 'var(--fg)',
        opacity: busy ? 0.5 : 1,
      }}
    >
      <span aria-hidden>{paused ? '▶' : '⏸'}</span>
      {paused ? 'resume' : 'pause'}
    </button>
  );
}
