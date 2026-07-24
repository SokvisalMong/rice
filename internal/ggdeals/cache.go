package ggdeals

import (
	"context"
	"sync"
	"time"
)

type cacheKey struct {
	appID  int64
	region string
}

type priceCacheEntry struct {
	value     *GamePrices
	fetchedAt time.Time
}

type bundleCacheEntry struct {
	value     *GameBundles
	fetchedAt time.Time
}

// CachedAPI caches successful lookups, including successful null records. The
// region is part of the key so one user's currency can never leak into another
// user's response.
type CachedAPI struct {
	source  API
	ttl     time.Duration
	now     func() time.Time
	mu      sync.RWMutex
	prices  map[cacheKey]priceCacheEntry
	bundles map[cacheKey]bundleCacheEntry
}

func NewCachedAPI(source API, ttl time.Duration) *CachedAPI {
	return &CachedAPI{
		source:  source,
		ttl:     ttl,
		now:     time.Now,
		prices:  make(map[cacheKey]priceCacheEntry),
		bundles: make(map[cacheKey]bundleCacheEntry),
	}
}

func (c *CachedAPI) Prices(ctx context.Context, appID int64, region string) (*GamePrices, error) {
	key := cacheKey{appID: appID, region: region}
	now := c.now()
	c.mu.RLock()
	entry, found := c.prices[key]
	c.mu.RUnlock()
	if found && now.Sub(entry.fetchedAt) < c.ttl {
		return entry.value, nil
	}

	value, err := c.source.Prices(ctx, appID, region)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.pruneLocked(now)
	c.prices[key] = priceCacheEntry{value: value, fetchedAt: now}
	c.mu.Unlock()
	return value, nil
}

func (c *CachedAPI) Bundles(ctx context.Context, appID int64, region string) (*GameBundles, error) {
	key := cacheKey{appID: appID, region: region}
	now := c.now()
	c.mu.RLock()
	entry, found := c.bundles[key]
	c.mu.RUnlock()
	if found && now.Sub(entry.fetchedAt) < c.ttl {
		return entry.value, nil
	}

	value, err := c.source.Bundles(ctx, appID, region)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.pruneLocked(now)
	c.bundles[key] = bundleCacheEntry{value: value, fetchedAt: now}
	c.mu.Unlock()
	return value, nil
}

func (c *CachedAPI) pruneLocked(now time.Time) {
	for key, entry := range c.prices {
		if now.Sub(entry.fetchedAt) >= c.ttl {
			delete(c.prices, key)
		}
	}
	for key, entry := range c.bundles {
		if now.Sub(entry.fetchedAt) >= c.ttl {
			delete(c.bundles, key)
		}
	}
}
