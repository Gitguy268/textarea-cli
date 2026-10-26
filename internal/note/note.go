package note

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// Note is one note as the library holds it. Created and Updated are epoch
// milliseconds, the unit the browser app stores.
type Note struct {
	ID      string
	Title   string
	Text    string
	Style   string // "" when the note has none
	Created int64
	Updated int64
	Words   int
}

var titleRe = regexp.MustCompile(`^(?:[ \t]*\n)*[ \t]*#[ \t]+([^\n]+)`)

// TitleOf names a note by its first level one heading, or "Textarea".
func TitleOf(text string) string {
	m := titleRe.FindStringSubmatch(text)
	if m == nil {
		return "Textarea"
	}
	return JSTrim(m[1])
}

// Data is a parsed front matter block. Values are string, int64 or bool.
type Data map[string]any

// Str reads a key as a string, "" when absent.
func (d Data) Str(key string) string {
	switch v := d[key].(type) {
	case string:
		return v
	case int64:
		return strconv.FormatInt(v, 10)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

// Num is Number(value) || 0 for the integer fields.
func (d Data) Num(key string) int64 {
	switch v := d[key].(type) {
	case int64:
		return v
	case string:
		n, err := strconv.ParseFloat(JSTrim(v), 64)
		if err != nil {
			return 0
		}
		return int64(n)
	case bool:
		if v {
			return 1
		}
	}
	return 0
}

const bom = "\xef\xbb\xbf"

var keyRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]*):[ \t]?(.*)$`)
var intRe = regexp.MustCompile(`^-?\d+$`)

// Parse reads the front matter block and the text out of a file. Data is nil
// when the file does not open with a block.
func Parse(source string) (Data, string) {
	normalized := strings.ReplaceAll(strings.TrimPrefix(source, bom), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if lines[0] != "---" {
		return nil, normalized
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return nil, normalized
	}
	data := Data{}
	for _, line := range lines[1:end] {
		m := keyRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		data[m[1]] = scalar(m[2])
	}
	return data, strings.Join(lines[end+1:], "\n")
}

func scalar(raw string) any {
	value := JSTrim(raw)
	if strings.HasPrefix(value, `"`) {
		var s string
		if err := json.Unmarshal([]byte(value), &s); err != nil {
			return value
		}
		return s
	}
	if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2 {
		return value[1 : len(value)-1]
	}
	if intRe.MatchString(value) {
		// Number() of a long digit run is a float in JavaScript; the fields
		// that use this are millisecond timestamps, well inside int64.
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			return n
		}
		f, _ := strconv.ParseFloat(value, 64)
		return int64(f)
	}
	if value == "true" || value == "false" {
		return value == "true"
	}
	return value
}

var plainRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func plain(s string) string {
	if plainRe.MatchString(s) {
		return s
	}
	return JSONString(s)
}

// Render writes a note as a file: the block, then the text verbatim.
func Render(n Note) string {
	lines := []string{
		"---",
		"id: " + plain(n.ID),
		"created: " + strconv.FormatInt(n.Created, 10),
		"updated: " + strconv.FormatInt(n.Updated, 10),
		"words: " + strconv.Itoa(n.Words),
	}
	if n.Style != "" {
		lines = append(lines, "style: "+JSONString(n.Style))
	}
	lines = append(lines, "---")
	return strings.Join(lines, "\n") + "\n" + n.Text
}

// FileTimestamp is the later of the `updated` a file claims and its mtime.
func FileTimestamp(data Data, mtime int64) int64 {
	claimed := data.Num("updated")
	if mtime > claimed {
		return mtime
	}
	return claimed
}

var mdExt = regexp.MustCompile(`(?i)\.md$`)

// FilenameFor is a unique Title.md among names already taken (lowercased).
// own is the note's current filename, which keeps its own slot.
func FilenameFor(title string, taken map[string]bool, own string) string {
	base := SanitizeFilename(mdExt.ReplaceAllString(title, ""), "note")
	exempt := strings.ToLower(own)
	isTaken := func(name string) bool {
		l := strings.ToLower(name)
		return l != exempt && taken[l]
	}
	name := base + ".md"
	for count := 2; isTaken(name); count++ {
		name = base + " (" + strconv.Itoa(count) + ").md"
	}
	return name
}

var (
	windowsReserved = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[0-9¹²³]|lpt[0-9¹²³])(\..*)?$`)
	illegal         = regexp.MustCompile(`[\x00-\x1f"*:<>?|\\/]`)
	trailing        = regexp.MustCompile(`[. ]+$`)
	leadingDots     = regexp.MustCompile(`^\.+`)
	newlines        = regexp.MustCompile(`\n+`)
)

// SanitizeFilename is lib/filename.js: a name safe on every filesystem,
// at most 180 UTF-8 bytes.
func SanitizeFilename(name, fallback string) string {
	result := newlines.ReplaceAllString(name, " ")
	result = illegal.ReplaceAllString(result, "_")
	result = trailing.ReplaceAllString(result, "")
	result = leadingDots.ReplaceAllString(result, "")
	result = JSTrim(result)
	if result == "" || result == "." || result == ".." {
		result = fallback
	}
	if windowsReserved.MatchString(result) {
		result = "_" + result
	}
	if len(result) <= 180 {
		return result
	}
	u := units(result)
	cut := 180
	if cut > len(u) {
		cut = len(u)
	}
	for cut > 0 && utf8LenOfUnits(u[:cut]) > 180 {
		cut--
	}
	out := JSTrim(fromUnits(u[:cut]))
	if out == "" {
		return fallback
	}
	return out
}
