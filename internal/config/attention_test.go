package config

import (
	"encoding/json"
	"testing"
)

func TestAttentionDefaults(t *testing.T) {
	// The shipped defaults turn both channels on: an empty section (nil
	// pointers inside) resolves to enabled.
	def := Defaults()
	if def.Attention == nil {
		t.Fatal("Defaults() must carry a non-nil Attention section")
	}
	if !def.Attention.BellEnabled() || !def.Attention.NotifyEnabled() {
		t.Fatal("default attention must have both channels on")
	}
}

func TestAttention_ExplicitFalseSurvivesRoundTrip(t *testing.T) {
	// A user's explicit "bell": false must survive a marshal/unmarshal —
	// this is why the fields are pointers, not plain bools.
	in := []byte(`{"attention": {"bell": false}}`)
	var cfg Config
	if err := json.Unmarshal(in, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Attention == nil || cfg.Attention.BellEnabled() {
		t.Fatal("explicit bell:false must stay off")
	}
	// notify absent inside an explicit section: still the default-on.
	if !cfg.Attention.NotifyEnabled() {
		t.Fatal("absent notify must default to on")
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Config
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if back.Attention == nil || back.Attention.BellEnabled() {
		t.Fatalf("round trip lost explicit bell:false: %s", out)
	}

	// A section absent entirely stays nil (attention off at the TUI only if
	// the caller passes nil — the CLI always passes the defaulted section).
	var bare Config
	if err := json.Unmarshal([]byte(`{}`), &bare); err != nil {
		t.Fatalf("unmarshal bare: %v", err)
	}
	if bare.Attention != nil {
		t.Fatalf("absent section must stay nil, got %+v", bare.Attention)
	}
}
