import { toasts, dismissToast, type ToastTone } from '../lib/toast';

const TONE_BORDER: Record<ToastTone, string> = {
  success: 'var(--ok)',
  error: 'var(--err)',
  info: 'var(--fg-muted)',
  warn: 'var(--warn)',
};

export function ToastStack() {
  const list = toasts.value;
  if (list.length === 0) return null;
  return (
    <div
      style={{
        position: 'fixed',
        right: 16,
        bottom: 16,
        display: 'flex',
        flexDirection: 'column',
        gap: 8,
        zIndex: 60,
        pointerEvents: 'none',
      }}
    >
      {list.map((t) => (
        <div
          key={t.id}
          style={{
            pointerEvents: 'auto',
            padding: '9px 13px',
            border: '1px solid var(--border-strong)',
            borderLeft: `3px solid ${TONE_BORDER[t.tone]}`,
            borderRadius: 6,
            background: 'var(--bg-elev)',
            fontSize: 12.5,
            color: 'var(--fg)',
            boxShadow: 'var(--shadow-md)',
            minWidth: 220,
            maxWidth: 360,
            display: 'flex',
            alignItems: 'flex-start',
            gap: 10,
            animation: 'rv-toast-in 220ms ease-out',
          }}
        >
          <span style={{ flex: 1, whiteSpace: 'pre-wrap' }}>{t.text}</span>
          <button
            type="button"
            onClick={() => dismissToast(t.id)}
            style={{
              color: 'var(--fg-faint)',
              fontSize: 14,
              lineHeight: 1,
            }}
            aria-label="Dismiss"
          >
            ×
          </button>
        </div>
      ))}
      <style>{`
        @keyframes rv-toast-in {
          from { opacity: 0; transform: translateX(8px); }
          to   { opacity: 1; transform: translateX(0); }
        }
      `}</style>
    </div>
  );
}
