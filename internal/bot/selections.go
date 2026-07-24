package bot

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SokvisalMong/rice/internal/steam"
	"github.com/bwmarrin/discordgo"
)

const (
	selectionCustomIDPrefix = "rice:game:"
	defaultSelectionTimeout = 60 * time.Second
)

type selectionKind uint8

const (
	selectionDeals selectionKind = iota + 1
	selectionBundles
	selectionSearch
)

type pendingSelection struct {
	userID      string
	kind        selectionKind
	games       map[string]steam.Game
	expiresAt   time.Time
	interaction *discordgo.Interaction
	timer       *time.Timer
}

type selectionState struct {
	selectionMu      sync.Mutex
	selections       map[string]*pendingSelection
	selectionTimeout time.Duration
	selectionNow     func() time.Time
}

func newSelectionState() selectionState {
	return selectionState{
		selections:       make(map[string]*pendingSelection),
		selectionTimeout: defaultSelectionTimeout,
		selectionNow:     time.Now,
	}
}

func (h *Handler) presentSelection(session *discordgo.Session, interaction *discordgo.Interaction, games []steam.Game, kind selectionKind) error {
	if len(games) == 0 {
		return errors.New("cannot present an empty game selection")
	}
	userID := interactionUserID(interaction)
	if userID == "" {
		return errors.New("Discord user ID is unavailable")
	}
	nonce, err := selectionNonce()
	if err != nil {
		return err
	}

	options := make([]discordgo.SelectMenuOption, 0, len(games))
	gameMap := make(map[string]steam.Game, len(games))
	for _, game := range games {
		value := strconv.FormatInt(game.ID, 10)
		gameMap[value] = game
		options = append(options, discordgo.SelectMenuOption{
			Label:       truncate(game.Name, 100),
			Value:       value,
			Description: "Steam App ID " + value,
		})
	}

	pending := &pendingSelection{
		userID:      userID,
		kind:        kind,
		games:       gameMap,
		expiresAt:   h.selectionNow().Add(h.selectionTimeout),
		interaction: interaction,
	}
	pending.timer = time.AfterFunc(h.selectionTimeout, func() {
		h.expireSelection(session, nonce)
	})

	h.selectionMu.Lock()
	h.selections[nonce] = pending
	h.selectionMu.Unlock()

	content := ""
	embeds := []*discordgo.MessageEmbed{selectionEmbed(games)}
	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{
				CustomID:    selectionCustomIDPrefix + nonce,
				Placeholder: "Choose a Steam game",
				MaxValues:   1,
				Options:     options,
			},
		}},
	}
	if _, err := session.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{
		Content:    &content,
		Embeds:     &embeds,
		Components: &components,
	}); err != nil {
		h.removeSelection(nonce)
		return err
	}
	return nil
}

func (h *Handler) handleSelection(session *discordgo.Session, event *discordgo.InteractionCreate) {
	data := event.MessageComponentData()
	if !strings.HasPrefix(data.CustomID, selectionCustomIDPrefix) {
		return
	}
	nonce := strings.TrimPrefix(data.CustomID, selectionCustomIDPrefix)
	userID := interactionUserID(event.Interaction)

	h.selectionMu.Lock()
	pending, found := h.selections[nonce]
	if found && !h.selectionNow().Before(pending.expiresAt) {
		delete(h.selections, nonce)
		pending.timer.Stop()
		found = false
	}
	if !found {
		h.selectionMu.Unlock()
		h.respondImmediateError(session, event.Interaction, "This game selection has expired.")
		return
	}
	if pending.userID != userID {
		h.selectionMu.Unlock()
		h.respondImmediateError(session, event.Interaction, "Only the person who ran the command can use this menu.")
		return
	}
	if len(data.Values) != 1 {
		h.selectionMu.Unlock()
		h.respondImmediateError(session, event.Interaction, "Choose exactly one game.")
		return
	}
	game, found := pending.games[data.Values[0]]
	if !found {
		h.selectionMu.Unlock()
		h.respondImmediateError(session, event.Interaction, "That game is not part of this selection.")
		return
	}
	delete(h.selections, nonce)
	pending.timer.Stop()
	h.selectionMu.Unlock()

	if err := session.InteractionRespond(event.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	}); err != nil {
		h.logInteractionError("acknowledge game selection", event.Interaction, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	switch pending.kind {
	case selectionDeals:
		h.deliverDeal(ctx, session, event.Interaction, game)
	case selectionSearch:
		h.publishDeal(ctx, session, event.Interaction, game)
	case selectionBundles:
		h.deliverBundles(ctx, session, event.Interaction, game)
	default:
		h.failDeferred(session, event.Interaction, "That selection is no longer supported.", errors.New("unknown selection kind"))
	}
}

func (h *Handler) expireSelection(session *discordgo.Session, nonce string) {
	h.selectionMu.Lock()
	pending, found := h.selections[nonce]
	if found {
		delete(h.selections, nonce)
	}
	h.selectionMu.Unlock()
	if !found {
		return
	}

	content := "Game selection expired after 60 seconds. Run the command again to retry."
	components := []discordgo.MessageComponent{}
	if _, err := session.InteractionResponseEdit(pending.interaction, &discordgo.WebhookEdit{
		Content:    &content,
		Components: &components,
	}); err != nil {
		h.logInteractionError("expire game selection", pending.interaction, err)
	}
}

func (h *Handler) removeSelection(nonce string) {
	h.selectionMu.Lock()
	pending, found := h.selections[nonce]
	if found {
		delete(h.selections, nonce)
	}
	h.selectionMu.Unlock()
	if found {
		pending.timer.Stop()
	}
}

// Close cancels outstanding menu timers during shutdown.
func (h *Handler) Close() {
	h.selectionMu.Lock()
	for nonce, pending := range h.selections {
		pending.timer.Stop()
		delete(h.selections, nonce)
	}
	h.selectionMu.Unlock()
}

func selectionNonce() (string, error) {
	var bytes [9]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}
