package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spa-skyson/pi-rate/internal/config"
	"github.com/spa-skyson/pi-rate/internal/provider"
)

// namedProvidersCfg builds a config with one declared provider, the fixture
// every named-provider test in this file resolves against.
func namedProvidersCfg() config.Config {
	return config.Config{
		Roles: map[string]config.RoleConfig{
			"default": {Model: "gpt-5.6-sol"},
		},
		DefaultProvider: "openai",
		Providers: map[string]config.ProviderConfig{
			"corp-claude": {
				Type:    "openai-compatible",
				BaseURL: "https://corp.example/v1",
				APIKey:  "sk-corp",
				Models:  map[string]config.ProviderModelConfig{"claude-opus-5": {ContextWindow: 200000}},
			},
		},
	}
}

// resetResolveFlags pins the package-level --model/--url flags to zero for the
// test's duration; several resolvers read them directly.
func resetResolveFlags(t *testing.T) {
	t.Helper()
	origURL, origModel := flagURL, flagModel
	t.Cleanup(func() { flagURL, flagModel = origURL, origModel })
	flagURL, flagModel = "", ""
}

func TestResolveRuntimeModelForRole_NamedProviderPrefix(t *testing.T) {
	resetResolveFlags(t)
	cfg := namedProvidersCfg()

	// The role names a built-in provider; the model's declared-provider
	// prefix must win over it.
	info, baseURL, err := resolveRuntimeModelForRole(cfg, "corp-claude/claude-opus-5", "anthropic", "")
	if err != nil {
		t.Fatalf("resolveRuntimeModelForRole: %v", err)
	}
	want := provider.Info{
		Provider: "corp-claude",
		Model:    "claude-opus-5",
		Custom:   true,
		Protocol: "openai-compatible",
		BaseURL:  "https://corp.example/v1",
	}
	if info != want {
		t.Errorf("info = %+v, want %+v", info, want)
	}
	if baseURL != "https://corp.example/v1" {
		t.Errorf("baseURL = %q, want the declared endpoint", baseURL)
	}
	if err := provider.ValidateModel(info); err != nil {
		t.Errorf("ValidateModel rejected a declared-provider model: %v", err)
	}
}

// A role naming a declared provider claims a bare model: without the named
// branch, NewLLM would see Provider "corp-claude" with no protocol and fail
// with "unsupported provider".
func TestResolveRuntimeModelForRole_NamedRoleProvider(t *testing.T) {
	resetResolveFlags(t)
	cfg := namedProvidersCfg()

	info, baseURL, err := resolveRuntimeModelForRole(cfg, "claude-opus-5", "corp-claude", "")
	if err != nil {
		t.Fatalf("resolveRuntimeModelForRole: %v", err)
	}
	if info.Provider != "corp-claude" || info.Model != "claude-opus-5" || !info.Custom || info.Protocol != "openai-compatible" {
		t.Errorf("info = %+v", info)
	}
	if baseURL != "https://corp.example/v1" {
		t.Errorf("baseURL = %q, want the declared endpoint", baseURL)
	}
}

// An explicit built-in prefix on the model name beats a role naming a
// declared provider — same rule as ProviderFromPrefix vs role providers.
func TestResolveRuntimeModelForRole_BuiltinPrefixBeatsNamedRole(t *testing.T) {
	resetResolveFlags(t)
	cfg := namedProvidersCfg()

	info, _, err := resolveRuntimeModelForRole(cfg, "openai/gpt-5.6-sol", "corp-claude", "")
	if err != nil {
		t.Fatalf("resolveRuntimeModelForRole: %v", err)
	}
	if info.Provider != "openai" {
		t.Errorf("provider = %q, want openai (explicit prefix wins)", info.Provider)
	}
}

func TestResolveRuntimeModelForRole_NamedAnthropicProtocol(t *testing.T) {
	resetResolveFlags(t)
	cfg := namedProvidersCfg()
	cfg.Providers["corp-proxy"] = config.ProviderConfig{
		Type:    "anthropic",
		BaseURL: "https://headroom.example",
		APIKey:  "sk-proxy",
	}

	info, baseURL, err := resolveRuntimeModelForRole(cfg, "corp-proxy/claude-opus-5", "", "")
	if err != nil {
		t.Fatalf("resolveRuntimeModelForRole: %v", err)
	}
	if info.Protocol != "anthropic" || info.Provider != "corp-proxy" {
		t.Errorf("info = %+v, want corp-proxy on the anthropic protocol", info)
	}
	if baseURL != "https://headroom.example" {
		t.Errorf("baseURL = %q, want the declared endpoint", baseURL)
	}
}

func TestResolveRuntimeModelForRole_NamedURLFlagWins(t *testing.T) {
	resetResolveFlags(t)
	flagURL = "https://override.example/v1"
	cfg := namedProvidersCfg()

	info, baseURL, err := resolveRuntimeModelForRole(cfg, "corp-claude/claude-opus-5", "", "")
	if err != nil {
		t.Fatalf("resolveRuntimeModelForRole: %v", err)
	}
	if baseURL != "https://override.example/v1" || info.BaseURL != "https://override.example/v1" {
		t.Errorf("baseURL = %q, info.BaseURL = %q, want the --url override", baseURL, info.BaseURL)
	}
}

func TestResolveSwitchedModel_NamedProvider(t *testing.T) {
	resetResolveFlags(t)
	cfg := namedProvidersCfg()

	// Case-insensitive prefix, key and endpoint both from the provider entry.
	info, baseURL, apiKey, err := resolveSwitchedModel(cfg, "CORP-CLAUDE/claude-opus-5", "anthropic")
	if err != nil {
		t.Fatalf("resolveSwitchedModel: %v", err)
	}
	if info.Provider != "corp-claude" || info.Model != "claude-opus-5" || info.Protocol != "openai-compatible" || !info.Custom {
		t.Errorf("info = %+v", info)
	}
	if baseURL != "https://corp.example/v1" {
		t.Errorf("baseURL = %q, want the declared endpoint", baseURL)
	}
	if apiKey != "sk-corp" {
		t.Errorf("apiKey = %q, want the declared key", apiKey)
	}
}

func TestSwitchedModelName_NamedProvider(t *testing.T) {
	resetResolveFlags(t)
	cfg := namedProvidersCfg()

	info := provider.Info{
		Provider: "corp-claude",
		Model:    "claude-opus-5",
		Custom:   true,
		Protocol: "openai-compatible",
		BaseURL:  "https://corp.example/v1",
	}
	// The bare name re-resolves onto built-in anthropic, so the persisted
	// spelling must carry the declared-provider prefix.
	if got := switchedModelName(cfg, info); got != "corp-claude/claude-opus-5" {
		t.Errorf("switchedModelName = %q, want corp-claude/claude-opus-5", got)
	}
	// A built-in model gains no declared-provider prefix just because one is
	// configured: the bare spelling already resolves to the same provider.
	bare := provider.Info{Provider: "anthropic", Model: "claude-opus-5"}
	if got := switchedModelName(cfg, bare); got != "claude-opus-5" {
		t.Errorf("switchedModelName = %q, want the bare name", got)
	}
}

func TestSwitchContextWindowSize_NamedProvider(t *testing.T) {
	resetResolveFlags(t)
	cfg := namedProvidersCfg()
	info := provider.Info{Provider: "corp-claude", Model: "claude-opus-5", Custom: true, Protocol: "openai-compatible"}

	if got := switchContextWindowSize(context.Background(), cfg, info, ""); got != 200000 {
		t.Errorf("window = %d, want the declared 200000", got)
	}
	// The per-model declaration is the most specific answer and beats the
	// global contextWindow — otherwise no switch could ever move the window
	// off the global value.
	cfg.ContextWindow = 128000
	if got := switchContextWindowSize(context.Background(), cfg, info, ""); got != 200000 {
		t.Errorf("window = %d, want the declared 200000 over the global 128000", got)
	}
	// The global still tops the (zero) catalog answer for a model nobody
	// declared.
	undeclared := provider.Info{Provider: "unheard-of", Model: "unheard-of-model"}
	if got := switchContextWindowSize(context.Background(), cfg, undeclared, ""); got != 128000 {
		t.Errorf("window = %d, want the global override 128000", got)
	}
}

func TestBuildCommitMsgFunc_NamedProvider(t *testing.T) {
	resetResolveFlags(t)
	cfg := namedProvidersCfg()
	cfg.Roles["commit"] = config.RoleConfig{Model: "corp-claude/claude-opus-5"}

	fn := buildCommitMsgFunc(context.Background(), cfg)
	if fn == nil {
		t.Fatal("buildCommitMsgFunc = nil, want a working generator for a declared-provider commit role")
	}
}

func TestNamedListProviders(t *testing.T) {
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"zeta":  {Type: "anthropic", BaseURL: "https://z.example"},
		"alpha": {BaseURL: "https://a.example"},
	}}
	got := namedListProviders(cfg)
	want := []modelListProvider{
		{name: "alpha", listAs: "openai-compatible"},
		{name: "zeta", listAs: "anthropic"},
	}
	if len(got) != len(want) {
		t.Fatalf("namedListProviders = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("namedListProviders[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSelectModelListProviders_Named(t *testing.T) {
	origURL := flagURL
	t.Cleanup(func() { flagURL = origURL })
	flagURL = ""
	t.Setenv("AZURE_OPENAI_ENDPOINT", "")

	named := []modelListProvider{
		{name: "corp-anthropic", listAs: "anthropic"},
		{name: "corp-claude", listAs: "openai-compatible"},
	}
	keys := map[string]string{"corp-claude": "sk-corp"}
	baseURLs := map[string]string{"corp-claude": "https://corp.example/v1", "corp-anthropic": "https://headroom.example"}

	// The argument accepts a declared provider, case-insensitively, and maps
	// it onto the endpoint its type selects.
	got, err := selectModelListProviders(io.Discard, []string{"CORP-CLAUDE"}, keys, baseURLs, named)
	if err != nil {
		t.Fatalf("selectModelListProviders: %v", err)
	}
	if len(got) != 1 || got[0] != (modelListProvider{name: "corp-claude", listAs: "openai-compatible"}) {
		t.Fatalf("got = %+v, want the single corp-claude entry", got)
	}

	// The sweep includes every declared provider after the built-ins.
	got, err = selectModelListProviders(io.Discard, nil, keys, baseURLs, named)
	if err != nil {
		t.Fatalf("selectModelListProviders sweep: %v", err)
	}
	var names []string
	for _, p := range got {
		names = append(names, p.name)
	}
	for _, want := range []string{"ollama", "corp-claude", "corp-anthropic"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("sweep missing %q (got %v)", want, names)
		}
	}

	// An unknown name still errors, naming the declared-provider escape hatch.
	if _, err := selectModelListProviders(io.Discard, []string{"nope"}, keys, baseURLs, named); err == nil ||
		!strings.Contains(err.Error(), "declared in config.json") {
		t.Fatalf("err = %v, want an unknown-provider error", err)
	}
}

// TestRunModelList_NamedProvider drives `pi model list <declared>` end to end
// against a stub: the listing must go to the declared endpoint with the
// declared key, and report the declared name.
func TestRunModelList_NamedProvider(t *testing.T) {
	isolateRunModelListEnv(t)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "corp-model-1", "owned_by": "corp"}},
		})
	}))
	defer srv.Close()

	writeGlobalConfig(t, map[string]any{
		"providers": map[string]any{
			"corp-claude": map[string]any{
				"type":    "openai-compatible",
				"baseURL": srv.URL,
				"apiKey":  "sk-corp",
			},
		},
	})

	out, err := runModelListCapture(t, "corp-claude")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "corp-claude (1 models)") {
		t.Errorf("output missing the declared provider header:\n%s", out)
	}
	if !strings.Contains(out, "corp-model-1") {
		t.Errorf("output missing the stub model:\n%s", out)
	}
	if gotAuth != "Bearer sk-corp" {
		t.Errorf("request Authorization = %q, want the declared key", gotAuth)
	}
}

// The anthropic type must list through the Anthropic endpoint shape, not the
// OpenAI one: the mapping from declared type to listing endpoint is the part
// this pins.
func TestRunModelList_NamedProviderAnthropicJSON(t *testing.T) {
	isolateRunModelListEnv(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "claude-x", "type": "model"}},
		})
	}))
	defer srv.Close()

	writeGlobalConfig(t, map[string]any{
		"providers": map[string]any{
			"corp-proxy": map[string]any{
				"type":    "anthropic",
				"baseURL": srv.URL,
				"apiKey":  "sk-proxy",
			},
		},
	})

	out, err := runModelListCapture(t, "corp-proxy", "-o", "json")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var doc modelListJSONDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not a JSON document: %v\n%s", err, out)
	}
	if doc.Provider != "corp-proxy" {
		t.Errorf("provider = %q, want the declared name", doc.Provider)
	}
	if len(doc.Models) != 1 || doc.Models[0].ID != "claude-x" {
		t.Fatalf("models = %+v", doc.Models)
	}
}
