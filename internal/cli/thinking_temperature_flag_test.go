package cli

import (
	"testing"

	"github.com/spa-skyson/pi-rate/internal/config"
)

// TestEffectiveThinkingLevel pins the precedence: an explicit --thinking flag
// beats config.json's thinking level, and an absent flag leaves the config
// value standing.
func TestEffectiveThinkingLevel(t *testing.T) {
	tests := []struct {
		name         string
		flagThinking string
		cfgLevel     string
		want         string
	}{
		{"flag wins", "high", "low", "high"},
		{"config stands without flag", "", "low", "low"},
		{"flag normalized", "  HIGH  ", "low", "high"},
		{"neither set", "", "", ""},
		{"blank flag falls back to config", "   ", "medium", "medium"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := flagThinking
			t.Cleanup(func() { flagThinking = orig })
			flagThinking = tt.flagThinking

			cfg := config.Config{ThinkingLevel: tt.cfgLevel}
			if got := effectiveThinkingLevel(cfg); got != tt.want {
				t.Fatalf("effectiveThinkingLevel() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestTemperatureFlagOpt pins the nil-means-unset seam: without --temperature
// there is no value to apply, with it the pointer carries the number, and a
// negative value a provider would reject is dropped.
func TestTemperatureFlagOpt(t *testing.T) {
	t.Run("absent flag is unset", func(t *testing.T) {
		origChanged, origVal := flagTemperatureChanged, flagTemperature
		t.Cleanup(func() { flagTemperatureChanged, flagTemperature = origChanged, origVal })
		flagTemperatureChanged = false
		flagTemperature = 0.3

		if got := temperatureFlagOpt(); got != nil {
			t.Fatalf("temperatureFlagOpt() = %v, want nil", *got)
		}
	})

	t.Run("passed flag carries the value", func(t *testing.T) {
		origChanged, origVal := flagTemperatureChanged, flagTemperature
		t.Cleanup(func() { flagTemperatureChanged, flagTemperature = origChanged, origVal })
		flagTemperatureChanged = true
		flagTemperature = 0.3

		got := temperatureFlagOpt()
		if got == nil || *got != 0.3 {
			t.Fatalf("temperatureFlagOpt() = %v, want 0.3", got)
		}
	})

	t.Run("negative is dropped", func(t *testing.T) {
		origChanged, origVal := flagTemperatureChanged, flagTemperature
		t.Cleanup(func() { flagTemperatureChanged, flagTemperature = origChanged, origVal })
		flagTemperatureChanged = true
		flagTemperature = -1

		if got := temperatureFlagOpt(); got != nil {
			t.Fatalf("temperatureFlagOpt() = %v, want nil", *got)
		}
	})
}
