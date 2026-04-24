import { createPortal } from 'preact/compat';
import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import {
  integrations,
  refreshIntegrations,
  type Integration,
} from '../lib/integrations';

export function IntegrationsRow() {
  useEffect(() => {
    void refreshIntegrations();
  }, []);
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 2 }}>
      <IntegrationChip icon={<BellIcon />} integ={integrations.value.ntfy} />
      <IntegrationChip
        icon={<TailscaleIcon />}
        integ={integrations.value.tailscale}
      />
    </div>
  );
}

function IntegrationChip({
  icon,
  integ,
}: {
  icon: preact.ComponentChildren;
  integ: Integration;
}) {
  const [open, setOpen] = useState(false);
  const btnRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (btnRef.current && btnRef.current.contains(e.target as Node)) return;
      setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <>
      <button
        ref={btnRef}
        type="button"
        onClick={() => setOpen((o) => !o)}
        title={`${integ.label} · ${integ.enabled ? 'enabled' : 'disabled'}`}
        style={{
          width: 30,
          height: 26,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          borderRadius: 6,
          color: integ.enabled ? 'var(--fg)' : 'var(--fg-ghost)',
          background: open ? 'var(--bg-hover)' : 'transparent',
          position: 'relative',
          transition: 'background 80ms, color 120ms',
        }}
      >
        {icon}
        <span
          style={{
            position: 'absolute',
            right: 4,
            bottom: 4,
            width: 6,
            height: 6,
            borderRadius: 99,
            background: integ.enabled ? 'var(--ok)' : 'var(--fg-ghost)',
            border: '1.5px solid var(--bg-card)',
          }}
        />
      </button>

      {open && (
        <IntegrationPopover
          integ={integ}
          anchorRef={btnRef}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  );
}

function IntegrationPopover({
  integ,
  anchorRef,
  onClose,
}: {
  integ: Integration;
  anchorRef: { current: HTMLButtonElement | null };
  onClose: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const [pos, setPos] = useState({ top: 0, left: 0, arrowLeft: 14 });

  useLayoutEffect(() => {
    const anchor = anchorRef.current;
    if (!anchor) return;
    const compute = () => {
      const r = anchor.getBoundingClientRect();
      const popWidth = 290;
      const margin = 8;
      const gap = 8;
      let left = r.left + r.width / 2 - popWidth / 2;
      left = Math.max(
        margin,
        Math.min(left, window.innerWidth - popWidth - margin),
      );
      const top = r.bottom + gap;
      const arrowLeft = Math.max(
        10,
        Math.min(popWidth - 20, r.left + r.width / 2 - left - 4),
      );
      setPos({ top, left, arrowLeft });
    };
    compute();
    window.addEventListener('resize', compute);
    window.addEventListener('scroll', compute, true);
    return () => {
      window.removeEventListener('resize', compute);
      window.removeEventListener('scroll', compute, true);
    };
  }, [anchorRef]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(integ.url);
      setCopied(true);
      setTimeout(() => setCopied(false), 1600);
    } catch {
      /* clipboard denied — ignore silently */
    }
  };

  const node = (
    <div
      onMouseDown={(e) => e.stopPropagation()}
      style={{
        position: 'fixed',
        top: pos.top,
        left: pos.left,
        width: 290,
        padding: '11px 13px 10px',
        display: 'flex',
        flexDirection: 'column',
        gap: 8,
        background: 'var(--bg-card)',
        color: 'var(--fg)',
        border: '1px solid var(--border)',
        borderRadius: 8,
        boxShadow: 'var(--shadow-md)',
        zIndex: 10000,
        fontSize: 12,
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 8,
        }}
      >
        <span style={{ fontWeight: 600, fontSize: 12 }}>{integ.label}</span>
        <span
          style={{
            fontSize: 10,
            padding: '2px 8px',
            borderRadius: 99,
            border: `1px solid ${integ.enabled ? 'var(--ok)' : 'var(--border)'}`,
            background: integ.enabled ? 'var(--ok-soft)' : 'transparent',
            color: integ.enabled ? 'var(--ok)' : 'var(--fg-faint)',
            textTransform: 'uppercase',
            letterSpacing: '0.06em',
            fontWeight: 600,
          }}
        >
          {integ.enabled ? 'enabled' : 'disabled'}
        </span>
      </div>
      <div
        style={{
          fontSize: 11.5,
          color: 'var(--fg-faint)',
          lineHeight: 1.45,
        }}
      >
        {integ.desc}
      </div>
      {integ.enabled && integ.url ? (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 6,
            padding: '5px 5px 5px 9px',
            background: 'var(--bg-sunken)',
            border: '1px solid var(--border)',
            borderRadius: 6,
            minWidth: 0,
          }}
        >
          <a
            class="mono"
            href={integ.url}
            target="_blank"
            rel="noopener noreferrer"
            title={integ.url}
            style={{
              flex: 1,
              minWidth: 0,
              whiteSpace: 'nowrap',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              fontSize: 10.5,
              color: 'var(--fg)',
              userSelect: 'all',
              textDecoration: 'none',
            }}
          >
            {integ.url}
          </a>
          <button
            type="button"
            onClick={copy}
            title={copied ? 'Copied' : 'Copy URL'}
            style={{
              width: 24,
              height: 22,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              borderRadius: 4,
              color: 'var(--fg-muted)',
              background: 'var(--bg-elev)',
              border: '1px solid var(--border)',
              flexShrink: 0,
              fontSize: 10,
            }}
          >
            {copied ? '✓' : '⧉'}
          </button>
        </div>
      ) : (
        <div
          style={{
            fontSize: 11,
            color: 'var(--fg-faint)',
            padding: '7px 10px',
            background: 'var(--bg-sunken)',
            borderRadius: 6,
            border: '1px dashed var(--border)',
            lineHeight: 1.45,
          }}
        >
          {integ.hint || 'Not configured.'}
        </div>
      )}
      <div
        style={{
          position: 'absolute',
          top: -5,
          left: pos.arrowLeft,
          width: 9,
          height: 9,
          background: 'var(--bg-card)',
          borderLeft: '1px solid var(--border)',
          borderTop: '1px solid var(--border)',
          transform: 'rotate(45deg)',
        }}
      />
      <span style={{ display: 'none' }} aria-hidden onClick={onClose} />
    </div>
  );

  return createPortal(node, document.body);
}

function BellIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M8 2c-2.2 0-4 1.8-4 4v3.2l-1 2h10l-1-2V6c0-2.2-1.8-4-4-4Z" />
      <path d="M6.5 13a1.5 1.5 0 0 0 3 0" />
    </svg>
  );
}

function TailscaleIcon() {
  const dot = (op: number) => (
    <circle cx="0" cy="0" r="1.6" fill="currentColor" opacity={op} />
  );
  return (
    <svg width="14" height="14" viewBox="-7 -7 14 14">
      {[-4, 0, 4].map((y, yi) =>
        [-4, 0, 4].map((x, xi) => (
          <g transform={`translate(${x} ${y})`} key={`${xi}-${yi}`}>
            {dot(yi === 1 && xi === 1 ? 1 : 0.55 - Math.abs(yi - 1) * 0.15)}
          </g>
        )),
      )}
    </svg>
  );
}
