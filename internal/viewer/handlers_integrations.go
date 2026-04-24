package viewer

import (
	"net/http"
	"os"
	"strings"
)

// handleIntegrations reports the current state of optional external
// integrations so the UI can reflect reality instead of carrying local
// toggle state. Each probe is self-contained and cheap (no network).
//
//   - tailscale: enabled when the viewer was launched with --tailscale and
//     tsnet bound a listener; TailscaleURL is set at startup in that case.
//   - ntfy: enabled when RALPH_NOTIFY_TOPIC is set (or --notify-topic is
//     wired through env). Server URL honours RALPH_NTFY_SERVER, defaulting
//     to https://ntfy.sh so the URL is always a valid deep-link.
func (s *Server) handleIntegrations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, IntegrationsResponse{
		Tailscale: probeTailscale(s.TailscaleURL),
		Ntfy:      probeNtfy(),
	})
}

func probeTailscale(url string) IntegrationStatus {
	out := IntegrationStatus{
		Label: "Tailscale",
		Desc:  "Peers on your tailnet can open this viewer without a token.",
	}
	if url == "" {
		out.Hint = "Restart the viewer with --tailscale to expose it on your tailnet."
		return out
	}
	out.Enabled = true
	out.URL = url
	return out
}

func probeNtfy() IntegrationStatus {
	out := IntegrationStatus{
		Label: "ntfy.sh",
		Desc:  "Push notifications when Ralph finishes, gets stuck, or needs input.",
	}
	topic := strings.TrimSpace(os.Getenv("RALPH_NOTIFY_TOPIC"))
	if topic == "" {
		out.Hint = "Set RALPH_NOTIFY_TOPIC (or pass --notify --notify-topic) to enable pushes."
		return out
	}
	server := strings.TrimRight(strings.TrimSpace(os.Getenv("RALPH_NTFY_SERVER")), "/")
	if server == "" {
		server = "https://ntfy.sh"
	}
	out.Enabled = true
	out.URL = server + "/" + topic
	return out
}
