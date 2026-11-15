package ui

// The menu's look: the web app's Customize panel, for a terminal. Four
// palettes with the web looks' names, and an optional accent over any of
// them. Kept in config.json only, never in a note. The terminal's own
// background is unknown, so every palette uses colours that read on light
// and dark terminals alike, and selection in Windows 97 is the one place a
// background is painted, as its menus did.

import (
	"regexp"

	"charm.land/lipgloss/v2"
)

// Palette is one look's colours, as hex.
type Palette struct {
	ID, Label                      string
	Accent, Status, Error, Tag     string
	Pin, Code                      string
	SelectFg, SelectBg, HeaderMark string
}

// Themes are the looks, in the order Settings cycles through them.
var Themes = []Palette{
	{ID: "classic", Label: "Classic", Accent: "#4f8ef7", Status: "#3fb950", Error: "#f85149", Tag: "#d29922", Pin: "#f0883e", Code: "#a5d6ff", HeaderMark: "▍"},
	{ID: "win97", Label: "Windows 97", Accent: "#008080", Status: "#00a000", Error: "#e04040", Tag: "#a0a000", Pin: "#b040b0", Code: "#008080", SelectFg: "#ffffff", SelectBg: "#000080", HeaderMark: "■ "},
	{ID: "xiao", Label: "Xiao", Accent: "#e70923", Status: "#5e8c3a", Error: "#f36a52", Tag: "#b8860b", Pin: "#e70923", Code: "#9a9a92", HeaderMark: "◆ "},
	{ID: "glass", Label: "Liquid glass", Accent: "#7cb7ff", Status: "#5fd4b4", Error: "#ff7a99", Tag: "#e8b84a", Pin: "#ff8ac6", Code: "#9fd8ff", HeaderMark: "◐ "},
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ThemeByID is a look by its id, or Classic.
func ThemeByID(id string) Palette {
	for _, p := range Themes {
		if p.ID == id {
			return p
		}
	}
	return Themes[0]
}

// NextTheme is the look after this one, wrapping around.
func NextTheme(id string) string {
	for i, p := range Themes {
		if p.ID == id {
			return Themes[(i+1)%len(Themes)].ID
		}
	}
	return Themes[1].ID
}

// ValidAccent says whether a custom accent is a #rrggbb colour.
func ValidAccent(s string) bool { return hexColor.MatchString(s) }

// headerMark is the look's mark before "textarea" in the header.
var headerMark = "▍"

// ApplyTheme rebuilds every style from a look and an optional accent.
func ApplyTheme(id, customAccent string) {
	p := ThemeByID(id)
	if ValidAccent(customAccent) {
		p.Accent = customAccent
	}
	accent = lipgloss.Color(p.Accent)
	accentStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	selStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	if p.SelectBg != "" {
		selStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(p.SelectFg)).Background(lipgloss.Color(p.SelectBg)).Bold(true)
	}
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(p.Status))
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(p.Error))
	tagStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(p.Tag))
	pinStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(p.Pin))
	h1Style = lipgloss.NewStyle().Foreground(accent).Bold(true)
	codeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(p.Code))
	linkStyle = lipgloss.NewStyle().Foreground(accent).Underline(true)
	markerStyle = lipgloss.NewStyle().Foreground(accent)
	headerMark = p.HeaderMark
}
