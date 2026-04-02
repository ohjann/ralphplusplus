package fusion

import (
	"context"
	"fmt"

	"github.com/ohjann/ralphplusplus/internal/debuglog"
	"github.com/ohjann/ralphplusplus/internal/judge"
	"github.com/ohjann/ralphplusplus/internal/prd"
	"github.com/ohjann/ralphplusplus/internal/worker"
)

// CompareResult holds the outcome of a fusion comparison.
type CompareResult struct {
	WinnerWorkerID worker.WorkerID
	WinnerChangeID string
	LoserWorkerIDs []worker.WorkerID
	LoserChangeIDs []string
	Reason         string
	Passed         bool
	Err            error
}

// DiffFunc retrieves the diff for a given worker. Returns the diff string and an error.
type DiffFunc func(workerID worker.WorkerID) (diff string, err error)

// RunCompare implements the shared fusion comparison logic used by both the
// daemon and TUI code paths. It determines a winner among the fusion group's
// results by comparing diffs via the judge.
func RunCompare(ctx context.Context, story *prd.UserStory, fg *FusionGroup, getDiff DiffFunc) CompareResult {
	if story == nil {
		return CompareResult{Err: fmt.Errorf("story not found")}
	}

	passing := fg.PassingResults()
	if len(passing) == 0 {
		return CompareResult{Passed: false}
	}

	// Single winner - no comparison needed
	if len(passing) == 1 {
		loserIDs, loserChangeIDs := collectLosers(fg, passing[0].WorkerID)
		return CompareResult{
			WinnerWorkerID: passing[0].WorkerID,
			WinnerChangeID: passing[0].ChangeID,
			LoserWorkerIDs: loserIDs,
			LoserChangeIDs: loserChangeIDs,
			Reason:         "only passing implementation",
			Passed:         true,
		}
	}

	// Multiple passed - get diffs and run comparison judge
	var candidates []judge.CompareCandidate
	for i, r := range passing {
		diff, err := getDiff(r.WorkerID)
		if err != nil {
			debuglog.Log("fusion: diff failed for worker %d: %v", r.WorkerID, err)
			continue
		}
		candidates = append(candidates, judge.CompareCandidate{
			Index:    i,
			ChangeID: r.ChangeID,
			Diff:     diff,
		})
	}

	if len(candidates) == 0 {
		return CompareResult{Err: fmt.Errorf("could not extract diffs from any candidate")}
	}

	result, err := judge.RunComparison(ctx, story, candidates)
	if err != nil {
		return CompareResult{Err: err}
	}

	if result.WinnerIndex < 0 || result.WinnerIndex >= len(passing) {
		return CompareResult{Err: fmt.Errorf("judge returned invalid winner index %d (have %d candidates)", result.WinnerIndex, len(passing))}
	}

	winnerPassing := passing[result.WinnerIndex]
	loserIDs, loserChangeIDs := collectLosers(fg, winnerPassing.WorkerID)
	return CompareResult{
		WinnerWorkerID: winnerPassing.WorkerID,
		WinnerChangeID: winnerPassing.ChangeID,
		LoserWorkerIDs: loserIDs,
		LoserChangeIDs: loserChangeIDs,
		Reason:         result.Reason,
		Passed:         true,
	}
}

// collectLosers returns all worker IDs and non-empty change IDs that are not the winner.
func collectLosers(fg *FusionGroup, winnerID worker.WorkerID) ([]worker.WorkerID, []string) {
	var loserIDs []worker.WorkerID
	var loserChangeIDs []string
	for _, r := range fg.Results {
		if r.WorkerID != winnerID {
			loserIDs = append(loserIDs, r.WorkerID)
			if r.ChangeID != "" {
				loserChangeIDs = append(loserChangeIDs, r.ChangeID)
			}
		}
	}
	return loserIDs, loserChangeIDs
}
