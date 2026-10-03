package telegramify

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func escapeChars(s, chars string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(chars, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
func escapeMD(s string) string { return escapeChars(s, "_*[]()~`>#+-=|{}.!\\") }
func markers(e MessageEntity) (string, string) {
	if s := map[string]string{"bold": "*", "italic": "_", "underline": "__", "strikethrough": "~", "spoiler": "||"}[e.Type]; s != "" {
		return s, s
	}
	switch e.Type {
	case "code":
		return "`", "`"
	case "pre":
		return "```" + e.Language + "\n", "\n```"
	case "text_link":
		return "[", "](" + escapeChars(e.URL, ")\\") + ")"
	case "custom_emoji":
		return "![", "](tg://emoji?id=" + e.CustomEmojiID + ")"
	case "text_mention":
		return "[", "]"
	case "date_time":
		s := "tg://time?unix="
		if e.UnixTime != nil {
			s += strconv.FormatInt(*e.UnixTime, 10)
		}
		if e.DateTimeFormat != nil {
			s += "&format=" + escapeChars(*e.DateTimeFormat, ")\\")
		}
		return "![", "](" + s + ")"
	}
	return "", ""
}

// EntitiesToMarkdownV2 serializes text and entities for parse_mode="MarkdownV2".
// Entity boundaries inside a UTF-16 surrogate pair or outside text are ignored.
func EntitiesToMarkdownV2(text string, entities []MessageEntity) string {
	if text == "" {
		return ""
	}
	if len(entities) == 0 {
		return escapeMD(text)
	}
	offsets := map[int]int{}
	units := 0
	for i, r := range text {
		offsets[units] = i
		units++
		if r > 0xffff {
			units++
		}
	}
	offsets[units] = len(text)
	type event struct {
		pos, kind, length, seq int
		entity                 MessageEntity
	}
	events := []event{}
	type quote struct{ start, end int }
	quotes := []quote{}
	expandableStarts, expandableEnds := map[int]bool{}, map[int]bool{}
	for i, e := range entities {
		start, ok := offsets[e.Offset]
		end, ok2 := offsets[e.Offset+e.Length]
		if !ok || !ok2 || end <= start {
			continue
		}
		if e.Type == "blockquote" || e.Type == "expandable_blockquote" {
			quotes = append(quotes, quote{start, end})
			if e.Type == "expandable_blockquote" {
				expandableStarts[start] = true
				expandableEnds[end] = true
			}
			continue
		}
		events = append(events, event{start, 1, -e.Length, i, e}, event{end, 0, e.Length, -i, e})
	}
	sort.SliceStable(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if a.pos != b.pos {
			return a.pos < b.pos
		}
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		if a.length != b.length {
			return a.length < b.length
		}
		return a.seq < b.seq
	})
	sort.Slice(quotes, func(i, j int) bool { return quotes[i].start < quotes[j].start })
	maxEnd := 0
	for i := range quotes {
		maxEnd = max(maxEnd, quotes[i].end)
		quotes[i].end = maxEnd
	}
	inQuote := func(pos int) bool {
		i := sort.Search(len(quotes), func(i int) bool { return quotes[i].start > pos })
		return i > 0 && quotes[i-1].end > pos
	}
	positions := map[int]bool{}
	for _, e := range events {
		positions[e.pos] = true
	}
	for p := range expandableEnds {
		positions[p] = true
	}
	boundaries := make([]int, 0, len(positions))
	for p := range positions {
		boundaries = append(boundaries, p)
	}
	sort.Ints(boundaries)
	var b strings.Builder
	prefix := func(pos int) {
		if expandableStarts[pos] {
			b.WriteString("**>")
		} else if inQuote(pos) {
			b.WriteByte('>')
		}
	}
	prefix(0)
	active := map[int]bool{}
	emit := func(start, end int, afterPre bool) {
		if afterPre && strings.HasPrefix(text[start:end], "\n\n") {
			start++
		}
		for i, r := range text[start:end] {
			chars := "_*[]()~`>#+-=|{}.!\\"
			if len(active) > 0 {
				chars = "`\\"
			}
			if strings.ContainsRune(chars, r) {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
			if r == '\n' {
				prefix(start + i + 1)
			}
		}
	}
	tag := func(s string, pos int) {
		if inQuote(pos) || (pos > 0 && inQuote(pos-1)) {
			s = strings.ReplaceAll(s, "\n", "\n>")
		}
		b.WriteString(s)
	}
	prev, index := 0, 0
	closedPre := false
	for _, pos := range boundaries {
		if pos > prev {
			emit(prev, pos, closedPre)
		}
		closedPre = false
		end := index
		for end < len(events) && events[end].pos == pos {
			end++
		}
		for _, ev := range events[index:end] {
			if ev.kind == 0 {
				delete(active, -ev.seq)
				_, close := markers(ev.entity)
				tag(close, pos)
				if ev.entity.Type == "pre" {
					closedPre = true
				}
			}
		}
		if expandableEnds[pos] {
			b.WriteString("||")
		}
		for _, ev := range events[index:end] {
			if ev.kind == 1 {
				if ev.entity.Type == "pre" || ev.entity.Type == "code" {
					active[ev.seq] = true
				}
				open, _ := markers(ev.entity)
				tag(open, pos)
			}
		}
		index = end
		prev = pos
	}
	if prev < len(text) {
		emit(prev, len(text), closedPre)
	}
	return b.String()
}

func Markdownify(markdown string, options *ConvertOptions) string {
	text, entities := Convert(markdown, options)
	return EntitiesToMarkdownV2(text, entities)
}
func Standardize(markdown string, options *ConvertOptions) string {
	return Markdownify(markdown, options)
}

// SplitMarkdownV2 includes escapes and formatting markers in its length budget.
func SplitMarkdownV2(text string, entities []MessageEntity, limit int) ([]string, error) {
	pending, err := SplitEntities(text, entities, limit)
	if err != nil {
		return nil, err
	}
	result := []string{}
	for len(pending) > 0 {
		c := pending[0]
		pending = pending[1:]
		rendered := EntitiesToMarkdownV2(c.Text, c.Entities)
		if UTF16Len(rendered) <= limit {
			result = append(result, rendered)
			continue
		}
		size := UTF16Len(c.Text)
		if size <= 1 {
			return nil, fmt.Errorf("a text unit and its MarkdownV2 markers exceed limit %d", limit)
		}
		parts, err := SplitEntities(c.Text, c.Entities, max(1, size/2))
		if err != nil || len(parts) <= 1 {
			return nil, fmt.Errorf("cannot split MarkdownV2 within limit %d", limit)
		}
		pending = append(parts, pending...)
	}
	return result, nil
}
