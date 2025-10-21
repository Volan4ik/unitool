package rate

import (
	"sync"
	"time"
)

type Limiter struct {
	mu sync.Mutex
	tokens int
	max int
	refill int
	interval time.Duration
	last time.Time
}

func New(max, refill int, interval time.Duration) *Limiter {
	return &Limiter{tokens: max, max: max, refill: refill, interval: interval, last: time.Now()}
}

func (l *Limiter) Allow() bool {
	l.mu.Lock(); defer l.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(l.last)
	if elapsed >= l.interval {
		steps := int(elapsed / l.interval)
		l.tokens = min(l.max, l.tokens + steps*l.refill)
		l.last = now
	}
	if l.tokens > 0 { l.tokens--; return true }
	return false
}

func min(a,b int) int { if a<b {return a}; return b }