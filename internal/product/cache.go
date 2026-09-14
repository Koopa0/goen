package product

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	valkey "github.com/valkey-io/valkey-go"
	"golang.org/x/sync/singleflight"
)

// ErrOverloaded is returned when cache failure recovery has exhausted its
// bounded database fallback budget for this request.
var ErrOverloaded = errors.New("product: cache fallback budget exhausted")

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
}

// PresentationCache is a Valkey-backed cache-aside store for Presentation.
type PresentationCache struct {
	client  valkey.Client
	flight  singleflight.Group
	metrics *CacheMetrics

	leaseTTL        time.Duration
	payloadTTL      time.Duration
	maxPayloadBytes int
	maxWaiters      int
	maxFallback     int

	fallbackMu sync.Mutex
	fallbackIn int

	disabled bool
}

// CacheConfig tunes the shared presentation cache.
type CacheConfig struct {
	LeaseTTL        time.Duration
	PayloadTTL      time.Duration
	MaxPayloadBytes int
	MaxWaiters      int
	MaxFallback     int
}

// DefaultCacheConfig is the production default for presentation caching.
func DefaultCacheConfig() CacheConfig {
	return CacheConfig{
		LeaseTTL:        10 * time.Second,
		PayloadTTL:      time.Hour,
		MaxPayloadBytes: 256 << 10,
		MaxWaiters:      32,
		MaxFallback:     8,
	}
}

// OpenPresentationCache connects to addr. An empty addr yields a disabled cache.
func OpenPresentationCache(addr string, cfg CacheConfig) (*PresentationCache, error) {
	if strings.TrimSpace(addr) == "" {
		return &PresentationCache{disabled: true, metrics: &CacheMetrics{}}, nil
	}
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:           []string{addr},
		DisableAutoPipelining: true,
		DisableRetry:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("open valkey: %w", err)
	}
	if cfg.LeaseTTL <= 0 {
		cfg = DefaultCacheConfig()
	}
	return &PresentationCache{
		client:          client,
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
	c.client.Close()
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
	key := cacheKey(productID, revision, locale)
	if pres, ok, err := c.read(ctx, key); err != nil {
		c.metrics.Errors.Add(1)
		return c.fallback(ctx, fill)
	} else if ok {
		c.metrics.Hits.Add(1)
		return pres, nil
	}
	c.metrics.Misses.Add(1)

	result := c.flight.DoChan(key, func() (any, error) {
		if pres, ok, err := c.read(ctx, key); err == nil && ok {
			return pres, nil
		}
		return c.fill(ctx, key, productID, fill)
	})

	select {
	case got := <-result:
		if got.Err != nil {
			return Presentation{}, got.Err
		}
		if got.Shared {
			c.metrics.Coalesced.Add(1)
		}
		pres, ok := got.Val.(Presentation)
		if !ok {
			return Presentation{}, errors.New("product: cache fill returned unexpected type")
		}
		return pres, nil
	case <-ctx.Done():
		return Presentation{}, ctx.Err()
	}
}

func (c *PresentationCache) read(ctx context.Context, key string) (Presentation, bool, error) {
	resp := c.client.Do(ctx, c.client.B().Get().Key(key).Build())
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
		_ = c.client.Do(ctx, c.client.B().Del().Key(key).Build())
		return Presentation{}, false, nil
	}
	var pres Presentation
	if err := json.Unmarshal(raw, &pres); err != nil {
		c.metrics.RejectedBytes.Add(1)
		_ = c.client.Do(ctx, c.client.B().Del().Key(key).Build())
		return Presentation{}, false, nil
	}
	if pres.ProductID == uuid.Nil {
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
	acquired := c.client.Do(ctx, c.client.B().Set().Key(lease).Value(token).
		Nx().Ex(c.leaseTTL).Build())
	leaseErr := acquired.Error()
	if leaseErr != nil && !valkey.IsValkeyNil(leaseErr) {
		c.metrics.Errors.Add(1)
		return c.fallback(ctx, fill)
	}
	ownsLease := leaseErr == nil

	if !ownsLease {
		return c.waitOrFallback(ctx, key, lease, fill)
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
	setErr := c.client.Do(ctx, c.client.B().Set().Key(key).Value(string(raw)).Ex(ttl).Build()).Error()
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
		return Presentation{}, waitErr
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
	waiters := 0
	for time.Now().Before(deadline) {
		if _, ok, err := c.read(ctx, key); err == nil && ok {
			return true, nil
		} else if err != nil {
			return false, err
		}
		exists := c.client.Do(ctx, c.client.B().Exists().Key(lease).Build())
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
		waiters++
		if waiters > c.maxWaiters {
			return false, ErrOverloaded
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
	script := `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`
	if err := c.client.Do(ctx, c.client.B().Eval().Script(script).Numkeys(1).Key(lease).Arg(token).Build()).Error(); err != nil {
		return
	}
}

func (c *PresentationCache) fallback(ctx context.Context, fill func(context.Context) (Presentation, error)) (Presentation, error) {
	if !c.tryFallback() {
		return Presentation{}, ErrOverloaded
	}
	defer c.endFallback()
	c.metrics.Fallbacks.Add(1)
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
