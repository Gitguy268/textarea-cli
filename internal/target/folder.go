// Package target mirrors the library into the places a note can also live:
// any folder (iCloud Drive, a Dropbox or Google Drive sync folder, a USB
// stick), an Obsidian vault, and on a Mac, Apple Notes.
package target

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Gitguy268/textarea-cli/internal/config"
	"github.com/Gitguy268/textarea-cli/internal/note"
	"github.com/Gitguy268/textarea-cli/internal/store"
	"github.com/Gitguy268/textarea-cli/internal/syncplan"
)

// Report is what one pass did, or would do on a dry run.
type Report struct {
	Target   string
	Written  []string
	Imported []string
	Removed  []string // files removed from the target
	Trashed  []string // notes moved to the library's trash
	DryRun   bool
}

// Empty reports whether the pass changed nothing.
func (r Report) Empty() bool {
	return len(r.Written)+len(r.Imported)+len(r.Removed)+len(r.Trashed) == 0
}

func (r Report) String() string {
	verb := ""
	if r.DryRun {
		verb = " (dry run: nothing was changed)"
	}
	if r.Empty() {
		return fmt.Sprintf("%s: already in step%s", r.Target, verb)
	}
	return fmt.Sprintf("%s: %d written, %d imported, %d files removed, %d notes to the trash%s",
		r.Target, len(r.Written), len(r.Imported), len(r.Removed), len(r.Trashed), verb)
}

// Sync runs one pass against a configured target.
func Sync(lib *store.Library, t config.Target, dryRun bool) (Report, error) {
	switch t.Kind {
	case "folder", "obsidian":
		return SyncFolder(lib, t.Name, t.Path, dryRun)
	case "applenotes":
		return SyncAppleNotes(lib, t, dryRun)
	}
	return Report{}, fmt.Errorf("unknown target kind %q", t.Kind)
}

func manifestKey(name string) string { return "target:" + name }

// SyncFolder mirrors the library and a folder of markdown files, newest
// wins, with the web app's decision table.
func SyncFolder(lib *store.Library, name, dir string, dryRun bool) (Report, error) {
	rep := Report{Target: name, DryRun: dryRun}
	abs, _ := filepath.Abs(dir)
	libAbs, _ := filepath.Abs(lib.Dir)
	if abs == libAbs {
		return rep, fmt.Errorf("%s is the library itself; pick another folder", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return rep, err
	}
	entries, err := lib.List()
	if err != nil {
		return rep, err
	}
	files, err := store.ReadFolder(dir)
	if err != nil {
		return rep, err
	}
	notes := make([]note.Note, len(entries))
	for i, e := range entries {
		notes[i] = e.Note
	}
	actions, next := syncplan.Plan(notes, files, lib.Manifest(manifestKey(name)), store.NewID)

	for _, a := range actions {
		switch a.Type {
		case syncplan.Write:
			rep.Written = append(rep.Written, a.Name)
		case syncplan.Import:
			rep.Imported = append(rep.Imported, a.Record.Title)
		case syncplan.RemoveFile:
			rep.Removed = append(rep.Removed, a.Name)
		case syncplan.RemoveNote:
			rep.Trashed = append(rep.Trashed, a.ID)
		}
	}
	if dryRun {
		return rep, nil
	}

	for _, a := range actions {
		var err error
		switch a.Type {
		case syncplan.Write:
			err = store.WriteFileAtomic(filepath.Join(dir, a.Name), []byte(a.Text))
			if err == nil && a.Remove != "" {
				os.Remove(filepath.Join(dir, a.Remove))
			}
		case syncplan.Import:
			_, err = lib.Put(a.Record)
		case syncplan.RemoveFile:
			err = os.Remove(filepath.Join(dir, a.Name))
			if os.IsNotExist(err) {
				err = nil
			}
		case syncplan.RemoveNote:
			err = lib.Trash(a.ID)
		}
		if err != nil {
			// The manifest is not committed, so the next pass sees the same
			// world and decides again.
			return rep, err
		}
	}
	// Our own writes must not look like outside edits next time.
	for id, slot := range next {
		if slot.FileUpdated == nil {
			if info, err := os.Stat(filepath.Join(dir, slot.Name)); err == nil {
				m := info.ModTime().UnixMilli()
				slot.FileUpdated = &m
				next[id] = slot
			}
		}
	}
	// Imports rewrote library files, which moved their mtimes and so the
	// `updated` the next List reads. Record those, or the next pass would see
	// every import as an app-side edit (harmless, but noisy).
	if len(rep.Imported) > 0 {
		if fresh, err := lib.List(); err == nil {
			for _, e := range fresh {
				if slot, ok := next[e.ID]; ok && e.Updated > slot.Updated {
					slot.Updated = e.Updated
					next[e.ID] = slot
				}
			}
		}
	}
	return rep, lib.SetManifest(manifestKey(name), next)
}

// Vault is one Obsidian vault Obsidian knows about.
type Vault struct {
	Path string
	Open bool
}

// ObsidianVaults reads obsidian.json from Obsidian's config folder.
func ObsidianVaults() []Vault {
	var dir string
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "Library", "Application Support", "obsidian")
	case "windows":
		dir = filepath.Join(os.Getenv("APPDATA"), "obsidian")
	default:
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			home, _ := os.UserHomeDir()
			cfg = filepath.Join(home, ".config")
		}
		dir = filepath.Join(cfg, "obsidian")
	}
	b, err := os.ReadFile(filepath.Join(dir, "obsidian.json"))
	if err != nil {
		return nil
	}
	var doc struct {
		Vaults map[string]struct {
			Path string `json:"path"`
			Open bool   `json:"open"`
		} `json:"vaults"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return nil
	}
	var out []Vault
	for _, v := range doc.Vaults {
		out = append(out, Vault{Path: v.Path, Open: v.Open})
	}
	return out
}
