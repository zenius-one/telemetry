// Package prom is the Prometheus implementation of readthrough.Observer. It lives in its own
// module so a service that does not use Prometheus never pulls client_golang.
//
// Cardinality is bounded by construction: the only free-form label is `class`, and a class must be
// declared up front. Anything undeclared collapses into class="other". Cache keys, tenant IDs and
// any identifier are impossible to pass in -- the readthrough.ClassObserver interface has no
// parameter for them.
package prom

import (
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/zenius-one/telemetry/packages/readthrough"
)

const (
	otherClass = "other"
	// bucketWidth is the width of the expiry-concentration window (D6a, D9.3 row 4).
	bucketWidth = 10 * time.Second
	// sampleCap bounds memory for the expiry sampler per class.
	sampleCap = 2048
	// minSamples: below this the share is statistically meaningless and reported as 0.
	minSamples = 100
)

var (
	validResults = map[string]bool{readthrough.ResultHit: true, readthrough.ResultNegativeHit: true, readthrough.ResultStale: true,
		readthrough.ResultMissLoaded: true, readthrough.ResultMissNoFound: true, readthrough.ResultShed: true, readthrough.ResultError: true}
	validRejects = map[string]bool{readthrough.RejectMalformed: true, readthrough.RejectUnauthorised: true}
	validLeases  = map[string]bool{readthrough.LeaseLeader: true, readthrough.LeaseWaiterServed: true, readthrough.LeaseWaiterTimeout: true}
)

// Metrics is the collection of read-through series. A nil *Metrics (and the *ClassMetrics it
// hands out) is valid and inert, so callers never need an "is monitoring on" branch.
type Metrics struct {
	requests, singleflightLeaders, singleflightSuppressed, dbLoads, tombstones *prometheus.CounterVec
	results, rejected, lease                                                   *prometheus.CounterVec
	dbInflight, breaker                                                        *prometheus.GaugeVec
	dbLoadSeconds, ttlRemaining                                                *prometheus.HistogramVec

	classes map[string]*ClassMetrics
}

// New registers the series on reg and returns Metrics for the declared classes. The declared
// readthrough.Class supplies the DB budget and (effective) jitter spread the alerts are measured
// against. Use Observer to hand it to readthrough.New.
func New(reg prometheus.Registerer, classes ...readthrough.Class) *Metrics {
	cv := func(name, help string, labels ...string) *prometheus.CounterVec {
		return prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
	}
	m := &Metrics{
		requests:               cv("readthrough_requests_total", "Lookups entering the component after D2 validation (demand denominator).", "class"),
		results:                cv("readthrough_results_total", "Lookup outcomes.", "class", "result"),
		rejected:               cv("readthrough_rejected_total", "D2 rejections stopped before cache or DB.", "class", "reason"),
		singleflightLeaders:    cv("readthrough_singleflight_leaders_total", "Callers that executed the loader path (one per collapsed group).", "class"),
		singleflightSuppressed: cv("readthrough_singleflight_suppressed_total", "Callers that waited and reused a leader's result.", "class"),
		lease:                  cv("readthrough_lease_total", "Cross-pod lease outcomes (D5).", "class", "outcome"),
		dbLoads:                cv("readthrough_db_loads_total", "Loader actually ran against the DB.", "class"),
		tombstones:             cv("readthrough_tombstones_written_total", "Negative entries created (D3).", "class"),
		dbInflight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "readthrough_db_inflight", Help: "Concurrent DB loads (compare with the bulkhead limit)."}, []string{"class"}),
		breaker: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "readthrough_breaker_state", Help: "0 closed, 1 half-open, 2 open."}, []string{"class"}),
		dbLoadSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "readthrough_db_load_seconds", Help: "Loader latency.",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5}}, []string{"class"}),
		ttlRemaining: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "readthrough_ttl_remaining_seconds", Help: "TTL assigned to recent writes (expiry-distribution view, D6a).",
			Buckets: ttlBuckets()}, []string{"class"}),
		classes: map[string]*ClassMetrics{},
	}
	reg.MustRegister(m.requests, m.results, m.rejected, m.singleflightLeaders, m.singleflightSuppressed,
		m.lease, m.dbLoads, m.tombstones, m.dbInflight, m.breaker, m.dbLoadSeconds, m.ttlRemaining)

	declared := append([]readthrough.Class{{Name: otherClass}}, classes...)
	budget := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "readthrough_db_budget_loads_per_second", Help: "Declared per-class DB budget L (ADR-0029 D6a)."}, []string{"class"})
	expected := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "readthrough_expected_bucket_share", Help: "Expected share of keys per 10 s expiry bucket given the declared jitter spread."}, []string{"class"})
	reg.MustRegister(budget, expected)

	for _, c := range declared {
		if _, dup := m.classes[c.Name]; dup {
			continue
		}
		cm := &ClassMetrics{m: m, name: c.Name}
		m.classes[c.Name] = cm
		// Touch every series so rate() has a zero baseline and dashboards show the class
		// before the first request.
		m.requests.WithLabelValues(c.Name)
		m.singleflightLeaders.WithLabelValues(c.Name)
		m.singleflightSuppressed.WithLabelValues(c.Name)
		m.dbLoads.WithLabelValues(c.Name)
		m.tombstones.WithLabelValues(c.Name)
		m.dbInflight.WithLabelValues(c.Name)
		m.breaker.WithLabelValues(c.Name).Set(readthrough.BreakerClosed)
		m.dbLoadSeconds.WithLabelValues(c.Name)
		m.ttlRemaining.WithLabelValues(c.Name)
		budget.WithLabelValues(c.Name).Set(c.DBBudgetLoadsPerSecond)
		share := 1.0
		if c.EffectiveSpread() > bucketWidth {
			share = float64(bucketWidth) / float64(c.EffectiveSpread())
		}
		expected.WithLabelValues(c.Name).Set(share)
	}

	names := make([]string, 0, len(m.classes))
	for n := range m.classes {
		names = append(names, n)
	}
	sort.Strings(names)
	reg.MustRegister(&expiryCollector{m: m, names: names})
	return m
}

func ttlBuckets() []float64 {
	// 10 s resolution for the first 10 minutes (where a cliff is visible), then coarse.
	b := make([]float64, 0, 70)
	for s := 10; s <= 600; s += 10 {
		b = append(b, float64(s))
	}
	return append(b, 900, 1800, 3600, 7200, 21600, 86400)
}

// Class returns the metrics handle for a declared class; undeclared names map to "other".
// Safe on a nil receiver.
func (m *Metrics) Class(name string) *ClassMetrics {
	if m == nil {
		return nil
	}
	if c, ok := m.classes[name]; ok {
		return c
	}
	return m.classes[otherClass]
}

// ClassMetrics is the per-class handle. A nil receiver is a no-op.
type ClassMetrics struct {
	m    *Metrics
	name string

	mu      sync.Mutex
	samples []int64 // absolute expiry unix-nanos of recent writes (ring)
	next    int
}

// Request counts one lookup entering the component (after D2 validation).
func (c *ClassMetrics) Request() {
	if c == nil {
		return
	}
	c.m.requests.WithLabelValues(c.name).Inc()
}

// Result counts a lookup outcome. Unknown results are dropped to keep labels bounded.
func (c *ClassMetrics) Result(result string) {
	if c == nil || !validResults[result] {
		return
	}
	c.m.results.WithLabelValues(c.name, result).Inc()
}

// Rejected counts a D2 rejection. These never reach Request().
func (c *ClassMetrics) Rejected(reason string) {
	if c == nil || !validRejects[reason] {
		return
	}
	c.m.rejected.WithLabelValues(c.name, reason).Inc()
}

// Flight records a caller's role in a singleflight group. ADR-0029 D9.1 counting rule 1:
// singleflight's `shared` flag is true for the leader too, so it must NOT be used. Set a
// local `ranLoader` flag inside the loader closure and pass it here:
//
//	ranLoader := false
//	v, err, _ := sf.Do(key, func() (any, error) { ranLoader = true; return load() })
//	cm.Flight(ranLoader)
func (c *ClassMetrics) Flight(ranLoader bool) {
	if c == nil {
		return
	}
	if ranLoader {
		c.m.singleflightLeaders.WithLabelValues(c.name).Inc()
	} else {
		c.m.singleflightSuppressed.WithLabelValues(c.name).Inc()
	}
}

// Lease counts a D5 lease outcome.
func (c *ClassMetrics) Lease(outcome string) {
	if c == nil || !validLeases[outcome] {
		return
	}
	c.m.lease.WithLabelValues(c.name, outcome).Inc()
}

// DBLoad marks the start of a real DB load and returns the func to call when it ends.
// ADR-0029 D9.1 counting rule 2: call this at the DB call itself -- not at singleflight entry
// and not when the leader found the value already in Redis.
func (c *ClassMetrics) DBLoad() (done func()) {
	if c == nil {
		return func() {}
	}
	start := time.Now()
	c.m.dbLoads.WithLabelValues(c.name).Inc()
	c.m.dbInflight.WithLabelValues(c.name).Inc()
	return func() {
		c.m.dbInflight.WithLabelValues(c.name).Dec()
		c.m.dbLoadSeconds.WithLabelValues(c.name).Observe(time.Since(start).Seconds())
	}
}

// TombstoneWritten counts a D3 negative entry.
func (c *ClassMetrics) TombstoneWritten() {
	if c == nil {
		return
	}
	c.m.tombstones.WithLabelValues(c.name).Inc()
}

// SetBreaker publishes the Redis circuit-breaker state (Breaker* constants).
func (c *ClassMetrics) SetBreaker(state int) {
	if c == nil {
		return
	}
	c.m.breaker.WithLabelValues(c.name).Set(float64(state))
}

// ObserveWrite records the TTL given to a cache write (D6a). Call it for every write,
// including each key of a bulk/pipeline write.
func (c *ClassMetrics) ObserveWrite(ttl time.Duration) {
	if c == nil {
		return
	}
	c.m.ttlRemaining.WithLabelValues(c.name).Observe(ttl.Seconds())
	exp := time.Now().Add(ttl).UnixNano()
	c.mu.Lock()
	if len(c.samples) < sampleCap {
		c.samples = append(c.samples, exp)
	} else {
		c.samples[c.next] = exp
		c.next = (c.next + 1) % sampleCap
	}
	c.mu.Unlock()
}

// maxBucketShare is the largest share of still-live sampled keys expiring within any single
// 10 s window -- the readthrough_expiry_bucket_share_max signal behind CacheExpiryConcentration.
func (c *ClassMetrics) maxBucketShare(now time.Time) float64 {
	c.mu.Lock()
	live := make([]int64, 0, len(c.samples))
	for _, e := range c.samples {
		if e > now.UnixNano() {
			live = append(live, e)
		}
	}
	c.mu.Unlock()
	if len(live) < minSamples {
		return 0
	}
	sort.Slice(live, func(i, j int) bool { return live[i] < live[j] })
	best, lo := 0, 0
	for hi := range live {
		for live[hi]-live[lo] > int64(bucketWidth) {
			lo++
		}
		if n := hi - lo + 1; n > best {
			best = n
		}
	}
	return float64(best) / float64(len(live))
}

// expiryCollector computes readthrough_expiry_bucket_share_max at scrape time.
type expiryCollector struct {
	m     *Metrics
	names []string
}

var expiryDesc = prometheus.NewDesc("readthrough_expiry_bucket_share_max",
	"Largest share of sampled live keys expiring inside one 10 s window (0 when too few samples).",
	[]string{"class"}, nil)

func (e *expiryCollector) Describe(ch chan<- *prometheus.Desc) { ch <- expiryDesc }
func (e *expiryCollector) Collect(ch chan<- prometheus.Metric) {
	now := time.Now()
	for _, n := range e.names {
		ch <- prometheus.MustNewConstMetric(expiryDesc, prometheus.GaugeValue, e.m.classes[n].maxBucketShare(now), n)
	}
}

// Observer adapts m to readthrough.Observer.
func (m *Metrics) Observer() readthrough.Observer { return observer{m} }

type observer struct{ m *Metrics }

func (o observer) Class(name string) readthrough.ClassObserver { return o.m.Class(name) }

var _ readthrough.ClassObserver = (*ClassMetrics)(nil)
