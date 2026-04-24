// Integration state for ntfy.sh + Tailscale.
//
// Server-authoritative: /api/integrations probes reality (tailnet listener
// up, RALPH_NOTIFY_TOPIC set) and the UI mirrors that. No local toggle —
// the chips reflect what's actually running.

import { signal } from '@preact/signals';
import { apiGet } from './api';

export interface Integration {
  enabled: boolean;
  label: string;
  desc: string;
  url: string;
  hint: string;
}

interface IntegrationStatusDTO {
  label: string;
  desc: string;
  enabled: boolean;
  url?: string;
  hint?: string;
}

interface IntegrationsResponse {
  tailscale: IntegrationStatusDTO;
  ntfy: IntegrationStatusDTO;
}

function fromDTO(d: IntegrationStatusDTO): Integration {
  return {
    enabled: !!d.enabled,
    label: d.label,
    desc: d.desc,
    url: d.url ?? '',
    hint: d.hint ?? '',
  };
}

// Shown before the first fetch completes and as fallback if the request
// fails. Disabled + empty URLs avoid misleading users.
const PLACEHOLDER: Record<string, Integration> = {
  ntfy: {
    enabled: false,
    label: 'ntfy.sh',
    desc: 'Push notifications when Ralph finishes, gets stuck, or needs input.',
    url: '',
    hint: 'Loading…',
  },
  tailscale: {
    enabled: false,
    label: 'Tailscale',
    desc: 'Peers on your tailnet can open this viewer without a token.',
    url: '',
    hint: 'Loading…',
  },
};

export const integrations = signal<Record<string, Integration>>(PLACEHOLDER);

export async function refreshIntegrations(): Promise<void> {
  try {
    const r = await apiGet<IntegrationsResponse>('/api/integrations');
    integrations.value = {
      ntfy: fromDTO(r.ntfy),
      tailscale: fromDTO(r.tailscale),
    };
  } catch {
    // Leave placeholder in place — a stale disabled state is better than
    // a confusing partial update.
  }
}
