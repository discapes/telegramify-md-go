// Package telegramify converts Markdown into Telegram text, entities and rich messages.
package telegramify

// Symbols controls the plain-text decorations added during entity conversion.
type Symbols struct {
	Headings                                                    [6]string
	Image, Link, TaskCompleted, TaskUncompleted, HorizontalRule string
	UnorderedListItem, OrderedListSuffix                        string
}

type MermaidConfig struct {
	Theme        string
	Width, Scale int
	ImageType    string
}

// RenderConfig is a value: copying it produces an independent configuration.
type RenderConfig struct {
	Symbols        Symbols
	Mermaid        MermaidConfig
	CiteExpandable bool
}

func DefaultConfig() RenderConfig {
	return RenderConfig{
		Symbols: Symbols{Headings: [6]string{"📌", "✏", "📚", "🔖", "", ""}, Image: "🖼", Link: "🔗", TaskCompleted: "✅", TaskUncompleted: "☑", HorizontalRule: "————————", UnorderedListItem: "⦁", OrderedListSuffix: "."},
		Mermaid: MermaidConfig{Theme: "default", Width: 1000, Scale: 2, ImageType: "webp"}, CiteExpandable: true,
	}
}

// ConvertOptions uses defaults when nil. DisableLatexEscape affects only \(...\)
// and \[...\] preprocessing; dollar-delimited math is always recognized.
type ConvertOptions struct {
	Config             *RenderConfig
	DisableLatexEscape bool
}

func renderConfig(config *RenderConfig) RenderConfig {
	if config != nil {
		return *config
	}
	return DefaultConfig()
}
