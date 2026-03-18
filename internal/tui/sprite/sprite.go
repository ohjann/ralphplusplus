package sprite

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Action represents the current action state of a sprite.
type Action int

const (
	Idle Action = iota
	WalkLeft
	WalkRight
	Jump
	Climb
	Fall
)

// Sprite color palette (Catppuccin Mocha).
var (
	_  = lipgloss.Color("") // transparent (zero value)
	cR = lipgloss.Color("#F38BA8") // Red
	cB = lipgloss.Color("#89B4FA") // Blue
	cG = lipgloss.Color("#A6E3A1") // Green
	cY = lipgloss.Color("#F9E2AF") // Yellow
	cP = lipgloss.Color("#FAB387") // Peach
	cK = lipgloss.Color("#F5C2E7") // Pink
	cC = lipgloss.Color("#94E2D5") // Cyan / Teal
	cW = lipgloss.Color("#CDD6F4") // White
	cD = lipgloss.Color("#45475A") // Dark (surface)
	cV = lipgloss.Color("#CBA6F7") // Violet / Mauve
	cS = lipgloss.Color("#585B70") // Subtle dark
	cN = lipgloss.Color("#7F849C") // Neutral (overlay1)
)

// Pixel grid dimensions.
const (
	pixelW = 8 // pixels wide
	pixelH = 6 // pixels tall (must be even)
)

// SpriteWidth is the width of the sprite in terminal columns.
// Half-block rendering: 1 pixel = 1 column.
const SpriteWidth = pixelW

// SpriteHeight is the height of the sprite in terminal rows.
// Half-block rendering: 2 pixel rows = 1 terminal row.
const SpriteHeight = pixelH / 2

// px is the pixel grid type for a single frame (6 rows × 8 cols).
type px = [pixelH][pixelW]lipgloss.Color

// frame holds one animation frame as a pixel grid.
type frame struct {
	Pixels px
}

// actionFrames maps each Action to its animation frames.
var actionFrames = map[Action][]frame{
	Idle: {
		{Pixels: spriteIdle1},
		{Pixels: spriteIdle2},
	},
	WalkLeft: {
		{Pixels: spriteWalkL1},
		{Pixels: spriteWalkL2},
	},
	WalkRight: {
		{Pixels: spriteWalkR1},
		{Pixels: spriteWalkR2},
	},
	Jump: {
		{Pixels: spriteJump1},
		{Pixels: spriteJump2},
	},
	Climb: {
		{Pixels: spriteClimb1},
		{Pixels: spriteClimb2},
	},
	Fall: {
		{Pixels: spriteFall1},
		{Pixels: spriteFall2},
	},
}

// ---------------------------------------------------------------------------
// Sprite pixel data — boxy ghost character (8×8)
// Wide white head with two dark eyes, brown belt, narrow body, stubby legs
// Based on retro sprite sheet reference
// ---------------------------------------------------------------------------
var (
	w = cW // body (white)
	d = cD // eyes (dark)
	b = lipgloss.Color("#7F5539") // belt (dark brown)

	// User-specified pixel grid (6 rows × 8 cols):
	//   Row 0: .  .  H  H  H  H  .  .    head
	//   Row 1: .  .  H  E  H  E  .  .    eyes
	//   Row 2: .  A  B  B  B  B  A  .    arms + body
	//   Row 3: A  .  B  B  B  B  .  A    arms + body
	//   Row 4: .  .  K  K  K  K  .  .    belt
	//   Row 5: .  .  L  .  .  L  .  .    legs
	// H=head, E=eye, A=arm, B=body, K=belt, L=leg

	// Idle: eyes centered
	spriteIdle1 = px{
		{"", "", w, w, w, w, "", ""},
		{"", "", w, d, w, d, "", ""},
		{"", w, w, w, w, w, w, ""},
		{w, "", w, w, w, w, "", w},
		{"", "", b, b, b, b, "", ""},
		{"", "", w, "", "", w, "", ""},
	}
	spriteIdle2 = px{
		{"", "", w, w, w, w, "", ""},
		{"", "", w, d, w, d, "", ""},
		{"", w, w, w, w, w, w, ""},
		{w, "", w, w, w, w, "", w},
		{"", "", b, b, b, b, "", ""},
		{"", "", w, "", "", w, "", ""},
	}

	// Walk left: eyes shifted left, single-pixel legs 1px apart alternating
	spriteWalkL1 = px{
		{"", "", w, w, w, w, "", ""},
		{"", "", d, w, d, w, "", ""},
		{"", w, w, w, w, w, w, ""},
		{w, "", w, w, w, w, "", w},
		{"", "", b, b, b, b, "", ""},
		{"", "", w, "", "", w, "", ""},  // left leg forward, right back
	}
	spriteWalkL2 = px{
		{"", "", w, w, w, w, "", ""},
		{"", "", d, w, d, w, "", ""},
		{"", w, w, w, w, w, w, ""},
		{w, "", w, w, w, w, "", w},
		{"", "", b, b, b, b, "", ""},
		{"", "", "", w, w, "", "", ""},  // legs swap
	}

	// Walk right: eyes shifted right, single-pixel legs 1px apart alternating
	spriteWalkR1 = px{
		{"", "", w, w, w, w, "", ""},
		{"", "", w, d, w, d, "", ""},
		{"", w, w, w, w, w, w, ""},
		{w, "", w, w, w, w, "", w},
		{"", "", b, b, b, b, "", ""},
		{"", "", w, "", "", w, "", ""},  // left leg forward, right back
	}
	spriteWalkR2 = px{
		{"", "", w, w, w, w, "", ""},
		{"", "", w, d, w, d, "", ""},
		{"", w, w, w, w, w, w, ""},
		{w, "", w, w, w, w, "", w},
		{"", "", b, b, b, b, "", ""},
		{"", "", "", w, w, "", "", ""},  // legs swap
	}

	// Jump: arms up alongside head
	spriteJump1 = px{
		{w, "", w, w, w, w, "", w},
		{w, "", w, d, w, d, "", w},
		{"", "", w, w, w, w, "", ""},
		{"", "", w, w, w, w, "", ""},
		{"", "", b, b, b, b, "", ""},
		{"", "", w, "", "", w, "", ""},
	}
	spriteJump2 = px{
		{w, "", w, w, w, w, "", w},
		{w, "", d, w, d, w, "", w},
		{"", "", w, w, w, w, "", ""},
		{"", "", w, w, w, w, "", ""},
		{"", "", b, b, b, b, "", ""},
		{"", "", "", w, w, "", "", ""},
	}

	// Climb: alternating arm positions
	spriteClimb1 = px{
		{"", "", w, w, w, w, "", ""},
		{"", "", w, d, w, d, "", ""},
		{w, "", w, w, w, w, "", ""},
		{"", "", w, w, w, w, "", w},
		{"", "", b, b, b, b, "", ""},
		{"", "", w, "", "", w, "", ""},
	}
	spriteClimb2 = px{
		{"", "", w, w, w, w, "", ""},
		{"", "", w, d, w, d, "", ""},
		{"", "", w, w, w, w, "", w},
		{w, "", w, w, w, w, "", ""},
		{"", "", b, b, b, b, "", ""},
		{"", "", w, "", "", w, "", ""},
	}

	// Fall: arms spread wide
	spriteFall1 = px{
		{"", "", w, w, w, w, "", ""},
		{"", "", w, d, w, d, "", ""},
		{w, w, w, w, w, w, w, w},
		{"", "", w, w, w, w, "", ""},
		{"", "", b, b, b, b, "", ""},
		{"", "", w, "", "", w, "", ""},
	}
	spriteFall2 = px{
		{"", "", w, w, w, w, "", ""},
		{"", "", d, w, d, w, "", ""},
		{w, w, w, w, w, w, w, w},
		{"", "", w, w, w, w, "", ""},
		{"", "", b, b, b, b, "", ""},
		{"", "", "", w, w, "", "", ""},
	}
)

// Sprite represents an animated character in the TUI world.
type Sprite struct {
	X         float64
	Y         float64
	VelY      float64
	Dir       int // -1 left, 0 neutral, 1 right
	Action    Action
	Frame     int
	FrameTick int
	OnGround  bool
	OnLadder  bool
}

// NewSprite creates a new Sprite at the given position.
func NewSprite(x, y float64) *Sprite {
	return &Sprite{
		X:        x,
		Y:        y,
		Dir:      1,
		Action:   Idle,
		OnGround: true,
	}
}

// Frames returns the styled lines for the current animation frame.
// Each pair of pixel rows is rendered as one line of half-block characters (▀/▄).
// Each character encodes two vertical pixels using fg (top) and bg (bottom) colors.
// This gives square-looking pixels since terminal chars are ~2x taller than wide.
func (s *Sprite) Frames() []string {
	frames := actionFrames[s.Action]
	if len(frames) == 0 {
		frames = actionFrames[Idle]
	}
	idx := s.Frame % len(frames)
	f := frames[idx]

	result := make([]string, SpriteHeight)
	for row := 0; row < SpriteHeight; row++ {
		topRow := row * 2
		botRow := row*2 + 1
		var buf strings.Builder
		for col := 0; col < SpriteWidth; col++ {
			top := f.Pixels[topRow][col]
			bot := f.Pixels[botRow][col]
			if top == "" && bot == "" {
				buf.WriteRune(' ')
			} else if top != "" && bot != "" {
				style := lipgloss.NewStyle().Foreground(top).Background(bot)
				buf.WriteString(style.Render("▀"))
			} else if top != "" {
				style := lipgloss.NewStyle().Foreground(top)
				buf.WriteString(style.Render("▀"))
			} else {
				style := lipgloss.NewStyle().Foreground(bot)
				buf.WriteString(style.Render("▄"))
			}
		}
		result[row] = buf.String()
	}
	return result
}

// Width returns the width of the sprite in terminal columns.
func (s *Sprite) Width() int {
	return SpriteWidth
}

// Height returns the height of the sprite in terminal rows.
func (s *Sprite) Height() int {
	return SpriteHeight
}
