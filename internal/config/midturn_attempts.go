package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// maxMidTurnAttempts is the sane ceiling for the mid-turn replay budget: a
// fat-fingered value must not turn every stalled stream into ten full turns.
const maxMidTurnAttempts = 10

// ResolveMidTurnAttempts returns the effective mid-turn replay budget: how
// many times a turn may run in total when a transient failure cuts it short
// mid-reply (issues #41, #43; consumed by the TUI agent loop).
//
// Precedence: PI_MIDTURN_ATTEMPTS, then config.json `midTurnAttempts:`, then
// DefaultMidTurnAttempts. The value is a total attempt count, the replays
// being the count minus one: 3 — the default — allows two replays; 0 or 1
// turns the replays off, which is the pre-#41 behavior where the first
// transient failure ends the turn.
//
// Garbage and out-of-range values (negative, over the ceiling) fall back to
// the default with a warning: a knob that silently ignored its input would
// read as a knob that honored it.
func (c Config) ResolveMidTurnAttempts() int {
	if v := strings.TrimSpace(os.Getenv(EnvMidTurnAttempts)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return midTurnAttemptsOrDefault(n, v, "env")
		}
		warnMidTurnAttempts(v, "env")
		return DefaultMidTurnAttempts
	}
	if c.MidTurnAttempts == nil {
		return DefaultMidTurnAttempts
	}
	return midTurnAttemptsOrDefault(*c.MidTurnAttempts, strconv.Itoa(*c.MidTurnAttempts), "config")
}

// midTurnAttemptsOrDefault passes a parsed budget through when it is usable —
// 0 (replays off) through maxMidTurnAttempts — and otherwise falls back to
// the default, loudly.
func midTurnAttemptsOrDefault(n int, raw, source string) int {
	if n >= 0 && n <= maxMidTurnAttempts {
		return n
	}
	warnMidTurnAttempts(raw, source)
	return DefaultMidTurnAttempts
}

func warnMidTurnAttempts(raw, source string) {
	slog.Warn("config: unusable midTurnAttempts ignored; using default",
		"value", raw, "source", source)
}
