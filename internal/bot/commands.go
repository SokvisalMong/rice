package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/SokvisalMong/rice/internal/ggdeals"
	"github.com/SokvisalMong/rice/internal/steam"
	"github.com/bwmarrin/discordgo"
)

const commandTimeout = 20 * time.Second

type steamAPI interface {
	Search(context.Context, string, int) ([]steam.Game, error)
}

type regionStore interface {
	Region(context.Context, string) (string, error)
	SetRegion(context.Context, string, string) error
}

// Handler owns command routing and short-lived component selection state.
type Handler struct {
	steam   steamAPI
	deals   ggdeals.API
	regions regionStore
	logger  *slog.Logger

	selectionState
}

func NewHandler(steamClient steamAPI, dealsClient ggdeals.API, regions regionStore, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		steam:          steamClient,
		deals:          dealsClient,
		regions:        regions,
		logger:         logger,
		selectionState: newSelectionState(),
	}
}

// Commands returns the complete slash-command schema. Nil default member
// permissions intentionally makes every server member eligible to use them.
func (h *Handler) Commands() []*discordgo.ApplicationCommand {
	minimumLength := 1

	gameOption := func() *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "game",
			Description: "Game name to search for on Steam",
			Required:    true,
			MinLength:   &minimumLength,
			MaxLength:   100,
		}
	}

	return []*discordgo.ApplicationCommand{
		{
			Name:        "deals",
			Description: "Find current and historical prices for a Steam game",
			Options:     []*discordgo.ApplicationCommandOption{gameOption()},
		},
		{
			Name:        "bundles",
			Description: "Find active bundles containing a Steam game",
			Options:     []*discordgo.ApplicationCommandOption{gameOption()},
		},
		{
			Name:        "region",
			Description: "Set the region and currency used for your deal results",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:         discordgo.ApplicationCommandOptionString,
					Name:         "region",
					Description:  "GG.deals region code, such as us, gb, or de",
					Required:     true,
					Autocomplete: true,
					MinLength:    &minimumLength,
					MaxLength:    2,
				},
			},
		},
		{
			Name:        "search",
			Description: "Search Steam and choose a game before viewing deals",
			Options:     []*discordgo.ApplicationCommandOption{gameOption()},
		},
	}
}

// HandleInteraction routes slash commands, autocompletion, and game menus.
func (h *Handler) HandleInteraction(session *discordgo.Session, event *discordgo.InteractionCreate) {
	if event == nil || event.Interaction == nil {
		return
	}

	switch event.Type {
	case discordgo.InteractionApplicationCommand:
		data := event.ApplicationCommandData()
		switch data.Name {
		case "deals":
			h.handleDeals(session, event)
		case "bundles":
			h.handleBundles(session, event)
		case "region":
			h.handleRegion(session, event)
		case "search":
			h.handleSearch(session, event)
		default:
			h.respondImmediateError(session, event.Interaction, "That command is not supported.")
		}
	case discordgo.InteractionApplicationCommandAutocomplete:
		h.handleAutocomplete(session, event)
	case discordgo.InteractionMessageComponent:
		h.handleSelection(session, event)
	}
}

func (h *Handler) handleDeals(session *discordgo.Session, event *discordgo.InteractionCreate) {
	query, err := gameQuery(event.ApplicationCommandData())
	if err != nil {
		h.respondImmediateError(session, event.Interaction, err.Error())
		return
	}
	if err := deferResponse(session, event.Interaction, false); err != nil {
		h.logInteractionError("acknowledge deals command", event.Interaction, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	games, err := h.steam.Search(ctx, query, 5)
	if err != nil {
		h.failDeferred(session, event.Interaction, "Steam search failed. Please try again.", err)
		return
	}
	if len(games) == 0 {
		h.editWithEmbed(session, event.Interaction, searchNotFoundEmbed(query))
		return
	}
	if isAmbiguous(query, games) {
		if err := h.presentSelection(session, event.Interaction, games, selectionDeals); err != nil {
			h.failDeferred(session, event.Interaction, "Could not create the game selection menu.", err)
		}
		return
	}
	h.deliverDeal(ctx, session, event.Interaction, games[0])
}

func (h *Handler) handleBundles(session *discordgo.Session, event *discordgo.InteractionCreate) {
	query, err := gameQuery(event.ApplicationCommandData())
	if err != nil {
		h.respondImmediateError(session, event.Interaction, err.Error())
		return
	}
	if err := deferResponse(session, event.Interaction, false); err != nil {
		h.logInteractionError("acknowledge bundles command", event.Interaction, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	games, err := h.steam.Search(ctx, query, 5)
	if err != nil {
		h.failDeferred(session, event.Interaction, "Steam search failed. Please try again.", err)
		return
	}
	if len(games) == 0 {
		h.editWithEmbed(session, event.Interaction, searchNotFoundEmbed(query))
		return
	}
	if isAmbiguous(query, games) {
		if err := h.presentSelection(session, event.Interaction, games, selectionBundles); err != nil {
			h.failDeferred(session, event.Interaction, "Could not create the game selection menu.", err)
		}
		return
	}
	h.deliverBundles(ctx, session, event.Interaction, games[0])
}

func (h *Handler) handleSearch(session *discordgo.Session, event *discordgo.InteractionCreate) {
	query, err := gameQuery(event.ApplicationCommandData())
	if err != nil {
		h.respondImmediateError(session, event.Interaction, err.Error())
		return
	}
	if err := deferResponse(session, event.Interaction, true); err != nil {
		h.logInteractionError("acknowledge search command", event.Interaction, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	games, err := h.steam.Search(ctx, query, 5)
	if err != nil {
		h.failDeferred(session, event.Interaction, "Steam search failed. Please try again.", err)
		return
	}
	if len(games) == 0 {
		h.editWithEmbed(session, event.Interaction, searchNotFoundEmbed(query))
		return
	}
	if err := h.presentSelection(session, event.Interaction, games, selectionSearch); err != nil {
		h.failDeferred(session, event.Interaction, "Could not create the game selection menu.", err)
	}
}

func (h *Handler) handleRegion(session *discordgo.Session, event *discordgo.InteractionCreate) {
	option := event.ApplicationCommandData().GetOption("region")
	if option == nil {
		h.respondImmediateError(session, event.Interaction, "Choose a region.")
		return
	}
	region, ok := ggdeals.NormalizeRegion(option.StringValue())
	if !ok {
		h.respondImmediateError(session, event.Interaction, "That region is not supported. Choose a value from autocomplete.")
		return
	}
	userID := interactionUserID(event.Interaction)
	if userID == "" {
		h.respondImmediateError(session, event.Interaction, "Could not identify your Discord account.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.regions.SetRegion(ctx, userID, region); err != nil {
		h.logger.Error("save Discord user region", "user_id", userID, "error", err)
		h.respondImmediateError(session, event.Interaction, "Could not save your region. Please try again.")
		return
	}

	name := regionName(region)
	content := fmt.Sprintf("Your deal region is now **%s — %s**.", strings.ToUpper(region), name)
	if err := session.InteractionRespond(event.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		h.logInteractionError("respond to region command", event.Interaction, err)
	}
}

func (h *Handler) handleAutocomplete(session *discordgo.Session, event *discordgo.InteractionCreate) {
	data := event.ApplicationCommandData()
	if data.Name != "region" {
		return
	}
	prefix := ""
	for _, option := range data.Options {
		if option.Name == "region" && option.Focused {
			prefix = strings.ToLower(strings.TrimSpace(option.StringValue()))
			break
		}
	}

	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(ggdeals.ValidRegions()))
	for _, region := range ggdeals.ValidRegions() {
		if prefix != "" && !strings.HasPrefix(region.Code, prefix) && !strings.Contains(strings.ToLower(region.Name), prefix) {
			continue
		}
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  strings.ToUpper(region.Code) + " — " + region.Name,
			Value: region.Code,
		})
	}
	if err := session.InteractionRespond(event.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	}); err != nil {
		h.logInteractionError("respond to region autocomplete", event.Interaction, err)
	}
}

func (h *Handler) deliverDeal(ctx context.Context, session *discordgo.Session, interaction *discordgo.Interaction, game steam.Game) {
	embed, userMessage, err := h.loadDeal(ctx, interaction, game)
	if err != nil {
		h.failDeferred(session, interaction, userMessage, err)
		return
	}
	h.editWithEmbed(session, interaction, embed)
}

func (h *Handler) publishDeal(ctx context.Context, session *discordgo.Session, interaction *discordgo.Interaction, game steam.Game) {
	gameName := escapeMarkdown(truncate(game.Name, 100))
	if err := editSelectionStatus(session, interaction, fmt.Sprintf("Looking up deals for **%s**...", gameName)); err != nil {
		h.logInteractionError("show public deal lookup status", interaction, err)
	}

	embed, userMessage, err := h.loadDeal(ctx, interaction, game)
	if err != nil {
		h.failDeferred(session, interaction, userMessage, err)
		return
	}
	if _, err := session.ChannelMessageSendEmbed(interaction.ChannelID, embed); err != nil {
		h.failDeferred(session, interaction, "Could not post the selected deal to this channel.", err)
		return
	}

	if err := editSelectionStatus(session, interaction, fmt.Sprintf("Posted deals for **%s** to this channel.", gameName)); err != nil {
		h.logInteractionError("confirm public deal response", interaction, err)
	}
}

func (h *Handler) loadDeal(ctx context.Context, interaction *discordgo.Interaction, game steam.Game) (*discordgo.MessageEmbed, string, error) {
	region, err := h.userRegion(ctx, interaction)
	if err != nil {
		return nil, "Could not load your region preference.", err
	}
	prices, err := h.deals.Prices(ctx, game.ID, region)
	if err != nil {
		return nil, "GG.deals lookup failed. Please try again.", err
	}
	return dealEmbed(game, prices, region), "", nil
}

func editSelectionStatus(session *discordgo.Session, interaction *discordgo.Interaction, content string) error {
	embeds := []*discordgo.MessageEmbed{}
	components := []discordgo.MessageComponent{}
	_, err := session.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{
		Content:    &content,
		Embeds:     &embeds,
		Components: &components,
	})
	return err
}

func (h *Handler) deliverBundles(ctx context.Context, session *discordgo.Session, interaction *discordgo.Interaction, game steam.Game) {
	region, err := h.userRegion(ctx, interaction)
	if err != nil {
		h.failDeferred(session, interaction, "Could not load your region preference.", err)
		return
	}
	bundles, err := h.deals.Bundles(ctx, game.ID, region)
	if err != nil {
		h.failDeferred(session, interaction, "GG.deals lookup failed. Please try again.", err)
		return
	}
	h.editWithEmbed(session, interaction, bundlesEmbed(game, bundles, region, time.Now().UTC()))
}

func (h *Handler) userRegion(ctx context.Context, interaction *discordgo.Interaction) (string, error) {
	userID := interactionUserID(interaction)
	if userID == "" {
		return "", errors.New("Discord user ID is unavailable")
	}
	region, err := h.regions.Region(ctx, userID)
	if err != nil {
		return "", err
	}
	region, ok := ggdeals.NormalizeRegion(region)
	if !ok {
		return "", fmt.Errorf("stored region %q is invalid", region)
	}
	return region, nil
}

func gameQuery(data discordgo.ApplicationCommandInteractionData) (string, error) {
	option := data.GetOption("game")
	if option == nil {
		return "", errors.New("provide a game name")
	}
	query := strings.TrimSpace(option.StringValue())
	if query == "" {
		return "", errors.New("game name cannot be empty")
	}
	return query, nil
}

func deferResponse(session *discordgo.Session, interaction *discordgo.Interaction, ephemeral bool) error {
	flags := discordgo.MessageFlags(0)
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}
	return session.InteractionRespond(interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: flags},
	})
}

func (h *Handler) editWithEmbed(session *discordgo.Session, interaction *discordgo.Interaction, embed *discordgo.MessageEmbed) {
	content := ""
	embeds := []*discordgo.MessageEmbed{embed}
	components := []discordgo.MessageComponent{}
	if _, err := session.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{
		Content:    &content,
		Embeds:     &embeds,
		Components: &components,
	}); err != nil {
		h.logInteractionError("edit interaction response", interaction, err)
	}
}

func (h *Handler) failDeferred(session *discordgo.Session, interaction *discordgo.Interaction, message string, cause error) {
	h.logInteractionError(message, interaction, cause)
	components := []discordgo.MessageComponent{}
	embeds := []*discordgo.MessageEmbed{}
	if _, err := session.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{
		Content:    &message,
		Embeds:     &embeds,
		Components: &components,
	}); err != nil {
		h.logInteractionError("edit failed interaction response", interaction, err)
	}
}

func (h *Handler) respondImmediateError(session *discordgo.Session, interaction *discordgo.Interaction, message string) {
	if err := session.InteractionRespond(interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: message,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		h.logInteractionError("respond with interaction error", interaction, err)
	}
}

func (h *Handler) logInteractionError(operation string, interaction *discordgo.Interaction, err error) {
	attributes := []any{"operation", operation, "error", err}
	if interaction != nil {
		attributes = append(attributes, "interaction_id", interaction.ID, "guild_id", interaction.GuildID)
	}
	h.logger.Error("Discord interaction failed", attributes...)
}

func interactionUserID(interaction *discordgo.Interaction) string {
	if interaction == nil {
		return ""
	}
	if interaction.Member != nil && interaction.Member.User != nil {
		return interaction.Member.User.ID
	}
	if interaction.User != nil {
		return interaction.User.ID
	}
	return ""
}

func regionName(code string) string {
	for _, region := range ggdeals.ValidRegions() {
		if region.Code == code {
			return region.Name
		}
	}
	return strings.ToUpper(code)
}

func steamURL(appID int64) string {
	return "https://store.steampowered.com/app/" + strconv.FormatInt(appID, 10) + "/"
}

func ggDealsURL(appID int64) string {
	return "https://gg.deals/steam/app/" + strconv.FormatInt(appID, 10) + "/"
}
