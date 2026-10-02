package config

import (
	"context"
	"log/slog"
	"testing"
)

// warnRecorder captures the messages logged through the default slog handler
// while it is installed. The resolver logs synchronously from the calling
// goroutine, so no locking is needed under -race.
type warnRecorder struct {
	msgs []string
}

func (w *warnRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (w *warnRecorder) Handle(_ context.Context, r slog.Record) error {
	w.msgs = append(w.msgs, r.Message)
	return nil
}

func (w *warnRecorder) WithAttrs([]slog.Attr) slog.Handler { return w }

func (w *warnRecorder) WithGroup(string) slog.Handler { return w }

// captureWarnings installs the recorder for the test and returns a reader.
func captureWarnings(t *testing.T) func() []string {
	t.Helper()
	w := &warnRecorder{}
	prev := slog.Default()
	slog.SetDefault(slog.New(w))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() []string { return w.msgs }
}

// Resolution order for the mid-turn replay budget: env override, config.json,
// default (issue #43). 0 and 1 mean the replays are off; garbage, negative
// and over-ceiling values fall back to the default with a warning — a knob
// that silently ignored its input would read as one that honored it.
func TestResolveMidTurnAttempts(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		config   *int
		want     int
		wantWarn bool
	}{
		{name: "unset config, unset env", want: DefaultMidTurnAttempts},
		{name: "config value", config: intPtr(5), want: 5},
		{name: "config 2 means one replay", config: intPtr(2), want: 2},
		{name: "config 1 disables replays", config: intPtr(1), want: 1},
		{name: "config 0 disables replays", config: intPtr(0), want: 0},
		{name: "config ceiling", config: intPtr(10), want: 10},
		{name: "config negative falls back", config: intPtr(-2), want: DefaultMidTurnAttempts, wantWarn: true},
		{name: "config over ceiling falls back", config: intPtr(11), want: DefaultMidTurnAttempts, wantWarn: true},
		{name: "env override wins", env: "2", config: intPtr(5), want: 2},
		{name: "env 0 disables over config", env: "0", config: intPtr(5), want: 0},
		{name: "env ceiling", env: "10", want: 10},
		{name: "garbage env falls back with warning", env: "abc", config: intPtr(5), want: DefaultMidTurnAttempts, wantWarn: true},
		{name: "negative env falls back", env: "-1", want: DefaultMidTurnAttempts, wantWarn: true},
		{name: "env over ceiling falls back", env: "11", want: DefaultMidTurnAttempts, wantWarn: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := captureWarnings(t)
			if tt.env != "" {
				t.Setenv(EnvMidTurnAttempts, tt.env)
			}
			cfg := Config{MidTurnAttempts: tt.config}
			if got := cfg.ResolveMidTurnAttempts(); got != tt.want {
				t.Errorf("ResolveMidTurnAttempts() = %d, want %d", got, tt.want)
			}
			if got := len(warnings()) > 0; got != tt.wantWarn {
				t.Errorf("warned = %v, want %v (messages: %q)", got, tt.wantWarn, warnings())
			}
		})
	}
}

// The default pins the behavior #41 shipped: the first attempt plus two
// replays. A different default is a deliberate behavior change, not a
// refactor.
func TestDefaultMidTurnAttemptsPinned(t *testing.T) {
	if DefaultMidTurnAttempts != 3 {
		t.Errorf("DefaultMidTurnAttempts = %d, want 3 (issue #41 behavior)", DefaultMidTurnAttempts)
	}
	if DefaultMidTurnAttempts < 1 || DefaultMidTurnAttempts > maxMidTurnAttempts {
		t.Errorf("DefaultMidTurnAttempts %d outside the usable 1..%d range", DefaultMidTurnAttempts, maxMidTurnAttempts)
	}
}
