import { useState } from 'preact/hooks';

export function ThinkingBlock({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const trimmed = text.trim();

  if (trimmed.length === 0) {
    return (
      <div
        style={{
          fontSize: 11.5,
          color: 'var(--fg-faint)',
          padding: '4px 8px',
          background: 'var(--bg-sunken)',
          border: '1px dashed var(--border)',
          borderRadius: 4,
          display: 'inline-block',
          marginBottom: 10,
          fontFamily: 'var(--font-mono)',
        }}
      >
        ✦ thinking · summary not provided
      </div>
    );
  }

  const firstLine = trimmed.split('\n', 1)[0].slice(0, 160);
  return (
    <div style={{ marginBottom: 10 }}>
      <button
        type="button"
        onClick={() => setOpen(!open)}
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 6,
          fontSize: 12,
          color: 'var(--fg-faint)',
          padding: '4px 0',
        }}
      >
        <span class="caret">{open ? '▾' : '▸'}</span>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11 }}>
          ✦ thinking
        </span>
        {!open && (
          <span
            style={{
              color: 'var(--fg-ghost)',
              fontStyle: 'italic',
            }}
          >
            · {firstLine}
          </span>
        )}
      </button>
      {open && (
        <div
          style={{
            marginTop: 6,
            padding: '10px 12px',
            background: 'var(--bg-sunken)',
            borderLeft: '2px solid var(--border-strong)',
            borderRadius: '0 6px 6px 0',
            fontSize: 13,
            color: 'var(--fg-muted)',
            lineHeight: 1.55,
            whiteSpace: 'pre-wrap',
            fontFamily: 'var(--font-sans)',
          }}
        >
          {text}
        </div>
      )}
    </div>
  );
}
