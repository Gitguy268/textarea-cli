package note

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Reading rates from lib/stats.js, in words per minute.
const (
	SpokenWPM = 130
	SilentWPM = 220
)

// Stats is countStats.
type Stats struct {
	Words, Characters, CharactersTight, Lines, Paragraphs, Sentences int
	SpeakingMinutes, ReadingMinutes                                  float64
}

func isCJKWord(r rune) bool {
	return (r >= 0x1100 && r <= 0x11ff) || (r >= 0x3040 && r <= 0x30ff) || (r >= 0x3130 && r <= 0x318f) ||
		(r >= 0x3400 && r <= 0x4dbf) || (r >= 0x4e00 && r <= 0x9fff) || (r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) || (r >= 0xff66 && r <= 0xff9f)
}

func isSeparator(r rune) bool {
	return IsJSSpace(r) || (r >= 0x3000 && r <= 0x303f) || (r >= 0xfe10 && r <= 0xfe1f) ||
		(r >= 0xff01 && r <= 0xff20) || (r >= 0xff3b && r <= 0xff40) || (r >= 0xff5b && r <= 0xff65)
}

// CountWords counts runs between separators, and every CJK character alone.
func CountWords(text string) int {
	words := 0
	inside := false
	for _, r := range text {
		switch {
		case isCJKWord(r):
			words++
			inside = false
		case isSeparator(r):
			inside = false
		case !inside:
			words++
			inside = true
		}
	}
	return words
}

var (
	paragraphSplit = regexp.MustCompile(`\n[ \t]*\n+`)
)

// CountStats is lib/stats.js countStats.
func CountStats(text string) Stats {
	s := Stats{Words: CountWords(text), Characters: CodePoints(text)}
	spaces := 0
	for _, r := range text {
		if IsJSSpace(r) {
			spaces++
		}
	}
	s.CharactersTight = s.Characters - spaces
	if text != "" {
		s.Lines = strings.Count(text, "\n") + 1
	}
	if t := JSTrim(text); t != "" {
		s.Paragraphs = len(paragraphSplit.Split(t, -1))
		s.Sentences = countSentences(t)
	}
	s.SpeakingMinutes = float64(s.Words) / SpokenWPM
	s.ReadingMinutes = float64(s.Words) / SilentWPM
	return s
}

// FormatDuration is lib/stats.js formatDuration.
func FormatDuration(minutes float64) string {
	if math.IsNaN(minutes) || math.IsInf(minutes, 0) || minutes <= 0 {
		return "0 sec"
	}
	seconds := int(math.Floor(minutes*60 + 0.5))
	if seconds < 60 {
		return fmt.Sprintf("%d sec", seconds)
	}
	whole := int(math.Floor(minutes + 0.5))
	if whole < 60 {
		return fmt.Sprintf("%d min", whole)
	}
	hours, rest := whole/60, whole%60
	if rest == 0 {
		return fmt.Sprintf("%d h", hours)
	}
	return fmt.Sprintf("%d h %02d min", hours, rest)
}

// countSentences is text.split(/(?<=[.!?…])[ \t]+|[ \t]*\n+/) with the empty
// parts dropped, scanned by hand because RE2 has no lookbehind.
func countSentences(t string) int {
	r := []rune(t)
	count, start := 0, 0
	part := func(end int) {
		if end > start {
			count++
		}
	}
	blank := func(c rune) bool { return c == ' ' || c == '\t' }
	for i := 0; i < len(r); {
		end := -1
		if i > 0 && blank(r[i]) && strings.ContainsRune(".!?…", r[i-1]) {
			end = i
			for end < len(r) && blank(r[end]) {
				end++
			}
		} else {
			j := i
			for j < len(r) && blank(r[j]) {
				j++
			}
			if j < len(r) && r[j] == '\n' {
				for j < len(r) && r[j] == '\n' {
					j++
				}
				end = j
			}
		}
		if end == -1 {
			i++
			continue
		}
		part(i)
		start, i = end, end
	}
	part(len(r))
	return count
}
