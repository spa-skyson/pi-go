package tui

import (
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The sea scene replaces the old matrix rain: while a turn runs, the top bar
// draws a small sea — layered animated waves, a pirate ship that sails from the
// left edge toward the island at the right edge, and (on wide zones) a palm island
// marking the treasure. It reuses the matrix's exact mechanics: feed while the
// agent produces output, tick on the 120ms timer, clear when the turn ends.

// Scene geometry. The scene is seaLines rows tall and exactly as wide as the
// zone feed/tick were given (the main panel's inner width) — no shrink, no
// centering pad, unlike the matrix tape it replaced.
const (
	// seaLines is the height of the scene: sky, three silhouette rows, surface,
	// and a deep parallax swell.
	seaLines = 6
	seaShipW = 12
	// seaIslandW is the island's reserved width at the right edge.
	seaIslandW = 11
	// seaIslandMin is the narrow-zone cutoff: below this the island does not
	// render (sea and ship only), so a cramped zone never clips the palm.
	seaIslandMin = 32
)

// The sprites. Every rune is ASCII except π — East Asian Ambiguous, declared
// in ambiguousChrome ("the pirate's hat insignia — the product's name, kept
// deliberately"), same trade the mascot art already makes. No emoji: the
// pirate flag is a ZWJ sequence no width table measures consistently (see the
// welcome-header decision in chat.go). TestSeaState_GlyphsAreWidthSafe pins
// this.
const (
	seaHull       = `\_________/` // broad pirate hull
	seaSailCrest  = `/|\  /|\`    // main and jib, leaning into the wind
	seaSailTrough = `/| \ /| \`   // sails opened on the trough
	seaCrown      = `\_\|/_/`     // wind-bent palm crown
	seaGull       = `\v/`
	seaSparkle    = `  .   '  .. `
	seaCurl       = `~\/~~/\~=`
	seaSwell      = `__~---~~__--`
	seaDeep       = `-~___~~---__`
	seaTreasure   = 'X'
)

// seaState holds the scene: pure layout state, no cell buffers. Waves, island
// and ship are functions of (width, phase, shipX, bob) at render time, so a
// resize is just a width write — the matrix needed to reallocate its grid.
type seaState struct {
	width   int  // scene width in cells, exactly what feed/tick were given
	active  bool // a turn is running and the bar should draw
	phase   int  // wave phase; advances with output (feed) and time (tick)
	shipX   int  // left column of the ship; advances one cell per tick
	bob     int  // four-step heave cycle; also selects the ship's pitch
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
	s.phase += 2
	s.bob = (s.bob + 1) % 4
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
	s.bob = 0
}

// seaCell is one drawn cell: a rune and the role color it wears. A space (or
// nil color) emits as a plain blank.
type seaCell struct {
	r rune
	c color.Color
}

// render composes the six scene rows. Three water rhythms move at different
// rates; sky details twinkle and drift more slowly. Land is painted before the
// moving ship, so arrival reads as a silhouette crossing the beach.
func (s *seaState) render() string {
	if !s.visible() {
		return ""
	}
	w := s.width

	// Role colors from the active theme; ANSI fallbacks when no palette has
	// been resolved yet, so the scene never renders invisible.
	p := s.palette
	seaC, deepC, hullC, sailC, isleC, skyC := p.Primary, p.Blue, p.Text, p.Subtext, p.Warning, p.Dim
	if !p.Valid {
		seaC, deepC, hullC, sailC, isleC, skyC =
			lipgloss.Color("4"), lipgloss.Color("6"), lipgloss.Color("7"), lipgloss.Color("8"), lipgloss.Color("3"), lipgloss.Color("8")
	}

	rows := make([][]seaCell, seaLines)
	for i := range rows {
		rows[i] = make([]seaCell, w)
		for j := range rows[i] {
			rows[i][j] = seaCell{r: ' '}
		}
	}

	// The sea: glitter, crests, and two deeper rhythms. The surface runs twice
	// as fast as the ship while the bottom layer lags behind.
	sparkle, curl, swell, deep := []rune(seaSparkle), []rune(seaCurl), []rune(seaSwell), []rune(seaDeep)
	for j := 0; j < w; j++ {
		if r := sparkle[(j+s.phase/2)%len(sparkle)]; r != ' ' {
			rows[3][j] = seaCell{r, skyC}
		}
		rows[4][j] = seaCell{curl[(j+s.phase)%len(curl)], seaC}
		rows[5][j] = seaCell{swell[(j+s.phase/2)%len(swell)], deepC}
		if (j+s.phase)%7 == 0 {
			rows[5][j] = seaCell{deep[(j+s.phase/2)%len(deep)], deepC}
		}
	}

	// The island at the right edge: palm crown, trunk, beach with the
	// treasure mark. Static — it is where the ship is going, not a mover.
	island := s.islandPresent()
	base := w
	if island {
		base = w - seaIslandW
		for k, r := range []rune(seaCrown) {
			rows[1][base+2+k] = seaCell{r, isleC}
		}
		rows[2][base+5] = seaCell{'/', isleC}
		rows[3][base+4] = seaCell{'/', isleC}
		rows[3][base+7] = seaCell{'[', isleC}
		rows[3][base+8] = seaCell{'X', isleC}
		rows[3][base+9] = seaCell{']', isleC}
		rows[4][base] = seaCell{seaTreasure, isleC}
		for j := base + 1; j < w; j++ {
			rows[4][j] = seaCell{'_', isleC}
		}
	}

	// Stars blink, the moon holds the horizon, and the gull crosses at a
	// fraction of the ship's speed.
	for j := 2 + s.phase%5; j < base; j += 11 {
		r := '.'
		if (j+s.phase/2)%3 == 0 {
			r = '*'
		}
		rows[0][j] = seaCell{r, skyC}
	}
	if w >= 18 {
		moon := min(w-4, max(1, base-5))
		for k, r := range []rune("(o)") {
			rows[0][moon+k] = seaCell{r, skyC}
		}
	}
	if g := (s.phase / 6) % max(1, base-3); w >= 12 && g+2 < base {
		for k, r := range []rune(seaGull) {
			rows[0][g+k] = seaCell{r, skyC}
		}
	}

	// The ship heaves through a 0,1,2,1 cycle. Alternating sail and hull shapes
	// make the bow visibly pitch instead of merely translating vertically.
	sail := seaSailCrest
	if s.bob == 1 || s.bob == 2 {
		sail = seaSailTrough
	}
	y := []int{0, 1, 0, 1}[s.bob]
	ship := []string{"      |P>", "   |  |", "  " + sail, "_/_|π|_\\_", seaHull}
	if s.bob >= 2 {
		ship[4] = ` \________/`
	}
	for row, line := range ship {
		if y+row >= seaLines {
			break
		}
		for k, r := range []rune(line) {
			if r != ' ' && s.shipX+k < w {
				c := sailC
				if row >= 3 {
					c = hullC
				}
				rows[y+row][s.shipX+k] = seaCell{r, c}
			}
		}
	}
	// Stern wake responds to output-driven phase changes as well as ticks.
	wake := []string{"..~", "~.."}[s.phase%2]
	for k, r := range []rune(wake) {
		x := s.shipX - 3 + k
		if x >= 0 && x < w {
			rows[min(seaLines-1, y+4)][x] = seaCell{r, sailC}
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
const seaTickInterval = 120 * time.Millisecond

// seaTickMsg is sent periodically to animate the sea scene.
type seaTickMsg struct{}

// seaTickCmd returns a command that sends a seaTickMsg after the interval.
func seaTickCmd() tea.Cmd {
	return tea.Tick(seaTickInterval, func(time.Time) tea.Msg {
		return seaTickMsg{}
	})
}
