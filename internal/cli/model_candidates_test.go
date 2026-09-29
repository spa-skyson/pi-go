package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
	"github.com/dimetron/pi-go/internal/tui"
)

// testModelConfig builds a config with two named providers (one declaring
// models, one not) and two roles, out of alphabetical order on purpose: the
// candidate list must sort both levels.
func testModelConfig() config.Config {
	return config.Config{
		Providers: map[string]config.ProviderConfig{
			"zai-coding-plan": {
				BaseURL: "https://api.example.com",
				APIKey:  "key-zai",
				Models: map[string]config.ProviderModelConfig{
					"glm-5.3":       {ContextWindow: 200_000},
					"glm-5.3-flash": {},
				},
			},
			"opencode-go": {
				BaseURL: "https://opencode.example.com",
				APIKey:  "key-oc",
			},
		},
		Roles: map[string]config.RoleConfig{
			"default": {Model: "glm-5.3", Provider: "zai-coding-plan"},
			"plan":    {Model: "gpt-5.6-sol"},
		},
	}
}

// The seed list: declared provider models first (sorted), then roles (sorted),
// each entry an executable /model argument.
func TestModelCandidates(t *testing.T) {
	items := modelCandidates(testModelConfig())

	want := []tui.SearchItem{
		{Text: "zai-coding-plan/glm-5.3", Description: "zai-coding-plan · 200K"},
		{Text: "zai-coding-plan/glm-5.3-flash", Description: "zai-coding-plan"},
		{Text: "default", Description: "role · glm-5.3 [zai-coding-plan]"},
		{Text: "plan", Description: "role · gpt-5.6-sol"},
	}
	if len(items) != len(want) {
		t.Fatalf("modelCandidates = %+v, want %d items", items, len(want))
	}
	// A provider declaring no models contributes nothing (no dangling
	// "opencode-go/" entry).
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, items[i], want[i])
		}
	}
}

func TestModelCandidatesEmpty(t *testing.T) {
	if items := modelCandidates(config.Config{}); items != nil {
		t.Errorf("modelCandidates on an empty config = %+v, want nil", items)
	}
}

// withTempCacheDir points the user cache dir at a temp dir for the duration
// of the test (os.UserCacheDir reads a different variable per platform).
func withTempCacheDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("LocalAppData", filepath.Join(home, "AppData", "Local"))
	dir := filepath.Join(home, ".cache", "pi-go", "models")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("cache dir: %v", err)
	}
	return dir
}

// cachedCatalogDoc mirrors the provider package's on-disk cache shape
// (catalogFile there is private): provider, fetched_at, models.
type cachedCatalogDoc struct {
	Provider  string               `json:"provider"`
	FetchedAt string               `json:"fetched_at"`
	Models    []provider.ModelInfo `json:"models"`
}

// writeCatalogCache installs a fake provider catalog in the XDG cache so
// refreshModelCandidates reads it instead of hitting the network.
func writeCatalogCache(t *testing.T, dir, name string, models []provider.ModelInfo) {
	t.Helper()
	b, err := json.MarshalIndent(cachedCatalogDoc{Provider: name, FetchedAt: "2026-01-01T00:00:00Z", Models: models}, "", "  ")
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
}

// The refresh merges each configured provider's cached catalog into the
// declared list, skips providers without a key or base URL, and deduplicates
// against the declared models.
func TestRefreshModelCandidates(t *testing.T) {
	dir := withTempCacheDir(t)
	writeCatalogCache(t, dir, "opencode-go", []provider.ModelInfo{
		{ID: "glm-5.3-flash", ContextWindow: 128_000},
		{ID: "qwen3-coder", ContextWindow: 0},
	})
	// A provider with no key: skipped, its catalog never read.
	writeCatalogCache(t, dir, "nokey-provider", []provider.ModelInfo{{ID: "hidden"}})

	cfg := testModelConfig()
	cfg.Providers["nokey-provider"] = config.ProviderConfig{BaseURL: "https://nokey.example.com"}

	items := refreshModelCandidates(context.Background(), cfg)
	if items == nil {
		t.Fatal("refreshModelCandidates returned nil, want the merged list")
	}

	var texts []string
	for _, it := range items {
		texts = append(texts, it.Text)
	}
	want := []string{
		"zai-coding-plan/glm-5.3",
		"zai-coding-plan/glm-5.3-flash",
		"default",
		"plan",
		// Catalog entries follow the declared ones.
		"opencode-go/glm-5.3-flash",
		"opencode-go/qwen3-coder",
	}
	if len(texts) != len(want) {
		t.Fatalf("refresh items = %v, want %v", texts, want)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Errorf("item %d = %q, want %q", i, texts[i], want[i])
		}
	}
	// The declared spelling won the dedup: the catalog's window does not
	// overwrite the declared description.
	for _, it := range items {
		if it.Text == "zai-coding-plan/glm-5.3" && it.Description != "zai-coding-plan · 200K" {
			t.Errorf("declared description overwritten: %+v", it)
		}
		if it.Text == "opencode-go/glm-5.3-flash" && it.Description != "opencode-go · 128K" {
			t.Errorf("catalog window missing: %+v", it)
		}
	}
}

// Nothing cached: the refresh fetches live through the provider's protocol
// and caches the result for next time. A provider whose listing fails is
// dropped silently — and legitimately re-listed on the next refresh — while
// the successful one is served from its cache.
func TestRefreshModelCandidatesFetchesAndToleratesErrors(t *testing.T) {
	dir := withTempCacheDir(t)

	counts := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counts[r.URL.Path]++
		if r.URL.Path == "/fail/models" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "glm-5.3-air"}},
		})
	}))
	defer srv.Close()

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"good": {BaseURL: srv.URL, APIKey: "k", Models: map[string]config.ProviderModelConfig{
				"declared": {},
			}},
			"bad": {BaseURL: srv.URL + "/fail", APIKey: "k"},
		},
	}

	items := refreshModelCandidates(context.Background(), cfg)
	if items == nil {
		t.Fatal("refresh returned nil, want declared + fetched")
	}
	var texts []string
	for _, it := range items {
		texts = append(texts, it.Text)
	}
	want := []string{"good/declared", "good/glm-5.3-air"}
	if len(texts) != len(want) || texts[0] != want[0] || texts[1] != want[1] {
		t.Fatalf("refresh items = %v, want %v", texts, want)
	}

	// The successful fetch was cached under the provider's own name — a
	// second refresh reads it instead of listing again.
	b, err := os.ReadFile(filepath.Join(dir, "good.json"))
	if err != nil {
		t.Fatalf("cache file for the fetched catalog: %v", err)
	}
	if !json.Valid(b) {
		t.Errorf("cache file not valid JSON: %s", b)
	}
	goodLists, badLists := counts["/models"], counts["/fail/models"]
	items = refreshModelCandidates(context.Background(), cfg)
	if counts["/models"] != goodLists {
		t.Errorf("second refresh listed the cached provider again (%d → %d)", goodLists, counts["/models"])
	}
	if counts["/fail/models"] == badLists {
		t.Errorf("the failing provider stopped being retried (%d → %d)", badLists, counts["/fail/models"])
	}
	for _, it := range items {
		if it.Text == "good/glm-5.3-air" {
			return // cached catalog served
		}
	}
	t.Errorf("second refresh lost the cached model: %+v", items)
}

// No providers configured at all: nothing to refresh.
func TestRefreshModelCandidatesEmpty(t *testing.T) {
	if items := refreshModelCandidates(context.Background(), config.Config{}); items != nil {
		t.Errorf("refresh on an empty config = %+v, want nil", items)
	}
}
