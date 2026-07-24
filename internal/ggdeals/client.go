package ggdeals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL      = "https://api.gg.deals/"
	maxResponseBodySize = 8 << 20
)

// Region is a GG.deals pricing region exposed by the /region command.
type Region struct {
	Code string
	Name string
}

var regions = [...]Region{
	{Code: "au", Name: "Australia"},
	{Code: "be", Name: "Belgium"},
	{Code: "br", Name: "Brazil"},
	{Code: "ca", Name: "Canada"},
	{Code: "ch", Name: "Switzerland"},
	{Code: "de", Name: "Germany"},
	{Code: "dk", Name: "Denmark"},
	{Code: "es", Name: "Spain"},
	{Code: "eu", Name: "European Union"},
	{Code: "fi", Name: "Finland"},
	{Code: "fr", Name: "France"},
	{Code: "gb", Name: "United Kingdom"},
	{Code: "ie", Name: "Ireland"},
	{Code: "it", Name: "Italy"},
	{Code: "nl", Name: "Netherlands"},
	{Code: "no", Name: "Norway"},
	{Code: "pl", Name: "Poland"},
	{Code: "se", Name: "Sweden"},
	{Code: "us", Name: "United States"},
}

// ValidRegions returns an independent copy of the regions accepted by GG.deals.
func ValidRegions() []Region {
	result := make([]Region, len(regions))
	copy(result, regions[:])
	return result
}

// NormalizeRegion lowercases and validates a GG.deals region code.
func NormalizeRegion(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, region := range regions {
		if value == region.Code {
			return value, true
		}
	}
	return "", false
}

// PriceSet contains the four prices returned by GG.deals.
type PriceSet struct {
	CurrentRetail     *string `json:"currentRetail"`
	CurrentKeyshops   *string `json:"currentKeyshops"`
	HistoricalRetail  *string `json:"historicalRetail"`
	HistoricalKeyshop *string `json:"historicalKeyshops"`
	Currency          string  `json:"currency"`
}

// GamePrices is one Steam application's price record.
type GamePrices struct {
	Title  string   `json:"title"`
	URL    string   `json:"url"`
	Prices PriceSet `json:"prices"`
}

// BundleTierGame identifies a game included in a bundle tier.
type BundleTierGame struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// BundleTier describes one purchasable tier of a bundle.
type BundleTier struct {
	Price      string           `json:"price"`
	Currency   string           `json:"currency"`
	GamesCount *int             `json:"gamesCount"`
	Games      []BundleTierGame `json:"games"`
}

// Bundle describes one bundle that contains the requested game.
type Bundle struct {
	Title    string       `json:"title"`
	URL      *string      `json:"url"`
	DateFrom string       `json:"dateFrom"`
	DateTo   *string      `json:"dateTo"`
	Tiers    []BundleTier `json:"tiers"`
}

// GameBundles is one Steam application's bundle record.
type GameBundles struct {
	Title   string   `json:"title"`
	URL     string   `json:"url"`
	Bundles []Bundle `json:"bundles"`
}

// API is the deal lookup contract used by the Discord command handler.
type API interface {
	Prices(context.Context, int64, string) (*GamePrices, error)
	Bundles(context.Context, int64, string) (*GameBundles, error)
}

// Client calls GG.deals' Steam App ID endpoints.
type Client struct {
	httpClient *http.Client
	baseURL    *url.URL
	apiKey     string
}

func NewClient(apiKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	baseURL, err := url.Parse(defaultBaseURL)
	if err != nil {
		panic("invalid GG.deals base URL: " + err.Error())
	}
	return &Client{httpClient: httpClient, baseURL: baseURL, apiKey: strings.TrimSpace(apiKey)}
}

func (c *Client) Prices(ctx context.Context, appID int64, region string) (*GamePrices, error) {
	var data map[string]*GamePrices
	if err := c.lookup(ctx, "v1/prices/by-steam-app-id/", appID, region, &data); err != nil {
		return nil, fmt.Errorf("get GG.deals prices: %w", err)
	}
	return data[strconv.FormatInt(appID, 10)], nil
}

func (c *Client) Bundles(ctx context.Context, appID int64, region string) (*GameBundles, error) {
	var data map[string]*GameBundles
	if err := c.lookup(ctx, "v1/bundles/by-steam-app-id/", appID, region, &data); err != nil {
		return nil, fmt.Errorf("get GG.deals bundles: %w", err)
	}
	return data[strconv.FormatInt(appID, 10)], nil
}

func (c *Client) lookup(ctx context.Context, path string, appID int64, region string, destination any) error {
	if appID <= 0 {
		return errors.New("Steam App ID must be positive")
	}
	if c.apiKey == "" {
		return errors.New("GG.deals API key is empty")
	}
	region, ok := NormalizeRegion(region)
	if !ok {
		return fmt.Errorf("unsupported GG.deals region %q", region)
	}

	endpoint := c.baseURL.ResolveReference(&url.URL{Path: path})
	query := endpoint.Query()
	query.Set("key", c.apiKey)
	query.Set("ids", strconv.FormatInt(appID, 10))
	query.Set("region", region)
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return errors.New("create GG.deals request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Rice Discord bot")

	response, err := c.httpClient.Do(request)
	if err != nil {
		var urlError *url.Error
		if errors.As(err, &urlError) {
			return fmt.Errorf("send request: %w", urlError.Err)
		}
		return errors.New("send GG.deals request")
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodySize+1))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxResponseBodySize {
		return errors.New("GG.deals response exceeded size limit")
	}

	var envelope struct {
		Success bool            `json:"success"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || !envelope.Success {
		message := strings.TrimSpace(envelope.Message)
		if message == "" && len(envelope.Data) > 0 {
			var apiError struct {
				Message string `json:"message"`
				Name    string `json:"name"`
			}
			if json.Unmarshal(envelope.Data, &apiError) == nil {
				message = strings.TrimSpace(apiError.Message)
				if message == "" {
					message = strings.TrimSpace(apiError.Name)
				}
			}
		}
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return fmt.Errorf("API returned HTTP %d: %s", response.StatusCode, message)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return errors.New("GG.deals response did not contain data")
	}
	if err := json.Unmarshal(envelope.Data, destination); err != nil {
		return fmt.Errorf("decode response data: %w", err)
	}
	return nil
}
