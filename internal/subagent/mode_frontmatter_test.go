package subagent

import "testing"

// TestAgentConfigModeFrontmatter covers the `mode:` key: the three valid
// values land, casing is normalized, and unknown values warn and leave the
// field empty (= subagent-only), mirroring the other soft-validated keys.
func TestAgentConfigModeFrontmatter(t *testing.T) {
	tests := []struct {
		name        string
		frontmatter string
		want        AgentMode
	}{
		{"absent keeps bundled default", "", ""},
		{"primary", "mode: primary\n", ModePrimary},
		{"subagent", "mode: subagent\n", ModeSubagent},
		{"all", "mode: all\n", ModeAll},
		{"trimmed and uppercased", "mode:   PRIMARY  \n", ModePrimary},
		{"empty stays empty", "mode: \n", ""},
		{"unknown warns and stays empty", "mode: orchestrator\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeAgent(t, dir, "moded", tt.frontmatter)

			cfg, err := ParseAgentFile(path)
			if err != nil {
				t.Fatalf("ParseAgentFile: %v", err)
			}
			if cfg.Mode != tt.want {
				t.Errorf("Mode = %q, want %q", cfg.Mode, tt.want)
			}
		})
	}
}

// TestAgentModeClassification pins what the two predicates mean: primary and
// all may take over the main session, only a bare primary is hidden from the
// subagent listing, and the empty default changes nothing for bundled agents.
func TestAgentModeClassification(t *testing.T) {
	tests := []struct {
		mode      AgentMode
		isPrimary bool
		only      bool
	}{
		{"", false, false},
		{ModeSubagent, false, false},
		{ModePrimary, true, true},
		{ModeAll, true, false},
	}
	for _, tt := range tests {
		ac := AgentConfig{Name: "x", Mode: tt.mode}
		if got := ac.IsPrimary(); got != tt.isPrimary {
			t.Errorf("mode %q: IsPrimary() = %v, want %v", tt.mode, got, tt.isPrimary)
		}
		if got := ac.PrimaryOnly(); got != tt.only {
			t.Errorf("mode %q: PrimaryOnly() = %v, want %v", tt.mode, got, tt.only)
		}
	}
}
