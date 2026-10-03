package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSetHomeIsScopedToTheTest(t *testing.T) {
	before, beforeErr := os.UserHomeDir()

	dir := t.TempDir()
	t.Run("inside", func(t *testing.T) {
		SetHome(t, dir)
		got, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("UserHomeDir: %v", err)
		}
		if got != dir {
			t.Errorf("UserHomeDir = %q, want %q", got, dir)
		}
	})

	after, afterErr := os.UserHomeDir()
	if after != before || (afterErr == nil) != (beforeErr == nil) {
		t.Errorf("home after the subtest = %q (%v), want it restored to %q (%v)", after, afterErr, before, beforeErr)
	}
}

func TestSetUnwritableHomeBlocksCreationBelowIt(t *testing.T) {
	home := SetUnwritableHome(t)

	info, err := os.Stat(home)
	if err != nil {
		t.Fatalf("stat %s: %v", home, err)
	}
	if info.IsDir() {
		t.Fatalf("%s is a directory, want a regular file standing in for HOME", home)
	}
	if got, _ := os.UserHomeDir(); got != home {
		t.Errorf("UserHomeDir = %q, want %q", got, home)
	}
	if err := os.MkdirAll(filepath.Join(home, ".pirate"), 0o755); err == nil {
		t.Error("MkdirAll below the file succeeded, want an error")
	}
}

func TestRequireShellReturnsARunnableShell(t *testing.T) {
	sh := RequireShell(t)
	if out, err := exec.Command(sh, "-c", "echo ok").Output(); err != nil || string(out) != "ok\n" {
		t.Errorf("%s -c 'echo ok' = %q, %v", sh, out, err)
	}
}

func TestRequireShellSkipsWithoutOne(t *testing.T) {
	// An empty PATH makes LookPath fail for bash and sh alike, so the helper
	// must skip rather than return a path that cannot run.
	t.Setenv("PATH", "")
	sh := RequireShell(t)
	t.Errorf("RequireShell returned %q with an empty PATH, want the test skipped", sh)
}

func TestUnsetHomeLeavesNothingToResolve(t *testing.T) {
	UnsetHome(t)
	if home, err := os.UserHomeDir(); err == nil {
		t.Errorf("UserHomeDir = %q, want an error with HOME unset", home)
	}
}

func TestFakeBinaryRuns(t *testing.T) {
	dir := t.TempDir()
	bin := FakeBinary(t, dir, "tool")
	if filepath.Dir(bin) != dir {
		t.Errorf("binary written to %q, want it under %q", bin, dir)
	}
	run := exec.Command(bin)
	if runtime.GOOS == "windows" {
		// A .bat is not a PE image; it runs through the command interpreter.
		run = exec.Command("cmd", "/c", bin)
	}
	if err := run.Run(); err != nil {
		t.Errorf("running %s: %v", bin, err)
	}
	// It must also be found by name through PATH, which is what callers that
	// put dir on PATH rely on.
	t.Setenv("PATH", dir)
	if _, err := exec.LookPath("tool"); err != nil {
		t.Errorf("LookPath(tool) with PATH=%s: %v", dir, err)
	}
}

// setDirtyEnv plants a recognizable value in every registered dirty variable
// plus a few that only the pattern sweep can catch. All through t.Setenv, so
// whatever a test below forgets to restore comes back at cleanup anyway.
func setDirtyEnv(t *testing.T) {
	for _, name := range dirtyEnvVars {
		t.Setenv(name, "dirty-"+name)
	}
	t.Setenv("FUTUREPROVIDER_API_KEY", "dirty-future-key")  // sweep: _API_KEY
	t.Setenv("FUTUREPROVIDER_BASE_URL", "dirty-future-url") // sweep: _BASE_URL
	t.Setenv("PI_FUTURE_KNOB", "dirty-future-knob")         // sweep: PI_*
	t.Setenv("NOT_A_DIRTY_VAR", "keep-me")                  // must survive
}

func TestSanitizeEnvRemovesEveryDirtyVar(t *testing.T) {
	setDirtyEnv(t)

	SanitizeEnv(t)

	for _, name := range dirtyEnvVars {
		if _, ok := os.LookupEnv(name); ok {
			t.Errorf("%s survived SanitizeEnv, want it unset", name)
		}
	}
	for _, name := range []string{"FUTUREPROVIDER_API_KEY", "FUTUREPROVIDER_BASE_URL", "PI_FUTURE_KNOB"} {
		if _, ok := os.LookupEnv(name); ok {
			t.Errorf("%s survived SanitizeEnv, want the pattern sweep to unset it", name)
		}
	}
	if got := os.Getenv("NOT_A_DIRTY_VAR"); got != "keep-me" {
		t.Errorf("NOT_A_DIRTY_VAR = %q, want it left alone", got)
	}
}

func TestSanitizeEnvRestoresThePreviousState(t *testing.T) {
	before, beforeOK := os.LookupEnv("ANTHROPIC_API_KEY")

	// SanitizeEnv restores at cleanup, which runs when the subtest returns —
	// so the restoration is only observable here, in the parent.
	t.Run("inside", func(t *testing.T) {
		setDirtyEnv(t)
		SanitizeEnv(t)
		if _, ok := os.LookupEnv("ANTHROPIC_API_KEY"); ok {
			t.Error("ANTHROPIC_API_KEY still set inside the test, want it sanitized")
		}
	})

	after, afterOK := os.LookupEnv("ANTHROPIC_API_KEY")
	if afterOK != beforeOK || after != before {
		t.Errorf("after the test ANTHROPIC_API_KEY = %q (set: %v), want restored to %q (set: %v)",
			after, afterOK, before, beforeOK)
	}
}

func TestUnsetEnvRemovesDirtyVarsProcessWide(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "dirty-key")
	t.Setenv("PI_AGENT_ID", "dirty-agent")

	UnsetEnv()

	for _, name := range []string{"ANTHROPIC_API_KEY", "PI_AGENT_ID"} {
		if _, ok := os.LookupEnv(name); ok {
			t.Errorf("%s survived UnsetEnv, want it unset process-wide", name)
		}
	}
}

func TestTempPirateHomePointsAtAFreshDirectory(t *testing.T) {
	dir := TempPirateHome(t)

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %s: %v", dir, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", dir)
	}
	for _, name := range []string{"PIRATE_HOME", "PI_GO_HOME"} {
		if got := os.Getenv(name); got != dir {
			t.Errorf("%s = %q, want %q", name, got, dir)
		}
	}
}
