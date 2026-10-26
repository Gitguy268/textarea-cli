// Package golden checks the Go port against textarea's own JavaScript, run
// under Node by testdata/oracle.mjs. Every case is asked of both sides and
// the answers compared as JSON.
package golden

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/Gitguy268/textarea-cli/internal/export"
	"github.com/Gitguy268/textarea-cli/internal/markdown"
	"github.com/Gitguy268/textarea-cli/internal/note"
	"github.com/Gitguy268/textarea-cli/internal/oracle"
	"github.com/Gitguy268/textarea-cli/internal/share"
	"github.com/Gitguy268/textarea-cli/internal/syncplan"
)

// same compares a Go value with the oracle's JSON after a round trip
// through JSON, so struct shapes and maps compare alike.
func same(t *testing.T, label string, got any, want json.RawMessage) {
	t.Helper()
	var g, w any
	b, _ := json.Marshal(got)
	json.Unmarshal(b, &g)
	json.Unmarshal(want, &w)
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s\n go: %s\n js: %s", label, b, want)
	}
}

func TestTextHelpers(t *testing.T) {
	var reqs []oracle.Request
	for _, text := range oracle.Fixtures {
		reqs = append(reqs,
			oracle.Request{"op": "parse", "source": text},
			oracle.Request{"op": "title", "text": text},
			oracle.Request{"op": "stats", "text": text},
			oracle.Request{"op": "tags", "text": text},
			oracle.Request{"op": "tasks", "text": text},
			oracle.Request{"op": "wiki", "text": text},
		)
	}
	res := oracle.Run(t, reqs)
	for i, text := range oracle.Fixtures {
		r := res[i*6 : i*6+6]
		data, body := note.Parse(text)
		var d any = nil
		if data != nil {
			d = map[string]any(data)
		}
		same(t, "parse "+fmt.Sprintf("%q", text), map[string]any{"data": d, "text": body}, r[0])
		same(t, "title "+fmt.Sprintf("%q", text), note.TitleOf(text), r[1])

		var js struct{ Words, Characters, CharactersTight, Lines, Paragraphs, Sentences int }
		json.Unmarshal(r[2], &js)
		s := note.CountStats(text)
		gs := struct{ Words, Characters, CharactersTight, Lines, Paragraphs, Sentences int }{s.Words, s.Characters, s.CharactersTight, s.Lines, s.Paragraphs, s.Sentences}
		if gs != js {
			t.Errorf("stats %q\n go: %+v\n js: %+v", text, gs, js)
		}

		tags := markdown.TagsIn(text)
		if tags == nil {
			tags = []string{}
		}
		same(t, "tags "+fmt.Sprintf("%q", text), tags, r[3])

		tasks := []map[string]any{}
		for _, task := range markdown.TasksIn(text) {
			tasks = append(tasks, map[string]any{"done": task.Done, "text": task.Text})
		}
		same(t, "tasks "+fmt.Sprintf("%q", text), tasks, r[4])

		wiki := markdown.WikiTitles(text)
		if wiki == nil {
			wiki = []string{}
		}
		same(t, "wiki "+fmt.Sprintf("%q", text), wiki, r[5])
	}
}

func TestRenderAndFilenames(t *testing.T) {
	notes := []note.Note{
		{ID: "n1", Created: 1000, Updated: 2000, Words: 2, Text: "# Note one\n\nbody"},
		{ID: "a b:c", Created: 1, Updated: 2, Words: 0, Style: `color: red; font: "x"`, Text: ""},
		{ID: "id-ok_1", Created: 0, Updated: 0, Words: 5, Style: "tab\there <b>&", Text: "---\nnot a block\n---"},
	}
	titles := []string{"Note", "Textarea", "a/b\\c:d*e?f\"g<h>i|j", "CON", "lpt1.txt", "...dots", "trailing. . ", "", "x.md", strings.Repeat("é", 120), strings.Repeat("😀", 50) + "z"}
	var reqs []oracle.Request
	for _, n := range notes {
		reqs = append(reqs, oracle.Request{"op": "render", "note": map[string]any{
			"id": n.ID, "created": n.Created, "updated": n.Updated, "words": n.Words, "style": n.Style, "text": n.Text,
		}})
	}
	taken := []string{"note.md", "note (2).md", "textarea.md", "textarea (2).md"}
	for _, title := range titles {
		reqs = append(reqs, oracle.Request{"op": "filename", "title": title, "taken": taken, "own": ""})
	}
	reqs = append(reqs, oracle.Request{"op": "filename", "title": "Note", "taken": taken, "own": "Note.md"})
	res := oracle.Run(t, reqs)
	for i, n := range notes {
		same(t, "render "+n.ID, note.Render(n), res[i])
	}
	takenSet := map[string]bool{"note.md": true, "note (2).md": true, "textarea.md": true, "textarea (2).md": true}
	for i, title := range titles {
		same(t, "filename "+fmt.Sprintf("%q", title), note.FilenameFor(title, takenSet, ""), res[len(notes)+i])
	}
	same(t, "filename own", note.FilenameFor("Note", takenSet, "Note.md"), res[len(res)-1])
}

func TestFormats(t *testing.T) {
	var reqs []oracle.Request
	var cases []export.Note
	for _, text := range oracle.Fixtures {
		n := export.Note{Text: text, Title: note.TitleOf(text), Hash: "#abc", ExportedAt: "2026-10-25T10:11:12.345Z"}
		cases = append(cases, n)
	}
	cases = append(cases, export.Note{Text: "x", Title: "true", Style: "color: red", ExportedAt: "2026-10-25T10:11:12.345Z"})
	for _, n := range cases {
		for _, f := range export.Formats {
			reqs = append(reqs, oracle.Request{"op": "convert", "format": f.ID, "note": map[string]any{
				"text": n.Text, "title": n.Title, "style": n.Style, "hash": n.Hash, "exportedAt": n.ExportedAt,
			}})
		}
	}
	res := oracle.Run(t, reqs)
	k := 0
	for _, n := range cases {
		for _, f := range export.Formats {
			got, _ := export.Convert(n, f.ID)
			same(t, f.ID+" "+fmt.Sprintf("%q", n.Text), got, res[k])
			k++
		}
	}
}

func TestLinks(t *testing.T) {
	contents := []string{"", "# Hi\n\nthere", "with style\x00color: red", strings.Repeat("long text 😀 ", 2000)}
	var reqs []oracle.Request
	for _, c := range contents {
		reqs = append(reqs, oracle.Request{"op": "decode", "hash": share.EncodeHash(c)})
		reqs = append(reqs, oracle.Request{"op": "encode", "content": c})
	}
	enc, err := share.EncryptHash("secret note\x00style", "pässwörd")
	if err != nil {
		t.Fatal(err)
	}
	reqs = append(reqs,
		oracle.Request{"op": "decrypt", "payload": enc, "password": "pässwörd"}, // NFD typed, NFC derived
		oracle.Request{"op": "encrypt", "content": "from js", "password": "pw"},
	)
	res := oracle.Live(t, reqs)
	for i, c := range contents {
		same(t, "js decodes go", c, res[i*2])
		var hash string
		json.Unmarshal(res[i*2+1], &hash)
		got, err := share.DecodeHash(hash)
		if err != nil || got != c {
			t.Errorf("go decodes js: %v", err)
		}
	}
	same(t, "js decrypts go", "secret note\x00style", res[len(res)-2])
	var payload string
	json.Unmarshal(res[len(res)-1], &payload)
	if got, err := share.DecryptHash(payload, "pw"); err != nil || got != "from js" {
		t.Errorf("go decrypts js: %q %v", got, err)
	}
	if _, err := share.DecryptHash(payload, "wrong"); err == nil {
		t.Error("a wrong password must fail")
	}
}

// ---------------------------------------------------------------- planSync

type scenario struct {
	entries  []note.Note
	files    []syncplan.File
	manifest syncplan.Manifest
}

func file(name, source string, mtime int64) syncplan.File {
	d, text := note.Parse(source)
	return syncplan.File{Name: name, Mtime: mtime, Data: d, Text: text}
}

func ptr(v int64) *int64 { return &v }

func entry(id, text string, created, updated int64) note.Note {
	return note.Note{ID: id, Title: note.TitleOf(text), Text: text, Created: created, Updated: updated, Words: note.CountWords(text)}
}

func jsFiles(files []syncplan.File) []map[string]any {
	out := []map[string]any{}
	for _, f := range files {
		var d any = nil
		if f.Data != nil {
			d = map[string]any(f.Data)
		}
		m := map[string]any{"name": f.Name, "mtime": f.Mtime, "data": d, "text": f.Text}
		if f.TooBig {
			m["text"] = nil
			m["tooBig"] = true
		}
		out = append(out, m)
	}
	return out
}

func jsEntries(entries []note.Note) []map[string]any {
	out := []map[string]any{}
	for _, e := range entries {
		out = append(out, map[string]any{"id": e.ID, "title": e.Title, "text": e.Text, "style": e.Style, "created": e.Created, "updated": e.Updated, "words": e.Words})
	}
	return out
}

// normalise turns either side's plan into one comparable shape: fresh ids
// become NEW, null style becomes "", a missing remove becomes "".
func normalise(raw []byte, known map[string]bool) string {
	var plan struct {
		Actions  []map[string]any          `json:"actions"`
		Manifest map[string]map[string]any `json:"manifest"`
	}
	json.Unmarshal(raw, &plan)
	fresh := func(id any) any {
		if s, ok := id.(string); ok && !known[s] {
			return "NEW"
		}
		return id
	}
	var b strings.Builder
	for _, a := range plan.Actions {
		if a["remove"] == nil {
			a["remove"] = ""
		}
		if a["id"] != nil {
			a["id"] = fresh(a["id"])
		}
		if r, ok := a["record"].(map[string]any); ok {
			r["id"] = fresh(r["id"])
			if r["style"] == nil {
				r["style"] = ""
			}
			a["id"] = r["id"]
		}
		if text, ok := a["text"].(string); ok {
			// A fresh id lands in the rendered block too.
			for _, line := range strings.Split(text, "\n") {
				if strings.HasPrefix(line, "id: ") && !known[line[4:]] {
					text = strings.Replace(text, line, "id: NEW", 1)
				}
			}
			a["text"] = text
		}
		j, _ := json.Marshal(a)
		b.Write(j)
		b.WriteByte('\n')
	}
	var keys []string
	for id, slot := range plan.Manifest {
		j, _ := json.Marshal(slot)
		keys = append(keys, fmt.Sprint(fresh(id))+"="+string(j))
	}
	sortStrings(keys)
	b.WriteString(strings.Join(keys, "\n"))
	return b.String()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func goPlan(sc scenario) []byte {
	n := 0
	actions, manifest := syncplan.Plan(sc.entries, sc.files, sc.manifest, func() string { n++; return fmt.Sprintf("fresh-%d", n) })
	var out struct {
		Actions  []map[string]any         `json:"actions"`
		Manifest map[string]syncplan.Slot `json:"manifest"`
	}
	out.Actions = []map[string]any{}
	for _, a := range actions {
		m := map[string]any{"type": a.Type}
		switch a.Type {
		case syncplan.Write:
			m["id"], m["name"], m["text"], m["remove"] = a.ID, a.Name, a.Text, a.Remove
		case syncplan.Import:
			r := a.Record
			m["record"] = map[string]any{"id": r.ID, "title": r.Title, "text": r.Text, "style": r.Style, "created": r.Created, "updated": r.Updated, "words": r.Words}
		case syncplan.RemoveNote:
			m["id"] = a.ID
		case syncplan.RemoveFile:
			m["name"] = a.Name
		}
		out.Actions = append(out.Actions, m)
	}
	out.Manifest = manifest
	b, _ := json.Marshal(out)
	return b
}

func scenarios() []scenario {
	e := entry("n1", "# Note one\n\nbody", 1000, 2000)
	man := func(name string, updated, fileUpdated int64) syncplan.Manifest {
		return syncplan.Manifest{"n1": {Name: name, Updated: updated, FileUpdated: ptr(fileUpdated)}}
	}
	rendered := note.Render(e)
	edited := e
	edited.Text, edited.Updated = "# Note one\n\nnew material", 4000
	renamed := entry("n1", "# Renamed\n\nbody", 1000, 4000)
	big := syncplan.File{Name: "Note one.md", Mtime: 3000, Data: note.Data{"id": "n1"}, TooBig: true}
	list := []scenario{
		{[]note.Note{e}, []syncplan.File{file("Plain.md", "# Plain\n\nfrom disk", 5000)}, syncplan.Manifest{}},
		{[]note.Note{e}, []syncplan.File{file("Note one.md", rendered, 3000)}, man("Note one.md", 2000, 3000)},
		{[]note.Note{edited}, []syncplan.File{file("Note one.md", rendered, 3000)}, man("Note one.md", 2000, 3000)},
		{[]note.Note{e}, []syncplan.File{file("Note one.md", note.Render(entry("n1", "# Note one\n\nedited elsewhere", 1000, 9000)), 9000)}, man("Note one.md", 2000, 3000)},
		{[]note.Note{e}, nil, man("Note one.md", 2000, 3000)},
		{nil, []syncplan.File{file("Note one.md", rendered, 3000)}, man("Note one.md", 2000, 3000)},
		{nil, []syncplan.File{file("Note one.md", "# Note one\n\nbody", 3000)}, man("Note one.md", 2000, 3000)},
		{nil, []syncplan.File{file("Foreign.md", "---\nid: foreign-1\ncreated: 5\nupdated: 6\n---\n# Foreign\n\ntext", 5)}, syncplan.Manifest{}},
		{[]note.Note{e}, []syncplan.File{file("Note one.md", rendered, 1500), file("Copy of Note one.md", rendered, 1500)}, syncplan.Manifest{}},
		{[]note.Note{renamed}, []syncplan.File{file("Note one.md", rendered, 3000)}, man("Note one.md", 2000, 3000)},
		{[]note.Note{e, entry("n2", "# Note one\n\nbody", 1000, 2500)}, nil, syncplan.Manifest{}},
		{[]note.Note{e}, []syncplan.File{file("Note one.md", "# Note one\n\nbody", 9000)}, man("Note one.md", 2000, 3000)},
		{[]note.Note{e}, []syncplan.File{file("Note one.md", "# Note one\n\nchanged outside", 9000)}, man("Note one.md", 2000, 3000)},
		{[]note.Note{e}, []syncplan.File{file("Whatever.md", rendered, 3000)}, man("Note one.md", 2000, 3000)},
		{[]note.Note{e}, []syncplan.File{big}, man("Note one.md", 2000, 3000)},
		{nil, []syncplan.File{big}, man("Note one.md", 2000, 3000)},
		{nil, []syncplan.File{{Name: "Foreign.md", Mtime: 3000, Data: note.Data{"id": "f"}, TooBig: true}}, syncplan.Manifest{}},
	}

	// And a few hundred random ones over a small alphabet, where the edges
	// (equal timestamps, renames, missing blocks) keep colliding.
	rng := rand.New(rand.NewSource(7))
	texts := []string{"# A\n\none", "# A\n\ntwo", "# B\n\none", "plain words", ""}
	times := []int64{1000, 2000, 3000, 4000}
	names := []string{"A.md", "B.md", "Textarea.md", "plain.md"}
	ids := []string{"n1", "n2", "n3"}
	for k := 0; k < 400; k++ {
		var sc scenario
		sc.manifest = syncplan.Manifest{}
		for _, id := range ids {
			if rng.Intn(2) == 0 {
				sc.entries = append(sc.entries, entry(id, texts[rng.Intn(len(texts))], 500, times[rng.Intn(len(times))]))
			}
			if rng.Intn(2) == 0 {
				sc.manifest[id] = syncplan.Slot{Name: names[rng.Intn(len(names))], Updated: times[rng.Intn(len(times))], FileUpdated: ptr(times[rng.Intn(len(times))])}
			}
		}
		used := map[string]bool{}
		for f := rng.Intn(4); f > 0; f-- {
			name := names[rng.Intn(len(names))]
			if used[name] {
				continue
			}
			used[name] = true
			text := texts[rng.Intn(len(texts))]
			source := text
			if rng.Intn(3) > 0 {
				source = note.Render(entry(ids[rng.Intn(len(ids))], text, 500, times[rng.Intn(len(times))]))
			}
			sc.files = append(sc.files, file(name, source, times[rng.Intn(len(times))]))
		}
		list = append(list, sc)
	}
	return list
}

func TestPlanSync(t *testing.T) {
	list := scenarios()
	var reqs []oracle.Request
	for _, sc := range list {
		m := sc.manifest
		if m == nil {
			m = syncplan.Manifest{}
		}
		reqs = append(reqs, oracle.Request{"op": "plan", "entries": jsEntries(sc.entries), "files": jsFiles(sc.files), "manifest": m})
	}
	known := map[string]bool{"n1": true, "n2": true, "n3": true, "foreign-1": true, "f": true}
	res := oracle.RunWith(t, reqs, func(a, b []byte) bool { return normalise(a, known) == normalise(b, known) })
	for i, sc := range list {
		g, j := normalise(goPlan(sc), known), normalise(res[i], known)
		if g != j {
			t.Errorf("scenario %d differs\n go:\n%s\n js:\n%s", i, g, j)
		}
	}
}
