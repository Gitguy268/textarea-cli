package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Item is one row of a menu. Key is its letter shortcut (never one of the
// movement keys h, j, k, l, g, G); Run is what Enter does. Value, when set, is shown at the right (a setting's current value).
type Item struct {
	Key   string
	Label string
	Desc  string
	Value func(env *Env) string
	Run   func(env *Env) tea.Cmd
}

// Menu is the Mole-style option list.
type Menu struct {
	title  string
	intro  string
	items  []Item
	cursor int
	build  func(env *Env) []Item // rebuilt when the menu comes back on top
	logo   bool
}

// NewMenu makes a menu whose rows are rebuilt from build each time it shows.
func NewMenu(env *Env, title, intro string, build func(env *Env) []Item) *Menu {
	return &Menu{title: title, intro: intro, build: build, items: build(env)}
}

func (m *Menu) Title() string { return m.title }

func (m *Menu) refresh(env *Env) tea.Cmd {
	m.items = m.build(env)
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	return nil
}

func (m *Menu) Update(env *Env, msg tea.Msg) (Screen, tea.Cmd) {
	k := keyOf(msg)
	if k == "" {
		return m, nil
	}
	switch k {
	case "up", "k", "ctrl+p":
		m.cursor = (m.cursor - 1 + len(m.items)) % len(m.items)
		return m, nil
	case "down", "j", "ctrl+n", "tab":
		m.cursor = (m.cursor + 1) % len(m.items)
		return m, nil
	case "home", "g":
		m.cursor = 0
		return m, nil
	case "end", "G":
		m.cursor = len(m.items) - 1
		return m, nil
	case "enter", "right", "l", "space":
		return m, m.run(env, m.cursor)
	case "esc", "left", "h", "backspace":
		return nil, nil
	}
	// A digit jumps to that row and runs it, the way Mole's menu does.
	if len(k) == 1 && k[0] >= '1' && k[0] <= '9' {
		i := int(k[0] - '1')
		if i < len(m.items) {
			m.cursor = i
			return m, m.run(env, i)
		}
	}
	for i, it := range m.items {
		if it.Key != "" && it.Key == k {
			m.cursor = i
			return m, m.run(env, i)
		}
	}
	return m, nil
}

func (m *Menu) run(env *Env, i int) tea.Cmd {
	if i < 0 || i >= len(m.items) || m.items[i].Run == nil {
		return nil
	}
	return m.items[i].Run(env)
}

const logoArt = `  ╺┳╸┏━╸╻ ╻╺┳╸
   ┃ ┣╸ ┏╋┛ ┃
   ╹ ┗━╸╹ ╹ ╹ `

func (m *Menu) View(env *Env, width, height int) string {
	var b strings.Builder
	if m.logo && height > len(m.items)+8 {
		b.WriteString(accentStyle.Render(logoArt) + "\n")
	}
	if m.intro != "" {
		b.WriteString(dimStyle.Render("  "+m.intro) + "\n")
	}
	b.WriteString("\n")
	labelWidth := 0
	for _, it := range m.items {
		if w := len([]rune(it.Label)); w > labelWidth {
			labelWidth = w
		}
	}
	for i, it := range m.items {
		num := "  "
		if i < 9 {
			num = fmt.Sprintf("%d ", i+1)
		}
		label := it.Label + strings.Repeat(" ", labelWidth-len([]rune(it.Label)))
		key := "   "
		if it.Key != "" {
			key = fmt.Sprintf("%-3s", it.Key)
		}
		desc := it.Desc
		if it.Value != nil {
			desc = it.Value(env)
		}
		line := ""
		if i == m.cursor {
			line = selStyle.Render(" ➤ "+num+label) + "  " + dimStyle.Render(key) + " " + desc
		} else {
			line = "   " + dimStyle.Render(num) + label + "  " + dimStyle.Render(key) + " " + dimStyle.Render(desc)
		}
		b.WriteString(truncate(line, width) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render(truncate("  ↑/↓ move · 1-9 or a letter to jump · Enter choose · Esc back · Ctrl+C quit", width)))
	return b.String()
}
