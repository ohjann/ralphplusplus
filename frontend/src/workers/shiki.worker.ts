// Web-worker wrapper around shiki core. Static imports for themes + langs
// so Vite can tree-shake to a single worker chunk — no per-language chunks.

import { createHighlighterCore, type HighlighterCore } from 'shiki/core';
import { createOnigurumaEngine } from 'shiki/engine/oniguruma';

import bash from 'shiki/langs/bash.mjs';
import diff from 'shiki/langs/diff.mjs';
import go from 'shiki/langs/go.mjs';
import html from 'shiki/langs/html.mjs';
import javascript from 'shiki/langs/javascript.mjs';
import json from 'shiki/langs/json.mjs';
import markdown from 'shiki/langs/markdown.mjs';
import python from 'shiki/langs/python.mjs';
import shell from 'shiki/langs/shellscript.mjs';
import sql from 'shiki/langs/sql.mjs';
import tsx from 'shiki/langs/tsx.mjs';
import typescript from 'shiki/langs/typescript.mjs';
import yaml from 'shiki/langs/yaml.mjs';

import githubDark from 'shiki/themes/github-dark.mjs';

const LANGS = [
  bash,
  diff,
  go,
  html,
  javascript,
  json,
  markdown,
  python,
  shell,
  sql,
  tsx,
  typescript,
  yaml,
];
const LANG_NAMES = new Set([
  'bash',
  'diff',
  'go',
  'html',
  'javascript',
  'js',
  'json',
  'markdown',
  'md',
  'python',
  'py',
  'shell',
  'sh',
  'shellscript',
  'sql',
  'tsx',
  'typescript',
  'ts',
  'yaml',
  'yml',
]);

type Req = { id: number; code: string; lang: string };
type Res = { id: number; html?: string; error?: string };

let hlPromise: Promise<HighlighterCore> | null = null;

function getHL(): Promise<HighlighterCore> {
  if (!hlPromise) {
    hlPromise = createHighlighterCore({
      themes: [githubDark],
      langs: LANGS,
      engine: createOnigurumaEngine(() => import('shiki/wasm')),
    });
  }
  return hlPromise;
}

self.addEventListener('message', async (ev: MessageEvent<Req>) => {
  const { id, code, lang } = ev.data;
  try {
    const hl = await getHL();
    const resolved = LANG_NAMES.has(lang) ? lang : 'text';
    const out = hl.codeToHtml(code, {
      lang: resolved,
      theme: 'github-dark',
    });
    self.postMessage({ id, html: out } satisfies Res);
  } catch (e) {
    self.postMessage({
      id,
      error: e instanceof Error ? e.message : String(e),
    } satisfies Res);
  }
});
