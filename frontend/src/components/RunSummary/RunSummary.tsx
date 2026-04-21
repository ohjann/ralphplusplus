import { useEffect } from 'preact/hooks';
import { signal } from '@preact/signals';
import {
  apiGet,
  type RunDetail,
  type PRDResponse,
  type StoryRecord,
} from '../../lib/api';
import { StatusPanel } from '../StatusPanel/StatusPanel';

const loading = signal<boolean>(false);
const error = signal<string>('');
const detail = signal<RunDetail | null>(null);
const prd = signal<PRDResponse | null>(null);
const currentKey = signal<string>('');

async function load(fp: string, runId: string) {
  const key = `${fp}/${runId}`;
  if (currentKey.value === key && detail.value) return;
  currentKey.value = key;
  loading.value = true;
  error.value = '';
  detail.value = null;
  prd.value = null;
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
  const isLive = m.status === 'running';

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
              <StatusPill status={m.status} />
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
          />
        </Section>
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
      <span style={{ flex: 1 }}>
        {unchanged
          ? 'PRD unchanged since this run.'
          : changed
            ? 'PRD has changed since this run.'
            : 'No PRD snapshot recorded for this run.'}
      </span>
      {(unchanged || changed) && (
        <a href={`/repos/${fp}/meta`} style={{ fontSize: 12 }}>
          view current →
        </a>
      )}
    </div>
  );
}

function StoriesList({
  fp,
  runId,
  stories,
}: {
  fp: string;
  runId: string;
  stories: StoryRecord[];
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
            <StoryStatus status={st.final_status ?? ''} />
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
