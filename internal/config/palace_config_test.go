package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPalace_EmbeddingsAPIParse checks the three new keys survive a config
// round trip and that a config without them stays nil (requirement 5: absent
// keys must not change behavior).
func TestPalace_EmbeddingsAPIParse(t *testing.T) {
	in := []byte(`{
		"palace": {
			"enabled": true,
			"embeddings_url": "http://llm.internal:8000/v1",
			"embeddings_model": "bge-m3",
			"embeddings_api_key": "sk-test-123"
		}
	}`)
	var cfg Config
	if err := json.Unmarshal(in, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	p := cfg.Palace
	if p == nil {
		t.Fatal("palace section lost")
	}
	if p.EmbeddingsURL != "http://llm.internal:8000/v1" {
		t.Errorf("embeddings_url = %q", p.EmbeddingsURL)
	}
	if p.EmbeddingsModel != "bge-m3" {
		t.Errorf("embeddings_model = %q", p.EmbeddingsModel)
	}
	if p.EmbeddingsAPIKey != "sk-test-123" {
		t.Errorf("embeddings_api_key = %q", p.EmbeddingsAPIKey)
	}

	// Absent keys read as empty, not as some sentinel default.
	var bare Config
	if err := json.Unmarshal([]byte(`{"palace": {"enabled": true}}`), &bare); err != nil {
		t.Fatalf("unmarshal bare: %v", err)
	}
	if bare.Palace.EmbeddingsURL != "" || bare.Palace.EmbeddingsModel != "" || bare.Palace.EmbeddingsAPIKey != "" {
		t.Errorf("absent keys must be empty, got %+v", bare.Palace)
	}

	// The key marshals back — it is stored, never dropped; masking is a
	// logging discipline, not a data transformation.
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "sk-test-123") {
		t.Error("round trip lost embeddings_api_key")
	}
}

// TestPalace_EmbeddingsAPIKeyStaysPlaceholderThroughSave pins the security
// contract for ${VAR} in palace settings: Load keeps the placeholder in the
// in-memory config — so a later Save() writes the placeholder back, never the
// expanded key — while ResolveEnvValue still hands the consumer the expanded
// value at the point of use. Expanding at load time instead would round-trip
// the secret into ~/.pirate/config.json on the next Save (role switches do
// exactly that), which is the leak this replaces.
func TestPalace_EmbeddingsAPIKeyStaysPlaceholderThroughSave(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIRATE_HOME", home)
	project := t.TempDir()
	t.Chdir(project)
	pirateDir := filepath.Join(project, ProjectDirName)
	if err := os.MkdirAll(pirateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgJSON := `{"palace": {"embeddings_url": "http://llm.internal:8000/v1", "embeddings_api_key": "${PALACE_TEST_KEY}"}}`
	if err := os.WriteFile(filepath.Join(pirateDir, "config.json"), []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	envSrc := "PALACE_TEST_KEY=from-dotenv-secret\n"
	if err := os.WriteFile(filepath.Join(pirateDir, ".env"), []byte(envSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(project)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Palace == nil {
		t.Fatal("palace section lost")
	}
	if got := cfg.Palace.EmbeddingsAPIKey; got != "${PALACE_TEST_KEY}" {
		t.Errorf("in-memory key = %q, want the literal ${VAR} placeholder (secrets must not enter Config)", got)
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	saved, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(saved), "${PALACE_TEST_KEY}") {
		t.Errorf("saved config lost the placeholder: %s", saved)
	}
	if strings.Contains(string(saved), "from-dotenv-secret") {
		t.Error("Save wrote the expanded key to config.json — the placeholder leaked")
	}

	// The consumer — the code building the embedder — still resolves it.
	if got := ResolveEnvValue(cfg.Palace.EmbeddingsAPIKey); got != "from-dotenv-secret" {
		t.Errorf("ResolveEnvValue = %q, want the .env value", got)
	}
}

// TestResolveEnvValue_ExpandsFromDotEnv checks the ${VAR} contract the palace
// settings rely on: project .env overrides home .env, and a missing variable
// expands to empty — the caller decides what an empty value means.
func TestResolveEnvValue_ExpandsFromDotEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIRATE_HOME", home)
	project := t.TempDir()
	t.Chdir(project)
	pirateDir := filepath.Join(project, ProjectDirName)
	if err := os.MkdirAll(pirateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte("PALACE_URL_VAR=home-value\nPALACE_BOTH=home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pirateDir, ".env"), []byte("PALACE_BOTH=project\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := ResolveEnvValue("http://${PALACE_URL_VAR}:8000/v1"); got != "http://home-value:8000/v1" {
		t.Errorf("home .env expansion = %q", got)
	}
	if got := ResolveEnvValue("${PALACE_BOTH}"); got != "project" {
		t.Errorf("project .env must override home: got %q", got)
	}
	if got := ResolveEnvValue("${PALACE_MISSING_VAR}"); got != "" {
		t.Errorf("missing variable = %q, want empty", got)
	}
	if got := ResolveEnvValue("no-placeholder"); got != "no-placeholder" {
		t.Errorf("plain value = %q, want unchanged", got)
	}
}
