package target

import (
	"html"
	"strings"

	xhtml "golang.org/x/net/html"
)

// MarkdownToNotes turns a note into the HTML Apple Notes stores: one <div>
// per line, headings as Notes' title and heading styles, everything else as
// the literal text. Keeping list markers and **stars** as text is what lets
// the trip back be exact for anything that was not restyled inside Notes.
func MarkdownToNotes(text string) string {
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			b.WriteString("<div><br></div>")
			continue
		}
		tag := ""
		body := line
		for level, prefix := range []string{"# ", "## ", "### "} {
			if strings.HasPrefix(line, prefix) && strings.TrimSpace(line[len(prefix):]) != "" {
				tag = []string{"h1", "h2", "h3"}[level]
				body = line[len(prefix):]
			}
		}
		inner := escapeSpaces(body)
		if tag != "" {
			inner = "<" + tag + ">" + inner + "</" + tag + ">"
		}
		b.WriteString("<div>" + inner + "</div>")
	}
	return b.String()
}

// escapeSpaces escapes the text and keeps runs of spaces and tabs, which
// HTML would otherwise collapse.
func escapeSpaces(s string) string {
	var b strings.Builder
	prevSpace := true // a leading space is kept too
	for _, r := range s {
		switch {
		case r == ' ' && prevSpace:
			b.WriteString("&nbsp;")
		case r == ' ':
			b.WriteByte(' ')
			prevSpace = true
			continue
		case r == '\t':
			b.WriteString("&#9;")
		default:
			b.WriteString(html.EscapeString(string(r)))
		}
		prevSpace = r == ' '
	}
	out := b.String()
	// A trailing plain space would be dropped by Notes.
	if strings.HasSuffix(out, " ") {
		out = out[:len(out)-1] + "&nbsp;"
	}
	return out
}

// NotesToMarkdown reads Notes' HTML back into markdown. Notes' own
// formatting becomes markdown where markdown has a word for it: headings,
// bold, italic, strikethrough, links, bulleted, numbered and check lists.
func NotesToMarkdown(source string) string {
	doc, err := xhtml.Parse(strings.NewReader(source))
	if err != nil {
		return source
	}
	w := &mdWriter{}
	w.walk(doc)
	w.endLine()
	return strings.Join(w.lines, "\n")
}

type mdWriter struct {
	lines []string
	cur   strings.Builder
	open  bool // a line has content or was started by a block
	lists []string
}

func (w *mdWriter) endLine() {
	if w.open {
		w.lines = append(w.lines, w.cur.String())
		w.cur.Reset()
		w.open = false
	}
}

func (w *mdWriter) write(s string) {
	w.cur.WriteString(s)
	w.open = true
}

func attr(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func (w *mdWriter) children(n *xhtml.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.walk(c)
	}
}

func (w *mdWriter) walk(n *xhtml.Node) {
	switch n.Type {
	case xhtml.TextNode:
		// Newlines in Notes' source are formatting, not text.
		t := strings.ReplaceAll(n.Data, "\n", "")
		t = strings.ReplaceAll(t, "\u00a0", " ")
		if t != "" {
			w.write(t)
		}
		return
	case xhtml.ElementNode:
	default:
		w.children(n)
		return
	}
	switch n.Data {
	case "br":
		if !w.open {
			// <div><br></div>: an empty line.
			w.open = true
		} else {
			w.endLine()
			// A <br> closing its block (Notes adds one after an edited
			// title) ends the line without starting an empty one.
			w.open = n.NextSibling != nil
		}
		return
	case "div", "p":
		w.endLine()
		w.children(n)
		w.endLine()
		return
	case "h1", "h2", "h3", "h4", "h5", "h6":
		w.endLine()
		w.write(strings.Repeat("#", int(n.Data[1]-'0')) + " ")
		w.children(n)
		w.endLine()
		return
	case "ul", "ol":
		w.endLine()
		kind := n.Data
		if strings.Contains(attr(n, "class"), "checklist") {
			kind = "check"
		}
		w.lists = append(w.lists, kind)
		w.children(n)
		w.lists = w.lists[:len(w.lists)-1]
		return
	case "li":
		w.endLine()
		indent := strings.Repeat("  ", max(0, len(w.lists)-1))
		marker := "- "
		if len(w.lists) > 0 {
			switch w.lists[len(w.lists)-1] {
			case "ol":
				marker = "1. "
			case "check":
				marker = "- [ ] "
				if strings.Contains(attr(n, "class"), "checked") {
					marker = "- [x] "
				}
			}
		}
		w.write(indent + marker)
		w.children(n)
		w.endLine()
		return
	case "b", "strong":
		if w.styled(n) {
			return
		}
		if level := notesHeading(n); level > 0 && !w.open {
			w.write(strings.Repeat("#", level) + " ")
			w.children(n)
			return
		}
		w.wrap(n, "**")
		return
	case "i", "em":
		w.wrap(n, "*")
		return
	case "strike", "s", "del":
		w.wrap(n, "~~")
		return
	case "tt", "code":
		w.wrap(n, "`")
		return
	case "a":
		href := attr(n, "href")
		var inner mdWriter
		inner.children(n)
		inner.endLine()
		text := strings.Join(inner.lines, " ")
		if href == "" || href == text {
			w.write(text)
		} else {
			w.write("[" + text + "](" + href + ")")
		}
		return
	case "img", "object", "style", "script", "head", "title":
		return
	}
	w.children(n)
}

// notesHeading is the markdown level of a Notes heading, 0 for plain bold.
// Notes stores the <h1>-<h3> it is given as bold in its own heading font:
// 24px for the title, 18px for a heading, no size for a subheading. After a
// scripted edit it may keep only the size, on a bare <span>.
func notesHeading(n *xhtml.Node) int {
	level := 0
	var visit func(c *xhtml.Node)
	visit = func(c *xhtml.Node) {
		for ; c != nil && level != 1 && level != 2; c = c.NextSibling {
			if c.Type != xhtml.ElementNode {
				continue
			}
			switch style := attr(c, "style"); {
			case strings.Contains(style, "font-size: 24px"):
				level = 1
			case strings.Contains(style, "font-size: 18px"):
				level = 2
			default:
				if c.Data == "font" && attr(c, "face") == ".AppleSystemUIFontBold" {
					level = 3
				}
				visit(c.FirstChild)
			}
		}
	}
	visit(n.FirstChild)
	return level
}

// styled reports a <b> that is only the heading's own weight, which Notes
// sometimes wraps inside a heading.
func (w *mdWriter) styled(n *xhtml.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == xhtml.ElementNode && len(p.Data) == 2 && p.Data[0] == 'h' {
			w.children(n)
			return true
		}
	}
	return false
}

func (w *mdWriter) wrap(n *xhtml.Node, mark string) {
	var inner mdWriter
	inner.children(n)
	inner.endLine()
	text := strings.Join(inner.lines, "\n")
	if strings.TrimSpace(text) == "" {
		w.write(text)
		return
	}
	w.write(mark + text + mark)
}
