package telegramify

import (
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark/ast"
)

var spoilerPattern = regexp.MustCompile(`(?s)\|\|(.+?)\|\|`)
var spoilerEdges = regexp.MustCompile(`(?s)^((?:[ \t]*\n(?:[ \t]*>[ \t]?)*)*)(.*?)((?:\n(?:[ \t]*>[ \t]?)*[ \t]*)*)$`)

// Spoilers are rewritten outside parser-recognized code, including nested fences.
func preprocessSpoilers(source []byte, doc ast.Node) []byte {
	type span struct{ start, end int }
	regions := []span{}
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.(type) {
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			start := sourceStart(n, source)
			if start < 0 {
				return ast.WalkSkipChildren, nil
			}
			end := start
			if n.Lines().Len() > 0 {
				end = n.Lines().At(n.Lines().Len() - 1).Stop
			}
			if _, ok := n.(*ast.FencedCodeBlock); ok {
				end = len(source)
				if value, ok := n.AttributeString("sourceEnd"); ok {
					end = value.(int)
				}
			}
			regions = append(regions, span{start, end})
			return ast.WalkSkipChildren, nil
		case *ast.CodeSpan:
			first, ok := n.FirstChild().(*ast.Text)
			if !ok {
				return ast.WalkSkipChildren, nil
			}
			last := n.LastChild().(*ast.Text)
			start, end := first.Segment.Start, last.Segment.Stop
			// CodeSpan may trim one space at each edge before exposing its segments.
			for start > 0 && source[start-1] != '`' {
				start--
			}
			for start > 0 && source[start-1] == '`' {
				start--
			}
			for end < len(source) && source[end] != '`' {
				end++
			}
			for end < len(source) && source[end] == '`' {
				end++
			}
			regions = append(regions, span{start, end})
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	sort.Slice(regions, func(i, j int) bool { return regions[i].start < regions[j].start })
	replace := func(s string) string {
		var b strings.Builder
		cursor := 0
		for _, m := range spoilerPattern.FindAllStringSubmatchIndex(s, -1) {
			if m[0] > 0 && s[m[0]-1] == '\\' {
				continue
			}
			body := s[m[2]:m[3]]
			edges := spoilerEdges.FindStringSubmatch(body)
			if len(edges) != 4 || edges[2] == "" {
				continue
			}
			b.WriteString(s[cursor:m[0]])
			b.WriteString(edges[1] + "<tg-spoiler>" + edges[2] + "</tg-spoiler>" + edges[3])
			cursor = m[1]
		}
		b.WriteString(s[cursor:])
		return b.String()
	}
	var b strings.Builder
	cursor := 0
	for _, r := range regions {
		if r.start < cursor {
			continue
		}
		b.WriteString(replace(string(source[cursor:r.start])))
		b.Write(source[r.start:r.end])
		cursor = r.end
	}
	b.WriteString(replace(string(source[cursor:])))
	return []byte(b.String())
}
