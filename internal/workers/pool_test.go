package workers

import (
	"context"
	"testing"
	"time"
)

func TestPoolTrySubmitWhenQueueFull(t *testing.T) {
	p := NewPool(1, 1)
	defer func() { _ = p.StopAndWait(context.Background()) }()

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	p.Submit(func(context.Context) error {
		started <- struct{}{}
		<-release
		return nil
	})
	<-started

	ok := p.TrySubmit(func(context.Context) error { return nil })
	if !ok {
		t.Fatal("expected first TrySubmit to fill queue")
	}
	ok = p.TrySubmit(func(context.Context) error { return nil })
	if ok {
		t.Fatal("expected second TrySubmit to fail when queue is full")
	}
	close(release)
}

func TestPoolRecoversFromPanicAndContinues(t *testing.T) {
	p := NewPool(2, 1)
	defer func() { _ = p.StopAndWait(context.Background()) }()
	done := make(chan struct{}, 1)

	p.Submit(func(context.Context) error { panic("boom") })
	p.Submit(func(context.Context) error {
		done <- struct{}{}
		return nil
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not continue after panic")
	}
}

func TestPoolStopAndWaitRejectsSubmits(t *testing.T) {
	p := NewPool(1, 1)
	if err := p.StopAndWait(context.Background()); err != nil {
		t.Fatalf("stop and wait: %v", err)
	}
	if ok := p.TrySubmit(func(context.Context) error { return nil }); ok {
		t.Fatal("expected TrySubmit=false after stop")
	}
}

func TestPoolSubmitAfterStopDoesNotPanic(t *testing.T) {
	p := NewPool(1, 1)
	if err := p.StopAndWait(context.Background()); err != nil {
		t.Fatalf("stop and wait: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Submit(func(context.Context) error { return nil })
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("submit blocked after stop")
	}
}
