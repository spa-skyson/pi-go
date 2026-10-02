package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// ResolveStreamIdleTimeout returns the effective stream-idle budget: how long
// an LLM stream may deliver no chunks before it is aborted as "llm stream
// idle" (see internal/provider idleStreamModel).
//
// Precedence: PI_STREAM_IDLE_TIMEOUT_MS, then config.json
// `streamIdleTimeout:`, then DefaultStreamIdleTimeout. Zero — from the env var
// or an explicit config value — disables the abort; an unset key keeps the
// default, which is why the field is a pointer. The env var is also the
// channel an agent's frontmatter `streamIdleTimeout:` rides to the child
// process (see the subagent orchestrator).
//
// Invalid env values are ignored, matching ResolveTimeout's treatment of its
// own PI_SUBAGENT_* knobs: a typo falls back to the configured default rather
// than failing a session over it.
func (c Config) ResolveStreamIdleTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv(EnvStreamIdleTimeoutMS)); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms >= 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	if c.StreamIdleTimeout == nil {
		return DefaultStreamIdleTimeout
	}
	return time.Duration(*c.StreamIdleTimeout) * time.Millisecond
}
