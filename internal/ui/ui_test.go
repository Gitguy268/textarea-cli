package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitguy268/textarea-cli/internal/config"
	"github.com/Gitguy268/textarea-cli/internal/store"
)

func testEnv(t *testing.T) *Env {
	t.Helper()
	lib, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Env{Lib: lib, Cfg: &config.Config{Library: lib.Dir, TrashDays: 30, Sort: "updated"}}
}

func press(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

var escKey = tea.KeyPressMsg{Code: tea.KeyEscape}

func rows(labels ...string) func(*Env, string) ([]Row, error) {
	return func(_ *Env, query string) ([]Row, error) {
		var out []Row
		for _, l := range labels {
			if query == "" || l == query {
				out = append(out, Row{Label: l})
			}
		}
		return out, nil
	}
}

func TestPickerAnyKeyOnEmptyList(t *testing.T) {
	env := testEnv(t)
	ran, rowRan := false, false
	p := NewPicker(env, "T", "empty", false, rows(), nil,
		PickerKey{Key: "a", Help: "show done", Any: true, Run: func(*Env, Row) tea.Cmd { ran = true; return nil }},
		PickerKey{Key: "D", Help: "trash", Run: func(*Env, Row) tea.Cmd { rowRan = true; return nil }},
	)
	p.Update(env, press('a'))
	if !ran || p.input.Value() != "" {
		t.Errorf("Any key on an empty list: ran=%v, filter %q", ran, p.input.Value())
	}
	p.Update(env, press('D'))
	if rowRan || p.input.Value() != "" {
		t.Errorf("a row key on an empty list must do nothing: ran=%v, filter %q", rowRan, p.input.Value())
	}
}

func TestPickerEscClearsThenLeaves(t *testing.T) {
	env := testEnv(t)
	p := NewPicker(env, "T", "empty", true, rows("one", "two"), nil)
	for _, r := range "two" {
		p.Update(env, press(r))
	}
	if len(p.rows) != 1 {
		t.Fatalf("filter %q left %d rows", p.input.Value(), len(p.rows))
	}
	if s, _ := p.Update(env, escKey); s == nil || p.input.Value() != "" || len(p.rows) != 2 {
		t.Fatalf("first Esc should clear the filter and stay")
	}
	if s, _ := p.Update(env, escKey); s != nil {
		t.Error("second Esc should leave the picker")
	}
}

func TestMenuShortcut(t *testing.T) {
	env := testEnv(t)
	ran := ""
	m := NewMenu(env, "M", "", func(*Env) []Item {
		return []Item{
			{Key: "n", Label: "New", Run: func(*Env) tea.Cmd { ran = "new"; return nil }},
			{Key: "q", Label: "Quit", Run: func(*Env) tea.Cmd { ran = "quit"; return nil }},
		}
	})
	m.Update(env, press('q'))
	if ran != "quit" || m.cursor != 1 {
		t.Errorf("q ran %q, cursor %d", ran, m.cursor)
	}
	m.Update(env, press('1'))
	if ran != "new" || m.cursor != 0 {
		t.Errorf("1 ran %q, cursor %d", ran, m.cursor)
	}
}

func TestCount(t *testing.T) {
	for n, want := range map[int]string{0: "0 notes", 1: "1 note", 2: "2 notes"} {
		if got := Count(n, "note"); got != want {
			t.Errorf("Count(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestFit(t *testing.T) {
	for in, want := range map[string]string{"ab": "ab   ", "abcdefgh": "abcd…", "日本語": "日本…"} {
		if got := Fit(in, 5); got != want {
			t.Errorf("Fit(%q, 5) = %q, want %q", in, got, want)
		}
	}
}
