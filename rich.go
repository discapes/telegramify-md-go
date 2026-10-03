package telegramify

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yuin/goldmark/ast"
	ext "github.com/yuin/goldmark/extension/ast"
)

const RichByteLimit = 32768
const RichBlockLimit = 500

// InputRichMessage holds exactly one body, including when that body is empty.
type InputRichMessage struct {
	HTML                *string `json:"html,omitempty"`
	Markdown            *string `json:"markdown,omitempty"`
	IsRTL               *bool   `json:"is_rtl,omitempty"`
	SkipEntityDetection *bool   `json:"skip_entity_detection,omitempty"`
}

func (r InputRichMessage) Validate() error {
	if (r.HTML == nil) == (r.Markdown == nil) {
		return fmt.Errorf("exactly one of HTML or Markdown must be set")
	}
	return nil
}
func (r InputRichMessage) MarshalJSON() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	type payload InputRichMessage
	return json.Marshal(payload(r))
}

type RichOptions struct {
	Mode                       string // "html" (default) or "markdown" (passthrough).
	IsRTL, SkipEntityDetection *bool
	LatexEscape                bool
}

func Richify(markdown string, options *RichOptions) (InputRichMessage, error) {
	if options == nil {
		options = &RichOptions{}
	}
	result := InputRichMessage{IsRTL: options.IsRTL, SkipEntityDetection: options.SkipEntityDetection}
	switch options.Mode {
	case "markdown":
		result.Markdown = &markdown
	case "", "html":
		src, doc := parseMarkdown(markdown, options.LatexEscape, true)
		w := richWalker{src: src, footnotes: map[int]string{}}
		ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if f, ok := n.(*ext.Footnote); ok && entering {
				w.footnotes[f.Index] = string(f.Ref)
			}
			return ast.WalkContinue, nil
		})
		html := w.children(doc)
		result.HTML = &html
	default:
		return result, fmt.Errorf("rich mode must be html or markdown")
	}
	return result, nil
}
func escapeHTML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
func escapeAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#x27;").Replace(s)
}

type richWalker struct {
	src       []byte
	footnotes map[int]string
}

func (w *richWalker) children(n ast.Node) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		b.WriteString(w.node(c))
	}
	return b.String()
}
func wrap(tag, body string) string { return "<" + tag + ">" + body + "</" + tag + ">" }
func (w *richWalker) node(n ast.Node) string {
	switch v := n.(type) {
	case *ast.Text:
		s := string(v.Segment.Value(w.src))
		if !v.IsRaw() {
			s = plainText([]byte(s))
		}
		s = escapeHTML(s)
		if v.SoftLineBreak() || v.HardLineBreak() {
			s += "<br/>"
		}
		return s
	case *ast.String:
		return escapeHTML(plainText(v.Value))
	case *ast.Paragraph:
		// Display math is a block even when the Markdown parser puts it in a paragraph.
		var out, inline strings.Builder
		paragraph := true
		flush := func() {
			if inline.Len() > 0 {
				if paragraph {
					out.WriteString(wrap("p", inline.String()))
				} else {
					out.WriteString(inline.String())
				}
				inline.Reset()
			}
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if s, ok := c.(*special); ok && s.Display {
				flush()
				paragraph = false
				out.WriteString(w.node(c))
			} else {
				inline.WriteString(w.node(c))
			}
		}
		flush()
		return out.String()
	case *ast.TextBlock:
		return w.children(n)
	case *ast.Heading:
		return wrap(fmt.Sprintf("h%d", v.Level), w.children(n))
	case *ast.Emphasis:
		tag := "i"
		if v.Level == 2 {
			tag = "b"
		}
		return wrap(tag, w.children(n))
	case *ext.Strikethrough:
		return wrap("s", w.children(n))
	case *ast.CodeSpan:
		return wrap("code", escapeHTML(codeText(n, w.src)))
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		code, lang := blockCode(n, w.src)
		body := escapeHTML(code)
		if strings.EqualFold(lang, "math") {
			return wrap("tg-math-block", body)
		}
		if lang != "" {
			body = `<code class="` + escapeAttr("language-"+lang) + `">` + body + "</code>"
		}
		return wrap("pre", body)
	case *ast.Blockquote:
		return wrap("blockquote", w.children(n))
	case *ast.Link:
		return richLink(plainText(v.Destination), w.children(n))
	case *ast.Image:
		url := plainText(v.Destination)
		alt := w.imageText(n)
		if id := emojiID(url); id != "" {
			return `<tg-emoji emoji-id="` + id + `">` + escapeHTML(alt) + "</tg-emoji>"
		}
		if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") {
			s := `<img src="` + escapeAttr(url) + `" alt="` + escapeAttr(alt) + `"`
			if len(v.Title) > 0 {
				s += ` title="` + escapeAttr(plainText(v.Title)) + `"`
			}
			return s + "/>"
		}
		if alt == "" {
			alt = url
		}
		return richLink(url, escapeHTML(alt))
	case *ast.AutoLink:
		return richLink(string(v.URL(w.src)), escapeHTML(string(v.Label(w.src))))
	case *ast.List:
		if v.IsOrdered() {
			return fmt.Sprintf(`<ol start="%d">`, v.Start) + w.children(n) + "</ol>"
		}
		return wrap("ul", w.children(n))
	case *ast.ListItem:
		return wrap("li", w.children(n))
	case *ext.TaskCheckBox:
		if v.IsChecked {
			return "✅ "
		}
		return "☑ "
	case *ast.ThematicBreak:
		return "<hr/>"
	case *ext.Table:
		var b strings.Builder
		b.WriteString("<table>")
		for row := n.FirstChild(); row != nil; row = row.NextSibling() {
			b.WriteString("<tr>")
			_, header := row.(*ext.TableHeader)
			i := 0
			for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
				tag := "td"
				if header {
					tag = "th"
				}
				attr := ""
				if i < len(v.Alignments) && v.Alignments[i] != ext.AlignNone {
					attr = ` align="` + v.Alignments[i].String() + `"`
				}
				b.WriteString("<" + tag + attr + ">" + w.children(cell) + "</" + tag + ">")
				i++
			}
			b.WriteString("</tr>")
		}
		b.WriteString("</table>")
		return b.String()
	case *ast.RawHTML:
		var b strings.Builder
		for i := 0; i < v.Segments.Len(); i++ {
			seg := v.Segments.At(i)
			b.Write(seg.Value(w.src))
		}
		s := b.String()
		tag := strings.ToLower(strings.TrimSpace(s))
		if tag == "<tg-spoiler>" || tag == "</tg-spoiler>" {
			return tag
		}
		return escapeHTML(s)
	case *ast.HTMLBlock:
		var b strings.Builder
		for i := 0; i < v.Lines().Len(); i++ {
			seg := v.Lines().At(i)
			b.Write(seg.Value(w.src))
		}
		if v.HasClosure() {
			b.Write(v.ClosureLine.Value(w.src))
		}
		return wrap("p", escapeHTML(b.String()))
	case *special:
		if v.Spoiler {
			return wrap("tg-spoiler", w.children(n))
		}
		tag := "tg-math"
		if v.Display {
			tag = "tg-math-block"
		}
		return wrap(tag, escapeHTML(v.Value))
	case *ext.Footnote:
		return `<tg-reference name="` + escapeAttr(string(v.Ref)) + `">` + w.children(n) + "</tg-reference>"
	case *ext.FootnoteLink:
		ref := w.footnotes[v.Index]
		return richLink("#"+ref, escapeHTML("["+ref+"]"))
	case *ext.FootnoteBacklink:
		return ""
	default:
		return w.children(n)
	}
}
func richLink(url, body string) string {
	if id := emojiID(url); id != "" {
		return `<tg-emoji emoji-id="` + id + `">` + body + "</tg-emoji>"
	}
	if url == "" {
		return body
	}
	return `<a href="` + escapeAttr(url) + `">` + body + "</a>"
}
func (w *richWalker) imageText(n ast.Node) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *ast.Text:
			b.WriteString(plainText(v.Segment.Value(w.src)))
			if v.SoftLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.CodeSpan:
			b.WriteString(codeText(c, w.src))
		default:
			b.WriteString(w.imageText(c))
		}
	}
	return b.String()
}
