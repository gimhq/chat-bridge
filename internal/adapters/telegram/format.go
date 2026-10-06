package telegram

import (
	"html"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/gotd/td/tg"
)

// mark is one side of an entity's markup at a UTF-16 position.
type mark struct {
	pos    int
	open   bool
	text   string
	start  int // entity start, for ordering nested closes
	length int
	code   bool // no escaping inside
}

// entitiesToMarkdown renders Telegram entities as the common markdown subset. The bool reports
// whether any markup was produced (plain text and markup-less entities leave it false).
func entitiesToMarkdown(text string, ents []tg.MessageEntityClass) (string, bool) {
	units := utf16.Encode([]rune(text))
	var marks []mark
	for _, e := range ents {
		off, length := e.GetOffset(), e.GetLength()
		if off < 0 || length <= 0 || off+length > len(units) {
			continue
		}
		var open, closer string
		code := false
		switch v := e.(type) {
		case *tg.MessageEntityBold:
			open, closer = "**", "**"
		case *tg.MessageEntityItalic:
			open, closer = "_", "_"
		case *tg.MessageEntityStrike:
			open, closer = "~~", "~~"
		case *tg.MessageEntityCode:
			open, closer, code = "`", "`", true
		case *tg.MessageEntityPre:
			open, closer, code = "```"+v.Language+"\n", "\n```", true
		case *tg.MessageEntityTextURL:
			open, closer = "[", "]("+v.URL+")"
		case *tg.MessageEntityMentionName:
			open, closer = "[", "](tg://user?id="+strconv.FormatInt(v.UserID, 10)+")"
		default:
			continue
		}
		marks = append(marks,
			mark{pos: off, open: true, text: open, start: off, length: length, code: code},
			mark{pos: off + length, open: false, text: closer, start: off, length: length, code: code})
	}
	if len(marks) == 0 {
		return text, false
	}
	sort.SliceStable(marks, func(i, j int) bool {
		a, b := marks[i], marks[j]
		if a.pos != b.pos {
			return a.pos < b.pos
		}
		if a.open != b.open {
			return !a.open // closes before opens at the same position
		}
		if a.open {
			return a.length > b.length // outer entity opens first
		}
		return a.start > b.start // inner entity closes first
	})
	var out strings.Builder
	last, codeDepth := 0, 0
	emit := func(from, to int) {
		if from >= to {
			return
		}
		seg := string(utf16.Decode(units[from:to]))
		if codeDepth == 0 {
			seg = escapeMarkdown(seg)
		}
		out.WriteString(seg)
	}
	for _, m := range marks {
		emit(last, m.pos)
		last = m.pos
		out.WriteString(m.text)
		if m.code {
			if m.open {
				codeDepth++
			} else {
				codeDepth--
			}
		}
	}
	emit(last, len(units))
	return out.String(), true
}

func escapeMarkdown(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune("\\*_~`[]", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// markdownToHTML renders the common markdown subset as Telegram HTML (bold, italic, strike,
// inline code, fenced code, links, blockquotes). Unknown or unclosed markup is kept literally.
func markdownToHTML(md string) string {
	lines := strings.Split(md, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "```") {
			lang := strings.TrimSpace(strings.TrimPrefix(line, "```"))
			var body []string
			j := i + 1
			for ; j < len(lines) && !strings.HasPrefix(lines[j], "```"); j++ {
				body = append(body, lines[j])
			}
			if j < len(lines) { // closed fence
				code := html.EscapeString(strings.Join(body, "\n"))
				if lang != "" {
					out = append(out, `<pre><code class="language-`+html.EscapeString(lang)+`">`+code+`</code></pre>`)
				} else {
					out = append(out, "<pre>"+code+"</pre>")
				}
				i = j
				continue
			}
		}
		if strings.HasPrefix(line, "> ") || line == ">" {
			var quoted []string
			for ; i < len(lines) && (strings.HasPrefix(lines[i], "> ") || lines[i] == ">"); i++ {
				quoted = append(quoted, inlineHTML(strings.TrimPrefix(strings.TrimPrefix(lines[i], ">"), " ")))
			}
			i--
			out = append(out, "<blockquote>"+strings.Join(quoted, "\n")+"</blockquote>")
			continue
		}
		out = append(out, inlineHTML(line))
	}
	return strings.Join(out, "\n")
}

// inlineHTML converts inline markup in one line.
func inlineHTML(s string) string {
	var out strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case r == '\\' && i+1 < len(rs):
			out.WriteString(html.EscapeString(string(rs[i+1])))
			i += 2
		case r == '`':
			if end := indexRune(rs, i+1, "`"); end > 0 {
				out.WriteString("<code>" + html.EscapeString(string(rs[i+1:end])) + "</code>")
				i = end + 1
				continue
			}
			out.WriteString("`")
			i++
		case strings.HasPrefix(string(rs[i:]), "**"):
			if end := indexRune(rs, i+2, "**"); end > 0 {
				out.WriteString("<b>" + inlineHTML(string(rs[i+2:end])) + "</b>")
				i = end + 2
				continue
			}
			out.WriteString("**")
			i += 2
		case strings.HasPrefix(string(rs[i:]), "~~"):
			if end := indexRune(rs, i+2, "~~"); end > 0 {
				out.WriteString("<s>" + inlineHTML(string(rs[i+2:end])) + "</s>")
				i = end + 2
				continue
			}
			out.WriteString("~~")
			i += 2
		case r == '_':
			if end := indexRune(rs, i+1, "_"); end > 0 && end > i+1 {
				out.WriteString("<i>" + inlineHTML(string(rs[i+1:end])) + "</i>")
				i = end + 1
				continue
			}
			out.WriteString("_")
			i++
		case r == '[':
			if closing := indexRune(rs, i+1, "]"); closing > 0 && closing+1 < len(rs) && rs[closing+1] == '(' {
				if end := indexRune(rs, closing+2, ")"); end > 0 {
					url := string(rs[closing+2 : end])
					out.WriteString(`<a href="` + html.EscapeString(url) + `">` + inlineHTML(string(rs[i+1:closing])) + "</a>")
					i = end + 1
					continue
				}
			}
			out.WriteString("[")
			i++
		default:
			out.WriteString(html.EscapeString(string(r)))
			i++
		}
	}
	return out.String()
}

// indexRune finds sep in rs at or after from, skipping backslash-escaped characters.
func indexRune(rs []rune, from int, sep string) int {
	seps := []rune(sep)
	for i := from; i+len(seps) <= len(rs); i++ {
		if rs[i] == '\\' {
			i++
			continue
		}
		if string(rs[i:i+len(seps)]) == sep {
			return i
		}
	}
	return -1
}
