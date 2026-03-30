package rate

import (
	"sync"
	"time"
)

type bucket struct {
	tokens   int
	max      int
	refill   int
	interval time.Duration
	last     time.Time
}

type Config struct {
	Max      int
	Refill   int
	Interval time.Duration
}

type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

func New(max, refill int, interval time.Duration) *Limiter {
	return NewByKind(map[string]Config{
		"": {Max: max, Refill: refill, Interval: interval},
	})
}

func NewByKind(cfg map[string]Config) *Limiter {
	now := time.Now()
	buckets := make(map[string]*bucket, len(cfg))
	for kind, c := range cfg {
		buckets[kind] = &bucket{
			tokens:   c.Max,
			max:      c.Max,
			refill:   c.Refill,
			interval: c.Interval,
			last:     now,
		}
	}
	return &Limiter{buckets: buckets}
}

func (l *Limiter) Allow() bool {
	return l.AllowKind("")
}

func (l *Limiter) AllowKind(kind string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.bucketForKind(kind)
	if b == nil {
		return true
	}

	now := time.Now()
	elapsed := now.Sub(b.last)
	if b.interval > 0 && elapsed >= b.interval {
		steps := int(elapsed / b.interval)
		b.tokens = min(b.max, b.tokens+steps*b.refill)
		b.last = now
	}
	if b.tokens > 0 {
		b.tokens--
		return true
	}
	return false
}

func (l *Limiter) bucketForKind(kind string) *bucket {
	if l == nil {
		return nil
	}
	if b, ok := l.buckets[kind]; ok {
		return b
	}
	return l.buckets[""]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
