package editor

import (
	"strings"
	"testing"
)

func TestArtifactKind(t *testing.T) {
	cases := map[string]string{
		"notes.md":       "md",
		"NOTES.MD":       "md",
		"page.html":      "html",
		"page.htm":       "html",
		"a/b/index.html": "html",
		"weird.txt":      "html", // non-md falls back to html (served as-is)
	}
	for in, want := range cases {
		if got := artifactKind(in); got != want {
			t.Errorf("artifactKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMarkdownToHTML(t *testing.T) {
	md := "# Title\n\nHello **bold** and *em* and `code`.\n\n" +
		"- one\n- two\n\n> a quote\n\n```\nx := 1\n```\n\n[link](https://e.com)\n"
	out := markdownToHTML("My Note", md)
	for _, want := range []string{
		"<title>My Note</title>",
		"<h1>Title</h1>",
		"<strong>bold</strong>",
		"<em>em</em>",
		"<code>code</code>",
		"<ul>",
		"<li>one</li>",
		"<blockquote>a quote</blockquote>",
		"<pre><code>",
		"x := 1",
		`<a href="https://e.com">link</a>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdownToHTML missing %q in:\n%s", want, out)
		}
	}
}

func TestMarkdownEscapesHTML(t *testing.T) {
	// raw markdown text must be escaped so embedded angle brackets can't inject
	// markup into the rendered page.
	out := renderMarkdownBody("a <script>alert(1)</script> b")
	if strings.Contains(out, "<script>") {
		t.Errorf("renderMarkdownBody did not escape html: %s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("renderMarkdownBody escaped form missing: %s", out)
	}
}

func TestHeadingLevel(t *testing.T) {
	cases := map[string]int{
		"# h":       1,
		"### h":     3,
		"###### h":  6,
		"####### h": 0, // 7 is too deep
		"#no space": 0,
		"plain":     0,
	}
	for in, want := range cases {
		if got := headingLevel(in); got != want {
			t.Errorf("headingLevel(%q) = %d, want %d", in, got, want)
		}
	}
}
