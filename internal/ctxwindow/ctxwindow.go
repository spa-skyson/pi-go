// Package ctxwindow resolves the context window of the model a session runs
// against, which is the denominator every compaction threshold is measured in.
//
// It is deliberately separate from internal/autocompact, which consumes the
// number: resolving a window means asking internal/provider, and piagent is
// barred by TestPiagentStaysIsolated from reaching provider construction even
// transitively. Keeping the two apart lets piagent install the same compaction
// hook as the CLI without acquiring that dependency.
package ctxwindow

import (
	"context"

	"github.com/spa-skyson/pi-rate/internal/config"
	"github.com/spa-skyson/pi-rate/internal/provider"
)

// Resolve reports the context window the running model actually
// has, in tokens. The most specific answer wins: a per-model declaration on a
// declared provider (config.json "providers".models.<model>.contextWindow)
// first, then the embedded catalog corrected by the two providers that can be
// asked at runtime — Ollama and OpenRouter — and finally the global
// context_window for everything still unknown.
//
// A zero result means the window is unknown, and compaction then never fires:
// AutoCompactConfig.Decide treats an unknown window as CompactionNone rather
// than guessing a percentage of nothing. Setting context_window in config.json
// is the escape hatch for models absent from both the catalog and the
// provider declarations; a per-model declaration outranks it, so switching to
// a declared model still moves the window off the global value.
func Resolve(ctx context.Context, cfg config.Config, info provider.Info, baseURL string) int64 {
	// The per-model declaration is the only source that names this exact
	// model, so it beats the catalog, the live queries and the global below.
	if n := cfg.ContextWindowFor(info.Provider, info.Model); n > 0 {
		return n
	}
	ctxWindowSize := provider.ContextWindowSizeFor(info.Provider, info.Model)
	if info.Ollama {
		if n := provider.OllamaContextWindowSize(ctx, baseURL, info.Model); n > 0 {
			ctxWindowSize = n
		}
	}
	if info.Provider == "openrouter" {
		if n := provider.OpenRouterContextWindowSize(ctx, baseURL, info.Model); n > 0 {
			ctxWindowSize = n
		}
	}
	if cfg.ContextWindow > 0 {
		ctxWindowSize = cfg.ContextWindow
	}
	return ctxWindowSize
}
