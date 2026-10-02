package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeThemeFile writes content at path, creating parent directories.
func writeThemeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newCustomThemeManager returns a manager with custom themes loaded from a
// temporary PIRATE_HOME and the given working directory.
func newCustomThemeManager(t *testing.T, home, cwd string) *ThemeManager {
	t.Helper()
	t.Setenv("PIRATE_HOME", home)
	tm := NewThemeManager()
	tm.LoadCustomThemes(cwd)
	return tm
}

func TestCustomThemeDirsWalkUp(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIRATE_HOME", filepath.Join(root, "home"))

	projectThemes := filepath.Join(root, "a", "b", ".pirate", "themes")
	writeThemeFile(t, filepath.Join(projectThemes, "x.json"), `{}`)

	want := projectThemes
	for _, cwd := range []string{
		filepath.Join(root, "a", "b", "c"), // nested below
		filepath.Join(root, "a", "b"),      // the directory itself
	} {
		dirs := customThemeDirs(cwd)
		found := false
		for _, d := range dirs {
			if d == want {
				found = true
			}
		}
		if !found {
			t.Errorf("customThemeDirs(%q) = %v, missing %q", cwd, dirs, want)
		}
		if dirs[0] != filepath.Join(root, "home", "themes") {
			t.Errorf("customThemeDirs(%q): global dir must come first, got %v", cwd, dirs)
		}
	}

	// Outside the project tree only the global dir is found.
	if dirs := customThemeDirs(root); len(dirs) != 1 {
		t.Errorf("customThemeDirs(outside) = %v, want only the global dir", dirs)
	}
}

func TestLoadCustomThemesProjectBeatsGlobal(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeThemeFile(t, filepath.Join(home, "themes", "x.json"),
		`{"displayName":"Global X","colors":{"text":"#111111","base":"#222222"}}`)
	project := filepath.Join(root, "proj")
	writeThemeFile(t, filepath.Join(project, ".pirate", "themes", "x.json"),
		`{"displayName":"Project X","colors":{"text":"#333333"}}`)

	tm := newCustomThemeManager(t, home, project)

	if !tm.HasTheme("x") {
		t.Fatal("custom theme x not loaded")
	}
	tm.SetTheme("x")
	colors := tm.Colors()
	if colors.Text != "#333333" {
		t.Errorf("Text = %q, want project value #333333", colors.Text)
	}
	// Whole-file priority: the project file replaces the global one, and its
	// missing roles come from the default theme, not from the global file.
	if colors.Base != "#1a1b26" {
		t.Errorf("Base = %q, want default #1a1b26 (project file replaces global)", colors.Base)
	}
	if got := tm.Current().DisplayName; got != "Project X" {
		t.Errorf("DisplayName = %q, want Project X", got)
	}

	// Without the project file the global one applies.
	if err := os.Remove(filepath.Join(project, ".pirate", "themes", "x.json")); err != nil {
		t.Fatal(err)
	}
	tm2 := newCustomThemeManager(t, home, project)
	if err := tm2.SetTheme("x"); err != nil {
		t.Fatalf("global x not loaded: %v", err)
	}
	if got := tm2.Colors().Text; got != "#111111" {
		t.Errorf("Text = %q, want global value #111111", got)
	}
}

func TestLoadCustomThemesOverridesBuiltin(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeThemeFile(t, filepath.Join(home, "themes", "dracula.json"),
		`{"colors":{"text":"#123456"}}`)

	tm := newCustomThemeManager(t, home, root)

	var builtin Theme
	var raw map[string]Theme
	if err := json.Unmarshal(themesJSON, &raw); err != nil {
		t.Fatal(err)
	}
	builtin = raw["dracula"]
	if builtin.Name == "" {
		t.Fatal("embedded dracula not found")
	}

	tm.SetTheme("dracula")
	colors := tm.Colors()
	if colors.Text != "#123456" {
		t.Errorf("Text = %q, want custom override #123456", colors.Text)
	}
	if colors.Base != builtin.Colors.Base {
		t.Errorf("Base = %q, want built-in dracula %q (partial merge)", colors.Base, builtin.Colors.Base)
	}
	if got := tm.Current().DisplayName; got != builtin.DisplayName {
		t.Errorf("DisplayName = %q, want built-in %q", got, builtin.DisplayName)
	}
	if !tm.Current().Custom {
		t.Error("overriding theme should be marked Custom")
	}
}

func TestLoadCustomThemesPartialUnknownFilledFromDefault(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeThemeFile(t, filepath.Join(home, "themes", "fresh.json"),
		`{"displayName":"Fresh","colors":{"text":"#abcdef"}}`)

	tm := newCustomThemeManager(t, home, root)

	if err := tm.SetTheme("fresh"); err != nil {
		t.Fatalf("SetTheme(fresh): %v", err)
	}
	colors := tm.Colors()
	if colors.Text != "#abcdef" {
		t.Errorf("Text = %q, want #abcdef", colors.Text)
	}
	var def map[string]Theme
	if err := json.Unmarshal(themesJSON, &def); err != nil {
		t.Fatal(err)
	}
	want := def[DefaultThemeName].Colors
	if colors.Base != want.Base || colors.Primary != want.Primary || colors.Error != want.Error {
		t.Errorf("partial theme not filled from default: got base=%q primary=%q error=%q, want %q/%q/%q",
			colors.Base, colors.Primary, colors.Error, want.Base, want.Primary, want.Error)
	}
	// 13 legacy roles are always filled; the extended roles fill from the
	// theme's own colors, except accent, which has no legacy carrier and
	// stays empty (the palette default applies) — 13 + 10 = 23.
	if n := colors.colorRoleCount(); n != 23 {
		t.Errorf("colorRoleCount() = %d, want 23 after merge", n)
	}
}

func TestLoadCustomThemesBrokenFileWarns(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeThemeFile(t, filepath.Join(home, "themes", "bad.json"), `{not json`)

	tm := newCustomThemeManager(t, home, root)

	warnings := tm.LoadCustomThemes(filepath.Join(root, "elsewhere"))
	if len(warnings) != 1 || !strings.Contains(warnings[0], "bad.json") {
		t.Fatalf("warnings = %v, want one naming bad.json", warnings)
	}
	// The manager stays alive: built-ins intact and selectable.
	before := tm.ThemeCount()
	if err := tm.SetTheme("dracula"); err != nil {
		t.Errorf("SetTheme after broken custom file: %v", err)
	}
	if tm.ThemeCount() != before {
		t.Errorf("ThemeCount changed after broken file: %d -> %d", before, tm.ThemeCount())
	}
	if tm.HasTheme("bad") {
		t.Error("broken file must not register a theme")
	}
}

func TestLoadCustomThemesEmptySkipped(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeThemeFile(t, filepath.Join(home, "themes", "empty.json"),
		`{"displayName":"Empty","themeType":"dark","colors":{}}`)

	tm := newCustomThemeManager(t, home, root)

	if tm.HasTheme("empty") {
		t.Error("theme without color roles must be skipped")
	}
	warnings := tm.LoadCustomThemes(root)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "no color roles") {
		t.Fatalf("warnings = %v, want a no-color-roles warning", warnings)
	}
}

func TestLoadCustomThemesRescanDropsRemoved(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	themePath := filepath.Join(home, "themes", "gone.json")
	writeThemeFile(t, themePath, `{"colors":{"text":"#101010"}}`)

	tm := newCustomThemeManager(t, home, root)
	if err := tm.SetTheme("gone"); err != nil {
		t.Fatalf("SetTheme(gone): %v", err)
	}

	if err := os.Remove(themePath); err != nil {
		t.Fatal(err)
	}
	tm.LoadCustomThemes(root)

	if tm.HasTheme("gone") {
		t.Error("removed custom theme should disappear on re-scan")
	}
	if got := tm.CurrentName(); got != DefaultThemeName {
		t.Errorf("CurrentName = %q, want fallback %q", got, DefaultThemeName)
	}
}

func TestThemeListMarksCustom(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeThemeFile(t, filepath.Join(home, "themes", "mine.json"),
		`{"colors":{"text":"#999999"}}`)

	tm := newCustomThemeManager(t, home, root)

	var custom *Theme
	for _, th := range tm.List() {
		if th.Name == "mine" {
			custom = &th
		} else if th.Custom {
			t.Errorf("built-in theme %q must not be marked custom", th.Name)
		}
	}
	if custom == nil {
		t.Fatal("custom theme missing from List()")
	}
	if !custom.Custom {
		t.Error("custom theme not marked Custom")
	}

	out := formatThemeList(tm.List(), tm.CurrentName(), darkPalette)
	if !strings.Contains(out, "(custom)") {
		t.Error("theme list should tag custom themes with (custom)")
	}
}

func TestHandleThemeCommandFlashOnBrokenFile(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeThemeFile(t, filepath.Join(home, "themes", "broken.json"), `{oops`)
	project := filepath.Join(root, "proj")

	t.Setenv("PIRATE_HOME", home)
	m := &model{
		themeManager: NewThemeManager(),
		cfg:          Config{WorkDir: project},
	}
	m.themeManager.LoadCustomThemes(project)

	m.handleThemeCommand(nil)

	if !strings.Contains(m.flash, "broken.json") {
		t.Errorf("flash = %q, want it to name broken.json", m.flash)
	}
}

// TestRepoExampleThemeParses pins themes/example.json: the documented sample
// must stay parseable and declare every color role, so a user copying it
// always starts from a complete theme.
func TestRepoExampleThemeParses(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "themes", "example.json"))
	if err != nil {
		t.Fatalf("read themes/example.json: %v", err)
	}
	var theme Theme
	if err := json.Unmarshal(raw, &theme); err != nil {
		t.Fatalf("themes/example.json does not parse into Theme: %v", err)
	}
	if n := theme.Colors.colorRoleCount(); n != 24 {
		t.Errorf("themes/example.json declares %d color roles, want 24", n)
	}
}
