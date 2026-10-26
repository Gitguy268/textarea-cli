package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
	qrcode "github.com/skip2/go-qrcode"
)

// Prompt asks for one line.
type Prompt struct {
	title, question, hint string
	input                 textinput.Model
	submit                func(env *Env, value string) tea.Cmd
	allowEmpty            bool
}

// NewPrompt makes a one-line question. submit runs on Enter; it usually
// pops the prompt itself (or replaces it) as part of its command.
func NewPrompt(title, question, hint, initial string, password bool, submit func(env *Env, value string) tea.Cmd) *Prompt {
	in := textinput.New()
	in.Prompt = "› "
	in.SetValue(initial)
	in.CursorEnd()
	if password {
		in.EchoMode = textinput.EchoPassword
		in.EchoCharacter = '•'
	}
	in.Focus()
	return &Prompt{title: title, question: question, hint: hint, input: in, submit: submit}
}

func (p *Prompt) Title() string { return p.title }

func (p *Prompt) Update(env *Env, msg tea.Msg) (Screen, tea.Cmd) {
	switch keyOf(msg) {
	case "esc":
		return nil, nil
	case "enter":
		v := p.input.Value()
		if strings.TrimSpace(v) == "" && !p.allowEmpty {
			return p, nil
		}
		return p, p.submit(env, v)
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return p, cmd
}

func (p *Prompt) View(env *Env, width, height int) string {
	p.input.SetWidth(max(10, width-6))
	s := "\n  " + boldStyle.Render(p.question) + "\n\n  " + p.input.View() + "\n"
	if p.hint != "" {
		s += "\n" + dimStyle.Render(ansi.Wordwrap("  "+p.hint, width-2, "")) + "\n"
	}
	return s + "\n" + dimStyle.Render("  Enter confirm · Esc cancel")
}

// Message shows a block of text, with optional keys of its own.
type Message struct {
	title  string
	body   string
	copy   string // what c copies, when set
	keys   []MessageKey
	scroll int
}

// MessageKey is one extra key on a message screen.
type MessageKey struct {
	Key, Help string
	Run       func(env *Env) tea.Cmd
}

// NewMessage makes a text screen. copyValue, when set, is what c copies.
func NewMessage(title, body, copyValue string, keys ...MessageKey) *Message {
	return &Message{title: title, body: body, copy: copyValue, keys: keys}
}

func (m *Message) Title() string { return m.title }

func (m *Message) Update(env *Env, msg tea.Msg) (Screen, tea.Cmd) {
	k := keyOf(msg)
	switch k {
	case "esc", "q", "enter", "left", "h":
		return nil, nil
	case "c":
		if m.copy != "" {
			return m, copyText(m.copy)
		}
	case "down", "j":
		m.scroll++
	case "up", "k":
		m.scroll = max(0, m.scroll-1)
	}
	for _, mk := range m.keys {
		if mk.Key == k {
			return m, mk.Run(env)
		}
	}
	return m, nil
}

func (m *Message) View(env *Env, width, height int) string {
	lines := strings.Split(ansi.Wrap(m.body, max(10, width-4), ""), "\n")
	room := height - 3
	if m.scroll > len(lines)-room {
		m.scroll = max(0, len(lines)-room)
	}
	end := min(len(lines), m.scroll+room)
	var b strings.Builder
	b.WriteString("\n")
	for _, l := range lines[m.scroll:end] {
		b.WriteString("  " + l + "\n")
	}
	help := []string{}
	if m.copy != "" {
		help = append(help, "c copy")
	}
	for _, mk := range m.keys {
		help = append(help, mk.Key+" "+mk.Help)
	}
	help = append(help, "Esc back")
	b.WriteString("\n" + dimStyle.Render("  "+strings.Join(help, " · ")))
	return b.String()
}

// copyText puts text on the clipboard: the system's when there is one,
// and the terminal's (OSC 52, which reaches through SSH) as well.
func copyText(text string) tea.Cmd {
	err := clipboard.WriteAll(text)
	cmds := []tea.Cmd{tea.SetClipboard(text)}
	if err != nil {
		cmds = append(cmds, say("Sent to the terminal's clipboard (no system clipboard tool found)"))
	} else {
		cmds = append(cmds, say("Copied to the clipboard"))
	}
	return tea.Batch(cmds...)
}

// QRLimit is about what a symbol holds before it stops scanning reliably;
// the web app refuses at the same size.
const QRLimit = 2300

// RenderQR draws a symbol in half blocks, dark on light whatever the
// terminal's own colours are, so phones read it either way.
func RenderQR(content string) (string, error) {
	q, err := qrcode.New(content, qrcode.Low)
	if err != nil {
		return "", err
	}
	bits := q.Bitmap()
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("#ffffff"))
	var b strings.Builder
	for y := 0; y < len(bits); y += 2 {
		var row strings.Builder
		for x := range bits[y] {
			top := bits[y][x]
			bottom := y+1 < len(bits) && bits[y+1][x]
			switch {
			case top && bottom:
				row.WriteString("█")
			case top:
				row.WriteString("▀")
			case bottom:
				row.WriteString("▄")
			default:
				row.WriteString(" ")
			}
		}
		b.WriteString(style.Render(row.String()) + "\n")
	}
	return b.String(), nil
}

// Count is "1 note", "2 notes".
func Count(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// Fit cuts s to width terminal cells and pads it to exactly that, for
// columns that hold wide characters.
func Fit(s string, width int) string {
	s = ansi.Truncate(s, width, "…")
	return s + strings.Repeat(" ", width-ansi.StringWidth(s))
}
