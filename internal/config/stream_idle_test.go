package config

import (
	"testing"
	"time"
)

// Resolution order for the stream-idle budget: env override, config.json,
// default. Zero — from either source — is "off", which is why the config
// field is a pointer: unset must stay distinguishable from explicit 0.
func TestResolveStreamIdleTimeout(t *testing.T) {
	tests := []struct {
		name   string
		env    string
		config *int
		want   time.Duration
	}{
		{name: "unset config, unset env", want: DefaultStreamIdleTimeout},
		{name: "config value", config: intPtr(60000), want: 60 * time.Second},
		{name: "config zero disables", config: intPtr(0), want: 0},
		{name: "env override wins", env: "5000", config: intPtr(60000), want: 5 * time.Second},
		{name: "env zero disables over config", env: "0", config: intPtr(60000), want: 0},
		{name: "env zero disables over default", env: "0", want: 0},
		{name: "invalid env ignored", env: "abc", config: intPtr(60000), want: 60 * time.Second},
		{name: "negative env ignored", env: "-5", want: DefaultStreamIdleTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv(EnvStreamIdleTimeoutMS, tt.env)
			}
			cfg := Config{StreamIdleTimeout: tt.config}
			if got := cfg.ResolveStreamIdleTimeout(); got != tt.want {
				t.Errorf("ResolveStreamIdleTimeout() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The default must sit safely between a healthy stream's inter-chunk cadence
// (well under a second) and the subagent inactivity watchdog that the
// heartbeat + retry pair is meant to preempt (5 minutes).
func TestDefaultStreamIdleTimeoutRelationships(t *testing.T) {
	if DefaultStreamIdleTimeout != 90*time.Second {
		t.Errorf("DefaultStreamIdleTimeout = %v, want 90s (issue #37)", DefaultStreamIdleTimeout)
	}
	if DefaultStreamIdleTimeout >= 5*time.Minute {
		t.Errorf("DefaultStreamIdleTimeout %v must stay under the 5m subagent inactivity watchdog", DefaultStreamIdleTimeout)
	}
}
