package archive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eoghanhynes/ralph/internal/prd"
)

// CheckAndArchive detects a branch change and archives the previous run's prd.json and progress.txt.
// Returns true if an archive was created.
func CheckAndArchive(projectDir, lastBranchFile, archiveDir, prdFile, progressFile string) (bool, error) {
	lastBranch, err := os.ReadFile(lastBranchFile)
	if err != nil {
		// No last branch file — nothing to archive
		return false, nil
	}

	p, err := prd.Load(prdFile)
	if err != nil {
		return false, nil
	}

	currentBranch := p.BranchName
	last := strings.TrimSpace(string(lastBranch))

	if currentBranch == "" || last == "" || currentBranch == last {
		return false, nil
	}

	// Branch changed — archive
	date := time.Now().Format("2006-01-02")
	folderName := strings.TrimPrefix(last, "ralph/")
	archiveFolder := filepath.Join(archiveDir, date+"-"+folderName)

	if err := os.MkdirAll(archiveFolder, 0o755); err != nil {
		return false, fmt.Errorf("creating archive folder: %w", err)
	}

	// Copy prd.json
	if data, err := os.ReadFile(prdFile); err == nil {
		_ = os.WriteFile(filepath.Join(archiveFolder, "prd.json"), data, 0o644)
	}
	// Copy progress.txt
	if data, err := os.ReadFile(progressFile); err == nil {
		_ = os.WriteFile(filepath.Join(archiveFolder, "progress.txt"), data, 0o644)
	}

	// Reset progress file
	initProgress(progressFile)

	return true, nil
}

// TrackBranch writes the current branch name to the last-branch file.
func TrackBranch(prdFile, lastBranchFile string) error {
	p, err := prd.Load(prdFile)
	if err != nil {
		return nil
	}
	if p.BranchName == "" {
		return nil
	}
	return os.WriteFile(lastBranchFile, []byte(p.BranchName), 0o644)
}

// EnsureProgressFile creates progress.txt if it doesn't exist.
func EnsureProgressFile(progressFile string) error {
	if _, err := os.Stat(progressFile); err == nil {
		return nil
	}
	initProgress(progressFile)
	return nil
}

func initProgress(path string) {
	content := fmt.Sprintf("# Ralph Progress Log\nStarted: %s\n---\n", time.Now().Format(time.RFC1123))
	_ = os.WriteFile(path, []byte(content), 0o644)
}
