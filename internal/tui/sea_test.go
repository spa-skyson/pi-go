package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/text/width"
)

// seaScene feeds a fresh scene at the given width and returns it with its
// ANSI-stripped, row-split frame.
func seaScene(t *testing.T, width int) (*seaState, []string) {
	t.Helper()
	var s seaState
	s.feed("ahoy", width)
	out := ansi.Strip(s.render())
	if out == "" {
		t.Fatal("expected a non-empty frame for an active scene")
	}
	return &s, strings.Split(out, "\n")
}

// The scene is seaLines rows tall and every row measures exactly the zone
// width — the frame between the rules must not push the panel wider.
func TestSeaState_RowGeometry(t *testing.T) {
	for width := 10; width <= 100; width++ {
		_, rows := seaScene(t, width)
		if len(rows) != seaLines {
			t.Errorf("width %d: scene has %d rows, want %d", width, len(rows), seaLines)
			continue
		}
		for i, row := range rows {
			if got := ansi.StringWidth(row); got != width {
				t.Errorf("width %d: row %d measures %d cells, want %d", width, i, got, width)
			}
		}
	}
}

// The ship sails left to right one cell per tick, reaching the island's edge,
// then a new run starts from the left.
func TestSeaState_ShipSailsAndCycles(t *testing.T) {
	const width = 40 // island present (>= seaIslandMin)
	s, _ := seaScene(t, width)

	maxX := s.shipMaxX()
	if maxX != width-seaIslandW-seaShipW {
		t.Fatalf("shipMaxX = %d, want %d", maxX, width-seaIslandW-seaShipW)
	}

	seen := map[int]bool{s.shipX: true}
	for i := 0; i < maxX+2; i++ {
		s.tick(width)
		seen[s.shipX] = true
	}
	// Every stop along the way plus the wrap back to 0 must have appeared.
	for x := 0; x <= maxX; x++ {
		if !seen[x] {
			t.Errorf("ship never visited column %d (max %d)", x, maxX)
		}
	}
	if s.shipX != 1 {
		t.Errorf("after maxX+2 ticks the ship started a new run, at column %d", s.shipX)
	}
}

// runeIndex finds sub in s, counting runes rather than bytes — the sky row
// carries π (two bytes), so byte offsets read one column off.
func runeIndex(s, sub string) int {
	rs, want := []rune(s), []rune(sub)
	for i := 0; i+len(want) <= len(rs); i++ {
		match := true
		for k := range want {
			if rs[i+k] != want[k] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// The ship sprite is actually in the frame, at the column seaState says.
func TestSeaState_ShipVisibleAtPosition(t *testing.T) {
	const width = 40
	s, _ := seaScene(t, width)
	s.bob = 0
	s.tick(width) // advance to a known position and settle the bob

	rows := strings.Split(ansi.Strip(s.render()), "\n")
	hullAt := runeIndex(rows[5], seaHull)
	if hullAt < 0 {
		t.Fatalf("hull missing from the waterline row:\n%s", rows[1])
	}
	if hullAt != s.shipX {
		t.Errorf("hull at column %d, want %d", hullAt, s.shipX)
	}
	if !strings.Contains(strings.Join(rows, "\n"), "π") {
		t.Errorf("sail missing from the scene:\n%s", strings.Join(rows, "\n"))
	}
}

// Bobbing: the sail swings from port to starboard of the mast as ticks pass.
func TestSeaState_BobAlternates(t *testing.T) {
	const width = 40
	s, _ := seaScene(t, width)

	b0 := s.bob
	s.tick(width)
	if s.bob != (b0+1)%4 {
		t.Error("bob should advance on a tick")
	}
	s.tick(width)
	if s.bob != (b0+2)%4 {
		t.Error("bob should advance through its four-step cycle")
	}

	s.bob = 0
	s.tick(width)
	crest := ansi.Strip(s.render())
	s.bob = 1
	s.tick(width)
	trough := ansi.Strip(s.render())
	if crest == trough {
		t.Errorf("sail must alternate between the two sprites:\n%s", crest)
	}
}

// The island renders at the right edge on wide zones only.
func TestSeaState_IslandOnWideOnly(t *testing.T) {
	const width = 40
	_, rows := seaScene(t, width)
	crown := runeIndex(rows[1], seaCrown)
	if crown < 0 {
		t.Fatalf("island crown missing on a wide zone:\n%s", rows[1])
	}
	if crown != width-seaIslandW+2 {
		t.Errorf("crown at column %d, want %d", crown, width-seaIslandW+2)
	}
	x := strings.Index(rows[4], "X")
	if x < 0 || x < width-seaIslandW {
		t.Errorf("treasure mark at %d, want inside the island zone [%d,%d)", x, width-seaIslandW, width)
	}

	// Narrow zone: sea and ship only.
	_, rows = seaScene(t, 20)
	for i, row := range rows {
		if strings.Contains(row, seaCrown) || strings.Contains(row, "X") {
			t.Errorf("narrow zone row %d draws island parts:\n%s", i, row)
		}
	}
}

// The palette reaches the scene: role colors change the frame's bytes.
func TestSeaState_PaletteApplied(t *testing.T) {
	var themed seaState
	themed.palette = darkPalette
	themed.feed("ahoy", 40)

	var plain seaState
	plain.feed("ahoy", 40) // zero Palette → ANSI fallback colors

	if themed.render() == plain.render() {
		t.Error("themed and fallback frames render identically — the palette is not applied")
	}
	if !strings.Contains(themed.render(), "\x1b[") {
		t.Error("themed frame carries no ANSI color")
	}
}

// Every sprite rune is width-safe: ASCII, or declared in ambiguousChrome (π).
// The pirate flag was rejected at the welcome banner for exactly this reason —
// keep it out of here too.
func TestSeaState_GlyphsAreWidthSafe(t *testing.T) {
	sprites := seaHull + seaSailCrest + seaSailTrough + seaCrown + seaGull + seaSparkle + seaCurl + seaSwell + seaDeep + string(seaTreasure)
	for _, r := range sprites {
		if r == ' ' || widthSafe(r) {
			continue
		}
		if _, allowed := ambiguousChrome[r]; allowed {
			continue
		}
		t.Errorf("sea sprite draws width-unsafe rune %q (U+%04X, %v)", r, r, width.LookupRune(r).Kind())
	}
}
