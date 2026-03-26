package roles

import "testing"

func TestShouldSkipArchitect(t *testing.T) {
	tests := []struct {
		name          string
		storyID       string
		wordCount     int
		expectSkip    bool
	}{
		{"FIX prefix skips", "FIX-042", 100, true},
		{"FIX prefix with low words also skips", "FIX-001", 10, true},
		{"low word count skips", "P4-001", 30, true},
		{"exactly 49 words skips", "FEAT-01", 49, true},
		{"exactly 50 words does not skip", "FEAT-01", 50, false},
		{"normal story does not skip", "P4-005", 200, false},
		{"zero words skips", "P4-001", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldSkipArchitect(tt.storyID, tt.wordCount)
			if got != tt.expectSkip {
				t.Errorf("ShouldSkipArchitect(%q, %d) = %v, want %v",
					tt.storyID, tt.wordCount, got, tt.expectSkip)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	tests := []struct {
		role       Role
		wantPrompt string
		wantModel  string
		wantTokens int
	}{
		{RoleArchitect, "prompts/architect.md", "opus", 16000},
		{RoleImplementer, "prompts/implementer.md", "sonnet", 32000},
		{RoleDebugger, "prompts/debugger.md", "opus", 32000},
		{RoleReviewer, "prompts/reviewer.md", "sonnet", 16000},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			cfg := DefaultConfig(tt.role)
			if cfg.Role != tt.role {
				t.Errorf("Role = %v, want %v", cfg.Role, tt.role)
			}
			if cfg.PromptFile != tt.wantPrompt {
				t.Errorf("PromptFile = %q, want %q", cfg.PromptFile, tt.wantPrompt)
			}
			if cfg.Model != tt.wantModel {
				t.Errorf("Model = %q, want %q", cfg.Model, tt.wantModel)
			}
			if cfg.MaxTokens != tt.wantTokens {
				t.Errorf("MaxTokens = %d, want %d", cfg.MaxTokens, tt.wantTokens)
			}
		})
	}
}

func TestDefaultConfigUnknownRole(t *testing.T) {
	cfg := DefaultConfig(Role("unknown"))
	if cfg.Role != Role("unknown") {
		t.Errorf("Role = %v, want unknown", cfg.Role)
	}
	if cfg.PromptFile != "" {
		t.Errorf("PromptFile = %q, want empty", cfg.PromptFile)
	}
	if cfg.MaxTokens != 16000 {
		t.Errorf("MaxTokens = %d, want 16000", cfg.MaxTokens)
	}
}
