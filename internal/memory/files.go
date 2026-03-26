package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	learningsFile    = "learnings.md"
	prdLearningsFile = "prd-learnings.md"
)

// memoryDir returns the path to {ralphHome}/memory/.
func memoryDir(ralphHome string) string {
	return filepath.Join(ralphHome, "memory")
}

// ReadLearnings returns the contents of {ralphHome}/memory/learnings.md.
// Returns empty string (not error) if the file doesn't exist yet.
func ReadLearnings(ralphHome string) (string, error) {
	return readMemoryFile(ralphHome, learningsFile)
}

// ReadPRDLearnings returns the contents of {ralphHome}/memory/prd-learnings.md.
// Returns empty string (not error) if the file doesn't exist yet.
func ReadPRDLearnings(ralphHome string) (string, error) {
	return readMemoryFile(ralphHome, prdLearningsFile)
}

// AppendLearning appends a LearningEntry to {ralphHome}/memory/learnings.md.
// Creates the memory/ directory if it doesn't exist.
func AppendLearning(ralphHome string, entry LearningEntry) error {
	return appendEntry(ralphHome, learningsFile, entry)
}

// AppendPRDLearning appends a LearningEntry to {ralphHome}/memory/prd-learnings.md.
// Creates the memory/ directory if it doesn't exist.
func AppendPRDLearning(ralphHome string, entry LearningEntry) error {
	return appendEntry(ralphHome, prdLearningsFile, entry)
}

func readMemoryFile(ralphHome, filename string) (string, error) {
	path := filepath.Join(memoryDir(ralphHome), filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func appendEntry(ralphHome, filename string, entry LearningEntry) error {
	dir := memoryDir(ralphHome)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	formatted := formatEntry(entry)

	f, err := os.OpenFile(filepath.Join(dir, filename), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(formatted)
	return err
}

// MemoryFileInfo holds summary info about a memory file.
type MemoryFileInfo struct {
	Name       string
	Exists     bool
	SizeBytes  int64
	EntryCount int
}

// MemoryStats returns summary info about memory files in {ralphHome}/memory/.
func MemoryStats(ralphHome string) []MemoryFileInfo {
	files := []string{learningsFile, prdLearningsFile}
	var stats []MemoryFileInfo
	for _, f := range files {
		path := filepath.Join(memoryDir(ralphHome), f)
		info := MemoryFileInfo{Name: f}
		fi, err := os.Stat(path)
		if err == nil {
			info.Exists = true
			info.SizeBytes = fi.Size()
			// Count entries by counting "### " prefixes
			if data, err := os.ReadFile(path); err == nil {
				info.EntryCount = strings.Count(string(data), "\n### ") + countLeadingEntry(data)
			}
		}
		stats = append(stats, info)
	}
	return stats
}

// countLeadingEntry returns 1 if data starts with "### ", else 0.
func countLeadingEntry(data []byte) int {
	if len(data) >= 4 && string(data[:4]) == "### " {
		return 1
	}
	return 0
}

func formatEntry(entry LearningEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n", entry.ID)
	fmt.Fprintf(&b, "- **Run:** %s\n", entry.Run)
	fmt.Fprintf(&b, "- **Stories:** %s\n", strings.Join(entry.Stories, ", "))
	fmt.Fprintf(&b, "- **Confirmed:** %d times\n", entry.Confirmed)
	fmt.Fprintf(&b, "- **Category:** %s\n", entry.Category)
	fmt.Fprintf(&b, "\n%s\n\n", entry.Content)
	return b.String()
}
