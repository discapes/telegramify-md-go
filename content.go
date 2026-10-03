package telegramify

// Content is one sendable item from Telegramify or TelegramifyRich.
// Use a type switch to select sendMessage, sendDocument, sendPhoto or sendRichMessage.
type Content interface{ Type() ContentType }
type ContentType string

const (
	ContentText  ContentType = "text"
	ContentFile  ContentType = "file"
	ContentPhoto ContentType = "photo"
	ContentRich  ContentType = "rich"
)

type ContentTrace struct {
	SourceType string         `json:"source_type"`
	Extra      map[string]any `json:"extra,omitempty"`
}
type Text struct {
	Text         string          `json:"text"`
	Entities     []MessageEntity `json:"entities"`
	ContentTrace ContentTrace    `json:"content_trace"`
}

func (Text) Type() ContentType { return ContentText }

type File struct {
	FileName        string          `json:"file_name"`
	FileData        []byte          `json:"file_data"`
	ContentTrace    ContentTrace    `json:"content_trace"`
	CaptionText     string          `json:"caption_text,omitempty"`
	CaptionEntities []MessageEntity `json:"caption_entities,omitempty"`
}

func (File) Type() ContentType { return ContentFile }

type Photo struct {
	FileName        string          `json:"file_name"`
	FileData        []byte          `json:"file_data"`
	ContentTrace    ContentTrace    `json:"content_trace"`
	CaptionText     string          `json:"caption_text,omitempty"`
	CaptionEntities []MessageEntity `json:"caption_entities,omitempty"`
}

func (Photo) Type() ContentType { return ContentPhoto }

type RichMessage struct {
	RichMessage  InputRichMessage `json:"rich_message"`
	ContentTrace ContentTrace     `json:"content_trace"`
}

func (RichMessage) Type() ContentType { return ContentRich }
