// Integration toggle + URL state for ntfy.sh + Tailscale.
//
// In the design prototype these lived in an edit-mode block. In
// production, enabled state persists to localStorage and URLs come from
// the daemon config (not yet wired — placeholders until a daemon
// /api/integrations endpoint lands).

import { signal } from '@preact/signals';

export interface Integration {
  enabled: boolean;
  label: string;
  desc: string;
  url: string;
}

const STORAGE_KEY = 'ralph.integrations';

const DEFAULTS: Record<string, Integration> = {
  ntfy: {
    enabled: false,
    label: 'ntfy.sh',
    desc:
      'Push notifications when Ralph finishes a story, gets stuck, or ' +
      'completes a run. Subscribe to the topic URL on any device.',
    url: 'https://ntfy.sh/ralph-not-configured',
  },
  tailscale: {
    enabled: false,
    label: 'Tailscale',
    desc:
      'Expose this viewer through your tailnet so other devices can ' +
      'open the same URL. Uses `tailscale serve` / `funnel` under the hood.',
    url: 'https://ralph.tail-not-configured.ts.net/',
  },
};

function readPersisted(): Record<string, Integration> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return DEFAULTS;
    const parsed = JSON.parse(raw) as Record<string, { enabled?: boolean }>;
    return {
      ntfy: { ...DEFAULTS.ntfy, enabled: !!parsed?.ntfy?.enabled },
      tailscale: {
        ...DEFAULTS.tailscale,
        enabled: !!parsed?.tailscale?.enabled,
      },
    };
  } catch {
    return DEFAULTS;
  }
}

export const integrations = signal<Record<string, Integration>>(readPersisted());

export function toggleIntegration(key: string) {
  const current = integrations.value;
  const integ = current[key];
  if (!integ) return;
  const next = {
    ...current,
    [key]: { ...integ, enabled: !integ.enabled },
  };
  integrations.value = next;
  try {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        ntfy: { enabled: next.ntfy.enabled },
        tailscale: { enabled: next.tailscale.enabled },
      }),
    );
  } catch {
    /* quota / disabled storage — fall through */
  }
}
