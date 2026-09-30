package cli

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/spa-skyson/pi-rate/internal/config"
	"github.com/spa-skyson/pi-rate/internal/provider"
	"github.com/spa-skyson/pi-rate/internal/tui"
)

// modelCatalogFetchTimeout bounds the whole background refresh behind the
// TUI's model popup: every uncached named provider shares this one budget, so
// a slow endpoint cannot keep the fetch (and the TUI exit) waiting for long.
const modelCatalogFetchTimeout = 10 * time.Second

// modelCandidates builds the seed list for the TUI /model popup: every model
// declared under a config.json provider ("<provider>/<model>", the spelling
// /model switches by) plus the configured roles, which handleModelCommand
// resolves by name. Each entry is an executable /model argument.
func modelCandidates(cfg config.Config) []tui.SearchItem {
	var items []tui.SearchItem
	for _, name := range slices.Sorted(maps.Keys(cfg.Providers)) {
		pc := cfg.Providers[name]
		for _, model := range slices.Sorted(maps.Keys(pc.Models)) {
			items = append(items, tui.SearchItem{
				Text:        name + "/" + model,
				Description: modelCandidateDesc(name, pc.Models[model].ContextWindow),
			})
		}
	}
	for _, role := range slices.Sorted(maps.Keys(cfg.Roles)) {
		rc := cfg.Roles[role]
		desc := "role · " + rc.Model
		if rc.Provider != "" {
			desc += " [" + rc.Provider + "]"
		}
		items = append(items, tui.SearchItem{Text: role, Description: desc})
	}
	return items
}

// modelCandidateDesc renders a popup description: the provider, plus the
// context window when one is declared ("zai-coding-plan · 200K").
func modelCandidateDesc(providerName string, window int64) string {
	if window <= 0 {
		return providerName
	}
	return providerName + " · " + humanTokens(window)
}

// refreshModelCandidates is the TUI models popup's background source: the
// declared list plus each named provider's catalog — the XDG cache when one
// exists, a live fetch (cached for next time) when it does not. Providers
// without both a key and a base URL are skipped: they have nowhere to list
// from. Errors are dropped, and nil comes back only when there is nothing
// beyond the declared list — the popup keeps what it has rather than
// flickering on a partial answer.
func refreshModelCandidates(ctx context.Context, cfg config.Config) []tui.SearchItem {
	ctx, cancel := context.WithTimeout(ctx, modelCatalogFetchTimeout)
	defer cancel()

	keys := cfg.ResolveAPIKeys()
	baseURLs := cfg.ResolveBaseURLs()

	items := modelCandidates(cfg)
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		seen[it.Text] = true
	}

	appended := false
	for _, name := range slices.Sorted(maps.Keys(cfg.Providers)) {
		if keys[name] == "" || baseURLs[name] == "" {
			continue
		}
		pc := cfg.Providers[name]
		opts := provider.ListModelsOptions{APIKey: keys[name], BaseURL: baseURLs[name]}
		models, err := provider.NamedCatalog(ctx, name, pc.Protocol(), opts)
		if err != nil {
			continue
		}
		for _, m := range models {
			text := name + "/" + m.ID
			if seen[text] {
				continue
			}
			seen[text] = true
			items = append(items, tui.SearchItem{Text: text, Description: modelCandidateDesc(name, m.ContextWindow)})
			appended = true
		}
	}
	if !appended {
		return nil
	}
	return items
}
