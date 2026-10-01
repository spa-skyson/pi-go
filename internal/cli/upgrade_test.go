package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/spa-skyson/pi-rate/internal/notice"
)

func TestNewUpgradeCmd(t *testing.T) {
	cmd := newUpgradeCmd()
	if cmd.Use != "upgrade" {
		t.Fatalf("Use = %q", cmd.Use)
	}
	if cmd.Short == "" || cmd.Long == "" {
		t.Fatal("expected help text")
	}
	if cmd.RunE == nil {
		t.Fatal("expected RunE")
	}
	if err := cmd.Args(cmd, []string{"extra"}); err == nil {
		t.Fatal("expected NoArgs error")
	}
}

func TestFetchLatestVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Error("expected User-Agent header")
		}
		fmt.Fprint(w, `{"tag_name":"v1.2.3"}`)
	}))
	defer srv.Close()

	version, err := fetchLatestVersion(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("fetchLatestVersion: %v", err)
	}
	if version != "v1.2.3" {
		t.Fatalf("version = %q, want v1.2.3", version)
	}
}

func TestFetchLatestVersionHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	if _, err := fetchLatestVersion(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected HTTP status error")
	}
}

func TestCheckForUpdateSkipsDisabledAndDevVersions(t *testing.T) {
	t.Setenv("PI_GO_UPDATE_CHECK", "0")
	checkForUpdate(context.Background(), "v1.0.0", "`pirate upgrade`")
	checkForUpdate(context.Background(), "", "`pirate upgrade`")
	checkForUpdate(context.Background(), "dev", "`pirate upgrade`")
}

// TestCheckForUpdateNotifiesWithHint pins the startup banner: point the
// release URL at a stub, capture the notice sink (the TUI's chat route), and
// assert the message names both versions and this front end's upgrade command.
func TestCheckForUpdateNotifiesWithHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v9.9.9"}`)
	}))
	defer srv.Close()
	prevURL := latestReleaseURL
	latestReleaseURL = srv.URL
	t.Cleanup(func() { latestReleaseURL = prevURL })

	var got []string
	prevSink := notice.SetSink(func(msg string) { got = append(got, msg) })
	defer func() { notice.SetSink(prevSink) }()

	checkForUpdate(context.Background(), "v1.0.0", "/update")
	if len(got) != 1 {
		t.Fatalf("notices = %v, want exactly one", got)
	}
	want := "⬆ Update available: v1.0.0 → v9.9.9 — run /update to upgrade"
	if got[0] != want {
		t.Errorf("notice = %q, want %q", got[0], want)
	}

	// Same version: silence.
	got = nil
	checkForUpdate(context.Background(), "v9.9.9", "/update")
	if len(got) != 0 {
		t.Errorf("up-to-date produced notices %v, want none", got)
	}
}

// TestNewUpdateChecker pins the /update check callback across its outcomes:
// a newer release is returned, an equal one comes back empty, and the dev
// guard reports the disabled error instead of a version.
func TestNewUpdateChecker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v2.0.0"}`)
	}))
	defer srv.Close()
	prevURL := latestReleaseURL
	latestReleaseURL = srv.URL
	t.Cleanup(func() { latestReleaseURL = prevURL })
	prevVersion := Version
	t.Cleanup(func() { Version = prevVersion })

	Version = "v1.0.0"
	latest, err := newUpdateChecker()(context.Background())
	if err != nil || latest != "v2.0.0" {
		t.Fatalf("checker = (%q, %v), want (v2.0.0, nil)", latest, err)
	}

	Version = "v2.0.0"
	latest, err = newUpdateChecker()(context.Background())
	if err != nil || latest != "" {
		t.Fatalf("up-to-date checker = (%q, %v), want (\"\", nil)", latest, err)
	}

	Version = "dev"
	if _, err := newUpdateChecker()(context.Background()); !errors.Is(err, errUpdateDisabled) {
		t.Fatalf("dev build err = %v, want errUpdateDisabled", err)
	}

	t.Setenv("PI_GO_UPDATE_CHECK", "0")
	Version = "v1.0.0"
	if _, err := newUpdateChecker()(context.Background()); !errors.Is(err, errUpdateDisabled) {
		t.Fatalf("opted-out err = %v, want errUpdateDisabled", err)
	}
}

func TestOutputTail(t *testing.T) {
	if got := outputTail("", 3); got != "" {
		t.Errorf("outputTail(\"\") = %q, want \"\"", got)
	}
	out := "l1\nl2\nl3\nl4\n"
	if got := outputTail(out, 3); got != "l2\nl3\nl4" {
		t.Errorf("outputTail = %q, want the last 3 lines", got)
	}
	if got := outputTail("only\n", 5); got != "only" {
		t.Errorf("outputTail = %q, want %q", got, "only")
	}
}

func TestFetchLatestVersionInvalidURLAndBadJSON(t *testing.T) {
	if _, err := fetchLatestVersion(context.Background(), http.DefaultClient, "://bad-url"); err == nil {
		t.Fatal("expected invalid URL error")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{`)
	}))
	defer srv.Close()
	if _, err := fetchLatestVersion(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestFetchLatestVersionMissingTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	if _, err := fetchLatestVersion(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected missing tag_name error")
	}
}

func TestParseVersionPartsInvalidInputs(t *testing.T) {
	for _, version := range []string{"", "1..2", "1.x.2"} {
		if got := parseVersionParts(version); got != nil {
			t.Fatalf("parseVersionParts(%q) = %v, want nil", version, got)
		}
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{"1.2.3", "v1.2.4", true},
		{"1.2.3+abc123", "v1.3.0", true},
		{"1.2.3", "v1.2.3", false},
		{"1.2.3", "v1.2.2", false},
		{"dev", "v1.2.3", false},
		{"1.10.0", "v1.9.9", false},
	}
	for _, tt := range tests {
		t.Run(tt.current+"/"+tt.latest, func(t *testing.T) {
			if got := isNewerVersion(tt.current, tt.latest); got != tt.want {
				t.Fatalf("isNewerVersion(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
	}
}

// TestRunUpgradePowerShellCommand pins that the Windows upgrade runs PowerShell
// with the install script URL piped into iex — the same one-liner as the
// quickinstall command — rather than downloading the script to a temp file.
func TestRunUpgradePowerShellCommand(t *testing.T) {
	cmd := runUpgradePowerShellCommand("https://example.com/install.ps1")
	if cmd == nil {
		t.Fatal("expected a command")
	}
	// exec.Command resolves Path via LookPath: on Windows that yields a full
	// path (e.g. C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe)
	// while on other platforms the binary can't be resolved, leaving the raw
	// name. Assert on the basename so the test passes on both.
	if filepath.Base(cmd.Path) != "powershell.exe" {
		t.Errorf("Path = %q, want powershell.exe", cmd.Path)
	}
	args := cmd.Args
	if len(args) < 4 {
		t.Fatalf("args = %v, want -NoProfile -Command <iwr ... | iex>", args)
	}
	if args[1] != "-NoProfile" || args[2] != "-Command" {
		t.Errorf("args = %v, want -NoProfile -Command", args)
	}
	want := "iwr https://example.com/install.ps1 -UseBasicParsing | iex"
	if args[3] != want {
		t.Errorf("command = %q, want %q", args[3], want)
	}
}
