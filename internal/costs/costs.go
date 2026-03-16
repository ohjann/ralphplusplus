package costs

import (
	"sync"
	"time"
)

// TokenUsage tracks token counts for a single API call or aggregation.
type TokenUsage struct {
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	CacheRead    int    `json:"cache_read"`
	CacheWrite   int    `json:"cache_write"`
	Model        string `json:"model"`
	Provider     string `json:"provider"`           // "claude" or "gemini"
	NumTurns     int    `json:"num_turns,omitempty"` // conversation turns (from Claude Code CLI)
	DurationMS   int    `json:"duration_ms,omitempty"`
}

// ModelPricing holds per-million-token prices for a model.
type ModelPricing struct {
	InputPricePerMToken  float64
	OutputPricePerMToken float64
}

// PricingTable maps model name to its pricing.
type PricingTable map[string]ModelPricing

// DefaultPricing contains current pricing for Claude and Gemini models.
var DefaultPricing = PricingTable{
	// Claude models
	"claude-opus-4-20250514":    {InputPricePerMToken: 15.0, OutputPricePerMToken: 75.0},
	"claude-sonnet-4-20250514":  {InputPricePerMToken: 3.0, OutputPricePerMToken: 15.0},
	"claude-haiku-4-20250506":   {InputPricePerMToken: 0.25, OutputPricePerMToken: 1.25},
	"claude-opus-4-6":           {InputPricePerMToken: 15.0, OutputPricePerMToken: 75.0},
	"claude-sonnet-4-6":         {InputPricePerMToken: 3.0, OutputPricePerMToken: 15.0},
	"claude-haiku-4-5-20251001": {InputPricePerMToken: 0.25, OutputPricePerMToken: 1.25},
	// Gemini models
	"gemini-2.5-pro":   {InputPricePerMToken: 1.25, OutputPricePerMToken: 10.0},
	"gemini-2.5-flash": {InputPricePerMToken: 0.15, OutputPricePerMToken: 0.60},
}

// IterationCost tracks the cost of a single iteration.
type IterationCost struct {
	TokenUsage TokenUsage    `json:"token_usage"`
	Duration   time.Duration `json:"duration"`
	Cost       float64       `json:"cost"`
}

// StoryCosting tracks costs for a single story.
type StoryCosting struct {
	StoryID    string          `json:"story_id"`
	Iterations []IterationCost `json:"iterations"`
	JudgeCosts []TokenUsage    `json:"judge_costs"`
	TotalCost  float64         `json:"total_cost"`
}

// RunCosting tracks costs for an entire run. Safe for concurrent access.
type RunCosting struct {
	mu                sync.Mutex
	Stories           map[string]*StoryCosting `json:"stories"`
	QualityCost       TokenUsage               `json:"quality_cost"`
	DAGCost           TokenUsage               `json:"dag_cost"`
	PlanCost          TokenUsage               `json:"plan_cost"`
	TotalCost         float64                  `json:"total_cost"`
	StartTime         time.Time                `json:"start_time"`
	TotalInputTokens  int                      `json:"total_input_tokens"`
	TotalOutputTokens int                      `json:"total_output_tokens"`
}

// Lock acquires the mutex for external callers that need consistent reads across fields.
func (rc *RunCosting) Lock() {
	rc.mu.Lock()
}

// Unlock releases the mutex.
func (rc *RunCosting) Unlock() {
	rc.mu.Unlock()
}

// CacheHitRateUnlocked computes cache hit rate without acquiring the lock.
// Caller must hold the lock.
func (rc *RunCosting) CacheHitRateUnlocked() float64 {
	var totalCacheRead, totalInput int
	for _, sc := range rc.Stories {
		for _, ic := range sc.Iterations {
			totalCacheRead += ic.TokenUsage.CacheRead
			totalInput += ic.TokenUsage.InputTokens
		}
		for _, jc := range sc.JudgeCosts {
			totalCacheRead += jc.CacheRead
			totalInput += jc.InputTokens
		}
	}
	if totalInput == 0 {
		return 0
	}
	return float64(totalCacheRead) / float64(totalInput)
}

// CalculateCost computes the cost from token counts and model pricing.
func CalculateCost(usage TokenUsage, pricing PricingTable) float64 {
	p, ok := pricing[usage.Model]
	if !ok {
		return 0
	}
	inputCost := float64(usage.InputTokens) * p.InputPricePerMToken / 1_000_000
	outputCost := float64(usage.OutputTokens) * p.OutputPricePerMToken / 1_000_000
	return inputCost + outputCost
}

// NewRunCosting initializes an empty RunCosting with StartTime set to now.
func NewRunCosting() *RunCosting {
	return &RunCosting{
		Stories:   make(map[string]*StoryCosting),
		StartTime: time.Now(),
	}
}

// AddIteration adds an iteration cost to a story and updates totals.
func (rc *RunCosting) AddIteration(storyID string, usage TokenUsage, duration time.Duration) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	cost := CalculateCost(usage, DefaultPricing)

	sc, ok := rc.Stories[storyID]
	if !ok {
		sc = &StoryCosting{StoryID: storyID}
		rc.Stories[storyID] = sc
	}

	sc.Iterations = append(sc.Iterations, IterationCost{
		TokenUsage: usage,
		Duration:   duration,
		Cost:       cost,
	})
	sc.TotalCost += cost

	rc.TotalCost += cost
	rc.TotalInputTokens += usage.InputTokens
	rc.TotalOutputTokens += usage.OutputTokens
}

// AddJudgeCost adds judge cost to a story.
func (rc *RunCosting) AddJudgeCost(storyID string, usage TokenUsage) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	cost := CalculateCost(usage, DefaultPricing)

	sc, ok := rc.Stories[storyID]
	if !ok {
		sc = &StoryCosting{StoryID: storyID}
		rc.Stories[storyID] = sc
	}

	sc.JudgeCosts = append(sc.JudgeCosts, usage)
	sc.TotalCost += cost

	rc.TotalCost += cost
	rc.TotalInputTokens += usage.InputTokens
	rc.TotalOutputTokens += usage.OutputTokens
}

// StoryCost returns the total cost for a specific story.
func (rc *RunCosting) StoryCost(storyID string) float64 {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if sc, ok := rc.Stories[storyID]; ok {
		return sc.TotalCost
	}
	return 0
}

// CostSnapshot is a JSON-serializable snapshot of RunCosting for checkpoint persistence.
type CostSnapshot struct {
	Stories           map[string]*StoryCosting `json:"stories"`
	QualityCost       TokenUsage               `json:"quality_cost"`
	DAGCost           TokenUsage               `json:"dag_cost"`
	PlanCost          TokenUsage               `json:"plan_cost"`
	TotalCost         float64                  `json:"total_cost"`
	TotalInputTokens  int                      `json:"total_input_tokens"`
	TotalOutputTokens int                      `json:"total_output_tokens"`
}

// Snapshot returns a serializable copy of the current costing state.
func (rc *RunCosting) Snapshot() CostSnapshot {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	// Deep copy stories map
	stories := make(map[string]*StoryCosting, len(rc.Stories))
	for id, sc := range rc.Stories {
		cp := *sc
		cp.Iterations = append([]IterationCost(nil), sc.Iterations...)
		cp.JudgeCosts = append([]TokenUsage(nil), sc.JudgeCosts...)
		stories[id] = &cp
	}

	return CostSnapshot{
		Stories:           stories,
		QualityCost:       rc.QualityCost,
		DAGCost:           rc.DAGCost,
		PlanCost:          rc.PlanCost,
		TotalCost:         rc.TotalCost,
		TotalInputTokens:  rc.TotalInputTokens,
		TotalOutputTokens: rc.TotalOutputTokens,
	}
}

// NewFromSnapshot creates a RunCosting restored from a checkpoint snapshot.
func NewFromSnapshot(snap CostSnapshot) *RunCosting {
	stories := snap.Stories
	if stories == nil {
		stories = make(map[string]*StoryCosting)
	}
	return &RunCosting{
		Stories:           stories,
		QualityCost:       snap.QualityCost,
		DAGCost:           snap.DAGCost,
		PlanCost:          snap.PlanCost,
		TotalCost:         snap.TotalCost,
		StartTime:         time.Now(),
		TotalInputTokens:  snap.TotalInputTokens,
		TotalOutputTokens: snap.TotalOutputTokens,
	}
}

// GetTotalCost returns the current total cost, safe for concurrent access.
func (rc *RunCosting) GetTotalCost() float64 {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.TotalCost
}

// GetStoryCost returns the total cost for a specific story, or 0 if not tracked.
func (rc *RunCosting) GetStoryCost(storyID string) float64 {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if sc, ok := rc.Stories[storyID]; ok {
		return sc.TotalCost
	}
	return 0
}


// CacheHitRate computes the cache hit rate from total cache reads vs total input tokens.
func (rc *RunCosting) CacheHitRate() float64 {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	var totalCacheRead, totalInput int
	for _, sc := range rc.Stories {
		for _, ic := range sc.Iterations {
			totalCacheRead += ic.TokenUsage.CacheRead
			totalInput += ic.TokenUsage.InputTokens
		}
		for _, jc := range sc.JudgeCosts {
			totalCacheRead += jc.CacheRead
			totalInput += jc.InputTokens
		}
	}

	if totalInput == 0 {
		return 0
	}
	return float64(totalCacheRead) / float64(totalInput)
}
