// Package store is the terminal's document library: a folder of markdown
// files in textarea's Folder sync format, so the web app can point its
// Folder sync straight at it and both see the same notes.
//
// What the format has no room for lives next to it in .textarea/:
// state.json holds pins, the archive, the trash's dates and each sync
// target's manifest, and trash/ holds deleted notes for their thirty days.
// Nothing in there is needed to read a note; deleting the directory loses
// only those extras.
package store

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Gitguy268/textarea-cli/internal/note"
	"github.com/Gitguy268/textarea-cli/internal/syncplan"
)

// The same budgets the web app's Folder sync keeps.
const (
	MaxFileBytes  = 10 << 20
	MaxTotalBytes = 100 << 20
	MaxFiles      = 1000
	metaDir       = ".textarea"
)

// Entry is a note plus where it lives and its flags.
type Entry struct {
	note.Note
	File     string
	Pinned   bool
	Archived bool
}

// Trashed is a note waiting in the trash.
type Trashed struct {
	note.Note
	DeletedAt int64
}

// State is .textarea/state.json.
type State struct {
	Pinned    map[string]bool              `json:"pinned"`
	Archived  map[string]bool              `json:"archived"`
	Trash     map[string]int64             `json:"trash"`
	Manifests map[string]syncplan.Manifest `json:"manifests"`
	// Extra holds per-target state that is not a planSync manifest, such as
	// Apple Notes' modification dates.
	Extra map[string]json.RawMessage `json:"extra,omitempty"`
}

// Library is one library folder.
type Library struct {
	Dir   string
	mu    sync.Mutex
	state State
	// Skipped names the files left alone on the last load, over budget.
	Skipped []string
}

// Now is the clock, in epoch milliseconds; tests replace it.
var Now = func() int64 { return time.Now().UnixMilli() }

// NewID is a random UUID v4, the shape crypto.randomUUID gives the web app.
func NewID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Open makes the folder if needed and reads its state.
func Open(dir string) (*Library, error) {
	if err := os.MkdirAll(filepath.Join(dir, metaDir, "trash"), 0o755); err != nil {
		return nil, err
	}
	l := &Library{Dir: dir}
	l.state = State{}
	if b, err := os.ReadFile(l.statePath()); err == nil {
		if err := json.Unmarshal(b, &l.state); err != nil {
			return nil, fmt.Errorf("%s is not readable: %w", l.statePath(), err)
		}
	}
	l.fill()
	return l, nil
}

func (l *Library) fill() {
	if l.state.Pinned == nil {
		l.state.Pinned = map[string]bool{}
	}
	if l.state.Archived == nil {
		l.state.Archived = map[string]bool{}
	}
	if l.state.Trash == nil {
		l.state.Trash = map[string]int64{}
	}
	if l.state.Manifests == nil {
		l.state.Manifests = map[string]syncplan.Manifest{}
	}
	if l.state.Extra == nil {
		l.state.Extra = map[string]json.RawMessage{}
	}
}

func (l *Library) statePath() string { return filepath.Join(l.Dir, metaDir, "state.json") }
func (l *Library) trashPath(id string) string {
	return filepath.Join(l.Dir, metaDir, "trash", note.SanitizeFilename(id, "note")+".md")
}

func (l *Library) saveState() error {
	b, _ := json.MarshalIndent(l.state, "", "  ")
	return WriteFileAtomic(l.statePath(), b)
}

// WriteFileAtomic writes through a temporary file and a rename, so a crash
// never leaves half a note. A file that is already there keeps its mode; a
// new one is 0644, like a file any other editor saves into the folder.
func WriteFileAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ReadFolder is the web app's gather(): every top-level markdown file,
// parsed, within the budgets. Over-budget files are probed for their block
// and never read.
func ReadFolder(dir string) ([]syncplan.File, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("the folder %s could not be read: %w", dir, err)
	}
	var files []syncplan.File
	var total int64
	for _, it := range items {
		if !it.Type().IsRegular() || !strings.HasSuffix(strings.ToLower(it.Name()), ".md") {
			continue
		}
		if len(files) >= MaxFiles {
			return nil, fmt.Errorf("%s holds more than %d markdown files, which is more than one pass can tell apart", dir, MaxFiles)
		}
		info, err := it.Info()
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, it.Name())
		mtime := info.ModTime().UnixMilli()
		if info.Size() > MaxFileBytes || total+info.Size() > MaxTotalBytes {
			f, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			probe := make([]byte, 4096)
			n, _ := f.Read(probe)
			f.Close()
			data, _ := note.Parse(string(probe[:n]))
			files = append(files, syncplan.File{Name: it.Name(), Mtime: mtime, Data: data, TooBig: true})
			continue
		}
		total += info.Size()
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		data, text := note.Parse(string(b))
		files = append(files, syncplan.File{Name: it.Name(), Mtime: mtime, Data: data, Text: text})
	}
	return files, nil
}

// List reads every note. A plain markdown file dropped into the folder is
// adopted - given an id and its block - the way the web app adopts one, and
// a copy that duplicates another file's id gets an id of its own.
func (l *Library) List() ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.list()
}

func (l *Library) list() ([]Entry, error) {
	files, err := ReadFolder(l.Dir)
	if err != nil {
		return nil, err
	}
	l.Skipped = nil
	seen := map[string]bool{}
	var out []Entry
	for _, f := range files {
		if f.TooBig {
			l.Skipped = append(l.Skipped, f.Name)
			continue
		}
		id := f.Data.Str("id")
		n := note.Note{
			ID:      id,
			Title:   note.TitleOf(f.Text),
			Text:    f.Text,
			Style:   f.Data.Str("style"),
			Created: f.Data.Num("created"),
			Updated: note.FileTimestamp(f.Data, f.Mtime),
			Words:   note.CountWords(f.Text),
		}
		if n.Created == 0 {
			n.Created = f.Mtime
		}
		if id == "" || seen[id] {
			n.ID = NewID()
			if err := WriteFileAtomic(filepath.Join(l.Dir, f.Name), []byte(note.Render(n))); err != nil {
				return nil, err
			}
		}
		seen[n.ID] = true
		out = append(out, Entry{Note: n, File: f.Name, Pinned: l.state.Pinned[n.ID], Archived: l.state.Archived[n.ID]})
	}
	return out, nil
}

// Sorted orders entries the way My documents does: pinned first, then by
// the chosen key ("updated", "created" or "title").
func Sorted(entries []Entry, by string) []Entry {
	out := append([]Entry{}, entries...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		switch by {
		case "created":
			return a.Created > b.Created
		case "title":
			return strings.ToLower(a.Title) < strings.ToLower(b.Title)
		}
		return a.Updated > b.Updated
	})
	return out
}

// Get finds one note by id.
func (l *Library) Get(id string) (Entry, error) {
	entries, err := l.List()
	if err != nil {
		return Entry{}, err
	}
	for _, e := range entries {
		if e.ID == id {
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("no note with id %s", id)
}

// Create writes a new note and returns it.
func (l *Library) Create(text string) (Entry, error) {
	now := Now()
	return l.Put(note.Note{ID: NewID(), Text: text, Created: now, Updated: now})
}

// Save stores an edited note: the text and style given, updated now.
func (l *Library) Save(e Entry) (Entry, error) {
	e.Updated = Now()
	return l.Put(e.Note)
}

// Put writes a record exactly as given (timestamps included), renaming its
// file when the title moved. It is how imports keep the moment they won by.
func (l *Library) Put(n note.Note) (Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries, err := l.list()
	if err != nil {
		return Entry{}, err
	}
	// The web app reads CRLF as LF (parseNote); writing it that way keeps
	// both sides holding the same text.
	n.Text = strings.ReplaceAll(n.Text, "\r\n", "\n")
	n.Title = note.TitleOf(n.Text)
	n.Words = note.CountWords(n.Text)
	own := ""
	taken := map[string]bool{}
	for _, e := range entries {
		if e.ID == n.ID {
			own = e.File
			if n.Created == 0 {
				n.Created = e.Created
			}
		}
		taken[strings.ToLower(e.File)] = true
	}
	// Names other programs hold in the folder count as taken too.
	if items, err := os.ReadDir(l.Dir); err == nil {
		for _, it := range items {
			taken[strings.ToLower(it.Name())] = true
		}
	}
	name := note.FilenameFor(n.Title, taken, own)
	if err := WriteFileAtomic(filepath.Join(l.Dir, name), []byte(note.Render(n))); err != nil {
		return Entry{}, err
	}
	if own != "" && own != name {
		os.Remove(filepath.Join(l.Dir, own))
	}
	return Entry{Note: n, File: name, Pinned: l.state.Pinned[n.ID], Archived: l.state.Archived[n.ID]}, nil
}

// SetFlags pins, unpins, archives or unarchives.
func (l *Library) SetFlags(id string, pinned, archived bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	setFlag(l.state.Pinned, id, pinned)
	setFlag(l.state.Archived, id, archived)
	return l.saveState()
}

func setFlag(m map[string]bool, id string, on bool) {
	if on {
		m[id] = true
	} else {
		delete(m, id)
	}
}

// Trash moves a note into .textarea/trash.
func (l *Library) Trash(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries, err := l.list()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.ID != id {
			continue
		}
		if err := WriteFileAtomic(l.trashPath(id), []byte(note.Render(e.Note))); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(l.Dir, e.File)); err != nil {
			return err
		}
		l.state.Trash[id] = Now()
		delete(l.state.Pinned, id)
		return l.saveState()
	}
	return fmt.Errorf("no note with id %s", id)
}

// ListTrash is the trash, most recently deleted first.
func (l *Library) ListTrash() ([]Trashed, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Trashed
	for id, at := range l.state.Trash {
		b, err := os.ReadFile(l.trashPath(id))
		if err != nil {
			continue
		}
		data, text := note.Parse(string(b))
		out = append(out, Trashed{Note: note.Note{
			ID: id, Title: note.TitleOf(text), Text: text, Style: data.Str("style"),
			Created: data.Num("created"), Updated: data.Num("updated"), Words: note.CountWords(text),
		}, DeletedAt: at})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt > out[j].DeletedAt })
	return out, nil
}

// Restore puts a trashed note back with its id and timestamps.
func (l *Library) Restore(id string) (Entry, error) {
	l.mu.Lock()
	b, err := os.ReadFile(l.trashPath(id))
	l.mu.Unlock()
	if err != nil {
		return Entry{}, errors.New("that note is no longer in the trash")
	}
	data, text := note.Parse(string(b))
	e, err := l.Put(note.Note{ID: id, Text: text, Style: data.Str("style"), Created: data.Num("created"), Updated: Now()})
	if err != nil {
		return Entry{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	os.Remove(l.trashPath(id))
	delete(l.state.Trash, id)
	return e, l.saveState()
}

// DeleteForever removes one trashed note for good.
func (l *Library) DeleteForever(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	os.Remove(l.trashPath(id))
	delete(l.state.Trash, id)
	return l.saveState()
}

// PurgeTrash deletes what has waited longer than days.
func (l *Library) PurgeTrash(days int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := Now() - int64(days)*24*60*60*1000
	changed := false
	for id, at := range l.state.Trash {
		if at < cutoff {
			os.Remove(l.trashPath(id))
			delete(l.state.Trash, id)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return l.saveState()
}

// Manifest is one sync target's manifest.
func (l *Library) Manifest(target string) syncplan.Manifest {
	l.mu.Lock()
	defer l.mu.Unlock()
	m := l.state.Manifests[target]
	if m == nil {
		m = syncplan.Manifest{}
	}
	return m
}

// SetManifest stores a target's manifest.
func (l *Library) SetManifest(target string, m syncplan.Manifest) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state.Manifests[target] = m
	return l.saveState()
}

// Extra reads a target's own state into v.
func (l *Library) Extra(key string, v any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if raw, ok := l.state.Extra[key]; ok {
		json.Unmarshal(raw, v)
	}
}

// SetExtra stores a target's own state.
func (l *Library) SetExtra(key string, v any) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	l.state.Extra[key] = b
	return l.saveState()
}
