package sprite

import "math/rand"

// AIState represents the AI's current behavioral state.
type AIState int

const (
	AIPatrol   AIState = iota
	AIIdle
	AIJumping
	AIClimbing
	AIWandering
)

// AI controls autonomous sprite behavior through a simple state machine.
type AI struct {
	State       AIState
	rng         *rand.Rand
	ticksInState int
	targetTicks  int // how many ticks to stay in current state
	patrolDist   int // remaining patrol distance
	climbDir     int // -1 up, 1 down
	stuckCounter int // frames at same position
	lastX        float64
	lastY        float64
}

// NewAI creates a new AI controller with the given random seed.
func NewAI(seed int64) *AI {
	return &AI{
		State: AIPatrol,
		rng:   rand.New(rand.NewSource(seed)),
	}
}

// Tick advances the AI by one frame, updating the sprite's behavior.
func (a *AI) Tick(s *Sprite, w *World) {
	a.ticksInState++

	// Recovery: detect stuck states.
	if s.X == a.lastX && s.Y == a.lastY {
		a.stuckCounter++
	} else {
		a.stuckCounter = 0
	}
	a.lastX = s.X
	a.lastY = s.Y

	if a.stuckCounter > 30 {
		a.recoverStuck(s, w)
		return
	}

	switch a.State {
	case AIPatrol:
		a.tickPatrol(s, w)
	case AIIdle:
		a.tickIdle(s, w)
	case AIJumping:
		a.tickJumping(s, w)
	case AIClimbing:
		a.tickClimbing(s, w)
	case AIWandering:
		a.tickWandering(s, w)
	}
}

// tickPatrol walks in the current direction until edge or random distance.
func (a *AI) tickPatrol(s *Sprite, w *World) {
	if a.ticksInState == 1 {
		// Starting patrol: pick a random distance between 5-20 steps.
		a.patrolDist = 5 + a.rng.Intn(16)
		if s.Dir == 0 {
			s.Dir = 1
		}
	}

	oldX := s.X
	s.Walk(s.Dir, w)

	// Check if we hit an edge (position didn't change) or finished distance.
	if s.X == oldX || a.patrolDist <= 0 {
		a.chooseNextState(s, w)
		return
	}
	a.patrolDist--
}

// tickIdle pauses for 3-8 ticks then resumes patrol.
func (a *AI) tickIdle(s *Sprite, w *World) {
	if a.ticksInState == 1 {
		a.targetTicks = 3 + a.rng.Intn(6)
		s.Action = Idle
		s.Frame = 0
		s.FrameTick = 0
	}
	if a.ticksInState >= a.targetTicks {
		a.transitionTo(AIPatrol)
	}
}

// tickJumping lets the physics engine handle the jump.
// Note: physics Update is called by Mascot.Tick after AI.Tick, so we must NOT
// call s.Update here to avoid double-applying gravity.
func (a *AI) tickJumping(s *Sprite, w *World) {
	if a.ticksInState == 1 {
		s.Jump()
		if s.OnGround {
			// Jump failed (already not on ground?), go back to patrol.
			a.transitionTo(AIPatrol)
			return
		}
	}
	if s.OnGround {
		a.transitionTo(AIPatrol)
	}
}

// tickClimbing moves toward a target platform via the nearest ladder.
func (a *AI) tickClimbing(s *Sprite, w *World) {
	if a.ticksInState == 1 {
		// Pick climb direction: prefer up, fall back to down.
		a.climbDir = -1
		if a.rng.Intn(2) == 0 {
			a.climbDir = 1
		}
	}

	if !s.OnLadder {
		// Walk toward nearest ladder.
		ladder := a.findNearestLadder(s, w)
		if ladder == nil {
			a.transitionTo(AIPatrol)
			return
		}
		dx := ladder.X - int(s.X)
		if dx == 0 {
			s.StartClimbOnLadder(a.climbDir, w)
			if !s.OnLadder {
				a.transitionTo(AIPatrol)
				return
			}
		} else {
			dir := 1
			if dx < 0 {
				dir = -1
			}
			s.Walk(dir, w)
		}
	} else {
		// On ladder: keep climbing.
		// Note: physics Update is called by Mascot.Tick after AI.Tick,
		// so we must NOT call s.Update here to avoid double-ticking.
		s.StartClimb(a.climbDir)

		// Check if we've reached a platform.
		ix, iy := int(s.X), int(s.Y)
		feetY := iy + s.Height()
		for i := range w.Platforms {
			p := &w.Platforms[i]
			if feetY == p.Y && ix >= p.X1 && ix+s.Width()-1 <= p.X2 {
				s.OnLadder = false
				s.OnGround = true
				s.Y = float64(p.Y - s.Height())
				s.Action = Idle
				a.transitionTo(AIPatrol)
				return
			}
		}
	}

	// Bail if climbing takes too long.
	if a.ticksInState > 60 {
		a.transitionTo(AIPatrol)
		s.OnLadder = false
	}
}

// tickWandering is a short random walk in a random direction.
func (a *AI) tickWandering(s *Sprite, w *World) {
	if a.ticksInState == 1 {
		a.patrolDist = 2 + a.rng.Intn(5)
		if a.rng.Intn(2) == 0 {
			s.Dir = -1
		} else {
			s.Dir = 1
		}
	}

	oldX := s.X
	s.Walk(s.Dir, w)

	if s.X == oldX || a.patrolDist <= 0 {
		a.transitionTo(AIPatrol)
		return
	}
	a.patrolDist--
}

// chooseNextState transitions from patrol based on weighted probabilities:
// 40% reverse patrol, 20% idle, 20% climb, 20% jump.
func (a *AI) chooseNextState(s *Sprite, w *World) {
	roll := a.rng.Intn(100)
	switch {
	case roll < 40:
		// Reverse direction and keep patrolling.
		s.Dir = -s.Dir
		if s.Dir == 0 {
			s.Dir = 1
		}
		a.transitionTo(AIPatrol)
	case roll < 60:
		a.transitionTo(AIIdle)
	case roll < 80:
		a.transitionTo(AIClimbing)
	default:
		a.transitionTo(AIJumping)
	}
}

// transitionTo switches the AI to a new state, resetting the tick counter.
func (a *AI) transitionTo(state AIState) {
	a.State = state
	a.ticksInState = 0
}

// findNearestLadder returns the ladder closest to the sprite's X position.
func (a *AI) findNearestLadder(s *Sprite, w *World) *Ladder {
	var best *Ladder
	bestDist := int(^uint(0) >> 1) // max int
	ix := int(s.X)
	for i := range w.Ladders {
		l := &w.Ladders[i]
		// Only consider ladders that span the sprite's vertical range.
		iy := int(s.Y)
		if iy+s.Height()-1 < l.Y1 || iy > l.Y2 {
			continue
		}
		dist := ix - l.X
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist {
			bestDist = dist
			best = &w.Ladders[i]
		}
	}
	return best
}

// recoverStuck resets the sprite to the nearest valid platform position.
func (a *AI) recoverStuck(s *Sprite, w *World) {
	a.stuckCounter = 0
	s.OnLadder = false
	s.VelY = 0

	// Find nearest platform and place sprite on it.
	ix := int(s.X)
	bestDist := int(^uint(0) >> 1)
	var bestPlat *Platform
	for i := range w.Platforms {
		p := &w.Platforms[i]
		if ix >= p.X1 && ix+s.Width()-1 <= p.X2 {
			dy := int(s.Y) - (p.Y - s.Height())
			if dy < 0 {
				dy = -dy
			}
			if dy < bestDist {
				bestDist = dy
				bestPlat = &w.Platforms[i]
			}
		}
	}
	if bestPlat != nil {
		s.Y = float64(bestPlat.Y - s.Height())
		s.OnGround = true
	} else if len(w.Platforms) > 0 {
		// Fallback: use the first platform.
		p := &w.Platforms[0]
		s.X = float64(p.X1 + (p.X2-p.X1)/2)
		s.Y = float64(p.Y - s.Height())
		s.OnGround = true
	}

	s.Action = Idle
	s.Frame = 0
	s.FrameTick = 0
	a.transitionTo(AIIdle)
}
