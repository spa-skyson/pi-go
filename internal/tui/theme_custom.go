package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spa-skyson/pi-rate/internal/config"
)

// customThemeDirs returns the directories scanned for custom theme files, in
// increasing precedence: the global ~/.pirate/themes first, then every project
// .pirate/themes found walking up from cwd — farthest first, so a file in a
// nearer project directory overrides a same-named file in a farther one (and
// any of them overrides a built-in theme of the same name).
func customThemeDirs(cwd string) []string {
	dirs := []string{filepath.Join(config.PirateHome(), "themes")}

	dir := filepath.Clean(cwd)
	if dir == "" || dir == "." {
		if abs, err := filepath.Abs("."); err == nil {
			dir = abs
		}
	}
	var project []string
	for {
		p := filepath.Join(dir, config.ProjectDirName, "themes")
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			project = append(project, p)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	for i := len(project) - 1; i >= 0; i-- {
		dirs = append(dirs, project[i])
	}
	return dirs
}

// LoadCustomThemes scans the custom theme directories and (re)loads every
// *.json file in them as one theme named after the file (lowercased, without
// the extension). A theme sharing a name with a built-in overrides it; a
// project file overrides a global file of the same name. Partial themes are
// completed role-by-role from the built-in of the same name, or from the
// default theme for unknown names. A broken file is reported in the returned
// warnings and skipped — the TUI keeps running on the current theme. Calling
// again re-scans: themes whose files have disappeared are dropped, and if the
// active theme was one of them it falls back to the default.
func (tm *ThemeManager) LoadCustomThemes(cwd string) []string {
	for name := range tm.customNames {
		delete(tm.themes, name)
	}
	tm.customNames = make(map[string]struct{})
	if tm.embedded == nil {
		tm.snapshotEmbedded()
	}
	if _, ok := tm.themes[tm.current]; !ok {
		// The active theme was a custom file that vanished; fall back.
		if _, ok := tm.embedded[DefaultThemeName]; ok {
			tm.current = DefaultThemeName
		} else if _, ok := tm.embedded["pi-classic"]; ok {
			tm.current = "pi-classic"
		}
	}

	var warnings []string
	for _, dir := range customThemeDirs(cwd) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // a missing directory is the common case
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
				continue
			}
			theme, warn := tm.loadThemeFile(filepath.Join(dir, entry.Name()))
			if warn != "" {
				warnings = append(warnings, warn)
				continue
			}
			theme.Custom = true
			tm.themes[theme.Name] = theme
			tm.customNames[theme.Name] = struct{}{}
		}
	}
	return warnings
}

// loadThemeFile reads one custom theme file. The file name is the theme name,
// so /theme's lowercased lookup always matches. An unreadable or invalid file,
// or one declaring no color role at all, yields a warning and no theme.
func (tm *ThemeManager) loadThemeFile(path string) (Theme, string) {
	base := filepath.Base(path)
	name := strings.ToLower(strings.TrimSuffix(base, ".json"))

	data, err := os.ReadFile(path)
	if err != nil {
		return Theme{}, fmt.Sprintf("%s: %v", base, err)
	}
	var theme Theme
	if err := json.Unmarshal(data, &theme); err != nil {
		return Theme{}, fmt.Sprintf("%s: invalid JSON: %v", base, err)
	}
	if theme.Colors.colorRoleCount() == 0 {
		return Theme{}, fmt.Sprintf("%s: no color roles declared", base)
	}

	builtin, ok := tm.embedded[name]
	if !ok {
		builtin = tm.defaultTheme()
	}
	theme.Name = name
	if theme.DisplayName == "" {
		if theme.DisplayName = builtin.DisplayName; theme.DisplayName == "" {
			theme.DisplayName = name
		}
	}
	if theme.ThemeType == "" {
		if theme.ThemeType = builtin.ThemeType; theme.ThemeType == "" {
			theme.ThemeType = "dark"
		}
	}
	theme.Colors = theme.Colors.merged(builtin.Colors)
	return theme, ""
}

// defaultTheme is the merge base for custom themes that do not override a
// built-in name.
func (tm *ThemeManager) defaultTheme() Theme {
	if t, ok := tm.embedded[DefaultThemeName]; ok {
		return t
	}
	if t, ok := tm.embedded["pi-classic"]; ok {
		return t
	}
	return Theme{ThemeType: "dark"}
}

// colorRoleCount counts the color roles the palette declares.
func (c ThemeColors) colorRoleCount() int {
	n := 0
	for _, s := range []string{
		c.Text, c.Base, c.Primary, c.Tool, c.Success, c.Error, c.Secondary,
		c.Info, c.Warning, c.DiffAdded, c.DiffRemoved, c.DiffAddedText, c.DiffRemovedText,
	} {
		if strings.TrimSpace(s) != "" {
			n++
		}
	}
	return n
}

// merged fills the roles this palette leaves empty from base, role by role.
func (c ThemeColors) merged(base ThemeColors) ThemeColors {
	out := c
	if out.Text == "" {
		out.Text = base.Text
	}
	if out.Base == "" {
		out.Base = base.Base
	}
	if out.Primary == "" {
		out.Primary = base.Primary
	}
	if out.Tool == "" {
		out.Tool = base.Tool
	}
	if out.Success == "" {
		out.Success = base.Success
	}
	if out.Error == "" {
		out.Error = base.Error
	}
	if out.Secondary == "" {
		out.Secondary = base.Secondary
	}
	if out.Info == "" {
		out.Info = base.Info
	}
	if out.Warning == "" {
		out.Warning = base.Warning
	}
	if out.DiffAdded == "" {
		out.DiffAdded = base.DiffAdded
	}
	if out.DiffRemoved == "" {
		out.DiffRemoved = base.DiffRemoved
	}
	if out.DiffAddedText == "" {
		out.DiffAddedText = base.DiffAddedText
	}
	if out.DiffRemovedText == "" {
		out.DiffRemovedText = base.DiffRemovedText
	}
	return out
}
