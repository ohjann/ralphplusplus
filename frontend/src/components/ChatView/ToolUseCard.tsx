import { useMemo } from 'preact/hooks';
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
  const inputJson = useMemo(() => {
    if (block.input === undefined) return '';
    try {
      return JSON.stringify(block.input, null, 2);
    } catch {
      return String(block.input);
    }
  }, [block.input]);

  return (
    <div class="my-2 rounded border border-neutral-800 bg-neutral-900/50">
      <div class="flex items-center gap-2 px-3 py-1.5 border-b border-neutral-800">
        <span class="text-[10px] uppercase tracking-wider text-neutral-500">
          tool
        </span>
        <span class="text-xs font-mono text-indigo-300">
          {block.tool_name || 'unknown'}
        </span>
        {block.tool_use_id && (
          <span class="text-[10px] font-mono text-neutral-600 ml-auto">
            {block.tool_use_id.slice(0, 8)}
          </span>
        )}
      </div>
      {inputJson && <ShikiCode code={inputJson} lang="json" />}
      {result && <ToolResultCard block={result} paired />}
    </div>
  );
}
