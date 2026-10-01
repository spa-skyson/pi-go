package subagent

import (
	"testing"
	"time"

	"github.com/spa-skyson/pi-rate/internal/testenv"
)

// TestBundledAgentTimeoutsAreSane walks every shipped agent definition and
// resolves the timeout it would actually run with.
//
// This exists because `memory-compressor` shipped `timeout: 30`. The unit is
// milliseconds, so that agent was SIGKILLed 30ms after starting — every single
// time, before it could emit a token — and nothing anywhere reported the cause.
// The value reads perfectly reasonable if you assume seconds, which is exactly
// why a person reviewing the file did not catch it.
func TestBundledAgentTimeoutsAreSane(t *testing.T) {
	// DiscoverAgents always loads the real home's user dir; isolate HOME so a
	// broken user agent file cannot fail this bundled-only check.
	testenv.SetHome(t, t.TempDir())

	res, err := DiscoverAgents(t.TempDir(), ScopeBundled)
	if err != nil {
		t.Fatalf("DiscoverAgents: %v", err)
	}
	if len(res.All) == 0 {
		t.Fatal("no bundled agents were loaded")
	}

	for _, a := range res.All {
		t.Run(a.Name, func(t *testing.T) {
			cfg := ResolveTimeout(a.Timeout)

			// A second is not enough for a model round trip, so any resolved
			// absolute timeout below it means the agent can never succeed.
			if cfg.Absolute < time.Second {
				t.Errorf("agent %q resolves to a %v absolute timeout — it can never produce output "+
					"(frontmatter `timeout:` is in MILLISECONDS; got %d)", a.Name, cfg.Absolute, a.Timeout)
			}
			if cfg.Inactivity <= 0 {
				t.Errorf("agent %q resolves to a non-positive inactivity timeout %v", a.Name, cfg.Inactivity)
			}
		})
	}
}

func TestMemoryCompressorHasAWorkableTimeout(t *testing.T) {
	testenv.SetHome(t, t.TempDir()) // keep the real home's user dir out of discovery

	res, err := DiscoverAgents(t.TempDir(), ScopeBundled)
	if err != nil {
		t.Fatalf("DiscoverAgents: %v", err)
	}

	var found bool
	for _, a := range res.All {
		if a.Name != "memory-compressor" {
			continue
		}
		found = true
		cfg := ResolveTimeout(a.Timeout)
		if cfg.Absolute < time.Minute {
			t.Errorf("memory-compressor absolute timeout = %v, want at least a minute "+
				"(the value is in milliseconds)", cfg.Absolute)
		}
	}
	if !found {
		t.Fatal("memory-compressor is not among the bundled agents")
	}
}

// TestParseAgentRejectsSubSecondTimeout pins the guard that stops the
// milliseconds-read-as-seconds mistake from silently recurring.
func TestParseAgentRejectsSubSecondTimeout(t *testing.T) {
	const def = `---
name: too-eager
description: test
role: smol
timeout: 30
---
body
`
	cfg, err := parseAgentContent(def, "test.md")
	if err != nil {
		t.Fatalf("parseAgentContent: %v", err)
	}
	if cfg.Timeout != 0 {
		t.Errorf("Timeout = %d, want 0 (implausible value ignored so the default applies)", cfg.Timeout)
	}
	if got := ResolveTimeout(cfg.Timeout); got.Absolute != DefaultAbsoluteTimeout {
		t.Errorf("resolved absolute = %v, want the default %v", got.Absolute, DefaultAbsoluteTimeout)
	}
}

func TestParseAgentKeepsPlausibleTimeout(t *testing.T) {
	const def = `---
name: patient
description: test
role: smol
timeout: 600000
---
body
`
	cfg, err := parseAgentContent(def, "test.md")
	if err != nil {
		t.Fatalf("parseAgentContent: %v", err)
	}
	if cfg.Timeout != 600000 {
		t.Errorf("Timeout = %d, want 600000", cfg.Timeout)
	}
	if got := ResolveTimeout(cfg.Timeout); got.Absolute != 10*time.Minute {
		t.Errorf("resolved absolute = %v, want 10m", got.Absolute)
	}
}

// TestParseAgentTimeoutSuffixes pins the human-readable `timeout:` spellings:
// unit suffixes (1h/45m/90s/500ms — case-insensitive, "45 m" tolerated) plus
// the bare-milliseconds backcompat form. The sub-second guard applies after
// conversion, so an explicit `500ms` is rejected exactly like the bare `30`
// that started it all: no agent run is over in under a second on purpose.
func TestParseAgentTimeoutSuffixes(t *testing.T) {
	tests := []struct {
		value  string
		wantMs int
		wantOK bool
	}{
		{"1h", 3600000, true},
		{"45m", 2700000, true},
		{"90s", 90000, true},
		{"2H", 7200000, true},
		{"45 m", 2700000, true},
		{"3600000", 3600000, true}, // bare milliseconds still honored
		{"500ms", 0, false},        // explicit but sub-second: the guard still applies
		{"1h30m", 0, false},        // combined forms are not parsed
		{"abc", 0, false},
		{"", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			gotMs, gotOK := parseAgentTimeout("agent", tt.value)
			if gotMs != tt.wantMs || gotOK != tt.wantOK {
				t.Errorf("parseAgentTimeout(%q) = (%d, %v), want (%d, %v)",
					tt.value, gotMs, gotOK, tt.wantMs, tt.wantOK)
			}
		})
	}
}

// TestParseAgentContentTimeoutSuffix drives the suffix through the real
// frontmatter path, the way an author writes it.
func TestParseAgentContentTimeoutSuffix(t *testing.T) {
	const def = `---
name: patient-suffix
description: test
role: smol
timeout: 30m
---
body
`
	cfg, err := parseAgentContent(def, "test.md")
	if err != nil {
		t.Fatalf("parseAgentContent: %v", err)
	}
	if cfg.Timeout != 30*60*1000 {
		t.Errorf("Timeout = %d, want %d (30m)", cfg.Timeout, 30*60*1000)
	}
}
