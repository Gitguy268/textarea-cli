// Package markdown is textarea's lib/markdown.js renderer, as a tokenizer.
//
// The browser draws a note from this walk, and tags, tasks and the editor's
// colours here come from the same walk, so a line the web app shows as a
// checkbox is a line the terminal counts as a task. JavaScript's sticky,
// lookbehind regular expressions become hand-written matchers: RE2 has
// neither. Positions are byte offsets into the Go string.
package markdown

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Gitguy268/textarea-cli/internal/note"
)

// jsSpace is JavaScript's \s as an RE2 class body: RE2's own \s is ASCII only.
const jsSpace = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// Node is one piece of a rendered note. Kind is "text", "link", "span" or
// "item"; Class names a span ("md-h1", "md-tag", "md-task" ...); Wiki is a
// wikilink's title.
type Node struct {
	Kind     string
	Class    string
	Value    string
	Href     string
	Wiki     string
	Level    int
	Ordered  bool
	Children []Node
}

// Options mirror the renderer's switches.
type Options struct {
	FileLinks, Wikilinks, Tasks, Tags bool
}

// All switches on: how the terminal draws a note.
var All = Options{FileLinks: true, Wikilinks: true, Tasks: true, Tags: true}

var (
	fenceOpen = regexp.MustCompile("^[ \t]{0,3}(`{3,}|~{3,})")
	listItem  = regexp.MustCompile(`^([ \t]*)(?:([-*+])|(\d{1,9})[.)])[ \t]+`)
	taskRe    = regexp.MustCompile(`^\[( |x|X)\]`)
)

// readFence returns the end of a fenced block starting at index, or -1.
func readFence(text string, index int) int {
	m := fenceOpen.FindStringSubmatch(text[index:])
	if m == nil {
		return -1
	}
	marker := m[1]
	firstBreak := strings.IndexByte(text[index+len(m[0]):], '\n')
	if firstBreak == -1 {
		return -1
	}
	firstBreak += index + len(m[0])
	ch := regexp.QuoteMeta(marker[:1])
	closing := regexp.MustCompile(`(?m)^[ \t]{0,3}` + ch + `{` + itoa(len(marker)) + `,}[ \t]*$`)
	rest := text[firstBreak+1:]
	loc := closing.FindStringIndex(rest)
	if loc == nil {
		return -1
	}
	return firstBreak + 1 + loc[1]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func listLevel(indent string) int {
	n := len(strings.ReplaceAll(indent, "\t", "  "))/2 + 1
	if n > 6 {
		return 6
	}
	return n
}

// Render is renderMarkdown.
func Render(text string, opt Options) []Node {
	var nodes []Node
	i := 0
	for i < len(text) {
		if end := readFence(text, i); end != -1 {
			nodes = append(nodes, Node{Kind: "span", Class: "md-codeblock", Value: text[i:end]})
			i = end
			continue
		}
		brk := strings.IndexByte(text[i:], '\n')
		stop := len(text)
		if brk != -1 {
			brk += i
			stop = brk
		}
		line := text[i:stop]

		m := listItem.FindStringSubmatchIndex(line)
		rest := ""
		if m != nil {
			rest = line[m[1]:]
		}
		var task []string
		if m != nil && opt.Tasks {
			if t := taskRe.FindStringSubmatch(rest); t != nil {
				after := rest[len(t[0]):]
				if after == "" || after[0] == ' ' || after[0] == '\t' {
					task = t
				}
			}
		}
		dash := m != nil && m[4] != -1 && line[m[4]:m[5]] == "-"
		if m != nil && (!dash || task != nil) {
			item := Node{Kind: "item", Level: listLevel(line[m[2]:m[3]]), Ordered: m[6] != -1}
			item.Children = append(item.Children, Node{Kind: "span", Class: "md-marker", Value: line[:m[1]]})
			if task != nil {
				class := "md-task"
				if task[1] != " " {
					class = "md-task md-task-done"
				}
				item.Children = append(item.Children, Node{Kind: "span", Class: class, Value: task[0]})
				item.Children = append(item.Children, renderInline(rest[len(task[0]):], opt)...)
			} else {
				item.Children = append(item.Children, renderInline(rest, opt)...)
			}
			nodes = append(nodes, item)
		} else {
			nodes = append(nodes, renderInline(line, opt)...)
		}
		if brk == -1 {
			break
		}
		nodes = append(nodes, Node{Kind: "text", Value: "\n"})
		i = brk + 1
	}
	return nodes
}

// A matcher reports the length of its token at position i of text, or 0.
type matcher struct {
	name  string
	match func(text string, i int) int
}

func anchored(pattern string, lineStart bool) func(string, int) int {
	re := regexp.MustCompile(`^(?:` + pattern + `)`)
	return func(text string, i int) int {
		if lineStart && i != 0 {
			return 0
		}
		loc := re.FindStringIndex(text[i:])
		if loc == nil {
			return 0
		}
		return loc[1]
	}
}

// heading is ^#{n}[ \t]+[^\n]*$ on a single line: the rest of the line.
func heading(n int, lineStart bool) func(string, int) int {
	re := anchored(strings.Repeat("#", n)+`[ \t]+[^\n]*$`, lineStart)
	return re
}

// tag is (?<!\S)#\p{L}[\p{L}\p{N}_/-]*.
func tag(text string, i int) int {
	if text[i] != '#' {
		return 0
	}
	if i > 0 {
		prev, _ := utf8.DecodeLastRuneInString(text[:i])
		if !note.IsJSSpace(prev) {
			return 0
		}
	}
	j := i + 1
	r, size := utf8.DecodeRuneInString(text[j:])
	if size == 0 || !unicode.IsLetter(r) {
		return 0
	}
	j += size
	for j < len(text) {
		r, size = utf8.DecodeRuneInString(text[j:])
		if !(unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '/' || r == '-') {
			break
		}
		j += size
	}
	return j - i
}

var (
	baseMatchers = []matcher{
		{"md-h1", heading(1, true)},
		{"md-h2", heading(2, true)},
		{"md-h3", heading(3, true)},
		{"md-h4", heading(4, true)},
		// The original's h5 has no ^, so it matches mid-line. Kept as is: the
		// two apps must agree, not be right.
		{"md-h5", heading(5, false)},
		{"md-h6", heading(6, true)},
		{"md-code", anchored("`[^`\n]*`", false)},
		{"md-bold", anchored(`\*\*[^*\n]+?\*\*`, false)},
		{"md-bold", anchored(`__[^_\n]+?__`, false)},
		{"md-strike", anchored(`~~[^~\n]+?~~`, false)},
		{"md-italic", anchored(`\*[^*\n]+?\*`, false)},
		{"md-italic", anchored(`_[^_\n]+?_`, false)},
		{"md-url", anchored(`https?://[^`+jsSpace+`<>()\[\]{}"'`+"`"+`]+`, false)},
		{"md-email", anchored(`@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`, false)},
		{"md-google", anchored(`google:[^\n]+`, false)},
	}
	wikiMatcher = matcher{"md-wiki", anchored(`\[\[[^\[\n]{1,200}?\]\]`, false)}
	fileMatcher = matcher{"md-file", anchored(`\[[^\[\]\n]{1,150}\]\(file:\d{1,10}\)`, false)}
	tagMatcher  = matcher{"md-tag", tag}
	localPart   = regexp.MustCompile(`[A-Za-z0-9._%+-]`)
)

func renderInline(text string, opt Options) []Node {
	matchers := append([]matcher{}, baseMatchers...)
	specials := "`~*#_h@g"
	if opt.FileLinks || opt.Wikilinks {
		specials += "["
	}
	if opt.Wikilinks {
		matchers = append(matchers, wikiMatcher)
	}
	if opt.FileLinks {
		matchers = append(matchers, fileMatcher)
	}
	if opt.Tags {
		matchers = append(matchers, tagMatcher)
	}

	var nodes []Node
	i := 0
	for i < len(text) {
		matched := false
		for _, m := range matchers {
			n := m.match(text, i)
			if n == 0 {
				continue
			}
			raw := text[i : i+n]
			switch m.name {
			case "md-url":
				nodes = append(nodes, Node{Kind: "link", Href: raw, Value: raw})
			case "md-email":
				nodes = pushEmail(text, i, raw, nodes)
			case "md-google":
				nodes = append(nodes, Node{Kind: "link", Href: "https://www.google.com/search?q=" + encodeURIComponent(raw[len("google:"):]), Value: raw})
			case "md-wiki":
				title := note.JSTrim(raw[2 : len(raw)-2])
				if title != "" {
					nodes = append(nodes, Node{Kind: "link", Wiki: title, Value: raw})
				} else {
					nodes = append(nodes, Node{Kind: "text", Value: raw})
				}
			case "md-file":
				nodes = append(nodes, Node{Kind: "link", Class: "md-file", Value: raw})
			default:
				nodes = append(nodes, Node{Kind: "span", Class: m.name, Value: raw})
			}
			i += n
			matched = true
			break
		}
		if matched {
			continue
		}
		next := strings.IndexAny(text[i:], specials)
		if next == 0 {
			_, size := utf8.DecodeRuneInString(text[i:])
			nodes = append(nodes, Node{Kind: "text", Value: text[i : i+size]})
			i += size
			continue
		}
		if next == -1 {
			next = len(text) - i
		}
		nodes = append(nodes, Node{Kind: "text", Value: text[i : i+next]})
		i += next
	}
	return nodes
}

func pushEmail(text string, at int, raw string, nodes []Node) []Node {
	start := at
	for start > 0 && localPart.MatchString(text[start-1:start]) {
		start--
	}
	local := text[start:at]
	if local == "" || strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") {
		return append(nodes, Node{Kind: "text", Value: raw})
	}
	remaining := len(local)
	var trimmed []Node
	for remaining > 0 && len(nodes) > 0 {
		prev := &nodes[len(nodes)-1]
		if prev.Kind != "text" {
			break
		}
		if len(prev.Value) <= remaining {
			remaining -= len(prev.Value)
			trimmed = append(trimmed, *prev)
			nodes = nodes[:len(nodes)-1]
		} else {
			prev.Value = prev.Value[:len(prev.Value)-remaining]
			remaining = 0
		}
	}
	if remaining > 0 {
		for k := len(trimmed) - 1; k >= 0; k-- {
			nodes = append(nodes, trimmed[k])
		}
		return append(nodes, Node{Kind: "text", Value: raw})
	}
	return append(nodes, Node{Kind: "link", Href: "mailto:" + local + raw, Value: local + raw})
}

// encodeURIComponent, for the google: links.
func encodeURIComponent(s string) string {
	const keep = "-_.!~*'()"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x80 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(keep, c) >= 0) {
			b.WriteByte(c)
			continue
		}
		b.WriteString("%" + strings.ToUpper(hex(c)))
	}
	return b.String()
}

func hex(c byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[c>>4], digits[c&15]})
}

var tagHint = regexp.MustCompile(`(?:^|[` + jsSpace + `])#\pL`)

// TagsIn is tagsIn: every #tag outside code, lowercased, repeats included.
func TagsIn(text string) []string {
	if !tagHint.MatchString(text) {
		return nil
	}
	var found []string
	var walk func([]Node)
	walk = func(nodes []Node) {
		for _, n := range nodes {
			if n.Class == "md-tag" {
				found = append(found, strings.ToLower(n.Value[1:]))
			} else if n.Children != nil {
				walk(n.Children)
			}
		}
	}
	walk(Render(text, Options{Tags: true}))
	return found
}

var wikiRe = regexp.MustCompile(`\[\[([^\[\n]{1,200}?)\]\]`)

// WikiTitles is wikiTitles: every [[Title]], in order.
func WikiTitles(text string) []string {
	var titles []string
	for _, m := range wikiRe.FindAllStringSubmatch(text, -1) {
		if t := note.JSTrim(m[1]); t != "" {
			titles = append(titles, t)
		}
	}
	return titles
}

// Task is one checkbox line. Line is its 0-based line number in the note,
// which is what lets the terminal tick it.
type Task struct {
	Done bool
	Text string
	Line int
}

// TasksIn is tasksIn, plus the line each task sits on.
func TasksIn(text string) []Task {
	var found []Task
	line := 0
	for _, n := range Render(text, Options{Tasks: true}) {
		if n.Kind == "text" || n.Kind == "span" {
			line += strings.Count(n.Value, "\n")
		}
		if n.Kind != "item" {
			continue
		}
		at := -1
		for k, c := range n.Children {
			if c.Kind == "span" && (c.Class == "md-task" || c.Class == "md-task md-task-done") {
				at = k
				break
			}
		}
		if at == -1 {
			continue
		}
		var b strings.Builder
		for _, c := range n.Children[at+1:] {
			b.WriteString(c.Value)
		}
		found = append(found, Task{
			Done: n.Children[at].Value[1] != ' ',
			Text: note.JSTrim(b.String()),
			Line: line,
		})
	}
	return found
}

// Toggle ticks or unticks the task on the given line, returning the new text.
func Toggle(text string, line int) string {
	lines := strings.Split(text, "\n")
	if line < 0 || line >= len(lines) {
		return text
	}
	l := lines[line]
	m := listItem.FindStringIndex(l)
	if m == nil || len(l) < m[1]+3 || l[m[1]] != '[' || l[m[1]+2] != ']' {
		return text
	}
	mark := "x"
	if l[m[1]+1] != ' ' {
		mark = " "
	}
	lines[line] = l[:m[1]+1] + mark + l[m[1]+2:]
	return strings.Join(lines, "\n")
}
