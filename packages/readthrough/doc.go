// Package readthrough protects a database behind a cachestore.CacheStore from the three ways a
// cache becomes an outage amplifier: stampede (many callers, one hot key), delayed stampede (many
// distinct keys expiring together) and penetration (requests for keys that do not exist). It also
// keeps a cache outage from becoming a database outage.
//
// It is generic. It does not know about tenants, regions, users or PII: the consuming service
// builds opaque keys (see JoinKey, HashKey), decides who may ask (authorise before calling Get),
// and declares its entity classes. The cache backend is any cachestore.CacheStore (use
// cachestore-go/redis New for Redis with an in-memory fallback). Observability goes through the
// Observer interface; the prom module provides a Prometheus implementation.
//
// The lookup path of Cache.Get:
//
//  1. Reject a malformed key (ErrInvalidKey): no cache, no loader.
//  2. Read the store. A tombstone answers ErrNotFound; a fresh value is returned; a soft-stale
//     value (StaleFor > 0) is returned while one caller refreshes it in the background.
//  3. On a miss, collapse in-process (singleflight), then across instances with a store lease
//     (SetNX), then run the loader through a per-class bulkhead under a detached, bounded
//     context. Overflow is shed with ErrShed rather than queued.
//  4. Write the value with a TTL the package jitters, or a short-lived tombstone when the loader
//     returned ErrNotFound. A write path that creates an entity must call Invalidate.
//
// Store errors are misses, never request failures; a circuit breaker stops hammering a dead store.
package readthrough
