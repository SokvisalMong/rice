package ggdeals

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestPricesBuildsValidatedRegionalLookup(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/prices/by-steam-app-id/" {
			t.Errorf("path = %q", request.URL.Path)
		}
		query := request.URL.Query()
		if query.Get("key") != "test-key" || query.Get("ids") != "367520" || query.Get("region") != "gb" {
			t.Errorf("unexpected query values: ids=%q region=%q key_present=%t", query.Get("ids"), query.Get("region"), query.Get("key") != "")
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{
			"success":true,
			"data":{
				"367520":{
					"title":"Hollow Knight",
					"url":"https://gg.deals/game/hollow-knight/",
					"prices":{
						"currentRetail":"7.49",
						"currentKeyshops":null,
						"historicalRetail":"4.99",
						"historicalKeyshops":"3.74",
						"currency":"GBP"
					}
				}
			}
		}`)
	}))
	defer server.Close()

	client := ggDealsClientForServer(t, server)
	prices, err := client.Prices(context.Background(), 367520, "GB")
	if err != nil {
		t.Fatalf("Prices() error = %v", err)
	}
	if prices == nil {
		t.Fatal("Prices() returned nil")
	}
	if prices.Title != "Hollow Knight" || prices.Prices.Currency != "GBP" {
		t.Errorf("Prices() = %#v", prices)
	}
	if prices.Prices.CurrentRetail == nil || *prices.Prices.CurrentRetail != "7.49" {
		t.Errorf("current retail = %v", prices.Prices.CurrentRetail)
	}
	if prices.Prices.CurrentKeyshops != nil {
		t.Errorf("current keyshops = %v, want nil", prices.Prices.CurrentKeyshops)
	}
}

func TestBundlesDecodesNullableFields(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{
			"success":true,
			"data":{
				"620":{
					"title":"Portal 2",
					"url":"https://gg.deals/game/portal-2/",
					"bundles":[{
						"title":"Valve Bundle",
						"url":null,
						"dateFrom":"2026-07-01 00:00:00",
						"dateTo":null,
						"tiers":[{"price":"9.99","currency":"USD","gamesCount":null,"games":[]}]
					}]
				}
			}
		}`)
	}))
	defer server.Close()

	client := ggDealsClientForServer(t, server)
	bundles, err := client.Bundles(context.Background(), 620, "us")
	if err != nil {
		t.Fatalf("Bundles() error = %v", err)
	}
	if bundles == nil || len(bundles.Bundles) != 1 {
		t.Fatalf("Bundles() = %#v", bundles)
	}
	bundle := bundles.Bundles[0]
	if bundle.URL != nil || bundle.DateTo != nil || bundle.Tiers[0].GamesCount != nil {
		t.Errorf("nullable fields were not preserved: %#v", bundle)
	}
}

func TestLookupRejectsInvalidRegionBeforeRequest(t *testing.T) {
	t.Parallel()
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	client := ggDealsClientForServer(t, server)
	if _, err := client.Prices(context.Background(), 620, "xx"); err == nil {
		t.Fatal("Prices() error = nil, want invalid region error")
	}
	if called {
		t.Fatal("invalid region consumed an API request")
	}
}

func TestPricesPreservesNullRecord(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(writer, `{"success":true,"data":{"1":null}}`)
	}))
	defer server.Close()

	client := ggDealsClientForServer(t, server)
	prices, err := client.Prices(context.Background(), 1, "us")
	if err != nil {
		t.Fatalf("Prices() error = %v", err)
	}
	if prices != nil {
		t.Errorf("Prices() = %#v, want nil", prices)
	}
}

func ggDealsClientForServer(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient("test-key", server.Client())
	client.baseURL = baseURL
	return client
}
