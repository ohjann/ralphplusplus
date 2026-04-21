import { useEffect } from 'preact/hooks';
import { signal } from '@preact/signals';
import { useRoute } from 'preact-iso';
import {
  apiGet,
  apiPost,
  ApiError,
  type PRDResponse,
  type PRDDocument,
  type PRDUserStory,
  type PRDValidationError,
  type PRDHashMismatchError,
} from '../lib/api';
import { pushToast } from '../lib/toast';
import { StoriesTable } from '../components/PRDEditor/StoriesTable';

// Module-scope signals — mirrors SettingsRoute's pattern so the route
// can survive a re-mount without refetching. Reset via `reset(fp)` when
// the user navigates to a different repo.
const loading = signal<boolean>(false);
const saving = signal<boolean>(false);
const error = signal<string>('');
const originalPRD = signal<PRDDocument | null>(null);
const editedPRD = signal<PRDDocument | null>(null);
const hash = signal<string>('');
const staleHash = signal<boolean>(false);
const fieldErrors = signal<Record<string, string>>({});
const currentFP = signal<string>('');

function normalize(input: unknown): PRDDocument {
  const d = (input as Partial<PRDDocument>) ?? {};
  const stories = (d.userStories ?? []).map<PRDUserStory>((s) => ({
    id: s.id ?? '',
    title: s.title ?? '',
    description: s.description ?? '',
    acceptanceCriteria: s.acceptanceCriteria ?? [],
    priority: typeof s.priority === 'number' ? s.priority : 0,
    passes: s.passes === true,
    notes: s.notes ?? '',
    dependsOn: s.dependsOn ?? [],
    approach: s.approach ?? '',
  }));
  return {
    project: d.project ?? '',
    branchName: d.branchName,
    description: d.description,
    buildCommand: d.buildCommand,
    repos: d.repos,
    constraints: d.constraints,
    userStories: stories,
  };
}

function deepClone<T>(v: T): T {
  return JSON.parse(JSON.stringify(v)) as T;
}

function isDirty(): boolean {
  if (!originalPRD.value || !editedPRD.value) return false;
  return (
    JSON.stringify(originalPRD.value) !== JSON.stringify(editedPRD.value)
  );
}

// validateClient mirrors the server-side prd.Validate rules so the Save
// button can disable in real-time and users don't round-trip to the server
// just to learn their priority was negative.
function validateClient(doc: PRDDocument): Record<string, string> {
  const errs: Record<string, string> = {};
  if (!doc.project || !doc.project.trim()) {
    errs['project'] = 'project is required';
  }
  const seen = new Map<string, number>();
  doc.userStories.forEach((s, i) => {
    const base = `userStories[${i}]`;
    if (!s.id || !s.id.trim()) {
      errs[`${base}.id`] = 'id is required';
    } else if (seen.has(s.id)) {
      errs[`${base}.id`] = `duplicate id "${s.id}"`;
    } else {
      seen.set(s.id, i);
    }
    if (!s.title || !s.title.trim()) errs[`${base}.title`] = 'title is required';
    if (!s.description || !s.description.trim())
      errs[`${base}.description`] = 'description is required';
    if (s.priority < 0)
      errs[`${base}.priority`] = 'priority must be zero or greater';
    (s.acceptanceCriteria || []).forEach((c, k) => {
      if (!c || !c.trim()) {
        errs[`${base}.acceptanceCriteria[${k}]`] =
          'acceptance criterion cannot be empty';
      }
    });
  });
  // dependsOn pass after id set is built.
  doc.userStories.forEach((s, i) => {
    const base = `userStories[${i}]`;
    (s.dependsOn || []).forEach((dep, k) => {
      const p = `${base}.dependsOn[${k}]`;
      if (!dep || !dep.trim()) {
        errs[p] = 'dependency id cannot be empty';
      } else if (dep === s.id) {
        errs[p] = 'story cannot depend on itself';
      } else if (!seen.has(dep)) {
        errs[p] = `unknown story id "${dep}"`;
      }
    });
  });
  return errs;
}

async function load(fp: string, force = false) {
  if (!force && currentFP.value === fp && editedPRD.value) return;
  currentFP.value = fp;
  loading.value = true;
  error.value = '';
  staleHash.value = false;
  fieldErrors.value = {};
  try {
    const r = await apiGet<PRDResponse>(
      `/api/repos/${encodeURIComponent(fp)}/prd`,
    );
    const doc = normalize(r.content);
    originalPRD.value = doc;
    editedPRD.value = deepClone(doc);
    hash.value = r.hash;
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) {
      // prd.json missing — start with a blank doc the user can populate.
      const blank = normalize({ project: '' });
      originalPRD.value = blank;
      editedPRD.value = deepClone(blank);
      hash.value = '';
    } else {
      error.value = e instanceof Error ? e.message : String(e);
    }
  } finally {
    loading.value = false;
  }
}

async function save(fp: string) {
  if (!editedPRD.value) return;
  saving.value = true;
  try {
    // Viewer-side pre-flight hash check: re-fetch the current hash and
    // block if the file changed on disk since this editor was loaded.
    // The server still accepts If-Match as a defence-in-depth fallback.
    try {
      const cur = await apiGet<PRDResponse>(
        `/api/repos/${encodeURIComponent(fp)}/prd`,
      );
      if (hash.value && cur.hash !== hash.value) {
        staleHash.value = true;
        pushToast('warn', 'PRD changed on disk — reload to see latest');
        return;
      }
    } catch (e) {
      if (!(e instanceof ApiError && e.status === 404 && hash.value === '')) {
        throw e;
      }
    }

    const headers: Record<string, string> = {};
    if (hash.value) headers['If-Match'] = hash.value;

    const r = await apiPost<PRDResponse>(
      `/api/repos/${encodeURIComponent(fp)}/prd`,
      editedPRD.value,
      headers,
    );
    const doc = normalize(r.content);
    originalPRD.value = doc;
    editedPRD.value = deepClone(doc);
    hash.value = r.hash;
    fieldErrors.value = {};
    staleHash.value = false;
    pushToast('success', 'PRD saved');
  } catch (e) {
    if (e instanceof ApiError) {
      if (e.status === 409) {
        const body = e.body as PRDHashMismatchError | undefined;
        staleHash.value = true;
        if (body?.currentHash) hash.value = body.currentHash;
        pushToast('warn', 'PRD changed on disk — reload to see latest');
        return;
      }
      if (e.status === 400) {
        const body = e.body as PRDValidationError | undefined;
        if (body?.error === 'validation_failed' && body.fields) {
          fieldErrors.value = body.fields;
          const firstPath = Object.keys(body.fields)[0];
          const firstMsg = firstPath ? body.fields[firstPath] : 'invalid PRD';
          pushToast('error', `Validation failed: ${firstPath} — ${firstMsg}`);
          return;
        }
      }
    }
    pushToast('error', e instanceof Error ? e.message : String(e));
  } finally {
    saving.value = false;
  }
}

function discard() {
  if (!originalPRD.value) return;
  editedPRD.value = deepClone(originalPRD.value);
  fieldErrors.value = {};
  staleHash.value = false;
}

export function PRDEditorRoute() {
  const { params } = useRoute();
  const fp = params.fp;
  useEffect(() => {
    if (fp) void load(fp);
  }, [fp]);

  if (!fp) return null;
  if (loading.value && !editedPRD.value)
    return (
      <div style={{ padding: 32, color: 'var(--fg-faint)' }}>
        Loading PRD…
      </div>
    );
  if (error.value)
    return (
      <div style={{ padding: 32, color: 'var(--err)' }}>
        Failed to load: {error.value}
      </div>
    );
  const edited = editedPRD.value;
  if (!edited) return null;

  const dirty = isDirty();
  const clientErrors = validateClient(edited);
  // Merge client + server errors — server errors are authoritative and win
  // on a key clash, client errors fill in anything the server hasn't seen.
  const allErrors: Record<string, string> = {
    ...clientErrors,
    ...fieldErrors.value,
  };
  const hasErrors = Object.keys(allErrors).length > 0;
  const saveDisabled =
    !dirty || saving.value || staleHash.value || hasErrors;

  return (
    <div style={{ padding: '22px 28px 80px', minHeight: '100%' }}>
      <div style={{ maxWidth: 1080, margin: '0 auto' }}>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 6,
            fontSize: 12,
            color: 'var(--fg-faint)',
            fontFamily: 'var(--font-mono)',
            marginBottom: 12,
          }}
        >
          <span>repo</span>
          <span style={{ color: 'var(--fg-ghost)' }}>/</span>
          <span style={{ color: 'var(--fg)' }}>prd</span>
        </div>

        <div
          style={{
            display: 'flex',
            alignItems: 'flex-end',
            justifyContent: 'space-between',
            gap: 16,
            marginBottom: 14,
            flexWrap: 'wrap',
          }}
        >
          <div>
            <h2
              style={{
                fontSize: 18,
                fontWeight: 600,
                letterSpacing: '-0.01em',
                margin: '0 0 4px',
                color: 'var(--fg)',
              }}
            >
              PRD editor
            </h2>
            <p
              style={{
                fontSize: 12,
                color: 'var(--fg-muted)',
                margin: 0,
              }}
            >
              Structured form for <code class="mono">prd.json</code>. Saves
              validate server-side and pretty-print on disk.
            </p>
          </div>
          <div
            style={{
              display: 'flex',
              gap: 8,
              alignItems: 'center',
            }}
          >
            {dirty && !staleHash.value && (
              <span
                style={{
                  fontSize: 11,
                  color: 'var(--warn)',
                  textTransform: 'uppercase',
                  letterSpacing: '0.08em',
                  fontWeight: 600,
                }}
              >
                unsaved
              </span>
            )}
            <button
              type="button"
              onClick={discard}
              disabled={!dirty || saving.value}
              style={{
                padding: '6px 12px',
                fontSize: 12,
                border: '1px solid var(--border)',
                borderRadius: 5,
                background: 'transparent',
                color: 'var(--fg)',
                cursor: !dirty || saving.value ? 'not-allowed' : 'pointer',
                opacity: !dirty || saving.value ? 0.5 : 1,
              }}
            >
              Discard
            </button>
            <button
              type="button"
              onClick={() => void save(fp)}
              disabled={saveDisabled}
              style={{
                padding: '6px 14px',
                fontSize: 12,
                border: '1px solid var(--ok)',
                borderRadius: 5,
                background: saveDisabled ? 'var(--bg-elev)' : 'var(--ok)',
                color: saveDisabled ? 'var(--fg-muted)' : 'white',
                cursor: saveDisabled ? 'not-allowed' : 'pointer',
                fontWeight: 600,
              }}
            >
              {saving.value ? 'Saving…' : 'Save'}
            </button>
          </div>
        </div>

        {staleHash.value && (
          <div
            style={{
              padding: '10px 14px',
              background: 'var(--warn-soft, #fff3cd)',
              color: 'var(--warn)',
              border: '1px solid var(--warn)',
              borderRadius: 6,
              fontSize: 13,
              marginBottom: 16,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              gap: 12,
            }}
          >
            <div>
              <strong style={{ fontWeight: 600 }}>PRD changed on disk.</strong>{' '}
              Another process (e.g. the daemon) updated{' '}
              <code class="mono">prd.json</code> after you opened this editor.
              Reload to see the latest; your edits will be lost.
            </div>
            <button
              type="button"
              onClick={() => void load(fp, true)}
              style={{
                padding: '5px 12px',
                fontSize: 12,
                border: '1px solid var(--warn)',
                borderRadius: 5,
                background: 'transparent',
                color: 'var(--warn)',
                cursor: 'pointer',
                fontWeight: 600,
              }}
            >
              Reload
            </button>
          </div>
        )}

        <ProjectMeta
          doc={edited}
          fieldErrors={allErrors}
          onChange={(patch) => {
            editedPRD.value = { ...edited, ...patch };
            if (Object.keys(fieldErrors.value).length > 0)
              fieldErrors.value = {};
          }}
        />

        <h3
          style={{
            fontSize: 13,
            fontWeight: 600,
            textTransform: 'uppercase',
            letterSpacing: '0.08em',
            color: 'var(--fg-muted)',
            margin: '22px 0 10px',
          }}
        >
          User stories
          <span
            style={{
              marginLeft: 8,
              fontFamily: 'var(--font-mono)',
              fontSize: 11,
              color: 'var(--fg-faint)',
            }}
          >
            {edited.userStories.length}
          </span>
        </h3>

        <StoriesTable
          stories={edited.userStories}
          fieldErrors={allErrors}
          onChange={(next) => {
            editedPRD.value = { ...edited, userStories: next };
            if (Object.keys(fieldErrors.value).length > 0)
              fieldErrors.value = {};
          }}
        />
      </div>
    </div>
  );
}

function ProjectMeta({
  doc,
  fieldErrors,
  onChange,
}: {
  doc: PRDDocument;
  fieldErrors: Record<string, string>;
  onChange: (patch: Partial<PRDDocument>) => void;
}) {
  const input = (value: string, hasErr: boolean, onInput: (v: string) => void) => (
    <input
      value={value}
      onInput={(e) => onInput((e.currentTarget as HTMLInputElement).value)}
      style={{
        width: '100%',
        padding: '5px 8px',
        fontSize: 12.5,
        background: 'var(--bg-card)',
        color: 'var(--fg)',
        border: `1px solid ${hasErr ? 'var(--err)' : 'var(--border)'}`,
        borderRadius: 5,
        boxSizing: 'border-box',
      }}
    />
  );
  const projectErr = fieldErrors['project'];
  return (
    <div
      style={{
        border: '1px solid var(--border)',
        borderRadius: 8,
        background: 'var(--bg-elev)',
        padding: 14,
        display: 'grid',
        gap: 12,
        gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
      }}
    >
      <div>
        <Label>Project</Label>
        {input(doc.project, !!projectErr, (v) => onChange({ project: v }))}
        {projectErr && (
          <div style={{ color: 'var(--err)', fontSize: 11, marginTop: 3 }}>
            {projectErr}
          </div>
        )}
      </div>
      <div>
        <Label>Branch name</Label>
        {input(doc.branchName ?? '', false, (v) => onChange({ branchName: v }))}
      </div>
      <div style={{ gridColumn: '1 / -1' }}>
        <Label>Description</Label>
        <textarea
          value={doc.description ?? ''}
          onInput={(e) =>
            onChange({
              description: (e.currentTarget as HTMLTextAreaElement).value,
            })
          }
          rows={2}
          style={{
            width: '100%',
            padding: '5px 8px',
            fontSize: 12.5,
            background: 'var(--bg-card)',
            color: 'var(--fg)',
            border: '1px solid var(--border)',
            borderRadius: 5,
            resize: 'vertical',
            boxSizing: 'border-box',
          }}
        />
      </div>
      <div>
        <Label>Build command</Label>
        {input(doc.buildCommand ?? '', false, (v) => onChange({ buildCommand: v }))}
      </div>
    </div>
  );
}

function Label({ children }: { children: preact.ComponentChildren }) {
  return (
    <label
      style={{
        display: 'block',
        fontSize: 10.5,
        textTransform: 'uppercase',
        letterSpacing: '0.08em',
        color: 'var(--fg-muted)',
        marginBottom: 4,
        fontWeight: 600,
      }}
    >
      {children}
    </label>
  );
}
