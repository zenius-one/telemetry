package prom_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	cachestore "github.com/zenius-one/cachestore/packages/cachestore-go"

	"github.com/zenius-one/telemetry/packages/readthrough"
	"github.com/zenius-one/telemetry/packages/readthrough/prom"
)

func TestObserver_should_publish_cache_events_as_prometheus_series(t *testing.T) {
	cls := readthrough.Class{Name: "patient", BaseTTL: time.Minute, DBBudgetLoadsPerSecond: 50}
	reg := prometheus.NewRegistry()
	m := prom.New(reg, cls)
	c, err := readthrough.New(cachestore.NewMemoryStore(cachestore.MemoryOptions{}), []readthrough.Class{cls},
		readthrough.WithObserver(m.Observer()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 3 {
		if _, err := c.Get(ctx, "patient", "p1", func(context.Context) ([]byte, error) { return []byte("v"), nil }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Get(ctx, "patient", "ghost", func(context.Context) ([]byte, error) { return nil, readthrough.ErrNotFound }); !errors.Is(err, readthrough.ErrNotFound) {
		t.Fatal(err)
	}
	if n, err := testutil.GatherAndCount(reg, "readthrough_requests_total", "readthrough_db_loads_total"); err != nil || n == 0 {
		t.Fatalf("series = %d, %v", n, err)
	}
	want := `# HELP readthrough_db_loads_total Loader actually ran against the DB.
# TYPE readthrough_db_loads_total counter
readthrough_db_loads_total{class="other"} 0
readthrough_db_loads_total{class="patient"} 2
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "readthrough_db_loads_total"); err != nil {
		t.Fatal(err) // 2 = one value load + one not-found load; the other 2 reads were hits
	}
}
