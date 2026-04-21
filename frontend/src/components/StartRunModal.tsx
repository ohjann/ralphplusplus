import { useEffect, useRef, useState } from 'preact/hooks';
import { pushToast } from '../lib/toast';
import { ApiError } from '../lib/api';

// POST /api/spawn-daemon body shape — mirrors internal/viewer/spawner.go.
interface SpawnRequest {
  repoPath: string;
  flags: Record<string, unknown>;
  confirm?: boolean;
}

interface SpawnOK {
  fp: string;
  pid: number;
}

interface SpawnWarn {
  warn: string;
  resolved: string;
}

interface SpawnErr {
  error?: string;
  details?: string;
}

type WorkersChoice = 'serial' | 'auto' | number;

interface FormState {
  repoPath: string;
  workers: WorkersChoice;
  judge: boolean;
  quality: boolean;
  model: string;
}

const DEFAULT_FORM: FormState = {
  repoPath: '',
  workers: 'serial',
  judge: true,
  quality: true,
  model: '',
};

function workersToFlag(w: WorkersChoice): Record<string, unknown> {
  if (w === 'serial') return { Workers: 1 };
  if (w === 'auto') return { Workers: 'auto' };
  return { Workers: w };
}

function buildFlags(f: FormState): Record<string, unknown> {
  const flags: Record<string, unknown> = { ...workersToFlag(f.workers) };
  if (!f.judge) flags.JudgeEnabled = false;
  if (!f.quality) flags.QualityReview = false;
  const m = f.model.trim();
  if (m) flags.ModelOverride = m;
  return flags;
}

async function postSpawn(req: SpawnRequest): Promise<
  | { kind: 'ok'; data: SpawnOK }
  | { kind: 'warn'; data: SpawnWarn }
  | { kind: 'err'; status: number; data: SpawnErr }
> {
  const res = await fetch('/api/spawn-daemon', {
    method: 'POST',
    headers: {
      'X-Ralph-Token': sessionStorage.getItem('ralph.token') ?? '',
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(req),
  });
  const body: unknown = await res.json().catch(() => ({}));
  if (res.ok) return { kind: 'ok', data: body as SpawnOK };
  if (res.status === 409) {
    const b = body as SpawnWarn & SpawnErr;
    if (typeof b.warn === 'string') return { kind: 'warn', data: b };
    return { kind: 'err', status: res.status, data: b };
  }
  throw new ApiError(res.status, (body as SpawnErr)?.error ?? `${res.status}`);
}

export function StartRunModal({ onClose }: { onClose: () => void }) {
  const [form, setForm] = useState<FormState>(DEFAULT_FORM);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>('');
  const [pendingWarn, setPendingWarn] = useState<SpawnWarn | null>(null);
  const pathRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    pathRef.current?.focus();
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  async function submit(e: Event, withConfirm = false) {
    e.preventDefault();
    if (busy) return;
    const repoPath = form.repoPath.trim();
    if (!repoPath) {
      setError('Repo path is required.');
      return;
    }
    setBusy(true);
    setError('');
    try {
      const out = await postSpawn({
        repoPath,
        flags: buildFlags(form),
        confirm: withConfirm,
      });
      if (out.kind === 'ok') {
        pushToast('success', `Daemon pid ${out.data.pid} (${out.data.fp.slice(0, 8)}…)`);
        onClose();
        return;
      }
      if (out.kind === 'warn') {
        setPendingWarn(out.data);
        return;
      }
      const msg =
        out.data.error === 'daemon_already_running'
          ? 'A daemon is already running for this repo.'
          : out.data.error ?? `Spawn failed (${out.status}).`;
      setError(msg);
    } catch (err) {
      if (err instanceof ApiError) {
        setError(`Spawn failed: ${err.message}`);
      } else {
        setError(err instanceof Error ? err.message : String(err));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Start a run"
      onClick={onClose}
      style={{
        position: 'fixed',
        inset: 0,
        background: 'rgba(0, 0, 0, 0.35)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 1000,
      }}
    >
      <form
        onClick={(e) => e.stopPropagation()}
        onSubmit={(e) => submit(e, false)}
        style={{
          background: 'var(--bg)',
          border: '1px solid var(--border)',
          borderRadius: 10,
          padding: '18px 20px 16px',
          width: 'min(520px, 92vw)',
          maxHeight: '86vh',
          overflowY: 'auto',
          display: 'flex',
          flexDirection: 'column',
          gap: 12,
          boxShadow: '0 20px 60px rgba(0,0,0,0.28)',
          color: 'var(--fg)',
        }}
      >
        <header
          style={{
            display: 'flex',
            alignItems: 'baseline',
            justifyContent: 'space-between',
            gap: 10,
          }}
        >
          <div>
            <div style={{ fontWeight: 600, fontSize: 15 }}>Start a run</div>
            <div
              style={{
                fontSize: 11,
                color: 'var(--fg-faint)',
                marginTop: 2,
              }}
            >
              Spawns <code style={{ fontFamily: 'var(--font-mono)' }}>ralph --daemon</code> against the chosen repo.
            </div>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close"
            style={{
              fontSize: 18,
              lineHeight: 1,
              color: 'var(--fg-faint)',
              padding: 4,
            }}
          >
            ×
          </button>
        </header>

        <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          <span style={{ fontSize: 11, color: 'var(--fg-muted)' }}>Repo path</span>
          <input
            ref={pathRef}
            type="text"
            value={form.repoPath}
            placeholder="/Users/you/code/my-repo"
            onInput={(e) =>
              setForm({
                ...form,
                repoPath: (e.currentTarget as HTMLInputElement).value,
              })
            }
            style={{
              padding: '7px 9px',
              background: 'var(--bg-elev)',
              border: '1px solid var(--border)',
              borderRadius: 6,
              color: 'var(--fg)',
              fontFamily: 'var(--font-mono)',
              fontSize: 12,
            }}
          />
        </label>

        <fieldset
          style={{
            border: '1px solid var(--border)',
            borderRadius: 6,
            padding: '8px 10px 10px',
            display: 'flex',
            flexDirection: 'column',
            gap: 8,
          }}
        >
          <legend style={{ fontSize: 11, color: 'var(--fg-muted)', padding: '0 4px' }}>
            Flags
          </legend>

          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span style={{ fontSize: 12, minWidth: 80 }}>Workers</span>
            <select
              value={typeof form.workers === 'number' ? String(form.workers) : form.workers}
              onChange={(e) => {
                const v = (e.currentTarget as HTMLSelectElement).value;
                const next: WorkersChoice =
                  v === 'serial' || v === 'auto' ? v : Number(v);
                setForm({ ...form, workers: next });
              }}
              style={{
                padding: '4px 6px',
                fontSize: 12,
                background: 'var(--bg-elev)',
                border: '1px solid var(--border)',
                borderRadius: 5,
                color: 'var(--fg)',
              }}
            >
              <option value="serial">serial (1)</option>
              <option value="2">2</option>
              <option value="3">3</option>
              <option value="4">4</option>
              <option value="auto">auto (scale to DAG)</option>
            </select>
          </div>

          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12 }}>
            <input
              type="checkbox"
              checked={form.judge}
              onChange={(e) =>
                setForm({ ...form, judge: (e.currentTarget as HTMLInputElement).checked })
              }
            />
            Judge enabled
          </label>

          <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12 }}>
            <input
              type="checkbox"
              checked={form.quality}
              onChange={(e) =>
                setForm({ ...form, quality: (e.currentTarget as HTMLInputElement).checked })
              }
            />
            Quality review
          </label>

          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span style={{ fontSize: 12, minWidth: 80 }}>Model</span>
            <input
              type="text"
              value={form.model}
              placeholder="(default)"
              onInput={(e) =>
                setForm({
                  ...form,
                  model: (e.currentTarget as HTMLInputElement).value,
                })
              }
              style={{
                flex: 1,
                padding: '4px 8px',
                fontSize: 12,
                background: 'var(--bg-elev)',
                border: '1px solid var(--border)',
                borderRadius: 5,
                color: 'var(--fg)',
                fontFamily: 'var(--font-mono)',
              }}
            />
          </div>
        </fieldset>

        {error && (
          <div
            style={{
              background: 'var(--err-soft, rgba(220, 53, 69, 0.12))',
              color: 'var(--err)',
              border: '1px solid var(--err)',
              borderRadius: 6,
              padding: '6px 9px',
              fontSize: 12,
            }}
          >
            {error}
          </div>
        )}

        {pendingWarn && (
          <div
            style={{
              background: 'var(--warn-soft, rgba(224, 158, 40, 0.12))',
              color: 'var(--warn, #a15c07)',
              border: '1px solid var(--warn, #e09e28)',
              borderRadius: 6,
              padding: '8px 10px',
              fontSize: 12,
              display: 'flex',
              flexDirection: 'column',
              gap: 6,
            }}
          >
            <div>
              <strong>Path outside $HOME.</strong> Resolved to{' '}
              <code style={{ fontFamily: 'var(--font-mono)' }}>{pendingWarn.resolved}</code>.
              Confirm to spawn anyway.
            </div>
            <div style={{ display: 'flex', gap: 8 }}>
              <button
                type="button"
                onClick={(e) => {
                  setPendingWarn(null);
                  void submit(e as unknown as Event, true);
                }}
                disabled={busy}
                style={{
                  padding: '4px 10px',
                  fontSize: 12,
                  border: '1px solid var(--warn, #e09e28)',
                  borderRadius: 5,
                  background: 'var(--warn, #e09e28)',
                  color: '#1a1205',
                }}
              >
                Confirm & start
              </button>
              <button
                type="button"
                onClick={() => setPendingWarn(null)}
                style={{
                  padding: '4px 10px',
                  fontSize: 12,
                  border: '1px solid var(--border)',
                  borderRadius: 5,
                  background: 'transparent',
                  color: 'var(--fg-muted)',
                }}
              >
                Cancel
              </button>
            </div>
          </div>
        )}

        <footer
          style={{
            display: 'flex',
            justifyContent: 'flex-end',
            gap: 8,
            marginTop: 4,
          }}
        >
          <button
            type="button"
            onClick={onClose}
            style={{
              padding: '6px 12px',
              fontSize: 12,
              border: '1px solid var(--border)',
              borderRadius: 5,
              background: 'transparent',
              color: 'var(--fg-muted)',
            }}
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={busy || !form.repoPath.trim()}
            style={{
              padding: '6px 14px',
              fontSize: 12,
              fontWeight: 600,
              border: '1px solid var(--accent-border)',
              borderRadius: 5,
              background: 'var(--accent-soft)',
              color: 'var(--accent-ink)',
              opacity: busy || !form.repoPath.trim() ? 0.5 : 1,
            }}
          >
            {busy ? 'starting…' : 'Start daemon'}
          </button>
        </footer>
      </form>
    </div>
  );
}
