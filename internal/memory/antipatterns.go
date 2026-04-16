package memory

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/ohjann/ralphplusplus/internal/costs"
	"github.com/ohjann/ralphplusplus/internal/events"
	"github.com/ohjann/ralphplusplus/internal/storystate"
)

// Detection thresholds. Tuned so a fresh project with one bad run doesn't
// trip every category — we want signal that's stood up across multiple stories.
const (
	fragileFileMinOccurrences      = 3
	frictionModuleMinStories       = 3
	frictionModuleAvgRatio         = 1.5 // module avg iterations vs global avg
	rejectionClusterMinStories     = 3
	rejectionSummaryClusterPrefix  = 80
)

// DetectAntiPatterns scans run history, story state, and events to surface
// recurring failure patterns. Returns an empty slice (no error) when there's
// no history yet.
func DetectAntiPatterns(projectDir string) ([]AntiPattern, error) {
	history, err := costs.LoadHistory(projectDir)
	if err != nil {
		return nil, err
	}

	var patterns []AntiPattern

	if fragile := detectFragileFiles(projectDir, history); len(fragile) > 0 {
		patterns = append(patterns, fragile...)
	}
	if friction := detectHighFrictionModules(projectDir, history); len(friction) > 0 {
		patterns = append(patterns, friction...)
	}

	evts, err := events.Load(projectDir)
	if err == nil {
		if rejections := detectRecurringRejections(evts); len(rejections) > 0 {
			patterns = append(patterns, rejections...)
		}
	}

	return patterns, nil
}

// detectFragileFiles flags files associated with ≥N failed/stuck stories
// across the run history. Reads files_touched from each failed story's
// current state.json (best-effort: re-runs overwrite state).
func detectFragileFiles(projectDir string, history costs.RunHistory) []AntiPattern {
	type fileHit struct {
		count   int
		stories map[string]bool
	}
	hits := make(map[string]*fileHit)

	for _, run := range history.Runs {
		for _, s := range run.StoryDetails {
			if s.Passed {
				continue
			}
			state, err := storystate.Load(projectDir, s.StoryID)
			if err != nil {
				continue
			}
			for _, f := range state.FilesTouched {
				h := hits[f]
				if h == nil {
					h = &fileHit{stories: make(map[string]bool)}
					hits[f] = h
				}
				h.count++
				h.stories[s.StoryID] = true
			}
		}
	}

	var out []AntiPattern
	for file, h := range hits {
		if h.count < fragileFileMinOccurrences {
			continue
		}
		stories := mapKeys(h.stories)
		out = append(out, AntiPattern{
			Category:        "fragile_file",
			Description:     "File repeatedly touched by failed or stuck stories",
			FilesAffected:   []string{file},
			OccurrenceCount: h.count,
			AffectedStories: stories,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OccurrenceCount > out[j].OccurrenceCount })
	return out
}

// detectHighFrictionModules flags directories whose stories average
// significantly more iterations than the run-wide baseline.
func detectHighFrictionModules(projectDir string, history costs.RunHistory) []AntiPattern {
	type moduleStats struct {
		iterations int
		stories    map[string]bool
	}
	mods := make(map[string]*moduleStats)
	var globalIter, globalCount int

	for _, run := range history.Runs {
		for _, s := range run.StoryDetails {
			if s.Iterations <= 0 {
				continue
			}
			globalIter += s.Iterations
			globalCount++

			state, err := storystate.Load(projectDir, s.StoryID)
			if err != nil {
				continue
			}
			seenDirs := make(map[string]bool)
			for _, f := range state.FilesTouched {
				dir := moduleKey(f)
				if dir == "" || seenDirs[dir] {
					continue
				}
				seenDirs[dir] = true
				m := mods[dir]
				if m == nil {
					m = &moduleStats{stories: make(map[string]bool)}
					mods[dir] = m
				}
				m.iterations += s.Iterations
				m.stories[s.StoryID] = true
			}
		}
	}

	if globalCount == 0 {
		return nil
	}
	globalAvg := float64(globalIter) / float64(globalCount)
	threshold := globalAvg * frictionModuleAvgRatio

	var out []AntiPattern
	for mod, m := range mods {
		if len(m.stories) < frictionModuleMinStories {
			continue
		}
		modAvg := float64(m.iterations) / float64(len(m.stories))
		if modAvg < threshold {
			continue
		}
		out = append(out, AntiPattern{
			Category:        "high_friction_module",
			Description:     "Stories touching this module average more iterations than baseline",
			FilesAffected:   []string{mod + "/"},
			OccurrenceCount: len(m.stories),
			AffectedStories: mapKeys(m.stories),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OccurrenceCount > out[j].OccurrenceCount })
	return out
}

// detectRecurringRejections clusters judge-fail events by the first N chars
// of their summary and flags clusters that span ≥N distinct stories.
func detectRecurringRejections(evts []events.Event) []AntiPattern {
	type cluster struct {
		count   int
		stories map[string]bool
		sample  string
	}
	clusters := make(map[string]*cluster)

	for _, e := range evts {
		if e.Type != events.EventJudgeResult || e.Meta["verdict"] != "fail" {
			continue
		}
		summary := strings.TrimPrefix(e.Summary, "Judge failed: ")
		summary = strings.TrimSpace(summary)
		if summary == "" {
			continue
		}
		key := summary
		if len(key) > rejectionSummaryClusterPrefix {
			key = key[:rejectionSummaryClusterPrefix]
		}
		key = strings.ToLower(key)

		c := clusters[key]
		if c == nil {
			c = &cluster{stories: make(map[string]bool), sample: summary}
			clusters[key] = c
		}
		c.count++
		if e.StoryID != "" {
			c.stories[e.StoryID] = true
		}
	}

	var out []AntiPattern
	for _, c := range clusters {
		if len(c.stories) < rejectionClusterMinStories {
			continue
		}
		out = append(out, AntiPattern{
			Category:        "recurring_judge_rejection",
			Description:     "Judge rejection recurring across stories: " + c.sample,
			OccurrenceCount: c.count,
			AffectedStories: mapKeys(c.stories),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OccurrenceCount > out[j].OccurrenceCount })
	return out
}

// moduleKey returns the first two path segments of a file path, e.g.
// "internal/runner/foo.go" → "internal/runner". Returns "" if the path
// has no directory component.
func moduleKey(path string) string {
	dir := filepath.Dir(path)
	if dir == "." || dir == "/" || dir == "" {
		return ""
	}
	parts := strings.Split(dir, string(filepath.Separator))
	if len(parts) >= 2 {
		return filepath.Join(parts[0], parts[1])
	}
	return parts[0]
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
