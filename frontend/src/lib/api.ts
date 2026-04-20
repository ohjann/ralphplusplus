// Central fetch wrapper for /api/** routes. Injects X-Ralph-Token from
// sessionStorage (set by main.tsx on first load) and parses JSON responses.
// Throws ApiError on non-2xx so callers can branch on status.

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = 'ApiError';
  }
}

function token(): string {
  return sessionStorage.getItem('ralph.token') ?? '';
}

export async function apiGet<T>(path: string): Promise<T> {
  const res = await fetch(path, { headers: { 'X-Ralph-Token': token() } });
  if (!res.ok) throw new ApiError(res.status, `${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

export async function apiPost<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method: 'POST',
    headers: {
      'X-Ralph-Token': token(),
      'Content-Type': 'application/json',
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) throw new ApiError(res.status, `${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

// Wire types — mirror internal/viewer/dto.go

export interface Bootstrap {
  version: string;
  featureFlags: string[];
  token: string;
}

export interface RepoSummary {
  fp: string;
  path: string;
  name: string;
  lastSeen: string;
  runCount: number;
}

export interface RunListItem {
  runId: string;
  kind: string;
  status: string;
  startTime: string;
  endTime?: string;
  gitBranch?: string;
  gitHeadSha?: string;
  iterations: number;
  inputTokens: number;
  outputTokens: number;
  totalCost?: number;
  durationMinutes?: number;
  firstPassRate?: number;
  modelsUsed?: string[];
}
