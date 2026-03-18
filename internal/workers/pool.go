package workers

import (
	"context"
	"log"
	"sync"
)

type Job func(ctx context.Context) error

type Pool struct {
	jobs    chan Job
	wg      sync.WaitGroup
	mu      sync.RWMutex
	stopped bool
}

func NewPool(buffer int, workers int) *Pool {
	if workers <= 0 {
		workers = 1
	}
	if buffer <= 0 {
		buffer = workers * 2
	}
	p := &Pool{jobs: make(chan Job, buffer)}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
	return p
}

func (p *Pool) worker() {
	defer p.wg.Done()
	for job := range p.jobs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("workers: recovered panic: %v", r)
				}
			}()
			if err := job(context.Background()); err != nil {
				log.Printf("workers: job error: %v", err)
			}
		}()
	}
}

func (p *Pool) Submit(job Job) {
	p.mu.RLock()
	stopped := p.stopped
	jobs := p.jobs
	p.mu.RUnlock()
	if stopped {
		return
	}
	// StopAndWait may close the channel between stopped-check and send.
	// Recover keeps shutdown path safe for callers that still use blocking Submit.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("workers: submit ignored during shutdown: %v", r)
		}
	}()
	jobs <- job
}

func (p *Pool) TrySubmit(job Job) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.stopped {
		return false
	}
	select {
	case p.jobs <- job:
		return true
	default:
		return false
	}
}

func (p *Pool) StopAndWait(ctx context.Context) error {
	p.mu.Lock()
	if !p.stopped {
		p.stopped = true
		close(p.jobs)
	}
	p.mu.Unlock()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
