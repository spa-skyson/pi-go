package tui

import (
	"context"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/spa-skyson/pi-rate/internal/config"
	"github.com/spa-skyson/pi-rate/internal/palace"
)

// memoryTickInterval is how often the sidebar memory status refreshes.
const memoryTickInterval = 30 * time.Second

// memoryTickMsg carries an updated palace status for the sidebar.
type memoryTickMsg struct {
	status *palace.PalaceStatus
}

// memoryTickCmd returns a command that queries the palace DB and returns
// a memoryTickMsg with the result. If no palace DB exists, it returns nil
// status (the sidebar section is hidden). resolved is the embedder config the
// CLI resolved at startup; nil keeps the palace defaults.
func memoryTickCmd(workDir string, resolved *palace.PalaceConfig) tea.Cmd {
	return func() tea.Msg {
		dbPath := filepath.Join(workDir, config.ProjectDirName, "palace.db")
		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			return memoryTickMsg{status: nil}
		}

		cfg := palace.DefaultConfig()
		if resolved != nil {
			cfg = *resolved
		}
		// The sidebar reads the project's own store, wherever the tick ran from.
		cfg.DBPath = dbPath
		p, err := palace.New(palace.WithConfig(cfg))
		if err != nil {
			return memoryTickMsg{status: nil}
		}
		defer p.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		status, err := p.Status(ctx)
		if err != nil {
			return memoryTickMsg{status: nil}
		}
		return memoryTickMsg{status: status}
	}
}
