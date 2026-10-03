package telegramify

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

type MermaidRenderer func(context.Context, string, RenderConfig) ([]byte, error)

type TelegramifyOptions struct {
	ConvertOptions
	MaxMessageLength int
	DisableMermaid   bool
	// MinFileLines defaults to 1 with nil options; 0 keeps code blocks inline.
	MinFileLines int
	// RenderMermaid overrides the HTTP renderer, for local rendering or tests.
	RenderMermaid MermaidRenderer
}

func DefaultTelegramifyOptions() TelegramifyOptions {
	return TelegramifyOptions{MaxMessageLength: 4096, MinFileLines: 1}
}

// Telegramify converts, extracts code and Mermaid blocks, and splits text.
// Default Mermaid rendering sends diagram source to mermaid.ink. Set
// DisableMermaid to keep diagrams inline, or provide a local RenderMermaid.
func Telegramify(ctx context.Context, markdown string, options *TelegramifyOptions) ([]Content, error) {
	opts := DefaultTelegramifyOptions()
	if options != nil {
		opts = *options
		if opts.MaxMessageLength == 0 {
			opts.MaxMessageLength = 4096
		}
	}
	if opts.MaxMessageLength < 1 || opts.MinFileLines < 0 {
		return nil, fmt.Errorf("message length must be positive and minimum file lines nonnegative")
	}
	text, entities, segments := ConvertWithSegments(markdown, &opts.ConvertOptions)
	result := []Content{}
	appendText := func(start, end, offset, endOffset int) error {
		chunk := trimChunk(TextChunk{text[start:end], clipEntities(entities, offset, endOffset)})
		chunks, err := SplitEntities(chunk.Text, chunk.Entities, opts.MaxMessageLength)
		if err != nil {
			return err
		}
		for _, c := range chunks {
			c = trimChunk(c)
			if c.Text != "" {
				result = append(result, Text{Text: c.Text, Entities: c.Entities, ContentTrace: ContentTrace{SourceType: "text"}})
			}
		}
		return nil
	}
	cursor, offset := 0, 0
	for _, s := range segments {
		extract := s.Kind == "mermaid" && !opts.DisableMermaid || s.Kind == "code_block" && opts.MinFileLines > 0 && strings.Count(s.RawCode, "\n")+1 >= opts.MinFileLines
		if !extract {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := appendText(cursor, s.TextStart, offset, s.UTF16Start); err != nil {
			return nil, err
		}
		if s.Kind == "code_block" {
			result = append(result, File{FileName: GetFilename(s.RawCode, s.Language), FileData: []byte(s.RawCode), ContentTrace: ContentTrace{SourceType: "file", Extra: map[string]any{"language": s.Language}}})
		} else {
			renderer := opts.RenderMermaid
			if renderer == nil {
				renderer = func(ctx context.Context, source string, config RenderConfig) ([]byte, error) {
					return RenderMermaid(ctx, source, nil, &config)
				}
			}
			config := renderConfig(opts.Config)
			data, err := renderer(ctx, s.RawCode, config)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			trace := ContentTrace{SourceType: "mermaid"}
			if err != nil {
				result = append(result, File{FileName: "invalid_mermaid.txt", FileData: []byte(s.RawCode), ContentTrace: trace})
			} else {
				caption := "Edit on mermaid.live"
				result = append(result, Photo{FileName: "mermaid." + config.Mermaid.ImageType, FileData: data, ContentTrace: trace, CaptionText: caption, CaptionEntities: []MessageEntity{{Type: "text_link", Length: UTF16Len(caption), URL: MermaidLiveURL(s.RawCode, &config)}}})
			}
		}
		cursor = s.TextEnd
		offset = s.UTF16End
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := appendText(cursor, len(text), offset, UTF16Len(text)); err != nil {
		return nil, err
	}
	return result, nil
}

var languageExtensions = map[string]string{
	"python": "py", "javascript": "js", "typescript": "ts", "java": "java", "c++": "cpp", "c": "c", "html": "html", "css": "css", "bash": "sh", "shell": "sh", "php": "php", "markdown": "md", "dotenv": "env", "json": "json", "yaml": "yaml", "xml": "xml", "dockerfile": "dockerfile", "plaintext": "txt", "toml": "toml", "go": "go", "ruby": "rb", "rust": "rs", "perl": "pl", "swift": "swift", "kotlin": "kt", "sql": "sql", "jsx": "jsx", "tsx": "tsx", "graphql": "graphql", "r": "r", "dart": "dart", "scala": "scala", "groovy": "groovy",
}
var filenamePattern = regexp.MustCompile(`[a-zA-Z0-9_.-]+\.[a-zA-Z0-9]+`)

func GetExt(language string) string {
	if ext := languageExtensions[strings.ToLower(language)]; ext != "" {
		return ext
	}
	return "txt"
}
func ExtractValidFilename(line string) string { return filenamePattern.FindString(line) }
func GetFilename(code, language string) string {
	lines := strings.Split(strings.TrimSpace(code), "\n")
	if len(lines) > 2 {
		lines = lines[:2]
	}
	name := ExtractValidFilename(strings.ReplaceAll(strings.Join(lines, ""), `\`, ""))
	ext := GetExt(language)
	if name == "" {
		return "readable." + ext
	}
	if strings.HasSuffix(name, "."+ext) && utf8.RuneCountInString(name) <= 24 {
		return name
	}
	return name + "." + ext
}
