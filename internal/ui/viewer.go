package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gitguy268/textarea-cli/internal/markdown"
	"github.com/Gitguy268/textarea-cli/internal/store"
)

var (
	h1Style     = lipgloss.NewStyle().Foreground(accent).Bold(true)
	hStyle      = lipgloss.NewStyle().Bold(true)
	codeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#a5d6ff"))
	linkStyle   = lipgloss.NewStyle().Foreground(accent).Underline(true)
	doneStyle   = lipgloss.NewStyle().Faint(true).Strikethrough(true)
	markerStyle = lipgloss.NewStyle().Foreground(accent)
	italicStyle = lipgloss.NewStyle().Italic(true)
	strikeStyle = lipgloss.NewStyle().Strikethrough(true)
)

// Highlight draws a note the way the web app colours it: the same walk,
// so a tag here is a tag there.
func Highlight(text string) []string {
	var lines []string
	var cur strings.Builder
	emit := func(s string) {
		parts := strings.Split(s, "\n")
		for i, p := range parts {
			if i > 0 {
				lines = append(lines, cur.String())
				cur.Reset()
			}
			cur.WriteString(p)
		}
	}
	styled := func(st lipgloss.Style, s string) {
		parts := strings.Split(s, "\n")
		for i, p := range parts {
			if i > 0 {
				lines = append(lines, cur.String())
				cur.Reset()
			}
			if p != "" {
				cur.WriteString(st.Render(p))
			}
		}
	}
	var node func(n markdown.Node, done bool)
	node = func(n markdown.Node, done bool) {
		switch n.Kind {
		case "text":
			if done {
				styled(doneStyle, n.Value)
			} else {
				emit(n.Value)
			}
		case "link":
			styled(linkStyle, n.Value)
		case "item":
			isDone := false
			for _, c := range n.Children {
				if c.Class == "md-task md-task-done" {
					isDone = true
				}
			}
			for _, c := range n.Children {
				node(c, isDone)
			}
		case "span":
			switch n.Class {
			case "md-h1":
				styled(h1Style, n.Value)
			case "md-h2", "md-h3", "md-h4", "md-h5", "md-h6":
				styled(hStyle, n.Value)
			case "md-code", "md-codeblock":
				styled(codeStyle, n.Value)
			case "md-bold":
				styled(boldStyle, n.Value)
			case "md-italic":
				styled(italicStyle, n.Value)
			case "md-strike":
				styled(strikeStyle, n.Value)
			case "md-tag":
				styled(tagStyle, n.Value)
			case "md-marker":
				styled(markerStyle, n.Value)
			case "md-task":
				styled(markerStyle, "☐")
				emit(n.Value[3:])
			case "md-task md-task-done":
				styled(markerStyle, "☑")
				emit(n.Value[3:])
			default:
				if done {
					styled(doneStyle, n.Value)
				} else {
					emit(n.Value)
				}
			}
		}
	}
	for _, n := range markdown.Render(text, markdown.All) {
		node(n, false)
	}
	lines = append(lines, cur.String())
	return lines
}

// Viewer is one note, read.
type Viewer struct {
	id     string
	entry  store.Entry
	scroll int
	task   int // index into tasks, -1 for none
	err    error
}

// NewViewer opens a note.
func NewViewer(env *Env, id string) *Viewer {
	v := &Viewer{id: id, task: -1}
	v.refresh(env)
	return v
}

func (v *Viewer) Title() string {
	if v.entry.Title == "Textarea" {
		if f := firstLine(v.entry.Text); f != "" {
			return truncate(f, 40)
		}
	}
	return truncate(v.entry.Title, 40)
}

func (v *Viewer) refresh(env *Env) tea.Cmd {
	v.entry, v.err = env.Lib.Get(v.id)
	return nil
}

func (v *Viewer) Update(env *Env, msg tea.Msg) (Screen, tea.Cmd) {
	if v.err != nil {
		if keyOf(msg) != "" {
			return nil, nil
		}
		return v, nil
	}
	tasks := markdown.TasksIn(v.entry.Text)
	switch k := keyOf(msg); k {
	case "esc", "q", "left", "h":
		return nil, nil
	case "down", "j":
		v.scroll++
	case "up", "k":
		v.scroll = max(0, v.scroll-1)
	case "pgdown", "space":
		v.scroll += 10
	case "pgup":
		v.scroll = max(0, v.scroll-10)
	case "g", "home":
		v.scroll = 0
	case "e", "enter":
		return v, editNote(env, v.entry)
	case "tab":
		if len(tasks) > 0 {
			v.task = (v.task + 1) % len(tasks)
		}
	case "shift+tab":
		if len(tasks) > 0 {
			v.task = (v.task - 1 + len(tasks)) % len(tasks)
		}
	case "x":
		if v.task >= 0 && v.task < len(tasks) {
			v.entry.Text = markdown.Toggle(v.entry.Text, tasks[v.task].Line)
			saved, err := env.Lib.Save(v.entry)
			if err != nil {
				return v, fail(err)
			}
			v.entry = saved
		}
	case "m", ".":
		return v, push(noteActions(env, v.entry.ID))
	default:
		if run := noteKey(env, v.entry, k); run != nil {
			return v, run
		}
	}
	return v, nil
}

func (v *Viewer) View(env *Env, width, height int) string {
	if v.err != nil {
		return "\n  " + errorStyle.Render(v.err.Error())
	}
	tasks := markdown.TasksIn(v.entry.Text)
	src := Highlight(v.entry.Text)
	if v.task >= 0 && v.task < len(tasks) {
		line := tasks[v.task].Line
		if line < len(src) {
			src[line] = lipgloss.NewStyle().Reverse(true).Render(ansi.Strip(src[line]))
		}
	}
	var lines []string
	for _, l := range src {
		lines = append(lines, strings.Split(ansi.Wrap(l, max(10, width-4), ""), "\n")...)
	}
	room := height - 2
	if v.scroll > len(lines)-room {
		v.scroll = max(0, len(lines)-room)
	}
	var b strings.Builder
	for _, l := range lines[v.scroll:min(len(lines), v.scroll+room)] {
		b.WriteString("  " + l + "\n")
	}
	for i := len(lines[v.scroll:min(len(lines), v.scroll+room)]); i < room; i++ {
		b.WriteString("\n")
	}
	meta := []string{ago(v.entry.Updated), v.entry.File}
	if v.entry.Pinned {
		meta = append(meta, "pinned")
	}
	if v.entry.Archived {
		meta = append(meta, "archived")
	}
	help := "e edit · m actions · Tab/x tasks · L link · y copy · Esc back"
	b.WriteString(dimStyle.Render(truncate("  "+strings.Join(meta, " · ")+"  │  "+help, width)))
	return b.String()
}

// Editor is the built-in editor: the note's source, plain, saved on Esc or
// Ctrl+S.
type Editor struct {
	entry store.Entry
	area  textarea.Model
	dirty bool
}

// NewEditor opens a note's source for editing.
func NewEditor(entry store.Entry) *Editor {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.MaxHeight = 0
	ta.CharLimit = 0
	ta.Prompt = ""
	ta.SetValue(entry.Text)
	ta.Focus()
	return &Editor{entry: entry, area: ta}
}

func (e *Editor) Title() string { return "Editing" }

func (e *Editor) save(env *Env) tea.Cmd {
	if e.area.Value() == e.entry.Text {
		return nil
	}
	e.entry.Text = e.area.Value()
	saved, err := env.Lib.Save(e.entry)
	if err != nil {
		return fail(err)
	}
	e.entry = saved
	return say("Saved " + saved.File)
}

// beforeQuit keeps an edit when the whole app is closed from the editor.
func (e *Editor) beforeQuit(env *Env) { e.save(env) }

func (e *Editor) Update(env *Env, msg tea.Msg) (Screen, tea.Cmd) {
	switch keyOf(msg) {
	case "esc":
		return nil, e.save(env)
	case "ctrl+s":
		return e, e.save(env)
	}
	var cmd tea.Cmd
	e.area, cmd = e.area.Update(msg)
	return e, cmd
}

func (e *Editor) View(env *Env, width, height int) string {
	e.area.SetWidth(max(10, width-4))
	e.area.SetHeight(max(3, height-2))
	words := 0
	for _, f := range strings.Fields(e.area.Value()) {
		if f != "" {
			words++
		}
	}
	return indent(e.area.View(), "  ") + "\n" + dimStyle.Render("  Esc save and close · Ctrl+S save · "+strconv.Itoa(words)+" words")
}

func indent(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

// editNote opens the built-in editor, or $EDITOR on the file itself when
// Settings asks for it.
func editNote(env *Env, e store.Entry) tea.Cmd {
	if env.Cfg.Editor != "external" {
		return push(NewEditor(e))
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		return push(NewEditor(e))
	}
	path := filepath.Join(env.Lib.Dir, e.File)
	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return statusMsg{"⚠ " + err.Error(), true}
		}
		if msg := afterExternalEdit(env, e); msg != "" {
			return statusMsg{msg, false}
		}
		return popMsg{0}
	})
}

// afterExternalEdit stamps the note properly once $EDITOR is done: the
// editor wrote the text, the library bumps `updated` and the word count.
func afterExternalEdit(env *Env, before store.Entry) string {
	after, err := env.Lib.Get(before.ID)
	if err != nil {
		return ""
	}
	if after.Text == before.Text {
		return ""
	}
	saved, err := env.Lib.Save(after)
	if err != nil {
		return "⚠ " + err.Error()
	}
	return "Saved " + saved.File
}
