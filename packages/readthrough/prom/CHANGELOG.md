# @zenius-one/telemetry-readthrough-prom-go

## 0.1.1

### Patch Changes

- [#4](https://github.com/zenius-one/telemetry/pull/4) [`d9be7cf`](https://github.com/zenius-one/telemetry/commit/d9be7cfae6d515ded507ca9fc13d4be44613b4e1) Thanks [@jayantasamaddar](https://github.com/jayantasamaddar)! - Require the released `readthrough` v0.1.0 and drop the bootstrap `replace`, so the module builds for consumers (v0.1.0 required an untagged `readthrough v0.0.0`).

## 0.1.0

### Minor Changes

- [#2](https://github.com/zenius-one/telemetry/pull/2) [`9e2937b`](https://github.com/zenius-one/telemetry/commit/9e2937b10d6f32d26fa29307320ef7140120d83a) Thanks [@jayantasamaddar](https://github.com/jayantasamaddar)! - Add `readthrough`: generic read-through cache protection (singleflight, store lease, tombstones,
  package-enforced TTL jitter, stale-while-revalidate, per-class bulkhead, store circuit breaker,
  `Warm`, `RefreshAhead`) over any `cachestore.CacheStore`, with an `Observer` interface. Add
  `readthrough/prom`, the Prometheus `Observer`.
