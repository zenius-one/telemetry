package readthrough

import "time"

// Result values passed to ClassObserver.Result.
const (
	ResultHit         = "hit"
	ResultNegativeHit = "negative_hit"
	ResultStale       = "stale"
	ResultMissLoaded  = "miss_loaded"
	ResultMissNoFound = "miss_not_found"
	ResultShed        = "shed"
	ResultError       = "error"
)

// Rejection reasons passed to ClassObserver.Rejected.
const (
	RejectMalformed    = "malformed"
	RejectUnauthorised = "unauthorised"
)

// Lease outcomes passed to ClassObserver.Lease.
const (
	LeaseLeader        = "leader"
	LeaseWaiterServed  = "waiter_served"
	LeaseWaiterTimeout = "waiter_timeout"
)

// Breaker states passed to ClassObserver.SetBreaker.
const (
	BreakerClosed   = 0
	BreakerHalfOpen = 1
	BreakerOpen     = 2
)

// Observer is how a Cache reports what it did. It is the only observability surface of this
// module, so the core takes no metrics dependency; see the prom module for Prometheus.
//
// Implementations must be safe for concurrent use and must keep label cardinality bounded: the
// only free-form value a Cache ever passes is the class name, and that is always one declared in
// New. A Cache never passes keys or any identifier.
type Observer interface {
	Class(name string) ClassObserver
}

// ClassObserver receives the events of one declared class.
type ClassObserver interface {
	// Request: a lookup entered the component after key validation (the demand denominator).
	Request()
	// Result: the outcome of a lookup (Result* constants).
	Result(result string)
	// Rejected: a lookup was refused before cache or loader (Reject* constants).
	Rejected(reason string)
	// Flight: a caller's role in an in-process collapse. ranLoader is true for the one caller
	// that executed the loader path, false for every caller that waited and reused its result.
	Flight(ranLoader bool)
	// Lease: a cross-instance lease outcome (Lease* constants).
	Lease(outcome string)
	// DBLoad marks the start of one real loader call and returns the func to call when it ends.
	// It is called only where the loader actually runs, never when a value was found in the cache.
	DBLoad() (done func())
	// TombstoneWritten: a negative entry was created.
	TombstoneWritten()
	// SetBreaker publishes the store circuit-breaker state (Breaker* constants).
	SetBreaker(state int)
	// ObserveWrite: a cache write was given this physical TTL. Called for every write, including
	// each key of Put and Warm, so the expiry distribution can be watched.
	ObserveWrite(ttl time.Duration)
}

type noopObserver struct{}

func (noopObserver) Class(string) ClassObserver { return noopClass{} }

type noopClass struct{}

func (noopClass) Request()                   {}
func (noopClass) Result(string)              {}
func (noopClass) Rejected(string)            {}
func (noopClass) Flight(bool)                {}
func (noopClass) Lease(string)               {}
func (noopClass) DBLoad() (done func())      { return func() {} }
func (noopClass) TombstoneWritten()          {}
func (noopClass) SetBreaker(int)             {}
func (noopClass) ObserveWrite(time.Duration) {}
