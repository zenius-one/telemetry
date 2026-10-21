package readthrough

import (
	"fmt"
	"time"
)

// Defaults applied by New to zero Class fields.
const (
	DefaultNegativeTTL        = 60 * time.Second
	DefaultLoaderTimeout      = 5 * time.Second
	DefaultMaxConcurrentLoads = 32
	DefaultRetryAfter         = time.Second
	// MaxKeyLen bounds a key so an attacker cannot make Redis hold arbitrarily large keys.
	MaxKeyLen = 512
)

// Class declares one kind of cached entity. Callers never pass a raw TTL: every write takes its
// TTL from the class and has jitter applied by the package, so a batch job cannot skip it.
type Class struct {
	// Name is the entity class (low cardinality: "patient", "catalog", ...). Required, unique.
	Name string

	// BaseTTL is the minimum time a loaded value is fresh. Required.
	BaseTTL time.Duration
	// Spread is the width of the jitter window added on top of BaseTTL (ttl = BaseTTL +
	// rand[0, spread]). Size it from the DB budget: spread >= N_keys / DBBudgetLoadsPerSecond.
	// The effective spread is never below 10% of BaseTTL (see EffectiveSpread). One-sided, so
	// jitter never shortens freshness below BaseTTL.
	Spread time.Duration

	// NegativeTTL is how long a tombstone ("does not exist") is kept. 0 selects
	// DefaultNegativeTTL; a negative value disables negative caching for the class.
	NegativeTTL time.Duration

	// StaleFor enables stale-while-revalidate when > 0: a value is served for StaleFor after it
	// goes soft-stale while one caller refreshes it. Only for data where brief staleness is
	// acceptable. The soft expiry is jittered like any TTL.
	StaleFor time.Duration

	// LoaderTimeout bounds one loader call. It runs under a detached context, not the first
	// caller's. Default DefaultLoaderTimeout.
	LoaderTimeout time.Duration
	// LeaseWait bounds how long an instance that lost the cross-instance lease polls for the
	// winner's value before loading itself. Default 2 x LoaderTimeout.
	LeaseWait time.Duration

	// MaxConcurrentLoads is the per-class bulkhead: loader calls allowed at once on this
	// instance. Beyond it lookups are shed with ErrShed, not queued. Default
	// DefaultMaxConcurrentLoads.
	MaxConcurrentLoads int
	// DBBudgetLoadsPerSecond is L: loads/s the backing store sustains for this class. It paces
	// Warm and is exported to the Observer's consumers for alert thresholds. 0 means unpaced.
	DBBudgetLoadsPerSecond float64
	// RetryAfter is the hint carried by ShedError. Default DefaultRetryAfter.
	RetryAfter time.Duration
}

// EffectiveSpread is the jitter window actually used: max(Spread, BaseTTL/10).
func (c Class) EffectiveSpread() time.Duration {
	return max(c.Spread, c.BaseTTL/10)
}

func (c Class) withDefaults() (Class, error) {
	if c.Name == "" {
		return c, fmt.Errorf("readthrough: class name is required")
	}
	if c.BaseTTL <= 0 {
		return c, fmt.Errorf("readthrough: class %q: BaseTTL must be positive", c.Name)
	}
	if c.Spread < 0 || c.StaleFor < 0 || c.LoaderTimeout < 0 || c.LeaseWait < 0 {
		return c, fmt.Errorf("readthrough: class %q: durations must not be negative", c.Name)
	}
	if c.NegativeTTL == 0 {
		c.NegativeTTL = DefaultNegativeTTL
	}
	if c.LoaderTimeout == 0 {
		c.LoaderTimeout = DefaultLoaderTimeout
	}
	if c.LeaseWait == 0 {
		c.LeaseWait = 2 * c.LoaderTimeout
	}
	if c.MaxConcurrentLoads <= 0 {
		c.MaxConcurrentLoads = DefaultMaxConcurrentLoads
	}
	if c.RetryAfter == 0 {
		c.RetryAfter = DefaultRetryAfter
	}
	return c, nil
}
