// Command text is textarea in the terminal.
//
//	text                  the menu
//	text some words       capture a note
//	text <command> …      see `text help`
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/Gitguy268/textarea-cli/internal/config"
	"github.com/Gitguy268/textarea-cli/internal/export"
	"github.com/Gitguy268/textarea-cli/internal/filter"
	"github.com/Gitguy268/textarea-cli/internal/markdown"
	"github.com/Gitguy268/textarea-cli/internal/note"
	"github.com/Gitguy268/textarea-cli/internal/share"
	"github.com/Gitguy268/textarea-cli/internal/store"
	"github.com/Gitguy268/textarea-cli/internal/target"
	"github.com/Gitguy268/textarea-cli/internal/ui"
)

var version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "text:", err)
		os.Exit(1)
	}
}

// commands are the first words that are not a capture.
var commands = map[string]func(env *ui.Env, args []string) error{
	"ls": cmdList, "list": cmdList,
	"show": cmdShow, "cat": cmdShow,
	"edit": cmdEdit, "new": cmdNew,
	"search": cmdList,
	"tasks":  cmdTasks,
	"today":  cmdToday,
	"link":   cmdLink, "open-link": cmdOpenLink,
	"export": cmdExport,
	"sync":   cmdSync,
	"trash":  cmdTrash,
	"rm":     cmdRemove,
	"config": cmdConfig,
	"menu":   func(env *ui.Env, _ []string) error { return runTUI(env, ui.MainMenu(env)) },
}

func run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			fmt.Print(ui.HelpText)
			return nil
		case "version", "-v", "--version":
			fmt.Println("text", version)
			return nil
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	lib, err := store.Open(cfg.Library)
	if err != nil {
		return err
	}
	lib.PurgeTrash(cfg.TrashDays)
	env := &ui.Env{Lib: lib, Cfg: cfg, Version: version}

	if len(args) == 0 {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			// Piped in with no arguments: a capture, like `text -`.
			return capture(env, readAll(os.Stdin))
		}
		return runTUI(env, ui.MainMenu(env))
	}
	if args[0] == "-" {
		return capture(env, readAll(os.Stdin))
	}
	if args[0] == "--" {
		// `text -- show the photos`: a capture, even when it starts like a command.
		return capture(env, strings.Join(args[1:], " "))
	}
	if cmd, ok := commands[args[0]]; ok {
		return cmd(env, args[1:])
	}
	if strings.HasPrefix(args[0], "-") && !strings.Contains(args[0], " ") {
		return fmt.Errorf("unknown flag %s; try `text help`", args[0])
	}
	return capture(env, strings.Join(args, " "))
}

func readAll(r io.Reader) string {
	b, _ := io.ReadAll(r)
	return strings.TrimRight(string(b), "\n")
}

func capture(env *ui.Env, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("nothing to capture")
	}
	e, err := env.Lib.Create(text + "\n")
	if err != nil {
		return err
	}
	fmt.Println("Captured", filepath.Join(env.Lib.Dir, e.File))
	return nil
}

func runTUI(env *ui.Env, first ui.Screen) error {
	_, err := tea.NewProgram(ui.New(env, first)).Run()
	return err
}

// resolve finds one note by id, id prefix, file name, title, or a unique
// piece of a title.
func resolve(lib *store.Library, query string) (store.Entry, error) {
	entries, err := lib.List()
	if err != nil {
		return store.Entry{}, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	for _, e := range entries {
		if e.ID == query || strings.ToLower(e.File) == q || strings.ToLower(e.File) == q+".md" {
			return e, nil
		}
	}
	var titled, partial []store.Entry
	for _, e := range entries {
		t := strings.ToLower(ui.Label(e.Note))
		switch {
		case t == q:
			titled = append(titled, e)
		case strings.Contains(t, q) || (len(q) >= 4 && strings.HasPrefix(e.ID, q)):
			partial = append(partial, e)
		}
	}
	pick := titled
	if len(pick) == 0 {
		pick = partial
	}
	switch len(pick) {
	case 0:
		return store.Entry{}, fmt.Errorf("no note matches %q", query)
	case 1:
		return pick[0], nil
	}
	var names []string
	for _, e := range store.Sorted(pick, "updated") {
		names = append(names, "  "+e.ID[:8]+"  "+ui.Label(e.Note))
	}
	return store.Entry{}, fmt.Errorf("%q matches %d notes; be more specific:\n%s", query, len(pick), strings.Join(names, "\n"))
}

type jsonNote struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Label    string   `json:"label"` // the title, or the first line when there's no heading
	File     string   `json:"file"`
	Created  string   `json:"created"`
	Updated  string   `json:"updated"`
	Words    int      `json:"words"`
	Pinned   bool     `json:"pinned"`
	Archived bool     `json:"archived"`
	Tags     []string `json:"tags"`
	Text     string   `json:"text,omitempty"`
}

func toJSON(e store.Entry, withText bool) jsonNote {
	tags := markdown.TagsIn(e.Text)
	if tags == nil {
		tags = []string{}
	}
	j := jsonNote{e.ID, e.Title, ui.Label(e.Note), e.File, export.ISO(time.UnixMilli(e.Created)), export.ISO(time.UnixMilli(e.Updated)), e.Words, e.Pinned, e.Archived, tags, ""}
	if withText {
		j.Text = e.Text
	}
	return j
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("text "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

// parse lets flags come after the positional words too: `text ls work --json`.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var rest []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		rest = append(rest, args[0])
		args = args[1:]
	}
	return rest, nil
}

func cmdList(env *ui.Env, args []string) error {
	fs := flags("ls")
	asJSON := fs.Bool("json", false, "print JSON")
	withText := fs.Bool("text", false, "with --json, include each note's text")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	entries, err := env.Lib.List()
	if err != nil {
		return err
	}
	entries = store.Sorted(filter.Apply(entries, strings.Join(rest, " ")), env.Cfg.Sort)
	if *asJSON {
		out := []jsonNote{}
		for _, e := range entries {
			out = append(out, toJSON(e, *withText))
		}
		return printJSON(out)
	}
	for _, e := range entries {
		pin := " "
		if e.Pinned {
			pin = "*"
		}
		fmt.Printf("%s %s  %s %s\n", pin, e.ID[:8], ui.Fit(ui.Label(e.Note), 40), time.UnixMilli(e.Updated).Format("2006-01-02 15:04"))
	}
	return nil
}

func one(env *ui.Env, args []string, what string) (store.Entry, error) {
	if len(args) == 0 {
		return store.Entry{}, fmt.Errorf("which note? `text %s <title, file or id>`", what)
	}
	return resolve(env.Lib, strings.Join(args, " "))
}

func cmdShow(env *ui.Env, args []string) error {
	fs := flags("show")
	asJSON := fs.Bool("json", false, "print JSON")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	e, err := one(env, rest, "show")
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(toJSON(e, true))
	}
	fmt.Print(e.Text)
	if !strings.HasSuffix(e.Text, "\n") {
		fmt.Println()
	}
	return nil
}

func cmdEdit(env *ui.Env, args []string) error {
	e, err := one(env, args, "edit")
	if err != nil {
		return err
	}
	if editor := externalEditor(env); editor != nil {
		cmd := exec.Command(editor[0], append(editor[1:], filepath.Join(env.Lib.Dir, e.File))...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
		if after, err := env.Lib.Get(e.ID); err == nil && after.Text != e.Text {
			_, err = env.Lib.Save(after)
			return err
		}
		return nil
	}
	return runTUI(env, ui.NewEditor(e))
}

// externalEditor is $VISUAL or $EDITOR when Settings asks for it, else nil.
func externalEditor(env *ui.Env) []string {
	if env.Cfg.Editor != "external" {
		return nil
	}
	for _, v := range []string{"VISUAL", "EDITOR"} {
		if parts := strings.Fields(os.Getenv(v)); len(parts) > 0 {
			return parts
		}
	}
	return nil
}

func cmdNew(env *ui.Env, args []string) error {
	text := strings.Join(args, " ")
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return capture(env, readAll(os.Stdin))
	}
	now := store.Now()
	e := store.Entry{Note: note.Note{ID: store.NewID(), Created: now, Updated: now, Text: ""}}
	ed := ui.NewEditor(e)
	if text != "" {
		saved, err := env.Lib.Create(text + "\n")
		if err != nil {
			return err
		}
		ed = ui.NewEditor(saved)
	}
	return runTUI(env, ed)
}

func cmdTasks(env *ui.Env, args []string) error {
	fs := flags("tasks")
	all := fs.Bool("all", false, "include done tasks")
	asJSON := fs.Bool("json", false, "print JSON")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	entries, err := env.Lib.List()
	if err != nil {
		return err
	}
	type task struct {
		Done bool   `json:"done"`
		Text string `json:"text"`
		Note string `json:"note"`
		ID   string `json:"id"`
		Line int    `json:"line"`
	}
	out := []task{}
	query := strings.ToLower(strings.Join(rest, " "))
	for _, e := range store.Sorted(entries, "updated") {
		if e.Archived {
			continue
		}
		for _, t := range markdown.TasksIn(e.Text) {
			if (t.Done && !*all) || (query != "" && !strings.Contains(strings.ToLower(t.Text+" "+e.Title), query)) {
				continue
			}
			out = append(out, task{t.Done, t.Text, e.Title, e.ID, t.Line + 1})
		}
	}
	if *asJSON {
		return printJSON(out)
	}
	for _, t := range out {
		box := "[ ]"
		if t.Done {
			box = "[x]"
		}
		fmt.Printf("%s %s  — %s\n", box, t.Text, t.Note)
	}
	return nil
}

func cmdToday(env *ui.Env, args []string) error {
	text := strings.Join(args, " ")
	if text == "" && !term.IsTerminal(int(os.Stdin.Fd())) {
		text = readAll(os.Stdin)
	}
	e, err := ui.AppendToday(env.Lib, text)
	if err != nil {
		return err
	}
	if text == "" {
		fmt.Print(e.Text)
		return nil
	}
	fmt.Println("Added to", e.File)
	return nil
}

func cmdLink(env *ui.Env, args []string) error {
	fs := flags("link")
	password := fs.String("p", "", "encrypt the link with this password")
	ask := fs.Bool("ask", false, "ask for a password without echoing it")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	e, err := one(env, rest, "link")
	if err != nil {
		return err
	}
	pw := *password
	if *ask {
		if pw, err = readPassword("Password: "); err != nil {
			return err
		}
	}
	link, err := share.Link(e.Text, e.Style, pw)
	if err != nil {
		return err
	}
	if len(share.HashOf(link)) > share.HashLimit {
		return errors.New("this note is too long for a link; export it instead")
	}
	fmt.Println(link)
	return nil
}

func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

func cmdOpenLink(env *ui.Env, args []string) error {
	fs := flags("open-link")
	password := fs.String("p", "", "password for an encrypted link")
	print := fs.Bool("print", false, "print the note instead of keeping it")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return errors.New("paste the link: `text open-link '<url>'`")
	}
	link := strings.Join(rest, " ")
	text, style, needs, err := share.Open(link, *password)
	if needs {
		pw, err2 := readPassword("This link is encrypted. Password: ")
		if err2 != nil {
			return err2
		}
		text, style, _, err = share.Open(link, pw)
	}
	if err != nil {
		return err
	}
	if *print {
		fmt.Println(text)
		return nil
	}
	now := store.Now()
	e, err := env.Lib.Put(note.Note{ID: store.NewID(), Text: text, Style: style, Created: now, Updated: now})
	if err != nil {
		return err
	}
	fmt.Println("Kept as", filepath.Join(env.Lib.Dir, e.File))
	return nil
}

func cmdExport(env *ui.Env, args []string) error {
	fs := flags("export")
	format := fs.String("f", "md", "format: txt md html json csv xml rtf tex yml")
	out := fs.String("o", "", "file to write (default: print)")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	e, err := one(env, rest, "export")
	if err != nil {
		return err
	}
	f := export.GetFormat(*format)
	if f.ID != *format {
		return fmt.Errorf("unknown format %q", *format)
	}
	hash := "#" + share.EncodeHash(share.JoinContent(e.Text, e.Style))
	text, _ := export.Convert(export.Note{Text: e.Text, Title: e.Title, Style: e.Style, Hash: hash, ExportedAt: export.ISO(time.Now())}, f.ID)
	if *out == "" {
		fmt.Print(text)
		return nil
	}
	if err := os.WriteFile(*out, []byte(text), 0o644); err != nil {
		return err
	}
	fmt.Println("Saved", *out)
	return nil
}

func cmdSync(env *ui.Env, args []string) error {
	fs := flags("sync")
	dry := fs.Bool("dry-run", false, "show what would change, change nothing")
	watch := fs.Duration("watch", 0, "keep syncing every interval, e.g. --watch 30s")
	add := fs.String("add", "", "add a target: folder, obsidian or applenotes")
	path := fs.String("path", "", "with --add: the folder (for obsidian, the vault)")
	folder := fs.String("folder", "Textarea", "with --add applenotes: the Notes folder")
	name := fs.String("name", "", "with --add: a name for the target")
	remove := fs.String("remove", "", "stop syncing the named target (deletes nothing)")
	list := fs.Bool("list", false, "list targets")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	cfg := env.Cfg
	switch {
	case *list:
		for _, t := range cfg.Targets {
			where := t.Path
			if t.Kind == "applenotes" {
				where = "Apple Notes › " + t.Folder
			}
			fmt.Printf("%-20s %-10s %s\n", t.Name, t.Kind, where)
		}
		return nil
	case *remove != "":
		if _, ok := cfg.Target(*remove); !ok {
			return fmt.Errorf("no target named %q", *remove)
		}
		cfg.RemoveTarget(*remove)
		return cfg.Save()
	case *add != "":
		t := config.Target{Kind: *add, Name: *name}
		switch *add {
		case "folder":
			if *path == "" {
				return errors.New("--add folder needs --path")
			}
			t.Path, _ = filepath.Abs(*path)
		case "obsidian":
			vault := *path
			if vault == "" {
				vaults := target.ObsidianVaults()
				if len(vaults) != 1 {
					return fmt.Errorf("Obsidian knows %d vaults; pick one with --path", len(vaults))
				}
				vault = vaults[0].Path
			}
			vault, _ = filepath.Abs(vault)
			t.Path = filepath.Join(vault, "Textarea")
		case "applenotes":
			t.Folder = *folder
		default:
			return fmt.Errorf("unknown target kind %q", *add)
		}
		if t.Name == "" {
			t.Name = map[string]string{"folder": filepath.Base(t.Path), "obsidian": "Obsidian", "applenotes": "Apple Notes"}[*add]
		}
		cfg.SetTarget(t)
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Println("Added", t.Name, "· run `text sync --dry-run` to see what the first sync will do")
		return nil
	}

	targets := cfg.Targets
	if len(rest) > 0 {
		t, ok := cfg.Target(strings.Join(rest, " "))
		if !ok {
			return fmt.Errorf("no target named %q (text sync --list)", strings.Join(rest, " "))
		}
		targets = []config.Target{t}
	}
	if len(targets) == 0 {
		return errors.New("no sync targets yet: `text sync --add folder --path ~/Dropbox/Notes`, --add obsidian or --add applenotes")
	}
	pass := func() error {
		for _, t := range targets {
			rep, err := target.Sync(env.Lib, t, *dry)
			if err != nil {
				return fmt.Errorf("%s: %w", t.Name, err)
			}
			if *watch == 0 || !rep.Empty() {
				fmt.Println(time.Now().Format("15:04:05"), rep)
			}
		}
		return nil
	}
	if *watch == 0 {
		return pass()
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	for {
		if err := pass(); err != nil {
			fmt.Fprintln(os.Stderr, "text:", err)
		}
		select {
		case <-stop:
			return nil
		case <-time.After(*watch):
		}
	}
}

func cmdTrash(env *ui.Env, args []string) error {
	list, err := env.Lib.ListTrash()
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "ls" {
		for _, t := range list {
			fmt.Printf("%s  %s deleted %s\n", t.ID[:8], ui.Fit(ui.Label(t.Note), 40), time.UnixMilli(t.DeletedAt).Format("2006-01-02 15:04"))
		}
		return nil
	}
	switch args[0] {
	case "empty":
		for _, t := range list {
			env.Lib.DeleteForever(t.ID)
		}
		fmt.Println("Emptied the trash:", ui.Count(len(list), "note"))
		return nil
	case "restore":
		if len(args) < 2 {
			return errors.New("`text trash restore <id or title>`")
		}
		q := strings.ToLower(strings.Join(args[1:], " "))
		for _, t := range list {
			if strings.HasPrefix(t.ID, q) || strings.Contains(strings.ToLower(ui.Label(t.Note)), q) {
				e, err := env.Lib.Restore(t.ID)
				if err != nil {
					return err
				}
				fmt.Println("Restored", e.File)
				return nil
			}
		}
		return fmt.Errorf("nothing in the trash matches %q", q)
	case "rm":
		return cmdRemove(env, args[1:])
	}
	return fmt.Errorf("unknown trash command %q (ls, restore, empty, rm)", args[0])
}

func cmdRemove(env *ui.Env, args []string) error {
	e, err := one(env, args, "rm")
	if err != nil {
		return err
	}
	if err := env.Lib.Trash(e.ID); err != nil {
		return err
	}
	fmt.Printf("Moved %s to the trash (kept %s)\n", e.File, ui.Count(env.Cfg.TrashDays, "day"))
	return nil
}

func cmdConfig(env *ui.Env, args []string) error {
	if len(args) > 0 && args[0] == "path" {
		fmt.Println(config.Path())
		return nil
	}
	return printJSON(env.Cfg)
}
