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

// TestPalace_EmbeddingsAPIKeyEnvSubstitution checks ${VAR} in
// embeddings_api_key expands from the project .env, so the token can live
// outside config.json — the same contract the MCP headers have.
func TestPalace_EmbeddingsAPIKeyEnvSubstitution(t *testing.T) {
	dir := t.TempDir()
	pirateDir := filepath.Join(dir, ProjectDirName)
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

	cfg, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Palace == nil {
		t.Fatal("palace section lost")
	}
	if cfg.Palace.EmbeddingsAPIKey != "from-dotenv-secret" {
		t.Errorf("key = %q, want the .env value", cfg.Palace.EmbeddingsAPIKey)
	}
}
