package dag

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/eoghanhynes/ralph/internal/prd"
)

type StoryNode struct {
	StoryID   string
	DependsOn []string
	Priority  int
}

type DAG struct {
	Nodes map[string]*StoryNode
}

type dagEntry struct {
	ID        string   `json:"id"`
	DependsOn []string `json:"dependsOn"`
}

// Analyze uses Claude Code CLI to explore the codebase and determine story dependencies.
func Analyze(ctx context.Context, projectDir string, stories []prd.UserStory) (*DAG, error) {
	storiesJSON, err := json.Marshal(stories)
	if err != nil {
		return nil, fmt.Errorf("marshaling stories: %w", err)
	}

	prompt := fmt.Sprintf(`Load the /jj-guide skill for reference.

You are analyzing a PRD's user stories to determine dependency ordering for parallel execution.

Here are the user stories:
%s

Explore the codebase to understand the project structure. Determine which stories depend on others based on actual code (shared files, imports, DB schemas, API endpoints, etc.).

A story X depends on story Y if X cannot be implemented without Y's changes being present first (e.g., X modifies a file that Y creates, or X uses an API that Y implements).

Return ONLY a JSON array with no other text, no markdown fences, no explanation:
[{"id":"STORY-ID","dependsOn":["OTHER-ID"]}]

If a story has no dependencies, use an empty array for dependsOn.
Every story ID from the input must appear exactly once in the output.`, string(storiesJSON))

	cmd := exec.CommandContext(ctx, "claude",
		"--dangerously-skip-permissions",
		"-p",
		"--output-format", "text",
	)
	cmd.Dir = projectDir
	cmd.Stdin = strings.NewReader(prompt)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("claude analysis failed: %w", err)
	}

	return parseDAGResponse(string(out), stories)
}

func parseDAGResponse(response string, stories []prd.UserStory) (*DAG, error) {
	// Extract JSON array from response (may have surrounding text)
	response = strings.TrimSpace(response)
	start := strings.Index(response, "[")
	end := strings.LastIndex(response, "]")
	if start < 0 || end < 0 || end <= start {
		return nil, fmt.Errorf("no JSON array found in response")
	}
	jsonStr := response[start : end+1]

	var entries []dagEntry
	if err := json.Unmarshal([]byte(jsonStr), &entries); err != nil {
		return nil, fmt.Errorf("parsing DAG JSON: %w", err)
	}

	// Build priority map from stories
	priorityMap := make(map[string]int)
	for _, s := range stories {
		priorityMap[s.ID] = s.Priority
	}

	dag := &DAG{Nodes: make(map[string]*StoryNode)}
	for _, e := range entries {
		dag.Nodes[e.ID] = &StoryNode{
			StoryID:   e.ID,
			DependsOn: e.DependsOn,
			Priority:  priorityMap[e.ID],
		}
	}

	return dag, nil
}

// Ready returns story IDs whose dependencies are all in the completed set.
func (d *DAG) Ready(completed map[string]bool) []string {
	var ready []string
	for id, node := range d.Nodes {
		if completed[id] {
			continue
		}
		allMet := true
		for _, dep := range node.DependsOn {
			if !completed[dep] {
				allMet = false
				break
			}
		}
		if allMet {
			ready = append(ready, id)
		}
	}
	// Sort by priority (lowest number = highest priority)
	sort.Slice(ready, func(i, j int) bool {
		ni := d.Nodes[ready[i]]
		nj := d.Nodes[ready[j]]
		return ni.Priority < nj.Priority
	})
	return ready
}

// FromCheckpoint reconstructs a DAG from checkpoint data (edges + stories for priority).
func FromCheckpoint(edges map[string][]string, stories []prd.UserStory) *DAG {
	priorityMap := make(map[string]int)
	for _, s := range stories {
		priorityMap[s.ID] = s.Priority
	}

	d := &DAG{Nodes: make(map[string]*StoryNode)}
	for id, deps := range edges {
		d.Nodes[id] = &StoryNode{
			StoryID:   id,
			DependsOn: deps,
			Priority:  priorityMap[id],
		}
	}
	return d
}

// Validate checks for cycles and dangling references.
func (d *DAG) Validate(storyIDs []string) error {
	idSet := make(map[string]bool)
	for _, id := range storyIDs {
		idSet[id] = true
	}

	// Check dangling references
	for id, node := range d.Nodes {
		for _, dep := range node.DependsOn {
			if !idSet[dep] {
				return fmt.Errorf("story %s depends on unknown story %s", id, dep)
			}
		}
	}

	// Topological sort to detect cycles
	visited := make(map[string]int) // 0=unvisited, 1=in-progress, 2=done
	var visit func(id string) error
	visit = func(id string) error {
		if visited[id] == 2 {
			return nil
		}
		if visited[id] == 1 {
			return fmt.Errorf("cycle detected involving story %s", id)
		}
		visited[id] = 1
		if node, ok := d.Nodes[id]; ok {
			for _, dep := range node.DependsOn {
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		visited[id] = 2
		return nil
	}

	for id := range d.Nodes {
		if err := visit(id); err != nil {
			return err
		}
	}

	return nil
}

// LinearFallback creates a DAG where stories are chained by priority (serial execution).
func LinearFallback(stories []prd.UserStory) *DAG {
	sorted := make([]prd.UserStory, len(stories))
	copy(sorted, stories)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})

	dag := &DAG{Nodes: make(map[string]*StoryNode)}
	for i, s := range sorted {
		node := &StoryNode{
			StoryID:  s.ID,
			Priority: s.Priority,
		}
		if i > 0 {
			node.DependsOn = []string{sorted[i-1].ID}
		}
		dag.Nodes[s.ID] = node
	}
	return dag
}
