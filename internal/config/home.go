package config

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ProjectDirName is the per-project data directory (".pirate"), used for
// project-scoped config.json, mcp.json, .env, agents, skills, sops and palace
// databases. Paths are always built as filepath.Join(dir, ProjectDirName, ...).
const ProjectDirName = ".pirate"

// legacyProjectDirName is the pre-rename project directory, still readable as
// migration input but never written.
const (
	legacyProjectDirName = ".pi-go"
	legacyHomeDirName    = ".pi-go"
)

// PirateHome returns the Pi-rate home directory: $PIRATE_HOME if set,
// otherwise $PI_GO_HOME (legacy alias, kept for compatibility with existing
// setups), otherwise $HOME/.pirate.
func PirateHome() string {
	if p := os.Getenv("PIRATE_HOME"); p != "" {
		return p
	}
	if p := os.Getenv("PI_GO_HOME"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return filepath.Join(home, ProjectDirName)
}

// LegacyHome returns the pre-rename home directory ($PI_GO_HOME or
// $HOME/.pi-go). It is only read by MigrateLegacyHome.
func LegacyHome() string {
	if p := os.Getenv("PI_GO_HOME"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, legacyHomeDirName)
}

// MigrateLegacyHome copies a legacy ~/.pi-go directory to ~/.pirate on first
// run: if the legacy home exists and the new home carries no real content yet,
// its contents (files, subdirectories, symlinks) are copied, a MIGRATED.txt
// marker is written and a note goes to stderr. The legacy directory is left
// untouched. Copy errors are warnings, not failures: migration proceeds with
// whatever copied.
//
// "No real content" means the new home does not exist, or exists without any
// of config.json, MIGRATED.txt, agents/ and skills/. Those four appear only
// when a migration has already run or a user has actually configured the tool,
// so finding any of them marks the home as populated and the migration is a
// no-op — a second start never re-copies. A home that exists but holds only
// incidental state (log/, memory/ or sessions/ created by a component that
// started earlier in the same process, or an empty directory) is treated as
// fresh: the copy proceeds into it and merges alongside what is already
// there. This keeps first-run migration working even when something created
// the home directory before this call.
func MigrateLegacyHome() {
	dst := PirateHome()
	src := LegacyHome()
	if src == "" || dst == src {
		return
	}
	if newHomePopulated(dst) {
		return // user already has a real new home; never touch it
	}
	if _, err := os.Lstat(src); err != nil {
		return // nothing to migrate
	}

	warn := func(err error) {
		fmt.Fprintf(os.Stderr, "pirate: warning: migration from %s incomplete: %v\n", src, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		warn(err)
		return
	}
	if err := copyDir(src, dst, warn); err != nil {
		warn(err)
	}

	// Best effort: partial copies are still useful, and the marker records
	// where the data came from either way.
	marker := fmt.Sprintf("migrated from ~/.pi-go at %s; old directory left untouched\n",
		time.Now().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(dst, "MIGRATED.txt"), []byte(marker), 0o644); err != nil {
		warn(err)
	}
	fmt.Fprintf(os.Stderr, "pirate: migrated configuration from ~/.pi-go to ~/.pirate (old directory kept)\n")
}

// newHomePopulated reports whether dst already carries real new-home content:
// any of config.json, MIGRATED.txt, agents/ or skills/. A missing directory,
// or one holding only incidental state (log/, memory/, sessions/ created by
// an early-starting component), is not populated.
func newHomePopulated(dst string) bool {
	for _, rel := range []string{"config.json", "MIGRATED.txt", "agents", "skills"} {
		if _, err := os.Lstat(filepath.Join(dst, rel)); err == nil {
			return true
		}
	}
	return false
}

// copyDir recursively copies src's entries into dst. Symlinks are recreated as
// symlinks (never followed), live SQLite edge files (*.db-shm, *.db-wal) are
// skipped — the database itself copies as a plain file. errFn receives copy
// errors and migration continues with what did copy.
func copyDir(src, dst string, errFn func(error)) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			errFn(err)
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			errFn(err)
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				errFn(err)
				return nil
			}
			if err := os.Symlink(link, target); err != nil && !os.IsExist(err) {
				errFn(err)
			}
		case d.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				errFn(err)
			}
		default:
			name := d.Name()
			if strings.HasSuffix(name, ".db-shm") || strings.HasSuffix(name, ".db-wal") {
				return nil
			}
			if err := copyFile(path, target, d); err != nil {
				errFn(err)
			}
		}
		return nil
	})
}

// copyFile copies one regular file, preserving its permission bits.
func copyFile(src, dst string, d fs.DirEntry) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	mode := fs.FileMode(0o644)
	if info, err := d.Info(); err == nil {
		mode = info.Mode().Perm()
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
