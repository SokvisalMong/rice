package bot

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SokvisalMong/rice/internal/ggdeals"
	"github.com/SokvisalMong/rice/internal/steam"
	"github.com/bwmarrin/discordgo"
)

func TestCommandSchemaExposesPhaseOneCommandsToAllMembers(t *testing.T) {
	t.Parallel()
	handler := NewHandler(nil, nil, nil, nil)
	commands := handler.Commands()
	if len(commands) != 4 {
		t.Fatalf("Commands() returned %d commands, want 4", len(commands))
	}

	byName := make(map[string]*discordgo.ApplicationCommand, len(commands))
	for _, command := range commands {
		byName[command.Name] = command
		if command.DefaultMemberPermissions != nil {
			t.Errorf("command %q restricts default member permissions", command.Name)
		}
	}
	for _, name := range []string{"deals", "bundles", "region", "search"} {
		if byName[name] == nil {
			t.Errorf("missing /%s command", name)
		}
	}
	for _, name := range []string{"deals", "bundles", "search"} {
		options := byName[name].Options
		if len(options) != 1 || options[0].Name != "game" || options[0].Type != discordgo.ApplicationCommandOptionString || !options[0].Required {
			t.Errorf("unexpected /%s options: %#v", name, options)
		}
	}
	if !byName["region"].Options[0].Autocomplete {
		t.Error("/region does not enable autocomplete")
	}
}

func TestGameQueryRequiresName(t *testing.T) {
	t.Parallel()
	gameData := discordgo.ApplicationCommandInteractionData{Options: []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "game", Type: discordgo.ApplicationCommandOptionString, Value: " Portal "},
	}}
	query, err := gameQuery(gameData)
	if err != nil || query != "Portal" {
		t.Errorf("game query = (%q, %v)", query, err)
	}

	emptyData := discordgo.ApplicationCommandInteractionData{Options: []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "game", Type: discordgo.ApplicationCommandOptionString, Value: "   "},
	}}
	if _, err := gameQuery(emptyData); err == nil {
		t.Error("empty game query was accepted")
	}
	if _, err := gameQuery(discordgo.ApplicationCommandInteractionData{}); err == nil {
		t.Error("missing game query was accepted")
	}
}

func TestSingleWordPrefixSearchIsAmbiguous(t *testing.T) {
	t.Parallel()
	portalResults := []steam.Game{
		{ID: 400, Name: "Portal"},
		{ID: 620, Name: "Portal 2"},
		{ID: 601360, Name: "Portal: Revolution"},
	}
	if !isAmbiguous("Portal", portalResults) {
		t.Error("Portal search was not treated as ambiguous")
	}
	if isAmbiguous("Hollow Knight", []steam.Game{{Name: "Hollow Knight"}, {Name: "Hollow Knight: Silksong"}}) {
		t.Error("multi-word first-result search was treated as ambiguous")
	}
}

func TestDealEmbedFormatsRegionalPricesAndNullResult(t *testing.T) {
	t.Parallel()
	retail := "7.49"
	historical := "4.99"
	prices := &ggdeals.GamePrices{
		Title: "Hollow Knight",
		URL:   "https://gg.deals/game/hollow-knight/",
		Prices: ggdeals.PriceSet{
			CurrentRetail:    &retail,
			HistoricalRetail: &historical,
			Currency:         "GBP",
		},
	}
	game := steam.Game{ID: 367520, Name: "Hollow Knight", ImageURL: "header.jpg"}
	embed := dealEmbed(game, prices, "gb")
	if embed.Title != "Hollow Knight" || embed.Thumbnail == nil {
		t.Errorf("deal embed metadata = %#v", embed)
	}
	if !strings.Contains(embed.Fields[0].Value, "£7.49") || !strings.Contains(embed.Fields[0].Value, "Not available") {
		t.Errorf("current price field = %q", embed.Fields[0].Value)
	}
	if !strings.Contains(embed.Footer.Text, "Region: GB") || !strings.Contains(embed.Footer.Text, "Currency: GBP") {
		t.Errorf("footer = %q", embed.Footer.Text)
	}

	missing := dealEmbed(game, nil, "us")
	if !strings.Contains(missing.Description, "No deals currently tracked") || !strings.Contains(missing.Fields[0].Value, "Steam store") {
		t.Errorf("no-deals embed = %#v", missing)
	}
}

func TestActiveBundlesFiltersExpiredAndFutureOffers(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	expiredEnd := "2026-07-20 00:00:00"
	activeEnd := "2026-07-30 00:00:00"
	futureStart := "2026-08-01 00:00:00"
	record := &ggdeals.GameBundles{Bundles: []ggdeals.Bundle{
		{Title: "Expired", DateFrom: "2026-07-01 00:00:00", DateTo: &expiredEnd},
		{Title: "No expiry", DateFrom: "2026-07-01 00:00:00"},
		{Title: "Future", DateFrom: futureStart},
		{Title: "Active", DateFrom: "2026-07-01 00:00:00", DateTo: &activeEnd},
	}}

	active := activeBundles(record, now)
	if len(active) != 2 {
		t.Fatalf("activeBundles() returned %d entries: %#v", len(active), active)
	}
	if active[0].Title != "Active" || active[1].Title != "No expiry" {
		t.Errorf("active bundle order = %q, %q", active[0].Title, active[1].Title)
	}
}

type staticDealsAPI struct {
	prices *ggdeals.GamePrices
}

func (api staticDealsAPI) Prices(context.Context, int64, string) (*ggdeals.GamePrices, error) {
	return api.prices, nil
}

func (staticDealsAPI) Bundles(context.Context, int64, string) (*ggdeals.GameBundles, error) {
	return nil, nil
}

type staticRegionStore struct{}

func (staticRegionStore) Region(context.Context, string) (string, error) {
	return "us", nil
}

func (staticRegionStore) SetRegion(context.Context, string, string) error {
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestSearchSelectionPublishesRegularChannelMessage(t *testing.T) {
	t.Parallel()
	retail := "7.49"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(nil, staticDealsAPI{prices: &ggdeals.GamePrices{
		Title: "Portal 2",
		Prices: ggdeals.PriceSet{
			CurrentRetail: &retail,
			Currency:      "USD",
		},
	}}, staticRegionStore{}, logger)

	type capturedRequest struct {
		method  string
		path    string
		payload map[string]any
	}
	var requests []capturedRequest
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		payload := make(map[string]any)
		if request.Body != nil {
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(body) > 0 {
				if decodeErr := json.Unmarshal(body, &payload); decodeErr != nil {
					t.Fatal(decodeErr)
				}
			}
		}
		requests = append(requests, capturedRequest{method: request.Method, path: request.URL.Path, payload: payload})

		status := http.StatusOK
		body := `{"id":"message"}`
		if strings.Contains(request.URL.Path, "/interactions/") {
			status = http.StatusNoContent
			body = ""
		}
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})}

	const nonce = "search-selection"
	game := steam.Game{ID: 620, Name: "Portal 2"}
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	handler.selections[nonce] = &pendingSelection{
		userID:    "user",
		kind:      selectionSearch,
		games:     map[string]steam.Game{"620": game},
		expiresAt: time.Now().Add(time.Hour),
		timer:     timer,
	}
	event := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID:        "component",
		AppID:     "app",
		ChannelID: "channel",
		Token:     "token",
		Type:      discordgo.InteractionMessageComponent,
		User:      &discordgo.User{ID: "user"},
		Data: discordgo.MessageComponentInteractionData{
			CustomID: selectionCustomIDPrefix + nonce,
			Values:   []string{"620"},
		},
	}}

	handler.handleSelection(session, event)

	var channelMessage *capturedRequest
	for index := range requests {
		request := &requests[index]
		if request.method != http.MethodPost {
			continue
		}
		if strings.Contains(request.path, "/webhooks/") {
			t.Fatalf("deal was posted as an interaction follow-up: %#v", request)
		}
		if strings.HasSuffix(request.path, "/channels/channel/messages") {
			channelMessage = request
		}
	}
	if channelMessage == nil {
		t.Fatalf("no regular channel message was sent: %#v", requests)
	}
	if _, found := channelMessage.payload["message_reference"]; found {
		t.Fatalf("channel message was sent as a reply: %#v", channelMessage.payload)
	}
	embeds, ok := channelMessage.payload["embeds"].([]any)
	if !ok || len(embeds) != 1 {
		t.Fatalf("channel message did not contain one deal embed: %#v", channelMessage.payload)
	}
}
