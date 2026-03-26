package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadLearningsFileNotExist(t *testing.T) {
	dir := t.TempDir()
	content, err := ReadLearnings(dir)
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if content != "" {
		t.Errorf("expected empty string, got %q", content)
	}
}

func TestReadPRDLearningsFileNotExist(t *testing.T) {
	dir := t.TempDir()
	content, err := ReadPRDLearnings(dir)
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if content != "" {
		t.Errorf("expected empty string, got %q", content)
	}
}

func TestAppendAndReadLearningRoundTrip(t *testing.T) {
	dir := t.TempDir()

	entry := LearningEntry{
		ID:        "lesson-2026-03-26-auth-retry",
		Run:       "feature-auth (2026-03-26)",
		Stories:   []string{"P55-003", "P55-007"},
		Confirmed: 2,
		Category:  "testing",
		Content:   "When auth tokens expire mid-request, retry with backoff.",
	}

	if err := AppendLearning(dir, entry); err != nil {
		t.Fatalf("AppendLearning: %v", err)
	}

	content, err := ReadLearnings(dir)
	if err != nil {
		t.Fatalf("ReadLearnings: %v", err)
	}

	if !strings.Contains(content, "### lesson-2026-03-26-auth-retry") {
		t.Error("missing entry header")
	}
	if !strings.Contains(content, "- **Run:** feature-auth (2026-03-26)") {
		t.Error("missing Run metadata")
	}
	if !strings.Contains(content, "- **Stories:** P55-003, P55-007") {
		t.Error("missing Stories metadata")
	}
	if !strings.Contains(content, "- **Confirmed:** 2 times") {
		t.Error("missing Confirmed metadata")
	}
	if !strings.Contains(content, "- **Category:** testing") {
		t.Error("missing Category metadata")
	}
	if !strings.Contains(content, "When auth tokens expire mid-request, retry with backoff.") {
		t.Error("missing content body")
	}
}

func TestAppendAndReadPRDLearningRoundTrip(t *testing.T) {
	dir := t.TempDir()

	entry := LearningEntry{
		ID:        "prd-lesson-001",
		Run:       "run-01",
		Stories:   []string{"P10-001"},
		Confirmed: 1,
		Category:  "sizing",
		Content:   "Stories over 5 subtasks should be split.",
	}

	if err := AppendPRDLearning(dir, entry); err != nil {
		t.Fatalf("AppendPRDLearning: %v", err)
	}

	content, err := ReadPRDLearnings(dir)
	if err != nil {
		t.Fatalf("ReadPRDLearnings: %v", err)
	}

	if !strings.Contains(content, "### prd-lesson-001") {
		t.Error("missing entry header")
	}
	if !strings.Contains(content, "Stories over 5 subtasks should be split.") {
		t.Error("missing content body")
	}
}

func TestMultipleAppends(t *testing.T) {
	dir := t.TempDir()

	entries := []LearningEntry{
		{ID: "L-001", Run: "run-01", Stories: []string{"S-001"}, Confirmed: 1, Category: "testing", Content: "First lesson."},
		{ID: "L-002", Run: "run-02", Stories: []string{"S-002", "S-003"}, Confirmed: 3, Category: "architecture", Content: "Second lesson."},
	}

	for _, e := range entries {
		if err := AppendLearning(dir, e); err != nil {
			t.Fatalf("AppendLearning(%s): %v", e.ID, err)
		}
	}

	content, err := ReadLearnings(dir)
	if err != nil {
		t.Fatalf("ReadLearnings: %v", err)
	}

	if !strings.Contains(content, "### L-001") {
		t.Error("missing first entry")
	}
	if !strings.Contains(content, "### L-002") {
		t.Error("missing second entry")
	}
	if !strings.Contains(content, "First lesson.") {
		t.Error("missing first content")
	}
	if !strings.Contains(content, "Second lesson.") {
		t.Error("missing second content")
	}
}

func TestMemoryDirectoryCreated(t *testing.T) {
	dir := t.TempDir()

	entry := LearningEntry{ID: "L-001", Run: "run-01", Stories: []string{"S-001"}, Confirmed: 1, Category: "testing", Content: "Test."}
	if err := AppendLearning(dir, entry); err != nil {
		t.Fatalf("AppendLearning: %v", err)
	}

	memDir := filepath.Join(dir, "memory")
	info, err := os.Stat(memDir)
	if err != nil {
		t.Fatalf("memory directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("memory path is not a directory")
	}
}
