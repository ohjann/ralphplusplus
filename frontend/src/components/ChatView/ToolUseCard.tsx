import { useMemo, useState } from 'preact/hooks';
import { ShikiCode } from './ShikiCode';
import type { Block } from '../../lib/turn-types';
import { ToolResultCard } from './ToolResultCard';

export function ToolUseCard({
  block,
  result,
}: {
  block: Block;
  result?: Block;
}) {
  const [open, setOpen] = useState(true);
  const inputJson = useMemo(() => {
    if (block.input === undefined) return '';
    try {
      return JSON.stringify(block.input, null, 2);
    } catch {
      return String(block.input);
    }
  }, [block.input]);

  const inputSummary = useMemo(() => {
    if (!block.input || typeof block.input !== 'object') return '';
    try {
      return Object.entries(block.input as Record<string, unknown>)
        .map(
          ([k, v]) =>
            `${k}: ${typeof v === 'string' ? `"${v.slice(0, 60)}${v.length > 60 ? '…' : ''}"` : v}`,
        )
        .join(', ')
        .slice(0, 180);
    } catch {
      return '';
    }
  }, [block.input]);

  const hasErr = result?.is_error === true;

  return (
    <div
      style={{
        marginTop: 10,
        border: '1px solid var(--border)',
        borderRadius: 8,
        overflow: 'hidden',
      }}
    >
      <button
        type="button"
        onClick={() => setOpen(!open)}
        style={{
          width: '100%',
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          padding: '7px 10px',
          background: 'var(--bg-sunken)',
          borderBottom: open ? '1px solid var(--border)' : 'none',
          textAlign: 'left',
        }}
      >
        <span class="caret">{open ? '▾' : '▸'}</span>
        <span class="chip indigo mono" style={{ padding: '1px 6px' }}>
          🔧 {block.tool_name || 'unknown'}
        </span>
        <span
          class="mono"
          style={{
            fontSize: 11,
            color: 'var(--fg-faint)',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
            flex: 1,
            minWidth: 0,
          }}
        >
          {inputSummary}
        </span>
        {hasErr && (
          <span class="chip err" style={{ fontSize: 10 }}>
            error
          </span>
        )}
      </button>
      {open && (
        <div>
          {inputJson && (
            <div
              style={{
                padding: '8px 10px',
                borderBottom: '1px solid var(--border-soft)',
              }}
            >
              <div
                style={{
                  fontSize: 10,
                  color: 'var(--fg-ghost)',
                  textTransform: 'uppercase',
                  letterSpacing: '0.08em',
                  marginBottom: 4,
                }}
              >
                input
              </div>
              <ShikiCode code={inputJson} lang="json" />
            </div>
          )}
          {result && <ToolResultCard block={result} paired />}
        </div>
      )}
    </div>
  );
}
