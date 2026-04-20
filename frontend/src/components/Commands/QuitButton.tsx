import { useState } from 'preact/hooks';
import { sendCommand } from '../../lib/commands';

export function QuitButton({ fp }: { fp: string }) {
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);

  async function doQuit() {
    if (busy) return;
    setBusy(true);
    try {
      await sendCommand(fp, 'quit', undefined, {
        success: 'Daemon shutting down',
        errorPrefix: 'Quit failed',
      });
    } catch {
      /* toast fired */
    } finally {
      setBusy(false);
      setConfirming(false);
    }
  }

  if (!confirming) {
    return (
      <button
        type="button"
        onClick={() => setConfirming(true)}
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 6,
          padding: '5px 10px',
          fontSize: 12,
          border: '1px solid var(--err)',
          borderRadius: 5,
          background: 'var(--bg-elev)',
          color: 'var(--err)',
        }}
      >
        <span aria-hidden>■</span>
        quit
      </button>
    );
  }

  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
      <span style={{ fontSize: 11.5, color: 'var(--fg-faint)' }}>
        Quit daemon?
      </span>
      <button
        type="button"
        onClick={doQuit}
        disabled={busy}
        style={{
          padding: '3px 8px',
          fontSize: 11,
          border: '1px solid var(--err)',
          borderRadius: 4,
          background: 'var(--err-soft)',
          color: 'var(--err)',
          opacity: busy ? 0.5 : 1,
        }}
      >
        {busy ? '…' : 'confirm'}
      </button>
      <button
        type="button"
        onClick={() => setConfirming(false)}
        disabled={busy}
        style={{
          padding: '3px 8px',
          fontSize: 11,
          border: '1px solid var(--border)',
          borderRadius: 4,
          background: 'transparent',
          color: 'var(--fg-muted)',
        }}
      >
        cancel
      </button>
    </div>
  );
}
