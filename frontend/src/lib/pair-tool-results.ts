import type { Turn, Block } from './turn-types';

// Builds an index from tool_use_id → the tool_result block that answered it.
// Walks turns in order; a tool_result in turn N answers the tool_use with the
// same id in any earlier turn (typically N-1 but not always — the agent can
// interleave). Later tool_results for the same id overwrite earlier ones,
// which matches Claude's semantics for retried tool calls.
export function pairToolResults(turns: Turn[]): Map<string, Block> {
  const map = new Map<string, Block>();
  for (const t of turns) {
    for (const b of t.blocks) {
      if (b.kind === 'tool_result' && b.tool_use_id) {
        map.set(b.tool_use_id, b);
      }
    }
  }
  return map;
}
