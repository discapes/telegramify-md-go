package telegramify

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	_ "golang.org/x/image/webp"
)

// GeneratePako encodes diagram source and theme for mermaid.live and mermaid.ink.
func GeneratePako(diagram string, config *RenderConfig) string {
	c := renderConfig(config)
	data, _ := json.Marshal(struct {
		Code    string `json:"code"`
		Mermaid struct {
			Theme string `json:"theme"`
		} `json:"mermaid"`
	}{Code: diagram, Mermaid: struct {
		Theme string `json:"theme"`
	}{c.Mermaid.Theme}})
	var b bytes.Buffer
	w, _ := zlib.NewWriterLevel(&b, zlib.BestCompression)
	_, _ = w.Write(data)
	_ = w.Close()
	return "pako:" + base64.URLEncoding.EncodeToString(b.Bytes())
}
func MermaidLiveURL(diagram string, config *RenderConfig) string {
	return "https://mermaid.live/edit/#" + GeneratePako(diagram, config)
}
func MermaidInkURL(diagram string, config *RenderConfig) string {
	c := renderConfig(config)
	q := url.Values{"theme": {c.Mermaid.Theme}, "width": {strconv.Itoa(c.Mermaid.Width)}, "scale": {strconv.Itoa(c.Mermaid.Scale)}, "type": {c.Mermaid.ImageType}}
	return "https://mermaid.ink/img/" + GeneratePako(diagram, config) + "?" + q.Encode()
}

// RenderMermaid downloads and validates an image from mermaid.ink. A nil client
// uses a ten-second timeout. Cancellation always propagates through the request.
func RenderMermaid(ctx context.Context, diagram string, client *http.Client, config *RenderConfig) ([]byte, error) {
	return DownloadImage(ctx, MermaidInkURL(diagram, config), client)
}
func DownloadImage(ctx context.Context, url string, client *http.Client) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "telegramify-markdown-go")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("image download: HTTP %d", res.StatusCode)
	}
	const maxImageBytes = 32 << 20
	data, err := io.ReadAll(io.LimitReader(res.Body, maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("image exceeds %d bytes", maxImageBytes)
	}
	if !IsImage(data) {
		return nil, fmt.Errorf("response is not a supported image")
	}
	return data, nil
}
func IsImage(data []byte) bool {
	_, _, err := image.DecodeConfig(bytes.NewReader(data))
	return err == nil
}
