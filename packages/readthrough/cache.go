package readthrough

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	cachestore "github.com/zenius-one/cachestore/packages/cachestore-go"
)

// Loader fetches the value for a key from the source of truth. Return ErrNotFound when the entity
// does not exist; any other error is returned to the caller and nothing is cached. It runs under a
// context that is detached from the caller's cancellation and bounded by Class.LoaderTimeout.
type Loader func(ctx context.Context) ([]byte, error)

// Cache is a read-through cache with stampede and penetration protection. Safe for concurrent
// use. Create it once per service with New.
type Cache struct {
	store   cachestore.CacheStore
	classes map[string]*class
	group   singleflight.Group
	br      *breaker
	now     func() time.Time
	obs     Observer

	refreshing sync.Map // class\x00key -> struct{}: in-flight background refreshes
}

type class struct {
	Class
	obs ClassObserver
	sem chan struct{} // bulkhead
}

func (c *class) leaseTTL() time.Duration { return c.LoaderTimeout + time.Second }

// Option configures New.
type Option func(*Cache)

// WithObserver sets the Observer (default: discards everything).
func WithObserver(o Observer) Option {
	return func(c *Cache) {
		if o != nil {
			c.obs = o
		}
	}
}

// WithClock replaces the time source (tests).
func WithClock(now func() time.Time) Option { return func(c *Cache) { c.now = now } }

// WithBreaker tunes the store circuit breaker: it opens after threshold consecutive store errors
// and probes again after cooldown. Defaults: 5 errors, 5 s.
func WithBreaker(threshold int, cooldown time.Duration) Option {
	return func(c *Cache) {
		c.br.threshold, c.br.cooldown = max(threshold, 1), cooldown
	}
}

// New returns a Cache over store for the declared classes. The store is owned by the caller.
func New(store cachestore.CacheStore, classes []Class, opts ...Option) (*Cache, error) {
	if store == nil {
		return nil, errors.New("readthrough: store is required")
	}
	c := &Cache{
		store:   store,
		classes: make(map[string]*class, len(classes)),
		now:     time.Now,
		obs:     noopObserver{},
	}
	c.br = &breaker{threshold: 5, cooldown: 5 * time.Second, now: func() time.Time { return c.now() }}
	c.br.onChange = func(state int) {
		for _, cl := range c.classes {
			cl.obs.SetBreaker(state)
		}
	}
	for _, o := range opts {
		o(c)
	}
	for _, d := range classes {
		d, err := d.withDefaults()
		if err != nil {
			return nil, err
		}
		if _, dup := c.classes[d.Name]; dup {
			return nil, fmt.Errorf("readthrough: duplicate class %q", d.Name)
		}
		c.classes[d.Name] = &class{Class: d, obs: c.obs.Class(d.Name), sem: make(chan struct{}, d.MaxConcurrentLoads)}
	}
	return c, nil
}

// flight is the outcome shared by every caller collapsed onto one load.
type flight struct {
	val    []byte
	err    error
	result string
}

// Get returns the value for key in class, loading it through load on a miss.
//
// Authorise the caller before calling Get: an existing and a non-existing key must be
// indistinguishable to someone who may not see it. Errors: ErrInvalidKey, ErrUnknownClass,
// ErrNotFound (tombstone or loader said so), ErrShed (*ShedError), the caller's ctx error while
// waiting, or the loader's own error.
func (c *Cache) Get(ctx context.Context, className, key string, load Loader) ([]byte, error) {
	cls, ok := c.classes[className]
	if !ok {
		return nil, ErrUnknownClass
	}
	if err := validateKey(key); err != nil {
		cls.obs.Rejected(RejectMalformed)
		return nil, err
	}
	cls.obs.Request()

	if raw, found, usable := c.sGet(ctx, valuePrefix+key); usable && found {
		if env, ok := decode(raw); ok {
			switch {
			case env.kind == kindTombstone:
				cls.obs.Result(ResultNegativeHit)
				return nil, ErrNotFound
			case c.now().UnixNano() < env.softExp:
				cls.obs.Result(ResultHit)
				return env.payload, nil
			case cls.StaleFor > 0:
				cls.obs.Result(ResultStale)
				c.refreshAsync(ctx, cls, key, load)
				return env.payload, nil
			}
		}
	}

	var ran atomic.Bool
	ch := c.group.DoChan(cls.Name+"\x00"+key, func() (any, error) {
		ran.Store(true)
		cls.obs.Flight(true)
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cls.LeaseWait+cls.LoaderTimeout+time.Second)
		defer cancel()
		return c.lead(dctx, cls, key, load, false), nil
	})
	select {
	case r := <-ch:
		if !ran.Load() {
			cls.obs.Flight(false)
		}
		f := r.Val.(flight)
		cls.obs.Result(f.result)
		return f.val, f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// lead runs the lease, the loader and the write for one collapsed group. ctx is already detached
// from any caller. force skips the "already fresh" re-check (refresh-ahead and background refresh).
func (c *Cache) lead(ctx context.Context, cls *class, key string, load Loader, force bool) flight {
	token := fmt.Sprintf("%016x%016x", rand.Uint64(), rand.Uint64())
	held := false
	switch won, usable := c.sSetNX(ctx, leasePrefix+key, []byte(token), cls.leaseTTL()); {
	case !usable:
		// Store unavailable: no cross-instance collapsing; the bulkhead is the protection.
	case won:
		held = true
		cls.obs.Lease(LeaseLeader)
	default:
		if f, ok := c.waitForValue(ctx, cls, key); ok {
			cls.obs.Lease(LeaseWaiterServed)
			return f
		}
		cls.obs.Lease(LeaseWaiterTimeout)
	}
	if held {
		defer c.release(ctx, key, token)
		if !force {
			// Another instance may have populated the key between our miss and our lease.
			if f, ok := c.freshFlight(ctx, key); ok {
				return f
			}
		}
	}
	return c.loadAndStore(ctx, cls, key, load)
}

func (c *Cache) loadAndStore(ctx context.Context, cls *class, key string, load Loader) flight {
	select {
	case cls.sem <- struct{}{}:
		defer func() { <-cls.sem }()
	default:
		return flight{err: &ShedError{Class: cls.Name, RetryAfter: cls.RetryAfter}, result: ResultShed}
	}
	lctx, cancel := context.WithTimeout(ctx, cls.LoaderTimeout)
	defer cancel()
	done := cls.obs.DBLoad()
	val, err := load(lctx)
	done()
	switch {
	case err == nil:
		c.writeValue(ctx, cls, key, val)
		return flight{val: val, result: ResultMissLoaded}
	case errors.Is(err, ErrNotFound):
		c.writeTombstone(ctx, cls, key)
		return flight{err: ErrNotFound, result: ResultMissNoFound}
	default:
		return flight{err: err, result: ResultError}
	}
}

func (c *Cache) writeValue(ctx context.Context, cls *class, key string, val []byte) {
	ttl := jitter(cls.BaseTTL, cls.EffectiveSpread())
	hard := ttl + cls.StaleFor
	env := envelope{kind: kindValue, softExp: c.now().Add(ttl).UnixNano(), payload: val}
	if c.sSet(ctx, valuePrefix+key, env.encode(), hard) {
		cls.obs.ObserveWrite(hard)
	}
}

// writeTombstone uses SetNX so a tombstone can never overwrite a value written concurrently.
func (c *Cache) writeTombstone(ctx context.Context, cls *class, key string) {
	if cls.NegativeTTL < 0 {
		return
	}
	ttl := jitter(cls.NegativeTTL, cls.NegativeTTL/10)
	if won, usable := c.sSetNX(ctx, valuePrefix+key, envelope{kind: kindTombstone}.encode(), ttl); usable && won {
		cls.obs.TombstoneWritten()
		cls.obs.ObserveWrite(ttl)
	}
}

func (c *Cache) freshFlight(ctx context.Context, key string) (flight, bool) {
	raw, found, usable := c.sGet(ctx, valuePrefix+key)
	if !usable || !found {
		return flight{}, false
	}
	env, ok := decode(raw)
	if !ok {
		return flight{}, false
	}
	if env.kind == kindTombstone {
		return flight{err: ErrNotFound, result: ResultNegativeHit}, true
	}
	if c.now().UnixNano() < env.softExp {
		return flight{val: env.payload, result: ResultHit}, true
	}
	return flight{}, false
}

// waitForValue polls for the lease holder's result with jittered backoff. It gives up when the
// lease holder disappears (released or expired without a value) or LeaseWait elapses.
func (c *Cache) waitForValue(ctx context.Context, cls *class, key string) (flight, bool) {
	deadline := time.Now().Add(cls.LeaseWait)
	delay := 5 * time.Millisecond
	for {
		t := time.NewTimer(jitter(delay/2, delay/2))
		select {
		case <-ctx.Done():
			t.Stop()
			return flight{}, false
		case <-t.C:
		}
		if f, ok := c.freshFlight(ctx, key); ok {
			return f, true
		}
		if _, held, usable := c.sGet(ctx, leasePrefix+key); !usable || !held {
			f, ok := c.freshFlight(ctx, key) // the holder may have finished between the two reads
			return f, ok
		}
		if time.Now().After(deadline) {
			return flight{}, false
		}
		delay = min(delay*2, 100*time.Millisecond)
	}
}

// release deletes the lease only if it is still ours. Get-then-Delete is not atomic; that is
// acceptable because the lease is an efficiency lock (a rare double load costs one extra read).
func (c *Cache) release(ctx context.Context, key, token string) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if v, found, usable := c.sGet(rctx, leasePrefix+key); usable && found && string(v) == token {
		c.sDel(rctx, leasePrefix+key)
	}
}

func (c *Cache) refreshAsync(ctx context.Context, cls *class, key string, load Loader) {
	id := cls.Name + "\x00" + key
	if _, running := c.refreshing.LoadOrStore(id, struct{}{}); running {
		return
	}
	parent := context.WithoutCancel(ctx)
	go func() {
		defer c.refreshing.Delete(id)
		dctx, cancel := context.WithTimeout(parent, cls.LeaseWait+cls.LoaderTimeout+time.Second)
		defer cancel()
		c.lead(dctx, cls, key, load, false)
	}()
}

// Invalidate removes keys (value and tombstone) in class. The write path that creates or changes
// an entity must call it after commit: it is what makes a cached "not found" safe.
func (c *Cache) Invalidate(ctx context.Context, className string, keys ...string) error {
	if _, ok := c.classes[className]; !ok {
		return ErrUnknownClass
	}
	full := make([]string, 0, len(keys))
	for _, k := range keys {
		if err := validateKey(k); err != nil {
			return err
		}
		full = append(full, valuePrefix+k)
	}
	if !c.sDel(ctx, full...) {
		return errors.New("readthrough: store unavailable; invalidation not applied")
	}
	return nil
}

// Put writes a value directly (batch jobs, write-through). The package applies the class TTL and
// jitter; callers cannot pass a TTL, so a batch job cannot create an expiry cliff.
func (c *Cache) Put(ctx context.Context, className, key string, val []byte) error {
	cls, ok := c.classes[className]
	if !ok {
		return ErrUnknownClass
	}
	if err := validateKey(key); err != nil {
		return err
	}
	ttl := jitter(cls.BaseTTL, cls.EffectiveSpread())
	hard := ttl + cls.StaleFor
	env := envelope{kind: kindValue, softExp: c.now().Add(ttl).UnixNano(), payload: val}
	if !c.sSet(ctx, valuePrefix+key, env.encode(), hard) {
		return errors.New("readthrough: store unavailable; value not written")
	}
	cls.obs.ObserveWrite(hard)
	return nil
}

// Reject lets the consuming service report a request it refused before calling Get (for example
// an authorisation failure), so rejections are counted next to the cache's own.
func (c *Cache) Reject(className, reason string) {
	if cls, ok := c.classes[className]; ok {
		cls.obs.Rejected(reason)
	}
}

// jitter returns base + rand[0, spread].
func jitter(base, spread time.Duration) time.Duration {
	if spread <= 0 {
		return base
	}
	return base + time.Duration(rand.Int64N(int64(spread)+1))
}

// Store access. Each returns usable=false when the breaker is open or the store errored; the
// caller then behaves as on a miss without a store.

func (c *Cache) sGet(ctx context.Context, k string) (b []byte, found, usable bool) {
	if !c.br.allow() {
		return nil, false, false
	}
	b, found, err := c.store.Get(ctx, k)
	c.br.record(err)
	return b, found, err == nil
}

func (c *Cache) sSet(ctx context.Context, k string, v []byte, ttl time.Duration) bool {
	if !c.br.allow() {
		return false
	}
	err := c.store.Set(ctx, k, v, ttl)
	c.br.record(err)
	return err == nil
}

func (c *Cache) sSetNX(ctx context.Context, k string, v []byte, ttl time.Duration) (won, usable bool) {
	if !c.br.allow() {
		return false, false
	}
	won, err := c.store.SetNX(ctx, k, v, ttl)
	c.br.record(err)
	return won, err == nil
}

func (c *Cache) sDel(ctx context.Context, keys ...string) bool {
	if !c.br.allow() {
		return false
	}
	err := c.store.Delete(ctx, keys...)
	c.br.record(err)
	return err == nil
}
