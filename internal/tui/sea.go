package tui

import (
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The sea scene replaces the old matrix rain: while a turn runs, the top bar
// draws a small sea — two animated wave rows, a ship that sails from the left
// edge toward the island at the right edge, and (on wide zones) a palm island
// marking the treasure. It reuses the matrix's exact mechanics: feed while the
// agent produces output, tick on the 150ms timer, clear when the turn ends.

// Scene geometry. The scene is seaLines rows tall and exactly as wide as the
// zone feed/tick were given (the main panel's inner width) — no shrink, no
// centering pad, unlike the matrix tape it replaced.
const (
	// seaLines is the height of the scene: sky (ship sail, palm crown, gull),
	// waterline (ship hull, palm trunk, beach), surface, swell.
	seaLines = 4
	// seaShipW is the ship's hull width; the sail row lives inside the same
	// five columns.
	seaShipW = 5
	// seaIslandW is the island's reserved width at the right edge.
	seaIslandW = 5
	// seaIslandMin is the narrow-zone cutoff: below this the island does not
	// render (sea and ship only), so a cramped zone never clips the palm.
	seaIslandMin = 24
)

// The sprites. Every rune is ASCII except π — East Asian Ambiguous, declared
// in ambiguousChrome ("the pirate's hat insignia — the product's name, kept
// deliberately"), same trade the mascot art already makes. No emoji: the
// pirate flag is a ZWJ sequence no width table measures consistently (see the
// welcome-header decision in chat.go). TestSeaState_GlyphsAreWidthSafe pins
// this.
const (
	seaHull       = `\_|_/` // hull; the mast is the '|' at index 2
	seaSailCrest  = ` π| `  // sail π to port of the mast — ship on a crest
	seaSailTrough = ` |π `  // sail swung to starboard — ship in a trough
	seaCrown      = `/|\`   // palm crown
	seaGull       = `~v~`   // one sky detail, static
	seaCurl       = `(~)`   // surface tile: foam curls
	seaSwell      = `~--`   // deeper tile: softer rhythm, parallax offset
	seaTreasure   = 'X'     // the mark on the island's beach
)

// seaState holds the scene: pure layout state, no cell buffers. Waves, island
// and ship are functions of (width, phase, shipX, bob) at render time, so a
// resize is just a width write — the matrix needed to reallocate its grid.
type seaState struct {
	width   int  // scene width in cells, exactly what feed/tick were given
	active  bool // a turn is running and the bar should draw
	phase   int  // wave phase; advances with output (feed) and time (tick)
	shipX   int  // left column of the ship; advances one cell per tick
	bob     bool // false = crest (sail to port), true = trough (sail to starboard)
	seed    int64
	palette Palette
}

// ensureWidth clamps and stores the zone width. The scene re-derives itself
// from width at render, so a change needs no reallocation.
func (s *seaState) ensureWidth(width int) {
	if width < 1 {
		width = 1
	}
	s.width = width
}

// islandPresent reports whether the zone is wide enough to draw the island.
func (s *seaState) islandPresent() bool {
	return s.width >= seaIslandMin
}

// shipMaxX is the rightmost ship column: the hull's right edge stops one cell
// short of the island when there is one, else short of the zone edge.
func (s *seaState) shipMaxX() int {
	limit := s.width
	if s.islandPresent() {
		limit = s.width - seaIslandW
	}
	return max(0, limit-seaShipW)
}

// feed marks the scene active and answers output with motion: the wave phase
// advances one step per ~64 bytes of token text (at least one, so every feed
// visibly moves the water), scaled down on narrow zones the way the matrix
// capped its shifts. The ship keeps its own time-based pace and is not
// affected — output volume should not teleport it.
func (s *seaState) feed(tokenText string, width int) {
	for _, b := range []byte(tokenText) {
		s.seed = s.seed*31 + int64(b)
	}
	s.active = true
	s.ensureWidth(width)

	shifts := len(tokenText) / 64
	if shifts < 1 {
		shifts = 1
	}
	if maxShifts := s.width / 4; shifts > maxShifts {
		shifts = maxShifts
	}
	s.phase += shifts
}

// tick advances the animation one 150ms step: the water drifts a cell, the
// ship rocks, and the ship sails one cell toward the island — wrapping to the
// left edge for a new run once it has docked.
func (s *seaState) tick(width int) {
	if !s.active {
		return
	}
	s.ensureWidth(width)
	s.phase++
	s.bob = !s.bob
	if next := s.shipX + 1; next > s.shipMaxX() {
		s.shipX = 0
	} else {
		s.shipX = next
	}
}

// visible reports whether render would return a non-empty scene, without
// building it. messageViewportHeight reserves the scene's rows on visible()
// while View draws them on render() != "", so the two must agree exactly —
// TestSeaVisibleMatchesRender pins them together.
func (s *seaState) visible() bool {
	return s.active && s.width > 0
}

// clear resets the scene when the turn ends. Seed and palette persist across
// turns, the way the matrix's seed did.
func (s *seaState) clear() {
	s.active = false
	s.width = 0
	s.phase = 0
	s.shipX = 0
	s.bob = false
}

// seaCell is one drawn cell: a rune and the role color it wears. A space (or
// nil color) emits as a plain blank.
type seaCell struct {
	r rune
	c color.Color
}

// render composes the four scene rows. Rows 0–1 are sky and waterline: the
// gull, the palm, and the ship's sail and hull. Rows 2–3 are the sea, tiled
// from repeating wave patterns whose offset is the phase, so the water drifts
// one cell leftward every step. The island is painted first, the gull and
// ship last — a moving sprite overwrites whatever it passes.
func (s *seaState) render() string {
	if !s.visible() {
		return ""
	}
	w := s.width

	// Role colors from the active theme; ANSI fallbacks when no palette has
	// been resolved yet, so the scene never renders invisible.
	p := s.palette
	seaC, hullC, sailC, isleC, birdC := p.Primary, p.Text, p.Subtext, p.Warning, p.Dim
	if !p.Valid {
		seaC, hullC, sailC, isleC, birdC =
			lipgloss.Color("4"), lipgloss.Color("7"), lipgloss.Color("8"), lipgloss.Color("3"), lipgloss.Color("8")
	}

	rows := make([][]seaCell, seaLines)
	for i := range rows {
		rows[i] = make([]seaCell, w)
		for j := range rows[i] {
			rows[i][j] = seaCell{r: ' '}
		}
	}

	// The sea: two rows, two rhythms, phase-offset so they do not move in
	// lockstep.
	curl, swell := []rune(seaCurl), []rune(seaSwell)
	for j := 0; j < w; j++ {
		rows[2][j] = seaCell{curl[(j+s.phase)%len(curl)], seaC}
		rows[3][j] = seaCell{swell[(j+s.phase+1)%len(swell)], seaC}
	}

	// The island at the right edge: palm crown, trunk, beach with the
	// treasure mark. Static — it is where the ship is going, not a mover.
	island := s.islandPresent()
	base := w
	if island {
		base = w - seaIslandW
		for k, r := range []rune(seaCrown) {
			rows[0][base+1+k] = seaCell{r, isleC}
		}
		rows[1][base+2] = seaCell{'|', isleC}
		rows[2][base] = seaCell{seaTreasure, isleC}
		for j := base + 1; j < w; j++ {
			rows[2][j] = seaCell{'_', isleC}
		}
	}

	// One sky detail, kept clear of the island's columns.
	if g := w / 4; w >= 12 && (!island || g+2 < base) {
		for k, r := range []rune(seaGull) {
			rows[0][g+k] = seaCell{r, birdC}
		}
	}

	// The ship: sail row rides the sky, hull row the waterline. The bob
	// alternates which side of the mast the sail hangs on — the visible
	// rock. The bounds check keeps a sprite wider than a tiny zone from
	// overrunning the row.
	sail := seaSailCrest
	if s.bob {
		sail = seaSailTrough
	}
	for k, r := range []rune(sail) {
		if r != ' ' && s.shipX+k < w {
			rows[0][s.shipX+k] = seaCell{r, sailC}
		}
	}
	for k, r := range []rune(seaHull) {
		if r != ' ' && s.shipX+k < w {
			rows[1][s.shipX+k] = seaCell{r, hullC}
		}
	}

	style := lipgloss.NewStyle()
	var b strings.Builder
	for i, row := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		for _, cell := range row {
			if cell.r == ' ' || cell.c == nil {
				b.WriteByte(' ')
				continue
			}
			b.WriteString(style.Foreground(cell.c).Render(string(cell.r)))
		}
	}
	return b.String()
}

// seaTickInterval is how often the scene animates while a turn runs — the
// matrix's cadence, kept.
const seaTickInterval = 150 * time.Millisecond

// seaTickMsg is sent periodically to animate the sea scene.
type seaTickMsg struct{}

// seaTickCmd returns a command that sends a seaTickMsg after the interval.
func seaTickCmd() tea.Cmd {
	return tea.Tick(seaTickInterval, func(time.Time) tea.Msg {
		return seaTickMsg{}
	})
}
