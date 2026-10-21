# @zenius-one/telemetry-readthrough-prom-go

## 0.1.0

### Minor Changes

- [#2](https://github.com/zenius-one/telemetry/pull/2) [`9e2937b`](https://github.com/zenius-one/telemetry/commit/9e2937b10d6f32d26fa29307320ef7140120d83a) Thanks [@jayantasamaddar](https://github.com/jayantasamaddar)! - Add `readthrough`: generic read-through cache protection (singleflight, store lease, tombstones,
  package-enforced TTL jitter, stale-while-revalidate, per-class bulkhead, store circuit breaker,
  `Warm`, `RefreshAhead`) over any `cachestore.CacheStore`, with an `Observer` interface. Add
  `readthrough/prom`, the Prometheus `Observer`.
