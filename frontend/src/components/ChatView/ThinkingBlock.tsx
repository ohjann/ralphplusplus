import { useState } from 'preact/hooks';

export function ThinkingBlock({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const firstLine = text.split('\n', 1)[0].slice(0, 160);
  return (
    <div class="border-l-2 border-neutral-700 pl-3 my-2 text-neutral-400">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        class="text-[11px] uppercase tracking-wider text-neutral-500 hover:text-neutral-300 flex items-center gap-1"
      >
        <span>{open ? '▾' : '▸'}</span>
        <span>thinking</span>
        {!open && firstLine && (
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
