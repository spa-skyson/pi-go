package sop

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spa-skyson/pi-rate/internal/config"
)

// LoadPDD returns the PDD SOP instruction text.
// Resolution order: project .pirate/sops/pdd.md → global ~/.pirate/sops/pdd.md → embedded default.
func LoadPDD(workDir string) (string, error) {
	// Try project-level override
	projectPath := filepath.Join(workDir, config.ProjectDirName, "sops", "pdd.md")
	if content, err := os.ReadFile(projectPath); err == nil {
		slog.Debug("loaded PDD SOP from project override", "path", projectPath)
		return string(content), nil
	}

	// Try global override
	globalPath := filepath.Join(config.PirateHome(), "sops", "pdd.md")
	if content, err := os.ReadFile(globalPath); err == nil {
		slog.Debug("loaded PDD SOP from global override", "path", globalPath)
		return string(content), nil
	}

	// Fall back to embedded default
	slog.Debug("using embedded default PDD SOP")
	return DefaultPDDSOP, nil
}
