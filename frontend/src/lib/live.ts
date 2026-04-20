// Wire types for the daemon event stream. Mirrors
// internal/daemon/protocol.go — the DaemonStateEvent plus sibling event
// shapes (WorkerLog, LogLine, MergeResult, StuckAlert) are all delivered
// through the DaemonEvent envelope on /api/live/:fp/events.

export interface WorkerStatus {
  id: number;
  story_id: string;
  story_title: string;
  state: string;
  role: string;
  iteration: number;
  activity_path: string;
  fusion_suffix?: string;
}

export interface StoryStatus {
  id: string;
  title: string;
  in_progress: boolean;
  completed: boolean;
  failed: boolean;
  failed_error?: string;
  blocked_by_dep?: string;
}

export interface CostTotals {
  total_cost: number;
  total_input_tokens: number;
  total_output_tokens: number;
}

export interface PlanQualityInfo {
  first_pass_count: number;
  retry_count: number;
  failed_count: number;
  total_stories: number;
  score: number;
}

export interface FusionMetrics {
  [k: string]: unknown;
}

export interface DaemonStateEvent {
  workers: Record<string, WorkerStatus>;
  stories: Record<string, StoryStatus>;
  active_story_ids: string[];
  phase: string;
  paused: boolean;
  total_stories: number;
  completed_count: number;
  failed_count: number;
  iteration_count: number;
  all_done: boolean;
  cost_totals: CostTotals;
  plan_quality: PlanQualityInfo;
  fusion_metrics: FusionMetrics;
  uptime: string;
  client_count: number;
  timestamp: string;
}

export interface MergeResultEvent {
  story_id: string;
  success: boolean;
  error?: string;
}

export interface StuckAlertEvent {
  worker_id: number;
  story_id: string;
  stuck_reason: string;
}

export type DaemonEventPayload =
  | { kind: 'state'; data: DaemonStateEvent }
  | { kind: 'merge_result'; data: MergeResultEvent }
  | { kind: 'stuck_alert'; data: StuckAlertEvent }
  | { kind: 'unknown'; data: unknown };

// LiveStream wraps EventSource with reconnect backoff. It exposes:
//   - onEvent(cb): invoked for every parsed payload
//   - onStatus(cb): invoked with 'open' | 'error' | 'closed' transitions
//   - close(): stops the stream, prevents further reconnects
// The stream auto-reconnects with exponential backoff capped at 15s.
// Token is read from sessionStorage and appended as ?token= (EventSource
// does not support custom headers in browsers).

type EventCb = (ev: DaemonEventPayload) => void;
type StatusCb = (status: 'open' | 'error' | 'closed') => void;

export interface LiveStream {
  onEvent(cb: EventCb): () => void;
  onStatus(cb: StatusCb): () => void;
  close(): void;
}

function classifyPayload(raw: unknown): DaemonEventPayload {
  // Daemon wire envelope is {type, data}. Unwrap before classifying.
  if (raw && typeof raw === 'object') {
    const env = raw as { type?: string; data?: unknown };
    if (typeof env.type === 'string' && env.data !== undefined) {
      switch (env.type) {
        case 'daemon_state':
          return { kind: 'state', data: env.data as DaemonStateEvent };
        case 'merge_result':
          return { kind: 'merge_result', data: env.data as MergeResultEvent };
        case 'stuck_alert':
          return { kind: 'stuck_alert', data: env.data as StuckAlertEvent };
      }
    }
  }
  return { kind: 'unknown', data: raw };
}

export function openLive(fp: string): LiveStream {
  const eventCbs = new Set<EventCb>();
  const statusCbs = new Set<StatusCb>();
  let es: EventSource | null = null;
  let closed = false;
  let backoff = 500;
  const MAX_BACKOFF = 15000;
  let retryTimer: number | null = null;

  function emit(ev: DaemonEventPayload) {
    for (const cb of eventCbs) cb(ev);
  }
  function emitStatus(s: 'open' | 'error' | 'closed') {
    for (const cb of statusCbs) cb(s);
  }

  function connect() {
    if (closed) return;
    const token = encodeURIComponent(sessionStorage.getItem('ralph.token') ?? '');
    const url = `/api/live/${encodeURIComponent(fp)}/events?token=${token}`;
    es = new EventSource(url);
    es.addEventListener('open', () => {
      backoff = 500;
      emitStatus('open');
    });
    es.addEventListener('message', (e: MessageEvent<string>) => {
      try {
        const parsed = JSON.parse(e.data);
        emit(classifyPayload(parsed));
      } catch {
        /* ignore malformed lines */
      }
    });
    es.addEventListener('error', () => {
      emitStatus('error');
      es?.close();
      es = null;
      if (closed) return;
      const wait = Math.min(backoff, MAX_BACKOFF);
      backoff = Math.min(backoff * 2, MAX_BACKOFF);
      retryTimer = setTimeout(connect, wait) as unknown as number;
    });
  }

  connect();

  return {
    onEvent(cb) {
      eventCbs.add(cb);
      return () => eventCbs.delete(cb);
    },
    onStatus(cb) {
      statusCbs.add(cb);
      return () => statusCbs.delete(cb);
    },
    close() {
      closed = true;
      if (retryTimer != null) clearTimeout(retryTimer);
      es?.close();
      es = null;
      emitStatus('closed');
    },
  };
}

// probeReach returns true if /api/live/:fp/state responds 200 within a short
// timeout, false on 503 / network error / abort. Used by the sidebar badge
// reconciler.
export async function probeReach(fp: string, signal?: AbortSignal): Promise<boolean> {
  try {
    const res = await fetch(`/api/live/${encodeURIComponent(fp)}/state`, {
      headers: { 'X-Ralph-Token': sessionStorage.getItem('ralph.token') ?? '' },
      signal,
    });
    return res.ok;
  } catch {
    return false;
  }
}
