package readthrough

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cachestore "github.com/zenius-one/cachestore/packages/cachestore-go"
)

// countingObserver records every event, for the metric-correctness assertions.
type countingObserver struct {
	mu      sync.Mutex
	counts  map[string]int
	writes  []time.Duration
	breaker int
}

func newObs() *countingObserver { return &countingObserver{counts: map[string]int{}} }

func (o *countingObserver) Class(string) ClassObserver { return o }
func (o *countingObserver) add(k string) {
	o.mu.Lock()
	o.counts[k]++
	o.mu.Unlock()
}
func (o *countingObserver) get(k string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.counts[k]
}
func (o *countingObserver) Request()          { o.add("request") }
func (o *countingObserver) Result(r string)   { o.add("result:" + r) }
func (o *countingObserver) Rejected(r string) { o.add("rejected:" + r) }
func (o *countingObserver) Flight(ran bool) {
	if ran {
		o.add("leaders")
	} else {
		o.add("suppressed")
	}
}
func (o *countingObserver) Lease(s string)    { o.add("lease:" + s) }
func (o *countingObserver) DBLoad() func()    { o.add("dbload"); return func() {} }
func (o *countingObserver) TombstoneWritten() { o.add("tombstone") }
func (o *countingObserver) SetBreaker(s int) {
	o.mu.Lock()
	o.breaker = s
	o.mu.Unlock()
}
func (o *countingObserver) ObserveWrite(d time.Duration) {
	o.mu.Lock()
	o.writes = append(o.writes, d)
	o.mu.Unlock()
}

func testClass() Class {
	return Class{Name: "patient", BaseTTL: time.Minute, Spread: 30 * time.Second,
		NegativeTTL: time.Minute, LoaderTimeout: 2 * time.Second, MaxConcurrentLoads: 64}
}

func newCache(t *testing.T, store cachestore.CacheStore, obs Observer, cls ...Class) *Cache {
	t.Helper()
	if len(cls) == 0 {
		cls = []Class{testClass()}
	}
	c, err := New(store, cls, WithObserver(obs))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func memStore() cachestore.CacheStore {
	return cachestore.NewMemoryStore(cachestore.MemoryOptions{MaxEntries: 100000})
}

func TestGet_should_collapse_a_herd_into_one_load(t *testing.T) {
	obs, calls := newObs(), atomic.Int64{}
	c := newCache(t, memStore(), obs)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 1000 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			v, err := c.Get(context.Background(), "patient", "k", func(context.Context) ([]byte, error) {
				calls.Add(1)
				time.Sleep(100 * time.Millisecond)
				return []byte("v"), nil
			})
			if err != nil || string(v) != "v" {
				t.Errorf("got %q, %v", v, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("loader calls = %d, want 1", calls.Load())
	}
	// Metric rules: leader is not "suppressed"; DB load counted once, at the call.
	if got := obs.get("leaders") + obs.get("suppressed"); got != 1000 {
		t.Fatalf("leaders+suppressed = %d, want 1000", got)
	}
	if obs.get("leaders") < 1 || obs.get("dbload") != 1 {
		t.Fatalf("leaders=%d dbload=%d", obs.get("leaders"), obs.get("dbload"))
	}
}

func TestGet_should_load_once_across_two_instances_sharing_a_store(t *testing.T) {
	store, calls := memStore(), atomic.Int64{}
	a, b := newCache(t, store, newObs()), newCache(t, store, newObs())
	load := func(context.Context) ([]byte, error) {
		calls.Add(1)
		time.Sleep(150 * time.Millisecond)
		return []byte("v"), nil
	}
	var wg sync.WaitGroup
	for i := range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := a
			if i%2 == 1 {
				c = b
			}
			if _, err := c.Get(context.Background(), "patient", "k", load); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("loader calls across 2 instances = %d, want 1", calls.Load())
	}
}

func TestGet_should_not_let_one_cancelled_caller_fail_the_herd(t *testing.T) {
	c := newCache(t, memStore(), newObs())
	first, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	load := func(ctx context.Context) ([]byte, error) {
		<-release
		return []byte("v"), ctx.Err()
	}
	errc := make(chan error, 1)
	go func() { _, err := c.Get(first, "patient", "k", load); errc <- err }()
	time.Sleep(50 * time.Millisecond)
	second := make(chan error, 1)
	go func() {
		v, err := c.Get(context.Background(), "patient", "k", load)
		if err == nil && string(v) != "v" {
			err = fmt.Errorf("got %q", v)
		}
		second <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller err = %v", err)
	}
	close(release)
	if err := <-second; err != nil {
		t.Fatalf("second caller failed: %v", err)
	}
}

func TestGet_should_tombstone_a_missing_key_and_bound_loader_calls(t *testing.T) {
	obs, calls := newObs(), atomic.Int64{}
	c := newCache(t, memStore(), obs)
	load := func(context.Context) ([]byte, error) { calls.Add(1); return nil, ErrNotFound }
	for range 500 {
		if _, err := c.Get(context.Background(), "patient", "ghost", load); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("loader calls = %d, want 1 (rest served from the tombstone)", calls.Load())
	}
	if obs.get("tombstone") != 1 || obs.get("result:negative_hit") != 499 {
		t.Fatalf("tombstone=%d negative_hit=%d", obs.get("tombstone"), obs.get("result:negative_hit"))
	}
}

func TestInvalidate_should_clear_a_tombstone_so_a_created_entity_is_found(t *testing.T) {
	c := newCache(t, memStore(), newObs())
	ctx := context.Background()
	if _, err := c.Get(ctx, "patient", "p1", func(context.Context) ([]byte, error) { return nil, ErrNotFound }); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// The entity is created; its write path invalidates after commit.
	if err := c.Invalidate(ctx, "patient", "p1"); err != nil {
		t.Fatal(err)
	}
	v, err := c.Get(ctx, "patient", "p1", func(context.Context) ([]byte, error) { return []byte("now"), nil })
	if err != nil || string(v) != "now" {
		t.Fatalf("got %q, %v", v, err)
	}
}

func TestGet_should_never_let_a_tombstone_overwrite_a_value(t *testing.T) {
	c := newCache(t, memStore(), newObs())
	ctx := context.Background()
	if err := c.Put(ctx, "patient", "k", []byte("real")); err != nil {
		t.Fatal(err)
	}
	c.writeTombstone(ctx, c.classes["patient"], "k")
	v, err := c.Get(ctx, "patient", "k", func(context.Context) ([]byte, error) { return nil, errors.New("must not load") })
	if err != nil || string(v) != "real" {
		t.Fatalf("got %q, %v", v, err)
	}
}

func TestGet_should_reject_malformed_keys_before_cache_and_loader(t *testing.T) {
	obs, calls := newObs(), atomic.Int64{}
	c := newCache(t, memStore(), obs)
	load := func(context.Context) ([]byte, error) { calls.Add(1); return []byte("x"), nil }
	for _, k := range []string{"", "bad\nkey", string(make([]byte, MaxKeyLen+1))} {
		if _, err := c.Get(context.Background(), "patient", k, load); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("key %q: err = %v", k, err)
		}
	}
	if calls.Load() != 0 || obs.get("request") != 0 || obs.get("rejected:malformed") != 3 {
		t.Fatalf("calls=%d request=%d rejected=%d", calls.Load(), obs.get("request"), obs.get("rejected:malformed"))
	}
	if _, err := c.Get(context.Background(), "nope", "k", load); !errors.Is(err, ErrUnknownClass) {
		t.Fatal(err)
	}
}

func TestGet_should_shed_beyond_the_bulkhead_instead_of_queueing(t *testing.T) {
	obs := newObs()
	cls := testClass()
	cls.MaxConcurrentLoads = 2
	c := newCache(t, memStore(), obs, cls)
	block, inflight, peak := make(chan struct{}), atomic.Int64{}, atomic.Int64{}
	load := func(context.Context) ([]byte, error) {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-block
		inflight.Add(-1)
		return []byte("v"), nil
	}
	var wg sync.WaitGroup
	var shed atomic.Int64
	for i := range 10 { // ten DISTINCT keys: singleflight cannot help
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Get(context.Background(), "patient", fmt.Sprintf("k%d", i), load)
			var se *ShedError
			if errors.Is(err, ErrShed) && errors.As(err, &se) && se.RetryAfter > 0 {
				shed.Add(1)
			}
		}()
	}
	time.Sleep(300 * time.Millisecond)
	close(block)
	wg.Wait()
	if peak.Load() > 2 {
		t.Fatalf("peak concurrent loads = %d, bulkhead is 2", peak.Load())
	}
	if shed.Load() != 8 {
		t.Fatalf("shed = %d, want 8", shed.Load())
	}
}

func TestPut_should_jitter_every_ttl_and_never_equal_the_base(t *testing.T) {
	obs := newObs()
	c := newCache(t, memStore(), obs)
	cls := c.classes["patient"]
	for i := range 10000 {
		if err := c.Put(context.Background(), "patient", fmt.Sprintf("k%d", i), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	distinct := map[time.Duration]bool{}
	buckets := map[int64]int{}
	for _, w := range obs.writes {
		if w < cls.BaseTTL || w > cls.BaseTTL+cls.EffectiveSpread() {
			t.Fatalf("ttl %v outside [%v, %v]", w, cls.BaseTTL, cls.BaseTTL+cls.EffectiveSpread())
		}
		distinct[w] = true
		buckets[int64(w/time.Second)]++
	}
	if len(distinct) < 1000 {
		t.Fatalf("only %d distinct TTLs for 10000 writes", len(distinct))
	}
	// 30 s spread over 10000 keys is ~333 keys/s; no single second may hold a cliff.
	for sec, n := range buckets {
		if n > 600 {
			t.Fatalf("%d keys expire in one second (%d s): expiry cliff", n, sec)
		}
	}
}

func TestEffectiveSpread_should_floor_at_ten_percent_of_base(t *testing.T) {
	cl := Class{Name: "x", BaseTTL: 30 * time.Minute, Spread: time.Second}
	if got := cl.EffectiveSpread(); got != 3*time.Minute {
		t.Fatalf("got %v", got)
	}
}

func TestGet_should_serve_stale_and_refresh_in_the_background(t *testing.T) {
	cls := testClass()
	cls.BaseTTL, cls.Spread, cls.StaleFor = 80*time.Millisecond, 1, time.Minute
	obs, version := newObs(), atomic.Int64{}
	c := newCache(t, memStore(), obs, cls)
	load := func(context.Context) ([]byte, error) { return []byte(fmt.Sprint("v", version.Add(1))), nil }
	ctx := context.Background()
	if v, _ := c.Get(ctx, "patient", "k", load); string(v) != "v1" {
		t.Fatalf("first = %q", v)
	}
	time.Sleep(cls.EffectiveSpread() + cls.BaseTTL + 50*time.Millisecond)
	v, err := c.Get(ctx, "patient", "k", load)
	if err != nil || string(v) != "v1" {
		t.Fatalf("stale read = %q, %v (want old value, no wait)", v, err)
	}
	if obs.get("result:stale") != 1 {
		t.Fatalf("stale results = %d", obs.get("result:stale"))
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		v, _ = c.Get(ctx, "patient", "k", load)
		if string(v) == "v2" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never refreshed, last = %q", v)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// downStore fails every call, like a dead Redis.
type downStore struct {
	cachestore.CacheStore
	calls atomic.Int64
}

func (d *downStore) fail() error { d.calls.Add(1); return errors.New("redis down") }
func (d *downStore) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, d.fail()
}
func (d *downStore) Set(context.Context, string, []byte, time.Duration) error { return d.fail() }
func (d *downStore) SetNX(context.Context, string, []byte, time.Duration) (bool, error) {
	return false, d.fail()
}
func (d *downStore) Delete(context.Context, ...string) error { return d.fail() }

func TestGet_should_open_the_breaker_and_cap_db_load_when_the_store_is_down(t *testing.T) {
	obs, down := newObs(), &downStore{}
	cls := testClass()
	cls.MaxConcurrentLoads = 3
	c, err := New(down, []Class{cls}, WithObserver(obs), WithBreaker(3, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	block, inflight, peak := make(chan struct{}), atomic.Int64{}, atomic.Int64{}
	load := func(context.Context) ([]byte, error) {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-block
		inflight.Add(-1)
		return []byte("v"), nil
	}
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Get(context.Background(), "patient", fmt.Sprintf("k%d", i), load)
		}()
	}
	time.Sleep(300 * time.Millisecond)
	close(block)
	wg.Wait()
	if peak.Load() > 3 {
		t.Fatalf("peak loads = %d with the store down; bulkhead is 3", peak.Load())
	}
	obs.mu.Lock()
	state := obs.breaker
	obs.mu.Unlock()
	if state != BreakerOpen {
		t.Fatalf("breaker state = %d, want open", state)
	}
	before := down.calls.Load()
	c.Get(context.Background(), "patient", "after", func(context.Context) ([]byte, error) { return []byte("v"), nil })
	if down.calls.Load() != before {
		t.Fatal("an open breaker must stop touching the store")
	}
}

func TestGet_should_not_count_a_db_load_when_the_value_appeared_before_the_lease(t *testing.T) {
	store, obs := memStore(), newObs()
	c := newCache(t, store, obs)
	other := newCache(t, store, newObs())
	_ = other.Put(context.Background(), "patient", "k", []byte("peer"))
	// Force the lead path directly, as if our GET had missed a moment before the peer wrote.
	f := c.lead(context.Background(), c.classes["patient"], "k", func(context.Context) ([]byte, error) {
		t.Error("loader must not run")
		return nil, nil
	}, false)
	if string(f.val) != "peer" || obs.get("dbload") != 0 {
		t.Fatalf("val=%q dbload=%d", f.val, obs.get("dbload"))
	}
}

func TestWarm_should_load_missing_keys_only_and_report(t *testing.T) {
	cls := testClass()
	cls.DBBudgetLoadsPerSecond = 2000
	c := newCache(t, memStore(), newObs(), cls)
	ctx := context.Background()
	_ = c.Put(ctx, "patient", "k0", []byte("cached"))
	keys := []string{"k0", "k1", "k2", "gone"}
	res, err := c.Warm(ctx, "patient", keys, func(_ context.Context, k string) ([]byte, error) {
		if k == "gone" {
			return nil, ErrNotFound
		}
		return []byte(k), nil
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if res != (WarmResult{Loaded: 2, AlreadyCached: 1, NotFound: 1}) {
		t.Fatalf("res = %+v", res)
	}
}

func TestRefreshAhead_should_reload_before_expiry(t *testing.T) {
	cls := testClass()
	cls.BaseTTL, cls.Spread = 2*time.Second, time.Millisecond
	c := newCache(t, memStore(), newObs(), cls)
	n := atomic.Int64{}
	stop, err := c.RefreshAhead("patient", "catalog", 100*time.Millisecond, func(context.Context) ([]byte, error) {
		return []byte(fmt.Sprint(n.Add(1))), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(450 * time.Millisecond)
	stop()
	if n.Load() < 3 {
		t.Fatalf("reloads = %d, want >= 3", n.Load())
	}
	if _, err := c.RefreshAhead("patient", "catalog", time.Hour, nil); err == nil {
		t.Fatal("interval >= BaseTTL must be rejected")
	}
}

func TestKeys(t *testing.T) {
	if a, b := JoinKey("a:b", "c"), JoinKey("a", "b:c"); a == b {
		t.Fatalf("collision: %q", a)
	}
	if JoinKey("o", "e", "id") != "o:e:id" {
		t.Fatal(JoinKey("o", "e", "id"))
	}
	h1, h2 := HashKey([]byte("s1"), "+911234"), HashKey([]byte("s2"), "+911234")
	if h1 == h2 || len(h1) != 64 || h1 == "+911234" {
		t.Fatalf("bad hash %q %q", h1, h2)
	}
}

func TestNew_should_reject_bad_declarations(t *testing.T) {
	for name, cls := range map[string][]Class{
		"no name":     {{BaseTTL: time.Second}},
		"no ttl":      {{Name: "x"}},
		"duplicate":   {{Name: "x", BaseTTL: time.Second}, {Name: "x", BaseTTL: time.Second}},
		"negative sp": {{Name: "x", BaseTTL: time.Second, Spread: -1}},
	} {
		if _, err := New(memStore(), cls); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, err := New(nil, nil); err == nil {
		t.Error("nil store: want error")
	}
}
