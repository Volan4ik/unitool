package workers

import (
	"context"
	"log"
)

type Job func(ctx context.Context) error

type Pool struct {
	jobs chan Job
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
		go p.worker()
	}
	return p
}

func (p *Pool) worker() {
	for job := range p.jobs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("workers: recovered panic: %v", r)
				}
			}()
			_ = job(context.Background())
		}()
	}
}

func (p *Pool) Submit(job Job) { p.jobs <- job }

func (p *Pool) TrySubmit(job Job) bool {
	select {
	case p.jobs <- job:
		return true
	default:
		return false
	}
}
