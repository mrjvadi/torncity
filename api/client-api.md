# Game client API — v1

**Contract version 1.1.** Every 1.x is compatible with 1.0: a client written
for 1.0 keeps working, and a 1.x client reads the new fields as absent on an
older server. 1.1 adds the world and the village (section 4.3): the
`settlement` object of the bootstrap, `GET /world`, `GET /world/chunks/…`,
`GET /settlements/{id}/layout`, the village commands from a client, and the
error codes `bad_chunk`, `world_not_created` and `no_settlement`. Nothing
that 1.0 returned has changed.

The contract between the game and a game client (a native build or the
Telegram Mini App build). Served by `cmd/clientapi` (package
`internal/clientapi`); realtime by Centrifugo v6 (`deployments/centrifugo`).

A client plays through **the same command pipeline as a Telegram chat**: a
command is published to the command stream with the player's identity and the
chat type `client`, the game core runs it and answers with the same screen it
would send to Telegram. The API returns that screen as rendered text, the
structured view behind it (for key screens), and its buttons as actions.

- Base URL: behind the server's reverse proxy (not public yet). In docker:
  `http://tc-clientapi:8081`; on the host: `http://127.0.0.1:58091`.
- JSON in, JSON out, UTF-8. Every error is
  `{"ok": false, "error": {"code": "...", "message": "..."}}`.
- Authenticated endpoints take `Authorization: Bearer <access_token>`.

## Contents

1. [Signing in](#1-signing-in)
2. [Commands](#2-commands)
3. [Views](#3-views)
4. [Bootstrap](#4-bootstrap) (4.3: the world and the village)
5. [Realtime](#5-realtime)
6. [Error codes](#6-error-codes)
7. [Bot commands, configuration and secrets](#7-bot-commands-configuration-and-secrets)

---

## 1. Signing in

A client signs in once and then holds two tokens:

| token | what | lifetime |
|---|---|---|
| `access_token` | JWT, HS256. Claims: `sub` = player id, `lang`, `did` = device id, `aud` = `torncity-client`, `exp`, `iat`, `jti` | `client.access_ttl` (15m) |
| `refresh_token` | opaque (`tcr1.` + 43 chars). Stored only as its SHA-256. **Replaced on every use.** | `client.refresh_ttl` (30 days) |

Every sign-in creates a **device** (a linked client). The player sees and signs
out devices from the bot with `/devices`. Signing a device out takes effect
immediately: its access token is refused on the next request.

### 1.1 Link code (native builds) — `POST /api/v1/auth/link`

The player sends `/link` (Persian: «اتصال») to the bot in its private chat and
gets a one-time 8-character code (letters `A–Z` without `I`/`O`, digits `2–9`),
valid for `client.link_code_ttl` (10 minutes), usable **once**. A player may
ask for `client.link_codes_per_hour` (5) codes an hour. The code is compared
case-insensitively; spaces and dashes are ignored.

```http
POST /api/v1/auth/link
Content-Type: application/json

{"code": "K7QM2H9A", "device_name": "Pixel 8"}
```

```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiI…",
  "token_type": "Bearer",
  "expires_in": 900,
  "refresh_token": "tcr1.q3W0fJ3m2y8Qe6o1XbVb7Jt5nJ0l2a9K1c4sQwZr9dA",
  "player": {"id": "8a4e0b3c-5b1d-4c64-9f0e-2f1d9c0b7a11", "code": "K7Q2M9A", "name": "Sara", "lang": "fa"}
}
```

`device_name` is optional (max 64 characters; shown in `/devices`). The device
plays through the bot the code was asked for in.

### 1.2 Telegram Mini App — `POST /api/v1/auth/telegram`

Opened inside Telegram, the client sends `Telegram.WebApp.initData` as it is
(the raw query string). No link code.

```http
POST /api/v1/auth/telegram
Content-Type: application/json

{"init_data": "query_id=AAH…&user=%7B%22id%22%3A424242%2C%22first_name%22%3A%22Sara%22…%7D&auth_date=1790000000&signature=…&hash=9f3c…", "device_name": "Telegram"}
```

Answer: the same as 1.1. The server checks the data as
<https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app>
describes — `hash` = HMAC-SHA256 over the sorted `key=value` lines with the
secret HMAC-SHA256("WebAppData", bot_token) — **against every active bot** (the
game runs several; the Mini App may be opened from any), and, when only the
newer `signature` field verifies, the Ed25519 third-party check with
Telegram's public key. It refuses data older than
`client.telegram_auth_max_age` (1 hour), data from the future, and **the same
data twice** (replay). Launch data is therefore single use: after signing in,
keep the refresh token; do not sign in with the same `initData` again.

The player is found or created by their Telegram user id exactly as the bot
does on first contact (same starting cash, same `player.created` event). The
device plays through the bot the Mini App was opened from.

### 1.3 Refresh — `POST /api/v1/auth/refresh`

```http
POST /api/v1/auth/refresh
Content-Type: application/json

{"refresh_token": "tcr1.q3W0fJ3m2y8Qe6o1XbVb7Jt5nJ0l2a9K1c4sQwZr9dA"}
```

Answer: a **new** pair (same shape as 1.1). The old refresh token is dead.
Presenting a refresh token that was already used is treated as theft: the
device is signed out (`refresh_token_reused`) and must link again. Refresh
shortly before `expires_in` runs out, or on a `401 unauthorized`.

### 1.4 Logout — `POST /api/v1/auth/logout`

Bearer required, no body. Signs this device out. Answer `{"ok": true}`.

Sign-in endpoints are rate limited per client address
(`client.sign_ins_per_minute`, 10).

---

## 2. Commands

### `POST /api/v1/command`

```http
POST /api/v1/command
Authorization: Bearer eyJhbGciOi…
Content-Type: application/json

{"command": "bank.show", "args": {}, "idempotency_key": "tap-5f1c"}
```

| field | |
|---|---|
| `command` | a player command of the game, `domain.action` (the list below, or any `command` an action gives you) |
| `args` | object; values are strings like the bot's buttons (numbers and booleans are accepted and turned into strings; a list of scalars is allowed; objects are refused). At most 16. |
| `idempotency_key` | optional, `[A-Za-z0-9_.:-]{1,64}`. A retry with the same key (after a timeout) does not run the command twice. Scoped to the device. |

Answer (a real `bank.show`, English):

```json
{
  "ok": true,
  "request_id": "0b9f…",
  "screen": "bank",
  "text": "🏦 Bank - Calderis branch\n\n💵 Cash: 125,000 Nil\n🏦 Bank balance: 480,000 Nil\n…",
  "view": {
    "city_code": "support", "city": "Support", "travelling": false, "no_city": false, "jailed": false,
    "cash": 125000, "bank": 480000, "withdrawal_fee_bps": 100,
    "deposits": [{"amount": 100000, "nonce": "n1", "all": false}, {"amount": 125000, "nonce": "n2", "all": true}],
    "withdrawals": null, "can_deposit": true, "can_withdraw": true, "notice": ""
  },
  "actions": [
    {"label": "📥 Deposit 100,000 Nil", "command": "bank.deposit", "args": {"amount": "100000", "nonce": "n1"}, "row": 0, "kind": "primary", "icon": "action:deposit"},
    {"label": "📥 Deposit all - 125,000 Nil", "command": "bank.deposit", "args": {"amount": "125000", "nonce": "n2"}, "row": 0, "kind": "primary", "icon": "action:deposit"},
    {"label": "✏️ Deposit any amount", "command": "bank.deposit", "input": {"field": "amount"}, "row": 1, "kind": "primary", "icon": "action:deposit"},
    {"label": "✏️ Withdraw any amount", "command": "bank.withdraw", "input": {"field": "amount"}, "row": 2, "kind": "primary", "icon": "action:withdraw"},
    {"label": "💸 Pay a player", "command": "bank.pay", "row": 3, "kind": "primary", "icon": "action:pay", "group": "bank_pay"},
    {"label": "🏛 National bank", "command": "loan.hub", "row": 3, "kind": "navigation", "icon": "action:loan"},
    {"label": "🔙 Back", "command": "player.profile.get", "row": 4, "kind": "navigation", "icon": "action:player"},
    {"label": "🔄 Refresh", "command": "bank.show", "row": 4, "kind": "navigation", "icon": "action:bank"}
  ]
}
```

| field | |
|---|---|
| `screen` | the view's name (section 3) when the screen has one; `error` for a refusal ("not enough energy", "you are travelling"…, its `text` says what); `notice` for a toast; otherwise the command's name |
| `text` | the screen, rendered and localized in the player's language, exactly as the bot shows it |
| `view` | structured facts of the screen (section 3), absent for screens without one |
| `actions` | the screen's buttons |
| `notice` | `{"text": "...", "alert": bool}` — a short message that does not replace the screen (what Telegram shows as a toast); `screen` is then `notice` |

**Actions.** Each button of the screen, translated from its Telegram callback
data by the same parser the gateway reads a pressed button with, so sending
`{command, args}` back is exactly pressing the button:

| field | |
|---|---|
| `label` | the button's text |
| `command`, `args` | send them back as they are |
| `input` | `{"field": "amount", "text": false}` — the button asks for a value: ask the player, put the answer in `args[field]` and send. `text: true` means words (a name), otherwise an amount. |
| `url` | a link button (no command) |
| `row` | the keyboard row, from 0 |
| `kind` | how to draw it: `primary`, `secondary`, `danger`, `navigation`, `back` or `confirm` |
| `icon` | a stable asset key (`action:deposit`, `nav:profile`…) the client maps to its own picture |
| `group` | present only for a step of a multi-step flow (a wizard, a proposal and its answer); steps sharing a `group` value belong together |

`kind`, `icon` and `group` come from `configs/actions.yml`, keyed by
`command` — the same table for every client, so a native build and the
Telegram keyboard always agree on what a button *means* even though only the
native build draws it differently. A command the file does not mention gets
the table's default (`secondary`, `action:default`). A `url` button (no
`command`) carries none of the three: it is a plain link.

Buttons whose command the game no longer serves are left out.

**Channels.** The client counts as the player's **private** chat. Commands the
game plays only in a Telegram group (`configs/commands.yml`, `channel: group`
— crime, for one) are refused with `403 group_only` and a localized message,
unless `client.group_commands` is set to `allow`.

**Timeouts.** The answer is awaited `client.command_timeout` (15s); after that
`504 timeout`. The command may still have run: retry with the same
`idempotency_key`.

Commands are rate limited per player (`client.commands_per_minute`, 120).

### Commands a client starts from

| command | args | screen |
|---|---|---|
| `player.profile.get` | — | `profile` (home) |
| `player.settings` | — | settings |
| `player.language.set` | `lang` | settings |
| `map.list` | `page`? | `city_map` — the player's own city and its places |
| `place.go` | `place` | walk to a place |
| `map.cities` | `page`? | `cities` — other cities to travel to |
| `travel.options` | `city` | `travel_options` |
| `travel.start` | `city`, `mode`, `max` (the fare shown, a ceiling) | journey started |
| `travel.status` | — | `travel_status` |
| `bank.show` | — | `bank` |
| `bank.deposit` / `bank.withdraw` | `amount`, `nonce`? | `bank` |
| `inventory.show` | `page`? | `inventory` |
| `job.status` | — | `job_status` |
| `life.me` | — | `life` |
| `device.link` / `device.list` / `device.revoke` | — / — / `device` | link code, linked devices |

Everything else is reached through `actions`. The full list of player
commands is `internal/commands`; argument names are
`internal/gateway/routing` (`argNames`).

---

## 3. Views

Screens that carry a `view`, and its shape. Rules for every view:

- keys are snake_case;
- durations are **whole seconds** in keys ending `_seconds`;
- instants are RFC 3339 UTC strings, or `null`;
- money is an integer in minor units;
- a missing object or list is `null`;
- a named thing is `{"code": "...", "name": "..."}` where `name` is the
  **authored** name; the localized name of cities and places is in the
  bootstrap (by code) and in `text`.

A view is attached only when the screen is shown to the player alone.

**`profile`** (`player.profile.get`)

```json
{"name": "Sara", "code": "K7Q2M9A", "avatar": "🦊", "city_code": "support", "city": "Support",
 "place": {"code": "old_town", "name": "Old Town"},
 "walk": {"to": {"code": "harbour", "name": "Harbour"}, "remaining_seconds": 120, "arrives_at": "2026-09-26T10:02:00Z"},
 "level": 3, "xp": 420, "next_level_xp": 600, "energy": 80, "max_energy": 100, "energy_full_in_seconds": 900,
 "health": 100, "max_health": 100, "cash": 125000, "bank": 480000,
 "travelling": false, "travel_to_code": "", "travel_to": "", "travel_remaining_seconds": 0,
 "work": {"job": {"job": {"career_code": "", "career_name": "", "rank": "", "title": ""}, "city_code": "", "city": "", "pay": 0, "shift_ends_in_seconds": 0},
          "course": {"course": {"code": "", "name": ""}, "remaining_seconds": 0, "paused": false}, "certificates": 0},
 "jail": null, "hospital": null, "achievements": 2,
 "rank": {"code": "", "name": "", "emoji": ""}, "age": 24, "stage": {"code": "", "name": ""},
 "needs": {"hunger": 20, "sleep": 35, "stress": 10, "happiness": 70, "body_bps": 10000, "xpbps": 10000, "pressing": []}}
```

`jail` and `hospital`, when not null: `{"city_code", "city", "remaining_seconds", "ends_at"}`.

**`dashboard`**: `{name, city_code, city, place, walk, level, energy, max_energy, travelling, cash, bank, jail}`.

**`city_map`** (`map.list`)

```json
{"city_code": "support", "city": "Support", "no_city": false, "travelling": false, "travelling_to_code": "", "travelling_to": "",
 "here": {"code": "old_town", "name": "Old Town"}, "walking": null, "others": 0,
 "places": [{"place": {"code": "harbour", "name": "Harbour"}, "walk_seconds": 180, "energy": 2,
             "services": ["bank"], "departures": null, "shops": null, "here": false}]}
```

**`cities`** (`map.cities`): `{destinations: [{code, name, distance_km}], page, pages, origin_code, origin, travelling, travelling_to_code, travelling_to}`.

**`travel_options`** (`travel.options`): `{from_code, from, to_code, to, cash, requoted, options: [{mode_code, mode_name, fare, wait_seconds, energy, busy, vehicle: {code, name} | null, condition}]}`.
Start a journey with `travel.start {city: to_code, mode: mode_code, max: fare}`.

**`travel_status`** (`travel.status`): `{from_code, from, to_code, to, mode_code, mode_name, remaining_seconds, arrives_at}`.

**`bank`** (`bank.show` and after a deposit or withdrawal): see the example in section 2. `deposits`/`withdrawals` are the quick amounts; send `bank.deposit {amount, nonce}` with the option's values.

**`inventory`** (`inventory.show`): `{lines: [{item: {code, name}, design, category, qty, serial, quality, uses_left, durability}], page, pages, total, in_escrow}`.

**`job_status`** (`job.status`): `{employed, job: {career_code, career_name, rank, title}, employer, city_code, city, pay, energy_cost, energy, max_energy, performance, shifts_in_tier, total_earned, at_workplace, top_tier, shift_length_seconds, workplace: {code, name}, walk_to_work_seconds, shift: {remaining_seconds, ends_at} | null, next: {…}, promotion_ready, missing: [{kind, skill, course_code, course_name, city_code, city, have, need, met, wait_seconds}]}`.

**`life`** (`life.me`): `{needs: {hunger, sleep, stress, happiness, body_bps, xpbps, pressing}, age, stage: {code, name}, intelligence, intelligence_max, course_bps, skill_bps, rank: {code, name, emoji} | null, next: … | null, next_need, worth: {cash, bank, escrow, equity, property, goods, debts, savings, gold, loans, total}, spots: [{spot, place, price, rest, relief, way: {place, walk_seconds} | null}], sleep_in_seconds, home, notice, notice_args}`.

New fields may be added to any view; a client must ignore keys it does not
know. Renaming or removing one is a new API version.

### 3.1 Every other screen

The screens above are the ones a client is likely to start from. Past them,
almost every screen the game renders carries a view of its own: crime,
travel, work, education, skills, the social graph, shops, the item and stock
markets, auctions, property, elections, factions, finance (loans, savings,
insurance), the gold exchange, player-held offices (governance, the
legislature, appointments), diplomacy, health, missions, specialist
recruitment, achievements, linked devices, settings and a character's life
and legacy.

The **screen name** is one of the `Screen*` constants in
`internal/telegram/screens/views.go` (the single source of truth — a name is
never renamed once shipped, only added to); its **shape** is exactly the
corresponding `XxxView` Go struct in that package, encoded with the rules
above (a `CrimeHubView` becomes the `crime_hub` screen's view, field by
field). A screen not reached from a client yet — currently the production,
company, military and war screens — carries no view; its `screen` is still
its command's name and its `text` is still the full Telegram rendering, so a
client can show it as plain text until its view lands.

Worked examples of every wired screen, in every field combination the game's
own tests cover, live in
`internal/telegram/screens/testdata/view-snapshots/<language>/<area>.json` —
the same golden files that guard this contract in CI. A screen shown to a
Telegram group carries no view (money and identity stay out of a shared
chat); the client-only channel never has this problem, since a client always
counts as the player's private chat (section 2).

---

## 4. Bootstrap

### `GET /api/v1/bootstrap`

Load once after signing in (and after a content change).

```json
{
  "player": {"id": "8a4e…", "code": "K7Q2M9A", "name": "Sara", "lang": "fa", "city_code": "support", "city": "ساپورت"},
  "content_version": 42,
  "languages": [{"code": "en", "name": "English"}, {"code": "fa", "name": "فارسی"}],
  "cities": [{"code": "support", "name": "ساپورت"}],
  "places": [{"code": "old_town", "name": "شهر قدیم"}, {"code": "harbour", "name": "بندر"}],
  "server_time": "2026-09-26T10:00:00Z",
  "realtime": true,
  "settlement": {
    "id": "4d1c…", "code": "v-k3x9", "name": "آمل", "tier": "village",
    "world_cell": 18211, "centre": {"lat": 36.4, "lon": 52.3, "chunk": {"face": 4, "lod": 10, "x": 523, "y": 512}},
    "is_head": true, "resident": false, "grid_lots": 5,
    "layout_path": "/api/v1/settlements/4d1c…/layout"
  }
}
```

Names are in the player's language. `places` are the places of the player's
current city. `realtime` says whether section 5 is available. `settlement`
(1.1) is the player's own settlement and is absent when they belong to none;
see 4.3.

---

### 4.1 Content catalogue — `GET /api/v1/content?since=<version>`

Every content entry a client may have to draw, by table, with its name in
every language and the asset keys its art is looked up by
(`internal/clientapi/catalogue.go`). Tables: `city`, `place`,
`company_type`, `item`, `component`, `mode`, `crime`, `course`, `skill`,
`technology`, `military_unit`. `asset.icon` is always `<table>:<code>`;
`asset.model` is set for tables drawn as buildings or vehicles (`place`,
`company_type`, `mode`, `military_unit`). Items, components, crimes, skills
and company types carry a `category`, places `kind: place`, military units
their branch as `category`, for fallback art.

```json
{"version": "v42", "langs": ["en", "fa"],
 "entries": {"place": [{"code": "bazaar", "name": {"en": "Bazaar", "fa": "بازار"},
                        "asset": {"model": "place:bazaar", "icon": "place:bazaar"}, "kind": "place"}], "...": []}}
```

With `?since=` naming the current version the answer is only
`{"version": "v42", "unchanged": true}`.

### 4.2 City map — `GET /api/v1/world/city?code=<city>`

A city's map, the player's own city when `code` is left out
(`internal/clientapi/worldmap.go`). The game knows places by walk time, not
position, so the server lays the map out the same way for every client:
2×2 lots between one-cell roads, filled from the centre out — the city's
places nearest walk first (the city centre in the middle, the airport on
the outskirts), then its companies oldest first, then greens and plazas.

```json
{"city": "support", "version": 2934512, "grid": {"w": 16, "h": 16},
 "water": {"side": "south", "width": 6}, "roads": [[0, 0], [1, 0]],
 "plots": [{"id": "company:Q7M2K9B", "x": 4, "y": 7, "w": 2, "h": 2, "kind": "company",
            "model": "company:factory", "rot": 0,
            "ref": {"table": "company_type", "code": "factory", "company_id": "Q7M2K9B", "owner": "Sara"},
            "name": {"en": "Alborz Steel", "fa": "Alborz Steel"}}]}
```

**The world has one content city, `support`** (docs/adr/0032): the seven
older city codes (`ostmarch`, `fenwick_span`, `aldrin_hollow`, `brennhaven`,
`kessmoor`, `calderis`, `vantor_reach`) no longer exist and answer
`city_not_found`. A client that cached a `city_code` sees `support` at its next
login, and the realtime channel of that city is `city:support`. Money is in
`SUP` (the neutral currency, shown «ساپ»); every amount keeps its value.

Kinds: `place`, `company`, `decor`. `version` changes whenever a plot does.
A company plot's tap sends `company.show {id}`, which the API serves as
`company.view {code}`.

### 4.3 The world and the village (contract 1.1)

The generated planet (docs/adr/0028) and a group's village on it. The old
city map above stays for `support`, the neutral city.

**Who may ask.** All of these need a signed-in player. Terrain is public
knowledge in the game, but a chunk is generated on demand, so anonymous
access would let anyone make a replica compute, and the per-player rate
limit needs a player to count against. The responses are still cacheable by
anyone (`Cache-Control: public` on chunks), so a CDN in front may serve a
chunk it already has.

#### The world — `GET /api/v1/world`

The active world's numbers, so a client can compute chunk addresses. `404
world_not_created` until an operator has run `admin world create`.
`ETag` + `Cache-Control: no-cache`: revalidate, the answer is a few hundred
bytes (about 1.3 KB with the biome table).

```json
{
  "id": "0a0a…", "seed": "20280928", "generator_version": 1, "params_hash": "3fa9…",
  "chunk_codec_version": 1,
  "chunk": {"faces": 6, "tile_edge": 32, "min_lod": 0, "max_lod": 10, "header_bytes": 33, "tile_bytes": 5},
  "tile_m": 305.4, "lot_m": 30.54, "lots_per_tile": 10, "planet_radius_km": 6371,
  "biomes": [{"index": 0, "code": "ocean", "water": true, "color": "1a4d73"},
             {"index": 6, "code": "temperate_forest", "color": "4c7a3d"}],
  "chunk_path": "/api/v1/world/chunks/{face}/{lod}/{x}/{y}",
  "created_at": "2026-09-30T12:00:00Z"
}
```

`seed` is a decimal string (a 64-bit number). The planet is a cube-sphere:
6 faces (OpenGL cubemap order: +X, -X, +Y, -Y, +Z, -Z), each cut by a
quadtree. At `lod` L a face has `2^L` chunks along each edge, `x` and `y`
in `[0, 2^L)`; LOD 0 is one chunk per face (the zoomed-out map),
`max_lod` (10) the gameplay resolution, `tile_edge` (32) × 32 tiles of
`tile_m` metres (each coarser LOD doubles the tile). A settlement lot is
`lot_m` metres, `lots_per_tile` (10) to a base tile's edge. `biome` in a
chunk is an index into `biomes`.

#### A chunk — `GET /api/v1/world/chunks/{face}/{lod}/{x}/{y}`

The binary chunk of the active world (`Content-Type:
application/vnd.torncity.chunk`), about 5 KB raw and 1.5–1.8 KB gzipped.
A chunk is a pure function of (world, generator version, address), so it is
**immutable**:

- `ETag: "<world id>.g<generator version>.c<codec version>.<face>.<lod>.<x>.<y>"`
  (strong; `-gz` is added before the closing quote on the gzip variant), and
  `Cache-Control: public, max-age=31536000, immutable`. `If-None-Match`
  answers `304` without generating anything.
- `Accept-Encoding: gzip` is honoured (`Content-Encoding: gzip`, `Vary:
  Accept-Encoding`). Brotli is left to the reverse proxy.
- Rate limited per player (`client.chunks_per_minute`, 1200).
- `400 bad_chunk`: face outside 0–5, lod outside 0–`max_lod`, or x/y outside
  `[0, 2^lod)`, or a segment that is not a plain decimal number.
  `404 world_not_created` when there is no world.

**Byte layout**, little-endian throughout:

| offset | size | field |
|---|---|---|
| 0 | 4 | magic, the bytes `4B 43 4E 57` |
| 4 | 1 | codec version (`chunk_codec_version`, 1) |
| 5 | 4 | generator version, `uint32` |
| 9 | 8 | world seed, `uint64` |
| 17 | 1 | face, `int8` |
| 18 | 1 | lod, `int8` |
| 19 | 4 | x, `int32` |
| 23 | 4 | y, `int32` |
| 27 | 2 | tile edge, `uint16` (32) |
| 29 | 4 | tile count, `uint32` (= edge²) |
| 33 | 5 × count | tiles, row-major (`index = j × edge + i`, `i` along x, `j` along y): elevation `int16`, biome `uint8`, flags `uint8`, deposit `uint8` |
| … | 2 | deposit count, `uint16` |
| … | … | per deposit: `uint8 n`, `n` bytes of deposit id, `uint8 m`, `m` bytes of resource code, `uint8` tile x, `uint8` tile y |

Tile `flags`: bit 0 ocean, bit 1 stream, bit 2 lake. `deposit` is `0`, or
1 + the index into the chunk's deposit list. Elevation is in the
generator's height unit (sea level is not 0; read it against the ocean
tiles), roughly metres. Coarser LODs carry no local detail and no deposits.

#### Your settlement — `bootstrap.settlement`

The settlement the player heads (`is_head`) or lives in (`resident`), with
`centre` (latitude/longitude of the middle of its lot grid, and the base-LOD
chunk holding it) and the side of the grid, `grid_lots` (5 for a village, 9
for a town, 15 for a city). Only the head may place, cancel or demolish
buildings. Absent when the player belongs to no settlement. Re-read the
bootstrap after a group founds its village.

#### The layout — `GET /api/v1/settlements/{id}/layout`

The lot grid with its terrain, and the buildings on it. The terrain is
`settlement.SampleGridDetail`, the very sampling the placement rules check a
building against, so what the layout marks `buildable` is what the server
accepts. `404 not_found` for an unknown id.

```json
{
  "version": "a5bcccc66911781d", "detail": "full",
  "viewer": {"member": true, "can_place": true},
  "settlement": {"id": "4d1c…", "code": "v-k3x9", "name": "آمل", "tier": "village", "world_cell": 18211,
                 "centre": {"lat": 36.4, "lon": 52.3, "chunk": {"face": 4, "lod": 10, "x": 523, "y": 512}}},
  "grid": {"lots": 5, "lot_m": 30.54, "origin": {"lat": 36.3989, "lon": 52.2989}, "slope_limit": 15},
  "lots": [[{"height_m": 1786.21, "slope_m": 0.5, "buildable": true, "biome": "temperate_forest", "tags": ["temperate_forest"]},
            {"height_m": 1783.9, "slope_m": 4.1, "buildable": false, "biome": "ocean", "water": "ocean", "tags": ["ocean", "coastal_lot"]}]],
  "buildings": [
    {"id": "9c1e…", "type": "civic_hall", "x": 1, "y": 1, "w": 2, "h": 2, "rotated": false, "state": "built", "visual_seed": 2891077541},
    {"id": "0b7d…", "type": "militia_camp", "x": 3, "y": 0, "w": 1, "h": 2, "rotated": true, "state": "under_construction",
     "started_at": "2026-09-30T11:00:00Z", "finish_at": "2026-09-30T11:45:00Z", "visual_seed": 118034}
  ],
  "roads": [{"x": 0, "y": 0}]
}
```

- **Geometry.** `lots[y][x]` is lot `(x, y)`; `x` grows east and `y` north,
  each a `lot_m` step on the ground; `grid.origin` is the lat/lon of the
  centre of lot `(0, 0)`. A lot is `slope_limit` steep when a neighbour differs
  by more than that (`tags` then holds `sloped_lot`). `water` is `ocean`,
  `lake`, `river` or `stream`, absent on dry ground. `tags` are the terrain
  tags the placement rules use (`river_lot`, `coastal_lot`, `sloped_lot`,
  `ore_deposit`, the biome). `buildable` is the terrain alone: occupancy is
  in `buildings`.
- **Buildings.** `x`, `y` is the top-left lot; `w`, `h` the footprint
  **after** the turn (`rotated` says a quarter turn was chosen at placement:
  a 2×1 camp turned is 1×2; the turn is permanent like the lot). `type` is a
  code of the `settlement_building` table of the content catalogue (4.1: name
  per language, `footprint`, `category` = role). `visual_seed` seeds the
  client's own look of the building; no model is ever shipped. `state`:
  `planned` (paid, not started), `under_construction`, `built`, `damaged`
  (`damage_bps` 1–9999), `ruin` (10000). Demolished and cancelled buildings
  are not listed: their lot is free. No rule damages a building yet, so
  today only `under_construction` and `built` occur.
- **Roads** repeat, for a client that draws them apart, the `road` buildings
  that stand.
- **Who sees what** (ADR 0030 §2.1). A **member** (the head, or anyone who
  holds an office in the village or lives in it) gets `detail: "full"`: every
  building with its id, timers and damage. Anyone else gets `detail:
  "coarse"`: the terrain, which is public, and only the buildings that stand
  (`built`): no ids, timers, construction, damage or roads under
  construction. Whether a foreign village is *in range* (the fog of war) is the
  realtime channel's business (section 5); the layout does not check it.
- **Polling.** `version` changes exactly when something a client draws
  changes (a building placed, finished, cancelled, damaged, the tier). It
  is also the `ETag` (`"<version>.<detail>"`), and the answer is `private,
  no-cache`: send `If-None-Match` and get `304`. A build's progress is not in
  the version: compute it from `started_at`, `finish_at` and the bootstrap's
  `server_time`. Size: about 3 KB for a village (5×5), 20 KB for a city
  (15×15). Rate limited per player (`client.layouts_per_minute`, 120).

#### Building from a client

The head builds with the same commands as the group, through `POST
/api/v1/command`; the settlement is always the player's own, nothing names
it. The same handler and the same rules run: there is no second copy. The
village commands are open to a client even when `client.group_commands` is
`refuse` (`client: true` in `configs/commands.yml`); founding a village still
needs a group.

| command | args | does |
|---|---|---|
| `settlement.build` | — | the build menu (`settlement_build_menu`) |
| `settlement.build.lots` | `code`, `rotate`? (`1`) | `settlement_build_lots`: for every lot, `state` and `fits` for this building |
| `settlement.build.place` | `code`, `x`, `y`, `rotated`? (bool), `confirm`? | without `confirm`: `settlement_build_confirm` (cost, materials, build time), nothing changes. With `confirm: "confirm"`: pays, draws the materials, starts the build, answers `settlement_construction_progress` |
| `settlement.build.cancel` | `id` | calls off a building **under construction**; the spend is forfeited, the lot is free again |
| `settlement.build.demolish` | `id` | removes a **finished** building; part of its cost returns to the treasury |
| `settlement.build.progress` | — | what is going up |
| `settlement.overview` | — | the village status |
| `settlement.knowledge` | — | the knowledge list |
| `settlement.knowledge.research` / `.buy` | `code` | starts a research / buys the item from Support |

(`place` also takes the Telegram spelling `lot: "3-1"` / `"3-1-r"`.) Send an
`idempotency_key` with every write, as in section 2. Rotating is a choice
made at placement: `rotated: true` turns the footprint; ask `lots` with
`rotate: 1` first to see which lots fit the turned building.

A refused command is `200` with `ok: false`, `screen: "village_refusal"` and a
coded error, which a client localises itself (`error.message` is the same
sentence Telegram shows, in the player's language):

```json
{"ok": false, "screen": "village_refusal", "view": {"kind": "unbuildable", "back": ""},
 "error": {"code": "village_unbuildable", "message": "🌊 روی این قطعه نمی‌توان ساخت."}, "actions": [...]}
```

| `error.code` | meaning |
|---|---|
| `village_no_settlement` | the player belongs to no settlement |
| `village_not_office_holder` | not the head |
| `village_unbuildable` | a lot of the footprint is water, a river or too steep |
| `village_occupied` | a lot of the footprint holds a building |
| `village_out_of_bounds` | the footprint leaves the grid |
| `village_terrain` | the building needs terrain it does not stand on (coast, deposit, arable land) |
| `village_prerequisite` | knowledge or a building of a role is missing |
| `village_literacy` | literacy too low |
| `village_concurrent_cap` | as many builds running as the tier allows |
| `village_insufficient_funds` | the treasury cannot pay |
| `village_materials` | the village stock lacks a material |
| `village_not_found` | unknown building type, id or malformed lot |
| `village_not_demolishable` / `village_not_cancellable` | wrong state for the action |
| `village_busy`, `village_already_owned`, `village_not_available` | research / purchase refusals |

A client learns the outcome of a placement by re-reading the layout (its
`version` moves) until realtime carries it.

## 5. Realtime

Centrifugo v6 (<https://centrifugal.dev/docs>), WebSocket endpoint
`/connection/websocket` (JSON protocol; any official Centrifugo client SDK).

### 5.1 Connecting — `GET /api/v1/realtime/token`

```json
{"token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9…", "expires_at": "2026-09-26T10:15:00Z",
 "user": "8a4e…", "channels": ["player:8a4e…"]}
```

A Centrifugo **connection token** (HS256; claims `sub` = player id, `exp`
= `client.realtime_token_ttl` (15m), `iat`, `channels`). The `channels` claim
subscribes the connection **server-side** to the player's personal channel
`player:<player_id>`: the client does not (and cannot) subscribe to it itself.
Fetch a new token when the SDK asks for one (its `getToken` callback).

### 5.2 A city channel — `GET /api/v1/realtime/subscribe?channel=city:<code>`

```json
{"token": "eyJhbGciOi…", "expires_at": "2026-09-26T10:15:00Z", "channel": "city:support"}
```

A **subscription token** (claims `sub`, `channel`, `exp`, `iat`) for the
public channel of the city the player is **currently in**; any other channel
is `403 forbidden_channel`. Re-subscribe after travelling.

### 5.3 What arrives

On `player:<id>` — every notice the bot sends the player (a journey landed, a
payment received, a shift paid…), as well as to Telegram:

```json
{"type": "notice", "kind": "travel.completed", "text": "🛬 You have arrived in Calderis…", "screen": "…", "view": {…}}
```

`kind` is the game event (`domain.event`); `text` is localized for the player;
`screen`/`view` are present when the notice's screen has a view.

Also on `player:<id>` — a HUD snapshot, sent again (debounced,
`notifications.vitals_min_interval`, 2s) whenever a notice's event may have
changed it, from any source (Telegram or a client):

```json
{"type": "vitals", "cash": 125000, "bank": 480000, "energy": 80, "max_energy": 100,
 "health": 100, "max_health": 100, "xp": 420, "level": 3, "unread": 2}
```

A full snapshot, not a diff: a client always replaces what it has with the
latest one, so an out-of-order delivery costs nothing. It is best effort,
like everything in this section — a client also learns these numbers from
every command's own `view` and from `bootstrap`/polling, so a missed or
delayed `vitals` publication is never the only way to find out.

On `city:<code>` — the city's public announcements (arrivals, jailings,
companies founded, elections, strikes…):

```json
{"type": "announce", "kind": "crime.jailed", "text": "📣 …", "texts": {"fa": "📣 …", "en": "📣 …"}}
```

`text` is in the game's default language; pick `texts[lang]` when present.

Both namespaces keep the last 20 publications for 5 minutes and recover them
on reconnect. Clients can never publish; only the server API key can (the notifier and
the operators' panel). A publishing failure never affects Telegram
delivery.

---

## 6. Error codes

| HTTP | `code` | meaning |
|---|---|---|
| 400 | `bad_request` | body is not JSON, or an argument is not usable |
| 400 | `unknown_command` | not a command the game serves to players |
| 401 | `unauthorized` | missing/invalid/expired access token, or the device was signed out — refresh, then sign in again |
| 401 | `invalid_code` | link code unknown, used or expired |
| 401 | `invalid_init_data` | Mini App data not signed by any of our bots, or unreadable |
| 401 | `init_data_expired` | Mini App data older than allowed |
| 401 | `init_data_replayed` | this Mini App data was already used |
| 401 | `invalid_refresh_token` | refresh token unknown, expired or of a signed-out device |
| 401 | `refresh_token_reused` | refresh token used twice: the device is signed out |
| 403 | `group_only` | the command is played in a Telegram group (localized message) |
| 403 | `banned` | an operator has banned this account; commands are refused until it is lifted |
| 403 | `forbidden_channel` | not the player's city channel |
| 400 | `bad_chunk` | (1.1) the chunk address names no chunk of this world |
| 404 | `not_found` | |
| 404 | `world_not_created` | (1.1) no world has been created yet (`admin world create`) |
| 404 | `no_settlement` | (1.1) the village endpoints are not configured on this server |
| 409 | `relink` | the device's bot is gone; link again |
| 429 | `rate_limited` | too many sign-ins, link codes or commands |
| 503 | `realtime_unavailable` | realtime is not configured |
| 504 | `timeout` | the command was not answered in time (retry with the same idempotency key) |
| 500 | `internal` | |

A command the game **refuses** (not enough energy, travelling, not enough
money…) is not an HTTP error: it is `200` with `screen: "error"` and the
refusal in `text`, as in the bot. (1.1) A refused **village** command is `200`
with `ok: false`, `screen: "village_refusal"` and an `error.code` of the form
`village_<kind>` (4.3).

---

## 7. Bot commands, configuration and secrets

**Bot commands** (private chat only):

| command | Persian | |
|---|---|---|
| `/link` | «اتصال» | a one-time code to link a game client |
| `/devices` | «دستگاه‌ها» | the linked clients, each with a sign-out button |

**Configuration** (`configs/config.yml`, override with `TORN_<SECTION>_<KEY>`):
`client.*` (listen, trusted_proxies, access_ttl, refresh_ttl, link_code_ttl,
link_codes_per_hour, sign_ins_per_minute, max_devices, command_timeout,
commands_per_minute, max_body_bytes, telegram_auth_max_age,
realtime_token_ttl, group_commands, mini_app_url, and from 1.1
chunk_cache_entries, chunks_per_minute, layouts_per_minute,
world_recheck_interval) and `realtime.*` (api_url, publish_timeout). The
world endpoints regenerate the planet from the active world's seed, so
`clientapi` reads `worldgen.*` and `configs/content` (`TORN_CONTENT_DIR`)
like the game.

**Secrets** (environment only, `.env`; never committed):

| variable | used by | |
|---|---|---|
| `CLIENT_JWT_SECRET` | clientapi | signs access tokens (≥ 32 characters) |
| `CENTRIFUGO_TOKEN_HMAC_SECRET` | clientapi, centrifugo | signs/verifies realtime tokens |
| `CENTRIFUGO_API_KEY` | notifier, panel, centrifugo | the realtime server API key |
| `CENTRIFUGO_ALLOWED_ORIGINS` | centrifugo | browser origins allowed a WebSocket, space-separated (Mini App, panel); native clients send none |
| `BOTxx_TOKEN` | clientapi | the bots' tokens (already in `.env`) — Mini App data is checked with them |

**Services**: `clientapi` (127.0.0.1:58091 → 8081, alias `tc-clientapi`),
`centrifugo` (image `centrifugo/centrifugo:v6.7.0`, 127.0.0.1:58092 → 8000,
alias `tc-centrifugo`, admin UI off). On the server both join
`antispam_default`; exposing them through the reverse proxy is a later step
(route the API and `/connection/websocket`).

**Migrations**: `0033_client_devices` (tables `client_devices`,
`client_refresh_tokens`); `0049_village_client` (1.1: a building's turn,
finish time and damage, cancelling, and a demolished or cancelled building
no longer holds its lot).

### Opening the Mini App from the bot (when its URL is public)

Not wired in code yet: set `client.mini_app_url` to the Mini App's https
address (it also becomes the one browser origin the API answers with CORS),
add that origin to `CENTRIFUGO_ALLOWED_ORIGINS`, then for **each** bot in
@BotFather:

1. `/mybots` → the bot → **Bot Settings** → **Configure Mini App** →
   **Enable Mini App** → send the URL (makes `t.me/<bot>/<app>` links work);
2. `/mybots` → the bot → **Bot Settings** → **Menu Button** →
   **Configure menu button** → send the same URL and a title (the button
   beside the message box opens it).

(The same can be done per bot with the Bot API's `setChatMenuButton` and a
`MenuButtonWebApp`.) Whichever bot the player opens it from is the bot the
device plays through.
