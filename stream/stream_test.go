package stream

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	telegramify "github.com/discapes/telegramify-md-go"
)

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("callback timed out")
		var zero T
		return zero
	}
}
func TestFeedDuringEmitAndFinalize(t *testing.T) {
	started := make(chan string, 4)
	release := make(chan struct{})
	final := make(chan string, 1)
	var calls atomic.Int32
	core, err := NewCore(context.Background(), CoreOptions[string]{Interval: 5 * time.Millisecond, KeepaliveTimeout: time.Second,
		Render: func(s string) (string, error) { return s, nil },
		Emit: func(ctx context.Context, s string) error {
			started <- s
			if calls.Add(1) == 1 {
				<-release
			}
			return nil
		},
		Finalize: func(ctx context.Context, s string) error { final <- s; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Cancel()
	if err = core.Feed("first"); err != nil {
		t.Fatal(err)
	}
	if got := receive(t, started); got != "first" {
		t.Fatal(got)
	}
	if err = core.Feed(" second"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if got := receive(t, started); got != "first second" {
		t.Fatalf("tokens received during emit were lost: %q", got)
	}
	if err = core.Finish(); err != nil {
		t.Fatal(err)
	}
	if got := receive(t, final); got != "first second" {
		t.Fatal(got)
	}
	if !errors.Is(core.Feed("late"), ErrDone) || core.State() != "done" {
		t.Fatal("stream accepted feed after finish")
	}
	if err = core.Finish(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-final:
		t.Fatal("finalized twice")
	default:
	}
}
func TestKeepaliveAndDegradedFinalization(t *testing.T) {
	t.Run("keepalive", func(t *testing.T) {
		emissions := make(chan string, 4)
		core, err := NewCore(context.Background(), CoreOptions[string]{Interval: 5 * time.Millisecond, KeepaliveTimeout: 10 * time.Millisecond, Render: func(s string) (string, error) { return s, nil }, Emit: func(ctx context.Context, s string) error { emissions <- s; return nil }, Finalize: func(context.Context, string) error { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		defer core.Cancel()
		core.Feed("same")
		if receive(t, emissions) != "same" || receive(t, emissions) != "same" {
			t.Fatal("keepalive changed content")
		}
	})
	t.Run("degraded", func(t *testing.T) {
		var calls atomic.Int32
		final := make(chan string, 1)
		failure := errors.New("network")
		core, err := NewCore(context.Background(), CoreOptions[string]{Interval: 2 * time.Millisecond, KeepaliveTimeout: time.Second, Render: func(s string) (string, error) { return s, nil }, Emit: func(context.Context, string) error { calls.Add(1); return failure }, Finalize: func(ctx context.Context, s string) error { final <- s; return nil }})
		if err != nil {
			t.Fatal(err)
		}
		defer core.Cancel()
		core.Feed("begin")
		deadline := time.Now().Add(time.Second)
		for !core.Degraded() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if !core.Degraded() || !errors.Is(core.LastError(), failure) {
			t.Fatal("did not enter degraded mode")
		}
		core.Feed(" end")
		if err = core.Finish(); err != nil {
			t.Fatal(err)
		}
		if receive(t, final) != "begin end" || calls.Load() != 3 {
			t.Fatal("degraded mode lost final content or continued emitting")
		}
	})
}
func TestCancellationWaitsForEmitBeforeCleanup(t *testing.T) {
	entered := make(chan struct{})
	events := make(chan string, 3)
	core, err := NewCore(context.Background(), CoreOptions[string]{Interval: time.Millisecond, KeepaliveTimeout: time.Second,
		Render: func(s string) (string, error) { return s, nil }, Emit: func(ctx context.Context, s string) error {
			close(entered)
			<-ctx.Done()
			events <- "emit stopped"
			return ctx.Err()
		},
		Finalize: func(context.Context, string) error { events <- "final"; return nil }, OnCancel: func(ctx context.Context) error {
			if ctx.Err() != nil {
				t.Error("cleanup context canceled")
			}
			events <- "clear"
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	core.Feed("x")
	receive(t, entered)
	if err = core.Cancel(); err != nil {
		t.Fatal(err)
	}
	if receive(t, events) != "emit stopped" || receive(t, events) != "clear" {
		t.Fatal("callbacks overlapped")
	}
	select {
	case e := <-events:
		t.Fatal(e)
	default:
	}
}
func TestConcurrentFeedAndFinish(t *testing.T) {
	var finals atomic.Int32
	var got string
	core, err := NewCore(context.Background(), CoreOptions[string]{Interval: time.Second, KeepaliveTimeout: time.Second, Render: func(s string) (string, error) { return s, nil }, Emit: func(context.Context, string) error { return nil }, Finalize: func(ctx context.Context, s string) error { got = s; finals.Add(1); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	var feeders sync.WaitGroup
	for i := 0; i < 8; i++ {
		feeders.Add(1)
		go func() {
			defer feeders.Done()
			for j := 0; j < 100; j++ {
				if err := core.Feed("x"); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	feeders.Wait()
	for i := 0; i < 8; i++ {
		feeders.Add(1)
		go func() {
			defer feeders.Done()
			if err := core.Finish(); err != nil {
				t.Error(err)
			}
		}()
	}
	feeders.Wait()
	if len(got) != 800 || finals.Load() != 1 {
		t.Fatalf("length %d, final calls %d", len(got), finals.Load())
	}
}
func TestDraftThinkingTailAndFullFinal(t *testing.T) {
	drafts := make(chan Payload, 8)
	finals := make(chan Payload, 1)
	delay := 5 * time.Millisecond
	options := DraftOptions{Options: Options{Mode: EntityMode, Interval: 2 * time.Millisecond}, DraftID: 123, ThinkingDelay: &delay}
	stream, err := NewDraftStream(context.Background(), func(ctx context.Context, p Payload) error { drafts <- p; return nil }, func(ctx context.Context, p Payload) error { finals <- p; return nil }, &options)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Cancel()
	input := strings.Repeat("😀", 2500)
	stream.Feed("**" + input + "**")
	first := receive(t, drafts)
	if first.Text != "" || first.DraftID != 123 {
		t.Fatalf("thinking: %+v", first)
	}
	data, _ := json.Marshal(first)
	if !strings.Contains(string(data), `"text":""`) || !strings.Contains(string(data), `"entities":[]`) {
		t.Fatalf("invalid empty draft JSON: %s", data)
	}
	next := receive(t, drafts)
	if telegramify.UTF16Len(next.Text) != 4096 || len(next.Entities) != 0 {
		t.Fatal("draft exceeded UTF-16 window")
	}
	if err = stream.Finish(); err != nil {
		t.Fatal(err)
	}
	final := receive(t, finals)
	if final.Text != input || len(final.Entities) != 1 || final.DraftID != 0 {
		t.Fatal("final was truncated")
	}
}
func TestDraftFinishDuringThinking(t *testing.T) {
	drafts := make(chan Payload, 3)
	finals := make(chan Payload, 1)
	delay := time.Hour
	stream, err := NewDraftStream(context.Background(), func(ctx context.Context, p Payload) error { drafts <- p; return nil }, func(ctx context.Context, p Payload) error { finals <- p; return nil }, &DraftOptions{Options: Options{Mode: EntityMode, Interval: time.Millisecond}, ThinkingDelay: &delay})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Cancel()
	stream.Feed("hello")
	receive(t, drafts)
	done := make(chan error, 1)
	go func() { done <- stream.Finish() }()
	if err = receive(t, done); err != nil {
		t.Fatal(err)
	}
	if receive(t, finals).Text != "hello" {
		t.Fatal("missing final")
	}
}
func TestEditSendsThenEditsWithConfig(t *testing.T) {
	sent := make(chan Payload, 1)
	edited := make(chan Payload, 1)
	config := telegramify.DefaultConfig()
	config.Symbols.UnorderedListItem = "-"
	stream, err := NewEditStream(context.Background(), func(ctx context.Context, p Payload) (int64, error) { sent <- p; return 42, nil }, func(ctx context.Context, id int64, p Payload) error {
		if id != 42 {
			t.Errorf("message ID %d", id)
		}
		edited <- p
		return nil
	}, &Options{Mode: EntityMode, Config: &config})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Cancel()
	stream.Feed("- a")
	if receive(t, sent).Text != "- a\n" {
		t.Fatal("config ignored")
	}
	stream.Feed("\n- b")
	if err = stream.Finish(); err != nil {
		t.Fatal(err)
	}
	if receive(t, edited).Text != "- a\n- b\n" {
		t.Fatal("edit incomplete")
	}
	id, ok := stream.MessageID()
	if !ok || id != 42 {
		t.Fatal("message ID unavailable")
	}
}
