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
}

type PRD struct {
	Project     string      `json:"project"`
	BranchName  string      `json:"branchName"`
	Description string      `json:"description"`
	UserStories []UserStory `json:"userStories"`
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
