package telegramify

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	ext "github.com/yuin/goldmark/extension/ast"
)

// Segment records an extractable code block. TextStart/TextEnd are UTF-8 byte
// offsets; UTF16Start/UTF16End are Telegram entity offsets.
type Segment struct {
	Kind       string `json:"kind"`
	TextStart  int    `json:"text_start"`
	TextEnd    int    `json:"text_end"`
	UTF16Start int    `json:"utf16_start"`
	UTF16End   int    `json:"utf16_end"`
	Language   string `json:"language"`
	RawCode    string `json:"raw_code"`
}

func Convert(markdown string, options *ConvertOptions) (string, []MessageEntity) {
	text, entities, _ := ConvertWithSegments(markdown, options)
	return text, entities
}

func ConvertWithSegments(markdown string, options *ConvertOptions) (string, []MessageEntity, []Segment) {
	if options == nil {
		options = &ConvertOptions{}
	}
	src, doc := parseMarkdown(markdown, !options.DisableLatexEscape, false)
	w := entityWalker{src: src, config: renderConfig(options.Config), lastStart: -1, entities: []MessageEntity{}, segments: []Segment{}}
	w.children(doc)
	if w.config.CiteExpandable {
		for i := range w.entities {
			if w.entities[i].Type == "blockquote" && w.entities[i].Length > 200 {
				w.entities[i].Type = "expandable_blockquote"
			}
		}
	}
	return w.buf.String(), w.entities, w.segments
}

type entityWalker struct {
	src                                            []byte
	config                                         RenderConfig
	buf                                            strings.Builder
	offset, trailing, blocks, lastStart, listDepth int
	entities                                       []MessageEntity
	segments                                       []Segment
	spoilers                                       []int
}

func (w *entityWalker) write(s string) {
	w.buf.WriteString(s)
	w.offset += UTF16Len(s)
	for _, r := range s {
		if r == '\n' {
			w.trailing++
		} else {
			w.trailing = 0
		}
	}
}
func (w *entityWalker) add(e MessageEntity) {
	e.Length = w.offset - e.Offset
	if e.Length > 0 {
		w.entities = append(w.entities, e)
	}
}
func (w *entityWalker) children(n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		w.node(c)
	}
}
func sourceStart(n ast.Node, src []byte) int {
	if start, ok := n.AttributeString("sourceStart"); ok {
		return start.(int)
	}
	pos := -1
	if n.Type() == ast.TypeBlock && n.Lines().Len() > 0 {
		pos = n.Lines().At(0).Start
	}
	if f, ok := n.(*ast.FencedCodeBlock); ok {
		if f.Info != nil {
			pos = f.Info.Segment.Start
		} else if pos > 0 {
			pos = max(0, pos-1)
			for pos > 0 && src[pos-1] != '\n' {
				pos--
			}
			pos = max(0, pos-1)
		}
	}
	if t, ok := n.(*ast.Text); ok {
		return t.Segment.Start
	}
	if s, ok := n.(*special); ok {
		return s.Start
	}
	if pos < 0 {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			pos = sourceStart(c, src)
			if pos >= 0 {
				break
			}
		}
	}
	if pos >= 0 {
		for pos > 0 && src[pos-1] != '\n' {
			pos--
		}
	}
	return pos
}
func (w *entityWalker) spacing(n ast.Node) { w.spacingAt(sourceStart(n, w.src)) }
func (w *entityWalker) spacingAt(pos int) {
	desired := 1
	if pos < 0 {
		desired = 2
	} else {
		newlines := 0
		for i := pos - 1; i > w.lastStart; i-- {
			ch := w.src[i]
			if ch == '\n' {
				newlines++
				if newlines >= 2 {
					desired = 2
					break
				}
			} else if ch != ' ' && ch != '\t' && ch != '>' {
				break
			}
		}
	}
	if w.blocks > 0 && w.trailing < desired {
		w.write(strings.Repeat("\n", desired-w.trailing))
	}
	if pos >= 0 {
		w.lastStart = pos
	}
}
func (w *entityWalker) node(n ast.Node) {
	switch v := n.(type) {
	case *ast.Text:
		s := string(v.Segment.Value(w.src))
		if !v.IsRaw() {
			s = plainText([]byte(s))
		}
		w.write(s)
		if v.SoftLineBreak() || v.HardLineBreak() {
			w.write("\n")
		}
	case *ast.String:
		w.write(plainText(v.Value))
	case *ast.Paragraph:
		if w.listDepth == 0 {
			w.spacing(n)
		}
		w.children(n)
		if w.listDepth == 0 {
			w.blocks++
		} else if w.trailing == 0 {
			w.write("\n")
		}
	case *ast.TextBlock:
		w.children(n)
	case *ast.Heading:
		w.spacing(n)
		prefix := w.config.Symbols.Headings[v.Level-1]
		if prefix != "" {
			w.write(prefix + " ")
		}
		start := w.offset
		w.children(n)
		if v.Level <= 2 {
			w.add(MessageEntity{Type: "underline", Offset: start})
		}
		kind := "bold"
		if v.Level >= 5 {
			kind = "italic"
		}
		w.add(MessageEntity{Type: kind, Offset: start})
		w.blocks++
	case *ast.Emphasis:
		start := w.offset
		w.children(n)
		kind := "italic"
		if v.Level == 2 {
			kind = "bold"
		}
		w.add(MessageEntity{Type: kind, Offset: start})
	case *ext.Strikethrough:
		start := w.offset
		w.children(n)
		w.add(MessageEntity{Type: "strikethrough", Offset: start})
	case *ast.CodeSpan:
		start := w.offset
		w.write(codeText(n, w.src))
		w.add(MessageEntity{Type: "code", Offset: start})
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		raw, lang := blockCode(n, w.src)
		w.spacing(n)
		seg := Segment{Kind: "code_block", TextStart: w.buf.Len(), UTF16Start: w.offset, Language: lang, RawCode: raw}
		if strings.EqualFold(lang, "mermaid") {
			seg.Kind = "mermaid"
		}
		w.write(raw)
		w.add(MessageEntity{Type: "pre", Offset: seg.UTF16Start, Language: lang})
		seg.TextEnd = w.buf.Len()
		seg.UTF16End = w.offset
		w.segments = append(w.segments, seg)
		w.blocks++
	case *ast.Blockquote:
		w.spacing(n)
		start := w.offset
		w.children(n)
		w.add(MessageEntity{Type: "blockquote", Offset: start})
		w.blocks++
	case *ast.Link:
		w.link(n, string(v.Destination), false)
	case *ast.Image:
		w.link(n, string(v.Destination), true)
	case *ast.AutoLink:
		start := w.offset
		w.write(string(v.Label(w.src)))
		w.add(MessageEntity{Type: "text_link", Offset: start, URL: string(v.URL(w.src))})
	case *ast.List:
		if w.listDepth == 0 {
			w.spacing(n)
		}
		w.listDepth++
		number := v.Start
		for item := n.FirstChild(); item != nil; item = item.NextSibling() {
			if w.buf.Len() > 0 && w.trailing == 0 {
				w.write("\n")
			}
			prefix := w.config.Symbols.UnorderedListItem
			if v.IsOrdered() {
				prefix = fmt.Sprint(number) + w.config.Symbols.OrderedListSuffix
				number++
			}
			if first := item.FirstChild(); first != nil {
				if task, ok := first.FirstChild().(*ext.TaskCheckBox); ok {
					prefix = w.config.Symbols.TaskUncompleted
					if task.IsChecked {
						prefix = w.config.Symbols.TaskCompleted
					}
				}
			}
			w.write(strings.Repeat("  ", w.listDepth-1) + prefix + " ")
			w.children(item)
			if w.trailing == 0 {
				w.write("\n")
			}
		}
		w.listDepth--
		if w.listDepth == 0 {
			w.blocks++
		}
	case *ext.TaskCheckBox:
	case *ast.ThematicBreak:
		w.spacing(n)
		w.write(w.config.Symbols.HorizontalRule)
		w.blocks++
	case *ext.Table:
		w.table(v)
	case *ast.RawHTML:
		var b strings.Builder
		for i := 0; i < v.Segments.Len(); i++ {
			seg := v.Segments.At(i)
			b.Write(seg.Value(w.src))
		}
		switch strings.ToLower(strings.TrimSpace(b.String())) {
		case "<tg-spoiler>":
			w.spoilers = append(w.spoilers, w.offset)
		case "</tg-spoiler>":
			if len(w.spoilers) > 0 {
				last := len(w.spoilers) - 1
				w.add(MessageEntity{Type: "spoiler", Offset: w.spoilers[last]})
				w.spoilers = w.spoilers[:last]
			}
		}
	case *ast.HTMLBlock:
	case *special:
		if v.Spoiler {
			start := w.offset
			w.children(n)
			w.add(MessageEntity{Type: "spoiler", Offset: start})
			return
		}
		s := v.Value
		if containsLatex(s) {
			s = strings.TrimSpace(LatexToUnicode(s))
		}
		kind := "code"
		if v.Display {
			w.spacingAt(v.Start)
			kind = "pre"
		}
		start := w.offset
		w.write(s)
		w.add(MessageEntity{Type: kind, Offset: start})
		if v.Display {
			w.blocks++
		}
	default:
		w.children(n)
	}
}
func (w *entityWalker) link(n ast.Node, url string, image bool) {
	url = plainText([]byte(url))
	id := emojiID(url)
	if image && id == "" {
		w.write(w.config.Symbols.Image)
	}
	start := w.offset
	w.children(n)
	if id != "" {
		w.add(MessageEntity{Type: "custom_emoji", Offset: start, CustomEmojiID: id})
	} else if url != "" {
		w.add(MessageEntity{Type: "text_link", Offset: start, URL: url})
	}
}
func (w *entityWalker) table(n *ext.Table) {
	w.spacing(n)
	rows := [][]string{}
	widths := make([]int, len(n.Alignments))
	for row := n.FirstChild(); row != nil; row = row.NextSibling() {
		cells := []string{}
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cw := entityWalker{src: w.src, config: w.config}
			cw.children(cell)
			s := cw.buf.String()
			i := len(cells)
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], utf8.RuneCountInString(s))
			cells = append(cells, s)
		}
		rows = append(rows, cells)
	}
	lines := []string{}
	for i, row := range rows {
		cells := make([]string, len(widths))
		for j, width := range widths {
			s := ""
			if j < len(row) {
				s = row[j]
			}
			cells[j] = s + strings.Repeat(" ", width-utf8.RuneCountInString(s))
		}
		lines = append(lines, strings.Join(cells, " | "))
		if i == 0 && len(rows) > 1 {
			for j, width := range widths {
				cells[j] = strings.Repeat("-", width)
			}
			lines = append(lines, strings.Join(cells, "-+-"))
		}
	}
	start := w.offset
	w.write(strings.Join(lines, "\n"))
	w.add(MessageEntity{Type: "pre", Offset: start})
	w.blocks++
}
