package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLibraryLifecycle(t *testing.T) {
	clock := int64(1_000_000)
	orig := Now
	Now = func() int64 { return clock }
	t.Cleanup(func() { Now = orig })

	dir := t.TempDir()
	lib, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	a, err := lib.Create("# Groceries\n\nmilk\n")
	if err != nil {
		t.Fatal(err)
	}
	if a.File != "Groceries.md" || a.Words == 0 || a.Created != clock || a.Updated != clock {
		t.Fatalf("create: %+v", a)
	}
	if info, _ := os.Stat(filepath.Join(dir, a.File)); info.Mode().Perm() != 0o644 {
		t.Errorf("new file mode %v, want 0644", info.Mode().Perm())
	}

	// A new title renames the file and keeps id and created.
	clock += 1000
	a.Text = "# Shopping\r\n\r\nmilk\r\n"
	a, err = lib.Save(a)
	if err != nil {
		t.Fatal(err)
	}
	if a.File != "Shopping.md" || a.Created != 1_000_000 || a.Updated != clock || a.Text != "# Shopping\n\nmilk\n" {
		t.Fatalf("save: %+v", a)
	}
	if _, err := os.Stat(filepath.Join(dir, "Groceries.md")); !os.IsNotExist(err) {
		t.Error("old file still there after rename")
	}

	b, _ := lib.Create("untitled\n")
	if err := lib.SetFlags(b.ID, true, false); err != nil {
		t.Fatal(err)
	}

	// Trash, list, restore.
	clock += 1000
	if err := lib.Trash(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, b.File)); !os.IsNotExist(err) {
		t.Error("trashed file still in the library")
	}
	trash, _ := lib.ListTrash()
	if len(trash) != 1 || trash[0].ID != b.ID || trash[0].DeletedAt != clock || trash[0].Created != b.Created {
		t.Fatalf("trash: %+v", trash)
	}
	reopened, _ := Open(dir)
	if reopened.state.Trash[b.ID] != clock || reopened.state.Pinned[b.ID] {
		t.Errorf("state.json after trash: %+v", reopened.state)
	}
	clock += 1000
	r, err := lib.Restore(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != b.ID || r.Created != b.Created || r.Text != b.Text {
		t.Fatalf("restore: %+v, was %+v", r, b)
	}
	if trash, _ := lib.ListTrash(); len(trash) != 0 {
		t.Errorf("trash after restore: %+v", trash)
	}

	// Purge only what has waited past the limit.
	lib.Trash(a.ID)
	clock += 29 * 24 * 60 * 60 * 1000
	lib.PurgeTrash(30)
	if trash, _ := lib.ListTrash(); len(trash) != 1 {
		t.Fatalf("purged too early: %+v", trash)
	}
	clock += 2 * 24 * 60 * 60 * 1000
	lib.PurgeTrash(30)
	if trash, _ := lib.ListTrash(); len(trash) != 0 {
		t.Fatalf("not purged after 31 days: %+v", trash)
	}
	if _, err := os.Stat(lib.trashPath(a.ID)); !os.IsNotExist(err) {
		t.Error("purged file still on disk")
	}
	if entries, _ := lib.List(); len(entries) != 1 || entries[0].ID != b.ID {
		t.Errorf("library after all that: %+v", entries)
	}
}

func TestWriteKeepsMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows files have a read-only bit, not permission bits")
	}
	path := filepath.Join(t.TempDir(), "note.md")
	os.WriteFile(path, []byte("x"), 0o600)
	if err := WriteFileAtomic(path, []byte("y")); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600 kept", info.Mode().Perm())
	}
}
