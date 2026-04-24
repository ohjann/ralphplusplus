package viewer

import (
	"net/http"
	"sort"
	"time"

	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/history"
)

// globalStatsActivityDays bounds the sparkline — 30 days is dense enough
// to read trends without overwhelming the UI.
const globalStatsActivityDays = 30

// handleGlobalStats serves GET /api/stats/global. Aggregates every repo's
// run-history into the shape the Home route needs: totals, kind
// breakdown, day-by-day activity, and a per-repo card list.
func (s *Server) handleGlobalStats(w http.ResponseWriter, r *http.Request) {
	repos, err := s.Index.Get(r.Context())
	if err != nil {
		http.Error(w, "load repos: "+err.Error(), http.StatusInternalServerError)
		return
	}

	totals := GlobalStatsTotals{Repos: len(repos)}
	runsByKind := make(map[string]int)
	byDay := make(map[string]*ActivityPoint)
	byRepo := make([]RepoStatsSummary, 0, len(repos))

	// Weighted first-pass numerator: sum(FirstPassRate * StoriesTotal).
	// Divide by totals.StoriesTotal at the end to get the correct average.
	var firstPassWeighted float64

	// Prime the sparkline with zero-days so gaps render as empty bars.
	today := time.Now().Local()
	for i := globalStatsActivityDays - 1; i >= 0; i-- {
		d := today.AddDate(0, 0, -i).Format("2006-01-02")
		byDay[d] = &ActivityPoint{Date: d}
	}
	cutoff := today.AddDate(0, 0, -(globalStatsActivityDays - 1))
	cutoffDate := time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 0, 0, 0, 0, cutoff.Location())

	for _, rp := range repos {
		h, err := costs.LoadHistory(rp.FP)
		if err != nil {
			continue
		}
		rs := RepoStatsSummary{
			FP:       rp.FP,
			Name:     rp.Meta.Name,
			Path:     rp.Meta.Path,
			LastSeen: rp.Meta.LastSeen,
		}
		for _, run := range h.Runs {
			totals.Runs++
			totals.TotalCost += run.TotalCost
			totals.DurationMinutes += run.DurationMinutes
			totals.TotalIterations += run.TotalIterations
			totals.StoriesTotal += run.StoriesTotal
			totals.StoriesCompleted += run.StoriesCompleted
			totals.StoriesFailed += run.StoriesFailed
			firstPassWeighted += run.FirstPassRate * float64(run.StoriesTotal)

			rs.Runs++
			rs.TotalCost += run.TotalCost
			rs.StoriesCompleted += run.StoriesCompleted
			rs.StoriesFailed += run.StoriesFailed

			kind := run.Kind
			if kind == "" {
				kind = "daemon"
			}
			runsByKind[kind]++

			if t := parseRunDate(run.Date); !t.IsZero() && !t.Before(cutoffDate) {
				key := t.Format("2006-01-02")
				if p, ok := byDay[key]; ok {
					p.Runs++
					p.Cost += run.TotalCost
				}
			}
		}
		byRepo = append(byRepo, rs)
	}

	if totals.StoriesTotal > 0 {
		totals.FirstPassRate = firstPassWeighted / float64(totals.StoriesTotal)
	}

	activity := make([]ActivityPoint, 0, len(byDay))
	for _, p := range byDay {
		activity = append(activity, *p)
	}
	sort.Slice(activity, func(i, j int) bool { return activity[i].Date < activity[j].Date })

	sort.Slice(byRepo, func(i, j int) bool {
		if byRepo[i].Runs != byRepo[j].Runs {
			return byRepo[i].Runs > byRepo[j].Runs
		}
		return byRepo[i].LastSeen.After(byRepo[j].LastSeen)
	})

	writeJSON(w, http.StatusOK, GlobalStatsResponse{
		Totals:        totals,
		RunsByKind:    runsByKind,
		ActivityByDay: activity,
		ByRepo:        byRepo,
	})
}

// parseRunDate is lenient: RunSummary.Date is a best-effort timestamp string
// written in various formats over time. Tries RFC3339 first, falls back to
// the common short form. Returns the zero time if nothing parses so callers
// can skip gracefully.
func parseRunDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.Local()
		}
	}
	return time.Time{}
}

// Silence the history import check — history is intentionally imported by
// other handlers in this package but not directly referenced here.
var _ = history.StatusRunning
