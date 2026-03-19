package prd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

type UserStory struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria []string `json:"acceptanceCriteria"`
	Priority           int      `json:"priority"`
	Passes             bool     `json:"passes"`
	Notes              string   `json:"notes"`
	DependsOn          []string `json:"dependsOn,omitempty"`
	Approach           string   `json:"approach,omitempty"`
}

type PRD struct {
	Project     string      `json:"project"`
	BranchName  string      `json:"branchName"`
	Description string      `json:"description"`
	Repos       []string    `json:"repos,omitempty"`
	Constraints []string    `json:"constraints,omitempty"`
	UserStories []UserStory `json:"userStories"`
}

// HasExplicitDependencies returns true if any story in the PRD has a non-empty DependsOn field.
func (p *PRD) HasExplicitDependencies() bool {
	for _, s := range p.UserStories {
		if len(s.DependsOn) > 0 {
			return true
		}
	}
	return false
}

// BuildDAGEdges constructs a dependency map from the stories' DependsOn fields.
func (p *PRD) BuildDAGEdges() map[string][]string {
	edges := make(map[string][]string)
	for _, s := range p.UserStories {
		edges[s.ID] = s.DependsOn
	}
	return edges
}

func Load(path string) (*PRD, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading prd.json: %w", err)
	}
	var p PRD
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parsing prd.json: %w", err)
	}
	return &p, nil
}

func Save(path string, p *PRD) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling prd.json: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// NextIncompleteStory returns the highest-priority story with passes==false, or nil.
func (p *PRD) NextIncompleteStory() *UserStory {
	var incomplete []UserStory
	for _, s := range p.UserStories {
		if !s.Passes {
			incomplete = append(incomplete, s)
		}
	}
	if len(incomplete) == 0 {
		return nil
	}
	sort.Slice(incomplete, func(i, j int) bool {
		return incomplete[i].Priority < incomplete[j].Priority
	})
	return &incomplete[0]
}

// AllComplete returns true if every story has passes==true.
func (p *PRD) AllComplete() bool {
	for _, s := range p.UserStories {
		if !s.Passes {
			return false
		}
	}
	return true
}

// CompletedCount returns how many stories have passes==true.
func (p *PRD) CompletedCount() int {
	n := 0
	for _, s := range p.UserStories {
		if s.Passes {
			n++
		}
	}
	return n
}

// TotalCount returns the total number of user stories.
func (p *PRD) TotalCount() int {
	return len(p.UserStories)
}

// FindStory returns the story with the given ID, or nil.
func (p *PRD) FindStory(id string) *UserStory {
	for i := range p.UserStories {
		if p.UserStories[i].ID == id {
			return &p.UserStories[i]
		}
	}
	return nil
}

// SetPasses sets the passes field for a story by ID.
func (p *PRD) SetPasses(id string, passes bool) {
	for i := range p.UserStories {
		if p.UserStories[i].ID == id {
			p.UserStories[i].Passes = passes
			return
		}
	}
}

// HasStory returns true if a story with the given ID exists.
func (p *PRD) HasStory(id string) bool {
	return p.FindStory(id) != nil
}

// InsertBefore inserts a new story before the story with the given ID,
// shifting priorities of all stories at or above the target priority.
func (p *PRD) InsertBefore(beforeID string, newStory UserStory) {
	targetPriority := -1
	for _, s := range p.UserStories {
		if s.ID == beforeID {
			targetPriority = s.Priority
			break
		}
	}
	if targetPriority < 0 {
		// Fallback: insert at lowest priority
		maxP := 0
		for _, s := range p.UserStories {
			if s.Priority > maxP {
				maxP = s.Priority
			}
		}
		newStory.Priority = maxP + 1
		p.UserStories = append(p.UserStories, newStory)
		return
	}

	// Shift priorities
	for i := range p.UserStories {
		if p.UserStories[i].Priority >= targetPriority {
			p.UserStories[i].Priority++
		}
	}
	newStory.Priority = targetPriority
	p.UserStories = append(p.UserStories, newStory)

	// Sort by priority
	sort.Slice(p.UserStories, func(i, j int) bool {
		return p.UserStories[i].Priority < p.UserStories[j].Priority
	})
}
