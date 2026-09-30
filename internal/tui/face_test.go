package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestFaceRenderer_DefaultMood(t *testing.T) {
	fr := NewFaceRenderer()
	if fr.GetMood() != MoodIdle {
		t.Errorf("expected MoodIdle, got %v", fr.GetMood())
	}
}

func TestFaceRenderer_SetMood(t *testing.T) {
	fr := NewFaceRenderer()

	tests := []struct {
		mood     AgentMood
		wantEyes string
	}{
		{MoodIdle, "◕ ◕"},
		{MoodThinking, "◔ ◕"},
		{MoodProcessing, "◔ ◔"},
		{MoodToolCall, "▸ ◂"},
		{MoodSpeaking, "◕ ◡"},
		{MoodHappy, "✧ ✧"},
		{MoodSad, "◡ ◡"},
	}

	for _, tt := range tests {
		fr.SetMood(tt.mood)
		got := fr.Eyes()
		if got != tt.wantEyes {
			t.Errorf("Eyes() for %v = %q, want %q", tt.mood, got, tt.wantEyes)
		}
	}
}

func TestAgentMood_String(t *testing.T) {
	tests := []struct {
		mood AgentMood
		want string
	}{
		{MoodIdle, "idle"},
		{MoodThinking, "thinking"},
		{MoodProcessing, "processing"},
		{MoodToolCall, "tool_call"},
		{MoodSpeaking, "speaking"},
		{MoodHappy, "happy"},
		{MoodSad, "sad"},
		{AgentMood(999), "unknown"},
	}

	for _, tt := range tests {
		got := tt.mood.String()
		if got != tt.want {
			t.Errorf("String() for %v = %q, want %q", tt.mood, got, tt.want)
		}
	}
}

func TestAgentMood_Eyes(t *testing.T) {
	// Unknown mood falls back to idle eyes
	got := AgentMood(999).Eyes()
	if got != "◕ ◕" {
		t.Errorf("Eyes() for unknown mood = %q, want %q", got, "◕ ◕")
	}
}

// TestMascot_PirateArtGrid pins the pirate's slot: every mood draws the same
// 8×5 monospace box, and no two moods draw the same art. The width is the
// sidebar slot — if a mood's row measures differently, the top of the frame
// shifts on every mood change.
func TestMascot_PirateArtGrid(t *testing.T) {
	moods := []AgentMood{
		MoodIdle, MoodThinking, MoodProcessing,
		MoodToolCall, MoodSpeaking, MoodHappy, MoodSad,
	}

	seen := map[string]bool{}
	for _, mood := range moods {
		art := mood.Mascot()
		if art == "" {
			t.Fatalf("mood %v: empty mascot", mood)
		}
		if seen[art] {
			t.Errorf("mood %v: mascot art is not distinct from another mood", mood)
		}
		seen[art] = true

		rows := strings.Split(art, "\n")
		if len(rows) != 5 {
			t.Errorf("mood %v: mascot has %d rows, want 5", mood, len(rows))
		}
		for i, row := range rows {
			if w := ansi.StringWidth(row); w != 8 {
				t.Errorf("mood %v: mascot row %d is %d cells wide, want 8 (the sidebar slot)", mood, i, w)
			}
		}
	}
	if len(seen) != len(moods) {
		t.Errorf("got %d distinct mascots, want %d", len(seen), len(moods))
	}
}

func TestFaceRenderer_ThreadSafety(t *testing.T) {
	fr := NewFaceRenderer()
	done := make(chan bool)

	for i := 0; i < 100; i++ {
		go func(n int) {
			mood := AgentMood(n % 7)
			fr.SetMood(mood)
			_ = fr.GetMood()
			_ = fr.Eyes()
			done <- true
		}(i)
	}

	for i := 0; i < 100; i++ {
		<-done
	}
}
