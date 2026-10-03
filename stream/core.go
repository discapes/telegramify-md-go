// Package stream renders a growing Markdown buffer through caller-provided
// Telegram callbacks. It has no dependency on a bot SDK.
package stream

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var errPending = errors.New("emission pending")

var ErrDone = errors.New("cannot feed after stream is done")

type CoreOptions[T any] struct {
	Render           func(string) (T, error)
	Emit             func(context.Context, T) error
	Finalize         func(context.Context, T) error
	OnCancel         func(context.Context) error
	Interval         time.Duration
	KeepaliveTimeout time.Duration
}

// Core serializes callbacks in one goroutine. Feed is safe during a callback;
// new tokens remain dirty until a subsequent emission. Call Finish or Cancel
// to release the worker, or cancel the context supplied to NewCore.
type Core[T any] struct {
	mu                             sync.Mutex
	buffer                         strings.Builder
	version, emitted               uint64
	state                          string
	stopping, cancelling, degraded bool
	lastErr, finalErr              error
	opts                           CoreOptions[T]
	ctx                            context.Context
	cancel                         context.CancelFunc
	stop, done                     chan struct{}
}

func NewCore[T any](ctx context.Context, options CoreOptions[T]) (*Core[T], error) {
	if options.Interval <= 0 {
		return nil, fmt.Errorf("stream interval must be positive")
	}
	if options.KeepaliveTimeout <= 0 {
		return nil, fmt.Errorf("keepalive timeout must be positive")
	}
	if options.Render == nil || options.Emit == nil || options.Finalize == nil {
		return nil, fmt.Errorf("render, emit and finalize callbacks are required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	c := &Core[T]{opts: options, ctx: runCtx, cancel: cancel, state: "idle", stop: make(chan struct{}), done: make(chan struct{})}
	go c.run()
	return c, nil
}
func (c *Core[T]) Feed(token string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopping || c.state == "done" || c.ctx.Err() != nil {
		return ErrDone
	}
	c.buffer.WriteString(token)
	c.version++
	c.state = "active"
	return nil
}
func (c *Core[T]) Buffer() string   { c.mu.Lock(); defer c.mu.Unlock(); return c.buffer.String() }
func (c *Core[T]) State() string    { c.mu.Lock(); defer c.mu.Unlock(); return c.state }
func (c *Core[T]) Degraded() bool   { c.mu.Lock(); defer c.mu.Unlock(); return c.degraded }
func (c *Core[T]) LastError() error { c.mu.Lock(); defer c.mu.Unlock(); return c.lastErr }
func (c *Core[T]) Finish() error    { return c.shutdown(false) }
func (c *Core[T]) Cancel() error    { return c.shutdown(true) }
func (c *Core[T]) shutdown(cancel bool) error {
	c.mu.Lock()
	if !c.stopping && c.state != "done" {
		c.stopping = true
		c.cancelling = cancel
		if cancel {
			c.cancel()
		}
		close(c.stop)
	}
	c.mu.Unlock()
	<-c.done
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.finalErr
}

// Consume finishes when tokens closes and cancels if the supplied context ends.
func (c *Core[T]) Consume(ctx context.Context, tokens <-chan string) error {
	for {
		select {
		case <-ctx.Done():
			_ = c.Cancel()
			return ctx.Err()
		case <-c.done:
			c.mu.Lock()
			err := c.finalErr
			c.mu.Unlock()
			if err != nil {
				return err
			}
			return ErrDone
		case token, ok := <-tokens:
			if !ok {
				return c.Finish()
			}
			if err := c.Feed(token); err != nil {
				return err
			}
		}
	}
}
func (c *Core[T]) run() {
	timer := time.NewTimer(c.opts.Interval)
	defer timer.Stop()
	defer c.cancel()
	lastSent := time.Time{}
	failures := 0
	for {
		select {
		case <-c.ctx.Done():
			c.complete(true)
			return
		case <-c.stop:
			c.mu.Lock()
			cancel := c.cancelling
			c.mu.Unlock()
			c.complete(cancel)
			return
		case <-timer.C:
			c.mu.Lock()
			body, version := c.buffer.String(), c.version
			due := c.state == "active" && !c.stopping && !c.degraded && (version != c.emitted || !lastSent.IsZero() && time.Since(lastSent) >= c.opts.KeepaliveTimeout)
			c.mu.Unlock()
			if due {
				payload, err := c.opts.Render(body)
				if err == nil {
					err = c.opts.Emit(c.ctx, payload)
				}
				c.mu.Lock()
				if errors.Is(err, errPending) {
					// The strategy will retry this version on the next tick.
				} else if err != nil {
					failures++
					c.lastErr = err
					if failures >= 3 {
						c.degraded = true
					}
				} else {
					failures = 0
					c.emitted = version
					lastSent = time.Now()
				}
				c.mu.Unlock()
			}
			timer.Reset(c.opts.Interval)
		}
	}
}
func (c *Core[T]) complete(cancel bool) {
	c.mu.Lock()
	c.stopping = true
	body := c.buffer.String()
	c.mu.Unlock()
	var err error
	if cancel {
		if c.opts.OnCancel != nil {
			// Clearing the remote draft needs a usable context after cancellation.
			cleanup, cancelCleanup := context.WithTimeout(context.WithoutCancel(c.ctx), 10*time.Second)
			err = c.opts.OnCancel(cleanup)
			cancelCleanup()
		}
		c.mu.Lock()
		manual := c.cancelling
		c.mu.Unlock()
		if err == nil && !manual {
			err = c.ctx.Err()
		}
	} else if body != "" {
		var payload T
		payload, err = c.opts.Render(body)
		if err == nil {
			err = c.opts.Finalize(c.ctx, payload)
		}
	}
	c.mu.Lock()
	c.state = "done"
	c.finalErr = err
	c.mu.Unlock()
	close(c.done)
}
