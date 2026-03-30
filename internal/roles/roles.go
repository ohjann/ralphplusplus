package roles

import "strings"

// Role represents an agent specialization.
type Role string

const (
	RoleArchitect   Role = "architect"
	RoleImplementer Role = "implementer"
	RoleDebugger    Role = "debugger"
	RoleReviewer    Role = "reviewer"
	RoleSimplify    Role = "simplify"
)

// AgentConfig holds role-specific configuration for an agent.
type AgentConfig struct {
	Role      Role
	PromptFile string
	Model     string
	MaxTokens int
}

// ShouldSkipArchitect returns true for FIX- prefix stories or descriptions
// under 50 words, indicating the architect phase can be skipped.
func ShouldSkipArchitect(storyID string, descriptionWordCount int) bool {
	if strings.HasPrefix(storyID, "FIX-") {
		return true
	}
	return descriptionWordCount < 50
}

// DefaultConfig returns sensible default configuration for a given role.
// PromptFile paths are relative to the ralph home directory.
func DefaultConfig(role Role) AgentConfig {
	switch role {
	case RoleArchitect:
		return AgentConfig{
			Role:      RoleArchitect,
			PromptFile: "prompts/architect.md",
			Model:     "opus",
			MaxTokens: 16000,
		}
	case RoleImplementer:
		return AgentConfig{
			Role:      RoleImplementer,
			PromptFile: "prompts/implementer.md",
			Model:     "sonnet",
			MaxTokens: 32000,
		}
	case RoleDebugger:
		return AgentConfig{
			Role:      RoleDebugger,
			PromptFile: "prompts/debugger.md",
			Model:     "opus",
			MaxTokens: 32000,
		}
	case RoleReviewer:
		return AgentConfig{
			Role:      RoleReviewer,
			PromptFile: "prompts/reviewer.md",
			Model:     "sonnet",
			MaxTokens: 16000,
		}
	case RoleSimplify:
		return AgentConfig{
			Role:       RoleSimplify,
			PromptFile: "prompts/simplify.md",
			Model:      "sonnet",
			MaxTokens:  16000,
		}
	default:
		return AgentConfig{
			Role:      role,
			MaxTokens: 16000,
		}
	}
}
