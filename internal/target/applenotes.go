package target

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"

	"github.com/Gitguy268/textarea-cli/internal/config"
	"github.com/Gitguy268/textarea-cli/internal/note"
	"github.com/Gitguy268/textarea-cli/internal/store"
)

// AppleNote is one note in the Notes folder.
type AppleNote struct {
	ID       string `json:"id"`
	Body     string `json:"body"`
	Created  int64  `json:"created"`
	Modified int64  `json:"modified"`
	Locked   bool   `json:"locked"`
}

// NotesOp is one change for Notes: "create", "update" or "delete".
type NotesOp struct {
	Op   string `json:"op"`
	ID   string `json:"id,omitempty"`
	HTML string `json:"html,omitempty"`
}

// NotesResult answers one op: the note's id and its modification date
// after the change.
type NotesResult struct {
	ID       string `json:"id"`
	Modified int64  `json:"modified"`
	Error    string `json:"error,omitempty"`
}

// NotesAPI is Apple Notes, or a fake of it in the tests.
type NotesAPI interface {
	List(folder string) ([]AppleNote, error)
	Apply(folder string, ops []NotesOp) ([]NotesResult, error)
}

// noteSlot ties a library note to its Notes twin.
//
// Modified is the Notes modification date last seen, and is the whole churn
// guard: markdown to HTML and back is not exact for text restyled inside
// Notes, so the body is never compared - a Notes note changed only when its
// date moved. Wrote is a hash of the HTML last written, so a library save
// that changed nothing Notes can show is not written again. Created says
// this app made the Notes note; only those are ever deleted. Gone marks a
// pair whose library note was deleted while its Notes twin is not ours to
// remove, so it is not imported back on the next pass.
type noteSlot struct {
	NotesID  string `json:"notesId"`
	Updated  int64  `json:"updated"`
	Modified int64  `json:"modified"`
	Wrote    string `json:"wrote"`
	Created  bool   `json:"created"`
	Gone     bool   `json:"gone,omitempty"`
}

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

// ErrNoNotes is returned off macOS.
var ErrNoNotes = errors.New("Apple Notes is only available on macOS")

// Notes is the backend SyncAppleNotes uses; tests replace it.
var Notes NotesAPI = systemNotes{}

// SyncAppleNotes mirrors the library and one Apple Notes folder.
func SyncAppleNotes(lib *store.Library, t config.Target, dryRun bool) (Report, error) {
	return syncNotes(lib, Notes, t, dryRun)
}

func syncNotes(lib *store.Library, api NotesAPI, t config.Target, dryRun bool) (Report, error) {
	rep := Report{Target: t.Name, DryRun: dryRun}
	folder := t.Folder
	if folder == "" {
		folder = "Textarea"
	}
	key := "applenotes:" + t.Name
	slots := map[string]noteSlot{}
	lib.Extra(key, &slots)

	entries, err := lib.List()
	if err != nil {
		return rep, err
	}
	remote, err := api.List(folder)
	if err != nil {
		return rep, err
	}
	byNotesID := map[string]AppleNote{}
	for _, n := range remote {
		byNotesID[n.ID] = n
	}
	claimed := map[string]bool{}
	for _, s := range slots {
		claimed[s.NotesID] = true
	}

	type pending struct {
		libID string
		op    NotesOp
		html  string
	}
	var ops []pending
	var imports []note.Note
	var trash []string
	alive := map[string]bool{}

	for _, e := range entries {
		alive[e.ID] = true
		html := MarkdownToNotes(e.Text)
		s, tracked := slots[e.ID]
		if !tracked || s.Gone {
			ops = append(ops, pending{e.ID, NotesOp{Op: "create", HTML: html}, html})
			rep.Written = append(rep.Written, e.Title)
			continue
		}
		twin, ok := byNotesID[s.NotesID]
		if !ok {
			// Deleted in Notes: the library note follows, the way a file
			// deleted in the Finder takes its note with it.
			trash = append(trash, e.ID)
			rep.Trashed = append(rep.Trashed, e.Title)
			continue
		}
		if twin.Locked {
			continue
		}
		notesChanged := twin.Modified != s.Modified
		appChanged := e.Updated > s.Updated
		if notesChanged && (twin.Modified > e.Updated || !appChanged) {
			text := NotesToMarkdown(twin.Body)
			if text != e.Text {
				n := e.Note
				n.Text = text
				n.Updated = twin.Modified
				imports = append(imports, n)
				rep.Imported = append(rep.Imported, note.TitleOf(text))
			}
			s.Modified = twin.Modified
			s.Updated = max(e.Updated, twin.Modified)
			slots[e.ID] = s
			continue
		}
		if appChanged && hashOf(html) != s.Wrote {
			ops = append(ops, pending{e.ID, NotesOp{Op: "update", ID: s.NotesID, HTML: html}, html})
			rep.Written = append(rep.Written, e.Title)
			continue
		}
		if appChanged {
			s.Updated = e.Updated
			slots[e.ID] = s
		}
	}

	// Pairs whose library note is gone.
	for id, s := range slots {
		if alive[id] || s.Gone {
			continue
		}
		twin, ok := byNotesID[s.NotesID]
		switch {
		case !ok:
			delete(slots, id)
		case s.Created && !twin.Locked:
			ops = append(ops, pending{id, NotesOp{Op: "delete", ID: s.NotesID}, ""})
			rep.Removed = append(rep.Removed, note.TitleOf(NotesToMarkdown(twin.Body)))
		default:
			s.Gone = true
			slots[id] = s
		}
	}

	// Notes in the folder nobody claims: adopted, never deleted.
	var adopt []AppleNote
	for _, n := range remote {
		if !claimed[n.ID] && !n.Locked {
			adopt = append(adopt, n)
		}
	}
	sort.Slice(adopt, func(i, j int) bool { return adopt[i].Created < adopt[j].Created })
	for _, n := range adopt {
		rep.Imported = append(rep.Imported, note.TitleOf(NotesToMarkdown(n.Body)))
	}

	if dryRun {
		return rep, nil
	}

	for _, n := range imports {
		if _, err := lib.Put(n); err != nil {
			return rep, err
		}
	}
	for _, n := range adopt {
		text := NotesToMarkdown(n.Body)
		e, err := lib.Put(note.Note{ID: store.NewID(), Text: text, Created: n.Created, Updated: n.Modified})
		if err != nil {
			return rep, err
		}
		slots[e.ID] = noteSlot{NotesID: n.ID, Updated: e.Updated, Modified: n.Modified}
	}
	for _, id := range trash {
		if err := lib.Trash(id); err != nil {
			return rep, err
		}
		delete(slots, id)
	}

	if len(ops) > 0 {
		list := make([]NotesOp, len(ops))
		for i, p := range ops {
			list[i] = p.op
		}
		results, err := api.Apply(folder, list)
		if err != nil {
			return rep, err
		}
		for i, p := range ops {
			if i >= len(results) || results[i].Error != "" {
				continue
			}
			r := results[i]
			switch p.op.Op {
			case "create":
				slots[p.libID] = noteSlot{NotesID: r.ID, Modified: r.Modified, Wrote: hashOf(p.html), Created: true}
			case "update":
				s := slots[p.libID]
				s.Modified, s.Wrote = r.Modified, hashOf(p.html)
				slots[p.libID] = s
			case "delete":
				delete(slots, p.libID)
			}
		}
	}

	// The library's `updated` after this pass, so our own writes and imports
	// do not read as app-side edits next time.
	fresh, err := lib.List()
	if err == nil {
		for _, e := range fresh {
			if s, ok := slots[e.ID]; ok && !s.Gone {
				s.Updated = e.Updated
				slots[e.ID] = s
			}
		}
	}
	return rep, lib.SetExtra(key, slots)
}
