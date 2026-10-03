package telegramify_test

import (
	"context"
	"encoding/json"
	"fmt"

	telegramify "github.com/discapes/telegramify-md-go"
)

func ExampleConvert() {
	text, entities := telegramify.Convert("Hello **world** 😀", nil)
	data, _ := json.Marshal(entities)
	fmt.Println(text)
	fmt.Println(string(data))
	// Output:
	// Hello world 😀
	// [{"type":"bold","offset":6,"length":5}]
}
func ExampleTelegramify() {
	options := telegramify.DefaultTelegramifyOptions()
	options.DisableMermaid = true
	parts, err := telegramify.Telegramify(context.Background(), "Intro\n\n```go\nfmt.Println(1)\n```", &options)
	if err != nil {
		panic(err)
	}
	for _, part := range parts {
		fmt.Println(part.Type())
	}
	// Output:
	// text
	// file
}
