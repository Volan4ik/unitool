package workers

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPoolSubmitAndStop(t *testing.T) {
	p := NewPool(1, 1)
	done := make(chan struct{})

	p.Submit(func(ctx context.Context) error {
		close(done)
		return nil
	})

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("job was not executed in time")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.StopAndWait(ctx); err != nil {
		t.Fatalf("unexpected stop error: %v", err)
	}
}

func TestPoolTrySubmitWhenQueueFull(t *testing.T) {
	p := NewPool(1, 1)
	started := make(chan struct{})
	block := make(chan struct{})

	p.Submit(func(ctx context.Context) error {
		close(started)
		<-block
		return nil
	})
	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first job did not start")
	}

	// Fill buffered queue with one pending job.
	ok := p.TrySubmit(func(ctx context.Context) error { return nil })
	if !ok {
		t.Fatal("expected TrySubmit to succeed for available buffer slot")
	}
	// Queue is full now while worker is blocked.
	ok = p.TrySubmit(func(ctx context.Context) error { return nil })
	if ok {
		t.Fatal("expected TrySubmit to fail when queue is full")
	}

	close(block)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.StopAndWait(ctx); err != nil {
		t.Fatalf("unexpected stop error: %v", err)
	}
}

func TestPoolStopAndWaitContextDeadline(t *testing.T) {
	p := NewPool(1, 1)
	block := make(chan struct{})

	p.Submit(func(ctx context.Context) error {
		<-block
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := p.StopAndWait(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}

	close(block)
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := p.StopAndWait(ctx2); err != nil {
		t.Fatalf("unexpected cleanup stop error: %v", err)
	}
}
