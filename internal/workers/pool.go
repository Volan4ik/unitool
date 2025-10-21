package workers

import "context"

type Job func(ctx context.Context) error

type Pool struct {
	jobs chan Job
}

func NewPool(buffer int, workers int) *Pool {
	p := &Pool{jobs: make(chan Job, buffer)}
	for i := 0; i < workers; i++ { go p.worker() }
	return p
}

func (p *Pool) worker() {
	for job := range p.jobs {
		_ = job(context.Background())
	}
}

func (p *Pool) Submit(job Job) { p.jobs <- job }