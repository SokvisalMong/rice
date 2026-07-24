package bot

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/SokvisalMong/rice/internal/ggdeals"
	"github.com/SokvisalMong/rice/internal/steam"
	"github.com/bwmarrin/discordgo"
)

const (
	dealColor    = 0x66C0F4
	bundleColor  = 0x8B5CF6
	neutralColor = 0x64748B
	warningColor = 0xF59E0B
)

func dealEmbed(game steam.Game, prices *ggdeals.GamePrices, region string) *discordgo.MessageEmbed {
	if prices == nil {
		return noDealsEmbed(game, region)
	}

	title := game.Name
	if strings.TrimSpace(prices.Title) != "" {
		title = prices.Title
	}
	link := prices.URL
	if strings.TrimSpace(link) == "" {
		link = ggDealsURL(game.ID)
	}
	embed := &discordgo.MessageEmbed{
		Title: title,
		URL:   link,
		Color: dealColor,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "💰 Current best price",
				Value:  pricePair(prices.Prices.CurrentRetail, prices.Prices.CurrentKeyshops, prices.Prices.Currency),
				Inline: true,
			},
			{
				Name:   "📉 Historical lowest",
				Value:  pricePair(prices.Prices.HistoricalRetail, prices.Prices.HistoricalKeyshop, prices.Prices.Currency),
				Inline: true,
			},
			{
				Name:  "🔗 Links",
				Value: fmt.Sprintf("[View on GG.deals](%s) · [Steam store](%s)", link, steamURL(game.ID)),
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: regionFooter(region, prices.Prices.Currency),
		},
	}
	if game.ImageURL != "" {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: game.ImageURL}
	}
	return embed
}

func noDealsEmbed(game steam.Game, region string) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{
		Title:       game.Name,
		URL:         steamURL(game.ID),
		Description: "No deals currently tracked on GG.deals for this Steam app.",
		Color:       neutralColor,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:  "Links",
				Value: fmt.Sprintf("[Steam store](%s) · [Check GG.deals](%s)", steamURL(game.ID), ggDealsURL(game.ID)),
			},
		},
		Footer: &discordgo.MessageEmbedFooter{Text: regionFooter(region, "")},
	}
	if game.ImageURL != "" {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: game.ImageURL}
	}
	return embed
}

func bundlesEmbed(game steam.Game, record *ggdeals.GameBundles, region string, now time.Time) *discordgo.MessageEmbed {
	link := ggDealsURL(game.ID)
	title := game.Name
	if record != nil {
		if strings.TrimSpace(record.URL) != "" {
			link = record.URL
		}
		if strings.TrimSpace(record.Title) != "" {
			title = record.Title
		}
	}

	embed := &discordgo.MessageEmbed{
		Title:  title + " — Active bundles",
		URL:    link,
		Color:  bundleColor,
		Footer: &discordgo.MessageEmbedFooter{Text: regionFooter(region, "")},
	}
	if game.ImageURL != "" {
		embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: game.ImageURL}
	}

	active := activeBundles(record, now)
	if len(active) == 0 {
		embed.Color = neutralColor
		embed.Description = fmt.Sprintf("No active bundles are currently tracked for this game.\n\n[View on GG.deals](%s) · [Steam store](%s)", link, steamURL(game.ID))
		return embed
	}

	const maximumDisplayedBundles = 5
	displayed := min(len(active), maximumDisplayedBundles)
	for index := range displayed {
		bundle := active[index]
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  truncate(bundle.Title, 256),
			Value: bundleFieldValue(bundle, link),
		})
	}
	if len(active) > displayed {
		embed.Description = fmt.Sprintf("Showing %d of %d active bundles. [View all for this game](%s).", displayed, len(active), link)
	}
	return embed
}

func activeBundles(record *ggdeals.GameBundles, now time.Time) []ggdeals.Bundle {
	if record == nil {
		return nil
	}
	now = now.UTC()
	active := make([]ggdeals.Bundle, 0, len(record.Bundles))
	for _, bundle := range record.Bundles {
		if start, ok := parseBundleTime(bundle.DateFrom); ok && start.After(now) {
			continue
		}
		if bundle.DateTo != nil {
			if end, ok := parseBundleTime(*bundle.DateTo); ok && !end.After(now) {
				continue
			}
		}
		active = append(active, bundle)
	}
	sort.SliceStable(active, func(left, right int) bool {
		leftEnd, leftOK := bundleEnd(active[left])
		rightEnd, rightOK := bundleEnd(active[right])
		if leftOK != rightOK {
			return leftOK
		}
		if !leftOK {
			return false
		}
		return leftEnd.Before(rightEnd)
	})
	return active
}

func bundleFieldValue(bundle ggdeals.Bundle, fallbackURL string) string {
	var builder strings.Builder
	const maximumDisplayedTiers = 5
	for index, tier := range bundle.Tiers {
		if index == maximumDisplayedTiers {
			fmt.Fprintf(&builder, "• %d more tiers\n", len(bundle.Tiers)-maximumDisplayedTiers)
			break
		}
		fmt.Fprintf(&builder, "• **%s**", formatAmount(tier.Price, tier.Currency))
		if tier.GamesCount != nil {
			fmt.Fprintf(&builder, " — choose %d of %d games", *tier.GamesCount, len(tier.Games))
		} else if len(tier.Games) > 0 {
			fmt.Fprintf(&builder, " — %d games", len(tier.Games))
		}
		builder.WriteByte('\n')
	}
	if len(bundle.Tiers) == 0 {
		builder.WriteString("Price tiers unavailable\n")
	}

	if end, ok := bundleEnd(bundle); ok {
		fmt.Fprintf(&builder, "Expires <t:%d:F> (<t:%d:R>)\n", end.Unix(), end.Unix())
	} else {
		builder.WriteString("No known expiry date\n")
	}
	link := fallbackURL
	if bundle.URL != nil && strings.TrimSpace(*bundle.URL) != "" {
		link = *bundle.URL
	}
	fmt.Fprintf(&builder, "[Open bundle](%s)", link)
	return truncate(builder.String(), 1024)
}

func bundleEnd(bundle ggdeals.Bundle) (time.Time, bool) {
	if bundle.DateTo == nil {
		return time.Time{}, false
	}
	return parseBundleTime(*bundle.DateTo)
}

func parseBundleTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		parsed, err := time.ParseInLocation(layout, value, time.UTC)
		if err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func searchNotFoundEmbed(query string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "No Steam apps found",
		Description: fmt.Sprintf("No Steam applications matched **%s**.", escapeMarkdown(truncate(query, 100))),
		Color:       neutralColor,
	}
}

func selectionEmbed(games []steam.Game) *discordgo.MessageEmbed {
	lines := make([]string, 0, len(games))
	for index, game := range games {
		lines = append(lines, fmt.Sprintf("%d. **%s** — Steam App ID `%d`", index+1, escapeMarkdown(truncate(game.Name, 100)), game.ID))
	}
	return &discordgo.MessageEmbed{
		Title:       "Choose a Steam game",
		Description: strings.Join(lines, "\n") + "\n\nThis menu expires after 60 seconds.",
		Color:       dealColor,
	}
}

func isAmbiguous(query string, games []steam.Game) bool {
	words := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(words) != 1 || len(games) < 2 {
		return false
	}
	needle := words[0]
	matches := 0
	for _, game := range games {
		name := strings.ToLower(strings.TrimSpace(game.Name))
		if name == needle || hasTitlePrefix(name, needle) {
			matches++
		}
	}
	return matches >= 2
}

func hasTitlePrefix(title, prefix string) bool {
	if !strings.HasPrefix(title, prefix) || len(title) == len(prefix) {
		return false
	}
	next, _ := utf8FirstRune(title[len(prefix):])
	return unicode.IsSpace(next) || strings.ContainsRune(":-–—()[]", next)
}

func utf8FirstRune(value string) (rune, int) {
	for _, character := range value {
		return character, len(string(character))
	}
	return 0, 0
}

func pricePair(retail, keyshops *string, currency string) string {
	return "Retail: **" + formatOptionalAmount(retail, currency) + "**\nKeyshops: **" + formatOptionalAmount(keyshops, currency) + "**"
}

func formatOptionalAmount(value *string, currency string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "Not available"
	}
	return formatAmount(*value, currency)
}

func formatAmount(value, currency string) string {
	value = strings.TrimSpace(value)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	switch currency {
	case "USD":
		return "$" + value
	case "EUR":
		return "€" + value
	case "GBP":
		return "£" + value
	case "AUD":
		return "A$" + value
	case "CAD":
		return "C$" + value
	case "BRL":
		return "R$" + value
	case "PLN":
		return value + " zł"
	case "CHF":
		return "CHF " + value
	case "DKK", "NOK", "SEK":
		return value + " " + currency
	case "":
		return value
	default:
		return value + " " + currency
	}
}

func regionFooter(region, currency string) string {
	footer := "Region: " + strings.ToUpper(region)
	if strings.TrimSpace(currency) != "" {
		footer += " · Currency: " + strings.ToUpper(currency)
	}
	return footer
}

func truncate(value string, maximumRunes int) string {
	runes := []rune(value)
	if len(runes) <= maximumRunes {
		return value
	}
	if maximumRunes <= 1 {
		return string(runes[:maximumRunes])
	}
	return string(runes[:maximumRunes-1]) + "…"
}

func escapeMarkdown(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"*", "\\*",
		"_", "\\_",
		"~", "\\~",
		"`", "\\`",
		"|", "\\|",
	)
	return replacer.Replace(value)
}
