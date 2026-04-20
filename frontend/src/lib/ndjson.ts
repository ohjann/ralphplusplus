// streamNdjson yields one parsed JSON object per line of a fetch body.
// Each line is JSON.parse'd; partial trailing data is held until the next
// chunk (or flushed at EOF if non-empty). Errors from fetch or JSON.parse
// propagate to the iterator consumer.

export async function* streamNdjson<T>(
  url: string,
  init?: RequestInit,
): AsyncIterable<T> {
  const res = await fetch(url, init);
  if (!res.ok) {
    throw new Error(`${url}: ${res.status} ${res.statusText}`);
  }
  if (!res.body) {
    throw new Error(`${url}: no response body`);
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = '';
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });
      let nl: number;
      while ((nl = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, nl);
        buf = buf.slice(nl + 1);
        if (line.length === 0) continue;
        yield JSON.parse(line) as T;
      }
    }
    buf += decoder.decode();
    if (buf.trim().length > 0) {
      yield JSON.parse(buf) as T;
    }
  } finally {
    try {
      reader.releaseLock();
    } catch {
      /* noop */
    }
  }
}
