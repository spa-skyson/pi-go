package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// isolateHome points os.UserHomeDir at a fresh directory so the no-env
// fallbacks of PirateHome/LegacyHome never read the developer's real home.
// Both env vars are set because os.UserHomeDir consults HOME on Unix and
// USERPROFILE on Windows.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestPirateHome_Precedence(t *testing.T) {
	tests := []struct {
		name       string
		pirateHome string
		piGoHome   string
		want       string
	}{
		{"PIRATE_HOME wins", "/tmp/p", "/tmp/legacy", "/tmp/p"},
		{"PI_GO_HOME is the fallback", "", "/tmp/legacy", "/tmp/legacy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			t.Setenv("PIRATE_HOME", tt.pirateHome)
			t.Setenv("PI_GO_HOME", tt.piGoHome)
			if got := PirateHome(); got != tt.want {
				t.Errorf("PirateHome() = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("no env falls back to $HOME/.pirate", func(t *testing.T) {
		home := isolateHome(t)
		t.Setenv("PIRATE_HOME", "")
		t.Setenv("PI_GO_HOME", "")
		want := filepath.Join(home, ".pirate")
		if got := PirateHome(); got != want {
			t.Errorf("PirateHome() = %q, want %q", got, want)
		}
	})
}

func TestLegacyHome(t *testing.T) {
	t.Run("PI_GO_HOME wins", func(t *testing.T) {
		isolateHome(t)
		t.Setenv("PI_GO_HOME", "/tmp/legacy")
		if got := LegacyHome(); got != "/tmp/legacy" {
			t.Errorf("LegacyHome() = %q, want /tmp/legacy", got)
		}
	})

	t.Run("no env falls back to $HOME/.pi-go", func(t *testing.T) {
		home := isolateHome(t)
		t.Setenv("PI_GO_HOME", "")
		want := filepath.Join(home, ".pi-go")
		if got := LegacyHome(); got != want {
			t.Errorf("LegacyHome() = %q, want %q", got, want)
		}
	})
}

// migrateFixture builds a legacy home with one of every entry type the copier
// must handle and returns the legacy and new home paths.
func migrateFixture(t *testing.T) (legacy, new string) {
	t.Helper()
	root := t.TempDir()
	legacy = filepath.Join(root, "legacy")
	new = filepath.Join(root, "new")
	for _, dir := range []string{
		filepath.Join(legacy, "sessions"),
		filepath.Join(legacy, "memory"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(legacy, "config.json"), `{"model":"gpt"}`)
	write(filepath.Join(legacy, "sessions", "s1.json"), "{}")
	write(filepath.Join(legacy, "memory", "claude-mem.db"), "db-bytes")
	write(filepath.Join(legacy, "memory", "claude-mem.db-shm"), "stale-shm")
	write(filepath.Join(legacy, "memory", "claude-mem.db-wal"), "stale-wal")
	// A symlink must be recreated as a symlink, never followed.
	if err := os.Symlink("config.json", filepath.Join(legacy, "config-link.json")); err != nil {
		t.Fatal(err)
	}
	return legacy, new
}

func TestMigrateLegacyHome(t *testing.T) {
	t.Run("copies files, subdirectories and symlinks, skips live sqlite edge files, writes marker", func(t *testing.T) {
		legacy, new := migrateFixture(t)
		t.Setenv("PIRATE_HOME", new)
		t.Setenv("PI_GO_HOME", legacy)

		MigrateLegacyHome()

		// Regular files and a nested subdirectory.
		for _, rel := range []string{
			"config.json",
			filepath.Join("sessions", "s1.json"),
			filepath.Join("memory", "claude-mem.db"),
		} {
			b, err := os.ReadFile(filepath.Join(new, rel))
			if err != nil {
				t.Fatalf("copied file %s missing: %v", rel, err)
			}
			if len(b) == 0 {
				t.Errorf("copied file %s is empty", rel)
			}
		}
		// Live SQLite edge files are skipped.
		for _, rel := range []string{
			filepath.Join("memory", "claude-mem.db-shm"),
			filepath.Join("memory", "claude-mem.db-wal"),
		} {
			if _, err := os.Lstat(filepath.Join(new, rel)); !os.IsNotExist(err) {
				t.Errorf("%s should have been skipped, got err=%v", rel, err)
			}
		}
		// The symlink is recreated as a symlink pointing at the same target.
		link := filepath.Join(new, "config-link.json")
		fi, err := os.Lstat(link)
		if err != nil {
			t.Fatalf("symlink not copied: %v", err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("config-link.json is not a symlink: mode=%v", fi.Mode())
		}
		if target, err := os.Readlink(link); err != nil || target != "config.json" {
			t.Errorf("symlink target = %q (err=%v), want config.json", target, err)
		}
		// Marker written.
		marker, err := os.ReadFile(filepath.Join(new, "MIGRATED.txt"))
		if err != nil {
			t.Fatalf("MIGRATED.txt not written: %v", err)
		}
		if len(marker) == 0 {
			t.Error("MIGRATED.txt is empty")
		}
		// Legacy directory left untouched.
		if _, err := os.Stat(filepath.Join(legacy, "config.json")); err != nil {
			t.Errorf("legacy config.json no longer there: %v", err)
		}
	})

	t.Run("second run is a no-op and never re-copies", func(t *testing.T) {
		legacy, new := migrateFixture(t)
		t.Setenv("PIRATE_HOME", new)
		t.Setenv("PI_GO_HOME", legacy)

		MigrateLegacyHome()

		// Remove a copied file and deface the marker; a re-copy would
		// restore both.
		copied := filepath.Join(new, "config.json")
		if err := os.Remove(copied); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(new, "MIGRATED.txt"), []byte("defaced"), 0o644); err != nil {
			t.Fatal(err)
		}

		MigrateLegacyHome()

		if _, err := os.Stat(copied); !os.IsNotExist(err) {
			t.Error("config.json was re-copied on the second run; migration is not idempotent")
		}
		b, err := os.ReadFile(filepath.Join(new, "MIGRATED.txt"))
		if err != nil || string(b) != "defaced" {
			t.Errorf("MIGRATED.txt was rewritten on the second run: %q err=%v", b, err)
		}
	})

	t.Run("both homes exist is a no-op", func(t *testing.T) {
		legacy, new := migrateFixture(t)
		if err := os.MkdirAll(new, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PIRATE_HOME", new)
		t.Setenv("PI_GO_HOME", legacy)
		sentinel := filepath.Join(new, "user-data.txt")
		if err := os.WriteFile(sentinel, []byte("mine"), 0o644); err != nil {
			t.Fatal(err)
		}

		MigrateLegacyHome()

		if _, err := os.Stat(sentinel); err != nil {
			t.Errorf("existing new home was touched: %v", err)
		}
		if _, err := os.Stat(filepath.Join(new, "config.json")); !os.IsNotExist(err) {
			t.Error("legacy content was copied into an existing new home")
		}
		if _, err := os.Stat(filepath.Join(new, "MIGRATED.txt")); !os.IsNotExist(err) {
			t.Error("MIGRATED.txt written for a no-op migration")
		}
	})

	t.Run("only the new home exists is a no-op", func(t *testing.T) {
		new := filepath.Join(t.TempDir(), "new")
		if err := os.MkdirAll(new, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PIRATE_HOME", new)
		t.Setenv("PI_GO_HOME", filepath.Join(t.TempDir(), "absent-legacy"))

		MigrateLegacyHome()

		if _, err := os.Stat(filepath.Join(new, "MIGRATED.txt")); !os.IsNotExist(err) {
			t.Error("MIGRATED.txt written although there was nothing to migrate")
		}
	})

	t.Run("PIRATE_HOME equal to PI_GO_HOME is a no-op", func(t *testing.T) {
		same := t.TempDir()
		t.Setenv("PIRATE_HOME", same)
		t.Setenv("PI_GO_HOME", same)

		MigrateLegacyHome() // must not recurse into itself or write a marker

		if _, err := os.Stat(filepath.Join(same, "MIGRATED.txt")); !os.IsNotExist(err) {
			t.Error("MIGRATED.txt written when home and legacy home are the same")
		}
	})

	t.Run("unreadable file does not abort the migration", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("permission-bit trick does not apply on Windows")
		}
		if os.Getuid() == 0 {
			t.Skip("root can read files regardless of permission bits")
		}
		legacy, new := migrateFixture(t)
		locked := filepath.Join(legacy, "unreadable.db")
		if err := os.WriteFile(locked, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(locked, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o600) })
		t.Setenv("PIRATE_HOME", new)
		t.Setenv("PI_GO_HOME", legacy)

		MigrateLegacyHome()

		if _, err := os.Stat(filepath.Join(new, "config.json")); err != nil {
			t.Errorf("migration stopped at the unreadable file: %v", err)
		}
		if _, err := os.Stat(filepath.Join(new, "MIGRATED.txt")); err != nil {
			t.Errorf("marker not written after a partial copy: %v", err)
		}
	})
}
