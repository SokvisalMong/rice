# Rice — Discord Game Deals Bot

> A Discord bot that lets users query game deals via `/` slash commands.

---

## API Research Summary

### Steam Web API

| Question | Answer |
|---|---|
| **Needs API key?** | The Steam Store search endpoint we use is key-free. |
| **How to get a key?** | Free, self-service at <https://steamcommunity.com/dev/apikey> (tied to a Steam account). We may want one later for features like owned-game lookup. |
| **Game search endpoint** | `GET https://store.steampowered.com/api/storesearch/?term={name}&cc=us&l=english` — returns `items[]` with `id` (Steam App ID), `name`, `type` ("app"/"bundle"/"package"). **Undocumented** but widely used and stable. |
| **Rate limit** | 100,000 calls/day per API key (if using one). No key-free rate limit published, but 1–2 req/sec sustained is safe. |
| **Caveat** | `storesearch` is undocumented. `type` field distinguishes apps from bundles/packages — we must filter for `type == "app"`. |

### GG.deals API

| Question | Answer |
|---|---|
| **API key** | Required. Free, generated at <https://gg.deals/settings/> (logged in). Passed as `?key=` query parameter. |
| **Prices endpoint** | `GET https://api.gg.deals/v1/prices/by-steam-app-id/?key={key}&ids={id1,id2,...}` — up to 100 Steam App IDs per request. |
| **Prices response** | Per game: `title`, `url` (GG.deals page), `prices` object with `currentRetail`, `currentKeyshops`, `historicalRetail`, `historicalKeyshops`, `currency`. Prices are strings (null if unavailable). |
| **Bundles endpoint** | `GET https://api.gg.deals/v1/bundles/by-steam-app-id/?key={key}&ids={id1,id2,...}` — same 100-ID limit. |
| **Bundles response** | Per game: `title`, `url`, `bundles[]` with `title`, `url`, `dateFrom`, `dateTo`, `tiers[]` (each with `price`, `currency`, `gamesCount`, `games[]`). |
| **All active bundles** | `GET https://api.gg.deals/v1/bundles/active/?key={key}` — paginated via `cursor`/`offset`. |
| **Redirection shortcut** | `https://gg.deals/steam/app/{id}/` redirects to the game's GG.deals page (useful for clean links in embeds). |
| **Region support** | Optional `region` param: `au`, `be`, `br`, `ca`, `ch`, `de`, `dk`, `es`, `eu`, `fi`, `fr`, `gb`, `ie`, `it`, `nl`, `no`, `pl`, `se`, `us`. Default: `us`. |
| **Rate limit** | 100 records/min, 1,000 records/hour. Each ID counts as one record. HTTP 429 with `x-ratelimit-limit`, `x-ratelimit-remaining`, `x-ratelimit-reset` headers. Invalid requests also count. |
| **Docs** | PDFs in `GG.deals/` directory. Also <https://gg.deals/api/> (may be Cloudflare-blocked for automated access). |


### Discord Bot Stack

| Choice | Rationale |
|---|---|
| **Language** | Go — single binary deployment, no runtime dependencies, fast startup |
| **Discord library** | `github.com/bwmarrin/discordgo` — most mature and widely used Go Discord library |
| **Slash commands** | `discordgo.ApplicationCommand` + `Session.ApplicationCommandCreate()` — register in `init()` or on bot ready |
| **Dev workflow** | Guild-scoped command registration for instant testing. Global registration for production. |
| **Invite scope** | Bot invite must include `applications.commands` OAuth2 scope. |

---

## Bot Flow

```
User: /deals game: "Hollow Knight"
  │
  ├─ 1. Steam Store Search API (no key)
  │     GET store.steampowered.com/api/storesearch/?term=Hollow Knight
  │     → { id: 367520, name: "Hollow Knight", type: "app" }
  │
  ├─ 2. GG.deals Prices API
  │     GET api.gg.deals/v1/prices/by-steam-app-id/?ids=367520&key=KEY
  │     → { "367520": { title, url, prices: { currentRetail, historicalRetail, ... } } }
  │
  └─ 3. (Optional) GG.deals Bundles API
        GET api.gg.deals/v1/bundles/by-steam-app-id/?ids=367520&key=KEY
        → active bundles containing this game
```

**Total API calls per query: 2** (search + prices). Bundles are optional/fetch-on-demand.

---

## Features

### Commands

| Command | Description |
|---|---|
| `/deals game:<name>` | Search Steam for a game by name. Auto-selects closest match, shows a selection menu if multiple close matches exist. Fetches GG.deals prices and returns an embed. |
| `/bundles game:<name>` | Search → show active bundles containing the game. Shows bundle name, tier prices, expiry date. |
| `/region <region>` | Set user's preferred region (e.g., `us`, `gb`, `de`). Stored in SQLite. Autocomplete on valid region codes. |
| `/search game:<name>` | Explicit search: shows a list of matching games to pick from. For when the user wants to browse results. |
| `/link <steamid>` | Link your Steam account. Accepts a SteamID64 (17-digit number) or a vanity URL name (e.g., `gab`). The bot auto-detects the format: if the input is all digits, it's treated as SteamID64; otherwise as a vanity URL resolved via `ResolveVanityURL`. Stores the mapping in SQLite. Requires `STEAM_API_KEY`. |
| `/unlink` | Remove your linked Steam account. Stops all wishlist tracking and scheduled deal checks for you. |
| `/wishlist status` | Show your linked Steam account, wishlist item count, last check time, and any pending deal notifications. |

### Deal Embed Format

```
┌─────────────────────────────────────────┐
│ [thumb] Hollow Knight                   │
│                                         │
│ 💰 Current Best Price                   │
│   Retail: $7.49 (Steam)                │
│   Keyshop: $5.29 (various)             │
│                                         │
│ 📉 Historical Lowest                    │
│   Retail: $4.99                         │
│   Keyshop: $3.74                        │
│                                         │
│ 🔗 View on GG.deals                    │
└─────────────────────────────────────────┘
```

### Behavior Rules

- **Closest match auto-selected**: Steam search returns results; bot picks the first `type == "app"` match by default. No exact-match threshold — first result wins.
- **Multiple close matches**: Only shown if the user explicitly uses `/search` or if the query is ambiguous enough to warrant a selection (e.g., "Portal" matches Portal, Portal 2, Portal Revolution). Show top 5 matches.
  - User picks from menu → bot fetches deals for that game.
  - `/search` keeps the chooser private, then posts the selected deal publicly to the channel.
  - Selection menu times out after 60 seconds.
- **No deals found**: If GG.deals returns null for a Steam App ID, show an embed with "No deals currently tracked on GG.deals" and a link to the Steam store page.
- **Region**: Each user's region preference is passed to the GG.deals API. Default: `us` if not set.
- **Prices**: Display in the user's currency. GG.deals returns prices in the region's currency.

### Caching

- **In-memory map** with 5-minute TTL per Steam App ID and region.
- Resets on bot restart — acceptable for a personal bot.
- Key: `(steamAppID, region) → { prices, bundles, timestamps }`.

### Permissions

- All server members can use all commands. No role restrictions.
- Guild install scopes: `bot` and `applications.commands`.
- Minimal bot permissions: **View Channels**, **Send Messages**, and **Embed Links** (`19456`).
- No privileged Gateway intents are required.

### Steam Wishlist API

| Question | Answer |
|---|---|
| **Endpoint** | `GET https://store.steampowered.com/wishlist/profiles/{STEAMID64}/wishlistdata/?cc={region}` — **undocumented**, used by Steam's own store frontend. |
| **Auth** | None. No API key required, but the user's wishlist **must be public** (or at least wishlist-visible). |
| **Response** | JSON object keyed by Steam App ID. Each value has `name`, `capsule` (header image URL), `review_score`, `release_date`, `platforms`, etc. |
| **Pagination** | Not paginated in the traditional sense — returns all wishlist items in one response (can be large). |
| **Rate limit** | Undocumented. Steam may block or throttle automated requests. Aggressive caching and polite intervals (≥5 min) required. |
| **Caveat** | Completely unsupported. Schema may change without notice. Private wishlists return empty/error. Must handle failures gracefully. |

### Steam Account Linking — Method Analysis

| Method | How It Works | Pros | Cons | Verdict |
|---|---|---|---|---|
| **SteamID64** | User pastes their 17-digit SteamID64 (e.g., `76561198000000000`). Found on steamid.io, steamdb.com/tools, or profile page source. | Deterministic, no ambiguity, no API key needed, always works. | Users don't know their SteamID64 offhand. Requires them to look it up externally. | ✅ **Supported** — primary method |
| **Vanity URL** | User enters their custom URL name (e.g., `gab` from `steamcommunity.com/id/gab/`). Resolved via `ISteamUser/ResolveVanityURL` API. | Familiar — users know their profile URL. One lookup resolves to SteamID64. | Requires `STEAM_API_KEY`. Vanity URLs can be changed. "Username" ≠ vanity URL — display names are **not** resolvable. | ✅ **Supported** — convenience method |
| **Friend Code** | User enters their Steam friend code (e.g., `1234-5678`). | Easy to find in Steam client. | **No official API** to convert friend code → SteamID64. Would require reverse-engineered math or third-party libs. Fragile, breaks if Valve changes the encoding. | ❌ **Not supported** |
| **Login with Steam (OpenID)** | Bot sends a link → user signs in on Steam → Steam redirects back with verified SteamID64. | Most user-friendly. Zero manual ID entry. Verified identity. | Requires a **web server** with public URL + HTTPS for the OAuth callback. Adds operational complexity (hosting, TLS, session management) far beyond a Discord bot's scope. | ❌ **Not supported** — overkill for this use case |

**Chosen approach:** Support **SteamID64** and **vanity URL** via `/link`. The bot detects which format was entered: 17-digit number → SteamID64; otherwise → treat as vanity URL and resolve via API. This requires `STEAM_API_KEY` to become a **required** env var (promoted from optional).

### Wishlist Deal Tracker

#### Overview

When a user links their Steam account via `/link`, the bot periodically fetches their public Steam wishlist, looks up prices on GG.deals, and DMs them if any game on their wishlist has a deal worth reporting.

#### Flow

```
Scheduled job (every 6 hours per user, staggered):
  │
  ├─ 1. Fetch Steam wishlist
  │     GET store.steampowered.com/wishlist/profiles/{STEAMID64}/wishlistdata/?cc={region}
  │     → { "367520": { name: "Hollow Knight", capsule: "...", ... }, ... }
  │     Returns all items in one response (no pagination).
  │
  ├─ 2. Batch into chunks of 100 (GG.deals limit)
  │     e.g., 247 items → 3 batches: [100, 100, 47]
  │
  ├─ 3. For each batch, call GG.deals Prices API
  │     GET api.gg.deals/v1/prices/by-steam-app-id/?ids={id1,...,id100}&key=KEY&region={region}
  │     → per-game: { title, url, prices: { currentRetail, currentKeyshops, historicalRetail, historicalKeyshops } }
  │     Respect rate limit: 100 records/min, 1,000/hour. Wait between batches if needed.
  │
  ├─ 4. Filter for deals
  │     A "deal" = game has a non-null currentRetail OR currentKeyshops price.
  │     Compare against user's threshold (default: any deal, configurable later).
  │
  └─ 5. Send DM summary embed
        One embed per user listing all games with active deals.
        Each entry: game name, current best price, link to GG.deals page.
        If no deals found → no DM sent (silent).
```

#### Batching Strategy (GG.deals 100-ID limit)

```go
func batchIDs(ids []string, batchSize int) [][]string {
    var batches [][]string
    for i := 0; i < len(ids); i += batchSize {
        end := i + batchSize
        if end > len(ids) { end = len(ids) }
        batches = append(batches, ids[i:end])
    }
    return batches
}
```

- Each batch = 1 API call. 500 wishlist items = 5 calls.
- Between batches: check `x-ratelimit-remaining` header. If < batch size, sleep until `x-ratelimit-reset`.
- Per user per cycle: max ~1,000 items (10 batches). Most users have <200.

#### Scheduling

- **Not** a global cron. Instead: when the bot starts, load all linked users from SQLite. For each user, calculate a staggered interval (e.g., `now + (userIndex % 12) * 30min`) so not all users are checked simultaneously.
- Use `time.Ticker` or `time.AfterFunc` per user. Re-sync on bot restart.
- Check interval: **6 hours** per user (configurable via env var).
- A user's check is skipped if: no Steam account linked, wishlist is private (empty response), or last check was < interval ago.

#### Rate Limit Budget

- GG.deals: 1,000 records/hour. If we have N users with average W wishlist items each:
  - Records/hour = N × W (each ID = 1 record)
  - Staggering across the hour keeps us under the limit
  - With 6-hour intervals, 24 users with 200 items each = 4,800 records/6hrs = 800/hour — within budget.
- If we exceed: respect 429 + `x-ratelimit-reset` header, defer remaining users to next window.

#### DM Embed Format (Wishlist Deals)

```
┌─────────────────────────────────────────┐
│ 🎮 Wishlist Deals Found!                │
│                                         │
│ Hollow Knight                           │
│   💰 $7.49 (retail) · $5.29 (keyshop)  │
│   🔗 gg.deals/game/hollow-knight/       │
│                                         │
│ Hades                                   │
│   💰 $12.49 (retail)                    │
│   🔗 gg.deals/game/hades/               │
│                                         │
│ Stardew Valley                          │
│   💰 $8.99 (retail) · $6.49 (keyshop)  │
│   🔗 gg.deals/game/stardew-valley/      │
│                                         │
│ Checked 247 wishlist items              │
└─────────────────────────────────────────┘
```

#### Database Schema Additions

```sql
-- Linked Steam accounts
CREATE TABLE IF NOT EXISTS steam_links (
    discord_user_id TEXT PRIMARY KEY,      -- Discord user snowflake
    steam_id64      TEXT NOT NULL,         -- Resolved SteamID64
    vanity_url      TEXT,                  -- Original vanity URL input (if used)
    linked_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
    region          TEXT DEFAULT 'us'      -- Per-user region override
);

-- Wishlist check metadata
CREATE TABLE IF NOT EXISTS wishlist_checks (
    discord_user_id TEXT PRIMARY KEY,
    last_check      DATETIME,
    item_count      INTEGER,              -- Number of items last fetched
    last_notified   DATETIME              -- Last time a DM was sent
);

-- Cached wishlist snapshots (optional, for diffing)
CREATE TABLE IF NOT EXISTS wishlist_items (
    discord_user_id TEXT NOT NULL,
    steam_app_id    TEXT NOT NULL,
    game_name       TEXT,
    fetched_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (discord_user_id, steam_app_id)
);
```

#### Behavior Rules (Wishlist)

- **Privacy**: If the Steam wishlist endpoint returns empty/403, send the user a DM: "Your Steam wishlist appears to be private. Please set it to public in your Steam profile privacy settings."
- **De-duplication**: Don't notify the same deal twice in a row. Compare current check's deal set against `last_notified` timestamp. Only DM if the set changed (new deals appeared, prices dropped, or deals expired).
- **No deals = silent**: If no games on the wishlist have active deals, don't send a DM.
- **Large wishlists**: Users with 500+ items are fine — batching handles it. But log a warning if someone exceeds 1,000 items (unusual, may indicate a public collector account).
- **Unlink**: `/unlink` deletes from `steam_links`, `wishlist_checks`, and `wishlist_items`. Stops all scheduled checks immediately.
---
## Project Structure

```
rice/
├── main.go                 # Entry point, bot setup, command registration
├── go.mod                  # Module definition
├── go.sum                  # Dependency checksums
├── internal/
│   ├── bot/
│   │   ├── commands.go     # Slash command handlers (deals, bundles, search, link, unlink, wishlist)
│   │   ├── embeds.go       # Deal, bundle, and wishlist response formatting
│   │   └── selections.go   # 60-second game selection state
│   ├── config/
│   │   └── config.go       # Environment configuration
│   ├── steam/
│   │   ├── client.go       # Steam Store Search API client
│   │   ├── wishlist.go     # Steam wishlist fetcher (undocumented endpoint)
│   │   └── linking.go      # SteamID64/vanity URL resolution (ResolveVanityURL)
│   ├── ggdeals/
│   │   ├── client.go       # GG.deals Prices + Bundles API client
│   │   └── cache.go        # Region-aware five-minute cache
│   ├── scheduler/
│   │   └── wishlist.go     # Per-user scheduled wishlist checks, batching, DM dispatch
│   └── store/
│       └── db.go           # SQLite: user preferences, steam links, wishlist checks
├── .env.example            # Template for required env vars
├── rice.db                 # SQLite database (gitignored)
└── GG.deals/               # API documentation PDFs
```

## Environment Variables

| Variable | Source | Required |
|---|---|---|
| `DISCORD_TOKEN` | Discord Developer Portal | Yes |
| `GGDEALS_API_KEY` | gg.deals/settings/ | Yes |
| `STEAM_API_KEY` | steamcommunity.com/dev/apikey | Yes (needed for `/link` vanity URL resolution) |
| `TEST_GUILD_ID` | Discord server ID | Dev only |
| `DATABASE_PATH` | Local filesystem path | Optional (defaults to `rice.db`) |
| `WISHLIST_CHECK_INTERVAL` | Duration string (e.g., `6h`) | Optional (defaults to `6h`) |

---

## Open Questions

1. ~~GG.deals API key~~ — ✅ Confirmed, key is in `.env`.
2. ~~Auto-select threshold~~ — ✅ First `type == "app"` result.
3. ~~Selection menu timeout~~ — ✅ 60 seconds.
4. ~~Steam account linking method~~ — ✅ SteamID64 + vanity URL. Friend code and OpenID dropped (no API / too complex).
5. **Wishlist deal threshold** — Should we notify on *any* price, or only when there's a meaningful discount? E.g., 10% off vs. any non-null price. Default: any deal (non-null `currentRetail` or `currentKeyshops`).
6. **Wishlist DM frequency** — 6 hours per user feels right, but should we let users configure this? Or is it fixed?
7. **Wishlist snapshot diffing** — Do we need `wishlist_items` table for diffing (detect new deals vs. stale), or is checking against `last_notified` timestamp + current deal set sufficient? Leaning toward: store the last deal set hash, only DM if changed.
