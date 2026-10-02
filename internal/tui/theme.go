package tui

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/spa-skyson/pi-rate/internal/config"
)

//go:embed themes.json
var themesJSON []byte

// ThemeColors holds the color roles used throughout the TUI.
// Colors are stored as hex strings and converted to color.Color via lipgloss.Color().
//
// The first thirteen roles are the original set every theme declares. The
// extended roles below them (#38) are optional: they carry the surface,
// border, muted-text and diff-detail colors the opencode-style rendering
// consumes. A theme that declares none of them is a legacy theme — it
// renders exactly as before, with the palette's own neutral surfaces.
type ThemeColors struct {
	Text            string `json:"text"`
	Base            string `json:"base"`
	Primary         string `json:"primary"`
	Tool            string `json:"tool"`
	Success         string `json:"success"`
	Error           string `json:"error"`
	Secondary       string `json:"secondary"`
	Info            string `json:"info"`
	Warning         string `json:"warning"`
	DiffAdded       string `json:"diffAdded"`
	DiffRemoved     string `json:"diffRemoved"`
	DiffAddedText   string `json:"diffAddedText"`
	DiffRemovedText string `json:"diffRemovedText"`

	// Extended roles (#38). Filled in for custom themes by merged() (panel
	// and element from base, borders and muted text from secondary, cursor
	// from primary); embedded legacy themes leave them empty and keep the
	// fixed palette surfaces. DiffLineNumber is declared for theme authors
	// ahead of a consumer: the /diff viewer trims its marker column rather
	// than rendering a gutter, so no row currently draws it — the /theme
	// palette preview shows it so a theme can still express the full
	// opencode palette.
	BackgroundPanel   string `json:"backgroundPanel,omitempty"`
	BackgroundElement string `json:"backgroundElement,omitempty"`
	BorderSubtle      string `json:"borderSubtle,omitempty"`
	BorderBase        string `json:"borderBase,omitempty"`
	BorderActive      string `json:"borderActive,omitempty"`
	Accent            string `json:"accent,omitempty"`
	TextMuted         string `json:"textMuted,omitempty"`
	DiffContext       string `json:"diffContext,omitempty"`
	DiffHunkHeader    string `json:"diffHunkHeader,omitempty"`
	DiffLineNumber    string `json:"diffLineNumber,omitempty"`
	EditorCursor      string `json:"editorCursor,omitempty"`
}

// hasExtendedTokens reports whether the palette declares at least one of the
// extended surface roles. This is the marker of a theme written for the
// extended rendering: only such themes drive the palette's surfaces and core
// colors; legacy themes keep the fixed dark/light palettes.
func (c ThemeColors) hasExtendedTokens() bool {
	for _, s := range []string{
		c.BackgroundPanel, c.BackgroundElement, c.BorderSubtle, c.BorderBase,
		c.BorderActive, c.Accent, c.TextMuted, c.DiffContext, c.DiffHunkHeader,
		c.DiffLineNumber, c.EditorCursor,
	} {
		if strings.TrimSpace(s) != "" {
			return true
		}
	}
	return false
}

// Color helpers that return color.Color for use with lipgloss styles.

func (c ThemeColors) TextColor() color.Color            { return lipgloss.Color(c.Text) }
func (c ThemeColors) BaseColor() color.Color            { return lipgloss.Color(c.Base) }
func (c ThemeColors) PrimaryColor() color.Color         { return lipgloss.Color(c.Primary) }
func (c ThemeColors) ToolColor() color.Color            { return lipgloss.Color(c.Tool) }
func (c ThemeColors) SuccessColor() color.Color         { return lipgloss.Color(c.Success) }
func (c ThemeColors) ErrorColor() color.Color           { return lipgloss.Color(c.Error) }
func (c ThemeColors) SecondaryColor() color.Color       { return lipgloss.Color(c.Secondary) }
func (c ThemeColors) InfoColor() color.Color            { return lipgloss.Color(c.Info) }
func (c ThemeColors) WarningColor() color.Color         { return lipgloss.Color(c.Warning) }
func (c ThemeColors) DiffAddedColor() color.Color       { return lipgloss.Color(c.DiffAdded) }
func (c ThemeColors) DiffRemovedColor() color.Color     { return lipgloss.Color(c.DiffRemoved) }
func (c ThemeColors) DiffAddedTextColor() color.Color   { return lipgloss.Color(c.DiffAddedText) }
func (c ThemeColors) DiffRemovedTextColor() color.Color { return lipgloss.Color(c.DiffRemovedText) }

// Theme represents a loaded color theme.
type Theme struct {
	Name        string      `json:"name"`
	DisplayName string      `json:"displayName"`
	ThemeType   string      `json:"themeType"`
	Colors      ThemeColors `json:"colors"`
	// Custom marks a theme loaded from a themes directory rather than the
	// embedded set; used only for the "(custom)" tag in the /theme list.
	Custom bool `json:"-"`
	// Extended marks a theme whose file declared at least one extended
	// surface role (#38). Only extended themes drive the palette's surfaces
	// and core colors; a legacy theme — embedded or custom — keeps the fixed
	// dark/light palette so its rendering is unchanged apart from the neutral
	// borders the new chrome draws. Set at load time, before merged() fills
	// the extended roles in, because the fill would otherwise make every
	// custom theme look extended.
	Extended bool `json:"-"`
}

// ThemeManager manages theme loading, selection, and color access.
type ThemeManager struct {
	themes  map[string]Theme
	current string
	// embedded snapshots the built-in themes at construction, so a partial
	// custom theme can be completed from the built-in it overrides even after
	// that built-in has been replaced in themes. customNames tracks which
	// entries a custom-directory scan loaded, so a re-scan can drop them.
	embedded    map[string]Theme
	customNames map[string]struct{}
}

// DefaultThemeName is the default theme when none is configured.
const DefaultThemeName = "tokyo-night"

// NewThemeManager creates a ThemeManager with all embedded themes loaded.
// Falls back to pi-classic if the embedded JSON is corrupt.
func NewThemeManager() *ThemeManager {
	tm := &ThemeManager{
		themes: make(map[string]Theme),
	}

	if err := tm.loadFromJSON(themesJSON); err != nil {
		tm.loadFallback()
	}
	tm.snapshotEmbedded()

	tm.current = DefaultThemeName
	if _, ok := tm.themes[tm.current]; !ok {
		if _, ok := tm.themes["pi-classic"]; ok {
			tm.current = "pi-classic"
		} else {
			for name := range tm.themes {
				tm.current = name
				break
			}
		}
	}

	return tm
}

// NewThemeManagerFromJSON creates a ThemeManager from custom JSON data.
func NewThemeManagerFromJSON(data []byte) (*ThemeManager, error) {
	tm := &ThemeManager{
		themes: make(map[string]Theme),
	}
	if err := tm.loadFromJSON(data); err != nil {
		return nil, err
	}
	tm.snapshotEmbedded()
	if len(tm.themes) > 0 {
		tm.current = DefaultThemeName
		if _, ok := tm.themes[tm.current]; !ok {
			for name := range tm.themes {
				tm.current = name
				break
			}
		}
	}
	return tm, nil
}

func (tm *ThemeManager) loadFromJSON(data []byte) error {
	var raw map[string]Theme
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse themes: %w", err)
	}
	for name, theme := range raw {
		theme.Extended = theme.Colors.hasExtendedTokens()
		tm.themes[name] = theme
	}
	return nil
}

func (tm *ThemeManager) loadFallback() {
	tm.themes["pi-classic"] = Theme{
		Name:        "pi-classic",
		DisplayName: "Pi Classic",
		ThemeType:   "dark",
		Colors: ThemeColors{
			Text:            "#bcbcbc",
			Base:            "#303030",
			Primary:         "#5f87ff",
			Tool:            "#5faf5f",
			Success:         "#5faf5f",
			Error:           "#ff5f5f",
			Secondary:       "#585858",
			Info:            "#af87ff",
			Warning:         "#ffaf5f",
			DiffAdded:       "#1e3a29",
			DiffRemoved:     "#3a1e2a",
			DiffAddedText:   "#5faf5f",
			DiffRemovedText: "#ff5f5f",
		},
	}
}

// snapshotEmbedded copies the current theme set as the merge base for custom
// themes. Called once from the constructors, before any custom theme loads.
func (tm *ThemeManager) snapshotEmbedded() {
	tm.embedded = make(map[string]Theme, len(tm.themes))
	for name, theme := range tm.themes {
		tm.embedded[name] = theme
	}
}

// Colors returns the current theme's color palette.
func (tm *ThemeManager) Colors() ThemeColors {
	return tm.themes[tm.current].Colors
}

// SetTheme changes the active theme. Returns an error if the theme doesn't exist.
func (tm *ThemeManager) SetTheme(name string) error {
	if _, ok := tm.themes[name]; !ok {
		return fmt.Errorf("unknown theme %q; use /theme to list available themes", name)
	}
	tm.current = name
	return nil
}

// Current returns the active theme.
func (tm *ThemeManager) Current() Theme {
	return tm.themes[tm.current]
}

// CurrentName returns the name of the active theme.
func (tm *ThemeManager) CurrentName() string {
	return tm.current
}

// List returns all available themes sorted by name.
func (tm *ThemeManager) List() []Theme {
	themes := make([]Theme, 0, len(tm.themes))
	for _, t := range tm.themes {
		themes = append(themes, t)
	}
	sort.Slice(themes, func(i, j int) bool {
		return themes[i].Name < themes[j].Name
	})
	return themes
}

// ThemeCount returns the number of loaded themes.
func (tm *ThemeManager) ThemeCount() int {
	return len(tm.themes)
}

// IsDark returns true if the current theme is a dark theme.
func (tm *ThemeManager) IsDark() bool {
	return tm.themes[tm.current].ThemeType == "dark"
}

// HasTheme returns true if a theme with the given name exists.
func (tm *ThemeManager) HasTheme(name string) bool {
	_, ok := tm.themes[name]
	return ok
}

// ClosestMatches returns theme names that contain the given substring,
// useful for suggesting alternatives when a theme isn't found.
func (tm *ThemeManager) ClosestMatches(query string, max int) []string {
	query = strings.ToLower(query)
	var matches []string
	for name := range tm.themes {
		if strings.Contains(strings.ToLower(name), query) ||
			strings.Contains(strings.ToLower(tm.themes[name].DisplayName), query) {
			matches = append(matches, name)
		}
	}
	sort.Strings(matches)
	if len(matches) > max {
		matches = matches[:max]
	}
	return matches
}

// saveThemeToConfig persists the theme name to ~/.pirate/config.json.
func saveThemeToConfig(name string) {
	if _, err := os.UserHomeDir(); err != nil {
		return
	}
	configPath := filepath.Join(config.PirateHome(), "config.json")

	// Read existing config.
	var raw map[string]any
	data, err := os.ReadFile(configPath)
	if err != nil {
		raw = make(map[string]any)
	} else {
		if err := json.Unmarshal(data, &raw); err != nil {
			raw = make(map[string]any)
		}
	}

	raw["theme"] = name

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(configPath), 0o755)
	_ = os.WriteFile(configPath, out, 0o644)
}
