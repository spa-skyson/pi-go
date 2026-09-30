package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/spf13/cobra"

	a2aserver "github.com/spa-skyson/pi-rate/internal/a2a/server"
	acpserver "github.com/spa-skyson/pi-rate/internal/acp/server"
	"github.com/spa-skyson/pi-rate/internal/config"
)

func newA2AServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "a2a",
		Short: "Run pirate as an A2A agent over HTTP",
		Long: `Run pirate as an A2A (Agent-to-Agent) agent that communicates with an
external A2A client over HTTP JSON-RPC. Use this when another agent or a kagent
harness drives pirate through the A2A protocol. The server serves an agent card at
/.well-known/agent-card.json and accepts A2A message requests on the root path.
The server returns when the process receives SIGINT.`,
		Args: cobra.NoArgs,
		RunE: runA2AServer,
	}
	cmd.Flags().StringVar(&flagModel, "model", "", "LLM model to use for A2A prompt handling")
	cmd.Flags().StringVar(&flagURL, "url", "", "Alternative base URL for the LLM API endpoint")
	cmd.Flags().StringArrayVar(&flagHeaders, "header", nil, "Extra HTTP header for LLM requests (key=value, repeatable)")
	if f := cmd.Flags().Lookup("header"); f != nil {
		f.NoOptDefVal = ""
	}
	cmd.Flags().BoolVar(&flagInsecure, "insecure", false, "Skip TLS certificate verification for LLM API calls")
	cmd.Flags().StringVar(&flagA2AAddr, "addr", "", "HTTP/gRPC listen address (default $PORT or :8085)")
	cmd.Flags().StringVar(&flagA2AReadyAddr, "ready-addr", ":8081", "Readiness listen address")
	return cmd
}

func runA2AServer(cmd *cobra.Command, _ []string) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()

	// Create error log file for server failures in the Pi-rate home sessions dir.
	logDir := filepath.Join(config.PirateHome(), "sessions")
	errFile := filepath.Join(logDir, "a2a-server.err.log")
	_ = os.MkdirAll(logDir, 0o755)
	f, err := os.OpenFile(errFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		defer f.Close()
	}

	// Build a logger that writes INFO+ to the err log file for crash RCA.
	var logger *slog.Logger
	if f != nil {
		logger = slog.New(&logHandler{f: f})
	}

	model := flagModel
	if model == "" {
		model = os.Getenv("PI_MODEL")
	}
	if model == "" {
		model = "glm-5.2:cloud"
	}

	baseURL := flagURL
	if baseURL == "" {
		baseURL = os.Getenv("PI_BASE_URL")
	}
	system := flagSystem
	if system == "" {
		system = os.Getenv("PI_SYSTEM")
	}

	// The A2A context id is the pirate session id, and the transcript behind it
	// is persisted, so a conversation survives Substrate replacing the actor:
	// the next message on the same context resumes it from the durable dir.
	rt := acpserver.RuntimeConfig{
		Model:    model,
		BaseURL:  baseURL,
		Headers:  flagHeaders,
		Insecure: flagInsecure,
		System:   system,
		Version:  versionString(),
	}
	if sessionSvc := openServerSessionStore(ctx, logger); sessionSvc != nil {
		rt.SessionService = sessionSvc
	}
	handler := acpserver.NewPromptHandler(rt)

	addr := flagA2AAddr
	if addr == "" {
		addr = ":" + os.Getenv("PORT")
		if os.Getenv("PORT") == "" {
			addr = ":8085"
		}
	}

	if err := a2aserver.Serve(ctx, a2aserver.ServeConfig{
		Addr:      addr,
		ReadyAddr: flagA2AReadyAddr,
		Handler:   handler,
		Logger:    logger,
	}); err != nil {
		if f != nil {
			_, _ = io.WriteString(f, fmt.Sprintf("%v\n", err))
		}
		return fmt.Errorf("a2a server: %w", err)
	}
	return nil
}
