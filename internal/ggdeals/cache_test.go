package ggdeals

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

type countingAPI struct {
	priceCalls  int
	bundleCalls int
	priceError  error
}

func (api *countingAPI) Prices(_ context.Context, appID int64, region string) (*GamePrices, error) {
	api.priceCalls++
	if api.priceError != nil {
		return nil, api.priceError
	}
	return &GamePrices{Title: strconv.FormatInt(appID, 10) + "-" + region}, nil
}

func (api *countingAPI) Bundles(_ context.Context, appID int64, region string) (*GameBundles, error) {
	api.bundleCalls++
	return &GameBundles{Title: strconv.FormatInt(appID, 10) + "-" + region}, nil
}

func TestCachedAPIKeysByAppAndRegionAndExpiresAtTTL(t *testing.T) {
	t.Parallel()
	source := &countingAPI{}
	cache := NewCachedAPI(source, 5*time.Minute)
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }

	first, err := cache.Prices(context.Background(), 367520, "us")
	if err != nil {
		t.Fatal(err)
	}
	second, err := cache.Prices(context.Background(), 367520, "us")
	if err != nil {
		t.Fatal(err)
	}
	if first != second || source.priceCalls != 1 {
		t.Fatalf("same key was not cached: calls=%d first=%p second=%p", source.priceCalls, first, second)
	}

	if _, err := cache.Prices(context.Background(), 367520, "gb"); err != nil {
		t.Fatal(err)
	}
	if source.priceCalls != 2 {
		t.Fatalf("region was not part of cache key: calls=%d", source.priceCalls)
	}

	now = now.Add(5 * time.Minute)
	if _, err := cache.Prices(context.Background(), 367520, "us"); err != nil {
		t.Fatal(err)
	}
	if source.priceCalls != 3 {
		t.Fatalf("entry did not expire at TTL: calls=%d", source.priceCalls)
	}
}

func TestCachedAPIDoesNotCacheErrors(t *testing.T) {
	t.Parallel()
	source := &countingAPI{priceError: errors.New("temporary failure")}
	cache := NewCachedAPI(source, 5*time.Minute)

	for range 2 {
		if _, err := cache.Prices(context.Background(), 620, "us"); err == nil {
			t.Fatal("Prices() error = nil")
		}
	}
	if source.priceCalls != 2 {
		t.Errorf("error was cached: calls=%d", source.priceCalls)
	}
}

func TestCachedAPICachesPricesAndBundlesIndependently(t *testing.T) {
	t.Parallel()
	source := &countingAPI{}
	cache := NewCachedAPI(source, 5*time.Minute)

	if _, err := cache.Prices(context.Background(), 620, "us"); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Bundles(context.Background(), 620, "us"); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Bundles(context.Background(), 620, "us"); err != nil {
		t.Fatal(err)
	}
	if source.priceCalls != 1 || source.bundleCalls != 1 {
		t.Errorf("calls = prices:%d bundles:%d, want 1 each", source.priceCalls, source.bundleCalls)
	}
}
