package telegramify

import (
	"fmt"
	"html"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	nethtml "golang.org/x/net/html"
)

type RichSplitOptions struct{ ByteLimit, BlockLimit int }

// SplitRich splits at top-level blocks, preserving inline formatting and list
// numbering. Atomic blocks that cannot be split are emitted alone with a warning,
// matching Python behavior; callers can inspect their byte length before sending.
func SplitRich(message InputRichMessage, options *RichSplitOptions) ([]InputRichMessage, error) {
	if err := message.Validate(); err != nil {
		return nil, err
	}
	if options == nil {
		options = &RichSplitOptions{RichByteLimit, RichBlockLimit}
	}
	if options.ByteLimit <= 0 || options.BlockLimit <= 0 {
		return nil, fmt.Errorf("rich byte and block limits must be positive")
	}
	result := []InputRichMessage{}
	add := func(body string) {
		r := message
		if message.HTML != nil {
			r.HTML = &body
		} else {
			r.Markdown = &body
		}
		result = append(result, r)
	}
	if message.Markdown != nil {
		source := *message.Markdown
		if strings.TrimSpace(source) == "" {
			return result, nil
		}
		if len(source) <= options.ByteLimit {
			return []InputRichMessage{message}, nil
		}
		current := ""
		for _, p := range strings.Split(source, "\n\n") {
			if len(p) > options.ByteLimit {
				if current != "" {
					add(current)
					current = ""
				}
				parts, err := splitBytes(p, options.ByteLimit, false)
				if err != nil {
					return nil, err
				}
				for _, part := range parts {
					add(part)
				}
				continue
			}
			if current != "" && len(current)+2+len(p) > options.ByteLimit {
				add(current)
				current = ""
			}
			if current != "" {
				current += "\n\n"
			}
			current += p
		}
		if current != "" {
			add(current)
		}
		return result, nil
	}
	current := ""
	count := 0
	flush := func() {
		if current != "" {
			add(current)
			current = ""
			count = 0
		}
	}
	for _, block := range htmlBlocks(*message.HTML) {
		if len(block) > options.ByteLimit {
			flush()
			parts, err := splitOversized(block, options.ByteLimit, 0)
			if err != nil {
				return nil, err
			}
			if len(parts) == 0 {
				parts = []string{block}
			}
			for _, part := range parts {
				if len(part) > options.ByteLimit {
					slog.Warn("Rich block exceeds byte limit and cannot be split", "bytes", len(part), "limit", options.ByteLimit)
				}
				add(part)
			}
			continue
		}
		if len(current)+len(block) > options.ByteLimit || count >= options.BlockLimit {
			flush()
		}
		current += block
		count++
	}
	flush()
	return result, nil
}

var voidTags = map[string]bool{"br": true, "hr": true, "img": true, "wbr": true, "input": true, "meta": true, "link": true, "source": true, "area": true, "base": true, "embed": true, "param": true, "col": true, "track": true}

func htmlBlocks(source string) []string {
	result := []string{}
	z := nethtml.NewTokenizer(strings.NewReader(source))
	start, pos, depth := 0, 0, 0
	for {
		kind := z.Next()
		if kind == nethtml.ErrorToken {
			break
		}
		raw := string(z.Raw())
		pos += len(raw)
		switch kind {
		case nethtml.StartTagToken:
			name, _ := z.TagName()
			if !voidTags[string(name)] {
				depth++
			}
		case nethtml.EndTagToken:
			if depth > 0 {
				depth--
			}
		}
		if depth == 0 {
			if strings.TrimSpace(source[start:pos]) != "" {
				result = append(result, source[start:pos])
			}
			start = pos
		}
	}
	if start < len(source) && strings.TrimSpace(source[start:]) != "" {
		result = append(result, source[start:])
	}
	return result
}

var prePattern = regexp.MustCompile(`(?s)^(<pre(?:><code class="[^"]+">|>))(.*)(</code></pre>|</pre>)$`)
var containerPattern = regexp.MustCompile(`^<(blockquote|ul|ol|table)(?:\s(?:"[^"]*"|'[^']*'|[^>"'])*)?>`)
var startAttr = regexp.MustCompile(`\sstart\s*=\s*(?:"([0-9]+)"|'([0-9]+)')`)
var anyStartAttr = regexp.MustCompile(`\sstart\s*=`)

func reopenList(open, tag string, items int) string {
	if tag != "ol" || items == 0 {
		return open
	}
	m := startAttr.FindStringSubmatchIndex(open)
	if m != nil {
		a, b := m[2], m[3]
		if a < 0 {
			a, b = m[4], m[5]
		}
		n, err := strconv.Atoi(open[a:b])
		if err != nil {
			return open
		}
		return open[:a] + strconv.Itoa(n+items) + open[b:]
	}
	if anyStartAttr.MatchString(open) {
		return open
	}
	return `<ol start="` + strconv.Itoa(1+items) + `"` + open[len("<ol"):]
}
func splitOversized(block string, limit, depth int) ([]string, error) {
	if strings.HasPrefix(block, "<p>") && strings.HasSuffix(block, "</p>") {
		budget := limit - len("<p></p>")
		if budget <= 0 {
			return nil, fmt.Errorf("byte limit must leave room for paragraph tags")
		}
		body := block[3 : len(block)-4]
		parts, ok := splitInline(body, budget)
		if !ok {
			var err error
			parts, err = splitBytes(fragmentText(body), budget, true)
			if err != nil {
				return nil, err
			}
			for i := range parts {
				parts[i] = escapeHTML(parts[i])
			}
			slog.Warn("Rich paragraph split as plain text because its tags exceed the byte budget")
		}
		for i := range parts {
			parts[i] = wrap("p", parts[i])
		}
		return parts, nil
	}
	if m := prePattern.FindStringSubmatch(block); m != nil {
		parts, err := splitBytes(html.UnescapeString(m[2]), limit-len(m[1])-len(m[3]), true)
		if err != nil {
			return nil, err
		}
		for i := range parts {
			parts[i] = m[1] + escapeHTML(parts[i]) + m[3]
		}
		return parts, nil
	}
	m := containerPattern.FindStringSubmatch(block)
	if m == nil || depth >= 16 {
		return nil, nil
	}
	open, tag := m[0], m[1]
	close := "</" + tag + ">"
	if !strings.HasSuffix(block, close) || len(htmlBlocks(block)) != 1 {
		return nil, nil
	}
	children := htmlBlocks(block[len(open) : len(block)-len(close)])
	items := 0
	for _, c := range children {
		if strings.HasPrefix(c, "<li") {
			items++
		}
	}
	budget := limit - max(len(open), len(reopenList(open, tag, items))) - len(close)
	if budget <= 0 {
		return nil, nil
	}
	expanded := []string{}
	for _, child := range children {
		if len(child) > budget {
			parts, err := splitOversized(child, budget, depth+1)
			if err != nil {
				return nil, err
			}
			if len(parts) > 0 {
				expanded = append(expanded, parts...)
				continue
			}
		}
		expanded = append(expanded, child)
	}
	groups := [][]string{}
	current := []string{}
	size := 0
	for _, child := range expanded {
		if len(current) > 0 && size+len(child) > budget {
			groups = append(groups, current)
			current = nil
			size = 0
		}
		current = append(current, child)
		size += len(child)
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	if len(groups) <= 1 {
		return nil, nil
	}
	result := []string{}
	before := 0
	for _, group := range groups {
		result = append(result, reopenList(open, tag, before)+strings.Join(group, "")+close)
		for _, child := range group {
			if strings.HasPrefix(child, "<li") {
				before++
			}
		}
	}
	return result, nil
}

var inlinePattern = regexp.MustCompile(`</?([A-Za-z][A-Za-z0-9-]*)(?:\s(?:"[^"]*"|'[^']*'|[^>"'])*)?/?>|&(?:#[0-9]+|#[xX][0-9a-fA-F]+|[A-Za-z][A-Za-z0-9]*);|\s{1,64}|[^<&\s]{1,64}|[<&]`)

func splitInline(fragment string, budget int) ([]string, bool) {
	type token struct{ value, kind, name string }
	tokens := []token{}
	for _, m := range inlinePattern.FindAllStringSubmatch(fragment, -1) {
		value, name := m[0], strings.ToLower(m[1])
		kind := "text"
		switch {
		case name == "":
			if strings.TrimSpace(value) == "" {
				kind = "space"
			}
		case strings.HasPrefix(value, "</"):
			kind = "close"
		case name == "br":
			kind = "br"
		case strings.HasSuffix(value, "/>") || voidTags[name]:
			kind = "void"
		default:
			kind = "open"
		}
		tokens = append(tokens, token{value, kind, name})
	}
	type frame struct{ open, close string }
	carried := []frame{}
	parts := []string{}
	for start := 0; start < len(tokens); {
		stack := append([]frame(nil), carried...)
		size, reserve := 0, 0
		for _, f := range stack {
			size += len(f.open)
			reserve += len(f.close)
		}
		end, lineCut, spaceCut := start, -1, -1
		var lineStack, spaceStack []frame
		for end < len(tokens) {
			t := tokens[end]
			next := append([]frame(nil), stack...)
			nextReserve := reserve
			switch t.kind {
			case "close":
				if len(next) == 0 || next[len(next)-1].close != "</"+t.name+">" {
					return nil, false
				}
				nextReserve -= len(next[len(next)-1].close)
				next = next[:len(next)-1]
			case "open":
				close := "</" + t.name + ">"
				next = append(next, frame{t.value, close})
				nextReserve += len(close)
			}
			if size+len(t.value)+nextReserve > budget {
				break
			}
			size += len(t.value)
			stack = next
			reserve = nextReserve
			end++
			if t.kind == "br" {
				lineCut = end
				lineStack = append([]frame(nil), stack...)
			} else if t.kind == "space" {
				spaceCut = end
				spaceStack = append([]frame(nil), stack...)
			}
		}
		cut, cutStack := end, stack
		if end == len(tokens) {
			if len(stack) > 0 {
				return nil, false
			}
		} else if end == start {
			return nil, false
		} else if lineCut >= 0 {
			cut, cutStack = lineCut, lineStack
		} else if spaceCut >= 0 {
			cut, cutStack = spaceCut, spaceStack
		}
		var b strings.Builder
		for _, f := range carried {
			b.WriteString(f.open)
		}
		for _, t := range tokens[start:cut] {
			b.WriteString(t.value)
		}
		for i := len(cutStack) - 1; i >= 0; i-- {
			b.WriteString(cutStack[i].close)
		}
		parts = append(parts, b.String())
		start = cut
		carried = cutStack
	}
	return parts, true
}
func fragmentText(fragment string) string {
	z := nethtml.NewTokenizer(strings.NewReader(fragment))
	var b strings.Builder
	for {
		t := z.Next()
		if t == nethtml.ErrorToken {
			break
		}
		if t == nethtml.TextToken {
			b.Write(z.Text())
		}
	}
	return b.String()
}
func splitBytes(text string, limit int, escaped bool) ([]string, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("byte limit must leave room for wrapper tags")
	}
	parts := []string{}
	start, size := 0, 0
	for i, r := range text {
		n := len(string(r))
		if escaped {
			n = len(escapeHTML(string(r)))
		}
		if n > limit {
			return nil, fmt.Errorf("byte limit %d cannot fit a rune", limit)
		}
		if size+n > limit {
			parts = append(parts, text[start:i])
			start = i
			size = 0
		}
		size += n
	}
	if start < len(text) {
		parts = append(parts, text[start:])
	}
	return parts, nil
}

func TelegramifyRich(markdown string, options *RichOptions) ([]RichMessage, error) {
	message, err := Richify(markdown, options)
	if err != nil {
		return nil, err
	}
	parts, err := SplitRich(message, nil)
	if err != nil {
		return nil, err
	}
	result := make([]RichMessage, 0, len(parts))
	for _, p := range parts {
		result = append(result, RichMessage{RichMessage: p, ContentTrace: ContentTrace{SourceType: "rich"}})
	}
	return result, nil
}
