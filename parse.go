package telegramify

import (
	"bytes"
	"html"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	ext "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var kindSpecial = ast.NewNodeKind("TelegramInline")

type special struct {
	ast.BaseInline
	Value   string
	Display bool
	Spoiler bool
	Start   int
}

func (n *special) Kind() ast.NodeKind         { return kindSpecial }
func (n *special) Dump(src []byte, level int) { ast.DumpHelper(n, src, level, nil, nil) }

type delimiterParser struct{ char byte }

func (p *delimiterParser) Trigger() []byte                           { return []byte{p.char} }
func (p *delimiterParser) IsDelimiter(b byte) bool                   { return b == p.char }
func (p *delimiterParser) CanOpenCloser(a, b *parser.Delimiter) bool { return a.Char == b.Char }
func (p *delimiterParser) OnMatch(n int) ast.Node {
	if p.char == '~' {
		return ext.NewStrikethrough()
	}
	return &special{Spoiler: true}
}
func (p *delimiterParser) Parse(parent ast.Node, reader text.Reader, pc parser.Context) ast.Node {
	line, seg := reader.PeekLine()
	n := parser.ScanDelimiter(line, reader.PrecendingCharacter(), 2, p)
	if n == nil || n.OriginalLength != 2 {
		return nil
	}
	n.Segment = seg.WithStop(seg.Start + 2)
	reader.Advance(2)
	pc.PushDelimiter(n)
	return n
}
func (p *delimiterParser) CloseBlock(ast.Node, parser.Context) {}

// Retain source ranges that Goldmark does not store on fenced code or rules.
type rangedBlockParser struct{ parser.BlockParser }

func (p *rangedBlockParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	_, segment := reader.PeekLine()
	node, state := p.BlockParser.Open(parent, reader, pc)
	if node != nil {
		node.SetAttributeString("sourceStart", segment.Start)
	}
	return node, state
}
func (p *rangedBlockParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	_, segment := reader.PeekLine()
	state := p.BlockParser.Continue(node, reader, pc)
	if state == parser.Close {
		node.SetAttributeString("sourceEnd", segment.Stop)
	}
	return state
}

var literalMathKey = parser.NewContextKey()

type mathParser struct{}

func (*mathParser) Trigger() []byte                     { return []byte{'$'} }
func (*mathParser) CloseBlock(ast.Node, parser.Context) {}
func (*mathParser) Parse(parent ast.Node, reader text.Reader, pc parser.Context) ast.Node {
	line, seg := reader.PeekLine()
	literal, _ := pc.Get(literalMathKey).(map[int]bool)
	if literal[seg.Start] {
		return nil
	}
	count := 1
	if len(line) > 1 && line[1] == '$' {
		count = 2
	}
	if count == 1 && len(line) > 1 {
		r, _ := utf8.DecodeRune(line[1:])
		if unicode.IsSpace(r) {
			return nil
		}
	}
	savedLine, savedPos := reader.Position()
	reader.Advance(count)
	var body strings.Builder
	for {
		var current text.Segment
		line, current = reader.PeekLine()
		if line == nil {
			break
		}
		for i := 0; i < len(line); i++ {
			if line[i] == '\\' {
				i++
				continue
			}
			if line[i] != '$' || (count == 2 && (i+1 >= len(line) || line[i+1] != '$')) {
				continue
			}
			if count == 1 {
				previous := rune(0)
				if i > 0 {
					previous, _ = utf8.DecodeLastRune(line[:i])
				} else {
					previous, _ = utf8.DecodeLastRuneInString(body.String())
				}
				if unicode.IsSpace(previous) {
					continue
				}
			}
			if count == 1 && i+1 < len(line) && line[i+1] >= '0' && line[i+1] <= '9' {
				if literal == nil {
					literal = map[int]bool{}
					pc.Set(literalMathKey, literal)
				}
				literal[current.Start+i] = true
				reader.SetPosition(savedLine, savedPos)
				return nil
			}
			body.Write(line[:i])
			reader.Advance(i + count)
			return &special{Value: body.String(), Display: count == 2, Start: seg.Start}
		}
		body.Write(line)
		reader.AdvanceLine()
	}
	reader.SetPosition(savedLine, savedPos)
	return nil
}

func parseMarkdown(markdown string, latex, rich bool) ([]byte, ast.Node) {
	markdown = strings.ReplaceAll(strings.ReplaceAll(markdown, "\r\n", "\n"), "\r", "\n")
	if latex {
		markdown = escapeLatex(markdown)
	}
	extensions := []goldmark.Extender{extension.Table, extension.TaskList}
	if rich {
		extensions = append(extensions, extension.Footnote)
	}
	md := goldmark.New(goldmark.WithExtensions(extensions...),
		goldmark.WithParserOptions(parser.WithBlockParsers(
			util.Prioritized(&rangedBlockParser{parser.NewThematicBreakParser()}, 199),
			util.Prioritized(&rangedBlockParser{parser.NewFencedCodeBlockParser()}, 699),
		)), goldmark.WithParserOptions(parser.WithInlineParsers(
			util.Prioritized(&delimiterParser{'~'}, 500), util.Prioritized(&mathParser{}, 150),
		)))
	source := []byte(markdown)
	if strings.Contains(markdown, "||") {
		doc := md.Parser().Parse(text.NewReader(source))
		source = preprocessSpoilers(source, doc)
	}
	return source, md.Parser().Parse(text.NewReader(source))
}

// Goldmark retains escapes and character references in ordinary text nodes.
func plainText(s []byte) string {
	var b strings.Builder
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", rune(s[i+1])) {
			b.WriteString(html.UnescapeString(string(s[start:i])))
			i++
			b.WriteByte(s[i])
			start = i + 1
		}
	}
	b.WriteString(html.UnescapeString(string(s[start:])))
	return b.String()
}

func codeText(n ast.Node, src []byte) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			b.Write(bytes.ReplaceAll(t.Segment.Value(src), []byte{'\n'}, []byte{' '}))
		}
	}
	return b.String()
}
func blockCode(n ast.Node, src []byte) (string, string) {
	var b strings.Builder
	for i := 0; i < n.Lines().Len(); i++ {
		seg := n.Lines().At(i)
		b.Write(seg.Value(src))
	}
	lang := ""
	if f, ok := n.(*ast.FencedCodeBlock); ok && f.Info != nil {
		lang = strings.TrimSpace(strings.SplitN(string(f.Info.Segment.Value(src)), ",", 2)[0])
	}
	return strings.TrimSuffix(b.String(), "\n"), lang
}
func emojiID(url string) string {
	id, ok := strings.CutPrefix(url, "tg://emoji?id=")
	if !ok || len(id) != 19 {
		return ""
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return id
}
