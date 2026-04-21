import type { PRDUserStory } from '../../lib/api';

// StoriesTable renders one row per user story. All cells are controlled by
// the parent route via `onChange(nextStories)`; this component is stateless
// and purely presentational. `fieldErrors` is a flat map keyed by JSON path
// (e.g. "userStories[2].id") — cells whose path is present get a red
// outline and an inline hint.
export interface StoriesTableProps {
  stories: PRDUserStory[];
  fieldErrors: Record<string, string>;
  onChange: (next: PRDUserStory[]) => void;
}

function emptyStory(priority: number): PRDUserStory {
  return {
    id: '',
    title: '',
    description: '',
    acceptanceCriteria: [],
    priority,
    passes: false,
    notes: '',
    dependsOn: [],
    approach: '',
  };
}

export function StoriesTable({
  stories,
  fieldErrors,
  onChange,
}: StoriesTableProps) {
  const replaceAt = (i: number, patch: Partial<PRDUserStory>) => {
    const next = stories.map((s, idx) => (idx === i ? { ...s, ...patch } : s));
    onChange(next);
  };

  const removeAt = (i: number) => {
    onChange(stories.filter((_, idx) => idx !== i));
  };

  const moveUp = (i: number) => {
    if (i === 0) return;
    const next = [...stories];
    const a = { ...next[i - 1] };
    const b = { ...next[i] };
    const pa = a.priority;
    a.priority = b.priority;
    b.priority = pa;
    next[i - 1] = b;
    next[i] = a;
    onChange(next);
  };

  const moveDown = (i: number) => {
    if (i === stories.length - 1) return;
    const next = [...stories];
    const a = { ...next[i] };
    const b = { ...next[i + 1] };
    const pa = a.priority;
    a.priority = b.priority;
    b.priority = pa;
    next[i] = b;
    next[i + 1] = a;
    onChange(next);
  };

  const addNew = () => {
    const maxP = stories.reduce((m, s) => (s.priority > m ? s.priority : m), 0);
    onChange([...stories, emptyStory(maxP + 1)]);
  };

  const allIDs = stories.map((s) => s.id).filter((id) => id.trim() !== '');

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      {stories.length === 0 && (
        <div
          style={{
            padding: 18,
            border: '1px dashed var(--border)',
            borderRadius: 8,
            color: 'var(--fg-faint)',
            fontSize: 13,
            textAlign: 'center',
            fontStyle: 'italic',
          }}
        >
          No stories yet. Use “Add story” below to create the first one.
        </div>
      )}

      {stories.map((s, i) => (
        <StoryRow
          key={i}
          index={i}
          story={s}
          canMoveUp={i > 0}
          canMoveDown={i < stories.length - 1}
          otherIDs={allIDs.filter((id) => id !== s.id)}
          fieldErrors={fieldErrors}
          onPatch={(patch) => replaceAt(i, patch)}
          onRemove={() => removeAt(i)}
          onMoveUp={() => moveUp(i)}
          onMoveDown={() => moveDown(i)}
        />
      ))}

      <div>
        <button
          type="button"
          onClick={addNew}
          style={{
            padding: '7px 14px',
            fontSize: 12.5,
            border: '1px solid var(--border)',
            borderRadius: 6,
            background: 'var(--bg-elev)',
            color: 'var(--fg)',
            cursor: 'pointer',
          }}
        >
          + Add story
        </button>
      </div>
    </div>
  );
}

interface StoryRowProps {
  index: number;
  story: PRDUserStory;
  canMoveUp: boolean;
  canMoveDown: boolean;
  otherIDs: string[];
  fieldErrors: Record<string, string>;
  onPatch: (patch: Partial<PRDUserStory>) => void;
  onRemove: () => void;
  onMoveUp: () => void;
  onMoveDown: () => void;
}

function StoryRow({
  index,
  story,
  canMoveUp,
  canMoveDown,
  otherIDs,
  fieldErrors,
  onPatch,
  onRemove,
  onMoveUp,
  onMoveDown,
}: StoryRowProps) {
  const base = `userStories[${index}]`;
  const err = (suffix: string) => fieldErrors[`${base}${suffix}`];

  const inputStyle = (hasErr: boolean) =>
    ({
      width: '100%',
      padding: '5px 8px',
      fontSize: 12.5,
      background: 'var(--bg-card)',
      color: 'var(--fg)',
      border: `1px solid ${hasErr ? 'var(--err)' : 'var(--border)'}`,
      borderRadius: 5,
      fontFamily: 'inherit',
      boxSizing: 'border-box',
    }) as const;

  const labelStyle = {
    display: 'block',
    fontSize: 10.5,
    textTransform: 'uppercase' as const,
    letterSpacing: '0.08em',
    color: 'var(--fg-muted)',
    marginBottom: 4,
    fontWeight: 600,
  };

  const errText = (msg?: string) =>
    msg ? (
      <div style={{ color: 'var(--err)', fontSize: 11, marginTop: 3 }}>
        {msg}
      </div>
    ) : null;

  return (
    <div
      style={{
        border: '1px solid var(--border)',
        borderRadius: 8,
        background: 'var(--bg-elev)',
        padding: 14,
        display: 'grid',
        gap: 12,
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 10,
          borderBottom: '1px solid var(--border-soft)',
          paddingBottom: 10,
        }}
      >
        <div style={{ display: 'flex', gap: 4 }}>
          <IconButton
            disabled={!canMoveUp}
            onClick={onMoveUp}
            label="Move up"
            glyph="↑"
          />
          <IconButton
            disabled={!canMoveDown}
            onClick={onMoveDown}
            label="Move down"
            glyph="↓"
          />
        </div>
        <span
          class="mono"
          style={{ fontSize: 11, color: 'var(--fg-faint)' }}
        >
          #{index + 1}
        </span>
        <span
          style={{
            fontSize: 11,
            padding: '2px 7px',
            borderRadius: 10,
            background: story.passes ? 'var(--ok-soft)' : 'var(--bg-card)',
            color: story.passes ? 'var(--ok)' : 'var(--fg-muted)',
            border: '1px solid var(--border-soft)',
          }}
          title="Daemon-owned; read-only in this editor"
        >
          {story.passes ? 'passes ✓' : 'pending'}
        </span>
        <div style={{ flex: 1 }} />
        <button
          type="button"
          onClick={onRemove}
          style={{
            padding: '4px 10px',
            fontSize: 11,
            border: '1px solid var(--border)',
            borderRadius: 5,
            background: 'transparent',
            color: 'var(--err)',
            cursor: 'pointer',
          }}
        >
          Delete
        </button>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 140px', gap: 12 }}>
        <div>
          <label style={labelStyle}>ID</label>
          <input
            value={story.id}
            onInput={(e) =>
              onPatch({ id: (e.currentTarget as HTMLInputElement).value })
            }
            placeholder="e.g. A-001"
            style={inputStyle(!!err('.id'))}
          />
          {errText(err('.id'))}
        </div>
        <div>
          <label style={labelStyle}>Title</label>
          <input
            value={story.title}
            onInput={(e) =>
              onPatch({ title: (e.currentTarget as HTMLInputElement).value })
            }
            style={inputStyle(!!err('.title'))}
          />
          {errText(err('.title'))}
        </div>
        <div>
          <label style={labelStyle}>Priority</label>
          <input
            type="number"
            min={0}
            step={1}
            value={String(story.priority)}
            onInput={(e) => {
              const raw = (e.currentTarget as HTMLInputElement).value;
              const n = Number.parseInt(raw, 10);
              onPatch({ priority: Number.isFinite(n) ? n : 0 });
            }}
            style={inputStyle(!!err('.priority'))}
          />
          {errText(err('.priority'))}
        </div>
      </div>

      <div>
        <label style={labelStyle}>Description</label>
        <textarea
          value={story.description}
          onInput={(e) =>
            onPatch({
              description: (e.currentTarget as HTMLTextAreaElement).value,
            })
          }
          rows={2}
          style={{ ...inputStyle(!!err('.description')), resize: 'vertical' }}
        />
        {errText(err('.description'))}
      </div>

      <div>
        <label style={labelStyle}>Acceptance criteria</label>
        <AcceptanceCriteriaEditor
          criteria={story.acceptanceCriteria}
          fieldErrors={fieldErrors}
          base={base}
          onChange={(next) => onPatch({ acceptanceCriteria: next })}
        />
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
        <div>
          <label style={labelStyle}>Depends on</label>
          <DependsOnEditor
            dependsOn={story.dependsOn ?? []}
            options={otherIDs}
            fieldErrors={fieldErrors}
            base={base}
            onChange={(next) => onPatch({ dependsOn: next })}
          />
        </div>
        <div>
          <label style={labelStyle}>Approach</label>
          <textarea
            value={story.approach ?? ''}
            onInput={(e) =>
              onPatch({
                approach: (e.currentTarget as HTMLTextAreaElement).value,
              })
            }
            rows={2}
            style={{ ...inputStyle(false), resize: 'vertical' }}
          />
        </div>
      </div>

      <div>
        <label style={labelStyle}>Notes</label>
        <textarea
          value={story.notes}
          onInput={(e) =>
            onPatch({ notes: (e.currentTarget as HTMLTextAreaElement).value })
          }
          rows={2}
          style={{ ...inputStyle(false), resize: 'vertical' }}
        />
      </div>
    </div>
  );
}

function IconButton({
  glyph,
  label,
  onClick,
  disabled,
}: {
  glyph: string;
  label: string;
  onClick: () => void;
  disabled: boolean;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={onClick}
      style={{
        width: 24,
        height: 24,
        borderRadius: 5,
        border: '1px solid var(--border)',
        background: 'var(--bg-card)',
        color: disabled ? 'var(--fg-ghost)' : 'var(--fg)',
        cursor: disabled ? 'not-allowed' : 'pointer',
        fontSize: 13,
        lineHeight: 1,
      }}
    >
      {glyph}
    </button>
  );
}

function AcceptanceCriteriaEditor({
  criteria,
  fieldErrors,
  base,
  onChange,
}: {
  criteria: string[];
  fieldErrors: Record<string, string>;
  base: string;
  onChange: (next: string[]) => void;
}) {
  const update = (i: number, value: string) => {
    onChange(criteria.map((c, idx) => (idx === i ? value : c)));
  };
  const remove = (i: number) => onChange(criteria.filter((_, idx) => idx !== i));
  const add = () => onChange([...criteria, '']);
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      {criteria.map((c, i) => {
        const err = fieldErrors[`${base}.acceptanceCriteria[${i}]`];
        return (
          <div key={i} style={{ display: 'flex', gap: 6, alignItems: 'flex-start' }}>
            <textarea
              value={c}
              onInput={(e) => update(i, (e.currentTarget as HTMLTextAreaElement).value)}
              rows={1}
              style={{
                flex: 1,
                padding: '5px 8px',
                fontSize: 12.5,
                background: 'var(--bg-card)',
                color: 'var(--fg)',
                border: `1px solid ${err ? 'var(--err)' : 'var(--border)'}`,
                borderRadius: 5,
                fontFamily: 'inherit',
                resize: 'vertical',
              }}
            />
            <button
              type="button"
              onClick={() => remove(i)}
              aria-label={`Remove criterion ${i + 1}`}
              style={{
                padding: '2px 8px',
                fontSize: 11,
                border: '1px solid var(--border)',
                borderRadius: 4,
                background: 'transparent',
                color: 'var(--fg-muted)',
                cursor: 'pointer',
              }}
            >
              ×
            </button>
          </div>
        );
      })}
      <div>
        <button
          type="button"
          onClick={add}
          style={{
            padding: '3px 10px',
            fontSize: 11,
            border: '1px dashed var(--border)',
            borderRadius: 4,
            background: 'transparent',
            color: 'var(--fg-muted)',
            cursor: 'pointer',
          }}
        >
          + Add criterion
        </button>
      </div>
    </div>
  );
}

function DependsOnEditor({
  dependsOn,
  options,
  fieldErrors,
  base,
  onChange,
}: {
  dependsOn: string[];
  options: string[];
  fieldErrors: Record<string, string>;
  base: string;
  onChange: (next: string[]) => void;
}) {
  const available = options.filter((id) => !dependsOn.includes(id));
  const addChip = (value: string) => {
    if (!value) return;
    onChange([...dependsOn, value]);
  };
  const removeChip = (i: number) =>
    onChange(dependsOn.filter((_, idx) => idx !== i));

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
        {dependsOn.length === 0 && (
          <span style={{ fontSize: 11, color: 'var(--fg-faint)', fontStyle: 'italic' }}>
            none
          </span>
        )}
        {dependsOn.map((id, i) => {
          const err = fieldErrors[`${base}.dependsOn[${i}]`];
          return (
            <span
              key={i}
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                gap: 5,
                padding: '2px 4px 2px 8px',
                borderRadius: 12,
                background: err ? 'var(--warn-soft, #fff3cd)' : 'var(--bg-card)',
                border: `1px solid ${err ? 'var(--err)' : 'var(--border)'}`,
                fontSize: 11.5,
                color: 'var(--fg)',
                fontFamily: 'var(--font-mono)',
              }}
              title={err ?? ''}
            >
              {id}
              <button
                type="button"
                onClick={() => removeChip(i)}
                aria-label={`Remove dep ${id}`}
                style={{
                  width: 16,
                  height: 16,
                  fontSize: 12,
                  color: 'var(--fg-muted)',
                  lineHeight: 1,
                  background: 'transparent',
                }}
              >
                ×
              </button>
            </span>
          );
        })}
      </div>
      <select
        value=""
        onChange={(e) => {
          const v = (e.currentTarget as HTMLSelectElement).value;
          if (v) {
            addChip(v);
            (e.currentTarget as HTMLSelectElement).value = '';
          }
        }}
        disabled={available.length === 0}
        style={{
          padding: '5px 8px',
          fontSize: 12,
          background: 'var(--bg-card)',
          color: 'var(--fg)',
          border: '1px solid var(--border)',
          borderRadius: 5,
          maxWidth: 200,
        }}
      >
        <option value="">
          {available.length === 0 ? '(no other stories)' : '+ add dependency…'}
        </option>
        {available.map((id) => (
          <option key={id} value={id}>
            {id}
          </option>
        ))}
      </select>
    </div>
  );
}
