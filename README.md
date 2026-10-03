# telegramify-md-go

A native Go port of [sudoskys/telegramify-markdown](https://github.com/sudoskys/telegramify-markdown), based on version 1.4.0. Requires Go 1.24 or newer. The package name is `telegramify`.

Goldmark parses CommonMark, tables, task lists and footnotes. Telegram-specific rendering, spoilers, math, UTF-16 offsets, message splitting, Mermaid handling and streaming are implemented in Go. There is no Python runtime dependency.

```go
import telegramify "github.com/discapes/telegramify-md-go"

text, entities := telegramify.Convert("Hello **world** 😀", nil)
// text: "Hello world 😀"
// entities: [{Type:"bold", Offset:6, Length:5}]
```

Pass `text` and `entities` to your bot SDK's message API. Entity structs have Telegram-compatible JSON tags. No parse mode is needed. For middleware requiring MarkdownV2:

```go
markdown := telegramify.Markdownify("Hello **world**!", nil)
// Hello *world*\!
```

## API

| Python | Go |
| --- | --- |
| `convert` | `Convert(markdown, options)` → text, entities |
| `convert_with_segments` | `ConvertWithSegments(markdown, options)` → text, entities, segments |
| `markdownify`, `standardize` | `Markdownify`, `Standardize` |
| `entities_to_markdownv2` | `EntitiesToMarkdownV2(text, entities)` |
| `utf16_len`, `count_text` | `UTF16Len`, `CountText` |
| `split_entities` | `SplitEntities(text, entities, limit)` → chunks, error |
| `split_markdownv2` | `SplitMarkdownV2(text, entities, limit)` → strings, error |
| `telegramify` | `Telegramify(ctx, markdown, options)` → content, error |
| `richify` | `Richify(markdown, options)` → payload, error |
| `split_rich` | `SplitRich(payload, options)` → payloads, error |
| `telegramify_rich` | `TelegramifyRich(markdown, options)` → rich messages, error |
| `LatexToUnicodeHelper.convert` | `LatexToUnicode(latex)` |
| `get_filename`, `get_ext` | `GetFilename`, `GetExt` |
| Mermaid URL/render helpers | `GeneratePako`, `MermaidLiveURL`, `MermaidInkURL`, `RenderMermaid` |
| `StreamCore`, `DraftStream`, `EditStream` | `stream.NewCore`, `stream.NewDraftStream`, `stream.NewEditStream` |

`nil` options select defaults. `DefaultConfig()` returns an independent value; there is no mutable global configuration. Start with that value when customizing symbols:

```go
config := telegramify.DefaultConfig()
config.Symbols.UnorderedListItem = "-"
config.CiteExpandable = false
text, entities := telegramify.Convert(markdown, &telegramify.ConvertOptions{
    Config: &config,
})
```

The default heading symbols and styles match Python. H1–H2 use bold and underline, H3–H4 use bold, and H5–H6 use italic. Tables become fixed-width `pre` entities. Code regions preserve literal spoiler delimiters. LaTeX conversion supports the original symbol/style tables, fractions, roots, scripts, accents and environments.

`DisableLatexEscape` disables preprocessing of `\(...\)` and `\[...\]`. Dollar-delimited math remains supported, matching Python. Rich rendering retains LaTeX in `<tg-math>` and `<tg-math-block>` by default.

## Content pipeline

```go
options := telegramify.DefaultTelegramifyOptions()
options.MaxMessageLength = 4096
options.MinFileLines = 3
options.DisableMermaid = true

parts, err := telegramify.Telegramify(ctx, markdown, &options)
if err != nil {
    return err
}
for _, part := range parts {
    switch item := part.(type) {
    case telegramify.Text:
        // sendMessage: item.Text, item.Entities
    case telegramify.File:
        // sendDocument: item.FileName, item.FileData
    case telegramify.Photo:
        // sendPhoto: item.FileData, item.CaptionText, item.CaptionEntities
    }
}
```

The default pipeline extracts code blocks with at least one line. Set `MinFileLines` to zero to keep code inline. `DefaultTelegramifyOptions()` makes those defaults explicit; a struct literal with `MinFileLines: 0` disables extraction.

Mermaid rendering defaults to the public **mermaid.ink** service and sends diagram source to that service. Disable it with `DisableMermaid`, or set the `RenderMermaid` callback for a local renderer. HTTP requests accept cancellation and use a ten-second timeout by default. Rendering failures produce `invalid_mermaid.txt`; cancellation returns the context error. Successful photos carry an “Edit on mermaid.live” caption link. Unit tests use a local HTTP server and injected renderers.

## Rich messages

```go
payload, err := telegramify.Richify(markdown, nil)
if err != nil {
    return err
}
parts, err := telegramify.SplitRich(payload, nil)
// Each InputRichMessage serializes to {"html":"..."}.
```

`RichOptions{Mode: "markdown"}` passes through Markdown. `IsRTL` and `SkipEntityDetection` are optional `*bool` fields. Exactly one of `InputRichMessage.HTML` and `.Markdown` must be set, including for empty bodies; validation runs when splitting or JSON-encoding.

The defaults are 32,768 UTF-8 bytes and 500 top-level blocks. Splitting closes and reopens inline tags, preserves escaped characters, and continues ordered-list numbering. As in Python, unsplittable atomic blocks (for example, a single oversized table row) are returned alone and logged with `slog.Warn`; inspect their size if strict rejection is required before sending.

## Streaming

The `stream` subpackage uses callbacks, so it works with any Telegram SDK. Each stream owns one goroutine. `Feed` is concurrency-safe; callbacks run serially. Call `Finish`, `Cancel`, or cancel the construction context to release the worker. Callbacks must honor their context and must not synchronously call `Finish` or `Cancel` on their own stream.

```go
import "github.com/discapes/telegramify-md-go/stream"

s, err := stream.NewDraftStream(ctx, sendDraft, sendFinal, &stream.DraftOptions{
    Options: stream.Options{Mode: stream.EntityMode},
    DraftID: 123,
})
if err != nil {
    return err
}
defer s.Cancel()

for token := range tokens {
    if err := s.Feed(token); err != nil {
        return err
    }
}
return s.Finish()
```

`sendDraft` and `sendFinal` have signature `func(context.Context, stream.Payload) error`. Draft payloads include `DraftID`; final payloads omit it. `Consume(ctx, tokens)` can consume a `<-chan string` and finish automatically.

Defaults: rich mode, a 300ms draft interval, a 500ms thinking delay, and a 25s keepalive. Entity drafts retain the final 4,096 UTF-16 units, dropping entities when truncated. Rich drafts retain the last chunk from `SplitRich`. Finalization sends the complete buffer as one payload; use the content pipelines in your callback if final messages require splitting. Three consecutive emission failures disable interim emissions; finalization still runs. Inspect `Degraded()` and `LastError()` for delivery failures.

`NewEditStream` takes send and edit callbacks (`func(context.Context, Payload) (int64, error)` and `func(context.Context, int64, Payload) error`). It sends once and edits the returned message ID thereafter. Its minimum interval is one second. `MessageID()` reports the ID once sent. Both strategies copy their supplied render configuration when constructed.

## Compatibility and validation

The port is tested against captured Python outputs, including the upstream repository's Markdown showcase documents. Intentional Go differences:

- Configuration is passed by value/pointer per request. Deprecated Python 0.x parameters and dynamic compatibility properties are omitted.
- Segment text positions are UTF-8 **byte offsets**; entity positions remain UTF-16 units.
- Invalid limits and budgets too small for a Unicode rune return errors instead of exceeding the limit.
- Custom emoji links correctly close their entity. Python 1.4.0 loses that entity for `[emoji](tg://emoji?id=...)`; image syntax already works in both versions.
- Rich drafts split at HTML boundaries; Python truncates raw HTML bytes. Unsplittable atomic blocks retain the rich splitter's behavior described above.
- Mermaid Pako strings can differ because JSON and compression encodings differ; their decoded diagram and theme are equivalent.

Goldmark and Python's pulldown-cmark parser can differ on obscure or malformed Markdown. Live Telegram delivery is not part of the offline test suite.

```sh
cd telegramify-md-go
go test -race ./...
go vet ./...
go test -run '^$' -fuzz FuzzConversionAndSplitting -fuzztime=20s -parallel=2
```

Python is needed only to regenerate reference fixtures and the symbol tables. From this repository root, use a Python environment with the upstream package installed from a checkout of version 1.4.0. Pass that checkout to the fixture generator:

```sh
python scripts/generate_fixtures.py /path/to/telegramify-markdown
python scripts/generate_latex.py
gofmt -w latex_tables.go
```

Licensed under MIT; the upstream copyright notice is preserved in [LICENSE](LICENSE).
