import { useState } from 'preact/hooks';
import type { Block } from '../../lib/turn-types';

const COLLAPSE_LINES = 10;

export function ToolResultCard({
  block,
  paired,
}: {
  block: Block;
  paired?: boolean;
}) {
  const text = block.output ?? '';
  const lines = text.split('\n');
  const needsCollapse = lines.length > COLLAPSE_LINES;
  const [open, setOpen] = useState(!needsCollapse);
  const shown = open ? text : lines.slice(0, COLLAPSE_LINES).join('\n');
  const err = block.is_error === true;

  return (
    <div style={{ padding: '8px 10px', background: paired ? 'transparent' : 'var(--bg-elev)' }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          fontSize: 10,
          color: err ? 'var(--err)' : 'var(--fg-ghost)',
          textTransform: 'uppercase',
          letterSpacing: '0.08em',
          marginBottom: 4,
        }}
      >
        <span>result{err ? ' · error' : ''}</span>
        {needsCollapse && (
          <button
            type="button"
            onClick={() => setOpen(!open)}
            style={{
              marginLeft: 'auto',
              color: 'var(--accent-ink)',
              fontSize: 11,
              textTransform: 'none',
              letterSpacing: 0,
            }}
          >
            {open ? 'collapse' : `show all ${lines.length} lines`}
          </button>
        )}
      </div>
      <pre
        class="code"
        style={{
          fontSize: 11.5,
          borderColor: err ? 'var(--err)' : 'var(--border-soft)',
          color: err ? 'var(--err)' : 'var(--fg)',
          whiteSpace: 'pre-wrap',
          overflowX: 'auto',
        }}
      >
        {shown}
        {!open && needsCollapse ? '\n…' : ''}
      </pre>
    </div>
  );
}
