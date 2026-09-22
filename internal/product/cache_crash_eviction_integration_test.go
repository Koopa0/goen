//go:build integration

package product_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	valkey "github.com/valkey-io/valkey-go"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/product"
)

// TestCacheFillOwnerProcess is selected only by the crash test's subprocess.
func TestCacheFillOwnerProcess(t *testing.T) {
	if os.Getenv("GOEN_CACHE_OWNER_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	childPool, err := pgxpool.New(t.Context(), os.Getenv("GOEN_CACHE_OWNER_DATABASE"))
	if err != nil {
		t.Fatal(err)
	}
	defer childPool.Close()
	cache := openCacheOnAddr(t, os.Getenv("GOEN_CACHE_OWNER_VALKEY"), product.DefaultCacheConfig())
	defer cache.Close()
	product.SetIntegrationFillPause(func(ctx context.Context) error {
		if err := os.WriteFile("ready", []byte("lease acquired"), 0600); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	})
	defer product.SetIntegrationFillPause(nil)
	server := cacheHTTPServer(t, childPool, cache)
	if err := os.WriteFile("url", []byte(server.URL), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-t.Context().Done():
	case <-time.After(40 * time.Second):
		t.Fatal("fill owner was not killed")
	}
}

func cacheHTTPServer(t *testing.T, db *pgxpool.Pool, cache *product.PresentationCache) *httptest.Server {
	t.Helper()
	handler := product.NewHandler(product.NewStoreWithCache(db, cache), testLogger(), "http://goen.example")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /p/{slug}", func(w http.ResponseWriter, r *http.Request) {
		handler.Detail(w, r.WithContext(i18n.WithLocale(r.Context(), i18n.ZhHant)))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

type cacheHTTPResult struct {
	status           int
	body, retryAfter string
	err              error
}

func requestCachedPage(ctx context.Context, url string) cacheHTTPResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return cacheHTTPResult{err: err}
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return cacheHTTPResult{err: err}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return cacheHTTPResult{status: response.StatusCode, body: string(body), retryAfter: response.Header.Get("Retry-After"), err: err}
}

func requireCachedPage(t *testing.T, url, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := requestCachedPage(ctx, url)
	if result.err != nil || result.status != http.StatusOK || !strings.Contains(result.body, ">"+name+"<") {
		t.Fatalf("product HTTP status=%d error=%v, want 200 with %q", result.status, result.err, name)
	}
}

func cacheControl(t *testing.T, addr string) valkey.Client {
	t.Helper()
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{addr}, DisableCache: true, ForceSingleClient: true, DisableRetry: true, DisableAutoPipelining: true, ConnWriteTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func cacheKeys(t *testing.T, client valkey.Client, pattern string) []string {
	t.Helper()
	keys, err := client.Do(t.Context(), client.B().Keys().Pattern(pattern).Build()).AsStrSlice()
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func cachePTTL(t *testing.T, client valkey.Client, key string) int64 {
	t.Helper()
	ttl, err := client.Do(t.Context(), client.B().Pttl().Key(key).Build()).AsInt64()
	if err != nil {
		t.Fatal(err)
	}
	return ttl
}

func waitOwnerFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(path) //nolint:gosec // G304: private test-owned IPC path
		if err == nil && len(body) > 0 {
			return string(body)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("owner did not write IPC file %s", filepath.Base(path))
	return ""
}

func startFillOwner(t *testing.T, addr string) (command *exec.Cmd, url, ready string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	urlFile, readyFile := filepath.Join(dir, "url"), filepath.Join(dir, "ready")
	log, err := os.Create(filepath.Join(dir, "owner.log")) //nolint:gosec // G304: log is inside this test's private temporary directory
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestCacheFillOwnerProcess$", "-test.timeout=45s") //nolint:gosec // G204: executes only this test binary with fixed test selection
	cmd.Env = append(os.Environ(), "GOEN_CACHE_OWNER_HELPER=1", "GOEN_CACHE_OWNER_DATABASE="+pool.Config().ConnString(), "GOEN_CACHE_OWNER_VALKEY="+addr)
	// Fixed IPC names stay inside the private directory chosen by the parent.
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		if t.Failed() {
			body, _ := os.ReadFile(filepath.Join(dir, "owner.log")) //nolint:gosec // G304: reads only this test's subprocess log
			t.Logf("owner output: %s", body)
		}
	})
	return cmd, waitOwnerFile(t, urlFile), readyFile
}

func TestKilledFillOwnerExpiresAndRecoversOverHTTP(t *testing.T) {
	evidence := cacheRecoveryArtifact(t, "killed-fill-owner")
	fixture := newCacheProduct(t)
	addr := dbtest.Valkey(t)
	control := cacheControl(t, addr)
	survivor := openCacheOnAddr(t, addr, product.DefaultCacheConfig())
	defer survivor.Close()
	survivorServer := cacheHTTPServer(t, pool, survivor)
	owner, ownerURL, ready := startFillOwner(t, addr)
	ownerResult := make(chan cacheHTTPResult, 1)
	ownerCtx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	go func() { ownerResult <- requestCachedPage(ownerCtx, ownerURL+"/p/"+fixture.slug) }()
	waitOwnerFile(t, ready)
	leases := cacheKeys(t, control, "goen:product:"+fixture.id.String()+":*:lease")
	if len(leases) != 1 {
		t.Fatalf("fill owner leases=%v, want exactly one", leases)
	}
	lease := leases[0]
	ttlBefore := cachePTTL(t, control, lease)
	evidence["observed_lease_pttl_before_kill_ms"] = ttlBefore
	evidence["owner_pid"] = owner.Process.Pid
	if ttlBefore <= 7000 {
		t.Fatalf("default 10-second lease has only %dms before kill", ttlBefore)
	}
	if err := owner.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	waitErr := owner.Wait()
	exitErr, signalled := errors.AsType[*exec.ExitError](waitErr)
	if !signalled || exitErr.ExitCode() != -1 {
		t.Fatalf("owner did not exit from a signal: %v", waitErr)
	}
	evidence["owner_killed_by_signal"] = true
	select {
	case result := <-ownerResult:
		if result.err == nil {
			t.Fatalf("killed owner's HTTP request completed: status=%d", result.status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("owner HTTP connection survived process death")
	}
	if ttl := cachePTTL(t, control, lease); ttl <= 0 {
		t.Fatalf("lease was released on SIGKILL: TTL=%d", ttl)
	}
	target := survivorServer.URL + "/p/" + fixture.slug
	started := time.Now()
	result := requestCachedPage(ownerCtx, target)
	elapsed := time.Since(started)
	evidence["orphan_lease_http_status"] = result.status
	evidence["orphan_lease_retry_after"] = result.retryAfter
	evidence["orphan_lease_elapsed_ms"] = elapsed.Milliseconds()
	if result.err != nil || result.status != http.StatusServiceUnavailable || result.retryAfter != "1" {
		t.Fatalf("orphan-lease response=%d retry=%q error=%v", result.status, result.retryAfter, result.err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("default 3-second fill budget took %v", elapsed)
	}
	if stats := product.CacheStatsOf(survivor); stats.Fills != 0 || stats.Fallbacks != 0 || stats.Filling != 0 || stats.Loads != 0 {
		t.Fatalf("orphan lease escaped bounded waiting: %+v", stats)
	}
	remaining := cachePTTL(t, control, lease)
	evidence["lease_pttl_after_bounded_response_ms"] = remaining
	if remaining <= 0 {
		t.Fatalf("wait did not fail before lease expiry: TTL=%d", remaining)
	}
	deadline := time.Now().Add(product.DefaultCacheConfig().LeaseTTL + 2*time.Second)
	for cachePTTL(t, control, lease) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("killed owner's lease did not expire")
		}
		time.Sleep(25 * time.Millisecond)
	}
	requireCachedPage(t, target, "Before")
	requireCachedPage(t, target, "Before")
	if stats := product.CacheStatsOf(survivor); stats.Fills != 1 || stats.Hits < 1 {
		t.Fatalf("same surviving instance failed to refill and hit: %+v", stats)
	}
	editCacheProduct(t, fixture, false)
	requireCachedPage(t, target, "After")
	evidence["same_instance_recovered"] = true
	evidence["committed_edit_visible"] = true
	evidence["recovery_stats"] = product.CacheStatsOf(survivor)
	t.Logf("SIGKILL pid=%d initial lease PTTL=%dms; default-budget 503 in %v; same HTTP instance refilled after expiry and selected committed edit", owner.Process.Pid, ttlBefore, elapsed)
}

func cacheInfoInt(t *testing.T, control valkey.Client, section, field string) int64 {
	t.Helper()
	info, err := control.Do(t.Context(), control.B().Arbitrary("INFO").Args(section).Build()).ToString()
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(info, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && key == field {
			number, parseErr := strconv.ParseInt(value, 10, 64)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			return number
		}
	}
	t.Fatalf("INFO %s missing %s", section, field)
	return 0
}

func setCacheMemory(t *testing.T, control valkey.Client, limit int64) {
	t.Helper()
	for key, value := range map[string]string{"maxmemory": strconv.FormatInt(limit, 10), "maxmemory-policy": "allkeys-lru", "maxmemory-samples": "10"} {
		if err := control.Do(t.Context(), control.B().Arbitrary("CONFIG").Args("SET", key, value).Build()).Error(); err != nil {
			t.Fatal(err)
		}
	}
	if got := cacheInfoInt(t, control, "memory", "maxmemory"); got != limit {
		t.Fatalf("maxmemory=%d, want %d", got, limit)
	}
}

func TestRealValkeyEvictionRebuildsOverHTTP(t *testing.T) {
	evidence := cacheRecoveryArtifact(t, "real-valkey-eviction")
	fixture := newCacheProduct(t)
	addr := dbtest.Valkey(t)
	control := cacheControl(t, addr)
	cache := openCacheOnAddr(t, addr, product.DefaultCacheConfig())
	defer cache.Close()
	server := cacheHTTPServer(t, pool, cache)
	target := server.URL + "/p/" + fixture.slug
	requireCachedPage(t, target, "Before")
	requireCachedPage(t, target, "Before")
	pattern := "goen:product:" + fixture.id.String() + ":*"
	keys := cacheKeys(t, control, pattern)
	if len(keys) != 1 || strings.HasSuffix(keys[0], ":lease") {
		t.Fatalf("warm payload keys=%v", keys)
	}
	if ttl := cachePTTL(t, control, keys[0]); ttl < 60000 {
		t.Fatalf("warm payload TTL=%dms, cannot distinguish expiry", ttl)
	}
	before := product.CacheStatsOf(cache)
	evictedBefore := cacheInfoInt(t, control, "stats", "evicted_keys")
	expiredBefore := cacheInfoInt(t, control, "stats", "expired_keys")
	used := cacheInfoInt(t, control, "memory", "used_memory")
	limit := used + (256 << 10)
	evidence["used_memory_before_bytes"] = used
	evidence["maxmemory_bytes"] = limit
	evidence["evicted_keys_before"] = evictedBefore
	evidence["expired_keys_before"] = expiredBefore
	evidence["policy"] = "allkeys-lru"
	setCacheMemory(t, control, limit)
	// LRU uses a coarse clock; pressure keys must be newer than the warm page.
	time.Sleep(1100 * time.Millisecond)
	pressure := strings.Repeat("p", 16<<10)
	writes := 0
	deadline := time.Now().Add(20 * time.Second)
	for ; writes < 512 && time.Now().Before(deadline); writes++ {
		key := fmt.Sprintf("eviction-pressure:%d", writes)
		if err := control.Do(t.Context(), control.B().Set().Key(key).Value(pressure).Build()).Error(); err != nil {
			t.Fatal(err)
		}
		// KEYS does not read the payload and refresh its LRU age.
		if len(cacheKeys(t, control, pattern)) == 0 {
			break
		}
	}
	missing := len(cacheKeys(t, control, pattern)) == 0
	evidence["payload_missing_under_pressure"] = missing
	evidence["pressure_write_count"] = min(writes+1, 512)
	if !missing {
		t.Fatal("payload was not evicted within 512 writes / 8 MiB / 20 seconds")
	}
	evictedAfter := cacheInfoInt(t, control, "stats", "evicted_keys")
	evidence["evicted_keys_after"] = evictedAfter
	if evictedAfter <= evictedBefore {
		t.Fatalf("missing real eviction: before=%d after=%d", evictedBefore, evictedAfter)
	}
	expired := cacheInfoInt(t, control, "stats", "expired_keys")
	evidence["expired_keys_after"] = expired
	if expired != expiredBefore {
		t.Fatalf("keys expired during eviction probe: before=%d after=%d", expiredBefore, expired)
	}
	requireCachedPage(t, target, "Before")
	requireCachedPage(t, target, "Before")
	after := product.CacheStatsOf(cache)
	evidence["stats_before_eviction"] = before
	evidence["stats_after_recovery"] = after
	if after.Fills != before.Fills+1 || after.Hits <= before.Hits {
		t.Fatalf("same instance did not rebuild and hit after eviction: before=%+v after=%+v", before, after)
	}
	var stock int
	if err := pool.QueryRow(t.Context(), `SELECT stock_quantity FROM product_variants WHERE product_id=$1`, fixture.id).Scan(&stock); err != nil {
		t.Fatal(err)
	}
	evidence["stock_after_recovery"] = stock
	if stock != 10 {
		t.Fatalf("presentation eviction changed stock to %d", stock)
	}
	evidence["same_instance_recovered"] = true
	t.Logf("allkeys-lru maxmemory=%d bytes used-before=%d pressure-writes=%d; evicted_keys=%d->%d expired_keys=%d; same HTTP instance rebuilt and hit", limit, used, writes+1, evictedBefore, evictedAfter, expiredBefore)
}

func cacheRecoveryArtifact(t *testing.T, scenario string) map[string]any {
	t.Helper()
	cfg := product.DefaultCacheConfig()
	data := map[string]any{"scenario": scenario, "tested_sha": os.Getenv("GOEN_CACHE_RECOVERY_SHA"), "default_lease_ttl_ms": cfg.LeaseTTL.Milliseconds(), "default_fill_budget_ms": cfg.FillBudget.Milliseconds()}
	if os.Getenv("GOEN_WRITE_CACHE_RECOVERY_ARTIFACTS") != "1" {
		return data
	}
	sha := os.Getenv("GOEN_CACHE_RECOVERY_SHA")
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(sha) {
		t.Fatal("cache artifact requires the exact tested SHA")
	}
	t.Cleanup(func() {
		data["passed"] = !t.Failed()
		encoded, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			t.Errorf("encode cache recovery evidence: %v", err)
			return
		}
		if err := os.MkdirAll("artifacts", 0750); err != nil {
			t.Errorf("create cache recovery artifact directory: %v", err)
			return
		}
		root, err := os.OpenRoot("artifacts")
		if err != nil {
			t.Errorf("open cache recovery artifact root: %v", err)
			return
		}
		defer root.Close()
		if err := root.MkdirAll(sha, 0750); err != nil {
			t.Errorf("create tested-SHA evidence directory: %v", err)
			return
		}
		if err := root.WriteFile(filepath.Join(sha, scenario+".json"), encoded, 0600); err != nil {
			t.Errorf("write cache recovery evidence: %v", err)
		}
	})
	return data
}
