package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitguy268/textarea-cli/internal/ai"
	"github.com/Gitguy268/textarea-cli/internal/config"
	"github.com/Gitguy268/textarea-cli/internal/export"
	"github.com/Gitguy268/textarea-cli/internal/filter"
	"github.com/Gitguy268/textarea-cli/internal/markdown"
	"github.com/Gitguy268/textarea-cli/internal/note"
	"github.com/Gitguy268/textarea-cli/internal/share"
	"github.com/Gitguy268/textarea-cli/internal/store"
	"github.com/Gitguy268/textarea-cli/internal/target"
)

// MainMenu is what `text` opens on.
func MainMenu(env *Env) *Menu {
	m := NewMenu(env, "Menu", "", mainItems)
	m.logo = true
	return m
}

func mainItems(env *Env) []Item {
	count := ""
	if entries, err := env.Lib.List(); err == nil {
		count = Count(len(entries), "note") + " in " + tildify(env.Lib.Dir)
	}
	items := []Item{
		{Key: "n", Label: "New note", Desc: "A blank page, saved as you leave it", Run: newNote},
		{Key: "o", Label: "Open…", Desc: count, Run: openPicker},
		{Key: "c", Label: "Quick capture", Desc: "One line, straight into a new note", Run: quickCapture},
		{Key: "d", Label: "Today", Desc: "Add a timestamped line to today's note", Run: today},
		{Key: "/", Label: "Search", Desc: "#tag · on:2026-10-25 · is:pinned · is:archived · words", Run: searchPicker},
		{Key: "t", Label: "Tasks", Desc: "Every open - [ ] across the library", Run: tasksPicker},
		{Key: "#", Label: "Tags", Desc: "Notes by #tag, nested ones included", Run: tagsPicker},
		{Key: "u", Label: "Open a link", Desc: "Paste a textarea link and keep the note", Run: openLink},
		{Key: "s", Label: "Sync ›", Desc: syncSummary(env), Run: func(env *Env) tea.Cmd { return push(syncMenu(env)) }},
		{Key: "r", Label: "Trash", Desc: trashSummary(env), Run: trashPicker},
	}
	if w, ok := ai.FindWhisper(env.Cfg.WhisperModel); ok {
		items = append(items, Item{Key: "v", Label: "Speech to text ›", Desc: "whisper.cpp · " + filepath.Base(w.Model), Run: func(env *Env) tea.Cmd { return push(speechMenu(env)) }})
	}
	items = append(items,
		Item{Key: ",", Label: "Settings ›", Desc: "Library, editor, sort, trash, models", Run: func(env *Env) tea.Cmd { return push(settingsMenu(env)) }},
		Item{Key: "?", Label: "Help", Desc: "Keys, and the commands for scripts", Run: help},
		Item{Key: "q", Label: "Quit", Desc: "", Run: func(*Env) tea.Cmd { return func() tea.Msg { return quitMsg{} } }},
	)
	return items
}

func tildify(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

func syncSummary(env *Env) string {
	if len(env.Cfg.Targets) == 0 {
		return "Obsidian, Apple Notes, or any folder (iCloud Drive, Dropbox…)"
	}
	var names []string
	for _, t := range env.Cfg.Targets {
		names = append(names, t.Name)
	}
	return strings.Join(names, ", ")
}

func trashSummary(env *Env) string {
	t, _ := env.Lib.ListTrash()
	return fmt.Sprintf("%d waiting · kept %s", len(t), Count(env.Cfg.TrashDays, "day"))
}

// ------------------------------------------------------------------ notes

func newNote(env *Env) tea.Cmd {
	now := store.Now()
	e := store.Entry{Note: note.Note{ID: store.NewID(), Created: now, Updated: now}}
	return push(NewEditor(e))
}

func openNote(env *Env, id string) tea.Cmd { return push(NewViewer(env, id)) }

func pickNote(env *Env, row Row) tea.Cmd { return openNote(env, row.Data.(string)) }

func notePickerKeys() []PickerKey {
	return []PickerKey{
		{Key: "e", Help: "edit", Run: func(env *Env, row Row) tea.Cmd {
			e, err := env.Lib.Get(row.Data.(string))
			if err != nil {
				return fail(err)
			}
			return editNote(env, e)
		}},
		{Key: "p", Help: "pin", Run: func(env *Env, row Row) tea.Cmd { return togglePin(env, row.Data.(string)) }},
		{Key: "D", Help: "trash", Run: func(env *Env, row Row) tea.Cmd { return trashNote(env, row.Data.(string), 0) }},
	}
}

func openPicker(env *Env) tea.Cmd {
	// A quick switcher: typing filters at once; Esc or an arrow leaves the
	// box, and then letters are the row keys.
	return push(NewPicker(env, "Open", "No notes yet. n makes one.", true, noteRows, pickNote, notePickerKeys()...))
}

func searchPicker(env *Env) tea.Cmd {
	return push(NewPicker(env, "Search", "Nothing matches.", true, noteRows, pickNote, notePickerKeys()...))
}

func queryPicker(env *Env, title, query string) tea.Cmd {
	p := NewPicker(env, title, "Nothing matches.", false, noteRows, pickNote, notePickerKeys()...)
	p.input.SetValue(query)
	p.reload(env)
	return push(p)
}

func quickCapture(env *Env) tea.Cmd {
	return push(NewPrompt("Capture", "What's on your mind?", "Becomes a note of its own. #tags work.", "", false,
		func(env *Env, value string) tea.Cmd {
			e, err := env.Lib.Create(value)
			if err != nil {
				return fail(err)
			}
			return tea.Batch(pop(), say("Captured · "+e.File))
		}))
}

// TodayTitle is the daily note's heading.
func TodayTitle(t time.Time) string { return t.Format("2006-01-02") }

// AppendToday adds "- HH:MM text" to today's note, making it first.
func AppendToday(lib *store.Library, text string) (store.Entry, error) {
	now := time.Now()
	title := TodayTitle(now)
	entries, err := lib.List()
	if err != nil {
		return store.Entry{}, err
	}
	e, ok := filter.FindByTitle(entries, title)
	if !ok {
		created, err := lib.Create("# " + title + "\n")
		if err != nil {
			return store.Entry{}, err
		}
		e = created
	}
	if strings.TrimSpace(text) == "" {
		return e, nil
	}
	body := strings.TrimRight(e.Text, "\n")
	e.Text = body + "\n- " + now.Format("15:04") + " " + strings.TrimSpace(text) + "\n"
	if !strings.Contains(body, "\n") {
		e.Text = body + "\n\n- " + now.Format("15:04") + " " + strings.TrimSpace(text) + "\n"
	}
	return lib.Save(e)
}

func today(env *Env) tea.Cmd {
	p := NewPrompt("Today", "Add to "+TodayTitle(time.Now()), "Enter on an empty line opens today's note instead.", "", false,
		func(env *Env, value string) tea.Cmd {
			e, err := AppendToday(env.Lib, value)
			if err != nil {
				return fail(err)
			}
			if strings.TrimSpace(value) == "" {
				return replace(NewViewer(env, e.ID))
			}
			return tea.Batch(pop(), say("Added to "+e.Title))
		})
	p.allowEmpty = true
	return push(p)
}

func togglePin(env *Env, id string) tea.Cmd {
	e, err := env.Lib.Get(id)
	if err != nil {
		return fail(err)
	}
	if err := env.Lib.SetFlags(id, !e.Pinned, e.Archived); err != nil {
		return fail(err)
	}
	if e.Pinned {
		return tea.Batch(say("Unpinned"), refreshTop())
	}
	return tea.Batch(say("Pinned to the top"), refreshTop())
}

func toggleArchive(env *Env, id string) tea.Cmd {
	e, err := env.Lib.Get(id)
	if err != nil {
		return fail(err)
	}
	if err := env.Lib.SetFlags(id, e.Pinned, !e.Archived); err != nil {
		return fail(err)
	}
	if e.Archived {
		return tea.Batch(say("Back from the archive"), refreshTop())
	}
	return tea.Batch(say("Archived · search is:archived to find it"), refreshTop())
}

func refreshTop() tea.Cmd { return func() tea.Msg { return popMsg{0} } }

func trashNote(env *Env, id string, leave int) tea.Cmd {
	if err := env.Lib.Trash(id); err != nil {
		return fail(err)
	}
	cmds := []tea.Cmd{say("Moved to the trash · Trash in the menu brings it back")}
	if leave > 0 {
		cmds = append(cmds, popN(leave))
	} else {
		cmds = append(cmds, refreshTop())
	}
	return tea.Batch(cmds...)
}

// noteActions is the note's own menu.
func noteActions(env *Env, id string) *Menu {
	return NewMenu(env, "Actions", "", func(env *Env) []Item {
		e, err := env.Lib.Get(id)
		if err != nil {
			return []Item{{Label: "Back", Desc: err.Error(), Run: func(*Env) tea.Cmd { return pop() }}}
		}
		pin, arch := "Pin", "Archive"
		if e.Pinned {
			pin = "Unpin"
		}
		if e.Archived {
			arch = "Unarchive"
		}
		items := []Item{
			{Key: "e", Label: "Edit", Desc: "Built-in editor, or $EDITOR from Settings", Run: func(env *Env) tea.Cmd { return tea.Batch(pop(), editNote(env, e)) }},
			{Key: "L", Label: "Share link", Desc: "A link that opens this note in the web app, optionally with a password", Run: func(env *Env) tea.Cmd { return shareLink(env, e) }},
			{Key: "Q", Label: "QR code", Desc: "The link as a symbol to scan with a phone", Run: func(env *Env) tea.Cmd { return showQR(env, e) }},
			{Key: "y", Label: "Copy as ›", Desc: "Any of the nine formats, on the clipboard", Run: func(env *Env) tea.Cmd { return push(formatMenu(env, e, true)) }},
			{Key: "x", Label: "Export ›", Desc: "Plain text, Markdown, HTML, JSON, CSV, XML, RTF, LaTeX, YAML", Run: func(env *Env) tea.Cmd { return push(formatMenu(env, e, false)) }},
			{Key: "p", Label: pin, Desc: "Kept at the top of the list", Run: func(env *Env) tea.Cmd { return togglePin(env, id) }},
			{Key: "a", Label: arch, Desc: "Out of the list, not deleted", Run: func(env *Env) tea.Cmd { return toggleArchive(env, id) }},
			{Key: "b", Label: "Backlinks", Desc: "Notes that link here with [[" + e.Title + "]]", Run: func(env *Env) tea.Cmd { return backlinks(env, e) }},
			{Key: "f", Label: "Follow a link", Desc: "Open a [[wikilink]] in this note", Run: func(env *Env) tea.Cmd { return followLinks(env, e) }},
			{Key: "i", Label: "Statistics", Desc: "Words, characters, reading time", Run: func(env *Env) tea.Cmd { return stats(env, e) }},
		}
		if ai.FindWriter(env.Cfg.LlamaModel) != nil {
			items = append(items, Item{Key: "w", Label: "Writing tools ›", Desc: "Improve, shorten, summarise, fix, casual: on this machine", Run: func(env *Env) tea.Cmd { return push(writingMenu(env, e)) }})
		}
		items = append(items, Item{Key: "D", Label: "Move to trash", Desc: "Kept " + Count(env.Cfg.TrashDays, "day"), Run: func(env *Env) tea.Cmd { return trashNote(env, id, 2) }})
		return items
	})
}

// noteKey is the viewer's shortcut for an action, nil when k is none.
func noteKey(env *Env, e store.Entry, k string) tea.Cmd {
	switch k {
	case "L":
		return shareLink(env, e)
	case "Q":
		return showQR(env, e)
	case "y":
		return push(formatMenu(env, e, true))
	case "p":
		return togglePin(env, e.ID)
	case "a":
		return toggleArchive(env, e.ID)
	case "b":
		return backlinks(env, e)
	case "f":
		return followLinks(env, e)
	case "i":
		return stats(env, e)
	case "w":
		if ai.FindWriter(env.Cfg.LlamaModel) != nil {
			return push(writingMenu(env, e))
		}
	case "D":
		// From the viewer: the note is gone, so is its page.
		return trashNote(env, e.ID, 1)
	}
	return nil
}

func exportNote(e store.Entry) export.Note {
	hash := "#" + share.EncodeHash(share.JoinContent(e.Text, e.Style))
	return export.Note{Text: e.Text, Title: e.Title, Style: e.Style, Hash: hash, ExportedAt: export.ISO(time.Now())}
}

func formatMenu(env *Env, e store.Entry, toClipboard bool) *Menu {
	title := "Export"
	if toClipboard {
		title = "Copy as"
	}
	return NewMenu(env, title, "", func(env *Env) []Item {
		var items []Item
		for _, f := range export.Formats {
			f := f
			items = append(items, Item{Label: f.Label, Desc: "." + f.Ext, Run: func(env *Env) tea.Cmd {
				text, _ := export.Convert(exportNote(e), f.ID)
				if toClipboard {
					return tea.Batch(popN(1), copyText(text))
				}
				dir, _ := os.Getwd()
				if home, err := os.UserHomeDir(); err == nil {
					if _, err := os.Stat(filepath.Join(home, "Downloads")); err == nil {
						dir = filepath.Join(home, "Downloads")
					}
				}
				path := filepath.Join(dir, note.SanitizeFilename(e.Title, "note")+"."+f.Ext)
				return push(NewPrompt("Save as", "Save "+f.Label+" to", "", tildify(path), false, func(env *Env, value string) tea.Cmd {
					if err := os.WriteFile(expand(value), []byte(text), 0o644); err != nil {
						return fail(err)
					}
					return tea.Batch(popN(2), say("Saved "+value))
				}))
			}})
		}
		return items
	})
}

func expand(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

func shareLink(env *Env, e store.Entry) tea.Cmd {
	p := NewPrompt("Share link", "Password (leave empty for none)", "With a password the link is AES-256-GCM encrypted, and the web app asks for it. The password is not in the link.", "", true,
		func(env *Env, password string) tea.Cmd {
			link, err := share.Link(e.Text, e.Style, password)
			if err != nil {
				return fail(err)
			}
			hash := share.HashOf(link)
			body := link
			if len(hash) > share.HashLimit {
				return replace(NewMessage("Share link", "This note is too long for a link (browsers refuse very long URLs). Export it as a file instead.", ""))
			}
			if float64(len(hash)) > share.HashLimit*0.75 {
				body += "\n\n" + dimStyle.Render("This link is getting long; some apps may cut it.")
			}
			keys := []MessageKey{}
			if password == "" && len(link) <= QRLimit {
				keys = append(keys, MessageKey{Key: "Q", Help: "QR code", Run: func(env *Env) tea.Cmd { return showQR(env, e) }})
			}
			return tea.Batch(replace(NewMessage("Share link", body, link, keys...)), copyText(link))
		})
	p.allowEmpty = true
	return push(p)
}

func showQR(env *Env, e store.Entry) tea.Cmd {
	link, _ := share.Link(e.Text, e.Style, "")
	if len(link) > QRLimit {
		return push(NewMessage("QR code", "This note is too long for a QR code: a symbol holds about 2.3 kB, and this link is "+strconv.Itoa(len(link))+" characters. Share link still works.", link))
	}
	qr, err := RenderQR(link)
	if err != nil {
		return fail(err)
	}
	return push(NewMessage("QR code", qr+"\n"+dimStyle.Render("Scan to open "+e.Title+" in the web app."), link))
}

func backlinks(env *Env, e store.Entry) tea.Cmd {
	return push(NewPicker(env, "Backlinks", "Nothing links to [["+e.Title+"]] yet.", false,
		func(env *Env, query string) ([]Row, error) {
			entries, err := env.Lib.List()
			if err != nil {
				return nil, err
			}
			var rows []Row
			for _, b := range filter.Backlinks(entries, e.Title, true) {
				tag := "mentions"
				if b.Linked {
					tag = "links"
				}
				if query != "" && !strings.Contains(strings.ToLower(b.Entry.Title+" "+b.Snippet), strings.ToLower(query)) {
					continue
				}
				rows = append(rows, Row{Label: b.Entry.Title, Tag: tag, Detail: b.Snippet, Data: b.Entry.ID})
			}
			return rows, nil
		}, pickNote))
}

func followLinks(env *Env, e store.Entry) tea.Cmd {
	titles := markdown.WikiTitles(e.Text)
	if len(titles) == 0 {
		return say("This note has no [[wikilinks]]")
	}
	return push(NewPicker(env, "Links", "", false,
		func(env *Env, query string) ([]Row, error) {
			entries, _ := env.Lib.List()
			seen := map[string]bool{}
			var rows []Row
			for _, t := range titles {
				if seen[strings.ToLower(t)] {
					continue
				}
				seen[strings.ToLower(t)] = true
				tag := "new"
				if _, ok := filter.FindByTitle(entries, t); ok {
					tag = ""
				}
				rows = append(rows, Row{Label: t, Tag: tag, Data: t})
			}
			return rows, nil
		},
		func(env *Env, row Row) tea.Cmd {
			title := row.Data.(string)
			entries, _ := env.Lib.List()
			if found, ok := filter.FindByTitle(entries, title); ok {
				return replace(NewViewer(env, found.ID))
			}
			created, err := env.Lib.Create("# " + title + "\n\n")
			if err != nil {
				return fail(err)
			}
			return tea.Batch(replace(NewViewer(env, created.ID)), say("Made "+created.File))
		}))
}

func stats(env *Env, e store.Entry) tea.Cmd {
	s := note.CountStats(e.Text)
	rows := [][2]string{
		{"Words", strconv.Itoa(s.Words)},
		{"Characters", strconv.Itoa(s.Characters)},
		{"Without spaces", strconv.Itoa(s.CharactersTight)},
		{"Lines", strconv.Itoa(s.Lines)},
		{"Paragraphs", strconv.Itoa(s.Paragraphs)},
		{"Sentences", strconv.Itoa(s.Sentences)},
		{"Reading time", note.FormatDuration(s.ReadingMinutes)},
		{"Speaking time", note.FormatDuration(s.SpeakingMinutes)},
		{"Created", time.UnixMilli(e.Created).Format("2 Jan 2006 15:04")},
		{"Edited", time.UnixMilli(e.Updated).Format("2 Jan 2006 15:04")},
		{"File", e.File},
	}
	if g := env.Cfg.WordGoal; g > 0 {
		rows = append(rows, [2]string{"Goal", fmt.Sprintf("%d of %d (%d%%)", s.Words, g, s.Words*100/g)})
	}
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(fmt.Sprintf("%-16s %s\n", dimStyle.Render(r[0]), r[1]))
	}
	return push(NewMessage("Statistics", b.String(), ""))
}

// ------------------------------------------------------------------ tasks

type taskRef struct {
	id   string
	line int
}

func tasksPicker(env *Env) tea.Cmd {
	showDone := false
	var p *Picker
	load := func(env *Env, query string) ([]Row, error) {
		entries, err := env.Lib.List()
		if err != nil {
			return nil, err
		}
		entries = store.Sorted(entries, "updated")
		var rows []Row
		for _, e := range entries {
			if e.Archived {
				continue
			}
			for _, t := range markdown.TasksIn(e.Text) {
				if t.Done && !showDone {
					continue
				}
				if query != "" && !strings.Contains(strings.ToLower(t.Text+" "+e.Title), strings.ToLower(query)) {
					continue
				}
				box := "☐ "
				if t.Done {
					box = "☑ "
				}
				rows = append(rows, Row{Label: box + t.Text, Detail: e.Title, Data: taskRef{e.ID, t.Line}})
			}
		}
		return rows, nil
	}
	toggle := func(env *Env, row Row) tea.Cmd {
		ref := row.Data.(taskRef)
		e, err := env.Lib.Get(ref.id)
		if err != nil {
			return fail(err)
		}
		e.Text = markdown.Toggle(e.Text, ref.line)
		if _, err := env.Lib.Save(e); err != nil {
			return fail(err)
		}
		return tea.Batch(say("Ticked · in "+e.Title), refreshTop())
	}
	p = NewPicker(env, "Tasks", "No open tasks. Write - [ ] in any note.", false, load,
		func(env *Env, row Row) tea.Cmd { return openNote(env, row.Data.(taskRef).id) },
		PickerKey{Key: "x", Help: "tick", Run: toggle},
		PickerKey{Key: "space", Help: "", Run: toggle},
		PickerKey{Key: "a", Help: "show done", Any: true, Run: func(env *Env, row Row) tea.Cmd {
			showDone = !showDone
			p.reload(env)
			return nil
		}},
	)
	return push(p)
}

func tagsPicker(env *Env) tea.Cmd {
	return push(NewPicker(env, "Tags", "No #tags yet.", false,
		func(env *Env, query string) ([]Row, error) {
			entries, err := env.Lib.List()
			if err != nil {
				return nil, err
			}
			var rows []Row
			for _, tc := range filter.TagCounts(entries) {
				if query != "" && !strings.Contains(tc.Tag, strings.ToLower(strings.TrimPrefix(query, "#"))) {
					continue
				}
				rows = append(rows, Row{Label: "#" + tc.Tag, Tag: strconv.Itoa(tc.Count), Data: tc.Tag})
			}
			return rows, nil
		},
		func(env *Env, row Row) tea.Cmd { return queryPicker(env, "#"+row.Data.(string), "#"+row.Data.(string)) }))
}

// ------------------------------------------------------------------ links

func openLink(env *Env) tea.Cmd {
	return push(NewPrompt("Open a link", "Paste a textarea link", "Anything with #… on the end: the web app's address bar, a QR code you scanned, or just the hash.", "", false,
		func(env *Env, value string) tea.Cmd {
			text, style, needs, err := share.Open(value, "")
			if err != nil {
				return fail(err)
			}
			if needs {
				return replace(NewPrompt("Open a link", "This note is encrypted. Password", "", "", true, func(env *Env, pw string) tea.Cmd {
					text, style, _, err := share.Open(value, pw)
					if err != nil {
						return fail(err)
					}
					return keepLinked(env, text, style)
				}))
			}
			return keepLinked(env, text, style)
		}))
}

func keepLinked(env *Env, text, style string) tea.Cmd {
	now := store.Now()
	e, err := env.Lib.Put(note.Note{ID: store.NewID(), Text: text, Style: style, Created: now, Updated: now})
	if err != nil {
		return fail(err)
	}
	return tea.Batch(replace(NewViewer(env, e.ID)), say("Kept as "+e.File))
}

// ------------------------------------------------------------------ trash

func trashPicker(env *Env) tea.Cmd {
	env.Lib.PurgeTrash(env.Cfg.TrashDays)
	p := NewPicker(env, "Trash", "The trash is empty.", false,
		func(env *Env, query string) ([]Row, error) {
			list, err := env.Lib.ListTrash()
			if err != nil {
				return nil, err
			}
			var rows []Row
			for _, t := range list {
				if query != "" && !strings.Contains(strings.ToLower(t.Title+" "+t.Text), strings.ToLower(query)) {
					continue
				}
				left := env.Cfg.TrashDays - int(time.Since(time.UnixMilli(t.DeletedAt)).Hours()/24)
				rows = append(rows, Row{Label: Label(t.Note), Detail: fmt.Sprintf("deleted %s · %s left", ago(t.DeletedAt), Count(left, "day")), Data: t.ID})
			}
			return rows, nil
		},
		func(env *Env, row Row) tea.Cmd {
			e, err := env.Lib.Restore(row.Data.(string))
			if err != nil {
				return fail(err)
			}
			return tea.Batch(say("Restored "+e.File), refreshTop())
		},
		PickerKey{Key: "D", Help: "delete forever", Run: func(env *Env, row Row) tea.Cmd {
			if err := env.Lib.DeleteForever(row.Data.(string)); err != nil {
				return fail(err)
			}
			return tea.Batch(say("Deleted for good"), refreshTop())
		}},
		PickerKey{Key: "E", Help: "empty", Any: true, Run: func(env *Env, row Row) tea.Cmd {
			list, _ := env.Lib.ListTrash()
			for _, t := range list {
				env.Lib.DeleteForever(t.ID)
			}
			return tea.Batch(say("Emptied the trash: "+Count(len(list), "note")), refreshTop())
		}},
	)
	p.enter = "restore"
	return push(p)
}

// ------------------------------------------------------------------- sync

type syncDoneMsg struct {
	report string
	err    error
}

// working waits for one slow job and then shows its answer.
type working struct {
	title, what string
	done        func(msg tea.Msg) (Screen, tea.Cmd, bool)
	started     time.Time
}

func (w *working) Title() string { return w.title }

func (w *working) Update(env *Env, msg tea.Msg) (Screen, tea.Cmd) {
	if s, cmd, ok := w.done(msg); ok {
		return s, cmd
	}
	if keyOf(msg) == "esc" {
		return nil, say("Still running in the background; its answer is dropped")
	}
	return w, nil
}

func (w *working) View(env *Env, width, height int) string {
	return "\n  " + accentStyle.Render("…") + " " + w.what + "\n\n" + dimStyle.Render("  Started "+w.started.Format("15:04:05"))
}

func runSync(env *Env, targets []config.Target, dryRun bool) tea.Cmd {
	what := "Syncing"
	if dryRun {
		what = "Working out what a sync would do"
	}
	w := &working{title: "Sync", what: what + "…", started: time.Now()}
	w.done = func(msg tea.Msg) (Screen, tea.Cmd, bool) {
		m, ok := msg.(syncDoneMsg)
		if !ok {
			return nil, nil, false
		}
		body := m.report
		if m.err != nil {
			body += "\n" + errorStyle.Render(m.err.Error())
		}
		keys := []MessageKey{}
		if dryRun && m.err == nil {
			keys = append(keys, MessageKey{Key: "r", Help: "run it for real", Run: func(env *Env) tea.Cmd {
				s, job := runSyncScreen(env, targets)
				return tea.Batch(replace(s), job)
			}})
		}
		return NewMessage("Sync", body, "", keys...), nil, true
	}
	return tea.Batch(push(w), syncJob(env, targets, dryRun))
}

func runSyncScreen(env *Env, targets []config.Target) (Screen, tea.Cmd) {
	w := &working{title: "Sync", what: "Syncing…", started: time.Now()}
	w.done = func(msg tea.Msg) (Screen, tea.Cmd, bool) {
		m, ok := msg.(syncDoneMsg)
		if !ok {
			return nil, nil, false
		}
		body := m.report
		if m.err != nil {
			body += "\n" + errorStyle.Render(m.err.Error())
		}
		return NewMessage("Sync", body, ""), nil, true
	}
	return w, syncJob(env, targets, false)
}

func syncJob(env *Env, targets []config.Target, dryRun bool) tea.Cmd {
	return func() tea.Msg {
		var lines []string
		for _, t := range targets {
			rep, err := target.Sync(env.Lib, t, dryRun)
			if err != nil {
				return syncDoneMsg{strings.Join(lines, "\n\n"), fmt.Errorf("%s: %w", t.Name, err)}
			}
			lines = append(lines, describe(rep))
		}
		return syncDoneMsg{strings.Join(lines, "\n\n"), nil}
	}
}

func describe(r target.Report) string {
	var b strings.Builder
	b.WriteString(boldStyle.Render(r.String()))
	list := func(label string, names []string) {
		if len(names) == 0 {
			return
		}
		b.WriteString("\n  " + dimStyle.Render(label) + " " + strings.Join(names, ", "))
	}
	list("written:", r.Written)
	list("imported:", r.Imported)
	list("files removed:", r.Removed)
	list("to the trash:", r.Trashed)
	return b.String()
}

func neverSynced(env *Env, t config.Target) bool {
	if t.Kind == "applenotes" {
		var slots map[string]any
		env.Lib.Extra("applenotes:"+t.Name, &slots)
		return len(slots) == 0
	}
	return len(env.Lib.Manifest("target:"+t.Name)) == 0
}

func syncMenu(env *Env) *Menu {
	return NewMenu(env, "Sync", "Newest wins. A first sync only adds; nothing is deleted until both sides know each other.", func(env *Env) []Item {
		var items []Item
		for _, t := range env.Cfg.Targets {
			t := t
			where := tildify(t.Path)
			if t.Kind == "applenotes" {
				where = "Apple Notes › " + t.Folder
			}
			items = append(items, Item{Label: "Sync " + t.Name, Desc: where, Run: func(env *Env) tea.Cmd {
				// The first pass against a target is shown before it runs.
				return runSync(env, []config.Target{t}, neverSynced(env, t))
			}})
		}
		if len(env.Cfg.Targets) > 1 {
			items = append(items, Item{Key: "a", Label: "Sync all", Desc: "Every target, one after another", Run: func(env *Env) tea.Cmd { return runSync(env, env.Cfg.Targets, false) }})
		}
		if len(env.Cfg.Targets) > 0 {
			items = append(items, Item{Key: "p", Label: "Preview", Desc: "What a sync of everything would do, changing nothing", Run: func(env *Env) tea.Cmd { return runSync(env, env.Cfg.Targets, true) }})
		}
		items = append(items,
			Item{Key: "f", Label: "Add a folder…", Desc: "iCloud Drive, Dropbox, Google Drive, OneDrive, a USB stick", Run: addFolder},
			Item{Key: "o", Label: "Add an Obsidian vault…", Desc: "Mirrors into a Textarea folder inside the vault", Run: addObsidian},
		)
		if runtime.GOOS == "darwin" {
			items = append(items, Item{Key: "n", Label: "Add Apple Notes…", Desc: "Two-way, in a Notes folder of its own", Run: addAppleNotes})
		}
		if len(env.Cfg.Targets) > 0 {
			items = append(items, Item{Key: "x", Label: "Remove a target…", Desc: "Stops syncing; deletes nothing", Run: removeTarget})
		}
		items = append(items, Item{Key: "w", Label: "Share with the web app", Desc: "Point its Folder sync at " + tildify(env.Lib.Dir), Run: func(env *Env) tea.Cmd {
			return push(NewMessage("The web app", "This library is a folder in the web app's own Folder sync format. In textarea (the browser app), open the menu, choose Folder sync, and pick:\n\n  "+env.Lib.Dir+"\n\nBoth apps then read and write the same notes. Folder sync needs Chrome, Edge, or Brave with the File System Access API switched on (brave://flags/#file-system-access-api).", env.Lib.Dir))
		}})
		return items
	})
}

func saveTarget(env *Env, t config.Target) tea.Cmd {
	env.Cfg.SetTarget(t)
	if err := env.Cfg.Save(); err != nil {
		return fail(err)
	}
	return tea.Batch(popN(1), say("Added "+t.Name+" · its first sync shows what it will do before doing it"))
}

func uniqueName(env *Env, base string) string {
	name := base
	for i := 2; ; i++ {
		if _, ok := env.Cfg.Target(name); !ok {
			return name
		}
		name = fmt.Sprintf("%s %d", base, i)
	}
}

func addFolder(env *Env) tea.Cmd {
	hint := "Any folder: a cloud drive's sync folder works as cloud sync."
	if home, err := os.UserHomeDir(); err == nil {
		icloud := filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs")
		if _, err := os.Stat(icloud); err == nil {
			hint += " iCloud Drive is " + tildify(icloud) + "."
		}
	}
	return push(NewPrompt("Add a folder", "Folder to mirror into", hint, "", false, func(env *Env, value string) tea.Cmd {
		path := expand(value)
		return saveTarget(env, config.Target{Name: uniqueName(env, filepath.Base(path)), Kind: "folder", Path: path})
	}))
}

func addObsidian(env *Env) tea.Cmd {
	vaults := target.ObsidianVaults()
	return push(NewPicker(env, "Obsidian", "Obsidian has no vaults on this computer. o types a path instead.", false,
		func(env *Env, query string) ([]Row, error) {
			var rows []Row
			for _, v := range vaults {
				tag := ""
				if v.Open {
					tag = "open"
				}
				rows = append(rows, Row{Label: filepath.Base(v.Path), Tag: tag, Detail: tildify(v.Path), Data: v.Path})
			}
			return rows, nil
		},
		func(env *Env, row Row) tea.Cmd {
			vault := row.Data.(string)
			return saveTarget(env, config.Target{Name: uniqueName(env, "Obsidian "+filepath.Base(vault)), Kind: "obsidian", Path: filepath.Join(vault, "Textarea")})
		},
		PickerKey{Key: "o", Help: "other path", Run: func(env *Env, row Row) tea.Cmd {
			return replace(NewPrompt("Obsidian", "Vault folder", "Notes go into a Textarea folder inside it.", "", false, func(env *Env, value string) tea.Cmd {
				vault := expand(value)
				return saveTarget(env, config.Target{Name: uniqueName(env, "Obsidian "+filepath.Base(vault)), Kind: "obsidian", Path: filepath.Join(vault, "Textarea")})
			}))
		}},
	))
}

func addAppleNotes(env *Env) tea.Cmd {
	return push(NewPrompt("Apple Notes", "Notes folder to use", "Created in your default Notes account if it does not exist. Only this folder is ever touched, and only notes this app made are ever deleted. macOS asks once whether this terminal may control Notes.", "Textarea", false,
		func(env *Env, value string) tea.Cmd {
			return saveTarget(env, config.Target{Name: uniqueName(env, "Apple Notes"), Kind: "applenotes", Folder: strings.TrimSpace(value)})
		}))
}

func removeTarget(env *Env) tea.Cmd {
	return push(NewPicker(env, "Remove", "", false,
		func(env *Env, query string) ([]Row, error) {
			var rows []Row
			for _, t := range env.Cfg.Targets {
				rows = append(rows, Row{Label: t.Name, Detail: t.Kind, Data: t.Name})
			}
			return rows, nil
		},
		func(env *Env, row Row) tea.Cmd {
			env.Cfg.RemoveTarget(row.Data.(string))
			if err := env.Cfg.Save(); err != nil {
				return fail(err)
			}
			return tea.Batch(pop(), say("Removed "+row.Data.(string)+" · nothing was deleted"))
		}))
}

// --------------------------------------------------------------- settings

func settingsMenu(env *Env) *Menu {
	return NewMenu(env, "Settings", "Saved in "+tildify(config.Path()), func(env *Env) []Item {
		c := env.Cfg
		save := func(msg string) tea.Cmd {
			if err := c.Save(); err != nil {
				return fail(err)
			}
			return tea.Batch(say(msg), refreshTop())
		}
		return []Item{
			{Key: "f", Label: "Library folder", Value: func(*Env) string { return tildify(c.Library) }, Run: func(env *Env) tea.Cmd {
				return push(NewPrompt("Library", "Library folder", "Notes already in the old folder stay there.", tildify(c.Library), false, func(env *Env, v string) tea.Cmd {
					lib, err := store.Open(expand(v))
					if err != nil {
						return fail(err)
					}
					env.Lib, c.Library = lib, expand(v)
					return tea.Batch(pop(), save("Library is now "+tildify(c.Library)))
				}))
			}},
			{Key: "e", Label: "Editor", Value: func(*Env) string {
				if c.Editor == "external" {
					return "$EDITOR (" + firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "not set: built-in") + ")"
				}
				return "built-in"
			}, Run: func(env *Env) tea.Cmd {
				if c.Editor == "external" {
					c.Editor = ""
				} else {
					c.Editor = "external"
				}
				return save("Editor changed")
			}},
			{Key: "o", Label: "Order", Value: func(*Env) string { return "by " + c.Sort }, Run: func(env *Env) tea.Cmd {
				c.Sort = map[string]string{"updated": "created", "created": "title", "title": "updated"}[c.Sort]
				return save("Notes ordered by " + c.Sort)
			}},
			{Key: "t", Label: "Trash keeps notes", Value: func(*Env) string { return strconv.Itoa(c.TrashDays) + " days" }, Run: func(env *Env) tea.Cmd {
				c.TrashDays = map[int]int{7: 30, 30: 90, 90: 7}[c.TrashDays]
				if c.TrashDays == 0 {
					c.TrashDays = 30
				}
				return save("Trash keeps notes " + strconv.Itoa(c.TrashDays) + " days")
			}},
			{Key: "w", Label: "Word-count goal", Value: func(*Env) string {
				if c.WordGoal == 0 {
					return "off"
				}
				return strconv.Itoa(c.WordGoal) + " words"
			}, Run: func(env *Env) tea.Cmd {
				return push(NewPrompt("Goal", "Words per note (0 for none)", "", strconv.Itoa(c.WordGoal), false, func(env *Env, v string) tea.Cmd {
					n, err := strconv.Atoi(strings.TrimSpace(v))
					if err != nil || n < 0 {
						return fail(fmt.Errorf("a whole number, please"))
					}
					c.WordGoal = n
					return tea.Batch(pop(), save("Goal saved"))
				}))
			}},
			{Key: "m", Label: "Writing model", Value: func(env *Env) string {
				if w := ai.FindWriter(c.LlamaModel); w != nil {
					return w.Name()
				}
				return "none found (llama.cpp, a llamafile, or Ollama)"
			}, Run: func(env *Env) tea.Cmd {
				return push(NewPrompt("Writing model", "A .gguf path, a .llamafile, or ollama:<model>", "Empty picks the first one found. Nothing is downloaded.", c.LlamaModel, false, func(env *Env, v string) tea.Cmd {
					c.LlamaModel = strings.TrimSpace(v)
					if !strings.HasPrefix(c.LlamaModel, "ollama:") && c.LlamaModel != "" {
						c.LlamaModel = expand(c.LlamaModel)
					}
					return tea.Batch(pop(), save("Writing model saved"))
				}))
			}},
			{Key: "s", Label: "Whisper model", Value: func(env *Env) string {
				if w, ok := ai.FindWhisper(c.WhisperModel); ok {
					return filepath.Base(w.Model)
				}
				return "needs whisper-cli (whisper.cpp) and a ggml model"
			}, Run: func(env *Env) tea.Cmd {
				return push(NewPrompt("Whisper model", "Path to a ggml-*.bin model", "", c.WhisperModel, false, func(env *Env, v string) tea.Cmd {
					c.WhisperModel = expand(v)
					return tea.Batch(pop(), save("Whisper model saved"))
				}))
			}},
			{Key: "l", Label: "Speech language", Value: func(*Env) string { return firstNonEmpty(c.Language, "auto") }, Run: func(env *Env) tea.Cmd {
				return push(NewPrompt("Language", "Language code (en, nl, de…) or auto", "", firstNonEmpty(c.Language, "auto"), false, func(env *Env, v string) tea.Cmd {
					c.Language = strings.TrimSpace(v)
					if c.Language == "auto" {
						c.Language = ""
					}
					return tea.Batch(pop(), save("Language saved"))
				}))
			}},
		}
	})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// ------------------------------------------------------------------- AI

type aiDoneMsg struct {
	text string
	err  error
}

func writingMenu(env *Env, e store.Entry) *Menu {
	return NewMenu(env, "Writing tools", "On this machine: the note never leaves it.", func(env *Env) []Item {
		var items []Item
		for _, t := range ai.Tools {
			t := t
			items = append(items, Item{Label: t.Label, Desc: t.Instruction, Run: func(env *Env) tea.Cmd { return runTool(env, e, t) }})
		}
		return items
	})
}

func runTool(env *Env, e store.Entry, t ai.Tool) tea.Cmd {
	w := ai.FindWriter(env.Cfg.LlamaModel)
	if w == nil {
		return fail(fmt.Errorf("no local model found"))
	}
	msgs, truncated, err := ai.BuildPrompt(t.ID, e.Text, "")
	if err != nil {
		return fail(err)
	}
	wait := &working{title: t.Label, what: t.Label + " with " + w.Name() + "…", started: time.Now()}
	wait.done = func(msg tea.Msg) (Screen, tea.Cmd, bool) {
		m, ok := msg.(aiDoneMsg)
		if !ok {
			return nil, nil, false
		}
		if m.err != nil {
			return NewMessage(t.Label, errorStyle.Render(m.err.Error()), ""), nil, true
		}
		body := m.text
		if truncated {
			body = dimStyle.Render("Only the first 24,000 characters were sent.") + "\n\n" + body
		}
		replaceKey := MessageKey{Key: "r", Help: "replace the note", Run: func(env *Env) tea.Cmd {
			fresh, err := env.Lib.Get(e.ID)
			if err != nil {
				return fail(err)
			}
			fresh.Text = m.text
			if _, err := env.Lib.Save(fresh); err != nil {
				return fail(err)
			}
			return tea.Batch(popN(2), say("Replaced · the old text is not kept, copy it first if you want it"))
		}}
		appendKey := MessageKey{Key: "a", Help: "add below", Run: func(env *Env) tea.Cmd {
			fresh, err := env.Lib.Get(e.ID)
			if err != nil {
				return fail(err)
			}
			fresh.Text = strings.TrimRight(fresh.Text, "\n") + "\n\n" + m.text + "\n"
			if _, err := env.Lib.Save(fresh); err != nil {
				return fail(err)
			}
			return tea.Batch(popN(2), say("Added below the note"))
		}}
		return NewMessage(t.Label, body, m.text, replaceKey, appendKey), nil, true
	}
	job := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		text, err := w.Chat(ctx, msgs)
		return aiDoneMsg{text, err}
	}
	return tea.Batch(push(wait), job)
}

func speechMenu(env *Env) *Menu {
	return NewMenu(env, "Speech to text", "whisper.cpp on this machine. The recording is never uploaded.", func(env *Env) []Item {
		items := []Item{}
		if ai.CanRecord() {
			items = append(items, Item{Key: "r", Label: "Record", Desc: "From the microphone; Enter stops", Run: record})
		}
		items = append(items, Item{Key: "f", Label: "Transcribe a file…", Desc: "Any audio ffmpeg reads, or a .wav", Run: func(env *Env) tea.Cmd {
			return push(NewPrompt("Transcribe", "Audio file", "", "", false, func(env *Env, v string) tea.Cmd {
				s, job := transcribing(env, expand(v), false)
				return tea.Batch(replace(s), job)
			}))
		}})
		return items
	})
}

type recorder struct {
	rec     *ai.Recorder
	started time.Time
}

func (r *recorder) Title() string { return "Recording" }

type tickMsg struct{}

func (r *recorder) Update(env *Env, msg tea.Msg) (Screen, tea.Cmd) {
	switch keyOf(msg) {
	case "enter", "space":
		if err := r.rec.Stop(); err != nil {
			return NewMessage("Recording", errorStyle.Render(err.Error()), ""), nil
		}
		return transcribing(env, r.rec.Path, true)
	case "esc":
		r.rec.Stop()
		os.Remove(r.rec.Path)
		return nil, say("Recording discarded")
	}
	if _, ok := msg.(tickMsg); ok {
		return r, tick()
	}
	return r, nil
}

func tick() tea.Cmd { return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} }) }

func (r *recorder) View(env *Env, width, height int) string {
	d := time.Since(r.started).Round(time.Second)
	return "\n  " + errorStyle.Render("●") + " Recording " + d.String() + "\n\n" + dimStyle.Render("  Enter stop and transcribe · Esc discard")
}

func record(env *Env) tea.Cmd {
	rec, err := ai.StartRecording()
	if err != nil {
		return fail(err)
	}
	return tea.Batch(push(&recorder{rec: rec, started: time.Now()}), tick())
}

func transcribing(env *Env, path string, temporary bool) (Screen, tea.Cmd) {
	w, _ := ai.FindWhisper(env.Cfg.WhisperModel)
	wait := &working{title: "Transcribing", what: "Transcribing with " + filepath.Base(w.Model) + "…", started: time.Now()}
	wait.done = func(msg tea.Msg) (Screen, tea.Cmd, bool) {
		m, ok := msg.(aiDoneMsg)
		if !ok {
			return nil, nil, false
		}
		if temporary {
			os.Remove(path)
		}
		if m.err != nil {
			return NewMessage("Transcript", errorStyle.Render(m.err.Error()), ""), nil, true
		}
		return NewMessage("Transcript", m.text, m.text,
			MessageKey{Key: "n", Help: "new note", Run: func(env *Env) tea.Cmd {
				e, err := env.Lib.Create(m.text)
				if err != nil {
					return fail(err)
				}
				return tea.Batch(replace(NewViewer(env, e.ID)), say("Kept as "+e.File))
			}},
			MessageKey{Key: "d", Help: "add to today", Run: func(env *Env) tea.Cmd {
				e, err := AppendToday(env.Lib, m.text)
				if err != nil {
					return fail(err)
				}
				return tea.Batch(pop(), say("Added to "+e.Title))
			}},
		), nil, true
	}
	return wait, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		text, err := w.Transcribe(ctx, path, env.Cfg.Language)
		return aiDoneMsg{text, err}
	}
}

// ------------------------------------------------------------------- help

// HelpText is the help screen and `text help`.
const HelpText = `In the menu
  ↑/↓ or j/k     move           1-9 or a letter   jump and choose
  Enter          choose         Esc               back
  Ctrl+C         quit (an open editor is saved first)

Reading a note
  e  edit        m  all actions      L  share link     Q  QR code
  y  copy as     p  pin              a  archive        b  backlinks
  f  follow a [[wikilink]]           i  statistics     w  writing tools
  Tab  next task, x ticks it         D  move to trash

From a shell
  text                        the menu
  text buy milk #home         capture a note without opening anything
  text -- rm the old boxes    capture words that start like a command
  echo … | text -             capture from stdin
  text today called the bank  add a timestamped line to today's note
  text ls [query]             list notes (search grammar, --json)
  text show <note>            print a note (by title, file or id)
  text edit <note>            edit in the built-in editor or $EDITOR
  text new [text]             new note, opened in the editor
  text tasks [--all]          open tasks across the library
  text link <note> [-p pw]    share link
  text open-link <url>        keep a note from a link
  text export <note> -f md    any of txt md html json csv xml rtf tex yml
  text sync [name] [--dry-run] [--watch]
  text rm <note>              move a note to the trash
  text trash [restore|empty]  the trash
  text config [path]          settings
`

func help(env *Env) tea.Cmd {
	return push(NewMessage("Help", HelpText, ""))
}
