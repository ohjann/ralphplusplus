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
  // Pure-tool_result user turns are the paired half of a tool_use — render
  // under the tool card instead.
  const isPairedToolResults =
    role === 'user' &&
    turn.blocks.length > 0 &&
    turn.blocks.every((b) => b.kind === 'tool_result');
  if (isPairedToolResults) return null;

  const isUser = role === 'user';

  return (
    <article
      style={{
        border: `1px solid ${isUser ? 'var(--accent-border)' : 'var(--border)'}`,
        borderRadius: 10,
        padding: '14px 16px',
        background: isUser ? 'var(--accent-soft)' : 'var(--bg-elev)',
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 7,
          marginBottom: 8,
        }}
      >
        <span
          style={{
            fontSize: 10.5,
            textTransform: 'uppercase',
            letterSpacing: '0.09em',
            fontWeight: 600,
            color: isUser ? 'var(--accent-ink)' : 'var(--fg-faint)',
          }}
        >
          {role}
        </span>
        <span
          class="mono"
          style={{ fontSize: 10.5, color: 'var(--fg-ghost)' }}
        >
          #{turn.index}
        </span>
        {turn.stop_reason && (
          <span
            style={{
              fontSize: 10.5,
              color: 'var(--fg-ghost)',
              marginLeft: 'auto',
            }}
          >
            {turn.stop_reason}
          </span>
        )}
      </div>
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
      return <ToolResultCard block={block} />;
    case 'text':
    default:
      return <AssistantText markdown={block.text ?? ''} />;
  }
}
