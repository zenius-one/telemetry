package readthrough

import (
	"sync"
	"time"
)

// breaker is a consecutive-failure circuit breaker around the cache store. Open means: do not
// touch the store (no GET, no lease, no write); loads go straight to the bulkhead.
type breaker struct {
	mu        sync.Mutex
	state     int
	failures  int
	openedAt  time.Time
	probing   bool
	threshold int
	cooldown  time.Duration
	now       func() time.Time
	onChange  func(state int)
}

func (b *breaker) allow() bool {
	b.mu.Lock()
	var changed bool
	var ok bool
	switch b.state {
	case BreakerClosed:
		ok = true
	case BreakerOpen:
		if b.now().Sub(b.openedAt) >= b.cooldown {
			b.state, b.probing, changed, ok = BreakerHalfOpen, true, true, true
		}
	case BreakerHalfOpen:
		if !b.probing {
			b.probing, ok = true, true
		}
	}
	state := b.state
	b.mu.Unlock()
	if changed {
		b.onChange(state)
	}
	return ok
}

func (b *breaker) record(err error) {
	b.mu.Lock()
	prev := b.state
	if err == nil {
		b.failures, b.probing = 0, false
		b.state = BreakerClosed
	} else {
		b.failures++
		b.probing = false
		if b.state == BreakerHalfOpen || b.failures >= b.threshold {
			b.state, b.openedAt = BreakerOpen, b.now()
		}
	}
	state := b.state
	b.mu.Unlock()
	if state != prev {
		b.onChange(state)
	}
}
