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

  const wrapperClass = paired
    ? 'border-t border-neutral-800'
    : 'my-2 border border-neutral-800 rounded bg-neutral-900/50';

  const headerTone = block.is_error
    ? 'text-red-400'
    : paired
      ? 'text-neutral-500'
      : 'text-emerald-400';

  return (
    <div class={wrapperClass}>
      <div class="flex items-center gap-2 px-3 py-1.5">
        <span class={`text-[10px] uppercase tracking-wider ${headerTone}`}>
          {block.is_error ? 'tool error' : 'tool result'}
        </span>
        {needsCollapse && (
          <button
            type="button"
            onClick={() => setOpen(!open)}
            class="ml-auto text-[10px] text-neutral-500 hover:text-neutral-300 uppercase tracking-wider"
          >
            {open ? 'collapse' : `show all ${lines.length} lines`}
          </button>
        )}
      </div>
      <pre class="px-3 pb-2 text-xs text-neutral-300 whitespace-pre-wrap font-mono overflow-x-auto">
        {shown}
        {!open && needsCollapse ? '\n…' : ''}
      </pre>
    </div>
  );
}
