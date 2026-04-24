// Package viewer DTOs — wire shapes for the /api/** endpoints.
//
// These types are the contract between the Go server and the Preact SPA.
// They deliberately flatten or embed internal types (history.Manifest,
// costs.RunSummary) so the frontend never has to know the on-disk layout.
package viewer

import (
	"encoding/json"
	"time"

	"github.com/ohjann/ralphplusplus/internal/config"
	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/history"
)

// Bootstrap is the shape returned by GET /api/bootstrap. Token is echoed so
// the SPA can store it in memory once parsed from the initial URL query;
// FeatureFlags is reserved for future toggles.
type Bootstrap struct {
	Version      string   `json:"version"`
	FeatureFlags []string `json:"featureFlags"`
	Token        string   `json:"token"`
}

// RepoSummary is one row of GET /api/repos, sorted by LastSeen desc.
type RepoSummary struct {
	FP       string    `json:"fp"`
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	LastSeen time.Time `json:"lastSeen"`
	RunCount int       `json:"runCount"`
}

// AggCosts aggregates costs.RunSummary across every run-history entry for a
// repo. Used only inside RepoDetail.
type AggCosts struct {
	Runs             int     `json:"runs"`
	TotalCost        float64 `json:"totalCost"`
	DurationMinutes  float64 `json:"durationMinutes"`
	TotalIterations  int     `json:"totalIterations"`
	StoriesTotal     int     `json:"storiesTotal"`
	StoriesCompleted int     `json:"storiesCompleted"`
	StoriesFailed    int     `json:"storiesFailed"`
}

// RepoDetail is GET /api/repos/:fp — stable identity plus aggregated cost.
type RepoDetail struct {
	Meta     history.RepoMeta `json:"meta"`
	AggCosts AggCosts         `json:"aggCosts"`
}

// RunListItem is one row of GET /api/repos/:fp/runs. Manifest fields carry
// tokens/iterations; the costs.* fields carry dollar cost and wall-clock
// duration. Cost fields are pointers because a running (or crashed-pre-
// Finalize) manifest has no matching RunSummary yet. The list is flat; the
// UI groups by Kind client-side.
type RunListItem struct {
	RunID       string     `json:"runId"`
	DisplayName string     `json:"displayName,omitempty"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	StartTime   time.Time  `json:"startTime"`
	EndTime    *time.Time `json:"endTime,omitempty"`
	GitBranch  string     `json:"gitBranch,omitempty"`
	GitHeadSHA string     `json:"gitHeadSha,omitempty"`
	// from manifest Totals
	Iterations   int `json:"iterations"`
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	// from costs.RunSummary (nil when no matching entry exists)
	TotalCost       *float64 `json:"totalCost,omitempty"`
	DurationMinutes *float64 `json:"durationMinutes,omitempty"`
	FirstPassRate   *float64 `json:"firstPassRate,omitempty"`
	ModelsUsed      []string `json:"modelsUsed,omitempty"`
}

// RunDetail is GET /api/repos/:fp/runs/:id. Summary is nil when no matching
// costs.RunSummary exists (e.g. the run is still running).
type RunDetail struct {
	Manifest history.Manifest  `json:"manifest"`
	Summary  *costs.RunSummary `json:"summary,omitempty"`
}

// GlobalStatsResponse is GET /api/stats/global — rollups across every repo
// Ralph has run against on this host. Callers render the top-line numbers,
// the activity sparkline, and the per-repo breakdown on Home.
type GlobalStatsResponse struct {
	Totals        GlobalStatsTotals  `json:"totals"`
	RunsByKind    map[string]int     `json:"runsByKind"`
	ActivityByDay []ActivityPoint    `json:"activityByDay"`
	ByRepo        []RepoStatsSummary `json:"byRepo"`
}

// GlobalStatsTotals aggregates numeric fields across all repos. Cost,
// iteration, and story totals sum directly; FirstPassRate is the
// stories-weighted average across runs (not simply mean of means).
type GlobalStatsTotals struct {
	Repos            int     `json:"repos"`
	Runs             int     `json:"runs"`
	TotalCost        float64 `json:"totalCost"`
	DurationMinutes  float64 `json:"durationMinutes"`
	TotalIterations  int     `json:"totalIterations"`
	StoriesTotal     int     `json:"storiesTotal"`
	StoriesCompleted int     `json:"storiesCompleted"`
	StoriesFailed    int     `json:"storiesFailed"`
	FirstPassRate    float64 `json:"firstPassRate"`
}

// ActivityPoint is one day's rollup in the Home sparkline. Date is
// YYYY-MM-DD in the server's local timezone.
type ActivityPoint struct {
	Date string  `json:"date"`
	Runs int     `json:"runs"`
	Cost float64 `json:"cost"`
}

// RepoStatsSummary is the compact per-repo card shown on Home.
type RepoStatsSummary struct {
	FP               string    `json:"fp"`
	Name             string    `json:"name"`
	Path             string    `json:"path"`
	Runs             int       `json:"runs"`
	TotalCost        float64   `json:"totalCost"`
	StoriesCompleted int       `json:"storiesCompleted"`
	StoriesFailed    int       `json:"storiesFailed"`
	LastSeen         time.Time `json:"lastSeen"`
}

// IntegrationStatus reflects one optional external integration. URL is the
// canonical address the user can open when Enabled is true; Hint is a short
// instruction shown when Enabled is false.
type IntegrationStatus struct {
	Label   string `json:"label"`
	Desc    string `json:"desc"`
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

// IntegrationsResponse is GET /api/integrations. Probes are cheap and run
// per-request; UI refreshes at mount time and after known state transitions.
type IntegrationsResponse struct {
	Tailscale IntegrationStatus `json:"tailscale"`
	Ntfy      IntegrationStatus `json:"ntfy"`
}

// SettingsResponse is GET /api/live/:fp/settings. When the daemon socket is
// reachable, Source is "daemon" and State holds the daemon's /api/state
// snapshot verbatim. When unreachable, Source is "file" and Config holds the
// parsed contents of <RepoMeta.Path>/.ralph/config.toml as a generic map so
// the UI can render any subset of fields without a Go-side schema bump.
// Exactly one of State or Config is populated per response.
type SettingsResponse struct {
	Source string                 `json:"source"`
	State  json.RawMessage        `json:"state,omitempty"`
	Config map[string]interface{} `json:"config,omitempty"`
}

// SettingsUpdateRequest is the body of POST /api/live/:fp/settings. It is a
// type alias for config.TomlConfig so the wire schema and field names always
// match the canonical config struct — adding a tunable to TomlConfig
// automatically extends the editor's accepted payload.
type SettingsUpdateRequest = config.TomlConfig

// SettingsUpdateResponse is the body of a successful POST /api/live/:fp/settings.
// Source distinguishes whether the write hit the live daemon or fell back to
// the on-disk config.toml; Applied is the list of TOML tag names that were
// changed (empty means the request had no non-nil fields).
type SettingsUpdateResponse struct {
	Source  string   `json:"source"`
	Applied []string `json:"applied"`
}

// SettingsValidationError is the body of a 400 validation_failed response from
// POST /api/live/:fp/settings. Fields is a TOML-tag-keyed map of error messages.
type SettingsValidationError struct {
	Error  string            `json:"error"`
	Fields map[string]string `json:"fields"`
}

// RepoMetaResponse is GET /api/repos/:fp/meta. Bundles the on-disk RepoMeta
// with aggregate cost stats and a per-Kind run count breakdown.
type RepoMetaResponse struct {
	Meta            history.RepoMeta `json:"meta"`
	AggCosts        AggCosts         `json:"aggCosts"`
	RunCountsByKind map[string]int   `json:"runCountsByKind"`
}

// PRDResponse is GET /api/repos/:fp/prd. Hash is the sha256-hex of the
// on-disk prd.json; Content is the parsed JSON body. MatchesRunSnapshot
// is set only when a run_id query param is provided and the referenced
// manifest carries a PRDSnapshot — then it reports whether the current
// file hash equals that snapshot. Omitted otherwise so the UI can tell
// "not compared" from "compared and differs".
type PRDResponse struct {
	Hash                string          `json:"hash"`
	Content             json.RawMessage `json:"content"`
	MatchesRunSnapshot  *bool           `json:"matchesRunSnapshot,omitempty"`
}
