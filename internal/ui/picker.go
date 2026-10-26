package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/Gitguy268/textarea-cli/internal/filter"
	"github.com/Gitguy268/textarea-cli/internal/note"
	"github.com/Gitguy268/textarea-cli/internal/store"
)

// Row is one line of a picker.
type Row struct {
	Label  string
	Detail string
	Tag    string // right-hand badge
	Data   any
}

// PickerKey is an extra key a picker answers: Key pressed on the row under
// the cursor runs Run. Any keys also work on an empty list, with a zero Row.
type PickerKey struct {
	Key, Help string
	Any       bool
	Run       func(env *Env, row Row) tea.Cmd
}

// Picker is a filterable list. Rows come from load, which receives the
// search box, so a picker over notes can use the search grammar.
type Picker struct {
	title  string
	empty  string
	input  textinput.Model
	load   func(env *Env, query string) ([]Row, error)
	pick   func(env *Env, row Row) tea.Cmd
	keys   []PickerKey
	rows   []Row
	cursor int
	offset int
	typing bool
	err    error
	// enter names what Enter does in the help line; "open" when empty.
	enter string
}

// NewPicker makes a picker. typing starts with the search box focused.
func NewPicker(env *Env, title, empty string, typing bool, load func(env *Env, query string) ([]Row, error), pick func(env *Env, row Row) tea.Cmd, keys ...PickerKey) *Picker {
	in := textinput.New()
	in.Prompt = "🔎 "
	in.Placeholder = "type to filter · #tag · on:2026-10-25 · is:pinned · is:archived"
	p := &Picker{title: title, empty: empty, input: in, load: load, pick: pick, keys: keys, typing: typing}
	if typing {
		p.input.Focus()
	}
	p.reload(env)
	return p
}

func (p *Picker) Title() string { return p.title }

func (p *Picker) reload(env *Env) {
	p.rows, p.err = p.load(env, p.input.Value())
	if p.cursor >= len(p.rows) {
		p.cursor = max(0, len(p.rows)-1)
	}
}

func (p *Picker) refresh(env *Env) tea.Cmd {
	p.reload(env)
	return nil
}

func (p *Picker) Update(env *Env, msg tea.Msg) (Screen, tea.Cmd) {
	k := keyOf(msg)
	if k == "" {
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		return p, cmd
	}
	switch k {
	case "esc":
		if p.typing && p.input.Value() != "" {
			p.input.SetValue("")
			p.reload(env)
			return p, nil
		}
		return nil, nil
	case "up", "ctrl+p":
		p.move(-1)
		return p, nil
	case "down", "ctrl+n":
		p.move(1)
		return p, nil
	case "pgup":
		p.move(-10)
		return p, nil
	case "pgdown":
		p.move(10)
		return p, nil
	case "enter":
		if len(p.rows) == 0 || p.pick == nil {
			return p, nil
		}
		return p, p.pick(env, p.rows[p.cursor])
	case "/":
		if !p.typing {
			p.typing = true
			return p, p.input.Focus()
		}
	}
	if !p.typing {
		switch k {
		case "k":
			p.move(-1)
			return p, nil
		case "j":
			p.move(1)
			return p, nil
		case "q", "left", "h":
			return nil, nil
		}
		for _, pk := range p.keys {
			if pk.Key != k {
				continue
			}
			if len(p.rows) > 0 {
				return p, pk.Run(env, p.rows[p.cursor])
			}
			if pk.Any {
				return p, pk.Run(env, Row{})
			}
			return p, nil
		}
		// Any other letter starts a search.
		if len([]rune(k)) == 1 {
			p.typing = true
			p.input.Focus()
		} else {
			return p, nil
		}
	}
	before := p.input.Value()
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	if p.input.Value() != before {
		p.cursor, p.offset = 0, 0
		p.reload(env)
	}
	return p, cmd
}

func (p *Picker) move(d int) {
	if len(p.rows) == 0 {
		return
	}
	p.cursor = min(max(p.cursor+d, 0), len(p.rows)-1)
	if p.typing && d != 0 {
		// Arrowing through results leaves the box, so letters act as keys.
		p.typing = false
		p.input.Blur()
	}
}

func (p *Picker) View(env *Env, width, height int) string {
	var b strings.Builder
	p.input.SetWidth(max(10, width-6))
	b.WriteString(" " + p.input.View() + "\n\n")
	listHeight := height - 5
	if p.err != nil {
		b.WriteString(errorStyle.Render("  "+p.err.Error()) + "\n")
	}
	if len(p.rows) == 0 {
		empty := p.empty
		if p.input.Value() != "" {
			empty = "Nothing matches."
		}
		b.WriteString(dimStyle.Render("  "+empty) + "\n")
	}
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+listHeight {
		p.offset = p.cursor - listHeight + 1
	}
	for i := p.offset; i < len(p.rows) && i < p.offset+listHeight; i++ {
		r := p.rows[i]
		label := r.Label
		detail := ""
		if r.Detail != "" {
			detail = "  " + dimStyle.Render(r.Detail)
		}
		tag := ""
		if r.Tag != "" {
			tag = "  " + tagStyle.Render(r.Tag)
		}
		if i == p.cursor {
			b.WriteString(truncate(selStyle.Render(" ➤ "+label)+tag+detail, width) + "\n")
		} else {
			b.WriteString(truncate("   "+label+tag+detail, width) + "\n")
		}
	}
	help := []string{"↑/↓ move", "Enter " + firstNonEmpty(p.enter, "open"), "/ search"}
	// While the search box has focus letters are typed, so the row keys only
	// show once ↑/↓ has left it.
	for _, pk := range p.keys {
		if pk.Help != "" && !p.typing {
			help = append(help, pk.Key+" "+pk.Help)
		}
	}
	if p.typing && len(p.keys) > 0 {
		help = append(help, "↓ then a key for actions")
	}
	help = append(help, "Esc back")
	count := fmt.Sprintf("%d", len(p.rows))
	b.WriteString("\n" + dimStyle.Render(truncate("  "+count+" · "+strings.Join(help, " · "), width)))
	return b.String()
}

// ago is a short "edited" time, the way the cards show it.
func ago(ms int64) string {
	t := time.UnixMilli(ms)
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case t.Year() == time.Now().Year():
		return t.Format("2 Jan")
	}
	return t.Format("2 Jan 2006")
}

func firstLine(text string) string {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "#"))
		if l != "" {
			return l
		}
	}
	return ""
}

// preview is the line after the title, the card's second line.
func preview(e store.Entry) string {
	lines := strings.Split(e.Text, "\n")
	seenTitle := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if !seenTitle && strings.HasPrefix(t, "#") && e.Title != "Textarea" {
			seenTitle = true
			continue
		}
		return t
	}
	return ""
}

// Label names a note in lists: its title, or its first line when it has no
// heading (titleOf calls every such note "Textarea").
func Label(e note.Note) string {
	if e.Title == "Textarea" {
		if f := firstLine(e.Text); f != "" {
			return f
		}
	}
	return e.Title
}

// noteRows lists notes for a picker, filtered by the search grammar and
// sorted the way My documents is.
func noteRows(env *Env, query string) ([]Row, error) {
	entries, err := env.Lib.List()
	if err != nil {
		return nil, err
	}
	entries = store.Sorted(filter.Apply(entries, query), env.Cfg.Sort)
	rows := make([]Row, 0, len(entries))
	for _, e := range entries {
		label := Label(e.Note)
		if e.Pinned {
			label = pinStyle.Render("📌 ") + label
		}
		detail := ago(e.Updated)
		if p := preview(e); p != "" && p != label {
			detail = p + " · " + detail
		}
		rows = append(rows, Row{Label: label, Detail: detail, Data: e.ID})
	}
	return rows, nil
}
