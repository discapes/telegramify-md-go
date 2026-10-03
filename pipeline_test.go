package telegramify

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPipelineOrderingAndClipping(t *testing.T) {
	options := DefaultTelegramifyOptions()
	options.MaxMessageLength = 8
	options.DisableMermaid = true
	parts, err := Telegramify(context.Background(), "**😀abcdefghij**\n\n```python\n# app.py\n\nprint('hi')\n```\n\nAfter", &options)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 4 {
		t.Fatalf("got %+v", parts)
	}
	first := parts[0].(Text)
	second := parts[1].(Text)
	file := parts[2].(File)
	last := parts[3].(Text)
	if first.Text != "😀abcdef" || second.Text != "ghij" || last.Text != "After" {
		t.Fatalf("wrong order or split: %+v", parts)
	}
	if first.Entities[0].Length != 8 || second.Entities[0].Offset != 0 || second.Entities[0].Length != 4 {
		t.Fatal("bold entity was not clipped")
	}
	if file.FileName != "app.py" || string(file.FileData) != "# app.py\n\nprint('hi')" {
		t.Fatalf("wrong file: %+v", file)
	}
	options.MinFileLines = 0
	options.MaxMessageLength = 4096
	parts, err = Telegramify(context.Background(), "```go\nfmt.Println(1)\n```", &options)
	if err != nil || len(parts) != 1 || parts[0].(Text).Entities[0].Type != "pre" {
		t.Fatalf("inline code: %+v, %v", parts, err)
	}
}

func TestMermaidSuccessFailureAndCancellation(t *testing.T) {
	options := DefaultTelegramifyOptions()
	options.RenderMermaid = func(ctx context.Context, source string, c RenderConfig) ([]byte, error) {
		if source != "graph TD\nA-->B" {
			t.Errorf("wrong source %q", source)
		}
		return []byte("image"), nil
	}
	parts, err := Telegramify(context.Background(), "Before\n\n```mermaid\ngraph TD\nA-->B\n```\n\nAfter", &options)
	if err != nil || len(parts) != 3 {
		t.Fatalf("%+v %v", parts, err)
	}
	photo := parts[1].(Photo)
	if photo.CaptionText != "Edit on mermaid.live" || len(photo.CaptionEntities) != 1 || photo.CaptionEntities[0].Length != UTF16Len(photo.CaptionText) {
		t.Fatalf("wrong photo caption: %+v", photo)
	}
	options.RenderMermaid = func(context.Context, string, RenderConfig) ([]byte, error) { return nil, errors.New("render failed") }
	parts, err = Telegramify(context.Background(), "```mermaid\ngraph TD\n```", &options)
	if err != nil || parts[0].(File).FileName != "invalid_mermaid.txt" {
		t.Fatalf("fallback: %+v %v", parts, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Telegramify(ctx, "hello", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestMermaidPakoAndDownload(t *testing.T) {
	config := DefaultConfig()
	config.Mermaid.Theme = "dark"
	source := "graph TD\nA[😀]-->B"
	packed := strings.TrimPrefix(GeneratePako(source, &config), "pako:")
	compressed, err := base64.URLEncoding.DecodeString(packed)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Code    string
		Mermaid struct{ Theme string }
	}
	if err = json.Unmarshal(data, &payload); err != nil || payload.Code != source || payload.Mermaid.Theme != "dark" {
		t.Fatalf("%s %v", data, err)
	}
	var pngData bytes.Buffer
	if err = png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Write(pngData.Bytes())
		case "/bad":
			w.Write([]byte("not an image"))
		default:
			w.WriteHeader(503)
		}
	}))
	defer server.Close()
	for _, path := range []string{"/ok", "/bad", "/error"} {
		data, err := DownloadImage(context.Background(), server.URL+path, server.Client())
		if path == "/ok" {
			if err != nil || !bytes.Equal(data, pngData.Bytes()) {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatalf("expected error for %s", path)
		}
	}
}
