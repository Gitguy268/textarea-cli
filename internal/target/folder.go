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
	"time"

	"github.com/Gitguy268/textarea-cli/internal/config"
	"github.com/Gitguy268/textarea-cli/internal/note"
	"github.com/Gitguy268/textarea-cli/internal/store"
	"github.com/Gitguy268/textarea-cli/internal/syncplan"
)

// AllowRemovals lets one run through the brake. Set by `text sync --allow-removals`.
var AllowRemovals bool

// The brake: how much one pass may remove before it stops and asks. Same rule
// as removalBrake in the web app's lib/folder-sync.js.
const (
	brakeMinRemovals = 3
	brakeShare       = 0.25
	parkedKeep       = 30 * 24 * time.Hour
)

// removalBrake says whether a plan removes too much to believe without asking:
// a cloud client mid-resync or an unmounted drive looks exactly like the reader
// deleting everything. It returns the removals and whether to stop.
func removalBrake(actions []syncplan.Action, tracked int) (int, bool) {
	removals := 0
	for _, a := range actions {
		if a.Type == syncplan.RemoveFile || a.Type == syncplan.RemoveNote {
			removals++
		}
	}
	if removals == 0 {
		return 0, false
	}
	everything := tracked >= 2 && removals >= tracked
	aLot := removals >= brakeMinRemovals && float64(removals)/float64(tracked) >= brakeShare
	return removals, everything || aLot
}

// parkFile takes a file out of the mirror without destroying it: it moves to
// .textarea/sync-removed under a timestamp, where the next pass cannot see it.
func parkFile(dir, name string) error {
	parked := filepath.Join(dir, ".textarea", "sync-removed")
	if err := os.MkdirAll(parked, 0o755); err != nil {
		return err
	}
	now := time.Now()
	err := os.Rename(filepath.Join(dir, name), filepath.Join(parked, fmt.Sprintf("%d-%s", now.UnixMilli(), name)))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if old, readErr := os.ReadDir(parked); readErr == nil {
		for _, e := range old {
			if info, infoErr := e.Info(); infoErr == nil && now.Sub(info.ModTime()) > parkedKeep {
				os.Remove(filepath.Join(parked, e.Name()))
			}
		}
	}
	return nil
}

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
	manifest := lib.Manifest(manifestKey(name))
	actions, next := syncplan.Plan(notes, files, manifest, store.NewID)

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
	if removals, stop := removalBrake(actions, len(manifest)); stop && !AllowRemovals {
		return rep, fmt.Errorf("sync paused: %d of %d notes look deleted, nothing was removed. Check %s; if you did delete them, run text sync --allow-removals", removals, len(manifest), dir)
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
			err = parkFile(dir, a.Name)
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
