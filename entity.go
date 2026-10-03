package telegramify

import (
	"fmt"
	"strings"
)

// MessageEntity uses UTF-16 code units for Offset and Length, as required by Telegram.
type MessageEntity struct {
	Type           string         `json:"type"`
	Offset         int            `json:"offset"`
	Length         int            `json:"length"`
	URL            string         `json:"url,omitempty"`
	Language       string         `json:"language,omitempty"`
	CustomEmojiID  string         `json:"custom_emoji_id,omitempty"`
	User           map[string]any `json:"user,omitempty"`
	UnixTime       *int64         `json:"unix_time,omitempty"`
	DateTimeFormat *string        `json:"date_time_format,omitempty"`
}

type TextChunk struct {
	Text     string          `json:"text"`
	Entities []MessageEntity `json:"entities"`
}

func UTF16Len(text string) int {
	n := 0
	for _, r := range text {
		n++
		if r > 0xffff {
			n++
		}
	}
	return n
}

func clipEntities(entities []MessageEntity, start, end int) []MessageEntity {
	result := make([]MessageEntity, 0)
	for _, e := range entities {
		a, b := max(e.Offset, start), min(e.Offset+e.Length, end)
		if a < b {
			e.Offset = a - start
			e.Length = b - a
			result = append(result, e)
		}
	}
	return result
}

// SplitEntities prefers newline boundaries and clips entities across chunks.
// A limit too small for a single rune returns an error instead of invalid UTF-8.
func SplitEntities(text string, entities []MessageEntity, limit int) ([]TextChunk, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("UTF-16 limit must be positive")
	}
	result := make([]TextChunk, 0)
	for start, offset := 0, 0; start < len(text); {
		end, units, newline, newlineUnits := start, 0, -1, 0
		for i, r := range text[start:] {
			size := 1
			if r > 0xffff {
				size = 2
			}
			if units+size > limit {
				break
			}
			units += size
			end = start + i + len(string(r))
			if r == '\n' {
				newline = end
				newlineUnits = units
			}
		}
		if end == start {
			return nil, fmt.Errorf("UTF-16 limit %d cannot fit a rune", limit)
		}
		if end < len(text) && newline > start {
			end = newline
			units = newlineUnits
		}
		if strings.TrimSpace(text[start:end]) != "" {
			result = append(result, TextChunk{text[start:end], clipEntities(entities, offset, offset+units)})
		}
		start = end
		offset += units
	}
	return result, nil
}

func trimChunk(c TextChunk) TextChunk {
	text := strings.Trim(c.Text, "\n")
	leading := len(c.Text) - len(strings.TrimLeft(c.Text, "\n"))
	return TextChunk{text, clipEntities(c.Entities, leading, leading+UTF16Len(text))}
}

// CountText counts Telegram plain-text length, including astral Unicode characters.
func CountText(text string) int { return UTF16Len(text) }
