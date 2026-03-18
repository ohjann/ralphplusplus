package sprite

// Physics constants.
const (
	Gravity    = 1
	JumpVel    = -3
	MaxFallVel = 4
	ClimbSpeed = 1
)

// Animation tick rates.
const (
	walkAnimRate  = 2 // advance frame every 2 ticks for walk
	climbAnimRate = 3 // advance frame every 3 ticks for climb
)

// Update advances one physics tick. It applies gravity when the sprite is not
// on the ground or on a ladder, and resolves collisions with platforms.
// Returns true if the sprite's position changed.
func (s *Sprite) Update(w *World) bool {
	oldX, oldY := s.X, s.Y

	if s.OnLadder {
		// No gravity while climbing.
		s.advanceAnimation(climbAnimRate)
	} else if !s.OnGround {
		// Apply gravity.
		s.VelY += Gravity
		if s.VelY > MaxFallVel {
			s.VelY = MaxFallVel
		}
		s.Y += s.VelY

		// Check for landing on a platform.
		ix, iy := int(s.X), int(s.Y)
		// The sprite is 2 rows tall; bottom row is iy+1.
		feetY := iy + 1
		for i := range w.Platforms {
			p := &w.Platforms[i]
			if s.VelY >= 0 && feetY >= p.Y-1 && int(oldY)+1 <= p.Y-1 &&
				ix >= p.X1 && ix+s.Width()-1 <= p.X2 {
				// Snap to platform: sprite bottom at p.Y - 1, so top at p.Y - 2.
				s.Y = float64(p.Y - s.Height())
				s.VelY = 0
				s.OnGround = true
				if s.Action == Jump || s.Action == Fall {
					s.Action = Idle
					s.Frame = 0
					s.FrameTick = 0
				}
				break
			}
		}
		if !s.OnGround && s.Action != Jump {
			s.Action = Fall
		}
	} else {
		// On ground — check we still have a platform beneath us.
		s.checkGroundSupport(w)
		if s.Action == WalkLeft || s.Action == WalkRight {
			s.advanceAnimation(walkAnimRate)
		}
	}

	return s.X != oldX || s.Y != oldY
}

// checkGroundSupport verifies the sprite still has a platform under it.
// If not, the sprite starts falling.
func (s *Sprite) checkGroundSupport(w *World) {
	ix := int(s.X)
	feetY := int(s.Y) + s.Height() // row just below sprite
	supported := false
	for i := range w.Platforms {
		p := &w.Platforms[i]
		if feetY == p.Y && ix >= p.X1 && ix+s.Width()-1 <= p.X2 {
			supported = true
			break
		}
	}
	if !supported {
		s.OnGround = false
		s.VelY = 0
		s.Action = Fall
	}
}

// Jump initiates a jump if the sprite is on the ground.
func (s *Sprite) Jump() {
	if !s.OnGround || s.OnLadder {
		return
	}
	s.VelY = JumpVel
	s.OnGround = false
	s.Action = Jump
	s.Frame = 0
	s.FrameTick = 0
}

// StartClimb begins climbing in the given direction (-1 up, 1 down).
// If the sprite is already on a ladder it moves vertically; otherwise it
// checks the world for a ladder at the sprite's position and starts climbing.
func (s *Sprite) StartClimb(dir int) {
	if s.OnLadder {
		s.Y += float64(dir * ClimbSpeed)
		s.advanceAnimation(climbAnimRate)
		return
	}
}

// StartClimbOnLadder is the world-aware entry point for climbing. It checks
// whether a ladder exists at the sprite's current position and begins climbing
// in the given direction (-1 up, 1 down).
func (s *Sprite) StartClimbOnLadder(dir int, w *World) {
	if s.OnLadder {
		s.StartClimb(dir)
		return
	}
	ix, iy := int(s.X), int(s.Y)
	for i := range w.Ladders {
		l := &w.Ladders[i]
		if ix <= l.X && ix+s.Width()-1 >= l.X && iy >= l.Y1 && iy+s.Height()-1 <= l.Y2 {
			s.OnLadder = true
			s.OnGround = false
			s.Action = Climb
			s.Frame = 0
			s.FrameTick = 0
			s.VelY = 0
			s.Y += float64(dir * ClimbSpeed)
			return
		}
	}
}

// Walk moves the sprite horizontally in the given direction (-1 left, 1 right)
// within the world bounds. It stops at platform edges so the sprite does not
// walk off into air.
func (s *Sprite) Walk(dir int, w *World) {
	if s.OnLadder || !s.OnGround {
		return
	}

	newX := s.X + float64(dir)
	ix := int(newX)

	// World boundary check.
	if ix < 0 || ix+s.Width()-1 >= w.Width {
		return
	}

	// Edge check: ensure there is a platform under the new position.
	feetY := int(s.Y) + s.Height()
	hasSupport := false
	for i := range w.Platforms {
		p := &w.Platforms[i]
		if feetY == p.Y && ix >= p.X1 && ix+s.Width()-1 <= p.X2 {
			hasSupport = true
			break
		}
	}
	if !hasSupport {
		return // would walk off edge
	}

	s.X = newX
	s.Dir = dir
	if dir < 0 {
		s.Action = WalkLeft
	} else {
		s.Action = WalkRight
	}
	s.advanceAnimation(walkAnimRate)
}

// advanceAnimation increments FrameTick and advances Frame at the given rate.
func (s *Sprite) advanceAnimation(rate int) {
	s.FrameTick++
	if s.FrameTick >= rate {
		s.FrameTick = 0
		s.Frame++
		frames := actionFrames[s.Action]
		if len(frames) > 0 {
			s.Frame = s.Frame % len(frames)
		}
	}
}
