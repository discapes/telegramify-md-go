package stream

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"time"
	"unicode/utf8"

	telegramify "github.com/discapes/telegramify-md-go"
)

type Mode string

const (
	RichMode   Mode = "rich"
	EntityMode Mode = "entity"
)

// Payload is either text/entities or a rich message. DraftID is zero for finals.
type Payload struct {
	Text        string                        `json:"text,omitempty"`
	Entities    []telegramify.MessageEntity   `json:"entities,omitempty"`
	RichMessage *telegramify.InputRichMessage `json:"rich_message,omitempty"`
	DraftID     int64                         `json:"draft_id,omitempty"`
}

// MarshalJSON includes an empty entity draft's required text and entities fields.
func (p Payload) MarshalJSON() ([]byte, error) {
	fields := map[string]any{}
	if p.RichMessage != nil {
		fields["rich_message"] = p.RichMessage
	} else {
		fields["text"] = p.Text
		entities := p.Entities
		if entities == nil {
			entities = []telegramify.MessageEntity{}
		}
		fields["entities"] = entities
	}
	if p.DraftID != 0 {
		fields["draft_id"] = p.DraftID
	}
	return json.Marshal(fields)
}

type Options struct {
	Mode                       Mode
	Interval, KeepaliveTimeout time.Duration
	Config                     *telegramify.RenderConfig
}

func normalize(options Options, interval time.Duration) (Options, error) {
	if options.Mode == "" {
		options.Mode = RichMode
	}
	if options.Mode != RichMode && options.Mode != EntityMode {
		return options, fmt.Errorf("mode must be rich or entity")
	}
	if options.Interval == 0 {
		options.Interval = interval
	}
	if options.KeepaliveTimeout == 0 {
		options.KeepaliveTimeout = 25 * time.Second
	}
	if options.Config != nil {
		copy := *options.Config
		options.Config = &copy
	}
	return options, nil
}
func render(buffer string, options Options) (Payload, error) {
	if options.Mode == EntityMode {
		text, entities := telegramify.Convert(buffer, &telegramify.ConvertOptions{Config: options.Config})
		return Payload{Text: text, Entities: entities}, nil
	}
	rich, err := telegramify.Richify(buffer, nil)
	return Payload{RichMessage: &rich}, err
}

type DraftOptions struct {
	Options
	DraftID int64
	// Nil uses 500ms; a pointer to zero disables the thinking indicator.
	ThinkingDelay      *time.Duration
	DisableCancelClear bool
}

type DraftStream struct {
	*Core[Payload]
	DraftID int64
}

func NewDraftStream(ctx context.Context, sendDraft, sendFinal func(context.Context, Payload) error, options *DraftOptions) (*DraftStream, error) {
	if sendDraft == nil || sendFinal == nil {
		return nil, fmt.Errorf("draft and final callbacks are required")
	}
	opts := DraftOptions{}
	if options != nil {
		opts = *options
	}
	var err error
	opts.Options, err = normalize(opts.Options, 300*time.Millisecond)
	if err != nil {
		return nil, err
	}
	if opts.DraftID == 0 {
		var data [8]byte
		if _, err := rand.Read(data[:]); err != nil {
			return nil, err
		}
		opts.DraftID = int64(binary.LittleEndian.Uint64(data[:])&0x7fffffff) | 1
	}
	delay := 500 * time.Millisecond
	if opts.ThinkingDelay != nil {
		delay = *opts.ThinkingDelay
	}
	if delay < 0 {
		return nil, fmt.Errorf("thinking delay cannot be negative")
	}
	empty := func() Payload {
		p := Payload{DraftID: opts.DraftID, Entities: []telegramify.MessageEntity{}}
		if opts.Mode == RichMode {
			s := "<p>…</p>"
			p.RichMessage = &telegramify.InputRichMessage{HTML: &s}
		}
		return p
	}
	first := true
	var thinkingUntil time.Time
	core, err := NewCore(ctx, CoreOptions[Payload]{
		Interval: opts.Interval, KeepaliveTimeout: opts.KeepaliveTimeout,
		Render: func(buffer string) (Payload, error) { return render(buffer, opts.Options) },
		Emit: func(ctx context.Context, p Payload) error {
			if first {
				first = false
				if delay > 0 {
					if err := sendDraft(ctx, empty()); err != nil {
						return err
					}
					thinkingUntil = time.Now().Add(delay)
					return errPending
				}
			}
			if time.Now().Before(thinkingUntil) {
				return errPending
			}
			p.DraftID = opts.DraftID
			if opts.Mode == EntityMode && telegramify.UTF16Len(p.Text) > 4096 {
				p.Text = tailUTF16(p.Text, 4096)
				p.Entities = []telegramify.MessageEntity{}
			}
			if opts.Mode == RichMode && p.RichMessage.HTML != nil && len(*p.RichMessage.HTML) > telegramify.RichByteLimit {
				// Split into well-formed HTML rather than cutting through an open tag.
				parts, err := telegramify.SplitRich(*p.RichMessage, nil)
				if err != nil {
					return err
				}
				if len(parts) > 0 {
					last := parts[len(parts)-1]
					p.RichMessage = &last
				}
			}
			return sendDraft(ctx, p)
		},
		Finalize: sendFinal,
		OnCancel: func(ctx context.Context) error {
			if !opts.DisableCancelClear {
				_ = sendDraft(ctx, empty())
			}
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	return &DraftStream{Core: core, DraftID: opts.DraftID}, nil
}
func tailUTF16(s string, limit int) string {
	size := 0
	for i := len(s); i > 0; {
		r, n := utf8.DecodeLastRuneInString(s[:i])
		units := 1
		if r > 0xffff {
			units = 2
		}
		if size+units > limit {
			return s[i:]
		}
		size += units
		i -= n
	}
	return s
}

type EditStream struct {
	*Core[Payload]
	mu        sync.Mutex
	messageID int64
	sent      bool
}

func (s *EditStream) MessageID() (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.messageID, s.sent
}
func NewEditStream(ctx context.Context, send func(context.Context, Payload) (int64, error), edit func(context.Context, int64, Payload) error, options *Options) (*EditStream, error) {
	if send == nil || edit == nil {
		return nil, fmt.Errorf("send and edit callbacks are required")
	}
	opts := Options{}
	if options != nil {
		opts = *options
	}
	var err error
	opts, err = normalize(opts, time.Second)
	if err != nil {
		return nil, err
	}
	if opts.Interval < time.Second {
		return nil, fmt.Errorf("edit interval must be at least one second")
	}
	s := &EditStream{}
	emit := func(ctx context.Context, p Payload) error {
		id, sent := s.MessageID()
		if sent {
			return edit(ctx, id, p)
		}
		id, err := send(ctx, p)
		if err == nil {
			s.mu.Lock()
			s.messageID = id
			s.sent = true
			s.mu.Unlock()
		}
		return err
	}
	s.Core, err = NewCore(ctx, CoreOptions[Payload]{Interval: opts.Interval, KeepaliveTimeout: opts.KeepaliveTimeout, Render: func(buffer string) (Payload, error) { return render(buffer, opts) }, Emit: emit, Finalize: emit})
	if err != nil {
		return nil, err
	}
	return s, nil
}
