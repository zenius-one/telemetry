# readthrough

Read-through cache protection for Go services. Put it between your handlers and a database and it
keeps a cache from becoming an outage amplifier.

```go
import (
	cacheredis "github.com/zenius-one/cachestore/packages/cachestore-go/redis"
	"github.com/zenius-one/telemetry/packages/readthrough"
)

store := cacheredis.New(redisClient, "myservice:", cachestore.MemoryOptions{}) // Redis + memory fallback
cache, err := readthrough.New(store, []readthrough.Class{{
	Name:                   "catalog",
	BaseTTL:                30 * time.Minute,
	Spread:                 5 * time.Minute,  // jitter window, sized from the DB budget
	StaleFor:               time.Minute,      // optional stale-while-revalidate
	MaxConcurrentLoads:     16,               // per-class bulkhead
	DBBudgetLoadsPerSecond: 50,
}}, readthrough.WithObserver(obs))

key := readthrough.JoinKey(scope, "catalog", id) // you decide the scope; always include the tenant
v, err := cache.Get(ctx, "catalog", key, func(ctx context.Context) ([]byte, error) {
	return loadFromDB(ctx, id) // return readthrough.ErrNotFound when it does not exist
})
// errors: ErrNotFound -> 404, *ShedError -> 503 + RetryAfter, ErrInvalidKey -> 400
```

## What it does

<!--prettier-ignore-->
| Threat | Defence |
| --- | --- |
| Stampede on one key | In-process `singleflight`, then a store lease (`SetNX`) across instances: about one load per key cluster-wide, no routing affinity required. The loader runs under a **detached, bounded** context, so one cancelled caller cannot fail the herd. |
| Delayed stampede (many keys expiring together) | The package jitters **every** write (`Get`, `Put`, `Warm`, refresh); callers cannot pass a TTL. Jitter is one-sided (`BaseTTL + rand[0, spread]`) with a floor of 10% of `BaseTTL`; size `Spread >= keys / DBBudgetLoadsPerSecond`. Soft expiry is jittered too. A per-class bulkhead is the backstop. |
| Penetration (keys that do not exist) | Malformed keys are rejected before the cache or loader. A confirmed "not found" is stored as a short-lived tombstone written with `SetNX` (never overwrites a value). `Invalidate` after creating an entity clears it. |
| Avalanche (store down) | Store errors are misses. A circuit breaker stops touching the store; loads go through the bulkhead and overflow is shed (`ErrShed`) rather than queued. |

Also: `Warm` (random order, paced at the class DB budget, bounded workers), `RefreshAhead` (hot
small sets reloaded at jittered intervals so users never miss), `StaleFor` stale-while-revalidate.

## What it deliberately does not do

- **Authorise.** Check the caller before `Get`, and answer "not found" and "not yours"
  identically.
- **Know your tenancy or PII rules.** Keys are opaque. Build them with `JoinKey`; hash anything
  secret or personal with `HashKey` (keyed HMAC). Never put raw PII in a key.
- **Pick an observability backend.** It emits through `Observer`; use
  [`readthrough/prom`](prom) for Prometheus, or implement the interface.
- **Pick the store.** Any `cachestore.CacheStore`. The lease needs a store shared by your
  instances (Redis); with a memory-only store protection is per process.

## Observer contract

Counting rules the implementations must keep: a collapse's leader is not "suppressed"
(`Flight(true)` once, `Flight(false)` for every other caller); `DBLoad` is called only where the
loader actually runs, never when a value turned up in the store. The only free-form value emitted
is the declared class name: never keys or identifiers.

## Staleness

`StaleFor` serves old data. Enable it only for classes where that is acceptable (catalogs,
configuration). Never for data where staleness is a safety issue.

## Known limits

- The lease release is get-then-delete, not atomic. The lease is an efficiency lock; a rare
  double load costs one extra read, not correctness.
- A failing loader behind a stale value is retried by the next reader that finds it stale
  (bounded by singleflight, the lease and the bulkhead, not by a backoff).
