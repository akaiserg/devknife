package core

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"
)

// ErrBusy is returned when every run slot stayed occupied until the deadline.
var ErrBusy = errors.New("devknife is busy with other runs; try again in a moment")

// TimeoutError reports a run that did not finish before its deadline.
type TimeoutError struct {
	Tool    string
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("%s did not finish within %s; try a smaller input, or run it from the terminal (devknife %s), which has no time limit", e.Tool, e.Timeout, e.Tool)
}

// Runner runs tools for frontends that serve untrusted or interactive input
// (web UI, MCP): every run gets a deadline, and at most N run at once.
//
// Go cannot stop a running function, so a run that misses its deadline keeps
// going in the background until it returns, and keeps holding its slot. That
// is deliberate: the slot count then bounds the real CPU and memory in use,
// not just the number of requests waiting for an answer. Tools keep their own
// work bounded (input caps, cost caps, diff timeout) so slots always come back.
type Runner struct {
	Timeout time.Duration // 0 = no deadline
	slots   chan struct{}
}

// NewRunner returns a runner with the given deadline per run and at most
// maxConcurrent runs at a time (<= 0 means GOMAXPROCS).
func NewRunner(timeout time.Duration, maxConcurrent int) *Runner {
	if maxConcurrent <= 0 {
		maxConcurrent = runtime.GOMAXPROCS(0)
	}
	return &Runner{Timeout: timeout, slots: make(chan struct{}, maxConcurrent)}
}

// Run executes the tool. It returns early with the context's error when ctx
// is canceled (the client went away), a *TimeoutError when the deadline
// passes, or ErrBusy when no slot freed up in time.
func (r *Runner) Run(ctx context.Context, t *Tool, req Request) (*Result, error) {
	var res *Result
	var err error
	derr := r.Do(ctx, func() { res, err = t.Execute(req) })
	var te *TimeoutError
	if errors.As(derr, &te) {
		te.Tool = t.ID
	}
	if derr != nil {
		return nil, derr
	}
	return res, err
}

// Do runs fn under the runner's deadline and concurrency limit. fn's results
// must only be read when Do returns nil.
func (r *Runner) Do(ctx context.Context, fn func()) error {
	if err := ctx.Err(); err != nil { // select would pick randomly if a slot were free too
		return err
	}
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ErrBusy
		}
		return ctx.Err()
	}
	done := make(chan struct{})
	go func() {
		defer func() { <-r.slots }()
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &TimeoutError{Tool: "the tool", Timeout: r.Timeout}
		}
		return ctx.Err()
	}
}
