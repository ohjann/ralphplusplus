import { useEffect, useState } from 'preact/hooks';
import { highlight } from '../../lib/highlight';
import styles from './ChatView.module.css';

export function ShikiCode({ code, lang }: { code: string; lang: string }) {
  const [html, setHtml] = useState<string>('');
  useEffect(() => {
    let cancelled = false;
    highlight(code, lang)
      .then((h) => {
        if (!cancelled) setHtml(h);
      })
      .catch(() => {
        /* leave html empty → fall back to plain pre */
      });
    return () => {
      cancelled = true;
    };
  }, [code, lang]);

  if (!html) {
    return (
      <pre class={`${styles.codeBlock} ${styles.codeBlockFallback}`}>
        <code>{code}</code>
      </pre>
    );
  }
  return (
    <div class={styles.codeBlock} dangerouslySetInnerHTML={{ __html: html }} />
  );
}
