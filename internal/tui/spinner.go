package tui

import (
	"math/rand/v2"
	"strings"
	"sync"
	"time"
)

// spinnerVerbs is the list of pirate-themed verbs shown while waiting for a
// response — this is Pi-rate, after all. Keep them gerunds, ASCII-safe
// (letters, apostrophe, hyphen), at most ~16 characters wide, and sorted.
var (
	spinnerVerbs = []string{
		"Ahoying", "Anchoring", "Avasting", "Aweighing",
		"Belaying", "Boarding", "Broadsiding", "Burying",
		"Cannonading", "Careening", "Carousing", "Commandeering",
		"Corsairing", "Crow's-nesting", "Cutlassing",
		"Doublooncounting",
		"Embarking",
		"Fathoming", "Figureheading", "Freebooting",
		"Gangplanking", "Grog-brewing",
		"Heaving", "Helming", "Hoisting", "Hornswoggling",
		"Islandhopping",
		"Jollyrogering",
		"Keelhauling", "Knot-tying",
		"Looting",
		"Marooning", "Mutinying",
		"Navigating",
		"Parleying", "Pillaging", "Planking", "Plundering", "Privateering",
		"Quaffing",
		"Reefing", "Rigging", "Rummaging",
		"Sailing", "Salvaging", "Scallywagging", "Scuppering",
		"Sextanting", "Shantying", "Spyglassing", "Swabbing", "Swashbuckling",
		"Tacking", "Treasuremapping",
		"Unfurling",
		"Voyaging",
		"Wassailing",
		"Yohohoing",
	}

	spinnerVerbWidth = maxStringWidth(spinnerVerbs)
	spinnerTextWidth = spinnerVerbWidth + len("* ") + len("...")
)

// spinnerSymbols are the rotating symbols shown before the verb.
var spinnerSymbols = []rune{'*', '+', '∙'}

// spinnerState holds the current spinner verb and rotation timing.
type spinnerState struct {
	mu       sync.Mutex
	current  string
	updated  time.Time
	symIndex int
	turns    int // counts full symbol rotations for the current word
	nowFn    func() time.Time
}

var spinner = &spinnerState{}

func (s *spinnerState) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// tick advances the spinner state and returns the formatted string.
func (s *spinnerState) tick() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()

	if s.current == "" {
		s.current = spinnerVerbs[rand.IntN(len(spinnerVerbs))]
		s.updated = now
	}

	// Advance symbol every 150ms
	if now.Sub(s.updated) >= 150*time.Millisecond {
		s.symIndex++
		if s.symIndex >= len(spinnerSymbols) {
			s.symIndex = 0
			s.turns++
		}
		// After 3 full rotations, pick a new word
		if s.turns >= 7 {
			s.current = spinnerVerbs[rand.IntN(len(spinnerVerbs))]
			s.turns = 0
			s.symIndex = 0
		}
		s.updated = now
	}

	sym := string(spinnerSymbols[s.symIndex])
	return sym + " " + s.current + "..." + spinnerVerbPadding(s.current)
}

func spinnerVerbPadding(verb string) string {
	if len(verb) >= spinnerVerbWidth {
		return ""
	}
	return strings.Repeat(" ", spinnerVerbWidth-len(verb))
}

func maxStringWidth(values []string) int {
	maxWidth := 0
	for _, value := range values {
		if len(value) > maxWidth {
			maxWidth = len(value)
		}
	}
	return maxWidth
}

// paddedStatusMode returns an idle mode label padded to the same width as spinnerVerb().
func paddedStatusMode(mode string) string {
	if len(mode) >= spinnerTextWidth {
		return mode
	}
	return mode + strings.Repeat(" ", spinnerTextWidth-len(mode))
}

// spinnerVerb returns the current spinner verb with a rotating symbol prefix.
func spinnerVerb() string {
	return spinner.tick()
}
