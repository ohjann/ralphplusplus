import { useMemo } from 'preact/hooks';
import { marked, type Token } from 'marked';
import { ShikiCode } from './ShikiCode';
import styles from './ChatView.module.css';

// Parse markdown into tokens so we can hand fenced code blocks off to the
// shiki web worker (async) while rendering the rest synchronously. Non-code
// tokens are stitched back through marked.parser so paragraph structure,
// lists, links, inline code, etc. all work.
export function AssistantText({ markdown }: { markdown: string }) {
  const groups = useMemo(() => {
    const tokens = marked.lexer(markdown);
    const acc: Array<
      | { kind: 'html'; html: string }
      | { kind: 'code'; code: string; lang: string }
    > = [];
    let pending: Token[] = [];
    const flush = () => {
      if (pending.length === 0) return;
      acc.push({
        kind: 'html',
        html: marked.parser(pending as Token[] & { links: Record<string, unknown> }),
      });
      pending = [];
    };
    for (const tok of tokens) {
      if (tok.type === 'code') {
        flush();
        acc.push({
          kind: 'code',
          code: (tok as { text: string }).text,
          lang: (tok as { lang?: string }).lang || 'text',
        });
      } else {
        pending.push(tok);
      }
    }
    flush();
    return acc;
  }, [markdown]);

  return (
    <div class={styles.prose}>
      {groups.map((g, i) =>
        g.kind === 'code' ? (
          <ShikiCode key={i} code={g.code} lang={g.lang} />
        ) : (
          <span key={i} dangerouslySetInnerHTML={{ __html: g.html }} />
        ),
      )}
    </div>
  );
}
