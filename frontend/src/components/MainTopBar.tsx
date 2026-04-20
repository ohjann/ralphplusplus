import { useLocation } from 'preact-iso';
import { IntegrationsRow } from './IntegrationsRow';

// Sticky 40px top bar — breadcrumb-style route label on the left,
// integration chips (ntfy + Tailscale) on the right. Mirrors the handoff.
export function MainTopBar() {
  const loc = useLocation();
  const label = deriveLabel(loc.path);
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 12,
        padding: '8px 20px 8px 28px',
        borderBottom: '1px solid var(--border-soft)',
        background: 'var(--bg-card)',
        position: 'sticky',
        top: 0,
        zIndex: 20,
        height: 40,
        boxSizing: 'border-box',
      }}
    >
      <div
        class="mono"
        style={{
          fontSize: 11,
          color: 'var(--fg-ghost)',
          letterSpacing: '0.04em',
        }}
      >
        {label}
      </div>
      <div style={{ flex: 1 }} />
      <IntegrationsRow />
    </div>
  );
}

function deriveLabel(path: string): string {
  if (path === '/' || path === '') return 'home';
  const iter = /^\/repos\/([^/]+)\/runs\/([^/]+)\/iter\/([^/]+)\/([^/]+)/.exec(
    path,
  );
  if (iter) return `${iter[1].slice(0, 8)} · transcript`;
  const run = /^\/repos\/([^/]+)\/runs\/([^/]+)/.exec(path);
  if (run) return `${run[1].slice(0, 8)} · ${run[2].slice(0, 8)}`;
  const settings = /^\/repos\/([^/]+)\/settings/.exec(path);
  if (settings) return `${settings[1].slice(0, 8)} · settings`;
  const meta = /^\/repos\/([^/]+)\/meta/.exec(path);
  if (meta) return `${meta[1].slice(0, 8)} · meta`;
  return path;
}
