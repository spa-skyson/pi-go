package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/spa-skyson/pi-rate/internal/config"
)

// defaultPalaceModelPath returns the default embedding model path.
func defaultPalaceModelPath() string {
	return filepath.Join(config.PirateHome(), "models", "sentence-transformers_all-MiniLM-L6-v2")
}

func newMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage the MemPalace memory system",
		Long: `Commands for managing the MemPalace memory system: download embedding models,
initialize palace databases, and view palace status.`,
	}

	cmd.AddCommand(newMemoryModelCmd())
	cmd.AddCommand(newMemoryInitCmd())
	cmd.AddCommand(newMemoryStatusCmd())
	cmd.AddCommand(newMemoryMineCmd())
	cmd.AddCommand(newMemorySearchCmd())
	cmd.AddCommand(newMemoryKGCmd())
	cmd.AddCommand(newMemoryWakeUpCmd())
	cmd.AddCommand(newMemoryRecentCmd())
	cmd.AddCommand(newMemoryClearCmd())

	return cmd
}
