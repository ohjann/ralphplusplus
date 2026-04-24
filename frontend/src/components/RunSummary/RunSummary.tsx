import { useEffect } from 'preact/hooks';
import { signal } from '@preact/signals';
import {
  apiGet,
  type RunDetail,
  type PRDResponse,
  type StoryRecord,
} from '../../lib/api';
import {
  openLive,
  probeReach,
  type DaemonStateEvent,
  type WorkerStatus,
} from '../../lib/live';
import { StatusPanel } from '../StatusPanel/StatusPanel';
import { ChatStream } from '../ChatView/ChatView';

const loading = signal<boolean>(false);
const error = signal<string>('');
const detail = signal<RunDetail | null>(null);
const prd = signal<PRDResponse | null>(null);
const currentKey = signal<string>('');
const reachByFP = signal<Record<string, boolean>>({});
const liveStateByFP = signal<Record<string, DaemonStateEvent | null>>({});

async function load(fp: string, runId: string, force = false) {
  const key = `${fp}/${runId}`;
  if (!force && currentKey.value === key && detail.value) return;
  currentKey.value = key;
  if (!force) {
    // Initial load — show the loading state. Background refreshes keep
    // the previous detail rendered so the UI does not flicker.
    loading.value = true;
    detail.value = null;
    prd.value = null;
  }
  error.value = '';
  try {
    const [d, p] = await Promise.all([
      apiGet<RunDetail>(`/api/repos/${fp}/runs/${runId}`),
      apiGet<PRDResponse>(
        `/api/repos/${fp}/prd?run_id=${encodeURIComponent(runId)}`,
      ).catch(() => null),
    ]);
    if (currentKey.value !== key) return;
    detail.value = d;
    prd.value = p;
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

function fmtTime(iso?: string): string {
  if (!iso) return '—';
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}
function fmtNum(n: number) {
  return n.toLocaleString();
}
function fmtCost(n: number) {
  return `$${n.toFixed(2)}`;
}
function fmtDuration(m: number) {
  if (m < 1) return `${Math.round(m * 60)}s`;
  if (m < 60) return `${m.toFixed(1)}m`;
  return `${Math.floor(m / 60)}h ${Math.round(m % 60)}m`;
}
function short(s: string, n = 8) {
  return s.slice(0, n);
}

export function RunSummary({ fp, runId }: { fp: string; runId: string }) {
  useEffect(() => {
    void load(fp, runId);
    // Probe reachability so the status pill can distinguish truly-running
    // from an orphaned manifest whose daemon has died without updating it.
    let cancelled = false;
    void (async () => {
      const ok = await probeReach(fp);
      if (!cancelled) reachByFP.value = { ...reachByFP.value, [fp]: ok };
    })();

    // Subscribe to the daemon's SSE stream. Every state event refreshes the
    // manifest in the background so new iterations/stories appear without a
    // reload. The stream auto-closes when the user leaves the page.
    let refreshTimer: number | null = null;
    const live = openLive(fp);
    const offEvent = live.onEvent((e) => {
      if (e.kind !== 'state') return;
      reachByFP.value = { ...reachByFP.value, [fp]: true };
      liveStateByFP.value = { ...liveStateByFP.value, [fp]: e.data };
      // Coalesce bursts of events into one fetch at most every 800ms —
      // the daemon can emit several state events per second during heavy
      // work and re-fetching the manifest that often is wasteful.
      if (refreshTimer != null) return;
      refreshTimer = setTimeout(() => {
        refreshTimer = null;
        void load(fp, runId, true);
      }, 800) as unknown as number;
    });

    return () => {
      cancelled = true;
      offEvent();
      live.close();
      if (refreshTimer != null) clearTimeout(refreshTimer);
    };
  }, [fp, runId]);

  if (loading.value && !detail.value) {
    return (
      <div style={{ padding: 32, color: 'var(--fg-faint)' }}>Loading run…</div>
    );
  }
  if (error.value) {
    return (
      <div style={{ padding: 32, color: 'var(--err)' }}>
        Failed to load: {error.value}
      </div>
    );
  }
  if (!detail.value) return null;

  const m = detail.value.manifest;
  const s = detail.value.summary;
  const p = prd.value;
  const daemonReachable = reachByFP.value[fp] === true;
  // Effective status honours reachability: a manifest claiming 'running' with
  // a dead daemon is actually orphaned/interrupted. Only show StatusPanel when
  // the daemon is actually reachable.
  const effectiveStatus =
    m.status === 'running' && !daemonReachable ? 'interrupted' : m.status;
  const isLive = effectiveStatus === 'running';

  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: isLive ? 'minmax(0, 1fr) 340px' : 'minmax(0, 1fr)',
        height: '100%',
        minHeight: 0,
      }}
    >
      <div
        style={{
          overflow: 'auto',
          padding: '22px 28px 80px',
        }}
      >
        {/* Breadcrumb */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 6,
            fontSize: 12,
            color: 'var(--fg-faint)',
            fontFamily: 'var(--font-mono)',
            marginBottom: 12,
            flexWrap: 'wrap',
          }}
        >
          <span>repos</span>
          <span style={{ color: 'var(--fg-ghost)' }}>/</span>
          <span>{m.repo_name || m.repo_path}</span>
          <span style={{ color: 'var(--fg-ghost)' }}>/</span>
          <span style={{ color: 'var(--fg)' }}>
            {m.display_name || short(m.run_id, 8)}
          </span>
        </div>

        {/* Header */}
        <div
          style={{
            display: 'flex',
            alignItems: 'flex-start',
            justifyContent: 'space-between',
            gap: 16,
            flexWrap: 'wrap',
          }}
        >
          <div style={{ minWidth: 0 }}>
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 10,
                flexWrap: 'wrap',
              }}
            >
              <h1
                style={{
                  fontSize: 22,
                  fontWeight: 600,
                  letterSpacing: '-0.015em',
                  margin: 0,
                  color: 'var(--fg)',
                }}
              >
                {m.display_name || m.repo_name || m.repo_path}
              </h1>
              {m.display_name && (
                <span
                  style={{
                    fontSize: 12,
                    color: 'var(--fg-faint)',
                  }}
                  title="Repository name"
                >
                  · {m.repo_name || m.repo_path}
                </span>
              )}
              <span class="pill indigo">{m.kind}</span>
              <StatusPill status={effectiveStatus} />
            </div>
            <div
              class="mono"
              style={{
                fontSize: 11.5,
                color: 'var(--fg-faint)',
                marginTop: 6,
              }}
            >
              {m.repo_path}
            </div>
          </div>
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
            <CopyButton text={m.run_id} label="copy run-id" />
          </div>
        </div>

        {/* Meta row */}
        <div
          style={{
            display: 'flex',
            flexWrap: 'wrap',
            gap: 16,
            padding: '10px 12px',
            marginTop: 14,
            background: 'var(--bg-elev)',
            border: '1px solid var(--border)',
            borderRadius: 6,
          }}
        >
          <MetaItem label="branch" value={m.git_branch || '—'} mono />
          <MetaItem
            label="HEAD"
            value={m.git_head_sha ? short(m.git_head_sha, 10) : '—'}
            mono
            title={m.git_head_sha}
          />
          <MetaItem label="started" value={fmtTime(m.start_time)} />
          <MetaItem label="ended" value={fmtTime(m.end_time)} />
          <MetaItem label="run-id" value={short(m.run_id, 10)} mono title={m.run_id} />
          <MetaItem label="ralph" value={m.ralph_version} mono />
        </div>

        {/* Models & flags */}
        {(m.flags?.length || m.claude_models) && (
          <Section title="Models & flags">
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
              {m.claude_models &&
                Object.entries(m.claude_models).map(([role, model]) => (
                  <span
                    key={role}
                    class="chip indigo mono"
                    title={`${role} → ${model}`}
                  >
                    <span
                      style={{ color: 'var(--accent-ink)', opacity: 0.7 }}
                    >
                      {role}
                    </span>
                    <span style={{ color: 'var(--fg-ghost)' }}>→</span>
                    <span>{model}</span>
                  </span>
                ))}
              {m.claude_models && m.flags && m.flags.length > 0 && (
                <div
                  style={{
                    width: 1,
                    height: 18,
                    background: 'var(--border)',
                    margin: '0 4px',
                    alignSelf: 'center',
                  }}
                />
              )}
              {m.flags?.map((f) => (
                <span key={f} class="chip mono">
                  {f}
                </span>
              ))}
            </div>
          </Section>
        )}

        {/* Metrics */}
        <Section title="Metrics">
          <div
            style={{
              display: 'grid',
              gridTemplateColumns:
                'repeat(auto-fill, minmax(140px, 1fr))',
              gap: 8,
            }}
          >
            <Metric
              label="input"
              value={fmtNum(m.totals.input_tokens)}
              sub="tokens"
            />
            <Metric
              label="output"
              value={fmtNum(m.totals.output_tokens)}
              sub="tokens"
            />
            <Metric
              label="cache read"
              value={fmtNum(m.totals.cache_read)}
              sub="tokens"
            />
            <Metric
              label="cache write"
              value={fmtNum(m.totals.cache_write)}
              sub="tokens"
            />
            <Metric
              label="iterations"
              value={fmtNum(m.totals.iterations)}
              sub="turns"
            />
            {s && (
              <>
                <Metric
                  label="cost"
                  value={fmtCost(s.total_cost)}
                  sub="usd"
                  accent
                />
                <Metric
                  label="duration"
                  value={fmtDuration(s.duration_minutes)}
                  sub="wall clock"
                />
                <Metric
                  label="first-pass"
                  value={`${Math.round(s.first_pass_rate * 100)}%`}
                  sub="rate"
                />
              </>
            )}
          </div>
        </Section>

        {/* PRD */}
        {p && <Section title="PRD"><PRDBlock fp={fp} prd={p} /></Section>}

        {/* Stories */}
        <Section
          title="Stories"
          hint={`${(m.stories ?? []).length} total · click iteration to view transcript`}
        >
          <StoriesList
            fp={fp}
            runId={runId}
            stories={m.stories ?? []}
            runStatus={effectiveStatus}
          />
        </Section>

        {/* Live activity — stacked transcript panels for each active worker.
            Mirrors the TUI's live pane; updates stream via ?follow=true. */}
        {isLive && <LiveActivity fp={fp} runId={runId} />}
      </div>

      {isLive && (
        <div
          style={{
            height: '100%',
            minHeight: 0,
            borderLeft: '1px solid var(--border)',
            overflow: 'hidden',
          }}
        >
          <StatusPanel fp={fp} />
        </div>
      )}
    </div>
  );
}

function StatusPill({ status }: { status: string }) {
  if (status === 'running') {
    return (
      <span class="pill ok">
        <span class="dot ok live" />
        running
      </span>
    );
  }
  if (status === 'complete') return <span class="pill">complete</span>;
  if (status === 'interrupted') return <span class="pill warn">interrupted</span>;
  return <span class="pill err">{status}</span>;
}

function MetaItem({
  label,
  value,
  mono,
  title,
}: {
  label: string;
  value: string;
  mono?: boolean;
  title?: string;
}) {
  return (
    <div
      style={{ display: 'flex', alignItems: 'center', gap: 6, minWidth: 0 }}
      title={title}
    >
      <span
        style={{
          fontSize: 11,
          color: 'var(--fg-faint)',
          textTransform: 'uppercase',
          letterSpacing: '0.06em',
        }}
      >
        {label}
      </span>
      <span
        class={mono ? 'mono' : ''}
        style={{
          fontSize: mono ? 12 : 13,
          color: 'var(--fg)',
        }}
      >
        {value}
      </span>
    </div>
  );
}

function Section({
  title,
  hint,
  children,
}: {
  title: string;
  hint?: string;
  children: preact.ComponentChildren;
}) {
  return (
    <section style={{ marginTop: 22 }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'baseline',
          justifyContent: 'space-between',
          gap: 12,
          marginBottom: 10,
        }}
      >
        <h2
          style={{
            fontSize: 12.5,
            fontWeight: 600,
            margin: 0,
            color: 'var(--fg-muted)',
            textTransform: 'uppercase',
            letterSpacing: '0.07em',
          }}
        >
          {title}
        </h2>
        {hint && (
          <span style={{ fontSize: 11.5, color: 'var(--fg-faint)' }}>
            {hint}
          </span>
        )}
      </div>
      {children}
    </section>
  );
}

function Metric({
  label,
  value,
  sub,
  accent,
}: {
  label: string;
  value: string;
  sub: string;
  accent?: boolean;
}) {
  return (
    <div
      style={{
        border: '1px solid var(--border)',
        borderRadius: 8,
        padding: '10px 12px',
        background: 'var(--bg-elev)',
        minWidth: 0,
      }}
    >
      <div
        style={{
          fontSize: 10.5,
          color: 'var(--fg-faint)',
          textTransform: 'uppercase',
          letterSpacing: '0.07em',
          fontWeight: 600,
        }}
      >
        {label}
      </div>
      <div
        class="mono"
        style={{
          fontSize: 18,
          fontWeight: 500,
          marginTop: 2,
          color: accent ? 'var(--accent-ink)' : 'var(--fg)',
          letterSpacing: '-0.01em',
        }}
      >
        {value}
      </div>
      <div
        style={{
          fontSize: 10.5,
          color: 'var(--fg-ghost)',
          marginTop: 1,
          textTransform: 'uppercase',
          letterSpacing: '0.05em',
        }}
      >
        {sub}
      </div>
    </div>
  );
}

function PRDBlock({ fp, prd }: { fp: string; prd: PRDResponse }) {
  const unchanged = prd.matchesRunSnapshot === true;
  const changed = prd.matchesRunSnapshot === false;
  const tone = unchanged ? 'ok' : changed ? 'warn' : 'neutral';
  const bg =
    tone === 'ok'
      ? 'var(--ok-soft)'
      : tone === 'warn'
        ? 'var(--warn-soft)'
        : 'var(--bg-elev)';
  const color =
    tone === 'ok'
      ? 'var(--ok)'
      : tone === 'warn'
        ? 'var(--warn)'
        : 'var(--fg-muted)';
  const border =
    tone === 'ok'
      ? 'var(--ok)'
      : tone === 'warn'
        ? 'var(--warn)'
        : 'var(--border)';
  const label = unchanged
    ? 'Matches the PRD at run start — no edits since.'
    : changed
      ? 'PRD has been edited since this run started.'
      : 'No PRD snapshot was captured for this run.';
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        padding: '9px 12px',
        background: bg,
        border: `1px solid ${border}`,
        borderRadius: 6,
        fontSize: 13,
        color,
      }}
    >
      <span class="mono" style={{ fontSize: 11, opacity: 0.8 }}>
        sha256:{short(prd.hash, 12)}
      </span>
      <span style={{ flex: 1 }}>{label}</span>
      <a href={`/repos/${fp}/prd`} style={{ fontSize: 12 }}>
        open PRD editor →
      </a>
    </div>
  );
}

// deriveStoryStatus picks the best label from the data in the manifest.
// Prefers the explicit `final_status` when present (set by SetStoryFinal).
// Otherwise falls back to a heuristic based on iteration shape + run status
// so archived runs display something more useful than a blank dash.
function deriveStoryStatus(st: StoryRecord, runStatus: string): string {
  if (st.final_status) return st.final_status;
  const iters = st.iterations ?? [];
  if (runStatus === 'running') {
    return iters.length === 0 ? 'queued' : 'in-progress';
  }
  if (iters.length === 0) return '';
  if (runStatus === 'interrupted') return 'partial';
  return 'complete';
}

function StoriesList({
  fp,
  runId,
  stories,
  runStatus,
}: {
  fp: string;
  runId: string;
  stories: StoryRecord[];
  runStatus: string;
}) {
  if (stories.length === 0) {
    return (
      <div
        style={{
          fontSize: 13,
          color: 'var(--fg-faint)',
          fontStyle: 'italic',
        }}
      >
        No stories recorded for this run.
      </div>
    );
  }
  return (
    <div
      style={{
        border: '1px solid var(--border)',
        borderRadius: 8,
        overflow: 'hidden',
        background: 'var(--bg-elev)',
      }}
    >
      {stories.map((st, i) => (
        <div
          key={st.story_id}
          style={{
            display: 'grid',
            gridTemplateColumns: '90px 120px 1fr auto',
            alignItems: 'center',
            gap: 12,
            padding: '10px 14px',
            borderTop: i === 0 ? 'none' : '1px solid var(--border-soft)',
          }}
        >
          <span
            class="mono"
            style={{ fontSize: 11.5, color: 'var(--fg-muted)' }}
          >
            {st.story_id}
          </span>
          <span>
            <StoryStatus status={deriveStoryStatus(st, runStatus)} />
          </span>
          <span style={{ fontSize: 13.5, color: 'var(--fg)' }}>
            {st.title || ''}
          </span>
          <div
            style={{
              display: 'flex',
              gap: 4,
              flexWrap: 'wrap',
              justifyContent: 'flex-end',
            }}
          >
            {(st.iterations?.length ?? 0) === 0 && (
              <span
                style={{ color: 'var(--fg-ghost)', fontSize: 11 }}
              >
                —
              </span>
            )}
            {st.iterations?.map((iter) => (
              <a
                key={iter.index}
                href={`/repos/${fp}/runs/${runId}/iter/${encodeURIComponent(st.story_id)}/${iter.index}`}
                class="mono"
                title={`${st.story_id} · iteration ${iter.index} · ${iter.role}`}
                style={{
                  border: '1px solid var(--accent-border)',
                  color: 'var(--accent-ink)',
                  background: 'var(--accent-soft)',
                  borderRadius: 4,
                  fontSize: 11,
                  padding: '2px 7px',
                  minWidth: 24,
                  textAlign: 'center',
                  textDecoration: 'none',
                }}
              >
                {iter.index}
              </a>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

function StoryStatus({ status }: { status: string }) {
  if (!status) return <span class="chip">—</span>;
  if (status === 'complete' || status === 'passed')
    return <span class="chip ok">complete</span>;
  if (status === 'failed') return <span class="chip err">failed</span>;
  if (status === 'in-progress' || status === 'running')
    return (
      <span class="chip indigo">
        <span class="dot ok live" />
        in progress
      </span>
    );
  if (status === 'queued') return <span class="chip">queued</span>;
  if (status === 'partial') return <span class="chip warn">partial</span>;
  return <span class="chip">{status}</span>;
}

function CopyButton({ text, label }: { text: string; label: string }) {
  const copied = signal<boolean>(false);
  const onClick = async () => {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      /* ignore */
    }
    copied.value = true;
    setTimeout(() => (copied.value = false), 1600);
  };
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        border: '1px solid var(--border)',
        background: 'var(--bg-elev)',
        color: 'var(--fg-muted)',
        borderRadius: 6,
        padding: '5px 10px',
        fontSize: 12,
      }}
    >
      {copied.value ? '✓ copied' : label}
    </button>
  );
}

function LiveActivity({ fp, runId }: { fp: string; runId: string }) {
  const state = liveStateByFP.value[fp];
  const workers: WorkerStatus[] = state
    ? Object.values(state.workers ?? {}).filter(
        (w) => w.story_id && w.role,
      )
    : [];
  return (
    <Section
      title="Live activity"
      hint={
        workers.length === 0
          ? 'waiting for worker…'
          : `${workers.length} active · streaming from daemon`
      }
    >
      {workers.length === 0 ? (
        <div
          style={{
            fontSize: 13,
            color: 'var(--fg-faint)',
            fontStyle: 'italic',
            padding: '12px 14px',
            border: '1px solid var(--border-soft)',
            borderRadius: 8,
            background: 'var(--bg-elev)',
          }}
        >
          No worker is currently claiming a story.
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
          {workers.map((w) => (
            <LiveWorkerPanel
              key={`${w.id}-${w.story_id}-${w.iteration}`}
              fp={fp}
              runId={runId}
              worker={w}
            />
          ))}
        </div>
      )}
    </Section>
  );
}

function LiveWorkerPanel({
  fp,
  runId,
  worker,
}: {
  fp: string;
  runId: string;
  worker: WorkerStatus;
}) {
  return (
    <div
      style={{
        border: '1px solid var(--accent-border)',
        borderRadius: 8,
        overflow: 'hidden',
        background: 'var(--bg-elev)',
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 10,
          padding: '8px 12px',
          background: 'var(--accent-soft)',
          borderBottom: '1px solid var(--accent-border)',
          fontSize: 12,
          color: 'var(--accent-ink)',
        }}
      >
        <span class="dot ok live" />
        <span class="mono" style={{ fontWeight: 600 }}>
          worker #{worker.id}
        </span>
        <span style={{ color: 'var(--fg-ghost)' }}>·</span>
        <span class="mono">{worker.story_id}</span>
        {worker.story_title && (
          <span
            style={{
              color: 'var(--fg-muted)',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
              minWidth: 0,
            }}
          >
            {worker.story_title}
          </span>
        )}
        <span style={{ color: 'var(--fg-ghost)', marginLeft: 'auto' }}>
          {worker.role} · iter {worker.iteration}
        </span>
      </div>
      <div style={{ padding: '12px 14px' }}>
        <ChatStream
          fp={fp}
          runId={runId}
          story={worker.story_id}
          iter={String(worker.iteration)}
          maxWidth={undefined}
        />
      </div>
    </div>
  );
}
