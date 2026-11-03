package ui

// A small, safe Markdown renderer for chat answers: headings, bold/italic,
// inline code, fenced code blocks, bullet and numbered lists, links and line
// breaks. It never interprets HTML: every byte that is not one of these forms is
// drawn as text. Links open in the desktop browser through a seam.

import (
	"regexp"
	"strings"

	"github.com/egoist/mygo"
	mui "github.com/egoist/mygo/ui"
)

// plainMarkdown wraps text into lines for rendering when no richer form fits.
// (Kept for the composer's one-line context.)

// renderMarkdown draws text block by block.
func (a *App) renderMarkdown(c *mui.Context, text string, size float32) {
	tk := a.tk
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			mui.Spacer(c).Height(4)
			i++
		case isFence(trimmed):
			// Fenced code block, until the closing fence. The language tag is
			// ignored (this is not a syntax highlighter).
			var code []string
			j := i + 1
			for ; j < len(lines); j++ {
				if isFence(strings.TrimSpace(lines[j])) {
					break
				}
				code = append(code, lines[j])
			}
			mui.Column(c).Background(tk.CodeBg).Radius(8).Padding(8, 10).Gap(1).
				Children(func() {
					for _, l := range code {
						mui.Text(c, l).FontSize(fs(11)).Font("monospace").
							TextColor(tk.CodeText)
					}
					if len(code) == 0 {
						mui.Text(c, " ").FontSize(fs(11)).Font("monospace").TextColor(tk.CodeText)
					}
				})
			i = j + 1
		case strings.HasPrefix(trimmed, "#"):
			level, rest := mdHeading(trimmed)
			if level == 0 {
				a.markdownParagraph(c, []string{line}, size)
				i++
				continue
			}
			sz := size + 4
			if level >= 3 {
				sz = size + 1
			} else if level == 2 {
				sz = size + 2
			}
			a.markdownInline(c, rest, sz, true, 0, "")
			i++
		case bullet(trimmed):
			body := strings.TrimSpace(trimmed[1:])
			mui.Row(c).Gap(7).AlignItems(mui.Start).Children(func() {
				mui.Text(c, "•").FontSize(fs(size)).TextColor(tk.TextMuted)
				a.markdownInline(c, body, size, false, 0, "")
			})
			i++
		case ordered(trimmed) != "":
			num := ordered(trimmed)
			body := strings.TrimSpace(trimmed[len(num)+1:])
			mui.Row(c).Gap(7).AlignItems(mui.Start).Children(func() {
				mui.Text(c, num+".").FontSize(fs(size)).TextColor(tk.TextMuted)
				a.markdownInline(c, body, size, false, 0, "")
			})
			i++
		default:
			// A paragraph: consecutive non-empty, non-block lines; line breaks are
			// kept as separate lines (a chat answer's newlines mean something).
			var para []string
			j := i
			for ; j < len(lines); j++ {
				t := strings.TrimSpace(lines[j])
				if t == "" || isFence(t) || strings.HasPrefix(t, "#") || bullet(t) || ordered(t) != "" {
					break
				}
				para = append(para, lines[j])
			}
			a.markdownParagraph(c, para, size)
			i = j
		}
	}
}

// markdownParagraph draws the lines of one paragraph; each source line is one
// line on screen (no reflow games), with inline spans resolved.
func (a *App) markdownParagraph(c *mui.Context, lines []string, size float32) {
	for _, line := range lines {
		a.markdownInline(c, strings.TrimRight(line, " "), size, false, 0, "")
	}
}

// markdownInline draws one line of inline Markdown.
func (a *App) markdownInline(c *mui.Context, text string, size float32, heading bool, _ int, _ string) {
	tk := a.tk
	spans := parseInline(text)
	if len(spans) == 0 {
		return
	}
	row := mui.Row(c).Gap(0).Wrap().AlignItems(mui.Center)
	row.Children(func() {
		for _, s := range spans {
			s := s
			label := s.text
			if label == "" {
				continue
			}
			t := mui.Text(c, label).FontSize(fs(size)).TextColor(tk.Text)
			if heading {
				t.Bold()
			}
			if s.bold {
				t.Bold()
			}
			if s.italic {
				t.Italic()
			}
			if s.code {
				t.Font("monospace").TextColor(tk.Accent)
			}
			if s.link != "" {
				t.TextColor(tk.Accent).Tooltip(s.link)
				link := s.link
				t.OnClick(func() { a.openExternal(link) })
			}
		}
	})
}

// mdSpan is one inline run.
type mdSpan struct {
	text         string
	bold, italic bool
	code         bool
	link         string
}

// parseInline splits one line into inline spans. Unmatched markers are literal
// text: a stray '*' never eats the rest of the answer.
func parseInline(s string) []mdSpan {
	var out []mdSpan
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			out = append(out, mdSpan{text: buf.String()})
			buf.Reset()
		}
	}
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "**"):
			if j := strings.Index(s[i+2:], "**"); j >= 0 {
				flush()
				out = append(out, mdSpan{text: s[i+2 : i+2+j], bold: true})
				i += 2 + j + 2
				continue
			}
		case s[i] == '`':
			if j := strings.IndexByte(s[i+1:], '`'); j >= 0 {
				flush()
				out = append(out, mdSpan{text: s[i+1 : i+1+j], code: true})
				i += 1 + j + 1
				continue
			}
		case s[i] == '*' && !strings.HasPrefix(s[i:], "**"):
			// Emphasis needs a real opening marker: "* " is a bullet/asterisk, not
			// the start of italics (and a stray marker never eats the line).
			if i+1 < len(s) && s[i+1] != ' ' && s[i+1] != '*' {
				if j := strings.IndexByte(s[i+1:], '*'); j >= 0 && j > 0 && s[i+j] != ' ' {
					flush()
					out = append(out, mdSpan{text: s[i+1 : i+1+j], italic: true})
					i += 1 + j + 1
					continue
				}
			}
		case s[i] == '[':
			if close := strings.IndexByte(s[i:], ']'); close > 0 {
				after := s[i+close+1:]
				if strings.HasPrefix(after, "(") {
					if end := strings.IndexByte(after, ')'); end > 0 {
						flush()
						out = append(out, mdSpan{
							text: s[i+1 : i+close],
							link: strings.TrimSpace(after[1:end]),
						})
						i += close + 1 + end + 1
						continue
					}
				}
			}
		}
		buf.WriteByte(s[i])
		i++
	}
	flush()
	return out
}

// openExternal opens a Markdown link in the desktop's default browser, off the
// frame (the browser call waits for the main thread).
var openExternalURL = func(url string) error { return mygo.Shell.OpenExternal(url) }

func (a *App) openExternal(url string) {
	url = strings.TrimSpace(url)
	if url == "" || !validAuthURL(url) {
		a.toast("链接不是 http(s) 地址，未打开", toastErr)
		return
	}
	a.runOffMain(func() {
		if err := openExternalURL(url); err != nil {
			a.postToUI(func() { a.toast("打开链接失败："+err.Error(), toastErr) })
		}
	})
}

// fence is ``` or ~~~ (with a language suffix allowed).
func isFence(t string) bool {
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

// mdHeading returns the level and the text of "# Title", or 0.
func mdHeading(t string) (int, string) {
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n >= len(t) || t[n] != ' ' {
		return 0, ""
	}
	return n, strings.TrimSpace(t[n:])
}

// bullet reports "- ", "* " or "+ " (but not a lone emphasis marker).
func bullet(t string) bool {
	return len(t) > 1 && (t[0] == '-' || t[0] == '*' || t[0] == '+') && t[1] == ' '
}

var orderedRE = regexp.MustCompile(`^(\d{1,3})\. `)

// ordered returns "1" for "1. item", or "".
func ordered(t string) string {
	m := orderedRE.FindStringSubmatch(t)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}
