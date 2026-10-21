package prom

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/zenius-one/telemetry/packages/readthrough"
)

func newTest(t *testing.T) (*Metrics, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	return New(reg, readthrough.Class{Name: "patient", BaseTTL: 30 * time.Minute, DBBudgetLoadsPerSecond: 50, Spread: 5 * time.Minute}), reg
}

func TestFlight_should_count_only_non_loaders_as_suppressed(t *testing.T) {
	m, _ := newTest(t)
	c := m.Class("patient")
	c.Flight(true) // the leader (ranLoader) -- singleflight's shared==true must not be used
	for range 999 {
		c.Flight(false)
	}
	if got := testutil.ToFloat64(m.singleflightLeaders.WithLabelValues("patient")); got != 1 {
		t.Fatalf("leaders = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.singleflightSuppressed.WithLabelValues("patient")); got != 999 {
		t.Fatalf("suppressed = %v, want 999", got)
	}
}

func TestDBLoad_should_count_only_when_the_db_call_is_made(t *testing.T) {
	m, _ := newTest(t)
	c := m.Class("patient")
	c.Flight(true) // leader found the value in Redis: no DBLoad() call
	if got := testutil.ToFloat64(m.dbLoads.WithLabelValues("patient")); got != 0 {
		t.Fatalf("db_loads = %v, want 0 for a leader that hit Redis", got)
	}
	done := c.DBLoad()
	if got := testutil.ToFloat64(m.dbInflight.WithLabelValues("patient")); got != 1 {
		t.Fatalf("inflight = %v, want 1 during load", got)
	}
	done()
	if got := testutil.ToFloat64(m.dbInflight.WithLabelValues("patient")); got != 0 {
		t.Fatalf("inflight = %v, want 0 after load", got)
	}
	if got := testutil.ToFloat64(m.dbLoads.WithLabelValues("patient")); got != 1 {
		t.Fatalf("db_loads = %v, want 1", got)
	}
}

func TestLabels_should_stay_bounded_when_classes_and_values_are_attacker_controlled(t *testing.T) {
	m, reg := newTest(t)
	for i := range 500 {
		c := m.Class("tenant-" + strings.Repeat("x", i%7) + string(rune('a'+i%26))) // undeclared
		c.Request()
		c.Result("made-up-result")
		c.Rejected("made-up-reason")
		c.Lease("made-up-outcome")
	}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if n := len(mf.GetMetric()); n > 40 {
			t.Errorf("%s has %d series, label cardinality is not bounded", mf.GetName(), n)
		}
		for _, mm := range mf.GetMetric() {
			for _, lp := range mm.GetLabel() {
				if lp.GetName() == "class" && lp.GetValue() != "patient" && lp.GetValue() != "other" {
					t.Errorf("%s leaked class label %q", mf.GetName(), lp.GetValue())
				}
			}
		}
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues("other")); got != 500 {
		t.Fatalf("other requests = %v, want 500", got)
	}
}

func TestNilMetrics_should_be_inert_when_monitoring_is_off(t *testing.T) {
	var m *Metrics
	c := m.Class("patient")
	c.Request()
	c.Result(readthrough.ResultHit)
	c.Flight(true)
	c.DBLoad()()
	c.ObserveWrite(time.Minute)
	c.SetBreaker(readthrough.BreakerOpen)
}

func TestMaxBucketShare_should_flag_synchronised_expiry(t *testing.T) {
	m, _ := newTest(t)
	synced, jittered := m.Class("patient"), m.Class("other")
	for range 1000 {
		synced.ObserveWrite(30 * time.Minute) // fixed TTL: the cliff
	}
	for i := range 1000 {
		jittered.ObserveWrite(30*time.Minute + time.Duration(i)*300*time.Millisecond) // spread over 5 min
	}
	now := time.Now()
	if got := synced.maxBucketShare(now); got < 0.99 {
		t.Errorf("fixed-TTL share = %v, want ~1", got)
	}
	if got := jittered.maxBucketShare(now); got > 0.1 {
		t.Errorf("jittered share = %v, want ~10s/300s=0.033", got)
	}
}

func TestMaxBucketShare_should_report_zero_when_too_few_samples(t *testing.T) {
	m, _ := newTest(t)
	c := m.Class("patient")
	for range 10 {
		c.ObserveWrite(time.Hour)
	}
	if got := c.maxBucketShare(time.Now()); got != 0 {
		t.Fatalf("share = %v, want 0 below minSamples", got)
	}
}

func TestExpectedShare_should_derive_from_declared_spread(t *testing.T) {
	_, reg := newTest(t)
	got, err := testutil.GatherAndCount(reg, "readthrough_expected_bucket_share")
	if err != nil || got != 2 { // patient + other
		t.Fatalf("expected 2 series, got %d (%v)", got, err)
	}
}
