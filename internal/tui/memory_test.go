package tui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/spa-skyson/pi-rate/internal/palace"
)

func TestMemoryTickCmd_NoDB(t *testing.T) {
	// No .pirate/palace.db in the temp dir → nil status.
	workDir := t.TempDir()
	cmd := memoryTickCmd(workDir, nil)
	msg := cmd()
	m, ok := msg.(memoryTickMsg)
	if !ok {
		t.Fatalf("expected memoryTickMsg, got %T", msg)
	}
	if m.status != nil {
		t.Errorf("expected nil status, got %+v", m.status)
	}
}

func TestMemoryTickCmd_WithDB(t *testing.T) {
	workDir := t.TempDir()
	dbPath := filepath.Join(workDir, ".pirate", "palace.db")

	// Create the palace DB so os.Stat passes and palace.New opens it.
	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("palace.New: %v", err)
	}
	p.Close()

	cmd := memoryTickCmd(workDir, nil)
	msg := cmd()
	m, ok := msg.(memoryTickMsg)
	if !ok {
		t.Fatalf("expected memoryTickMsg, got %T", msg)
	}
	if m.status == nil {
		t.Fatal("expected non-nil status")
	}
	if m.status.DrawerCount != 0 {
		t.Errorf("DrawerCount = %d, want 0", m.status.DrawerCount)
	}
}

func TestMemoryTickCmd_PalaceNewError(t *testing.T) {
	workDir := t.TempDir()
	// Make .pirate/palace.db a directory so palace.New fails to open it as a DB.
	dbPath := filepath.Join(workDir, ".pirate", "palace.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(dbPath, 0o755); err != nil {
		t.Fatalf("mkdir dbPath: %v", err)
	}

	cmd := memoryTickCmd(workDir, nil)
	msg := cmd()
	m, ok := msg.(memoryTickMsg)
	if !ok {
		t.Fatalf("expected memoryTickMsg, got %T", msg)
	}
	if m.status != nil {
		t.Errorf("expected nil status when palace.New fails, got %+v", m.status)
	}
}

// TestMemoryTickCmd_UsesResolvedEmbedder pins the sidebar wiring: the tick must
// open the palace with the embedder config the CLI resolved (tui.Config.Palace),
// so Status names the configured backend. Built from defaults instead, a config
// that selected the api backend would report the wrong embedder here — the same
// drift the rest of the memory commands were fixed for.
func TestMemoryTickCmd_UsesResolvedEmbedder(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[0.1,0.2,0.3,0.4]}]}`))
	}))
	t.Cleanup(ts.Close)

	workDir := t.TempDir()
	dbPath := filepath.Join(workDir, ".pirate", "palace.db")
	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("palace.New: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	resolved := palace.PalaceConfig{APIEmbedderURL: ts.URL, APIEmbedderModel: "bge-m3"}
	msg := memoryTickCmd(workDir, &resolved)()
	m, ok := msg.(memoryTickMsg)
	if !ok {
		t.Fatalf("expected memoryTickMsg, got %T", msg)
	}
	if m.status == nil {
		t.Fatal("expected non-nil status")
	}
	if m.status.Embedder != "api/bge-m3" {
		t.Errorf("sidebar embedder = %q, want api/bge-m3 (the resolved backend)", m.status.Embedder)
	}
}
