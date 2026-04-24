import { useEffect, useState } from 'preact/hooks';
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

// Module-scope signals survive route remounts so switching away and back
// does not reload the PRD unless the user explicitly refreshes.
const loading = signal<boolean>(false);
const saving = signal<boolean>(false);
const error = signal<string>('');
const prdDoc = signal<PRDDocument | null>(null);
const hash = signal<string>('');
const staleHash = signal<boolean>(false);
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

async function load(fp: string, force = false) {
  if (!force && currentFP.value === fp && prdDoc.value) return;
  currentFP.value = fp;
  loading.value = true;
  error.value = '';
  staleHash.value = false;
  try {
    const r = await apiGet<PRDResponse>(
      `/api/repos/${encodeURIComponent(fp)}/prd`,
    );
    prdDoc.value = normalize(r.content);
    hash.value = r.hash;
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) {
      prdDoc.value = normalize({ project: '' });
      hash.value = '';
    } else {
      error.value = e instanceof Error ? e.message : String(e);
    }
  } finally {
    loading.value = false;
  }
}

async function save(fp: string, doc: PRDDocument): Promise<boolean> {
  saving.value = true;
  try {
    try {
      const cur = await apiGet<PRDResponse>(
        `/api/repos/${encodeURIComponent(fp)}/prd`,
      );
      if (hash.value && cur.hash !== hash.value) {
        staleHash.value = true;
        pushToast('warn', 'PRD changed on disk — reload to see latest');
        return false;
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
      doc,
      headers,
    );
    prdDoc.value = normalize(r.content);
    hash.value = r.hash;
    staleHash.value = false;
    pushToast('success', 'PRD saved');
    return true;
  } catch (e) {
    if (e instanceof ApiError) {
      if (e.status === 409) {
        const body = e.body as PRDHashMismatchError | undefined;
        staleHash.value = true;
        if (body?.currentHash) hash.value = body.currentHash;
        pushToast('warn', 'PRD changed on disk — reload to see latest');
        return false;
      }
      if (e.status === 400) {
        const body = e.body as PRDValidationError | undefined;
        if (body?.error === 'validation_failed' && body.fields) {
          const firstPath = Object.keys(body.fields)[0];
          const firstMsg = firstPath ? body.fields[firstPath] : 'invalid PRD';
          pushToast('error', `Validation failed: ${firstPath} — ${firstMsg}`);
          return false;
        }
      }
    }
    pushToast('error', e instanceof Error ? e.message : String(e));
    return false;
  } finally {
    saving.value = false;
  }
}

export function PRDEditorRoute() {
  const { params } = useRoute();
  const fp = params.fp;
  const [jsonMode, setJsonMode] = useState(false);

  useEffect(() => {
    if (fp) void load(fp);
  }, [fp]);

  if (!fp) return null;
  if (loading.value && !prdDoc.value)
    return (
      <div style={{ padding: 32, color: 'var(--fg-faint)' }}>Loading PRD…</div>
    );
  if (error.value)
    return (
      <div style={{ padding: 32, color: 'var(--err)' }}>
        Failed to load: {error.value}
      </div>
    );
  const doc = prdDoc.value;
  if (!doc) return null;

  return (
    <div style={{ padding: '22px 28px 80px', minHeight: '100%' }}>
      <div style={{ maxWidth: 880, margin: '0 auto' }}>
        <Breadcrumb />

        <div
          style={{
            display: 'flex',
            alignItems: 'flex-start',
            justifyContent: 'space-between',
            gap: 16,
            marginBottom: 14,
            flexWrap: 'wrap',
          }}
        >
          <div style={{ minWidth: 0 }}>
            <h1
              style={{
                fontSize: 22,
                fontWeight: 600,
                letterSpacing: '-0.015em',
                margin: 0,
                color: 'var(--fg)',
              }}
            >
              {doc.project || 'Untitled project'}
            </h1>
            {doc.branchName && (
              <div
                class="mono"
                style={{ fontSize: 11.5, color: 'var(--fg-faint)', marginTop: 4 }}
              >
                branch: {doc.branchName}
              </div>
            )}
          </div>
          {!jsonMode && (
            <button
              type="button"
              onClick={() => setJsonMode(true)}
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: 6,
                padding: '7px 12px',
                fontSize: 12.5,
                fontWeight: 600,
                borderRadius: 6,
                border: '1px solid var(--border)',
                background: 'var(--bg-elev)',
                color: 'var(--fg)',
                cursor: 'pointer',
              }}
            >
              <PencilIcon />
              Edit JSON
            </button>
          )}
        </div>

        {staleHash.value && (
          <StaleHashBanner onReload={() => void load(fp, true)} />
        )}

        {jsonMode ? (
          <JsonEditor
            doc={doc}
            saving={saving.value}
            onCancel={() => setJsonMode(false)}
            onSave={async (next) => {
              const ok = await save(fp, next);
              if (ok) setJsonMode(false);
            }}
          />
        ) : (
          <>
            <PRDOverview doc={doc} />
            <StoriesReadView stories={doc.userStories} />
            <AddStoryForm
              existingIds={new Set(doc.userStories.map((s) => s.id))}
              onAdd={async (story) => {
                const next: PRDDocument = {
                  ...doc,
                  userStories: [...doc.userStories, story],
                };
                await save(fp, next);
              }}
              saving={saving.value}
            />
          </>
        )}
      </div>
    </div>
  );
}

function Breadcrumb() {
  return (
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
  );
}

function StaleHashBanner({ onReload }: { onReload: () => void }) {
  return (
    <div
      style={{
        padding: '10px 14px',
        background: 'var(--warn-soft)',
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
        <strong style={{ fontWeight: 600 }}>PRD changed on disk.</strong> Reload
        to see the latest; your edits will be lost.
      </div>
      <button
        type="button"
        onClick={onReload}
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
  );
}

function PRDOverview({ doc }: { doc: PRDDocument }) {
  const hasConstraints = (doc.constraints?.length ?? 0) > 0;
  const hasRepos = (doc.repos?.length ?? 0) > 0;
  return (
    <section
      style={{
        border: '1px solid var(--border)',
        borderRadius: 8,
        background: 'var(--bg-elev)',
        padding: '14px 16px',
        marginBottom: 22,
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
      }}
    >
      {doc.description && (
        <Field label="Description">
          <div
            style={{
              fontSize: 13.5,
              color: 'var(--fg)',
              lineHeight: 1.55,
              whiteSpace: 'pre-wrap',
            }}
          >
            {doc.description}
          </div>
        </Field>
      )}
      {doc.buildCommand && (
        <Field label="Build command">
          <code
            class="mono"
            style={{
              display: 'inline-block',
              padding: '3px 7px',
              fontSize: 12,
              background: 'var(--bg-sunken)',
              border: '1px solid var(--border-soft)',
              borderRadius: 4,
            }}
          >
            {doc.buildCommand}
          </code>
        </Field>
      )}
      {hasRepos && (
        <Field label="Repos">
          <ul style={{ margin: 0, paddingLeft: 18, fontSize: 13 }}>
            {doc.repos!.map((r, i) => (
              <li key={i} style={{ color: 'var(--fg)' }}>
                {r}
              </li>
            ))}
          </ul>
        </Field>
      )}
      {hasConstraints && (
        <Field label="Constraints">
          <ul style={{ margin: 0, paddingLeft: 18, fontSize: 13 }}>
            {doc.constraints!.map((c, i) => (
              <li key={i} style={{ color: 'var(--fg)' }}>
                {c}
              </li>
            ))}
          </ul>
        </Field>
      )}
      {!doc.description && !doc.buildCommand && !hasRepos && !hasConstraints && (
        <div style={{ fontSize: 12.5, color: 'var(--fg-faint)', fontStyle: 'italic' }}>
          No project-level fields yet. Use “Edit JSON” to add description,
          build command, repos, or constraints.
        </div>
      )}
    </section>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: preact.ComponentChildren;
}) {
  return (
    <div>
      <div
        style={{
          fontSize: 10.5,
          textTransform: 'uppercase',
          letterSpacing: '0.08em',
          color: 'var(--fg-faint)',
          fontWeight: 600,
          marginBottom: 4,
        }}
      >
        {label}
      </div>
      {children}
    </div>
  );
}

function StoriesReadView({ stories }: { stories: PRDUserStory[] }) {
  return (
    <section style={{ marginBottom: 24 }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'baseline',
          justifyContent: 'space-between',
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
          User stories
        </h2>
        <span style={{ fontSize: 11.5, color: 'var(--fg-faint)' }}>
          {stories.length} total
        </span>
      </div>
      {stories.length === 0 ? (
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
          No stories yet. Add one below.
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          {stories.map((s) => (
            <StoryCard key={s.id || s.title} story={s} />
          ))}
        </div>
      )}
    </section>
  );
}

function StoryCard({ story }: { story: PRDUserStory }) {
  const pass = story.passes;
  return (
    <article
      style={{
        border: '1px solid var(--border)',
        borderRadius: 8,
        background: 'var(--bg-elev)',
        padding: '12px 14px',
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'baseline',
          gap: 10,
          marginBottom: 6,
          flexWrap: 'wrap',
        }}
      >
        <span class="chip mono">{story.id || '—'}</span>
        <span style={{ fontSize: 14, color: 'var(--fg)', fontWeight: 600 }}>
          {story.title || '(untitled)'}
        </span>
        <span style={{ marginLeft: 'auto', display: 'flex', gap: 6 }}>
          {typeof story.priority === 'number' && (
            <span
              class="chip mono"
              title="priority"
              style={{ fontSize: 11 }}
            >
              p{story.priority}
            </span>
          )}
          <span class={pass ? 'chip ok' : 'chip'}>
            {pass ? 'passes' : 'pending'}
          </span>
        </span>
      </div>
      {story.description && (
        <div
          style={{
            fontSize: 13,
            color: 'var(--fg-muted)',
            lineHeight: 1.55,
            whiteSpace: 'pre-wrap',
            marginBottom:
              (story.acceptanceCriteria?.length ?? 0) > 0 ||
              (story.dependsOn?.length ?? 0) > 0
                ? 8
                : 0,
          }}
        >
          {story.description}
        </div>
      )}
      {(story.acceptanceCriteria?.length ?? 0) > 0 && (
        <ul
          style={{
            margin: '6px 0 0',
            paddingLeft: 18,
            fontSize: 12.5,
            color: 'var(--fg)',
          }}
        >
          {story.acceptanceCriteria!.map((c, i) => (
            <li key={i} style={{ marginBottom: 2 }}>
              {c}
            </li>
          ))}
        </ul>
      )}
      {(story.dependsOn?.length ?? 0) > 0 && (
        <div
          style={{
            fontSize: 11.5,
            color: 'var(--fg-faint)',
            marginTop: 6,
            fontFamily: 'var(--font-mono)',
          }}
        >
          depends on: {story.dependsOn!.join(', ')}
        </div>
      )}
    </article>
  );
}

function JsonEditor({
  doc,
  saving,
  onCancel,
  onSave,
}: {
  doc: PRDDocument;
  saving: boolean;
  onCancel: () => void;
  onSave: (next: PRDDocument) => Promise<void>;
}) {
  const [text, setText] = useState(() => JSON.stringify(doc, null, 2));
  const [parseErr, setParseErr] = useState<string>('');

  const onSaveClick = async () => {
    let parsed: unknown;
    try {
      parsed = JSON.parse(text);
    } catch (e) {
      setParseErr(e instanceof Error ? e.message : String(e));
      return;
    }
    setParseErr('');
    await onSave(normalize(parsed));
  };

  return (
    <section
      style={{
        border: '1px solid var(--border)',
        borderRadius: 8,
        background: 'var(--bg-elev)',
        padding: 14,
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
      }}
    >
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          gap: 10,
        }}
      >
        <div
          style={{
            fontSize: 12.5,
            color: 'var(--fg-muted)',
          }}
        >
          Full <code class="mono">prd.json</code> — parsed + validated
          server-side on save.
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <button
            type="button"
            onClick={onCancel}
            disabled={saving}
            style={btnGhost}
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={() => void onSaveClick()}
            disabled={saving}
            style={btnPrimary(saving)}
          >
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>
      <textarea
        value={text}
        onInput={(e) => setText((e.currentTarget as HTMLTextAreaElement).value)}
        spellcheck={false}
        style={{
          width: '100%',
          minHeight: 480,
          fontFamily: 'var(--font-mono)',
          fontSize: 12,
          lineHeight: 1.55,
          padding: 12,
          background: 'var(--bg-sunken)',
          color: 'var(--fg)',
          border: `1px solid ${parseErr ? 'var(--err)' : 'var(--border)'}`,
          borderRadius: 6,
          resize: 'vertical',
          boxSizing: 'border-box',
          tabSize: 2,
        }}
      />
      {parseErr && (
        <div
          style={{
            fontSize: 12,
            color: 'var(--err)',
            background: 'var(--err-soft)',
            border: '1px solid var(--err)',
            borderRadius: 5,
            padding: '6px 9px',
          }}
        >
          JSON parse error: {parseErr}
        </div>
      )}
    </section>
  );
}

function AddStoryForm({
  existingIds,
  onAdd,
  saving,
}: {
  existingIds: Set<string>;
  onAdd: (story: PRDUserStory) => Promise<void>;
  saving: boolean;
}) {
  const [id, setId] = useState('');
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [priority, setPriority] = useState('0');
  const [localErr, setLocalErr] = useState('');

  const reset = () => {
    setId('');
    setTitle('');
    setDescription('');
    setPriority('0');
    setLocalErr('');
  };

  const submit = async () => {
    const trimmedId = id.trim();
    const trimmedTitle = title.trim();
    const trimmedDesc = description.trim();
    if (!trimmedId) return setLocalErr('id is required');
    if (existingIds.has(trimmedId))
      return setLocalErr(`id "${trimmedId}" already exists`);
    if (!trimmedTitle) return setLocalErr('title is required');
    if (!trimmedDesc) return setLocalErr('description is required');
    const p = Number(priority);
    if (!Number.isFinite(p) || p < 0)
      return setLocalErr('priority must be a non-negative number');
    setLocalErr('');
    await onAdd({
      id: trimmedId,
      title: trimmedTitle,
      description: trimmedDesc,
      acceptanceCriteria: [],
      priority: p,
      passes: false,
      notes: '',
      dependsOn: [],
      approach: '',
    });
    reset();
  };

  return (
    <section
      style={{
        border: '1px dashed var(--border)',
        borderRadius: 8,
        padding: '14px 16px',
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
      }}
    >
      <h3
        style={{
          fontSize: 12.5,
          fontWeight: 600,
          margin: 0,
          color: 'var(--fg-muted)',
          textTransform: 'uppercase',
          letterSpacing: '0.07em',
        }}
      >
        Add story
      </h3>
      <div
        style={{
          display: 'grid',
          gap: 10,
          gridTemplateColumns: '140px 1fr 80px',
        }}
      >
        <LabeledInput
          label="ID"
          value={id}
          mono
          placeholder="RV-042"
          onChange={setId}
        />
        <LabeledInput
          label="Title"
          value={title}
          placeholder="Short summary of the story"
          onChange={setTitle}
        />
        <LabeledInput
          label="Priority"
          value={priority}
          mono
          placeholder="0"
          onChange={setPriority}
        />
      </div>
      <LabeledTextarea
        label="Description"
        value={description}
        placeholder="What is this story about? What's the acceptance criteria?"
        onChange={setDescription}
      />
      {localErr && (
        <div
          style={{
            fontSize: 12,
            color: 'var(--err)',
            background: 'var(--err-soft)',
            border: '1px solid var(--err)',
            borderRadius: 5,
            padding: '6px 9px',
          }}
        >
          {localErr}
        </div>
      )}
      <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
        <button type="button" onClick={reset} disabled={saving} style={btnGhost}>
          Clear
        </button>
        <button
          type="button"
          onClick={() => void submit()}
          disabled={saving || !id.trim() || !title.trim() || !description.trim()}
          style={btnPrimary(saving || !id.trim() || !title.trim() || !description.trim())}
        >
          {saving ? 'Saving…' : 'Add & save'}
        </button>
      </div>
    </section>
  );
}

function LabeledInput({
  label,
  value,
  placeholder,
  mono,
  onChange,
}: {
  label: string;
  value: string;
  placeholder?: string;
  mono?: boolean;
  onChange: (v: string) => void;
}) {
  return (
    <div>
      <MiniLabel>{label}</MiniLabel>
      <input
        value={value}
        placeholder={placeholder}
        onInput={(e) => onChange((e.currentTarget as HTMLInputElement).value)}
        class={mono ? 'mono' : ''}
        style={{
          width: '100%',
          padding: '6px 9px',
          fontSize: 12.5,
          background: 'var(--bg-elev)',
          color: 'var(--fg)',
          border: '1px solid var(--border)',
          borderRadius: 5,
          boxSizing: 'border-box',
        }}
      />
    </div>
  );
}

function LabeledTextarea({
  label,
  value,
  placeholder,
  onChange,
}: {
  label: string;
  value: string;
  placeholder?: string;
  onChange: (v: string) => void;
}) {
  return (
    <div>
      <MiniLabel>{label}</MiniLabel>
      <textarea
        value={value}
        placeholder={placeholder}
        onInput={(e) =>
          onChange((e.currentTarget as HTMLTextAreaElement).value)
        }
        rows={3}
        style={{
          width: '100%',
          padding: '6px 9px',
          fontSize: 12.5,
          background: 'var(--bg-elev)',
          color: 'var(--fg)',
          border: '1px solid var(--border)',
          borderRadius: 5,
          boxSizing: 'border-box',
          resize: 'vertical',
          lineHeight: 1.5,
        }}
      />
    </div>
  );
}

function MiniLabel({ children }: { children: preact.ComponentChildren }) {
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

function PencilIcon() {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2.2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.121 2.121 0 1 1 3 3L7 19l-4 1 1-4 12.5-12.5z" />
    </svg>
  );
}

const btnGhost: preact.JSX.CSSProperties = {
  padding: '6px 12px',
  fontSize: 12,
  border: '1px solid var(--border)',
  borderRadius: 5,
  background: 'transparent',
  color: 'var(--fg)',
  cursor: 'pointer',
};

function btnPrimary(disabled: boolean): preact.JSX.CSSProperties {
  return {
    padding: '6px 14px',
    fontSize: 12,
    border: '1px solid var(--accent-border)',
    borderRadius: 5,
    background: disabled ? 'var(--bg-elev)' : 'var(--accent-soft)',
    color: disabled ? 'var(--fg-muted)' : 'var(--accent-ink)',
    cursor: disabled ? 'not-allowed' : 'pointer',
    fontWeight: 600,
  };
}
