package interactive

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/eoghanhynes/ralph/internal/dag"
	"github.com/eoghanhynes/ralph/internal/prd"
)

// StoryCreator manages the creation of interactive task stories with
// auto-incrementing T-prefix IDs. The counter persists across the session
// (monotonically increasing). It is safe for concurrent use.
type StoryCreator struct {
	counter atomic.Int64
	mu      sync.Mutex
}

// NewStoryCreator returns a StoryCreator starting at counter 0.
func NewStoryCreator() *StoryCreator {
	return &StoryCreator{}
}

// nextID returns the next auto-incrementing story ID in T-001 format.
func (sc *StoryCreator) nextID() string {
	n := sc.counter.Add(1)
	return fmt.Sprintf("T-%03d", n)
}

// CreateAndAppend creates a new interactive task story from the given task text
// and optional clarification Q&A, appends it to the PRD's UserStories slice,
// and adds it to the DAG so that ScheduleReady picks it up on the next tick.
//
// The story has:
//   - ID: auto-incrementing T-001, T-002, ... format
//   - Title: first ~80 characters of taskText
//   - Description: full taskText plus clarificationQA (if non-empty)
//   - DependsOn: empty (no DAG edges)
//   - Priority: 0 (immediately ready)
func (sc *StoryCreator) CreateAndAppend(taskText, clarificationQA string, p *prd.PRD, d *dag.DAG) prd.UserStory {
	story := sc.CreateStory(taskText, clarificationQA)

	sc.mu.Lock()
	defer sc.mu.Unlock()

	p.UserStories = append(p.UserStories, story)
	d.AddNode(story.ID, nil, story.Priority)

	return story
}

// CreateStory builds a UserStory from the task text and clarification Q&A
// without appending it anywhere. Useful when the caller manages insertion.
func (sc *StoryCreator) CreateStory(taskText, clarificationQA string) prd.UserStory {
	id := sc.nextID()

	title := taskText
	if len(title) > 80 {
		title = title[:80]
	}

	description := taskText
	if clarificationQA != "" {
		description += "\n\n## Clarification Q&A\n" + clarificationQA
	}

	return prd.UserStory{
		ID:          id,
		Title:       title,
		Description: description,
		DependsOn:   []string{},
		Priority:    0,
	}
}
