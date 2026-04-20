import type { Turn as TurnT, Block } from '../../lib/turn-types';
import { AssistantText } from './AssistantText';
import { ThinkingBlock } from './ThinkingBlock';
import { ToolUseCard } from './ToolUseCard';
import { ToolResultCard } from './ToolResultCard';

export function Turn({
  turn,
  toolResults,
}: {
  turn: TurnT;
  toolResults: Map<string, Block>;
}) {
  const role = turn.role;
  // A "user" turn carrying only tool_result blocks is the paired half of an
  // earlier tool_use — we render it as part of the corresponding ToolUseCard
  // instead. If the user turn has any text/thinking blocks, render the turn
  // normally so we don't hide real user content.
  const isPairedToolResults =
    role === 'user' &&
    turn.blocks.length > 0 &&
    turn.blocks.every((b) => b.kind === 'tool_result');
  if (isPairedToolResults) return null;

  const bubbleClass =
    role === 'assistant'
      ? 'bg-neutral-900/40 border border-neutral-800'
      : role === 'user'
        ? 'bg-indigo-500/5 border border-indigo-500/20'
        : 'bg-neutral-900/20 border border-neutral-800';

  return (
    <article class={`my-3 rounded px-4 py-3 ${bubbleClass}`}>
      <header class="flex items-center gap-2 mb-2">
        <span class="text-[10px] uppercase tracking-wider text-neutral-500">
          {role}
        </span>
        <span class="text-[10px] font-mono text-neutral-600">#{turn.index}</span>
        {turn.stop_reason && (
          <span class="text-[10px] text-neutral-600 ml-auto">
            {turn.stop_reason}
          </span>
        )}
      </header>
      {turn.blocks.map((b, i) => (
        <BlockView key={i} block={b} toolResults={toolResults} />
      ))}
    </article>
  );
}

function BlockView({
  block,
  toolResults,
}: {
  block: Block;
  toolResults: Map<string, Block>;
}) {
  switch (block.kind) {
    case 'thinking':
      return <ThinkingBlock text={block.text ?? ''} />;
    case 'tool_use': {
      const paired = block.tool_use_id
        ? toolResults.get(block.tool_use_id)
        : undefined;
      return <ToolUseCard block={block} result={paired} />;
    }
    case 'tool_result':
      // Unpaired tool_results (rare) render standalone. Paired ones render
      // inside their ToolUseCard; the turn.tsx wrapper swallows pure-
      // tool_result user turns above.
      return <ToolResultCard block={block} />;
    case 'text':
    default:
      return <AssistantText markdown={block.text ?? ''} />;
  }
}
