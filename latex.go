package telegramify

import (
	"regexp"
	"strings"
	"unicode"
)

// LatexToUnicode converts mathematical LaTeX into readable Unicode text.
// Unknown commands are preserved. It does not invoke TeX or external programs.
func LatexToUnicode(input string) string { p := latexParser{input: []rune(input)}; return p.parse(0) }

type latexParser struct {
	input      []rune
	pos, depth int
}

func (p *latexParser) parse(end rune) string {
	if p.depth >= 128 {
		s := string(p.input[p.pos:])
		p.pos = len(p.input)
		return s
	}
	p.depth++
	defer func() { p.depth-- }()
	var b strings.Builder
	for p.pos < len(p.input) {
		r := p.input[p.pos]
		if r == end {
			p.pos++
			break
		}
		switch {
		case r == '\\':
			cmd := p.command()
			if cmd == `\frac` {
				rs := []rune(b.String())
				if len(rs) > 0 && unicode.IsDigit(rs[len(rs)-1]) {
					b.WriteByte(' ')
				}
			}
			b.WriteString(p.handle(cmd))
		case r == '{':
			p.pos++
			b.WriteString(p.parse('}'))
		case r == '_' || r == '^':
			p.pos++
			b.WriteString(script(p.arg(), r))
		case unicode.IsSpace(r):
			start := p.pos
			for p.pos < len(p.input) && unicode.IsSpace(p.input[p.pos]) {
				p.pos++
			}
			if strings.ContainsRune(string(p.input[start:p.pos]), '\n') {
				b.WriteString("\n\n")
			} else {
				b.WriteByte(' ')
			}
		default:
			b.WriteRune(r)
			p.pos++
		}
	}
	return b.String()
}
func (p *latexParser) command() string {
	start := p.pos
	p.pos++
	if p.pos < len(p.input) {
		if asciiLetter(p.input[p.pos]) {
			for p.pos < len(p.input) && asciiLetter(p.input[p.pos]) {
				p.pos++
			}
		} else {
			p.pos++
		}
	}
	return string(p.input[start:p.pos])
}
func asciiLetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }
func (p *latexParser) arg() string {
	if p.pos >= len(p.input) {
		return ""
	}
	r := p.input[p.pos]
	if r == '\\' {
		return p.handle(p.command())
	}
	p.pos++
	if r == '{' {
		return p.parse('}')
	}
	return string(r)
}
func (p *latexParser) optional() string {
	if p.pos < len(p.input) && p.input[p.pos] == '[' {
		p.pos++
		return p.parse(']')
	}
	return ""
}
func mapped(s string, table map[string]string) (string, bool) {
	var b strings.Builder
	for _, r := range s {
		v, ok := table[string(r)]
		if !ok {
			return "", false
		}
		b.WriteString(v)
	}
	return b.String(), true
}
func script(s string, kind rune) string {
	s = strings.TrimSpace(s)
	table := superscripts
	if kind == '_' {
		table = subscripts
	}
	if v, ok := mapped(s, table); ok {
		return v
	}
	if len([]rune(s)) > 1 {
		s = "(" + s + ")"
	}
	return string(kind) + s
}
func combine(cmd, s string) string {
	c, ok := combining[cmd]
	if !ok {
		return s
	}
	r := []rune(s)
	switch c.position {
	case "FirstChar":
		i := min(1, len(r))
		for i < len(r) && (unicode.IsSpace(r[i]) || unicode.IsMark(r[i])) {
			i++
		}
		return string(r[:i]) + c.mark + string(r[i:])
	case "LastChar":
		return s + c.mark
	default:
		var b strings.Builder
		for _, r := range r {
			b.WriteRune(r)
			b.WriteString(c.mark)
		}
		return b.String()
	}
}
func parenthesize(s string) string {
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) || r == '_') {
			return "(" + s + ")"
		}
	}
	return s
}
func (p *latexParser) handle(cmd string) string {
	if v, ok := latexSymbols[cmd]; ok {
		return v
	}
	if _, ok := combining[cmd]; ok {
		return combine(cmd, p.arg())
	}
	if table, ok := latexStyles[cmd]; ok {
		var b strings.Builder
		for _, r := range p.arg() {
			if v, ok := table[string(r)]; ok {
				b.WriteString(v)
			} else {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	switch cmd {
	case `\not`:
		s := strings.TrimSpace(p.arg())
		if s == "" {
			return " "
		}
		if v, ok := notMap[s]; ok {
			return v
		}
		r := []rune(s)
		return string(r[0]) + "̸" + string(r[1:])
	case `\frac`:
		n, d := strings.TrimSpace(p.arg()), strings.TrimSpace(p.arg())
		if n == "" && d == "" {
			return ""
		}
		if v, ok := fractions[n+"/"+d]; ok {
			return v
		}
		return parenthesize(n) + "/" + parenthesize(d)
	case `\sqrt`:
		index, s := strings.TrimSpace(p.optional()), strings.TrimSpace(p.arg())
		root := map[string]string{"": "√", "2": "√", "3": "∛", "4": "∜"}[index]
		if root == "" {
			if v, ok := mapped(index, superscripts); ok {
				root = v + "√"
			} else {
				root = "(" + index + ")√"
			}
		}
		return root + combine(`\overline`, s)
	case `\text`, `\operatorname`, `\mbox`, `\textrm`, `\textup`, `\mathop`:
		return p.arg()
	case `\left`, `\right`:
		s := p.arg()
		if s == "." {
			return ""
		}
		return strings.TrimPrefix(s, `\`)
	case `\binom`, `\tbinom`, `\dbinom`:
		return "C(" + p.arg() + "," + p.arg() + ")"
	case `\boxed`:
		return "[" + p.arg() + "]"
	case `\pmod`:
		return " (mod " + p.arg() + ")"
	case `\phantom`, `\hphantom`, `\vphantom`:
		return strings.Repeat(" ", max(1, len([]rune(p.arg()))))
	case `\overset`, `\stackrel`, `\underset`:
		a, b := p.arg(), p.arg()
		kind := '^'
		table := superscripts
		if cmd == `\underset` {
			kind = '_'
			table = subscripts
		}
		v, ok := mapped(a, table)
		if !ok || v == "" {
			v = string(kind) + "(" + a + ")"
		}
		return b + v
	case `\substack`:
		return strings.Join(strings.FieldsFunc(p.arg(), func(r rune) bool { return r == '\n' }), ", ")
	case `\color`:
		p.arg()
		return ""
	case `\cancel`, `\bcancel`, `\xcancel`, `\sout`, `\underbrace`:
		return combine(`\underline`, p.arg())
	case `\overbrace`:
		return combine(`\overline`, p.arg())
	case `\xrightarrow`, `\xleftarrow`:
		s := p.arg()
		arrow := "→"
		if cmd == `\xleftarrow` {
			arrow = "←"
		}
		if strings.TrimSpace(s) != "" {
			arrow += "(" + s + ")"
		}
		return arrow
	case `\begin`:
		name := p.arg()
		remaining := string(p.input[p.pos:])
		marker := `\end{` + name + "}"
		body, _, found := strings.Cut(remaining, marker)
		if found {
			p.pos += len([]rune(body + marker))
		} else {
			p.pos = len(p.input)
		}
		return latexEnvironment(name, body)
	case `\end`:
		p.arg()
		return ""
	}
	return cmd
}
func latexEnvironment(name, body string) string {
	wrappers := map[string][2]string{"matrix": {}, "pmatrix": {"(", ")"}, "bmatrix": {"[", "]"}, "Bmatrix": {"{", "}"}, "vmatrix": {"|", "|"}, "Vmatrix": {"‖", "‖"}, "smallmatrix": {}, "array": {}}
	wrap, matrix := wrappers[name]
	if name == "array" {
		body = strings.TrimLeftFunc(body, unicode.IsSpace)
		if strings.HasPrefix(body, "{") {
			if i := strings.IndexByte(body, '}'); i >= 0 {
				body = body[i+1:]
			}
		}
	}
	align := strings.Contains("|align|aligned|gather|gathered|equation|equation*|multline|multline*|split|flalign|flalign*|", "|"+name+"|")
	if !matrix && !align && name != "cases" {
		return LatexToUnicode(body)
	}
	rows := []string{}
	for _, row := range strings.Split(body, `\\`) {
		row = strings.TrimSpace(row)
		if row == "" {
			continue
		}
		if align {
			rows = append(rows, LatexToUnicode(strings.ReplaceAll(row, "&", " ")))
			continue
		}
		cells := strings.Split(row, "&")
		for i := range cells {
			cells[i] = LatexToUnicode(strings.TrimSpace(cells[i]))
		}
		sep := "  "
		if name == "smallmatrix" || name == "cases" {
			sep = ", "
		}
		rows = append(rows, strings.Join(cells, sep))
	}
	if name == "cases" {
		for i := range rows {
			prefix := "⎨ "
			if i == 0 {
				prefix = "⎧ "
			} else if i == len(rows)-1 {
				prefix = "⎩ "
			}
			rows[i] = prefix + rows[i]
		}
	}
	sep := "\n"
	if name == "smallmatrix" {
		sep = "; "
	}
	return wrap[0] + strings.Join(rows, sep) + wrap[1]
}
func containsLatex(s string) bool {
	if len([]rune(s)) < 5 {
		return false
	}
	for _, cmd := range []string{`\frac`, `\sqrt`, `\begin`} {
		if strings.Contains(s, cmd) {
			return true
		}
	}
	for cmd := range latexSymbols {
		if strings.Contains(s, cmd) {
			return true
		}
	}
	for cmd := range notMap {
		if strings.Contains(s, cmd) {
			return true
		}
	}
	for cmd := range latexStyles {
		if strings.Contains(s, cmd) {
			return true
		}
	}
	return false
}

var latexBlock = regexp.MustCompile(`(?s)\\\[(.*?)\\\]`)
var latexInline = regexp.MustCompile(`(?s)\\\((.*?)\\\)`)

func escapeLatex(s string) string {
	parts := strings.Split(s, "\n\n")
	for i := range parts {
		for _, p := range []struct {
			re        *regexp.Regexp
			delimiter string
		}{{latexBlock, "$$"}, {latexInline, "$"}} {
			parts[i] = p.re.ReplaceAllStringFunc(parts[i], func(match string) string {
				body := p.re.FindStringSubmatch(match)[1]
				if !containsLatex(body) {
					return match
				}
				return p.delimiter + strings.TrimSpace(LatexToUnicode(body)) + p.delimiter
			})
		}
	}
	return strings.Join(parts, "\n\n")
}
