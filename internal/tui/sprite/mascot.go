package sprite

// Mascot bundles a Sprite, World, and AI into a single facade
// that the TUI model can drive with simple method calls.
type Mascot struct {
	Spr   *Sprite
	World World
	AI    *AI
}

// NewMascot creates a Mascot with a sprite positioned on the first platform.
func NewMascot() *Mascot {
	return &Mascot{
		Spr: NewSprite(10, 1),
		AI:  NewAI(42),
	}
}

// Resize rebuilds the world geometry from the given layout parameters
// and repositions the sprite onto the closest valid platform.
func (m *Mascot) Resize(lp LayoutParams) {
	m.World = BuildWorld(lp)
	if len(m.World.Platforms) == 0 {
		return
	}

	// Find the closest platform to the sprite's current Y position.
	bestIdx := 0
	bestDist := int(^uint(0) >> 1)
	iy := int(m.Spr.Y)
	for i, p := range m.World.Platforms {
		dy := iy - (p.Y - m.Spr.Height())
		if dy < 0 {
			dy = -dy
		}
		if dy < bestDist {
			bestDist = dy
			bestIdx = i
		}
	}

	p := m.World.Platforms[bestIdx]
	ix := int(m.Spr.X)

	// Clamp X to stay within the platform.
	if ix < p.X1 {
		m.Spr.X = float64(p.X1)
	} else if ix+m.Spr.Width()-1 > p.X2 {
		m.Spr.X = float64(p.X2 - m.Spr.Width() + 1)
	}

	// Snap Y to the platform surface.
	m.Spr.Y = float64(p.Y - m.Spr.Height())
	m.Spr.OnGround = true
	m.Spr.OnLadder = false
	m.Spr.VelY = 0
}

// Tick advances the mascot by one frame (AI + physics).
// It skips ticking if the world has not been initialized yet (no platforms).
func (m *Mascot) Tick() {
	if len(m.World.Platforms) == 0 {
		return
	}
	m.AI.Tick(m.Spr, &m.World)
	m.Spr.Update(&m.World)
}

// Overlay composites the sprite onto the given TUI output string.
func (m *Mascot) Overlay(output string) string {
	return Overlay(output, m.Spr)
}
