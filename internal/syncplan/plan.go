// Package syncplan is planSync from textarea's lib/folder-sync.js: the whole
// newest-wins mirror between a library and a folder of markdown files,
// decided as a pure function over three snapshots so it can be tested with
// no disk at all. The rules, in order, for every note:
//
//   - File gone, manifest says there was one: the note was deleted outside,
//     and goes too. No manifest entry, no deletion, which is why a first pass
//     never removes anything.
//   - File newer than the note: import it, unless the text is identical.
//   - Note newer, or nothing changed: write the file only when the text,
//     style or filename differs.
//
// Files no note claims: a tracked name whose note is gone is removed, a file
// with a known or unknown id is imported, and a plain markdown file is
// adopted with a fresh id - never deleted.
package syncplan

import (
	"strings"

	"github.com/Gitguy268/textarea-cli/internal/note"
)

// File is one markdown file in the folder, parsed. TooBig files were
// identified from a probe and never read: Text is empty and no action may
// depend on it.
type File struct {
	Name   string
	Mtime  int64
	Data   note.Data
	Text   string
	TooBig bool
}

// Slot is a manifest entry. FileUpdated is nil for a file the executor is
// about to write; it fills in the real mtime afterwards.
type Slot struct {
	Name        string `json:"name"`
	Updated     int64  `json:"updated"`
	FileUpdated *int64 `json:"fileUpdated"`
}

// Manifest maps note id to the file it was mirrored to.
type Manifest map[string]Slot

// Action kinds.
const (
	Write      = "write"
	Import     = "import"
	RemoveNote = "remove-note"
	RemoveFile = "remove-file"
)

// Action is one step for the executor. Write carries Name, Text and, when the
// file is renamed, Remove (the old name). Import carries Record.
type Action struct {
	Type   string
	ID     string
	Name   string
	Text   string
	Remove string
	Record note.Note
}

func ptr(v int64) *int64 { return &v }

func sameNote(entry note.Note, f File) bool {
	return entry.Text == f.Text && entry.Style == f.Data.Str("style")
}

func importRecord(f File, entry *note.Note, newID func() string) note.Note {
	id := f.Data.Str("id")
	if id == "" && entry != nil {
		id = entry.ID
	}
	if id == "" {
		id = newID()
	}
	created := f.Data.Num("created")
	if created == 0 {
		created = f.Mtime
	}
	if entry != nil {
		created = entry.Created
	}
	return note.Note{
		ID:      id,
		Title:   note.TitleOf(f.Text),
		Text:    f.Text,
		Style:   f.Data.Str("style"),
		Created: created,
		Updated: note.FileTimestamp(f.Data, f.Mtime),
		Words:   note.CountWords(f.Text),
	}
}

// Plan decides one reconcile. newID makes ids for adopted files.
func Plan(entries []note.Note, files []File, manifest Manifest, newID func() string) ([]Action, Manifest) {
	var actions []Action
	next := Manifest{}
	ids := map[string]bool{}
	for _, e := range entries {
		ids[e.ID] = true
	}
	byName := map[string]*File{}
	byID := map[string]*File{}
	taken := map[string]bool{}
	for i := range files {
		f := &files[i]
		byName[f.Name] = f
		if id := f.Data.Str("id"); id != "" {
			if _, ok := byID[id]; !ok {
				byID[id] = f
			}
		}
		taken[lower(f.Name)] = true
	}
	matched := map[string]bool{}

	for _, entry := range entries {
		tracked, isTracked := manifest[entry.ID]
		file := byID[entry.ID]
		if file == nil && isTracked {
			if c := byName[tracked.Name]; c != nil && (c.Data.Str("id") == "" || c.Data.Str("id") == entry.ID) {
				file = c
			}
		}

		if file == nil {
			if isTracked {
				actions = append(actions, Action{Type: RemoveNote, ID: entry.ID})
			} else {
				name := note.FilenameFor(entry.Title, taken, "")
				taken[lower(name)] = true
				actions = append(actions, Action{Type: Write, ID: entry.ID, Name: name, Text: note.Render(entry)})
				next[entry.ID] = Slot{Name: name, Updated: entry.Updated}
			}
			continue
		}
		matched[file.Name] = true

		if file.TooBig {
			next[entry.ID] = Slot{Name: file.Name, Updated: entry.Updated, FileUpdated: ptr(file.Mtime)}
			continue
		}

		var trackedUpdated, trackedFile int64
		if isTracked {
			trackedUpdated = tracked.Updated
			if tracked.FileUpdated != nil {
				trackedFile = *tracked.FileUpdated
			}
		}
		appChanged := entry.Updated > trackedUpdated
		fileChanged := file.Mtime > trackedFile
		fileTs := note.FileTimestamp(file.Data, file.Mtime)
		hasID := file.Data.Str("id") != ""

		if fileChanged && fileTs > entry.Updated {
			source := entry
			if !sameNote(entry, *file) {
				e := entry
				record := importRecord(*file, &e, newID)
				actions = append(actions, Action{Type: Import, ID: record.ID, Record: record})
				source = record
			}
			if !hasID {
				actions = append(actions, Action{Type: Write, ID: entry.ID, Name: file.Name, Text: note.Render(source)})
				next[entry.ID] = Slot{Name: file.Name, Updated: source.Updated}
			} else {
				next[entry.ID] = Slot{Name: file.Name, Updated: source.Updated, FileUpdated: ptr(file.Mtime)}
			}
			continue
		}

		name := note.FilenameFor(entry.Title, taken, file.Name)
		remove := ""
		if name != file.Name {
			remove = file.Name
		}
		if !hasID {
			actions = append(actions, Action{Type: Write, ID: entry.ID, Name: name, Text: note.Render(entry), Remove: remove})
			next[entry.ID] = Slot{Name: name, Updated: entry.Updated}
			continue
		}
		if (appChanged || fileChanged) && (!sameNote(entry, *file) || name != file.Name) {
			delete(taken, lower(file.Name))
			taken[lower(name)] = true
			actions = append(actions, Action{Type: Write, ID: entry.ID, Name: name, Text: note.Render(entry), Remove: remove})
			next[entry.ID] = Slot{Name: name, Updated: entry.Updated}
			continue
		}
		taken[lower(file.Name)] = true
		next[entry.ID] = Slot{Name: file.Name, Updated: entry.Updated, FileUpdated: ptr(file.Mtime)}
	}

	for i := range files {
		f := files[i]
		if matched[f.Name] {
			continue
		}
		id := f.Data.Str("id")
		if id != "" && ids[id] {
			continue
		}
		_, idTracked := manifest[id]
		if (id != "" && idTracked) || nameTracked(manifest, f.Name) {
			actions = append(actions, Action{Type: RemoveFile, Name: f.Name})
			continue
		}
		if f.TooBig {
			continue
		}
		record := importRecord(f, nil, newID)
		actions = append(actions, Action{Type: Import, ID: record.ID, Record: record})
		if id == "" {
			actions = append(actions, Action{Type: Write, ID: record.ID, Name: f.Name, Text: note.Render(record)})
			next[record.ID] = Slot{Name: f.Name, Updated: record.Updated}
		} else {
			next[record.ID] = Slot{Name: f.Name, Updated: record.Updated, FileUpdated: ptr(f.Mtime)}
		}
	}
	return actions, next
}

func nameTracked(m Manifest, name string) bool {
	for _, s := range m {
		if s.Name == name {
			return true
		}
	}
	return false
}

func lower(s string) string { return strings.ToLower(s) }
