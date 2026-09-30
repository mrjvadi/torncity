# Game client API — v1

**Contract version 1.4.** Every 1.x is compatible with 1.0: a client written
for 1.0 keeps working, and a 1.x client reads the new fields as absent on an
older server. 1.1 adds the world and the village (section 4.3): the
`settlement` object of the bootstrap, `GET /world`, `GET /world/chunks/…`,
`GET /settlements/{id}/layout`, the village commands from a client, and the
error codes `bad_chunk`, `world_not_created` and `no_settlement`. 1.2 adds
presence and the settlement channel (sections 5.4 and 5.5): the
`settlement:<id>` realtime channel (also on the connection token),
`POST /realtime/heartbeat`, `GET /players/{id}/status`,
`GET /settlements/{id}/players`, `settlement.who` and the error code
`not_in_settlement`. 1.3 adds the founding form (section 4.4):
`settlement.found.draft` and `settlement.found.submit`, the `founding_*`
error codes, the group's Mini App button, and `emblem`, `motto` and
`currency` on the bootstrap `settlement`. 1.4 adds land and private
buildings (section 4.3, "Land and private buildings"): `tenure`, `terms` and
`viewer.resident` on the layout, `private`, `owner` and `mine` on a layout
building, and the commands `settlement.land`, `settlement.lot.buy`,
`settlement.private`, `settlement.private.lots`, `settlement.private.place`,
`settlement.mine`, `settlement.home.rest`, `settlement.tax.pay`,
`settlement.terms` and `settlement.work`. Nothing that 1.0, 1.1, 1.2 or 1.3
returned has changed.

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
4. [Bootstrap](#4-bootstrap) (4.3: the world and the village, 4.4: the founding form)
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
| `settlement.join` | `settlement` (the village id), `confirm`? | makes the village the player's **home** (a player has one). Without `confirm`: `village_residence_confirm`, nothing changes. With `confirm: "confirm"`: moves the home and answers `village_residence_done`; the realtime token then carries `settlement:<id>` (fetch a new one) and the roster gains the player |
| `settlement.leave` | `settlement`? , `confirm`? | sends the player home to the neutral city, same two steps |

`settlement.overview` carries `resident` (does the viewer live here) and
`settlement_id`. The village founder is a resident from the moment of founding;
everyone else joins. Moving home has a cool-down (`settlement.residence_cooldown`)
that also applies to buying a home elsewhere; the head cannot leave.

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
| `village_already_resident` / `village_not_resident` | join / leave when the home is / is not this village |
| `village_residence_cooldown` | the home moved too recently (the message names the wait) |
| `village_holds_office` | the head cannot leave the village |
| `village_no_home` | the city to return to is not configured |

A client learns the outcome of a placement by re-reading the layout (its
`version` moves) — or, with realtime, from the settlement channel's
`layout_version` (section 5.4) — until it does.

#### Land and private buildings (contract 1.4)

Every lot is **commons** (the village's) until a resident buys it; a lot a
resident owns is **freehold**. A resident builds a **private building** (a
house, a stall) on their own lot from their own cash; the village head builds
civic buildings and never on a private lot.

The layout of a **member** gains (a stranger's layout has none of these):

```json
{"viewer": {"member": true, "can_place": false, "resident": true},
 "tenure": [{"x": 0, "y": 3, "tenure": "freehold", "mine": true, "owner": "Sara"}],
 "terms": {"lot_price": 400, "permit_fee": 100, "tax_bps": 200},
 "buildings": [{"id": "…", "type": "cottage", "x": 0, "y": 3, "private": true, "owner": "Sara", "mine": true, "state": "built", "…": "…"}]}
```

A lot **not** in `tenure` (and buildable, with nothing on it) is on sale at
`terms.lot_price`. `mine` is relative to the asker. `version` moves when a lot
or a building changes hands (member and head versions only; the public version
never carries owners). The settlement channel's `lot_bought` and
`build_started` events carry the new `layout_version` as usual.

| command | args | does |
|---|---|---|
| `settlement.land` | — | `settlement_land`: `rows[y][x]` with `state` (`free`, `mine`, `taken` + `owner`, `building`, `road`, `water`, `steep`), `price`, `cash`, `owned`, `max`, `can_buy` |
| `settlement.lot.buy` | `x`, `y`, `confirm`? | resident only. Without `confirm`: `settlement_lot_buy_confirm` (price, cash), nothing changes. With `confirm: "confirm"`: pays the price **to the village treasury**, the lot is the player's; answers `settlement_lot_buy_done` |
| `settlement.private` | — | `settlement_private_menu`: the citizen catalogue, only what the village can build now (`lines[]`: `building`, `cost_money`, `permit_fee`, `materials[]` with `need`/`have`/`buy`/`buy_cost`, `build_time`, `total`, `affordable`) |
| `settlement.private.lots` | `code`, `rotate`? (`1`) | `settlement_private_lots`: `rows[y][x]` with `fits` true only where the whole footprint is the player's own free land |
| `settlement.private.place` | `code`, `x`, `y`, `rotated`?, `confirm`? | without `confirm`: `settlement_private_confirm` (the bill), nothing changes. With `confirm: "confirm"`: pays the cost and the permit (to the treasury), takes materials the player carries and buys the rest at the reference price, starts the timer; answers `settlement_mine` |
| `settlement.mine` | — | `settlement_mine`: own lots and buildings, `home`, `can_rest`, `assessed`, `tax_per_period`, `debt` |
| `settlement.home.rest` | — | the owner of a finished house rests (a little health and mood, once per cool-down; pays nothing) |
| `settlement.tax.pay` | — | pays the unpaid property tax as far as cash goes |
| `settlement.terms` | `lot_price`?, `permit_fee`?, `tax_bps`? | the head reads the terms and moves a lever inside its bounds; answers `settlement_terms` |
| `settlement.work` | — | `settlement_work`: where work is (Support until the village has producers) |

Cancelling or demolishing a private building (`settlement.build.cancel` /
`.demolish`) is the **owner's**, never the head's; nothing returns to the
treasury for it. The property tax (`terms.tax_bps` of the lots' price plus the
buildings' cost, each period) is collected on the village's own tick; what a
resident cannot pay stays a debt.

Refusals (`error.code`, all `village_…`): `village_citizen_lot_taken`,
`village_citizen_lot_limit`, `village_citizen_zoning` (private share full),
`village_citizen_not_owner`, `village_citizen_no_cash`,
`village_citizen_private_only` (the head placing a resident's kind),
`village_citizen_lot_private` (the head on a private lot), `village_citizen_rest_wait`,
`village_citizen_no_house`, `village_citizen_terms_range`, `village_citizen_no_debt`,
`village_citizen_off`, `village_citizen_no_lots`, plus the older
`village_not_resident`, `village_unbuildable`, `village_occupied`.

### 4.4 The founding form (contract 1.3)

A group's «ساخت روستا» no longer founds a village at once. It opens a short
**draft** (`settlement.founding_draft_ttl`, 30 minutes; one open draft per
group) and answers in the group with a message and one button,
«📝 تکمیل اطلاعات روستا». The button opens the game on the form; the founder
fills in the village's **name**, **motto**, **emblem** and the **national
currency** it reserves, and only submitting the form founds the village.
Nothing is founded if the draft runs out; the group may then ask again.

**The button.** `web_app` inline buttons are allowed only in private chats
(<https://core.telegram.org/bots/api#inlinekeyboardbutton>), so the group's
button is a `url` button to the bot's Main Mini App direct link
`https://t.me/<bot_username>?startapp=<param>` (Direct Link Mini Apps,
<https://core.telegram.org/bots/webapps>): it opens the Mini App from a
group for whoever presses it, with `<param>` in the launch data as
`start_param` (and `tgWebAppStartParam`). The parameter is
`found_<draft id without dashes>` (38 characters; Telegram allows
`A-Z a-z 0-9 _ -` up to 64). The bot's username comes from the bot registry,
the Mini App from BotFather (section 7, "Opening the Mini App from the bot");
no username or Mini App URL is written in code. A client that finds
`found_…` in its `start_param` signs in as usual and opens the form. Without
a start parameter (the link was lost, or the game was opened from the bot's
private chat) `settlement.found.draft` with no `draft` argument returns the
player's own open draft.

**Who.** Only the player who sent «ساخت روستا» can submit the draft.
Anybody else who opens it reads it (`state: "other"`); a second «ساخت روستا»
from another member is answered with "X is completing the details" and the
same button.

| command | args | answers |
|---|---|---|
| `settlement.found.draft` | `draft`? (id, with or without dashes) | `founding_form` |
| `settlement.found.submit` | `draft`, `name`, `motto`?, `currency_name`, `currency_code`, `currency_symbol`?, `shape`, `color_a`, `color_b`, `icon`, `check`? (`1`) | `settlement_founded`, or `founding_checked` with `check`, or a refusal |

Both are commands of a client (`client: true`). Send an `idempotency_key`
with `submit`; a repeated submit of a draft that already became a village is
answered from that village, never a second one.

`founding_form` view:

```json
{"state": "mine", "draft": "5b1c1d0e-…", "expires_at": "2026-09-30T18:30:00Z",
 "founder": "Sara", "suggested_name": "کورندال",
 "default_emblem": {"shape": "shield", "color_a": "crimson", "color_b": "gold", "icon": "wheat"},
 "limits": {"name_min": 3, "name_max": 24, "motto_max": 60, "currency_name_min": 3,
            "currency_name_max": 24, "currency_code_len": 3, "currency_symbol_max": 3},
 "shapes":  [{"code": "shield", "name": "سپر", "emoji": "🛡"}],
 "palette": [{"code": "crimson", "name": "سرخ", "emoji": "🔴", "hex": "#b3261e"}],
 "icons":   [{"code": "wheat", "name": "خوشهٔ گندم", "emoji": "🌾"}],
 "neutral_currency": "SUP"}
```

`state` is `mine` (the viewer may submit), `other` (read only), `expired` or
`founded` (then `settlement_id` and `settlement_name`). `suggested_name` is the
generated place name the form starts from. The **emblem** is not an image: it
is the four codes above, drawn by the client as an SVG (an outline in the
`shape`, filled with the two colours, the `icon` in the middle) and written by
the server as emoji in Telegram (`🛡 🌾 🔴🟡`). The catalogue is
`configs/content/founding.yml`, so it can grow without a new build. There is no
upload: no storage and no moderation are needed.

**Rules** (checked again by the server on every submit): the name and the
currency name are Persian or Latin letters, spaces, hyphens and the
zero-width non-joiner, within the limits, never a link, mention or address,
never a banned word, and a name never a reserved one; a village name is unique
among settlements (case, spaces and hyphens ignored); the currency code is
exactly `currency_code_len` capital Latin letters, unique across the codes
already reserved and the existing currencies, and never `SUP`, `NIL` or a
real-world code; the symbol is 1 to `currency_symbol_max` characters (the
code when empty); the two emblem colours differ. **The currency is only
reserved.** The village keeps using the neutral SUP until it declares a
country (docs/adr/0029, phase C4), when the reservation becomes its national
currency; the ledger is not touched.

A refused form is `200` with `ok: false`, `screen: "founding_refusal"` and
`error.code` `founding_<kind>`; `error.message` is the sentence Telegram
shows, in the player's language.

| `error.code` | meaning |
|---|---|
| `founding_no_draft` | no such draft |
| `founding_expired` | the draft's time ran out; nothing was founded |
| `founding_not_founder` | the viewer did not start the draft |
| `founding_already` | the group already has a village |
| `founding_invalid` | the form has problems; `view.problems` is `[{"field", "code"}]` |
| `founding_no_world`, `founding_unavailable` | the world or the form's content is not ready |

Problem `code`s by `field`: `name` — `name_short`, `name_long`, `name_chars`,
`name_link`, `name_forbidden`, `name_reserved`, `name_taken`; `motto` —
`motto_long`, `motto_chars`, `motto_link`, `motto_forbidden`;
`currency_name` — `currency_name_short`, `currency_name_long`,
`currency_name_chars`, `currency_name_forbidden`, `currency_name_taken`;
`currency_code` — `currency_code_format`, `currency_code_reserved`,
`currency_code_forbidden`, `currency_code_taken`; `currency_symbol` —
`currency_symbol_invalid`; `emblem` — `emblem_invalid`. With `check: "1"` the
same checks run and nothing is founded (`founding_checked`), which is how a
client validates a name or a code as the player types.

The answer to a successful submit is `settlement_founded`:
`{"name", "settlement_id", "biome_code", "nearby_feature", "buildings",
"protected_until", "founder", "emblem", "emblem_text", "motto",
"currency_name", "currency_code", "currency_symbol"}`. The group is told by
the notifier (an announcement with the name, emblem, motto and currency), and
the client moves to the new village, whose id is `settlement_id`. The
`settlement` of the bootstrap then carries `emblem` (`shape`, `color_a`,
`color_b`, `icon`), `motto` and `currency` (`code`, `name`, `symbol`) too.

## 5. Realtime

Centrifugo v6 (<https://centrifugal.dev/docs>), WebSocket endpoint
`/connection/websocket` (JSON protocol; any official Centrifugo client SDK).

### 5.1 Connecting — `GET /api/v1/realtime/token`

```json
{"token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9…", "expires_at": "2026-09-26T10:15:00Z",
 "user": "8a4e…", "channels": ["player:8a4e…", "settlement:3c1f…"]}
```

A Centrifugo **connection token** (HS256; claims `sub` = player id, `exp`
= `client.realtime_token_ttl` (15m), `iat`, `channels`). The `channels` claim
subscribes the connection **server-side** to the player's personal channel
`player:<player_id>` and to the channel of every settlement the player lives
in or is standing in (`settlement:<id>`, section 5.4; usually one, two while
visiting): the client does not (and cannot) subscribe to them itself. The
list is computed by the server when the token is issued, so fetch a new
token after moving house or travelling (or when the SDK asks for one, its
`getToken` callback).

### 5.2 A city channel — `GET /api/v1/realtime/subscribe?channel=city:<code>`

```json
{"token": "eyJhbGciOi…", "expires_at": "2026-09-26T10:15:00Z", "channel": "city:support"}
```

A **subscription token** (claims `sub`, `channel`, `exp`, `iat`) for the
public channel of the city the player is **currently in**, or for the
`settlement:<id>` channel of a settlement the player lives in or stands in;
any other channel is `403 forbidden_channel`. Re-subscribe after travelling.

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


### 5.4 A settlement's channel — `settlement:<settlement_id>` (contract 1.2)

Full detail of what happens in one village (a settlement is a `cities` row;
its id is the one the layout and player-list endpoints take). Every
publication is small typed JSON with the same envelope:

```json
{"type": "build_finished", "settlement_id": "3c1f…", "seq": 1790724836123, "at": "2026-09-30T10:00:00Z",
 "building_id": "…", "type_code": "watch_hut",
 "layout_version": {"head": "9f2c…", "member": "51ab…", "public": "c07e…"}}
```

Two numbers say whether a client's picture is current, for two different
pictures:

* **`seq`** orders the channel: it grows by one for every publication of that
  settlement (one counter per settlement, in Redis, decided once per event so
  a redelivery carries the same number). A client keeps the last `seq` it
  applied: **a `seq` that is not the last plus one, or that is not higher,
  means it missed something — fetch the layout and the player list again**
  instead of trusting its picture. Publications may arrive out of order
  (replicas race), which is the same signal. `GET /settlements/{id}/players`
  carries the `seq` it is current to; ignore publications at or below it.
* **`layout_version`** is on every publication that changes the layout (a
  building placed, finished, cancelled or pulled down): the `version` that
  `GET /settlements/{id}/layout` will report once the change has committed,
  for each kind of viewer — `head` (may place: `viewer.can_place`), `member`
  (any other member) and `public` (everyone else). Compare the one that is
  yours with the `version` of the layout you hold: equal means you are already
  current, anything else means fetch it again. (The layout's `version` is a
  hash of the picture, so it says whether a picture is current but cannot
  count what was missed; that is what `seq` is for. It does not move on
  construction progress: a client counts that down from `started_at` and
  `finish_at`.)

| `type` | fields | when |
|---|---|---|
| `build_started` | `building_id`, `type_code`, `lot_x`, `lot_y`, `rotated`, `finish_at`, `layout_version` | the head, or (1.4) a resident on their own lot, placed a building and paid for it |
| `lot_bought` | `lot_x`, `lot_y`, `layout_version` | a resident bought a lot (1.4); the versions include who owns what, so refetch the layout when yours differs |
| `build_finished` | `building_id`, `type_code`, `layout_version` | construction reached its end |
| `build_cancelled` | `building_id`, `type_code`, `layout_version` | the head called off a building still going up |
| `build_salvaged` | `building_id`, `type_code`, `layout_version` | a building was pulled down and its scrap credited |
| `relocated` | (none) | an operator moved the village to a better site (its terrain and the founding kit's lots changed): fetch the layout again. Carries no `layout_version`; `grid.origin` in the new layout is where the grid now stands |
| `research_started` | `research_id`, `code`, `finish_at` | the village began researching |
| `research_finished` | `code` | the research ended; the village knows the item |
| `knowledge_bought` | `code` | the village bought an item from Support |
| `literacy_changed` | `literacy_share_bps` | a teaching step finished and literacy moved |
| `head_changed` | `office`, `vacated`, `layout_stale`, `player_id`?, `player_name`? | the head office was filled or vacated (appointment, dismissal, election); `layout_stale` says who may place changed, so fetch the layout |
| `member_joined` | `player_id`, `player_name`?, `via` (`travel` \| `residence`) | someone arrived or moved in |
| `member_left` | `player_id`, `player_name`?, `via` | someone left or moved out |

There is no `build_progress`: construction has no intermediate state to
report (see `started_at`/`finish_at` above). History is kept like the other
namespaces (20 publications, 5 minutes, recovered on reconnect); beyond that,
the `seq` gap rule is the recovery.

### 5.5 Presence (contract 1.2)

A player is **online** while a Redis key written by any signed-in call (or the
heartbeat) is alive: `realtime.presence_ttl` (30s) after the last one. Nothing
is written when a player goes away. What a player is doing is always derived by
the server from their open timed action, never reported by the client:
`idle`, `travelling`, `working`, `studying`, `training`, `hospital`, `jail`,
`building`, `fighting` (`activity_label` is the localized words).

Each player chooses who may see them — `everyone` (default), `contacts`
(accepted friends and faction mates) or `nobody` — in the bot's private
settings screen (`player.presence.set`); as in Telegram, choosing `nobody`
also hides everyone else's presence from you. Their own settlement always
sees them (an open question for the owner, ADR 0030 section 8).

**`POST /api/v1/realtime/heartbeat`** — `{"ok": true, "ttl_seconds": 30}`.
Call it every ~`ttl_seconds/2` while the app is open but idle; any other
authenticated call already counts.

**`GET /api/v1/players/{id}/status`** — `{id}` is the player's id.

```json
{"id": "…", "name": "Sara", "code": "K7Q2M9A", "visible": true, "online": true,
 "activity": "working", "activity_label": "در حال کار", "place": "city_centre"}
```

Shaped by the viewer's relation: same settlement — everything (`place` too);
accepted friend or faction mate elsewhere — status and activity, **no
place**; same country only — `online` alone, and only if the player chose
`everyone`; anyone else — `"visible": false` and nothing more (which is not
the same as offline). A field the rules withhold is absent.

**`GET /api/v1/settlements/{id}/players`** — the settlement's residents and
whoever is standing in it, for members only (`403 not_in_settlement`
otherwise):

```json
{"settlement_id": "3c1f…", "seq": 1790724836123, "online": 2, "hidden": false,
 "players": [{"id": "…", "name": "Sara", "code": "K7Q2M9A", "visible": true, "online": true,
              "activity": "idle", "activity_label": "آنلاین", "place": "city_centre"}]}
```

`hidden: true` means the caller chose `nobody`, so no row carries presence.
Keep it live with the `member_*` publications of section 5.4.

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
| 403 | `forbidden_channel` | not the player's city channel, or a settlement they are not in |
| 400 | `bad_chunk` | (1.1) the chunk address names no chunk of this world |
| 403 | `not_in_settlement` | the settlement's player list is for its residents and whoever stands there |
| 404 | `not_found` | |
| 404 | `world_not_created` | (1.1) no world has been created yet (`admin world create`) |
| 404 | `no_settlement` | (1.1) the village endpoints are not configured on this server |
| 200 | `founding_*` | (1.3) a refused founding form, `ok: false` with `screen: "founding_refusal"`; see 4.4 |
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

**Migrations**: `0051_founding_form` (1.3: founding drafts, the emblem, motto and
name key of a founded village, and the currency a village reserves);
`0033_client_devices` (tables `client_devices`,
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
