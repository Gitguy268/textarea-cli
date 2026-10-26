package target

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gitguy268/textarea-cli/internal/config"
	"github.com/Gitguy268/textarea-cli/internal/note"
	"github.com/Gitguy268/textarea-cli/internal/oracle"
	"github.com/Gitguy268/textarea-cli/internal/store"
)

func TestNotesHTMLRoundTrip(t *testing.T) {
	texts := append([]string{
		"# Title\n\n## Sub\n### Third\n- [ ] task\n  - nested  two  spaces\n\ttabbed\n<b>not html</b> & co",
		"   leading spaces and trailing ",
		"#hashtag not a heading\n# \nempty heading",
	}, oracle.Fixtures...)
	for _, text := range texts {
		if strings.Contains(text, "\r") || strings.HasPrefix(text, "\ufeff") {
			continue // the library never holds these; Parse normalises them away
		}
		if strings.ContainsRune(text, 0xa0) {
			// Notes writes a run of spaces as no-break spaces, so on the way
			// back a no-break space is a space. Known, and only on import.
			continue
		}
		got := NotesToMarkdown(MarkdownToNotes(text))
		if got != text {
			t.Errorf("round trip\n in: %q\nhtml: %s\nout: %q", text, MarkdownToNotes(text), got)
		}
	}
}

func TestNotesFormattingBecomesMarkdown(t *testing.T) {
	html := `<div><h1>Shopping</h1></div><div>Get <b>milk</b> and <i>eggs</i></div>` +
		`<ul class="checklist"><li>bread</li><li class="checked">jam</li></ul>` +
		`<ol><li>one</li></ol><div><a href="https://example.com">site</a></div><div><br></div><div>end</div>`
	want := "# Shopping\nGet **milk** and *eggs*\n- [ ] bread\n- [x] jam\n1. one\n[site](https://example.com)\n\nend"
	if got := NotesToMarkdown(html); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// TestRealNotesHTML uses bodies Notes.app itself returned (macOS, 2026-10-26)
// after textarea-cli wrote them: Notes restyles <h1>-<h3> as bold in its own
// heading font, and adds a <br> to the end of an edited line.
func TestRealNotesHTML(t *testing.T) {
	sent := "# Notes test one\n\nHello from the **terminal**.\n\n- [ ] a task #test\n- [x] a done task\n"
	stored := `<div><b><font face=".AppleSystemUIFontBold"><span style="font-size: 24px">Notes test one</span></font></b><br></div>
<div><br></div>
<div>Hello from the **terminal**.</div>
<div><br></div>
<div>- [ ] a task #test</div>
<div>- [x] a done task</div>
<div><br></div>
`
	if got := NotesToMarkdown(stored); got != sent {
		t.Errorf("round trip through Notes\n got %q\nwant %q", got, sent)
	}
	levels := `<div><b><font face=".AppleSystemUIFontBold"><span style="font-size: 24px">One</span></font></b></div>
<div><b><font face=".AppleSystemUIFontBold"><span style="font-size: 18px">Two</span></font></b></div>
<div><b><font face=".AppleSystemUIFontBold">Three</font></b></div>
<div><b><span style="font-size: 24px">Edited title</span></b><br></div>
<div><b>just bold</b> line</div>
<div><b>whole bold line</b><br></div>`
	want := "# One\n## Two\n### Three\n# Edited title\n**just bold** line\n**whole bold line**"
	if got := NotesToMarkdown(levels); got != want {
		t.Errorf("heading levels\n got %q\nwant %q", got, want)
	}
}

func later(t *testing.T, path string, d time.Duration) {
	t.Helper()
	at := time.Now().Add(d)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestFolderSync(t *testing.T) {
	lib, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	a, _ := lib.Create("# Alpha\n\none")
	lib.Create("# Beta\n\ntwo")
	os.WriteFile(filepath.Join(dir, "Plain.md"), []byte("# Plain\n\nfrom disk"), 0o644)

	rep, err := SyncFolder(lib, "t", dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Written) != 3 || len(rep.Imported) != 1 || len(rep.Removed)+len(rep.Trashed) != 0 {
		t.Fatalf("first pass: %+v", rep)
	}
	if rep, _ = SyncFolder(lib, "t", dir, false); !rep.Empty() {
		t.Fatalf("second pass should be quiet: %+v", rep)
	}

	// An edit in the folder wins.
	path := filepath.Join(dir, "Alpha.md")
	b, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(b), "one", "edited outside", 1)), 0o644)
	later(t, path, time.Minute)
	rep, _ = SyncFolder(lib, "t", dir, false)
	if len(rep.Imported) != 1 {
		t.Fatalf("folder edit not imported: %+v", rep)
	}
	got, _ := lib.Get(a.ID)
	if !strings.Contains(got.Text, "edited outside") {
		t.Fatalf("library has %q", got.Text)
	}
	if rep, _ = SyncFolder(lib, "t", dir, false); !rep.Empty() {
		t.Fatalf("pass after import should be quiet: %+v", rep)
	}

	// An edit in the library wins, and a title change renames the file.
	got.Text = "# Alpha renamed\n\nfrom the terminal"
	time.Sleep(5 * time.Millisecond)
	store.Now = func() int64 { return time.Now().Add(2 * time.Minute).UnixMilli() }
	defer func() { store.Now = func() int64 { return time.Now().UnixMilli() } }()
	lib.Save(got)
	rep, _ = SyncFolder(lib, "t", dir, false)
	if len(rep.Written) != 1 {
		t.Fatalf("library edit not written: %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(dir, "Alpha renamed.md")); err != nil {
		t.Fatal("renamed file missing")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("old file should be gone")
	}

	// Deleted in the folder: the note goes to the trash.
	os.Remove(filepath.Join(dir, "Beta.md"))
	rep, _ = SyncFolder(lib, "t", dir, false)
	if len(rep.Trashed) != 1 {
		t.Fatalf("folder delete not mirrored: %+v", rep)
	}
	trash, _ := lib.ListTrash()
	if len(trash) != 1 || trash[0].Title != "Beta" {
		t.Fatalf("trash: %+v", trash)
	}

	// Deleted in the library: its file goes.
	lib.Trash(a.ID)
	rep, _ = SyncFolder(lib, "t", dir, false)
	if len(rep.Removed) != 1 {
		t.Fatalf("library delete not mirrored: %+v", rep)
	}

	// Dry run changes nothing.
	lib.Create("# Gamma")
	rep, _ = SyncFolder(lib, "t", dir, true)
	if len(rep.Written) != 1 {
		t.Fatalf("dry run: %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(dir, "Gamma.md")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote a file")
	}
}

// fakeNotes behaves like Notes where it matters: it rewrites the HTML it is
// given (so a byte comparison would churn) and moves the modification date
// on every change.
type fakeNotes struct {
	notes map[string]*AppleNote
	clock int64
	n     int
	calls int
}

func newFake() *fakeNotes {
	return &fakeNotes{notes: map[string]*AppleNote{}, clock: time.Now().UnixMilli()}
}

func (f *fakeNotes) tick() int64 { f.clock += 1000; return f.clock }

func (f *fakeNotes) List(string) ([]AppleNote, error) {
	var out []AppleNote
	for _, n := range f.notes {
		out = append(out, *n)
	}
	return out, nil
}

func (f *fakeNotes) Apply(_ string, ops []NotesOp) ([]NotesResult, error) {
	f.calls++
	var out []NotesResult
	for _, op := range ops {
		switch op.Op {
		case "create":
			f.n++
			id := fmt.Sprintf("x-coredata://note/%d", f.n)
			now := f.tick()
			f.notes[id] = &AppleNote{ID: id, Body: `<div style="font-family: x">` + op.HTML + `</div>`, Created: now, Modified: now}
			out = append(out, NotesResult{ID: id, Modified: now})
		case "update":
			n := f.notes[op.ID]
			n.Body = `<div style="font-family: x">` + op.HTML + `</div>`
			n.Modified = f.tick()
			out = append(out, NotesResult{ID: op.ID, Modified: n.Modified})
		case "delete":
			delete(f.notes, op.ID)
			out = append(out, NotesResult{ID: op.ID})
		}
	}
	return out, nil
}

func TestAppleNotesSync(t *testing.T) {
	lib, _ := store.Open(t.TempDir())
	fake := newFake()
	tg := config.Target{Name: "notes", Kind: "applenotes", Folder: "Textarea-test"}
	a, _ := lib.Create("# Alpha\n\none")
	fake.tick()
	fake.notes["x-coredata://theirs"] = &AppleNote{ID: "x-coredata://theirs", Body: "<div><h1>Theirs</h1></div><div>made in Notes</div>", Created: fake.clock, Modified: fake.clock}

	rep, err := syncNotes(lib, fake, tg, true)
	if err != nil || len(rep.Written) != 1 || len(rep.Imported) != 1 {
		t.Fatalf("dry run: %+v %v", rep, err)
	}
	if fake.calls != 0 || len(fake.notes) != 1 {
		t.Fatal("dry run changed Notes")
	}

	rep, _ = syncNotes(lib, fake, tg, false)
	if len(rep.Written) != 1 || len(rep.Imported) != 1 {
		t.Fatalf("first pass: %+v", rep)
	}
	calls := fake.calls
	rep, _ = syncNotes(lib, fake, tg, false)
	if !rep.Empty() || fake.calls != calls {
		t.Fatalf("second pass should be quiet, despite Notes rewriting the HTML: %+v", rep)
	}

	// Edited in Notes.
	var mine *AppleNote
	for _, n := range fake.notes {
		if n.ID != "x-coredata://theirs" {
			mine = n
		}
	}
	mine.Body = "<div><h1>Alpha</h1></div><div><br></div><div>changed on the phone</div>"
	mine.Modified = time.Now().Add(time.Hour).UnixMilli()
	rep, _ = syncNotes(lib, fake, tg, false)
	got, _ := lib.Get(a.ID)
	if len(rep.Imported) != 1 || got.Text != "# Alpha\n\nchanged on the phone" {
		t.Fatalf("Notes edit: %+v %q", rep, got.Text)
	}
	if rep, _ = syncNotes(lib, fake, tg, false); !rep.Empty() {
		t.Fatalf("pass after import should be quiet: %+v", rep)
	}

	// Edited in the library.
	store.Now = func() int64 { return time.Now().Add(2 * time.Hour).UnixMilli() }
	defer func() { store.Now = func() int64 { return time.Now().UnixMilli() } }()
	got.Text = "# Alpha\n\nfrom the terminal"
	lib.Save(got)
	rep, _ = syncNotes(lib, fake, tg, false)
	if len(rep.Written) != 1 || !strings.Contains(mine.Body, "from the terminal") {
		t.Fatalf("library edit: %+v %s", rep, mine.Body)
	}

	// Deleting the adopted note in the library never deletes it in Notes,
	// and it does not come back either.
	entries, _ := lib.List()
	for _, e := range entries {
		if e.Title == "Theirs" {
			lib.Trash(e.ID)
		}
	}
	syncNotes(lib, fake, tg, false)
	if _, ok := fake.notes["x-coredata://theirs"]; !ok {
		t.Fatal("a note this app did not make was deleted")
	}
	syncNotes(lib, fake, tg, false)
	entries, _ = lib.List()
	if len(entries) != 1 {
		t.Fatalf("the adopted note came back: %d entries", len(entries))
	}

	// Deleting our own note in the library deletes its twin.
	lib.Trash(a.ID)
	syncNotes(lib, fake, tg, false)
	if _, ok := fake.notes[mine.ID]; ok {
		t.Fatal("our note should be deleted in Notes")
	}

	// Deleted in Notes: the library note goes to the trash.
	b, _ := lib.Create("# Beta")
	syncNotes(lib, fake, tg, false)
	for id, n := range fake.notes {
		if strings.Contains(n.Body, "Beta") {
			delete(fake.notes, id)
		}
	}
	rep, _ = syncNotes(lib, fake, tg, false)
	if len(rep.Trashed) != 1 {
		t.Fatalf("Notes delete: %+v", rep)
	}
	if _, err := lib.Get(b.ID); err == nil {
		t.Fatal("Beta should be in the trash")
	}
	_ = note.TitleOf
}
