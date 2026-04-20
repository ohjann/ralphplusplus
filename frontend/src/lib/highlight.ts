// Main-thread client for the shiki web worker. Call highlight(code, lang)
// and await the HTML string. Subsequent calls are multiplexed over a
// single worker via a monotonically increasing request id.

type Pending = {
  resolve: (html: string) => void;
  reject: (err: Error) => void;
};

let worker: Worker | null = null;
const pending = new Map<number, Pending>();
let nextId = 0;

function ensureWorker(): Worker {
  if (worker) return worker;
  worker = new Worker(new URL('../workers/shiki.worker.ts', import.meta.url), {
    type: 'module',
  });
  worker.addEventListener('message', (ev: MessageEvent) => {
    const { id, html, error } = ev.data as {
      id: number;
      html?: string;
      error?: string;
    };
    const p = pending.get(id);
    if (!p) return;
    pending.delete(id);
    if (error) p.reject(new Error(error));
    else p.resolve(html ?? '');
  });
  worker.addEventListener('error', (ev) => {
    for (const p of pending.values()) p.reject(new Error(ev.message));
    pending.clear();
  });
  return worker;
}

export function highlight(code: string, lang: string): Promise<string> {
  const id = nextId++;
  return new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    ensureWorker().postMessage({ id, code, lang });
  });
}
