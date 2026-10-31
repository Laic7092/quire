package epubwrite

import (
	"strings"
	"testing"
)

func TestSanitizeFragment(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		notWant []string
	}{
		{
			name: "unclosed paragraphs",
			in:   "<p>one<p>two",
			want: []string{"<p>one</p><p>two</p>"},
		},
		{
			name: "entities and non-breaking space",
			in:   `<p>a&nbsp;b &amp; c &lt; d&nbsp;</p>`,
			want: []string{"a&#160;b &amp; c &lt; d&#160;", "&amp;"},
			// The named entity must be resolved, never re-emitted raw.
			notWant: []string{"&nbsp;"},
		},
		{
			name: "line breaks",
			in:   "a<br>b<br/>c",
			want: []string{"a<br/>b<br/>c"},
			// XHTML has no bare <br>.
			notWant: []string{"<br>", "<br >"},
		},
		{
			name:    "scripts and styles disappear with their text",
			in:      `<p>keep</p><script>alert("x")</script><style>p{color:red}</style>`,
			want:    []string{"<p>keep</p>"},
			notWant: []string{"alert", "color:red", "<script", "<style"},
		},
		{
			name:    "remote images become alt text",
			in:      `<p>x<img src="https://example.test/a.png" alt="a picture">y</p>`,
			want:    []string{"<p>xa picturey</p>"},
			notWant: []string{"<img", "example.test"},
		},
		{
			name:    "image without alt disappears",
			in:      `<p>a<img src="https://example.test/a.png">b</p>`,
			want:    []string{"<p>ab</p>"},
			notWant: []string{"<img"},
		},
		{
			name:    "javascript links lose the href",
			in:      `<p><a href="javascript:alert(1)">click</a> <a href="JaVaScRiPt:alert(1)">two</a> <a href="java&#09;script:alert(1)">three</a></p>`,
			want:    []string{"click", "two", "three"},
			notWant: []string{"javascript", "alert(1)", "<a "},
		},
		{
			name: "safe links survive",
			in:   `<p><a href="https://example.test/?a=1&amp;b=2" onclick="steal()">e</a></p>`,
			want: []string{`<p><a href="https://example.test/?a=1&amp;b=2">e</a></p>`},
			// All other attributes, including event handlers, are dropped.
			notWant: []string{"onclick"},
		},
		{
			name: "special characters are escaped",
			in:   `<p>&lt;&gt;&amp;&quot;&#39; text</p>`,
			want: []string{"&lt;&gt;&amp;&quot;&#39; text"},
		},
		{
			name:    "form controls disappear",
			in:      `<p>before</p><form><input value="x"><select><option>o</option></select><textarea>t</textarea><button>b</button></form><p>after</p>`,
			want:    []string{"<p>before</p>", "<p>after</p>"},
			notWant: []string{"<form", "<input", "<select", "<textarea", "<button", "option"},
		},
		{
			name:    "iframes, objects and embeds disappear",
			in:      `<p>a</p><iframe src="https://example.test"></iframe><object data="x"><p>fallback</p></object><embed src="y"><p>b</p>`,
			want:    []string{"<p>a</p>", "<p>b</p>"},
			notWant: []string{"<iframe", "<object", "<embed", "fallback"},
		},
		{
			name: "unknown wrappers are unwrapped",
			in:   `<table><tr><td><p>x</p></td></tr></table><section><p>y</p></section>`,
			want: []string{"<p>x</p>", "<p>y</p>"},
		},
		{
			name:    "attributes are stripped",
			in:      `<p class="x" style="color:red" data-y="z">a</p><span id="s">b</span>`,
			want:    []string{"<p>a</p>", "<span>b</span>"},
			notWant: []string{"class=", "style=", "data-y", "id="},
		},
		{
			name: "allowed inline and block elements survive",
			in:   `<h1>h</h1><h6>h6</h6><blockquote>q</blockquote><ul><li>u</li></ul><ol><li>o</li></ol><pre><code>c</code></pre><ruby>漢<rt>kan</rt><rp>(</rp></ruby><em>e</em><strong>s</strong><i>i</i><b>b</b><u>u</u><s>s</s>`,
			want: []string{"<h1>h</h1>", "<h6>h6</h6>", "<blockquote>q</blockquote>", "<ul><li>u</li></ul>", "<ol><li>o</li></ol>", "<pre><code>c</code></pre>", "<ruby>漢<rt>kan</rt><rp>(</rp></ruby>", "<em>e</em>", "<strong>s</strong>", "<i>i</i>", "<b>b</b>", "<u>u</u>", "<s>s</s>"},
		},
		{
			name: "invalid XML characters are dropped",
			in:   "<p>a\x00b\x01c</p>",
			want: []string{"<p>abc</p>"},
		},
		{
			name: "empty fragment",
			in:   "",
			want: []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitizeFragment(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("output %q does not contain %q", got, want)
				}
			}
			for _, bad := range tt.notWant {
				if strings.Contains(got, bad) {
					t.Errorf("output %q still contains %q", got, bad)
				}
			}
			doc := chapterDocument("t", "und", got)
			if err := wellFormed(doc); err != nil {
				t.Errorf("sanitized output is not well-formed XHTML: %v\n%s", err, doc)
			}
		})
	}
}

func TestSanitizeFragmentAlwaysWellFormed(t *testing.T) {
	fragments := []string{
		"<p><b>a<i>b</p>",
		"<li>orphan",
		"</p>",
		"<p>&unknown;</p>",
		"<div><span><span>deep</div></span>",
		"<h1><h2>x</h1></h2>",
		"<pre>\n\tcode</pre>",
		"<p>中文引号「」与 emoji 📚</p>",
	}
	for _, f := range fragments {
		body, err := sanitizeFragment(f)
		if err != nil {
			t.Fatalf("sanitize %q: %v", f, err)
		}
		if err := wellFormed(chapterDocument("t", "und", body)); err != nil {
			t.Errorf("fragment %q produced invalid XHTML: %v\n%s", f, err, body)
		}
	}
}

func TestSafeHref(t *testing.T) {
	tests := map[string]string{
		"":                             "",
		"  ":                           "",
		"javascript:alert(1)":          "",
		"JAVASCRIPT:alert(1)":          "",
		"java\tscript:alert(1)":        "",
		" vbscript:x":                  "",
		"data:text/html,<script>x</s>": "",
		"https://example.test/a":       "https://example.test/a",
		"chapter-2.xhtml#section":      "chapter-2.xhtml#section",
		"/OEBPS/chapter-3.xhtml":       "/OEBPS/chapter-3.xhtml",
		"mailto:reader@example.test":   "mailto:reader@example.test",
		"relative/path?q=1":            "relative/path?q=1",
		"#fragment":                    "#fragment",
		"http://example.test/?a=1&b=2": "http://example.test/?a=1&b=2",
	}
	for in, want := range tests {
		if got := safeHref(in); got != want {
			t.Errorf("safeHref(%q) = %q, want %q", in, got, want)
		}
	}
}
