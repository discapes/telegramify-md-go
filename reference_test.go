package telegramify

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func TestPythonReference(t *testing.T) {
	data, err := os.ReadFile("testdata/reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		SplitRich []struct {
			Message InputRichMessage
			Options struct {
				ByteLimit  int `json:"byte_limit"`
				BlockLimit int `json:"block_limit"`
			}
			Result []InputRichMessage
		} `json:"split_rich"`

		Convert []struct {
			Markdown, Text string
			Entities       []MessageEntity
			DisableLatex   bool `json:"disable_latex"`
			Symbols        map[string]string
			Expandable     bool
		}
		Latex []struct{ Source, Result string }
		MDV2  []struct {
			Text, Result string
			Entities     []MessageEntity
		}
		Rich []struct {
			Markdown string
			Options  struct {
				Mode                string
				IsRTL               *bool `json:"is_rtl"`
				SkipEntityDetection *bool `json:"skip_entity_detection"`
				LatexEscape         bool  `json:"latex_escape"`
			}
			Result InputRichMessage
		}
	}
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for i, f := range fixtures.Convert {
		t.Run(fmt.Sprintf("convert/%03d", i), func(t *testing.T) {
			c := DefaultConfig()
			s := f.Symbols
			for i := range c.Symbols.Headings {
				c.Symbols.Headings[i] = s[fmt.Sprintf("heading_level_%d", i+1)]
			}
			c.Symbols.Image = s["image"]
			c.Symbols.TaskCompleted = s["task_completed"]
			c.Symbols.TaskUncompleted = s["task_uncompleted"]
			c.Symbols.HorizontalRule = s["horizontal_rule"]
			c.Symbols.UnorderedListItem = s["unordered_list_item"]
			c.Symbols.OrderedListSuffix = s["ordered_list_suffix"]
			c.CiteExpandable = f.Expandable
			text, entities := Convert(f.Markdown, &ConvertOptions{Config: &c, DisableLatexEscape: f.DisableLatex})
			// Python opens custom_emoji for this link but closes text_link, losing
			// the entity. Go closes the link's actual entity type.
			if f.Markdown == "[😀](tg://emoji?id=5368324170671202286)" {
				f.Entities = []MessageEntity{{Type: "custom_emoji", Length: 2, CustomEmojiID: "5368324170671202286"}}
			}
			if text != f.Text || !reflect.DeepEqual(entities, f.Entities) {
				t.Errorf("input %q\ntext got %q want %q\nentities got %+v\nwant %+v", f.Markdown, text, f.Text, entities, f.Entities)
			}
		})
	}
	for i, f := range fixtures.Latex {
		t.Run(fmt.Sprintf("latex/%03d", i), func(t *testing.T) {
			if got := LatexToUnicode(f.Source); got != f.Result {
				t.Errorf("%q: got %q want %q", f.Source, got, f.Result)
			}
		})
	}
	for i, f := range fixtures.MDV2 {
		t.Run(fmt.Sprintf("mdv2/%03d", i), func(t *testing.T) {
			if got := EntitiesToMarkdownV2(f.Text, f.Entities); got != f.Result {
				t.Errorf("got %q want %q", got, f.Result)
			}
		})
	}
	for i, f := range fixtures.Rich {
		t.Run(fmt.Sprintf("rich/%03d", i), func(t *testing.T) {
			got, err := Richify(f.Markdown, &RichOptions{Mode: f.Options.Mode, IsRTL: f.Options.IsRTL, SkipEntityDetection: f.Options.SkipEntityDetection, LatexEscape: f.Options.LatexEscape})
			if err != nil || !reflect.DeepEqual(got, f.Result) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(f.Result)
				t.Errorf("input %q\ngot %s\nwant %s (err %v)", f.Markdown, a, b, err)
			}
		})
	}
	for i, f := range fixtures.SplitRich {
		t.Run(fmt.Sprintf("split_rich/%03d", i), func(t *testing.T) {
			options := RichSplitOptions{RichByteLimit, RichBlockLimit}
			if f.Options.ByteLimit != 0 {
				options.ByteLimit = f.Options.ByteLimit
			}
			if f.Options.BlockLimit != 0 {
				options.BlockLimit = f.Options.BlockLimit
			}
			got, err := SplitRich(f.Message, &options)
			if err != nil || !reflect.DeepEqual(got, f.Result) {
				t.Errorf("split differs: got %d chunks, want %d; error %v", len(got), len(f.Result), err)
				for i := 0; i < min(len(got), len(f.Result)); i++ {
					if !reflect.DeepEqual(got[i], f.Result[i]) {
						a, _ := json.Marshal(got[i])
						b, _ := json.Marshal(f.Result[i])
						t.Errorf("chunk %d: got %.400s want %.400s", i, a, b)
						break
					}
				}
			}
		})
	}

}
