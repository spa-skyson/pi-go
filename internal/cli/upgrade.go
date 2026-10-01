package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/spa-skyson/pi-rate/internal/notice"
)

const upgradeScriptURL = "https://raw.githubusercontent.com/spa-skyson/pi-rate/main/scripts/install.sh"
const upgradeScriptURLWin = "https://raw.githubusercontent.com/spa-skyson/pi-rate/main/scripts/install.ps1"

// latestReleaseURL is a var so tests can point it at an httptest server.
var latestReleaseURL = "https://api.github.com/repos/spa-skyson/pi-rate/releases/latest"

type releaseInfo struct {
	TagName string `json:"tag_name"`
}

// updateCheckAllowed reports whether an update check may run at all: dev
// builds have no release version to compare against, and PI_GO_UPDATE_CHECK=0
// opts the host out entirely.
func updateCheckAllowed(version string) bool {
	return version != "" && version != "dev" && os.Getenv("PI_GO_UPDATE_CHECK") != "0"
}

// checkForUpdate fetches the newest release and raises a notice when it is
// newer than the running build. hint names the upgrade command for this front
// end ("`pirate upgrade`" on the CLI, "/update" in the TUI).
func checkForUpdate(ctx context.Context, currentVersion, hint string) {
	if !updateCheckAllowed(currentVersion) {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	latest, err := fetchLatestVersion(ctx, http.DefaultClient, latestReleaseURL)
	if err != nil {
		return
	}
	if isNewerVersion(currentVersion, latest) {
		notice.Notifyf("⬆ Update available: %s → %s — run %s to upgrade", currentVersion, latest, hint)
	}
}

// errUpdateDisabled marks a check that cannot run: dev build or opted-out
// host. The startup check swallows it; /update surfaces it.
var errUpdateDisabled = errors.New("update checks are disabled for dev builds")

// newUpdateChecker builds the TUI's update-check callback: the newest release
// tag when it is newer than the running build, "" when up to date. Errors are
// returned, not swallowed — /update reports them in the chat.
func newUpdateChecker() func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if !updateCheckAllowed(Version) {
			return "", errUpdateDisabled
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		latest, err := fetchLatestVersion(ctx, http.DefaultClient, latestReleaseURL)
		if err != nil {
			return "", err
		}
		if !isNewerVersion(Version, latest) {
			return "", nil
		}
		return latest, nil
	}
}

// outputTail keeps the last maxLines lines of captured installer output, for
// an error notice that names what actually went wrong.
func outputTail(out string, maxLines int) string {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return ""
	}
	lines := strings.Split(out, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}

// newUpdateInstaller builds the TUI's apply-update callback. The script runs
// with its output captured — the TUI owns the terminal, so the script must
// never write to it (AGENTS.md, TUI output safety) — and on failure the error
// carries the tail of what it printed.
func newUpdateInstaller() func(context.Context) error {
	return func(ctx context.Context) error {
		var out bytes.Buffer
		if err := runUpgradeScript(ctx, &out, &out); err != nil {
			return fmt.Errorf("%s: %w", outputTail(out.String(), 10), err)
		}
		return nil
	}
}

func fetchLatestVersion(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "pirate/"+versionString())

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest release returned HTTP %d", resp.StatusCode)
	}

	var release releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("decoding latest release: %w", err)
	}
	if release.TagName == "" {
		return "", fmt.Errorf("latest release missing tag_name")
	}
	return release.TagName, nil
}

func isNewerVersion(current, latest string) bool {
	currentParts := parseVersionParts(current)
	latestParts := parseVersionParts(latest)
	if len(currentParts) == 0 || len(latestParts) == 0 {
		return false
	}
	maxLen := len(currentParts)
	if len(latestParts) > maxLen {
		maxLen = len(latestParts)
	}
	for i := 0; i < maxLen; i++ {
		currentPart := 0
		if i < len(currentParts) {
			currentPart = currentParts[i]
		}
		latestPart := 0
		if i < len(latestParts) {
			latestPart = latestParts[i]
		}
		if latestPart != currentPart {
			return latestPart > currentPart
		}
	}
	return false
}

func parseVersionParts(version string) []int {
	version = normalizeVersion(version)
	if version == "" {
		return nil
	}
	parts := strings.Split(version, ".")
	parsed := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		parsed = append(parsed, n)
	}
	return parsed
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	if i := strings.Index(version, "+"); i >= 0 {
		version = version[:i]
	}
	return version
}

func newUpgradeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade pi-go to the latest version",
		Long: `Downloads and runs the official install script to upgrade pi-go to the latest version.

The script detects your platform and installs the binary to the appropriate location.
Run with sudo if the default location requires elevated permissions.`,
		Args: cobra.NoArgs,
		RunE: runUpgrade,
	}
	return cmd
}

func runUpgrade(cmd *cobra.Command, _ []string) error {
	fmt.Fprintln(os.Stderr, "Upgrading pi-go...")
	if runtime.GOOS == "windows" {
		fmt.Fprintln(os.Stderr, "Detected Windows — using PowerShell install script.")
	}
	// The CLI owns the terminal: the script's progress goes straight to it.
	return runUpgradeScript(cmd.Context(), os.Stdout, os.Stderr)
}

// runUpgradeScript downloads and runs the official install script. ctx bounds
// the run; out and errOut receive the script's streams — the CLI wires the
// terminal, the TUI captures both into one buffer.
func runUpgradeScript(ctx context.Context, out, errOut io.Writer) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = runUpgradePowerShellCommand(upgradeScriptURLWin)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("curl -fsSL %s | bash", upgradeScriptURL))
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = out
	cmd.Stderr = errOut
	return cmd.Run()
}

// runUpgradePowerShellCommand builds the PowerShell invocation that upgrades
// pi-go on Windows. It runs the install script straight from the GitHub URL,
// the same way the quickinstall one-liner does — iwr the script and pipe it
// into iex — rather than downloading it to a temp file. Kept separate from
// runUpgradePowerShell so the command shape is testable without executing it.
func runUpgradePowerShellCommand(scriptURL string) *exec.Cmd {
	return exec.Command("powershell.exe", "-NoProfile", "-Command",
		fmt.Sprintf("iwr %s -UseBasicParsing | iex", scriptURL))
}
