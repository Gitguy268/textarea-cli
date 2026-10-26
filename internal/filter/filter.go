// Package filter is My documents' search grammar from lib/filter.js:
// `#tag` (nested tags included), `on:2026-10-25`, `is:pinned`,
// `is:archived`, and any other words as a phrase over title and text. Also
// backlinks, from lib/backlinks.js.
package filter

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Gitguy268/textarea-cli/internal/markdown"
	"github.com/Gitguy268/textarea-cli/internal/store"
)

// Query is a parsed search.
type Query struct {
	Phrase   string
	Tags     []string
	Day      string
	Pinned   bool
	Archived bool
}

var (
	tagToken = regexp.MustCompile(`^#\pL`)
	dayToken = regexp.MustCompile(`^on:\d{4}-\d{2}-\d{2}$`)
)

// Parse reads a search box.
func Parse(q string) Query {
	var p Query
	var words []string
	for _, tok := range strings.Fields(strings.ToLower(q)) {
		switch {
		case tagToken.MatchString(tok):
			p.Tags = append(p.Tags, tok[1:])
		case dayToken.MatchString(tok):
			p.Day = tok[3:]
		case tok == "is:pinned":
			p.Pinned = true
		case tok == "is:archived":
			p.Archived = true
		default:
			words = append(words, tok)
		}
	}
	p.Phrase = strings.Join(words, " ")
	return p
}

// DayOf is a local calendar day, YYYY-MM-DD.
func DayOf(ms int64) string { return time.UnixMilli(ms).Format("2006-01-02") }

// Match is matchNote.
func Match(e store.Entry, q Query) bool {
	if e.Archived != q.Archived {
		return false
	}
	if q.Pinned && !e.Pinned {
		return false
	}
	if q.Day != "" && DayOf(e.Created) != q.Day && DayOf(e.Updated) != q.Day {
		return false
	}
	if len(q.Tags) > 0 {
		tags := markdown.TagsIn(e.Text)
		for _, want := range q.Tags {
			found := false
			for _, tag := range tags {
				if tag == want || strings.HasPrefix(tag, want+"/") {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	if q.Phrase == "" {
		return true
	}
	return strings.Contains(strings.ToLower(e.Title), q.Phrase) || strings.Contains(strings.ToLower(e.Text), q.Phrase)
}

// Apply filters entries by a search box.
func Apply(entries []store.Entry, q string) []store.Entry {
	p := Parse(q)
	var out []store.Entry
	for _, e := range entries {
		if Match(e, p) {
			out = append(out, e)
		}
	}
	return out
}

// TagCount is one tag and how many notes carry it.
type TagCount struct {
	Tag   string
	Count int
}

// TagCounts counts tags over the unarchived notes, most used first.
func TagCounts(entries []store.Entry) []TagCount {
	counts := map[string]int{}
	for _, e := range entries {
		if e.Archived {
			continue
		}
		seen := map[string]bool{}
		for _, t := range markdown.TagsIn(e.Text) {
			if !seen[t] {
				seen[t] = true
				counts[t]++
			}
		}
	}
	var out []TagCount
	for t, c := range counts {
		out = append(out, TagCount{t, c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Tag < out[j].Tag
	})
	return out
}

// Backlink is a note that links to (or mentions) another.
type Backlink struct {
	Entry   store.Entry
	Linked  bool
	Snippet string
}

// Backlinks is findBacklinks.
func Backlinks(entries []store.Entry, title string, mentions bool) []Backlink {
	wanted := strings.ToLower(strings.TrimSpace(title))
	if wanted == "" {
		return nil
	}
	var out []Backlink
	for _, e := range entries {
		if strings.ToLower(strings.TrimSpace(e.Title)) == wanted {
			continue
		}
		linked := false
		for _, t := range markdown.WikiTitles(e.Text) {
			if strings.ToLower(t) == wanted {
				linked = true
				break
			}
		}
		if linked {
			out = append(out, Backlink{e, true, snippet(e.Text, wanted, true)})
		} else if mentions && strings.Contains(strings.ToLower(e.Text), wanted) {
			out = append(out, Backlink{e, false, snippet(e.Text, wanted, false)})
		}
	}
	return out
}

var spaces = regexp.MustCompile(`\s+`)

func snippet(text, wanted string, linked bool) string {
	for _, line := range strings.Split(text, "\n") {
		ok := false
		if linked {
			for _, t := range markdown.WikiTitles(line) {
				if strings.ToLower(t) == wanted {
					ok = true
				}
			}
		} else {
			ok = strings.Contains(strings.ToLower(line), wanted)
		}
		if ok {
			flat := strings.TrimSpace(spaces.ReplaceAllString(line, " "))
			if r := []rune(flat); len(r) > 120 {
				return string(r[:119]) + "…"
			}
			return flat
		}
	}
	return ""
}

// FindByTitle resolves a [[wikilink]] the way the web app does: the first
// note with that title, case-insensitively.
func FindByTitle(entries []store.Entry, title string) (store.Entry, bool) {
	wanted := strings.ToLower(strings.TrimSpace(title))
	for _, e := range entries {
		if strings.ToLower(strings.TrimSpace(e.Title)) == wanted {
			return e, true
		}
	}
	return store.Entry{}, false
}
