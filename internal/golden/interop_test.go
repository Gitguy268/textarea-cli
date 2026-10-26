package golden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gitguy268/textarea-cli/internal/oracle"
	"github.com/Gitguy268/textarea-cli/internal/store"
)

type webPass struct {
	Result struct {
		Written, Imported, Removed int
	} `json:"result"`
	Entries map[string]struct {
		ID, Title, Text string
		Updated         int64
	} `json:"entries"`
}

// TestWebAppInterop points the web app's own Folder sync at a textarea-cli
// library: notes and edits cross both ways, and once both sides have had
// their say a pass changes nothing and no file is rewritten.
func TestWebAppInterop(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "web.json")
	web := func(args ...string) webPass {
		t.Helper()
		var p webPass
		out := oracle.Script(t, "interop.mjs", append([]string{statePath, dir}, args...)...)
		if err := json.Unmarshal(out, &p); err != nil {
			t.Fatalf("interop output: %v\n%s", err, out)
		}
		return p
	}
	// mtimes are compared in milliseconds; let each side's write land in a
	// later one than the last.
	tick := func() { time.Sleep(15 * time.Millisecond) }

	lib, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	texts := []string{
		"# From the terminal\n\nhello\n",
		"buy milk #home\n",
		"- [ ] call mum\n",
		"# Café ☕\n\nnaïve — “quotes”\r\n",
		"# CON\n\nreserved on Windows\n",
	}
	ids := map[string]string{}
	for _, text := range texts {
		e, err := lib.Create(text)
		if err != nil {
			t.Fatal(err)
		}
		ids[text] = e.ID
	}
	browserID := "0b9e3c1a-1111-4222-8333-444455556666"
	browserText := "# From the browser\n\n- [ ] task 📅 2026-10-30 ⏫\nstatus:: draft\n#work/taxes\n"
	now := time.Now().UnixMilli()
	b, _ := json.Marshal(map[string]any{"id": browserID, "title": "From the browser", "text": browserText, "created": now, "updated": now, "words": 9})
	web("put", string(b))

	p := web("pass")
	if p.Result.Imported != len(texts) || p.Result.Written != 1 || p.Result.Removed != 0 {
		t.Fatalf("first pass %+v, want %d imported, 1 written", p.Result, len(texts))
	}
	for text, id := range ids {
		text = strings.ReplaceAll(text, "\r\n", "\n")
		if got := p.Entries[id].Text; got != text {
			t.Errorf("web app holds %q, terminal wrote %q", got, text)
		}
	}
	got, err := lib.Get(browserID)
	if err != nil || got.Text != browserText {
		t.Fatalf("terminal sees %q (%v), web app wrote %q", got.Text, err, browserText)
	}

	// An edit on each side.
	tick()
	term, _ := lib.Get(ids[texts[0]])
	term.Text = "# From the terminal\n\nhello, edited in the terminal\n"
	if _, err := lib.Save(term); err != nil {
		t.Fatal(err)
	}
	tick()
	edited := browserText + "\nedited in the browser\n"
	b, _ = json.Marshal(map[string]any{"id": browserID, "title": "From the browser", "text": edited, "updated": time.Now().UnixMilli(), "words": 13})
	web("put", string(b))
	tick()

	p = web("pass")
	if p.Result.Imported != 1 || p.Result.Written != 1 {
		t.Fatalf("second pass %+v, want 1 imported, 1 written", p.Result)
	}
	if p.Entries[term.ID].Text != term.Text {
		t.Errorf("terminal edit did not reach the web app: %q", p.Entries[term.ID].Text)
	}
	if got, _ := lib.Get(browserID); got.Text != edited {
		t.Errorf("browser edit did not reach the terminal: %q", got.Text)
	}

	// Settled: nothing to do, and reading through the terminal rewrites nothing.
	p = web("pass")
	if p.Result != (webPass{}).Result {
		t.Fatalf("third pass %+v, want no changes", p.Result)
	}
	before := snapshot(t, dir)
	if _, err := lib.List(); err != nil {
		t.Fatal(err)
	}
	p = web("pass")
	if p.Result != (webPass{}).Result {
		t.Fatalf("pass after the terminal read %+v, want no changes", p.Result)
	}
	if after := snapshot(t, dir); !sameFiles(before, after) {
		t.Errorf("files changed on a settled pass:\n%v\n%v", before, after)
	}

	// A note the terminal trashes leaves the web app too.
	if err := lib.Trash(ids[texts[1]]); err != nil {
		t.Fatal(err)
	}
	p = web("pass")
	if p.Result.Removed != 1 {
		t.Fatalf("pass after trash %+v, want 1 removed", p.Result)
	}
	if _, ok := p.Entries[ids[texts[1]]]; ok {
		t.Error("trashed note is still in the web app")
	}
}

type fileState struct {
	data  string
	mtime time.Time
}

func snapshot(t *testing.T, dir string) map[string]fileState {
	t.Helper()
	out := map[string]fileState{}
	items, _ := os.ReadDir(dir)
	for _, it := range items {
		if it.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, it.Name()))
		if err != nil {
			t.Fatal(err)
		}
		info, _ := it.Info()
		out[it.Name()] = fileState{string(b), info.ModTime()}
	}
	return out
}

func sameFiles(a, b map[string]fileState) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w.data != v.data || !w.mtime.Equal(v.mtime) {
			return false
		}
	}
	return true
}
