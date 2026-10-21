package readthrough

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"
)

// KeyLoader loads one key; Warm calls it only for keys that are not already cached.
type KeyLoader func(ctx context.Context, key string) ([]byte, error)

// WarmResult summarises a Warm run.
type WarmResult struct {
	Loaded, AlreadyCached, NotFound, Shed, Failed int
}

// Warm populates keys of class through the normal read-through path, so it gets the same lease,
// bulkhead, tombstone and jitter behaviour as live traffic. Keys are visited in random order and
// paced at the class's DBBudgetLoadsPerSecond (when set) by a bounded worker pool, so populating
// does not itself create the load spike jitter is meant to avoid. Use it at startup and after a
// store restart.
func (c *Cache) Warm(ctx context.Context, className string, keys []string, load KeyLoader, workers int) (WarmResult, error) {
	cls, ok := c.classes[className]
	if !ok {
		return WarmResult{}, ErrUnknownClass
	}
	if workers <= 0 {
		workers = 4
	}
	order := append([]string(nil), keys...)
	rand.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })

	var loaded, cached, notFound, shed, failed atomic.Int64
	work := make(chan string)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range work {
				var ran atomic.Bool
				_, err := c.Get(ctx, className, key, func(lctx context.Context) ([]byte, error) {
					ran.Store(true)
					return load(lctx, key)
				})
				switch {
				case err == nil && ran.Load():
					loaded.Add(1)
				case err == nil:
					cached.Add(1)
				case errors.Is(err, ErrNotFound):
					notFound.Add(1)
				case errors.Is(err, ErrShed):
					shed.Add(1)
				default:
					failed.Add(1)
				}
			}
		}()
	}

	var tick <-chan time.Time
	if cls.DBBudgetLoadsPerSecond > 0 {
		t := time.NewTicker(time.Duration(float64(time.Second) / cls.DBBudgetLoadsPerSecond))
		defer t.Stop()
		tick = t.C
	}
	var runErr error
feed:
	for _, key := range order {
		if tick != nil {
			select {
			case <-tick:
			case <-ctx.Done():
				runErr = ctx.Err()
				break feed
			}
		}
		select {
		case work <- key:
		case <-ctx.Done():
			runErr = ctx.Err()
			break feed
		}
	}
	close(work)
	wg.Wait()
	return WarmResult{int(loaded.Load()), int(cached.Load()), int(notFound.Load()), int(shed.Load()), int(failed.Load())}, runErr
}

// RefreshAhead keeps a small, hot key (a catalog, a permission snapshot) permanently fresh: it
// reloads key every `every` (minus up to 20% random, so many refreshers desynchronise) and so
// users never take the miss. every must be shorter than the class BaseTTL. Call the returned stop
// func on shutdown. Reload failures keep the previous value until it expires.
func (c *Cache) RefreshAhead(className, key string, every time.Duration, load Loader) (stop func(), err error) {
	cls, ok := c.classes[className]
	if !ok {
		return nil, ErrUnknownClass
	}
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if every <= 0 || every >= cls.BaseTTL {
		return nil, errors.New("readthrough: refresh interval must be positive and shorter than the class BaseTTL")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			rctx, rcancel := context.WithTimeout(ctx, cls.LeaseWait+cls.LoaderTimeout+time.Second)
			c.lead(rctx, cls, key, load, true)
			rcancel()
			t := time.NewTimer(every - time.Duration(rand.Int64N(int64(every/5)+1)))
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
	}()
	return func() { cancel(); <-done }, nil
}
