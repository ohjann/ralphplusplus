package retro

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseImprovementsFromActivity(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantCount   int
		wantParsed  bool
	}{
		{
			name: "valid improvements",
			content: `Some activity output...
<improvements>
[
  {
    "title": "Add idle timeout",
    "category": "resilience",
    "severity": "high",
    "description": "Daemon runs forever",
    "rationale": "Memory leak risk",
    "acceptance_criteria": ["Timeout after 5m idle"],
    "affected_files": ["daemon.go"]
  },
  {
    "title": "Add CLI client mode",
    "category": "dx",
    "severity": "medium",
    "description": "No headless control",
    "rationale": "Can't script ralph",
    "acceptance_criteria": ["ralph status works"],
    "affected_files": ["main.go"]
  }
]
</improvements>
More output...`,
			wantCount:  2,
			wantParsed: true,
		},
		{
			name:        "empty improvements",
			content:     "output\n<improvements>[]</improvements>\nmore",
			wantCount:   0,
			wantParsed:  true,
		},
		{
			name:        "no tags",
			content:     "just some activity output without any tags",
			wantCount:   0,
			wantParsed:  false,
		},
		{
			name:        "malformed JSON",
			content:     "<improvements>not valid json</improvements>",
			wantCount:   0,
			wantParsed:  false,
		},
		{
			name:        "unclosed tag",
			content:     "<improvements>[{\"title\":\"test\"}]",
			wantCount:   0,
			wantParsed:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile := filepath.Join(t.TempDir(), "activity.log")
			if err := os.WriteFile(tmpFile, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}

			improvements, parsed := parseImprovementsFromActivity(tmpFile)
			if parsed != tt.wantParsed {
				t.Errorf("parsed = %v, want %v", parsed, tt.wantParsed)
			}
			if len(improvements) != tt.wantCount {
				t.Errorf("got %d improvements, want %d", len(improvements), tt.wantCount)
			}
		})
	}
}

func TestParseSummaryFromActivity(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "valid summary",
			content: "output\n<summary>\nThe design is solid overall.\n</summary>\nmore",
			want:    "The design is solid overall.",
		},
		{
			name:    "no summary",
			content: "just activity output",
			want:    "",
		},
		{
			name:    "empty summary",
			content: "<summary></summary>",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile := filepath.Join(t.TempDir(), "activity.log")
			if err := os.WriteFile(tmpFile, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}

			got := parseSummaryFromActivity(tmpFile)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatSummaryNoImprovements(t *testing.T) {
	result := &RetroResult{
		Timestamp:    time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
		PRDProject:   "Ralph",
		Improvements: nil,
		Summary:      "The design is sound.",
	}

	output := FormatSummary(result)
	if output == "" {
		t.Fatal("expected non-empty output")
	}
	if !contains(output, "No improvements identified") {
		t.Error("expected 'No improvements identified' in output")
	}
	if !contains(output, "The design is sound") {
		t.Error("expected summary text in output")
	}
}

func TestFormatSummaryWithImprovements(t *testing.T) {
	result := &RetroResult{
		Timestamp:  time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
		PRDProject: "Ralph",
		Improvements: []Improvement{
			{Title: "Add idle timeout", Category: "resilience", Severity: "high", Description: "Daemon runs forever"},
			{Title: "Add CLI mode", Category: "dx", Severity: "medium", Description: "No headless control"},
			{Title: "Consolidate servers", Category: "architecture", Severity: "low", Description: "Two HTTP servers"},
		},
		Summary: "Generally well-designed with a few gaps.",
	}

	output := FormatSummary(result)
	if !contains(output, "3 improvement(s)") {
		t.Error("expected improvement count")
	}
	if !contains(output, "1 high") {
		t.Error("expected severity breakdown")
	}
	if !contains(output, "resilience") {
		t.Error("expected category grouping")
	}
	if !contains(output, "Add idle timeout") {
		t.Error("expected improvement title")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && containsHelper(s, sub)
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
