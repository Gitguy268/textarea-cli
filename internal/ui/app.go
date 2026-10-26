// Package ui is the `text` menu: a stack of screens over the library, in
// the spirit of Mole's option menu (the idea only; Mole is GPL-3.0 and none
// of its code is here). Every screen is keyboard first: arrows or j/k, a
// number or a letter to jump, Enter to choose, Esc to go back.
package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gitguy268/textarea-cli/internal/config"
	"github.com/Gitguy268/textarea-cli/internal/store"
)

// Env is what every screen can reach.
type Env struct {
	Lib     *store.Library
	Cfg     *config.Config
	Version string
}

// Screen is one layer of the stack.
type Screen interface {
	Update(env *Env, msg tea.Msg) (Screen, tea.Cmd)
	View(env *Env, width, height int) string
	Title() string
}

// Messages screens send to the app.
type (
	pushMsg    struct{ s Screen }
	popMsg     struct{ n int }
	replaceMsg struct{ s Screen }
	statusMsg  struct {
		text string
		err  bool
	}
	quitMsg struct{}
)

func push(s Screen) tea.Cmd    { return func() tea.Msg { return pushMsg{s} } }
func pop() tea.Cmd             { return func() tea.Msg { return popMsg{1} } }
func popN(n int) tea.Cmd       { return func() tea.Msg { return popMsg{n} } }
func replace(s Screen) tea.Cmd { return func() tea.Msg { return replaceMsg{s} } }
func say(text string) tea.Cmd  { return func() tea.Msg { return statusMsg{text, false} } }
func fail(err error) tea.Cmd {
	return func() tea.Msg { return statusMsg{"⚠ " + err.Error(), true} }
}

// App is the root model.
type App struct {
	env           *Env
	stack         []Screen
	width, height int
	status        string
	statusErr     bool
}

// New makes the app with the given first screen.
func New(env *Env, first Screen) *App {
	return &App{env: env, stack: []Screen{first}, width: 80, height: 24}
}

// Init implements tea.Model.
func (a *App) Init() tea.Cmd { return nil }

func (a *App) top() Screen { return a.stack[len(a.stack)-1] }

// Update implements tea.Model.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = m.Width, m.Height
	case pushMsg:
		a.stack = append(a.stack, m.s)
		a.status = ""
		return a, nil
	case popMsg:
		for i := 0; i < m.n && len(a.stack) > 1; i++ {
			a.stack = a.stack[:len(a.stack)-1]
		}
		if r, ok := a.top().(refresher); ok {
			return a, r.refresh(a.env)
		}
		return a, nil
	case replaceMsg:
		a.stack[len(a.stack)-1] = m.s
		return a, nil
	case statusMsg:
		a.status, a.statusErr = m.text, m.err
		return a, nil
	case quitMsg:
		return a, tea.Quit
	case tea.KeyPressMsg:
		if m.String() == "ctrl+c" {
			for _, s := range a.stack {
				if q, ok := s.(interface{ beforeQuit(*Env) }); ok {
					q.beforeQuit(a.env)
				}
			}
			return a, tea.Quit
		}
		a.status = ""
	}
	next, cmd := a.top().Update(a.env, msg)
	if next == nil {
		if len(a.stack) == 1 {
			return a, tea.Quit
		}
		a.stack = a.stack[:len(a.stack)-1]
		if r, ok := a.top().(refresher); ok {
			return a, tea.Batch(cmd, r.refresh(a.env))
		}
		return a, cmd
	}
	a.stack[len(a.stack)-1] = next
	return a, cmd
}

// refresher screens reload when they come back to the top.
type refresher interface {
	refresh(env *Env) tea.Cmd
}

// View implements tea.Model.
func (a *App) View() tea.View {
	s := a.top()
	var b strings.Builder
	b.WriteString(header(a.env, a.stack, a.width))
	b.WriteString("\n")
	bodyHeight := a.height - 4
	if bodyHeight < 3 {
		bodyHeight = 3
	}
	body := s.View(a.env, a.width, bodyHeight)
	lines := strings.Split(body, "\n")
	if len(lines) > bodyHeight {
		lines = lines[:bodyHeight]
	}
	b.WriteString(strings.Join(lines, "\n"))
	for i := len(lines); i < bodyHeight; i++ {
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if a.status != "" {
		st := statusStyle
		if a.statusErr {
			st = errorStyle
		}
		b.WriteString(st.Render(truncate(a.status, a.width)))
	}
	v := tea.NewView(b.String())
	v.AltScreen = true
	v.WindowTitle = "text · " + s.Title()
	return v
}

var (
	accent      = lipgloss.Color("#4f8ef7")
	accentStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	boldStyle   = lipgloss.NewStyle().Bold(true)
	selStyle    = lipgloss.NewStyle().Foreground(accent).Bold(true)
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#3fb950"))
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#f85149"))
	tagStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#d29922"))
	pinStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#f0883e"))
)

func header(env *Env, stack []Screen, width int) string {
	var crumbs []string
	for _, s := range stack {
		crumbs = append(crumbs, s.Title())
	}
	left := accentStyle.Render("▍textarea") + dimStyle.Render("  "+strings.Join(crumbs, " › "))
	return truncate(left, width)
}

func truncate(s string, width int) string {
	if width <= 0 {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

func keyOf(msg tea.Msg) string {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		return k.String()
	}
	return ""
}
