package telegramify

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitEntitiesUnicodeBoundaries(t *testing.T) {
	text := "😀ab\n中文😀xyz"
	entities := []MessageEntity{{Type: "bold", Offset: 2, Length: 10}}
	chunks, err := SplitEntities(text, entities, 5)
	if err != nil {
		t.Fatal(err)
	}
	var joined strings.Builder
	offset := 0
	for _, c := range chunks {
		if !utf8.ValidString(c.Text) || UTF16Len(c.Text) > 5 {
			t.Fatalf("bad chunk %+v", c)
		}
		joined.WriteString(c.Text)
		for _, e := range c.Entities {
			if e.Offset < 0 || e.Length <= 0 || e.Offset+e.Length > UTF16Len(c.Text) || offset+e.Offset < 2 || offset+e.Offset+e.Length > 12 {
				t.Fatalf("invalid entity %+v", e)
			}
		}
		offset += UTF16Len(c.Text)
	}
	if joined.String() != text {
		t.Fatal("text lost during splitting")
	}
	if _, err := SplitEntities("😀", nil, 1); err == nil {
		t.Fatal("expected impossible-limit error")
	}
}
func TestSplitMarkdownV2Budgets(t *testing.T) {
	text := strings.Repeat("😀*x\\\n", 40)
	parts, err := SplitMarkdownV2(text, []MessageEntity{{Type: "bold", Length: UTF16Len(text)}}, 32)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range parts {
		if UTF16Len(p) > 32 || !utf8.ValidString(p) || !strings.HasPrefix(p, "*") || !strings.HasSuffix(p, "*") {
			t.Fatalf("bad chunk %q", p)
		}
	}
	if _, err := SplitMarkdownV2("a", []MessageEntity{{Type: "text_link", Length: 1, URL: strings.Repeat("x", 100)}}, 8); err == nil {
		t.Fatal("expected impossible-marker error")
	}
}
func TestRichJSONValidation(t *testing.T) {
	if _, err := json.Marshal(InputRichMessage{}); err == nil {
		t.Fatal("missing body accepted")
	}
	empty := ""
	payload := InputRichMessage{HTML: &empty}
	data, err := json.Marshal(payload)
	if err != nil || string(data) != `{"html":""}` {
		t.Fatalf("%s %v", data, err)
	}
}

func FuzzConversionAndSplitting(f *testing.F) {
	for _, s := range []string{"Hello **😀**", "> ||secret||\n\n```go\nx\n```", "$\\frac{1}{2}$", "|a|b|\n|-|-|\n|1|2|", "```\n```", "`` ||a|| ``"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if !utf8.ValidString(source) || len(source) > 10000 {
			t.Skip()
		}
		text, entities := Convert(source, nil)
		limit := UTF16Len(text)
		boundaries := map[int]bool{0: true}
		offset := 0
		for _, r := range text {
			offset++
			if r > 0xffff {
				offset++
			}
			boundaries[offset] = true
		}
		for _, e := range entities {
			if e.Offset < 0 || e.Length <= 0 || e.Offset+e.Length > limit || !boundaries[e.Offset] || !boundaries[e.Offset+e.Length] {
				t.Fatalf("invalid entity %+v in %q", e, text)
			}
		}
		chunks, err := SplitEntities(text, entities, 32)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range chunks {
			if !utf8.ValidString(c.Text) || UTF16Len(c.Text) > 32 {
				t.Fatalf("invalid chunk %q", c.Text)
			}
		}
		if _, err := Richify(source, nil); err != nil {
			t.Fatal(err)
		}
	})
}
