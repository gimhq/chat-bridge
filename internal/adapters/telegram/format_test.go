package telegram

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestEntitiesToMarkdown(t *testing.T) {
	text := "hello bold and code here, see site and Ali"
	ents := []tg.MessageEntityClass{
		&tg.MessageEntityBold{Offset: 6, Length: 4},
		&tg.MessageEntityCode{Offset: 15, Length: 4},
		&tg.MessageEntityTextURL{Offset: 30, Length: 4, URL: "https://x.io"},
		&tg.MessageEntityMentionName{Offset: 39, Length: 3, UserID: 42},
	}
	got, formatted := entitiesToMarkdown(text, ents)
	want := "hello **bold** and `code` here, see [site](https://x.io) and [Ali](tg://user?id=42)"
	if !formatted || got != want {
		t.Fatalf("got %q (formatted=%v)\nwant %q", got, formatted, want)
	}
	// Offsets are UTF-16 code units: an emoji before the entity counts as two.
	got, _ = entitiesToMarkdown("😀 bold", []tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 3, Length: 4}})
	if got != "😀 **bold**" {
		t.Fatalf("utf-16 offsets: %q", got)
	}
	// Pre blocks keep their language; nested/overlapping entities render inner first.
	got, _ = entitiesToMarkdown("x\nfmt.Println()\ny", []tg.MessageEntityClass{&tg.MessageEntityPre{Offset: 2, Length: 13, Language: "go"}})
	if got != "x\n```go\nfmt.Println()\n```\ny" {
		t.Fatalf("pre: %q", got)
	}
	got, _ = entitiesToMarkdown("bold italic", []tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 0, Length: 11}, &tg.MessageEntityItalic{Offset: 5, Length: 6}})
	if got != "**bold _italic_**" {
		t.Fatalf("nested: %q", got)
	}
	// Plain text stays plain and is not flagged as formatted.
	if got, formatted := entitiesToMarkdown("plain", nil); got != "plain" || formatted {
		t.Fatalf("plain: %q %v", got, formatted)
	}
	// Entities that carry no markup (bare URLs, @mentions) do not flag the text either.
	if _, formatted := entitiesToMarkdown("see https://a.b", []tg.MessageEntityClass{&tg.MessageEntityURL{Offset: 4, Length: 11}}); formatted {
		t.Fatal("bare url flagged as formatted")
	}
	// Markdown characters in plain text are escaped when other entities apply.
	got, _ = entitiesToMarkdown("a*b bold", []tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 4, Length: 4}})
	if got != "a\\*b **bold**" {
		t.Fatalf("escape: %q", got)
	}
}

func TestMarkdownToHTML(t *testing.T) {
	cases := map[string]string{
		"**b** _i_ ~~s~~ `c`":     "<b>b</b> <i>i</i> <s>s</s> <code>c</code>",
		"[t](https://x.io)":       `<a href="https://x.io">t</a>`,
		"```go\nfmt()\n```":       `<pre><code class="language-go">fmt()</code></pre>`,
		"a < b & c":               "a &lt; b &amp; c",
		"> quoted\nplain":         "<blockquote>quoted</blockquote>\nplain",
		"a\\*b":                   "a*b",
		"[u](tg://user?id=42) hi": `<a href="tg://user?id=42">u</a> hi`,
		"**unclosed":              "**unclosed",
	}
	for in, want := range cases {
		if got := markdownToHTML(in); got != want {
			t.Errorf("markdownToHTML(%q) = %q, want %q", in, got, want)
		}
	}
}
