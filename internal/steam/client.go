package steam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseURL      = "https://store.steampowered.com/api/"
	maxResponseBodySize = 4 << 20
)

// Game is the Steam metadata Rice needs to identify and display a result.
type Game struct {
	ID       int64
	Name     string
	ImageURL string
}

// Client calls the key-free Steam Store endpoint used by Rice.
type Client struct {
	httpClient *http.Client
	baseURL    *url.URL
}

func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	baseURL, err := url.Parse(defaultBaseURL)
	if err != nil {
		panic("invalid Steam base URL: " + err.Error())
	}
	return &Client{httpClient: httpClient, baseURL: baseURL}
}

// Search returns up to limit Steam applications in the order supplied by the
// Store search endpoint. Bundles and packages are deliberately omitted.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Game, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("steam search query is empty")
	}
	if limit <= 0 {
		return []Game{}, nil
	}

	values := url.Values{
		"term": {query},
		"cc":   {"us"},
		"l":    {"english"},
	}
	var response struct {
		Items []struct {
			Type      string `json:"type"`
			Name      string `json:"name"`
			ID        int64  `json:"id"`
			TinyImage string `json:"tiny_image"`
		} `json:"items"`
	}
	if err := c.get(ctx, "storesearch/", values, &response); err != nil {
		return nil, fmt.Errorf("search Steam: %w", err)
	}

	games := make([]Game, 0, min(limit, len(response.Items)))
	for _, item := range response.Items {
		if item.Type != "app" || item.ID <= 0 || strings.TrimSpace(item.Name) == "" {
			continue
		}
		games = append(games, Game{ID: item.ID, Name: item.Name, ImageURL: item.TinyImage})
		if len(games) == limit {
			break
		}
	}
	return games, nil
}

func (c *Client) get(ctx context.Context, path string, values url.Values, destination any) error {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: path})
	endpoint.RawQuery = values.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Rice Discord bot")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8<<10))
		return fmt.Errorf("unexpected HTTP status %s", response.Status)
	}

	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBodySize))
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
