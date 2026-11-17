package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunnerDeadlineSlotsAndCancel(t *testing.T) {
	r := NewRunner(50*time.Millisecond, 1)
	release := make(chan struct{})
	// a run past its deadline returns a TimeoutError, but keeps its slot
	err := r.Do(context.Background(), func() { <-release })
	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("want TimeoutError, got %v", err)
	}
	// so the next run finds no slot before its own deadline
	if err := r.Do(context.Background(), func() {}); !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy, got %v", err)
	}
	close(release)
	time.Sleep(10 * time.Millisecond)
	if err := r.Do(context.Background(), func() {}); err != nil {
		t.Fatalf("slot should be free again: %v", err)
	}
	// a cancelled caller is not run at all
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false
	if err := r.Do(ctx, func() { ran = true }); !errors.Is(err, context.Canceled) || ran {
		t.Fatalf("cancelled: err %v, ran %v", err, ran)
	}
}

func TestExecuteCapsOutput(t *testing.T) {
	big := &Tool{ID: "big", Run: func(Request) (*Result, error) { return &Result{Output: string(make([]byte, MaxOutput+1))}, nil }}
	if _, err := big.Execute(Request{}); err == nil {
		t.Fatal("an oversized result should be an error")
	}
}
