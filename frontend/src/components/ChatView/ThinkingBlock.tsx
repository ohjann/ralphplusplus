import { useState } from 'preact/hooks';

export function ThinkingBlock({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const trimmed = text.trim();

  // Anthropic's extended-thinking API sometimes streams only a signature_delta
  // (cryptographic commitment) without the plaintext thinking_delta events —
  // typically on long thinking sequences or cache-heavy calls. The thinking
  // happened server-side but the text isn't in the stream. Render a
  // non-clickable tag so users aren't confused by an empty dropdown.
  // (Distinct from Anthropic's `redacted_thinking` block type, which is a
  // separate kind we'd render differently if it appears.)
  if (trimmed.length === 0) {
    return (
      <div class="border-l-2 border-neutral-800 pl-3 my-2 text-[11px] uppercase tracking-wider text-neutral-600">
        thinking · summary not provided
      </div>
    );
  }

  const firstLine = trimmed.split('\n', 1)[0].slice(0, 160);
  return (
    <div class="border-l-2 border-neutral-700 pl-3 my-2 text-neutral-400">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        class="text-[11px] uppercase tracking-wider text-neutral-500 hover:text-neutral-300 flex items-center gap-1"
      >
        <span>{open ? '▾' : '▸'}</span>
        <span>thinking</span>
        {!open && (
          <span class="normal-case tracking-normal text-neutral-500 italic truncate max-w-md">
            · {firstLine}
          </span>
        )}
      </button>
      {open && (
        <pre class="mt-1 whitespace-pre-wrap text-xs text-neutral-400 font-sans">
          {text}
        </pre>
      )}
    </div>
  );
}
