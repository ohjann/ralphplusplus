import { useEffect } from 'preact/hooks';
import { signal } from '@preact/signals';
import { useRoute, useLocation } from 'preact-iso';
import { marked } from 'marked';
import { apiGet, ApiError } from '../lib/api';

interface DocFile {
  path: string;
  name: string;
  size: number;
  mtime: string;
}

// Module-scope signals keep the rendered doc around when users switch
// between files — a small comfort that feels noticeably faster than
// re-fetching on every click.
const currentFP = signal<string>('');
const files = signal<DocFile[]>([]);
const selectedPath = signal<string>('');
const contentByPath = signal<Record<string, string>>({});
const loadingList = signal<boolean>(false);
const loadingContent = signal<boolean>(false);
const listError = signal<string>('');
const contentError = signal<string>('');

async function loadList(fp: string) {
  if (currentFP.value === fp && files.value.length > 0) return;
  currentFP.value = fp;
  loadingList.value = true;
  listError.value = '';
  files.value = [];
  try {
    const list = await apiGet<DocFile[]>(
      `/api/repos/${encodeURIComponent(fp)}/docs`,
    );
    files.value = list;
  } catch (e) {
    listError.value = e instanceof Error ? e.message : String(e);
  } finally {
    loadingList.value = false;
  }
}

async function loadContent(fp: string, path: string) {
  selectedPath.value = path;
  if (contentByPath.value[path] !== undefined) return;
  loadingContent.value = true;
  contentError.value = '';
  try {
    const r = await fetch(
      `/api/repos/${encodeURIComponent(fp)}/docs/raw?path=${encodeURIComponent(path)}`,
      {
        headers: {
          'X-Ralph-Token': sessionStorage.getItem('ralph.token') ?? '',
        },
      },
    );
    if (!r.ok) {
      throw new ApiError(r.status, await r.text());
    }
    const text = await r.text();
    contentByPath.value = { ...contentByPath.value, [path]: text };
  } catch (e) {
    contentError.value = e instanceof Error ? e.message : String(e);
  } finally {
    loadingContent.value = false;
  }
}

export function DocsRoute() {
  const { params } = useRoute();
  const loc = useLocation();
  const fp = params.fp;

  useEffect(() => {
    if (fp) void loadList(fp);
  }, [fp]);

  useEffect(() => {
    // Auto-select README.md or the first entry when the list lands and the
    // user hasn't already picked something else.
    if (!fp) return;
    if (selectedPath.value) return;
    const list = files.value;
    if (list.length === 0) return;
    const pick =
      list.find((f) => f.name.toLowerCase() === 'readme.md') ?? list[0];
    void loadContent(fp, pick.path);
  }, [fp, files.value.length]);

  if (!fp) return null;

  const list = files.value;
  const sel = selectedPath.value;
  const text = sel ? contentByPath.value[sel] : '';
  const html = text ? renderMarkdown(text) : '';

  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: '260px minmax(0, 1fr)',
        gap: 0,
        height: '100%',
        minHeight: 0,
      }}
    >
      <aside
        style={{
          borderRight: '1px solid var(--border)',
          overflowY: 'auto',
          padding: '16px 4px 32px',
          background: 'var(--bg-sunken)',
        }}
      >
        <div
          style={{
            padding: '0 12px 8px',
            fontSize: 11,
            textTransform: 'uppercase',
            letterSpacing: '0.08em',
            color: 'var(--fg-muted)',
            fontWeight: 600,
          }}
        >
          Docs
        </div>
        {loadingList.value && list.length === 0 && (
          <div
            style={{
              padding: '4px 12px',
              fontSize: 12,
              color: 'var(--fg-faint)',
            }}
          >
            Loading…
          </div>
        )}
        {listError.value && (
          <div
            style={{
              padding: '6px 12px',
              fontSize: 12,
              color: 'var(--err)',
            }}
          >
            {listError.value}
          </div>
        )}
        {list.length === 0 && !loadingList.value && !listError.value && (
          <div
            style={{
              padding: '6px 12px',
              fontSize: 12,
              color: 'var(--fg-faint)',
              fontStyle: 'italic',
            }}
          >
            No .md files in this repo.
          </div>
        )}
        <FileTree
          files={list}
          selected={sel}
          onSelect={(p) => {
            void loadContent(fp, p);
            // Mirror selection into the URL so browser history works. Using
            // replaceState keeps the back button meaningful (one Docs page,
            // not one entry per clicked file).
            const q = new URLSearchParams(window.location.search);
            q.set('file', p);
            const href = `${loc.path}?${q.toString()}`;
            window.history.replaceState(null, '', href);
          }}
        />
      </aside>

      <div
        style={{
          overflowY: 'auto',
          padding: '22px 28px 80px',
          minWidth: 0,
        }}
      >
        {!sel && list.length > 0 && (
          <div style={{ fontSize: 13, color: 'var(--fg-faint)' }}>
            Select a document from the list.
          </div>
        )}
        {sel && (
          <>
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 6,
                fontSize: 12,
                color: 'var(--fg-faint)',
                fontFamily: 'var(--font-mono)',
                marginBottom: 14,
                flexWrap: 'wrap',
              }}
            >
              <span>repo</span>
              <span style={{ color: 'var(--fg-ghost)' }}>/</span>
              <span>docs</span>
              <span style={{ color: 'var(--fg-ghost)' }}>/</span>
              <span style={{ color: 'var(--fg)' }}>{sel}</span>
            </div>
            {loadingContent.value && !text && (
              <div style={{ fontSize: 13, color: 'var(--fg-faint)' }}>
                Loading {sel}…
              </div>
            )}
            {contentError.value && (
              <div
                style={{
                  padding: '10px 12px',
                  marginBottom: 14,
                  borderRadius: 6,
                  background: 'var(--err-soft)',
                  border: '1px solid var(--err)',
                  color: 'var(--err)',
                  fontSize: 13,
                }}
              >
                {contentError.value}
              </div>
            )}
            {html && <MarkdownBody html={html} />}
          </>
        )}
      </div>
    </div>
  );
}

function FileTree({
  files,
  selected,
  onSelect,
}: {
  files: DocFile[];
  selected: string;
  onSelect: (path: string) => void;
}) {
  const groups = new Map<string, DocFile[]>();
  for (const f of files) {
    const slash = f.path.lastIndexOf('/');
    const dir = slash < 0 ? '' : f.path.slice(0, slash);
    const arr = groups.get(dir);
    if (arr) arr.push(f);
    else groups.set(dir, [f]);
  }
  const dirs = Array.from(groups.keys()).sort((a, b) => {
    if (a === '') return -1;
    if (b === '') return 1;
    return a.localeCompare(b);
  });
  return (
    <div style={{ display: 'flex', flexDirection: 'column' }}>
      {dirs.map((dir) => (
        <div key={dir} style={{ marginBottom: 6 }}>
          {dir && (
            <div
              style={{
                padding: '6px 12px 2px',
                fontSize: 10.5,
                color: 'var(--fg-faint)',
                textTransform: 'uppercase',
                letterSpacing: '0.06em',
                fontFamily: 'var(--font-mono)',
              }}
            >
              {dir}/
            </div>
          )}
          {groups.get(dir)!.map((f) => (
            <button
              key={f.path}
              type="button"
              onClick={() => onSelect(f.path)}
              style={{
                display: 'block',
                width: '100%',
                textAlign: 'left',
                padding: '5px 12px 5px 18px',
                fontSize: 12.5,
                color:
                  selected === f.path ? 'var(--accent-ink)' : 'var(--fg)',
                background:
                  selected === f.path ? 'var(--accent-soft)' : 'transparent',
                borderLeft:
                  selected === f.path
                    ? '2px solid var(--accent)'
                    : '2px solid transparent',
                cursor: 'pointer',
              }}
            >
              {f.name}
            </button>
          ))}
        </div>
      ))}
    </div>
  );
}

function MarkdownBody({ html }: { html: string }) {
  return (
    <div
      class="docs-prose"
      style={{ maxWidth: 820, color: 'var(--fg)', fontSize: 14, lineHeight: 1.65 }}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}

function renderMarkdown(text: string): string {
  try {
    return marked.parse(text, { async: false }) as string;
  } catch {
    return `<pre>${escapeHtml(text)}</pre>`;
  }
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}
