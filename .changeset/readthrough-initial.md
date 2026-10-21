---
"@zenius-one/telemetry-readthrough-go": minor
"@zenius-one/telemetry-readthrough-prom-go": minor
---

Add `readthrough`: generic read-through cache protection (singleflight, store lease, tombstones,
package-enforced TTL jitter, stale-while-revalidate, per-class bulkhead, store circuit breaker,
`Warm`, `RefreshAhead`) over any `cachestore.CacheStore`, with an `Observer` interface. Add
`readthrough/prom`, the Prometheus `Observer`.
