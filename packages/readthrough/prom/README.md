# readthrough/prom

Prometheus implementation of `readthrough.Observer`, in its own module so services that do not use Prometheus never pull `client_golang`.

```go
m := prom.New(prometheus.DefaultRegisterer, class1, class2) // the same readthrough.Class values
cache, _ := readthrough.New(store, classes, readthrough.WithObserver(m.Observer()))
```

Series: `readthrough_requests_total`, `_results_total{result}`, `_rejected_total{reason}`,
`_singleflight_leaders_total`, `_singleflight_suppressed_total`, `_lease_total{outcome}`,
`_db_loads_total`, `_db_inflight`, `_db_load_seconds`, `_tombstones_written_total`,
`_breaker_state`, `_ttl_remaining_seconds`, `_db_budget_loads_per_second`,
`_expected_bucket_share`, `_expiry_bucket_share_max`.

Labels are only `class` (+ `result`/`reason`/`outcome`). Undeclared classes collapse into
`class="other"`; keys and identifiers cannot be passed in. A nil `*Metrics` is inert.
