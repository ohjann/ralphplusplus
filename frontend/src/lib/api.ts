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

export interface Totals {
  input_tokens: number;
  output_tokens: number;
  cache_read: number;
  cache_write: number;
  iterations: number;
}

export interface IterationRecord {
  index: number;
  role: string;
  model?: string;
  session_id?: string;
  start_time: string;
  end_time?: string;
  prompt_file: string;
  transcript_file: string;
  meta_file: string;
  error?: string;
}

export interface StoryRecord {
  story_id: string;
  title?: string;
  iterations?: IterationRecord[];
  final_status?: string;
}

export interface Manifest {
  schema_version: number;
  run_id: string;
  display_name?: string;
  kind: string;
  repo_fp: string;
  repo_path: string;
  repo_name: string;
  git_branch?: string;
  git_head_sha?: string;
  prd_path?: string;
  prd_snapshot?: string;
  ralph_version: string;
  claude_models?: Record<string, string>;
  flags?: string[];
  pid: number;
  hostname: string;
  process_start: string;
  start_time: string;
  end_time?: string;
  status: string;
  stories?: StoryRecord[];
  totals: Totals;
}

export interface RunSummary {
  prd: string;
  date: string;
  run_id?: string;
  kind?: string;
  stories_total: number;
  stories_completed: number;
  stories_failed: number;
  total_cost: number;
  duration_minutes: number;
  total_iterations: number;
  first_pass_rate: number;
  models_used?: string[];
}

export interface RunDetail {
  manifest: Manifest;
  summary?: RunSummary;
}

export interface PRDResponse {
  hash: string;
  content: unknown;
  matchesRunSnapshot?: boolean;
}

// Matches internal/viewer/dto.go SettingsResponse. Exactly one of state
// (daemon source) or config (file source) is populated per response.
export interface SettingsResponse {
  source: 'daemon' | 'file';
  state?: unknown; // DaemonStateEvent-shaped when source==='daemon'
  config?: Record<string, unknown>;
}

// Matches internal/viewer/dto.go RepoMetaResponse.
export interface RepoMetaAggCosts {
  runs: number;
  totalCost: number;
  durationMinutes: number;
  totalIterations: number;
  storiesTotal: number;
  storiesCompleted: number;
  storiesFailed: number;
}

export interface RepoMetaRecord {
  path: string;
  name: string;
  git_first_sha?: string;
  first_seen: string;
  last_seen: string;
  last_run_id?: string;
  run_count: number;
}

export interface RepoMetaResponse {
  meta: RepoMetaRecord;
  aggCosts: RepoMetaAggCosts;
  runCountsByKind: Record<string, number>;
}

export interface RunListItem {
  runId: string;
  displayName?: string;
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
