package telegramify

import (
	"fmt"
	"strings"
	"testing"

	nethtml "golang.org/x/net/html"
)

func assertBalanced(t *testing.T, source string) {
	t.Helper()
	stack := []string{}
	z := nethtml.NewTokenizer(strings.NewReader(source))
	for {
		kind := z.Next()
		if kind == nethtml.ErrorToken {
			break
		}
		name, _ := z.TagName()
		tag := string(name)
		switch kind {
		case nethtml.StartTagToken:
			if !voidTags[tag] {
				stack = append(stack, tag)
			}
		case nethtml.EndTagToken:
			if len(stack) == 0 || stack[len(stack)-1] != tag {
				t.Fatalf("unbalanced closing tag %s in %q", tag, source)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) > 0 {
		t.Fatalf("unclosed tags %v", stack)
	}
}
func TestRichContainerSplitting(t *testing.T) {
	cases := []struct{ name, body string }{
		{"quote", "<blockquote><p>" + strings.Repeat(`a <b>bold &amp; <i>nested</i></b><br/>`, 30) + "</p></blockquote>"},
		{"list", `<ol class="x" start='98'>` + strings.Repeat("<li>😀 item</li>", 30) + "</ol>"},
		{"table", "<table>" + strings.Repeat("<tr><td>😀 &amp; x</td></tr>", 30) + "</table>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			parts, err := SplitRich(InputRichMessage{HTML: &c.body}, &RichSplitOptions{ByteLimit: 150, BlockLimit: 500})
			if err != nil {
				t.Fatal(err)
			}
			if len(parts) < 2 {
				t.Fatal("not split")
			}
			var text strings.Builder
			for _, part := range parts {
				if len(*part.HTML) > 150 {
					t.Fatalf("chunk over budget: %s", *part.HTML)
				}
				assertBalanced(t, *part.HTML)
				text.WriteString(fragmentText(*part.HTML))
			}
			if text.String() != fragmentText(c.body) {
				t.Fatal("split lost or duplicated text")
			}
			if c.name == "list" {
				index := 98
				for _, p := range parts {
					if !strings.Contains(*p.HTML, fmt.Sprintf("start='%d'", index)) {
						t.Fatalf("list numbering changed: %s", *p.HTML)
					}
					index += strings.Count(*p.HTML, "<li>")
				}
			}
		})
	}
}
func TestRichBlockBudgetAndMetadata(t *testing.T) {
	markdown := strings.Repeat("paragraph\n\n", 501)
	messages, err := TelegramifyRich(markdown, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || strings.Count(*messages[0].RichMessage.HTML, "<p>") != 500 {
		t.Fatal("block limit not enforced")
	}
	rtl := false
	body := strings.Repeat("<p>😀 &amp;</p>", 10)
	parts, err := SplitRich(InputRichMessage{HTML: &body, IsRTL: &rtl}, &RichSplitOptions{ByteLimit: 40, BlockLimit: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range parts {
		if p.IsRTL == nil || *p.IsRTL {
			t.Fatal("explicit false metadata lost")
		}
	}
}
