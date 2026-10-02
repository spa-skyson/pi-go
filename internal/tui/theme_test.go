package tui

import (
	"image/color"
	"path/filepath"
	"strings"
	"testing"
)

func TestThemeManagerLoad(t *testing.T) {
	tm := NewThemeManager()

	if got := tm.ThemeCount(); got != 41 {
		t.Errorf("ThemeCount() = %d, want 41", got)
	}

	if got := tm.CurrentName(); got != DefaultThemeName {
		t.Errorf("CurrentName() = %q, want %q", got, DefaultThemeName)
	}
}

func TestThemeManagerSetTheme(t *testing.T) {
	tm := NewThemeManager()

	if err := tm.SetTheme("dracula"); err != nil {
		t.Fatalf("SetTheme(dracula) error: %v", err)
	}
	if got := tm.CurrentName(); got != "dracula" {
		t.Errorf("CurrentName() = %q, want dracula", got)
	}

	if err := tm.SetTheme("nonexistent"); err == nil {
		t.Error("SetTheme(nonexistent) should return error")
	}
	if got := tm.CurrentName(); got != "dracula" {
		t.Errorf("CurrentName() = %q, want dracula (should not change on error)", got)
	}
}

func TestThemeManagerFallback(t *testing.T) {
	tm := &ThemeManager{themes: make(map[string]Theme)}
	if err := tm.loadFromJSON([]byte("not json")); err == nil {
		t.Error("loadFromJSON with corrupt data should return error")
	}
	tm.loadFallback()

	if _, ok := tm.themes["pi-classic"]; !ok {
		t.Error("fallback should load pi-classic theme")
	}
}

func TestThemeColors(t *testing.T) {
	tm := NewThemeManager()
	colors := tm.Colors()

	checks := map[string]string{
		"Text":            colors.Text,
		"Base":            colors.Base,
		"Primary":         colors.Primary,
		"Tool":            colors.Tool,
		"Success":         colors.Success,
		"Error":           colors.Error,
		"Secondary":       colors.Secondary,
		"Info":            colors.Info,
		"Warning":         colors.Warning,
		"DiffAdded":       colors.DiffAdded,
		"DiffRemoved":     colors.DiffRemoved,
		"DiffAddedText":   colors.DiffAddedText,
		"DiffRemovedText": colors.DiffRemovedText,
	}
	for name, val := range checks {
		if val == "" {
			t.Errorf("color %s is empty", name)
		}
		if !strings.HasPrefix(val, "#") {
			t.Errorf("color %s = %q, want hex value starting with #", name, val)
		}
	}
}

func TestThemeColorsConversion(t *testing.T) {
	tm := NewThemeManager()
	colors := tm.Colors()

	// Verify all color.Color helpers don't panic.
	// lipgloss.Color is just a string alias.
	colorMethods := []struct {
		name string
		fn   func() any
	}{
		{"TextColor", func() any { return colors.TextColor() }},
		{"BaseColor", func() any { return colors.BaseColor() }},
		{"PrimaryColor", func() any { return colors.PrimaryColor() }},
		{"ToolColor", func() any { return colors.ToolColor() }},
		{"SuccessColor", func() any { return colors.SuccessColor() }},
		{"ErrorColor", func() any { return colors.ErrorColor() }},
		{"SecondaryColor", func() any { return colors.SecondaryColor() }},
		{"InfoColor", func() any { return colors.InfoColor() }},
		{"WarningColor", func() any { return colors.WarningColor() }},
		{"DiffAddedColor", func() any { return colors.DiffAddedColor() }},
		{"DiffRemovedColor", func() any { return colors.DiffRemovedColor() }},
		{"DiffAddedTextColor", func() any { return colors.DiffAddedTextColor() }},
		{"DiffRemovedTextColor", func() any { return colors.DiffRemovedTextColor() }},
	}
	for _, tc := range colorMethods {
		if c := tc.fn(); c == nil {
			t.Errorf("%s() returned nil", tc.name)
		}
	}
}

func TestThemeManagerList(t *testing.T) {
	tm := NewThemeManager()

	themes := tm.List()
	if len(themes) != 41 {
		t.Errorf("List() returned %d themes, want 41", len(themes))
	}

	for i := 1; i < len(themes); i++ {
		if themes[i].Name < themes[i-1].Name {
			t.Errorf("themes not sorted: %q before %q", themes[i-1].Name, themes[i].Name)
			break
		}
	}
}

func TestThemeManagerIsDark(t *testing.T) {
	tm := NewThemeManager()

	if !tm.IsDark() {
		t.Error("tokyo-night should be dark")
	}

	if err := tm.SetTheme("github-light"); err != nil {
		t.Fatalf("SetTheme error: %v", err)
	}
	if tm.IsDark() {
		t.Error("github-light should not be dark")
	}
}

func TestThemeManagerHasTheme(t *testing.T) {
	tm := NewThemeManager()

	if !tm.HasTheme("dracula") {
		t.Error("should have dracula theme")
	}
	if tm.HasTheme("nonexistent") {
		t.Error("should not have nonexistent theme")
	}
}

func TestThemeManagerClosestMatches(t *testing.T) {
	tm := NewThemeManager()

	matches := tm.ClosestMatches("cat", 5)
	if len(matches) == 0 {
		t.Fatal("ClosestMatches(cat) should find catppuccin themes")
	}
	for _, m := range matches {
		if !strings.Contains(m, "catppuccin") {
			t.Errorf("unexpected match %q for query cat", m)
		}
	}

	matches = tm.ClosestMatches("dark", 2)
	if len(matches) > 2 {
		t.Errorf("ClosestMatches with max=2 returned %d results", len(matches))
	}
}

func TestThemeManagerCurrent(t *testing.T) {
	tm := NewThemeManager()

	theme := tm.Current()
	if theme.Name != "tokyo-night" {
		t.Errorf("Current().Name = %q, want tokyo-night", theme.Name)
	}
	if theme.DisplayName != "Tokyo Night" {
		t.Errorf("Current().DisplayName = %q, want Tokyo Night", theme.DisplayName)
	}
	if theme.ThemeType != "dark" {
		t.Errorf("Current().ThemeType = %q, want dark", theme.ThemeType)
	}
}

func TestAllThemesHaveRequiredColors(t *testing.T) {
	tm := NewThemeManager()

	for _, theme := range tm.List() {
		c := theme.Colors
		fields := map[string]string{
			"Text":    c.Text,
			"Base":    c.Base,
			"Primary": c.Primary,
			"Tool":    c.Tool,
			"Success": c.Success,
			"Error":   c.Error,
		}
		for field, val := range fields {
			if val == "" {
				t.Errorf("theme %q: color %s is empty", theme.Name, field)
			}
		}
	}
}

func TestNewThemeManagerFromJSON(t *testing.T) {
	data := []byte(`{
		"test-theme": {
			"name": "test-theme",
			"displayName": "Test Theme",
			"themeType": "dark",
			"colors": {
				"text": "#ffffff",
				"base": "#000000",
				"primary": "#ff0000",
				"tool": "#00ff00",
				"success": "#00ff00",
				"error": "#ff0000",
				"secondary": "#808080",
				"info": "#0000ff",
				"warning": "#ffff00",
				"diffAdded": "#003300",
				"diffRemoved": "#330000",
				"diffAddedText": "#00ff00",
				"diffRemovedText": "#ff0000"
			}
		}
	}`)

	tm, err := NewThemeManagerFromJSON(data)
	if err != nil {
		t.Fatalf("NewThemeManagerFromJSON error: %v", err)
	}
	if tm.ThemeCount() != 1 {
		t.Errorf("ThemeCount() = %d, want 1", tm.ThemeCount())
	}
	if !tm.HasTheme("test-theme") {
		t.Error("should have test-theme")
	}

	_, err = NewThemeManagerFromJSON([]byte("invalid"))
	if err == nil {
		t.Error("NewThemeManagerFromJSON should error on invalid JSON")
	}
}

// TestPaletteForLightThemeRendersDifferently pins the fix for light themes:
// before the palette was threaded into the renderers, switching to a light
// theme changed nothing on screen because every renderer hardcoded dark Mocha
// colors. paletteFor must produce a palette whose key differs from the dark
// default, so the render cache and the renderers actually pick up the change.
func TestPaletteForLightThemeRendersDifferently(t *testing.T) {
	tm := NewThemeManager()
	if err := tm.SetTheme("github-light"); err != nil {
		t.Fatalf("SetTheme(github-light) error: %v", err)
	}
	light := paletteFor(tm.Current())
	if !light.Valid {
		t.Fatal("light palette should be valid")
	}
	if paletteKey(light) == paletteKey(darkPalette) {
		t.Error("light palette key equals dark palette key; light theme would not re-render")
	}
	// The light palette's text must be dark (legible on a light background),
	// not the dark theme's light text.
	if colorString(light.Text) == colorString(darkPalette.Text) {
		t.Errorf("light theme text %q should differ from dark theme text %q",
			colorString(light.Text), colorString(darkPalette.Text))
	}
}

// TestPaletteForDarkThemeMatchesDefault ensures the default (dark) theme
// resolves to the same Mocha palette the renderers used before theming, so
// existing dark output is byte-for-byte unchanged.
func TestPaletteForDarkThemeMatchesDefault(t *testing.T) {
	tm := NewThemeManager()
	dark := paletteFor(tm.Current())
	if !dark.Valid {
		t.Fatal("dark palette should be valid")
	}
	if paletteKey(dark) != paletteKey(darkPalette) {
		t.Errorf("default theme palette key %d != dark default %d",
			paletteKey(dark), paletteKey(darkPalette))
	}
}

// extendedThemeJSON is a custom theme that declares the full extended token
// set (#38).
const extendedThemeJSON = `{
	"full": {
		"name": "full",
		"displayName": "Full",
		"themeType": "dark",
		"colors": {
			"text": "#eeeeee", "base": "#0a0a0a", "primary": "#fab283",
			"tool": "#56b6c2", "success": "#7fd88f", "error": "#e06c75",
			"secondary": "#5c9cf5", "info": "#56b6c2", "warning": "#f5a742",
			"diffAdded": "#20303b", "diffRemoved": "#37222c",
			"diffAddedText": "#4fd6be", "diffRemovedText": "#c53b53",
			"backgroundPanel": "#141414", "backgroundElement": "#1e1e1e",
			"borderSubtle": "#323232", "borderBase": "#484848",
			"borderActive": "#606060", "accent": "#9d7cd8",
			"textMuted": "#808080", "diffContext": "#828bb8",
			"diffHunkHeader": "#828bb8", "diffLineNumber": "#505050",
			"editorCursor": "#fab283"
		}
	}
}`

// TestThemeExtendedTokensRoundTrip loads a theme carrying the extended token
// set and pins every role through the manager.
func TestThemeExtendedTokensRoundTrip(t *testing.T) {
	tm, err := NewThemeManagerFromJSON([]byte(extendedThemeJSON))
	if err != nil {
		t.Fatalf("NewThemeManagerFromJSON: %v", err)
	}
	if err := tm.SetTheme("full"); err != nil {
		t.Fatalf("SetTheme(full): %v", err)
	}
	c := tm.Colors()
	checks := map[string]string{
		"BackgroundPanel":   c.BackgroundPanel,
		"BackgroundElement": c.BackgroundElement,
		"BorderSubtle":      c.BorderSubtle,
		"BorderBase":        c.BorderBase,
		"BorderActive":      c.BorderActive,
		"Accent":            c.Accent,
		"TextMuted":         c.TextMuted,
		"DiffContext":       c.DiffContext,
		"DiffHunkHeader":    c.DiffHunkHeader,
		"DiffLineNumber":    c.DiffLineNumber,
		"EditorCursor":      c.EditorCursor,
	}
	for role, val := range checks {
		if val == "" {
			t.Errorf("extended role %s is empty after load", role)
		}
		if !strings.HasPrefix(val, "#") {
			t.Errorf("extended role %s = %q, want hex value", role, val)
		}
	}
	if c.BackgroundPanel != "#141414" || c.BorderSubtle != "#323232" || c.TextMuted != "#808080" {
		t.Errorf("extended roles not preserved verbatim: panel=%q subtle=%q muted=%q",
			c.BackgroundPanel, c.BorderSubtle, c.TextMuted)
	}
	if !tm.Current().Extended {
		t.Error("theme declaring extended tokens must be marked Extended")
	}
}

// TestThemeTokenFallbackTable pins what a theme gets for the extended roles
// depending on what its file declared:
//
//	full set    → roles verbatim, theme is Extended
//	legacy only → roles stay empty (embedded) or fall back to the theme's own
//	              legacy colors (custom, via merged), and the theme is NOT
//	              Extended, so the palette stays the fixed dark default
//	garbage     → load error, no theme
func TestThemeTokenFallbackTable(t *testing.T) {
	tests := []struct {
		name string
		json string
		// wantExtended is the Extended marker after the load path ran.
		wantExtended bool
		// check runs against the loaded manager; nil for the garbage case.
		check func(t *testing.T, tm *ThemeManager)
	}{
		{
			name:         "full token set loads verbatim",
			json:         extendedThemeJSON,
			wantExtended: true,
			check: func(t *testing.T, tm *ThemeManager) {
				_ = tm.SetTheme("full")
				if got := tm.Colors().BorderSubtle; got != "#323232" {
					t.Errorf("BorderSubtle = %q, want #323232", got)
				}
			},
		},
		{
			name:         "legacy-only custom theme falls back to its own colors",
			json:         "", // loads through the custom-directory path below
			wantExtended: false,
			check: func(t *testing.T, tm *ThemeManager) {
				_ = tm.SetTheme("legacy")
				c := tm.Colors()
				// merged() fills the extended roles from the theme's own
				// legacy colors: panel/element ← base, borders/muted/diff ←
				// secondary, cursor ← primary.
				if c.BackgroundPanel != "#010203" || c.BackgroundElement != "#010203" {
					t.Errorf("panel/element = %q/%q, want base #010203 twice",
						c.BackgroundPanel, c.BackgroundElement)
				}
				if c.BorderSubtle != "#777777" || c.BorderBase != "#777777" || c.BorderActive != "#777777" {
					t.Errorf("borders = %q/%q/%q, want secondary #777777",
						c.BorderSubtle, c.BorderBase, c.BorderActive)
				}
				if c.TextMuted != "#777777" || c.DiffContext != "#777777" {
					t.Errorf("muted/context = %q/%q, want secondary #777777", c.TextMuted, c.DiffContext)
				}
				if c.EditorCursor != "#ff0000" {
					t.Errorf("EditorCursor = %q, want primary #ff0000", c.EditorCursor)
				}
				// Not marked Extended → the palette keeps the fixed defaults,
				// so a legacy custom theme renders as before (plus neutral
				// borders drawn from the palette).
				if tm.Current().Extended {
					t.Error("legacy custom theme must not be marked Extended")
				}
				if got := paletteFor(tm.Current()); paletteKey(got) != paletteKey(darkPalette) {
					t.Error("legacy custom theme palette key differs from the dark default")
				}
			},
		},
		{
			name:         "embedded legacy theme keeps empty extended roles",
			json:         `{"emb":{"name":"emb","displayName":"Emb","themeType":"dark","colors":{"text":"#ffffff","base":"#000000","primary":"#ffffff","tool":"#ffffff","success":"#ffffff","error":"#ffffff","secondary":"#888888","info":"#ffffff","warning":"#ffffff","diffAdded":"#003300","diffRemoved":"#330000","diffAddedText":"#00ff00","diffRemovedText":"#ff0000"}}}`,
			wantExtended: false,
			check: func(t *testing.T, tm *ThemeManager) {
				_ = tm.SetTheme("emb")
				c := tm.Colors()
				if c.BackgroundPanel != "" || c.BorderSubtle != "" {
					t.Errorf("embedded legacy theme must keep extended roles empty, got panel=%q subtle=%q",
						c.BackgroundPanel, c.BorderSubtle)
				}
			},
		},
		{
			name:         "garbage errors",
			json:         `{not json`,
			wantExtended: false,
			check:        nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var tm *ThemeManager
			var err error
			if tc.json == "" {
				// The custom-directory path: write a legacy-only theme file
				// and load it the way the TUI does. merged() — which applies
				// the own-color fallback for the extended roles — runs only
				// on this path, never for embedded themes.
				root := t.TempDir()
				home := filepath.Join(root, "home")
				t.Setenv("PIRATE_HOME", home)
				writeThemeFile(t, filepath.Join(home, "themes", "legacy.json"),
					`{"displayName":"Legacy","colors":{"text":"#ffffff","base":"#010203",
					"primary":"#ff0000","secondary":"#777777","tool":"#00ff00",
					"success":"#00ff00","error":"#ff0000","info":"#0000ff",
					"warning":"#ffff00","diffAdded":"#003300","diffRemoved":"#330000",
					"diffAddedText":"#00ff00","diffRemovedText":"#ff0000"}}`)
				tm = NewThemeManager()
				tm.LoadCustomThemes(root)
				err = tm.SetTheme("legacy")
			} else {
				tm, err = NewThemeManagerFromJSON([]byte(tc.json))
			}
			if tc.check == nil {
				if err == nil {
					t.Fatal("expected a load error for garbage input")
				}
				return
			}
			if err != nil {
				t.Fatalf("theme load: %v", err)
			}
			if tm.Current().Extended != tc.wantExtended {
				t.Errorf("Extended = %v, want %v", tm.Current().Extended, tc.wantExtended)
			}
			tc.check(t, tm)
		})
	}
}

// TestOpenCodeThemePinsThePalette pins the built-in opencode theme (#38): it
// must load with every key color the palette declares, and paletteFor must
// resolve those colors into the render palette so switching to the theme
// actually repaints the chrome.
func TestOpenCodeThemePinsThePalette(t *testing.T) {
	tm := NewThemeManager()
	if !tm.HasTheme("opencode") {
		t.Fatal("embedded opencode theme not found")
	}
	if err := tm.SetTheme("opencode"); err != nil {
		t.Fatalf("SetTheme(opencode): %v", err)
	}
	theme := tm.Current()
	if !theme.Extended {
		t.Error("opencode theme must be marked Extended")
	}
	if theme.ThemeType != "dark" {
		t.Errorf("ThemeType = %q, want dark", theme.ThemeType)
	}
	want := map[string]string{
		"Text":              "#eeeeee",
		"Base":              "#0a0a0a",
		"Primary":           "#fab283",
		"Secondary":         "#5c9cf5",
		"Error":             "#e06c75",
		"Warning":           "#f5a742",
		"Success":           "#7fd88f",
		"Info":              "#56b6c2",
		"BackgroundPanel":   "#141414",
		"BackgroundElement": "#1e1e1e",
		"BorderSubtle":      "#323232",
		"BorderBase":        "#484848",
		"BorderActive":      "#606060",
		"Accent":            "#9d7cd8",
		"TextMuted":         "#808080",
		"DiffContext":       "#828bb8",
		"DiffHunkHeader":    "#828bb8",
		"DiffAddedText":     "#4fd6be",
		"DiffRemovedText":   "#c53b53",
		"DiffAdded":         "#20303b",
		"DiffRemoved":       "#37222c",
		"EditorCursor":      "#fab283",
	}
	c := theme.Colors
	got := map[string]string{
		"Text": c.Text, "Base": c.Base, "Primary": c.Primary,
		"Secondary": c.Secondary, "Error": c.Error, "Warning": c.Warning,
		"Success": c.Success, "Info": c.Info,
		"BackgroundPanel": c.BackgroundPanel, "BackgroundElement": c.BackgroundElement,
		"BorderSubtle": c.BorderSubtle, "BorderBase": c.BorderBase, "BorderActive": c.BorderActive,
		"Accent": c.Accent, "TextMuted": c.TextMuted,
		"DiffContext": c.DiffContext, "DiffHunkHeader": c.DiffHunkHeader,
		"DiffAddedText": c.DiffAddedText, "DiffRemovedText": c.DiffRemovedText,
		"DiffAdded": c.DiffAdded, "DiffRemoved": c.DiffRemoved,
		"EditorCursor": c.EditorCursor,
	}
	for role, wantHex := range want {
		if got[role] != wantHex {
			t.Errorf("opencode %s = %q, want %q", role, got[role], wantHex)
		}
	}

	// The resolved palette carries the theme's own surfaces and core colors,
	// so the chrome actually changes when the theme is selected.
	p := paletteFor(theme)
	for role, wantExpr := range map[string]struct {
		got  color.Color
		want string
	}{
		"BorderSubtle":    {p.BorderSubtle, "#323232"},
		"BorderActive":    {p.BorderActive, "#606060"},
		"BackgroundPanel": {p.BackgroundPanel, "#141414"},
		"TextMuted":       {p.TextMuted, "#808080"},
		"Primary":         {p.Primary, "#fab283"},
		"Text":            {p.Text, "#eeeeee"},
		"DiffContext":     {p.DiffContext, "#828bb8"},
		"EditorCursor":    {p.EditorCursor, "#fab283"},
	} {
		if gotHex := colorString(wantExpr.got); gotHex != wantExpr.want {
			t.Errorf("palette %s = %q, want %q", role, gotHex, wantExpr.want)
		}
	}
	// A different palette key is what invalidates the render caches on the
	// /theme switch.
	if paletteKey(p) == paletteKey(darkPalette) {
		t.Error("opencode palette key equals the dark default; the switch would not repaint")
	}
}
