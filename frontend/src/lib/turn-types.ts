// Wire types for the TurnV1 schema emitted by
// internal/viewer/transcript/parser.go.

export interface Usage {
  input_tokens?: number;
  output_tokens?: number;
  cache_read_input_tokens?: number;
  cache_creation_input_tokens?: number;
}

export interface Block {
  kind: 'text' | 'thinking' | 'tool_use' | 'tool_result' | string;
  text?: string;
  tool_name?: string;
  tool_use_id?: string;
  input?: unknown;
  output?: string;
  is_error?: boolean;
}

export interface Turn {
  index: number;
  role: 'user' | 'assistant' | 'system' | string;
  blocks: Block[];
  stop_reason?: string;
  started_at?: string;
  usage?: Usage;
}
