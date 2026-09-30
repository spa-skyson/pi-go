package tui

import "sync"

// AgentMood represents the current emotional state of the agent.
type AgentMood int

const (
	MoodIdle       AgentMood = iota // Default waiting state
	MoodThinking                    // Processing/reasoning
	MoodProcessing                  // Tool execution
	MoodToolCall                    // About to call a tool
	MoodSpeaking                    // Producing text output
	MoodHappy                       // Task completed successfully
	MoodSad                         // Error or task failed
)

// moodEyes maps each mood to a simple eyes string for the status bar.
var moodEyes = map[AgentMood]string{
	MoodIdle:       "◕ ◕",
	MoodThinking:   "◔ ◕",
	MoodProcessing: "◔ ◔",
	MoodToolCall:   "▸ ◂",
	MoodSpeaking:   "◕ ◡",
	MoodHappy:      "✧ ✧",
	MoodSad:        "◡ ◡",
}

// moodMascot maps each mood to the full pirate (5 rows, 8 cells wide).
//
// The art is a fixed monospace grid: hat, face rows and shoulders with the
// hook hand. The mood is a detail on the two face rows — a squint, a ship's
// wheel, an open mouth, a tear — while the frame never moves, so the sidebar
// slot does not jump when the mood changes.
//
// Every glyph here is East Asian Neutral or Narrow, or declared in
// ambiguousChrome (π, █), so the art measures the same in every terminal
// regardless of mood, and every mood occupies the same 8×5 box. See
// TestMascotGlyphsAreWidthSafe and TestMascot_PirateArtGrid.
var moodMascot = map[AgentMood]string{
	// Neutral: patch, eye, beard, hook at rest.
	MoodIdle: ` _.--._ 
/__π___\
(  █ ◕ )
  \____/
(_|  |J)`,
	// Spyglass (or palm) out at the eye, scanning the horizon.
	MoodThinking: ` _.--._ 
/__π___\
( █ ◔ )>
  \____/
(_|  |J)`,
	// At the ship's wheel: hands on )o(.
	MoodProcessing: ` _.--._ 
/__π___\
(  █ ◔ )
 \|)o(|/
(_|  |J)`,
	// Eyes narrowed onto the tool about to be called.
	MoodToolCall: ` _.--._ 
/__π___\
( █ ▸◂ )
  \____/
(_|  |J)`,
	// Talking: open mouth in the beard.
	MoodSpeaking: ` _.--._ 
/__π___\
(  █ ◡ )
  \_o__/
(_|  |J)`,
	// Grin: π-shaped teeth under the hat insignia.
	MoodHappy: ` _.--._ 
/__π___\
(  █ ✧ )
  \_ππ_/
(_|  |J)`,
	// Tear rolling down from under the patch, eye downcast.
	MoodSad: ` _.--._ 
/__π___\
(  █ ◡ )
 ∙\____/
(_|  |J)`,
}

// String returns a human-readable name for the mood.
func (m AgentMood) String() string {
	switch m {
	case MoodIdle:
		return "idle"
	case MoodThinking:
		return "thinking"
	case MoodProcessing:
		return "processing"
	case MoodToolCall:
		return "tool_call"
	case MoodSpeaking:
		return "speaking"
	case MoodHappy:
		return "happy"
	case MoodSad:
		return "sad"
	default:
		return "unknown"
	}
}

// Eyes returns the eyes string for this mood.
func (m AgentMood) Eyes() string {
	if e, ok := moodEyes[m]; ok {
		return e
	}
	return moodEyes[MoodIdle]
}

// Mascot returns the full pirate art for this mood.
func (m AgentMood) Mascot() string {
	if f, ok := moodMascot[m]; ok {
		return f
	}
	return moodMascot[MoodIdle]
}

// FaceRenderer tracks the agent's current mood (thread-safe).
type FaceRenderer struct {
	mu   sync.RWMutex
	mood AgentMood
}

// NewFaceRenderer creates a new face renderer with default idle mood.
func NewFaceRenderer() *FaceRenderer {
	return &FaceRenderer{mood: MoodIdle}
}

// SetMood changes the agent's current mood.
func (f *FaceRenderer) SetMood(mood AgentMood) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mood = mood
}

// GetMood returns the current mood.
func (f *FaceRenderer) GetMood() AgentMood {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.mood
}

// Eyes returns the eyes string for the current mood.
func (f *FaceRenderer) Eyes() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.mood.Eyes()
}

// Mascot returns the full pirate art for the current mood.
func (f *FaceRenderer) Mascot() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.mood.Mascot()
}
