package product

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	valkey "github.com/valkey-io/valkey-go"
)

// ErrOverloaded is returned when cache failure recovery has exhausted its
// bounded presentation admission or work budget for this request.
var ErrOverloaded = errors.New("product: cache fallback budget exhausted")

// integrationFillPause is set only by integration acceptance tests.
var integrationFillPause func(context.Context) error

// CacheMetrics counts cache behaviour without customer-identifying labels.
type CacheMetrics struct {
	Hits          atomic.Uint64
	Misses        atomic.Uint64
	Errors        atomic.Uint64
	Fills         atomic.Uint64
	Coalesced     atomic.Uint64
	Fallbacks     atomic.Uint64
	Evictions     atomic.Uint64
	RejectedBytes atomic.Uint64
	Rejected      atomic.Uint64
}

// PresentationCache is a Valkey-backed cache-aside store for Presentation.
type PresentationCache struct {
	client    valkey.Client
	mu        sync.Mutex
	calls     map[string]*presentationCall
	waiters   int
	loads     chan struct{}
	gets      chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
	metrics   *CacheMetrics

	leaseTTL        time.Duration
	payloadTTL      time.Duration
	maxPayloadBytes int
	maxWaiters      int
	maxFallback     int
	maxFills        int
	operationBudget time.Duration
	fillBudget      time.Duration

	fallbackMu sync.Mutex
	fallbackIn int

	disabled bool
}

// presentationCall shares one completion channel, so cancelled followers leave
// no per-caller channels attached to a still-running fill.
type presentationCall struct {
	done         chan struct{}
	presentation Presentation
	err          error
}

// CacheConfig tunes the shared presentation cache.
type CacheConfig struct {
	LeaseTTL        time.Duration
	PayloadTTL      time.Duration
	MaxPayloadBytes int
	// MaxWaiters bounds local followers across all in-progress keys.
	MaxWaiters  int
	MaxFallback int
	// MaxFills bounds owners, including owners waiting on a shared lease.
	MaxFills int
	// MaxRequests bounds active page loads before metadata reads and cache calls.
	MaxRequests     int
	OperationBudget time.Duration
	FillBudget      time.Duration
}

// DefaultCacheConfig is the production default for presentation caching.
func DefaultCacheConfig() CacheConfig {
	return CacheConfig{
		LeaseTTL:        10 * time.Second,
		PayloadTTL:      time.Hour,
		MaxPayloadBytes: 256 << 10,
		MaxWaiters:      32,
		MaxFallback:     8,
		MaxFills:        8,
		MaxRequests:     64,
		OperationBudget: 250 * time.Millisecond,
		FillBudget:      3 * time.Second,
	}
}

// OpenPresentationCache connects to addr. An empty addr yields a disabled cache.
func OpenPresentationCache(addr string, cfg CacheConfig) (*PresentationCache, error) {
	if strings.TrimSpace(addr) == "" {
		return &PresentationCache{disabled: true, metrics: &CacheMetrics{}}, nil
	}
	defaults := DefaultCacheConfig()
	if cfg.LeaseTTL <= 0 {
		cfg = defaults
	}
	if cfg.MaxRequests <= 0 {
		cfg.MaxRequests = defaults.MaxRequests
	}
	if cfg.MaxFills <= 0 {
		cfg.MaxFills = defaults.MaxFills
	}
	if cfg.OperationBudget <= 0 {
		cfg.OperationBudget = defaults.OperationBudget
	}
	if cfg.FillBudget <= 0 {
		cfg.FillBudget = defaults.FillBudget
	}
	if cfg.MaxWaiters < 0 || cfg.MaxFallback < 0 {
		return nil, errors.New("product: cache admission limits cannot be negative")
	}
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress: []string{addr}, DisableAutoPipelining: true, DisableRetry: true,
		DisableCache: true, ForceSingleClient: true,
		Dialer: net.Dialer{Timeout: cfg.OperationBudget}, ConnWriteTimeout: cfg.OperationBudget,
		BlockingPoolSize: cfg.MaxRequests,
	})
	if err != nil {
		return nil, fmt.Errorf("open valkey: %w", err)
	}
	return &PresentationCache{
		client:   client,
		calls:    make(map[string]*presentationCall),
		loads:    make(chan struct{}, cfg.MaxRequests),
		gets:     make(chan struct{}, cfg.MaxRequests),
		closed:   make(chan struct{}),
		maxFills: cfg.MaxFills, operationBudget: cfg.OperationBudget, fillBudget: cfg.FillBudget,
		metrics:         &CacheMetrics{},
		leaseTTL:        cfg.LeaseTTL,
		payloadTTL:      cfg.PayloadTTL,
		maxPayloadBytes: cfg.MaxPayloadBytes,
		maxWaiters:      cfg.MaxWaiters,
		maxFallback:     cfg.MaxFallback,
	}, nil
}

// Enabled reports whether Valkey is wired.
func (c *PresentationCache) Enabled() bool {
	return c != nil && !c.disabled
}

// Close releases the Valkey client.
func (c *PresentationCache) Close() {
	if c == nil || c.client == nil {
		return
	}
	c.closeOnce.Do(func() { close(c.closed); c.client.Close() })
}

// Get reads a presentation payload. fill loads and stores on miss.
func (c *PresentationCache) Get(
	ctx context.Context,
	productID uuid.UUID,
	revision int64,
	locale string,
	fill func(context.Context) (Presentation, error),
) (Presentation, error) {
	if !c.Enabled() {
		p, err := fill(ctx)
		return p, err
	}
	if !c.admit(c.gets) {
		return Presentation{}, ErrOverloaded
	}
	defer func() { <-c.gets }()
	work, cancel := context.WithTimeout(ctx, c.fillBudget)
	defer cancel()
	select {
	case <-c.closed:
		return Presentation{}, context.Canceled
	default:
	}
	go func() {
		select {
		case <-c.closed:
			cancel()
		case <-work.Done():
		}
	}()
	if err := work.Err(); err != nil {
		return Presentation{}, err
	}
	key := cacheKey(productID, revision, locale)
	if pres, ok, err := c.read(work, key); err != nil {
		c.metrics.Errors.Add(1)
		return c.fallback(work, fill)
	} else if ok {
		if pres.ProductID == productID {
			c.metrics.Hits.Add(1)
			return pres, nil
		}
		c.metrics.RejectedBytes.Add(1)
	}
	c.metrics.Misses.Add(1)
	return c.coalesce(work, key, productID, fill)
}

func (c *PresentationCache) admit(slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		c.metrics.Rejected.Add(1)
		return false
	}
}

func (c *PresentationCache) coalesce(ctx context.Context, key string, productID uuid.UUID, fill func(context.Context) (Presentation, error)) (Presentation, error) {
	c.mu.Lock()
	if call, ok := c.calls[key]; ok {
		if c.waiters >= c.maxWaiters {
			c.mu.Unlock()
			c.metrics.Rejected.Add(1)
			return Presentation{}, ErrOverloaded
		}
		c.waiters++
		c.mu.Unlock()
		defer func() { c.mu.Lock(); c.waiters--; c.mu.Unlock() }()
		c.metrics.Coalesced.Add(1)
		select {
		case <-ctx.Done():
			return Presentation{}, ctx.Err()
		case <-call.done:
			return call.presentation, call.err
		}
	}
	if len(c.calls) >= c.maxFills {
		c.mu.Unlock()
		c.metrics.Rejected.Add(1)
		return Presentation{}, ErrOverloaded
	}
	call := &presentationCall{done: make(chan struct{})}
	c.calls[key] = call
	c.mu.Unlock()
	// The owner runs synchronously. Its request cancellation stops shared work;
	// other requests may retry, and no detached fill outlives its admission slot.
	defer func() {
		c.mu.Lock()
		delete(c.calls, key)
		close(call.done)
		c.mu.Unlock()
	}()
	if pres, ok, err := c.read(ctx, key); err == nil && ok && pres.ProductID == productID {
		call.presentation = pres
	} else {
		call.presentation, call.err = c.fill(ctx, key, productID, fill)
	}
	return call.presentation, call.err
}

func (c *PresentationCache) read(ctx context.Context, key string) (Presentation, bool, error) {
	op, cancel := context.WithTimeout(ctx, c.operationBudget)
	defer cancel()
	resp := c.client.Do(op, c.client.B().Get().Key(key).Build())
	if err := resp.Error(); err != nil {
		if valkey.IsValkeyNil(err) {
			return Presentation{}, false, nil
		}
		return Presentation{}, false, err
	}
	raw, err := resp.AsBytes()
	if err != nil {
		return Presentation{}, false, err
	}
	if len(raw) > c.maxPayloadBytes {
		c.metrics.RejectedBytes.Add(1)
		_ = c.client.Do(op, c.client.B().Del().Key(key).Build())
		return Presentation{}, false, nil
	}
	var pres Presentation
	if err := json.Unmarshal(raw, &pres); err != nil {
		c.metrics.RejectedBytes.Add(1)
		_ = c.client.Do(op, c.client.B().Del().Key(key).Build())
		return Presentation{}, false, nil
	}
	if pres.ProductID == uuid.Nil || !strings.HasPrefix(key, "goen:product:"+pres.ProductID.String()+":") {
		c.metrics.RejectedBytes.Add(1)
		return Presentation{}, false, nil
	}
	return pres, true, nil
}

func (c *PresentationCache) fill(
	ctx context.Context,
	key string,
	productID uuid.UUID,
	fill func(context.Context) (Presentation, error),
) (Presentation, error) {
	token, err := randomToken()
	if err != nil {
		return Presentation{}, err
	}
	lease := key + ":lease"
	op, cancel := context.WithTimeout(ctx, c.operationBudget)
	acquired := c.client.Do(op, c.client.B().Set().Key(lease).Value(token).
		Nx().Ex(c.leaseTTL).Build())
	leaseErr := acquired.Error()
	cancel()
	if leaseErr != nil && !valkey.IsValkeyNil(leaseErr) {
		c.metrics.Errors.Add(1)
		return c.fallback(ctx, fill)
	}
	ownsLease := leaseErr == nil

	if !ownsLease {
		return c.waitOrFallback(ctx, key, lease, fill)
	}

	if integrationFillPause != nil {
		if pauseErr := integrationFillPause(ctx); pauseErr != nil {
			c.releaseLease(ctx, lease, token)
			return Presentation{}, pauseErr
		}
	}

	pres, fillErr := fill(ctx)
	if fillErr != nil {
		c.releaseLease(ctx, lease, token)
		return Presentation{}, fillErr
	}
	if pres.ProductID != productID {
		c.releaseLease(ctx, lease, token)
		return Presentation{}, fmt.Errorf("product: fill product id %s does not match gate %s",
			pres.ProductID, productID)
	}
	raw, marshalErr := json.Marshal(pres)
	if marshalErr != nil {
		c.releaseLease(ctx, lease, token)
		return Presentation{}, marshalErr
	}
	if len(raw) > c.maxPayloadBytes {
		c.metrics.RejectedBytes.Add(1)
		c.releaseLease(ctx, lease, token)
		return pres, nil
	}
	ttl := payloadTTLWithJitter(c.payloadTTL)
	write, cancelWrite := context.WithTimeout(ctx, c.operationBudget)
	setErr := c.client.Do(write, c.client.B().Set().Key(key).Value(string(raw)).Ex(ttl).Build()).Error()
	cancelWrite()
	if setErr != nil {
		c.metrics.Errors.Add(1)
		c.releaseLease(ctx, lease, token)
		return pres, nil
	}
	c.metrics.Fills.Add(1)
	c.releaseLease(ctx, lease, token)
	return pres, nil
}

func (c *PresentationCache) waitOrFallback(
	ctx context.Context,
	key, lease string,
	fill func(context.Context) (Presentation, error),
) (Presentation, error) {
	waited, waitErr := c.waitForFill(ctx, key, lease)
	if waitErr != nil {
		if ctx.Err() != nil {
			return Presentation{}, ctx.Err()
		}
		c.metrics.Errors.Add(1)
		return c.fallback(ctx, fill)
	}
	if waited {
		if pres, ok, readErr := c.read(ctx, key); readErr == nil && ok {
			return pres, nil
		}
	}
	return c.fallback(ctx, fill)
}

func (c *PresentationCache) waitForFill(ctx context.Context, key, lease string) (bool, error) {
	deadline := time.Now().Add(c.leaseTTL)
	for time.Now().Before(deadline) {
		if _, ok, err := c.read(ctx, key); err == nil && ok {
			return true, nil
		} else if err != nil {
			return false, err
		}
		op, cancel := context.WithTimeout(ctx, c.operationBudget)
		exists := c.client.Do(op, c.client.B().Exists().Key(lease).Build())
		cancel()
		if exists.Error() != nil {
			return false, exists.Error()
		}
		n, countErr := exists.AsInt64()
		if countErr != nil {
			return false, countErr
		}
		if n == 0 {
			return false, nil
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		case <-timer.C:
		}
	}
	return false, nil
}

func (c *PresentationCache) releaseLease(ctx context.Context, lease, token string) {
	op, cancel := context.WithTimeout(ctx, c.operationBudget)
	defer cancel()
	script := `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`
	if err := c.client.Do(op, c.client.B().Eval().Script(script).Numkeys(1).Key(lease).Arg(token).Build()).Error(); err != nil {
		return
	}
}

func (c *PresentationCache) fallback(ctx context.Context, fill func(context.Context) (Presentation, error)) (Presentation, error) {
	if err := ctx.Err(); err != nil {
		return Presentation{}, err
	}
	if !c.tryFallback() {
		c.metrics.Rejected.Add(1)
		return Presentation{}, ErrOverloaded
	}
	defer c.endFallback()
	c.metrics.Fallbacks.Add(1)
	if integrationFillPause != nil {
		if pauseErr := integrationFillPause(ctx); pauseErr != nil {
			return Presentation{}, pauseErr
		}
	}
	return fill(ctx)
}

func (c *PresentationCache) tryFallback() bool {
	c.fallbackMu.Lock()
	defer c.fallbackMu.Unlock()
	if c.fallbackIn >= c.maxFallback {
		return false
	}
	c.fallbackIn++
	return true
}

func (c *PresentationCache) endFallback() {
	c.fallbackMu.Lock()
	c.fallbackIn--
	c.fallbackMu.Unlock()
}

func cacheKey(productID uuid.UUID, revision int64, locale string) string {
	return fmt.Sprintf("goen:product:%s:rev:%d:loc:%s:v%d",
		productID.String(), revision, locale, presentationSchemaVersion)
}

func payloadTTLWithJitter(base time.Duration) time.Duration {
	if base <= 0 {
		return time.Hour
	}
	jitter, err := rand.Int(rand.Reader, big.NewInt(int64(base/10)))
	if err != nil {
		return base
	}
	return base - base/20 + time.Duration(jitter.Int64())
}

func randomToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
