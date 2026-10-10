# Game client API — v1

**Contract version 1.4.** Every 1.x is compatible with 1.0: a client written
**Contract version 1.5.** Every 1.x is compatible with 1.0: a client written
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
`currency` on the bootstrap `settlement`. 1.4 adds travel to villages
(section 4.5): `location` on the bootstrap, villages among the destinations of
`map.cities` (with `village`, `emblem`, `settlement_id`, `lat`, `lon`, `fare`,
`wait_seconds`), and the `walk` and `cart` transport modes. 1.5 adds the
building panel, batch placement, automatic roads and land (section 4.3,
"Building panels, batches, roads and land"): `settlement.building.view`,
`settlement.build.place_many`, the `village_batch` and
`village_no_road` error codes, the realtime publication
`build_batch_started`, `auto_roads` on `build_started`, and a
`grid.lots` that may be larger than the tier's base side. (The paid land
expansion `settlement.grid.grow`, `village_grid_max` and `grid_grown` of 1.5 were
removed: land now opens by roads, see "Roads that open land" below.) The content
catalogue's `settlement_building` entries gain `cap_exempt` (roads), which tells
a client which buildings it may lay many at a time. Nothing that 1.0,
1.1, 1.2, 1.3 or 1.4 returned has changed. 1.6 adds state sync (section
5.6): `GET /state`, `GET /updates?since=`, the `updates` and
`updates_too_long` publications on the player's channel, `updates` on a
command's answer, `features.updates` on the bootstrap, `updates` on the
realtime token, and the error code `state_sync_off`. Nothing that 1.0 to 1.5
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
5. [Realtime](#5-realtime) (5.6: state sync, the snapshot, the update log and the push)
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
| `updates` | (1.6) `{"pts", "records"}`: the state sync records this command caused, when ready within `state_sync.command_wait` (300 ms); see 5.6 |

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
| `inventory.show` | `page`? | `inventory`: `lines` (each with `shelf {code, group, label, group_label}`), `bags` (the two slots, `belt` then `back`: `{slot, bag}`, `bag` is `{item, serial, full_space, space, wear, wear_max, torn, comfort_kg, hard_kg}` or null) and `carry` (`{used, capacity, base, load_g, comfort_g, hard_g}`: space in «جا», load in grams) |
| `inventory.bag.wear` | `item` (a bag piece's serial) | `item_detail` of the bag (`bag.worn` true) |
| `inventory.bag.off` | `slot` (`belt` or `back`) | `inventory` |
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

**`cities`** (`map.cities`): `{destinations: [{code, name, distance_km, village, settlement_id, emblem, lat, lon, fare, wait_seconds}], page, pages, origin_code, origin, travelling, travelling_to_code, travelling_to}`.

**`travel_options`** (`travel.options`): `{from_code, from, to_code, to, cash, requoted, options: [{mode_code, mode_name, fare, wait_seconds, energy, busy, vehicle: {code, name} | null, condition}]}`.
Start a journey with `travel.start {city: to_code, mode: mode_code, max: fare}`.

`fare` and `wait_seconds` (the cheapest fare, the fastest real wait) are set for a destination the world derived (section 4.5) and `0` for one a content route reaches, which is priced when its mode is chosen.

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

### 3.2 The life area's neutral screens (presentation split)

The screens of the player's own place, journeys, life, devices, bag and property carry a typed `view` and `actions` by meaning
(`id`, `command`, named `args`, `kind`, `subject`) and no `text`, `label` or `row`: `profile`, `dashboard`, `city_map`, `cities`, `travel_*`,
`walk_started`, `not_here`, `life`, `card`, `history`, `avatars`, `sleep_pay`, `settings`, `devices`, `device_link`, `achievements`, `inventory`,
`item_*`, `drop_confirm`, `property*`, `refusal` and `error`. The generated types are `api/views.gen.ts`. A refused request is `ok: false` with
`error.code` (`life_<kind>`, `item_<kind>`, `property_<kind>`, `refusal_<kind>`) and its data in `error.args` (`wait_seconds`, `max`, `min_chars`...);
the `error` screen is an `ok: true` screen whose view says why (`code`, `args`). An action that asks for a value (`property.sell`, `property.let`,
`life.bio`) carries `input {field, text}`. The village overview's `support.services` lists the services of the central city the village lacks
(`bank`, `market`, `knowledge`, `hospital`). The content catalogue adds the tables `property_type`, `achievement`, `shop`, `avatar`, `career` and
`career_tier` (code `<career>.<rank>`).

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
  "location": {"kind": "city", "code": "support", "name": "ساپورت", "home": true,
               "centre": {"lat": 32.3, "lon": -47.7}},
  "settlement": {
    "id": "4d1c…", "code": "v-k3x9", "name": "آمل", "tier": "village",
    "world_cell": 18211, "centre": {"lat": 36.4, "lon": 52.3, "chunk": {"face": 4, "lod": 10, "x": 523, "y": 512}},
    "is_head": true, "resident": false, "grid_lots": 5,
    "layout_path": "/api/v1/settlements/4d1c…/layout"
  }
}
```

Names are in the player's language. `places` are the places of the player's
current city. `realtime` says whether section 5 is available.
`features` (1.6) says which optional parts are served: `features.updates`
is state sync (5.6); a client that sees it keeps a store instead of polling. `settlement`
(1.1) is the player's own settlement and is absent when they belong to none;
see 4.3.

#### Where you stand — `bootstrap.location` (1.4)

The place the player stands in **now**, which is not always their own
settlement (`bootstrap.settlement`): a traveller stands in another group's
village, or in Support. Absent for a player who is nowhere.

| field | meaning |
|---|---|
| `kind` | `city` (a content city: Support) or `settlement` (a founded village) |
| `code`, `name` | the city's or village's code and its name in the player's language |
| `centre` | `{lat, lon}` (a village's also carries `chunk`): where it stands on the world |
| `home` | the player lives here (their residence) |
| `settlement_id`, `tier`, `world_cell`, `grid_lots`, `layout_path`, `emblem`, `motto`, `currency` | for `kind: "settlement"` only, as on `bootstrap.settlement`. Draw the place with `GET /settlements/{id}/layout` (a stranger gets `detail: "coarse"`, section 4.3) |

Re-read the bootstrap (or `GET /players/{id}/status`) after `travel.completed`
to learn the new place; `player.city_code` follows it.

---

### 4.5 Travel to Support and to villages (1.4)

Every founded village and Support are destinations for everyone: a traveller is
not a settler (residence is `settlement.join`), and beginner protection does
not close a village's door. No route is authored between them; the game derives
each journey from where the two stand on the world:

```
distance  = great-circle km between the two spots (haversine, planet radius 6371 km, rounded up, at least 1)
per mode  = boarding + distance / speed    (game time; the real wait is that / 60, the game clock)
fare      = base_fare + fare_per_km x distance     (private modes, paid to the system sink)
modes     = the modes config `travel.world_reach` lists whose reach covers the distance
```

Today: `walk` (5 km/h, free, up to 60 km), `cart` (20 km/h, 20 + 2/km, up to
500 km), `car` (90 km/h, 5/km, up to 21 000 km). A place no listed mode reaches
is not a destination.

The flow is the ordinary one: `map.cities` (Support first, then villages,
nearest first, paged; `village: true` marks a village) -> `travel.options {city:
<code>}` -> `travel.start {city, mode, max, method}` -> the scheduler lands the
player with `travel.arrive`; `players.city_id` (the place) moves, the
residence does not. A `member_joined` (`via: "travel"`) reaches the destination
village's settlement channel and a `member_left` the origin's (founded villages
only; Support has no settlement channel).

---

### 4.1 Content catalogue — `GET /api/v1/content?since=<version>`

Every content entry a client may have to draw, by table, with its name in
every language and the asset keys its art is looked up by
(`internal/clientapi/catalogue.go`). Tables: `city`, `place`,
`company_type`, `item`, `component`, `mode`, `crime`, `course`, `skill`,
`technology`, `military_unit`; and, for what a notice names (ADR 0039 section
8), `achievement`, `mission`, `property_type`, `treaty_type`, `loan_product`,
`insurance_product` and `office`; for the companies, production and recruitment
screens, `supplier`, `design_slot`, `attribute`, `building_role` (what an
availability condition names: a craft building of tier 3) and `specialist_name`
(two entries, `first` and `last`, each a list separated by `|`: a specialist is
named `first[seed % n] last[(seed / n) % m]` from the view's `name_seed`), and
the availability tags of every `company_type` (the stage a kind of business
starts at, what it needs). `asset.icon` is always `<table>:<code>`;
`asset.model` is set for tables drawn as buildings or vehicles (`place`,
`company_type`, `mode`, `military_unit`). Items and components also carry a `shelf` (ADR 0046: the leaf of the item tree,
`food.grain`, `bags.back`), named by the tables `item_shelf` (a leaf; its
`category` is its group) and `item_shelf_group` (a top-level group): a market
filters and groups by it. A `settlement_building` carries a `build_category`
(table `build_category`: housing, shops, construction, production, farming,
public, security, other), the group of the build menu; the menu's lines
(`settlement_build_menu`) carry the same code as `category`.
Items, components, crimes, skills
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

#### Building panels, batches, roads and land (contract 1.5)

**The panel.** `settlement.building.view {building_id, mode?}` answers screen
`settlement_building_view`; any member may ask, only the head (`can_manage`)
gets the actions. Every building shows what it IS; demolishing is a small,
last, confirmed action, never the panel's face. `view`:

| field | meaning |
|---|---|
| `id`, `building {code,name}`, `role`, `tier`, `x`, `y`, `w`, `h`, `rotated`, `description` | the building; `description` is what it is and does, in the player's language |
| `kind` | which panel to draw: `road`, `civic_hall`, `storage`, `school`, `security`, `shop`, `generic` (draw an unknown kind as `generic`) |
| `state` | `complete` or `building` |
| `effects` | `[{target, value}]` what the building adds (basis points, except `housing_capacity`); `upkeep` |
| `started_at`, `finish_at`, `left_seconds`, `progress_percent` | under construction (0..100, already computed) |
| `stock` | `storage`: `[{item {code,name}, kind: "component"|"item", qty}]` the village store; the store has no capacity in the content yet, so none is sent |
| `literacy_percent`, `teaching` | `school`: teaching runs by itself while an education building stands, so it is a state, not a switch |
| `treasury`, `population`, `research` | `civic_hall`: the village numbers and the research running now |
| `shop` | `shop` (ADR 0046): the same view as the `village_shop` screen (below), so the building's panel draws the shelf without another page |
| `can_manage`, `has_upgrade`, `mode` | management; `has_upgrade` tells whether the "upgrade" action is worth showing |
| `upgrades` | only with `mode: "up"`: `[{building, tier, cost_money, build_time_seconds, available, missing}]`, the **next tier of the building's role** from the content's own ladder (a tier-2 building requires a tier-1 one of its role). It is revealed only when pressed. Levels *within* one building are a later phase and nothing here blocks them |

`mode`: `up` reveals the upgrade, `dm` and `cx` are the confirmation screens
of demolishing a finished building and calling off one going up (the
confirming press is `settlement.build.demolish` / `settlement.build.cancel`).

**Batch placement.** `settlement.build.place_many {code, lots | from+to,
confirm?}` starts them all in one command and one transaction: the same
validation as `place` for every lot (bounds, water, occupied, terrain,
knowledge, literacy), judged against a grid that already counts the batch's
own earlier lots; one total spend; one `build_batch_started` publication.
Without `confirm` it answers `settlement_build_batch_confirm` (`count`, `lots`,
`cost_money`, `materials`, `build_time_seconds`). Any bad lot refuses the WHOLE batch
(`village_batch`, every offending lot named). **The cap rule:** the
concurrent-construction cap (village 1, town 2, city 4) is about big builds;
the content marks the road `cap_exempt`, so a road never waits for a slot and
never holds one, alone or in a batch. Only cap-exempt one-lot types can be
batched (anything else answers `village_not_available`).

**Automatic roads.** `place` also lays the road that connects the building:
the cheapest run of free buildable lots (Dijkstra over the four neighbours;
open ground costs more than ground beside buildings, the edge or water, and a
turn costs extra, so streets run along the edges of what stands and straight)
from a lot beside the building to a lot beside the network (any road, or the
civic hall). The roads are finished at once, cost `settlement.auto_road_cost`
per lot (10), and ride in the same `build_started` (`auto_roads`). A building
that already touches the network gets none; one no road could reach is refused
(`village_no_road`) before anything is paid. `settlement_build_confirm` carries
`auto_roads` (the number of lots) and the cost already includes them. The
founding kit already puts a road against the hall, so founding needs nothing.

**Land: roads that open it (ADR 0044 5.5, owner decision 2026-10-03).** The grid
a village is founded with (`grid.lots`, 5 a side; a village that once bought
expansions keeps the bigger grid) is only its FIRST BLOCK of land. The paid
expansion is gone. The holder of the top office draws a road out of the grid and
the lots along it open for sale:

- `settlement.road.plan` `{x, y}` (or `to`: a lot token, `from`?, `class`?
  (`path` by default), `confirm`?). Without `confirm` it answers
  `settlement_road_quote` and changes nothing; with it, `settlement_road_planned`
  (a PLAN is stored: free, idempotent). The view carries `from`, `to`, `class`,
  `options` (every road class with `available` and the research it `missing`),
  `lots`, `length_m`, `climb_m`, `max_grade_bps`, `crossings`, `lot_cost`,
  `crossing_cost`, `full_cost` (the price of building all of it, paid by buyers
  piece by piece), `opens`/`usable`/`water`/`steep`, `path` and `open_cells`.
  Refusals: `village_road_no_network`, `_end_blocked`, `_water`, `_no_route`,
  `_no_bridge`, `_too_long`, `_foreign`, `_open_cap`, `_same`, `_class_locked`.
- `settlement.road.cancel` `{id}` takes a plan back while nothing is laid and no
  lot along it is sold or built on (`village_road_in_use` otherwise).
- Lot coordinates are **absolute from the first grid's south-west corner and may
  be negative** (land west and south of it). A lot token writes a negative
  coordinate as `m` and its size: `m3-7`, `5-m2`. `x`/`y` numbers are accepted
  everywhere a token is.
- The layout (members) gains `land: {plans, cells, open}`: `cells` are the lots of
  drawn roads (`built` once a purchase laid them), `open` the lots the roads
  opened with their ground (`buildable`, `reason` `water` or `steep`, `height_m`,
  `slope_m`, `biome`, `tags`). A laid road cell is also in `buildings` and
  `roads` like any road. `settlement.land` gains `outer` (the same lots with
  their state and `access`/`cost`), `roads` (each drawn road with how many lots
  are built, for sale and sold) and `can_draw`; `settlement.build.lots` and
  `settlement.private.lots` gain `outer` (cells with `fits`).
- `settlement.lot.buy` on an opened lot charges the lot price to the treasury
  and, in the same purchase, the lane to the road and the unlaid stretch of the
  drawn road up to it (reason `settlement_lot_road`, one journal row); the cells
  are laid once, so the next buyer pays only what is still unlaid.
- The publication `land_changed` (`kind`: `road_planned` or `road_cancelled`,
  `layout_version`) replaces `grid_grown`: fetch the layout again.

#### Your settlement — `bootstrap.settlement`

The settlement the player heads (`is_head`) or lives in (`resident`), with
`centre` (latitude/longitude of the middle of its lot grid, and the base-LOD
chunk holding it) and the side of the grid, `grid_lots` (5 for a village, 9
for a town, 15 for a city, plus the expansions the settlement bought). Only the head may place, cancel or demolish
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
| `settlement.build.place_many` | `code`, `lots` (`[{x, y}]`, or tokens `"3-1"`), or `from`/`to` (two ends of a line, along the row and then down the column), `confirm`? | lays several one-lot buildings **of a cap-exempt type (roads)** in ONE command; see "Building panels, batches, roads and land" |
| `settlement.building.view` | `building_id`, `mode`? (`up` \| `dm` \| `cx`) | one placed building's own panel (`settlement_building_view`) |
| `settlement.road.plan` | `x`, `y` (or `to`), `from`?, `class`?, `confirm`? | draws a road out of the first grid; without `confirm`: `settlement_road_quote`; with it: `settlement_road_planned`; see "Land: roads that open it" |
| `settlement.charter.view` | - | the charter: offices, holders, what the viewer may do, the permission catalogue, the audit (`village_charter`); the founder's office is `id: "founder"` until the first write |
| `settlement.charter.office.save` | `office`? (empty creates; `"founder"` or an id edits), `title`, `seats`?, `grants` (`[{permission, limit?}]`), `acquisition`?, `term_days`? | create or edit an office (`village_charter_changed`); numbers may be sent as numbers or strings |
| `settlement.charter.office.close` / `.appoint` / `.dismiss` / `.resign` | `office`, `player`? (public code) | close an office, seat or unseat a resident, leave an office |
| `settlement.timezone.set` | `offset_minutes` (multiple of 15, -720..840) | the charter changes the settlement's own time zone (permission `settings.timezone`, once per `settlement.timezone_cooldown`); answers `village_charter_changed` |
| `settlement.charter.election.open` | `office` (`"founder"` for the head seat) | call an election for an elected office (or the vacant head seat); permission `election.call` |
| `settlement.charter.stand` | `ballot` | put the player's name on an election during candidacy |
| `settlement.charter.vote` | `ballot`, `choice` (a candidate's public code, or `yes`/`no`) | one secret, final vote |
| `settlement.charter.recall.start` | `office`, `player` (public code) | open a recall petition about a holder after their first days |
| `settlement.charter.recall.sign` | `petition` | sign a petition; the signature that reaches the threshold opens the vote |
| `settlement.road.cancel` | `id` | takes an unlaid, unsold road plan back (`settlement_road_cancelled`) |
| `settlement.build.cancel` | `id` | calls off a building **under construction**; the spend is forfeited, the lot is free again |
| `settlement.build.demolish` | `id` | removes a **finished** building; part of its cost returns to the treasury |
| `settlement.build.progress` | — | what is going up |
| `settlement.materials` | — | `village_materials`: the village stock (`stock`, `used`, `capacity`) and Support's market (`market`: item, unit price); `can_buy` says whether the viewer may spend the treasury |
| `settlement.shop` | — | `village_shop` (ADR 0046): the village's shop. `closed` is `""` (open), `no_shopkeeper`, `unpaid` or `not_yet`; `next_delivery`, `delivery_hour`, `wage`, `tax_bps` (+ `tax_max_bps`, `tax_presets`), `price_cap_bps` (+ `cap_min_bps`, `cap_max_bps`, `cap_presets`), `can_set_cap` (the head), `presets` (buy quantities), `resident` (may buy), `building` (the shop building stands), `lines` (`item {code,name}`, `kind` `item`/`component`, `shelf`, `price`, `reference`, `stock`, `left_today`, `fits`, `max_buy`, `tradable`), `locked` (what a building or research would add: `needs_buildings`, `needs_knowledge`), `free_space`, `capacity`, `free_g`, `repairs` (`serial`, `slot`, `wear`, `wear_max`, `torn`, `cost`), `can_repair`, `cash`; right after an act `bought` or `mended` |
| `settlement.shop.buy` | `item`, `qty`, `method`?, `nonce`? | without `method`: `village_shop_checkout` (`unit`, `total`, `tax`, `tax_bps`, `space`, `free_space`, `grams`, `payment`, `nonce`), nothing changes. With `method` (`cash`/`card`) and the `nonce`: pays, delivers, answers `village_shop` with `bought`. Refusals are `village_shop_refusal` with code `village_shop_<kind>`: `closed`, `not_there`, `sold_out`, `player_cap`, `no_space`, `too_heavy`, `not_here` |
| `settlement.shop.cap` / `settlement.shop.tax` | `bps` | the head sets the shop's price ceiling (10000..15000) / sales tax; answers `village_shop`; `village_shop_cap_range` outside the bounds |
| `settlement.shop.repair` | `item` (a bag piece's serial) | the shop building's counter mends a bag; answers `village_shop` with `mended`; `village_shop_no_building` without one |
| `settlement.money` | — | `village_money` (ADR 0046 section 7, a display): `currency`, `market`/`reserve` (`none` while the settlement currency has no book or reserve), `nil_unit_sup`, `nil_per_unit_micro` (millionths of a Nil per unit of the neutral money), `examples`, `treasury` (+ `treasury_nil_micro`), `output` (+ `output_nil_micro`, `output_days`), `basket` and its `index_bps`/`cover_bps`. Nothing moves, nothing converts |
| `settlement.materials.buy` | `item`, `qty`, `confirm`? | the head buys a material from Support's market with SUP from the treasury. Without `confirm`: `village_materials_buy_confirm` (unit price, total), nothing changes. With `confirm: "confirm"`: pays, puts the goods in the stock and answers `village_materials` with `bought` |
| `settlement.work` | `id`? | without `id`: `village_work`, the workplaces (`places`: `id`, what one shift `produces` and `consumes`, `wage`, `shift`, `workers`, `busy`, `ready`) and the viewer's own shift (`mine`). With `id` (a workplace's building id): a resident starts a timed shift there and the answer is `village_work_started` |
| `settlement.labor.board` | — | `labor_board`, the hiring board: `jobs` (`id`, `building_id`, `building`, `kind` `construction`/`production`, `employer_kind` `settlement`/`player`, `employer`, `wage` per shift, `left` shifts the budget still pays, `progress_bps`, `left_minutes` worker-minutes of work, `workers`, `npc_crew`, `can_take`, `mine`, `points` the work one shift of the viewer adds), `market` (`housing`, `pool`, `available`, `working`, `vacancies`, `tightness_bps`, `level` `slack`/`balanced`/`tight`/`short`, `npc_wage`, `min_wage`), `working` (the viewer's shift in progress), `sites` (buildings under construction with no open job the viewer may post one for), `resident` |
| `settlement.labor.site` | `id` (a building id) | `labor_site`, the panel of a construction site: `status`, `progress_bps`, `required_minutes`, `done_minutes`, `left_minutes`, `job`, `workers` (`worker`, `worker_npc`, `level`, `finish_at`, `left`, `points`), `market`, `can_work` with `work_wage` and `work_points`, `working`, and for the employer `can_employ`, `hire_presets` (crew sizes), `wage_presets` (`percent`, `wage`), `npc_available`, `npc_wage`; `can_post` when the site has no job and the viewer may post one; `just` names what the last press did |
| `settlement.labor.take` | `id` (a job id) | a player takes the job and works one timed shift; answers `labor_site` with `just: "worked"`. When the shift ends the work is added to the building and the employer's wage is paid. A player of any skill may take it; players and NPC labourers compete for the same work |
| `settlement.labor.hire` | `id` (a job id), `n` | the employer sets the NPC crew (0 sends them home when their shifts end) and the free labourers start at once, paid the market wage a shift at a time; the crew is kept up until the work is done |
| `settlement.labor.wage` | `id`, `n` (percent of the market wage) | the employer sets the wage a player gets per shift; never under the village's minimum wage |
| `settlement.labor.close` | `id` | the employer takes the job off the board; shifts in progress still end and are paid |
| `settlement.labor.post` | `id` (a building id) | the employer posts the job of a building under construction that has none, or of a standing workplace |
| `settlement.labor.mine` | — | `labor_mine`, the viewer's own status: `level` (`apprentice`/`journeyman`/`master`), `productivity_bps`, `shifts`, `earned`, `next_level`, `next_shifts`, `working`, `market` |
| `settlement.overview` | — | the village status |
| `settlement.knowledge` | — | the knowledge list |
| `settlement.knowledge.research` / `.buy` | `code` | starts a research / buys the item from Support |
| `settlement.join` | `settlement` (the village id), `confirm`? | makes the village the player's **home** (a player has one). Without `confirm`: `village_residence_confirm`, nothing changes. With `confirm: "confirm"`: moves the home and answers `village_residence_done`; the realtime token then carries `settlement:<id>` (fetch a new one) and the roster gains the player |
| `settlement.leave` | `settlement`? , `confirm`? | sends the player home to the neutral city, same two steps |
| `settlement.promotion.view` | — | the way forward: the goals of the **next** tier only (`village_promotion`) |
| `settlement.promote` | `confirm`? | the head takes the settlement one tier up. Unmet goals answer `village_promotion` (nothing changes); met, without `confirm`: `village_promote_confirm`; with `confirm: "confirm"`: `village_promoted` |

**Tier promotion (contract 1.4, additive).** A settlement grows village → town →
city by development, not by land. `settlement.overview` carries `promotion`
(absent at the top of the ladder): `village`, `from`, `to`, `met`, `can_promote`
(the viewer holds the head office), `office` (the head office after the step)
and `criteria`, each `{kind, role?, current, required, met}`. `kind` is
`residents` (people), `literacy` (basis points), `buildings` (finished, roads
not counted), `role` (`role` names the service, `current`/`required` are the
building tier standing/needed), `knowledge` (things the village researched or
bought) or `treasury` (minor units). A village is only ever shown the step to
the town; the city step appears once it is a town. When `met` and
`can_promote`, `settlement.promote` is the button. Promotion is free,
instant and permanent; the sitting head succeeds into the new head office
(`village_head` → `town_head` → `mayor`), and the settlement's `tier` in
bootstrap changes. Fetch a new bootstrap/layout after the realtime
`promoted` publication.

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
| `village_batch` | (1.5) a batch was refused as a whole; `view.lots` is `[{x, y, kind}]`, every offending lot with its own kind (`occupied`, `unbuildable`, `out_of_bounds`, `terrain`, `prerequisite`, `literacy`, `concurrent_cap`, `not_available`, `not_found`); nothing was paid or built |
| `village_no_road` | (1.5) the building could never be reached by road: no free buildable ground beside it leads to the network |
| `village_road_no_route` and the other `village_road_*` | (1.6) a road plan was refused; see "Land: roads that open it" |
| `village_not_found` | unknown building type, id or malformed lot |
| `village_not_demolishable` / `village_not_cancellable` | wrong state for the action |
| `village_busy`, `village_already_owned`, `village_not_available` | research / purchase refusals |
| `village_already_resident` / `village_not_resident` | join / leave when the home is / is not this village |
| `village_residence_cooldown` | the home moved too recently (the message names the wait) |
| `village_holds_office` | the head cannot leave the village |
| `village_no_home` | the city to return to is not configured |
| `village_storage_full` | (additive) the village stock has no room; a granary adds room |
| `village_already_working` / `village_workplace_full` / `village_not_workplace` | (additive) shift refusals: the resident already works, every place is taken, the building cannot be worked in |

**The prerequisite path (additive).** A `village_materials` or
`village_prerequisite` refusal of a build, a research or a shift also carries
what is missing and where it comes from, in `view`:

```json
{"kind": "materials", "back": "settlement:build", "action": "build",
 "subject": {"code": "housing_block", "name": "بلوک مسکونی"},
 "needs": [{"kind": "material", "item": {"code": "timber", "name": "Timber"},
            "have": 2, "need": 10, "price": 18,
            "makers": [{"building": {"code": "woodcutter_camp", "name": "…"}, "built": false}]}]}
```

`needs[].kind` is `material` (`have`, `need`, the `makers` that produce it —
standing ones first — and Support's unit `price`, absent when the village
cannot buy it), `knowledge` (`item`, or `options`: the items that provide a
capability) or `building` (`options`: the buildings of the role a promotion
needs). Only what the village is already offered is named. The `actions` of the
refusal carry the buttons to the sources (`settlement.build.lots`,
`settlement.work`, `settlement.materials.buy`, `settlement.knowledge`).

**Construction is done by workers (additive, migration 0059).** A building
placed while `labor.*` is configured carries the work it needs
(`work_required`, worker-minutes: its build time times `labor.reference_crew`)
and is finished only by the shifts of workers: the `settlement.build.place`
answer and the layout give the building no `finish_at`, `settlement.build.progress`
lines carry `by_work`, `progress_bps` and `left_minutes` instead of a finish time,
and the `settlement.built` event is sent when the last shift ends. A game client
that drew a timer from `finish_at` shows `progress_bps` from
`settlement.labor.site` instead. Skill is productivity (apprentice 70 %,
journeyman 100 %, master 130 % of a shift); an NPC labourer works at 85 %.
The NPC labour pool follows housing (more homes, more workers) and an NPC
labourer's wage rises with scarcity and falls with surplus, never below the
village's minimum wage.

**What is listed (additive).** The build menu, `settlement.build.lots` and
`settlement.build.place` follow the settlement's tier: a village lists only
tier 1 buildings whose knowledge it holds (a bank, port or airport is a
city's, barracks and police posts a town's) and refuses the others with
`village_not_available`. A building still lacking a standing building of a role
is listed as locked (`missing_buildings`); one whose materials the stock lacks
carries `short`.

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
 "shapes":  [{"code": "shield"}],
 "palette": [{"code": "crimson", "hex": "#b3261e"}],
 "icons":   [{"code": "wheat"}],
 "neutral_currency": "SUP"}
```

`state` is `mine` (the viewer may submit), `other` (read only), `expired` or
`founded` (then `settlement_id` and `settlement_name`). `suggested_name` is the
generated place name the form starts from; `founder` is empty when the player
has no name worth showing. The choices are **codes**: the server sends no
name, no emoji and no sentence, and each client words a shape, a colour and an
icon from its own table. The **emblem** is not an image: it is the four codes
above, drawn by the client as an SVG (an outline in the `shape`, filled with
the two colours, the `icon` in the middle); Telegram writes it as emoji from
its own locale layer. The catalogue is
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
`error.code` `founding_<kind>` (`error.args.name` is the village a group
already has, for `founding_already`); there is no `error.message`, the client
words the code. A founding command refused in a group answers
`settlement_refusal` with `error.code` `settlement_group_only`,
`settlement_no_world` or `settlement_already`, the same way.

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
"protected_until", "founder", "emblem", "motto",
"currency_name", "currency_code", "currency_symbol"}` (the emblem is its four
codes). The group is told by the notifier (an announcement with the name,
emblem, motto and currency, carried as data to every edge), and
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
visiting): the client does not (and cannot) subscribe to them itself. `updates: true` (1.6) says the player's channel carries
state sync publications (5.6). The
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
{"type": "notice", "kind": "bank.payment_received", "screen": "payment_notice",
 "view": {"payer_name": "Ada", "payer_code": "B3C4D5F", "method": "card", "amount": 12500},
 "actions": [{"id": "notice.bank", "command": "bank.show"}, {"id": "notice.profile", "command": "player.profile.get"}]}
```

A notice is **data** (contract 1.5, ADR 0039 section 8): `kind` is the game
event (`domain.event`); `screen` is the notice's code (`payment_notice`,
`market_filled_notice`, `hunger_notice`, … the notices area, one per kind of
notice) and `view` its facts, which the client words with its own table, and
from the code picks its colour and icon; `actions` are where the player may go
next, the arguments named as the command takes them. There is **no `text`**: the
Telegram wording of earlier builds is gone from this channel. A notice whose
screen is not carried as data yet has only `kind` (and a `view` when its screen
has one); a client shows a generic line for it and opens the inbox.

The inbox (`inbox.show` → `inbox_hub`, `inbox.category` → `inbox_category`)
keeps each stored notice as the same data: `items[].notice` is
`{"screen", "view"}` for a notice carried as data, or `{"text"}` for one that
is not yet, with `items[].kind`, `ago_seconds` and `link` (`{command, args}` or
empty).

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

`text` is in the game's default language; pick `texts[lang]` when present. An
announcement carried as data (the founding of a village,
`kind: "settlement.founded"`) has `screen` and `view` instead of any text, like
a notice; the other lines are text until their screens are migrated.

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
| `lot_bought` | `lot_x`, `lot_y`, `auto_roads`? (`[{building_id, lot_x, lot_y}]`, the road the sale laid to the lot, docs/adr/0043), `layout_version` | a resident bought a lot (1.4); the versions include who owns what, so refetch the layout when yours differs |
| `lot_repaired` | `lot_x`, `lot_y`, `auto_roads`?, `layout_version` | a resident put right a lot no road reached (docs/adr/0043): the road laid, cut through their own land, or the sale rescinded; refetch the layout when yours differs |
| `build_started` | `building_id`, `type_code`, `lot_x`, `lot_y`, `rotated`, `finish_at`, `auto_roads`? (1.5: `[{building_id, lot_x, lot_y}]`, roads the game laid with it, already finished), `layout_version` | the head placed a building and paid for it |
| `build_batch_started` | `type_code`, `count`, `buildings` (`[{building_id, lot_x, lot_y}]`), `finish_at`, `layout_version` | (1.5) the head placed several buildings with one command |
| `land_changed` | `kind` (`road_planned` \| `road_cancelled`), `layout_version` | (1.6) a road was drawn or taken back: the land that is open changed, fetch the layout |
| `build_finished` | `building_id`, `type_code`, `layout_version` | construction reached its end |
| `build_cancelled` | `building_id`, `type_code`, `layout_version` | the head called off a building still going up |
| `build_salvaged` | `building_id`, `type_code`, `layout_version` | a building was pulled down and its scrap credited |
| `relocated` | (none) | an operator moved the village to a better site (its terrain and the founding kit's lots changed): fetch the layout again. Carries no `layout_version`; `grid.origin` in the new layout is where the grid now stands |
| `research_started` | `research_id`, `code`, `finish_at` | the village began researching |
| `research_finished` | `code` | the research ended; the village knows the item |
| `knowledge_bought` | `code` | the village bought an item from Support |
| `literacy_changed` | `literacy_share_bps` | a teaching step finished and literacy moved |
| `head_changed` | `office`, `vacated`, `layout_stale`, `player_id`?, `player_name`? | the head office was filled or vacated (appointment, dismissal, election); `layout_stale` says who may place changed, so fetch the layout |
| `promoted` | `from`, `tier`, `office`, `layout_stale`, `head_player_id`? | the settlement grew into the next tier; its grid, build cap and head office changed, so fetch the layout (and the bootstrap for the new `tier`) |
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

### 5.6 State sync — snapshot, update log and push (contract 1.6)

A client that keeps the player's state in a store instead of asking each
screen again (docs/adr/0034). Everything here is additive: a 1.5 client
ignores it, and the legacy `notice`, `vitals` and `inbox` publications of
5.3 keep coming. A server says it serves this with
`bootstrap.features.updates: true` (and `updates: true` on the realtime
token); `state_sync.enabled` switches it.

**The model.** The client holds **entities**: `(kind, id)` with a version
`v` and data `d`. Every change to one of the player's entities is a
**record** in the player's own log, numbered **pts** = 1, 2, 3… with no
holes. The client keeps the pts it has applied; `GET /state` gives a
snapshot and the pts it is current to; `GET /updates?since=<pts>` gives what
came after; the player's channel pushes new records as they are written; and
a command's answer carries the records the command itself caused. All data
is neutral (codes, numbers, ids, RFC 3339 instants): the client words it.

| kind | id | data |
|---|---|---|
| `player` | player id | `name`, `code`, `lang`, `status`, `level`, `xp`, `next_level_xp`, `rank` (code of the catalogue table `life_rank`) |
| `vitals` | player id | `energy`, `nerve`, `health`, each `{value, max, as_of, regen?: {amount, every_seconds, bps}}` |
| `wallet` | currency code | `currency`, `cash`, `bank`, `premium` (the premium currency, Nil), `primary` (the game's money, the one prices are in) |
| `inventory` | item code | `item`, `qty`, `holdings` (`{carried: n, escrow: n}`), `pieces` (`[{id, holding, quality, uses_left}]`) |
| `skill` | skill code | `skill`, `level`, `xp` |
| `timed_action` | action id | `kind` (`travel`, `education`, `work_shift`, `settlement_work`, `place_move`…), `ref_type`, `ref_id`, `state`, `started_at`, `finish_at` |
| `location` | `self` | `city` (code), `place`, `settlement` (id, when the city is a founded settlement), `travel` (`{from, to, mode, departed_at, arrives_at}` or null), `walk` (`{from, to, started_at, arrives_at}` or null) |
| `inbox` | `self` | `unread`, `latest` (ids of the newest notices, newest first) |
| `notice` | notice id | `kind`, `category`, `screen`, `view`, `created_at`, `read`, `instant` (told at once; the others wait in the inbox) — the newest `state_sync.notices_kept` (50); older ones through `inbox.show` |
| `residence` | `self` | `settlement`, `code`, `name`, `tier`, `is_head`, `resident` (absent: the player belongs to no settlement) |
| `settlement` | settlement id | `id`, `code`, `name`, `tier`, `viewer` (`head`, `member` or `public`), `grid_lots`, `layout_version` (the version `GET /settlements/{id}/layout` answers **this** viewer), and for members `treasury` (`{currency, balance}`), `knowledge` (count), `research` (`{code, finish_at}` or null), `election` (`{office, opens_at, candidacy_ends_at, voting_ends_at}` or null: the open election; the client takes the candidacy until `candidacy_ends_at`, then the vote until `voting_ends_at`), and for everyone `buildings` (per-viewer overlay, below). Market-day end times do not exist in the game yet and are not sent |
| `goal` | `self` | the next goal for the quest strip: `code` (`<source>.<kind>`: `mission.work_shift`, `promotion.residents`, `promotion.role`, `promotion.ready`…), `args` (strings: `mission`, `target`, `role`, `from`, `to`), `progress`, `target` (in the goal's own unit), `go_to` (screen address). Source order: the oldest mission the player has under way, then the settlement's next unmet promotion goal (the head gets `promotion.ready` when all are met). Absent when there is nothing to aim at; ADR 0044 goals will feed the same entity |
| `relations` | `self` | `friends` (player ids), `faction` (`{id, rank}` or null), `presence` (`everyone`, `contacts`, `nobody`) |

**The building overlay** (`settlement.buildings[]`, keyed by the layout's building `id`) is cut for the viewer and re-projected on every settlement event, so a client fetches no panel per building:
`tier` (the level in the role's ladder, 0 outside one), `role`, `status` (`working`, `idle`, `building`), `reasons[]` (why a standing building is idle: `no_road`, `no_staff`, `storage_full`, `no_input`, `damaged`), `can_upgrade` (true only when the viewer may upgrade it now: the office or ownership, a next step whose requirements are met, treasury and stock cover it, a builder is free; never true for a private building, whose cash the summary does not read), `actions[]` (the verbs this viewer may perform, `info` first: `upgrade`, `demolish`, `cancel`, `workers`, `take_shift`, `help_build`, `treasury`, `research`, `elections`, `road`), `staff` (`{have, need}` for a workplace: shifts working now against its `workers`), `output_ready` (`{good, amount}`; reserved, never sent yet: no building holds its own output). A visitor (`viewer: public`) gets `tier` and `["info"]` for the standing buildings only. Standing roads have no entry (a building with no entry is "info" only); roads going up do.

**Your photo — `GET /api/me/photo`** (also `/api/v1/me/photo`; bearer token). The player's Telegram profile photo, proxied from the Bot API (`getUserProfilePhotos`, `getFile`, then the file download; the bot token never leaves the server), cached in Redis (`client.photo_ttl`, absence `client.photo_missing_ttl`) and answered with `Content-Type`, `ETag` and `Cache-Control: private, max-age=<photo_ttl>`; `If-None-Match` gets 304. `404` when there is no photo (show initials), `502` when Telegram could not be reached (try again later), `429` past `client.photos_per_minute`. The client fetches it with its bearer header and shows a blob URL.

**Regen is counted by the client.** No record is written as energy or
nerve comes back. Shown value =
`min(max, value + floor(elapsed × bps / 10000 / every_seconds) × amount)`,
with `elapsed` measured from `as_of` on the server's clock (the offset from
`server_time`), never the device clock alone. A meter with no `regen` (health)
shows `value`.

#### `GET /api/v1/state[?kinds=wallet,vitals]`

```json
{"pts": 18234, "epoch": "1", "server_time": "2026-10-02T10:00:00Z",
 "entities": {
   "wallet": {"SUP": {"v": 55, "d": {"currency": "SUP", "cash": 125000, "bank": 480000, "premium": false, "primary": true}}},
   "vitals": {"8a4e…": {"v": 4402, "d": {"energy": {"value": 80, "max": 100, "as_of": "2026-10-02T09:58:12Z",
                                                    "regen": {"amount": 5, "every_seconds": 900, "bps": 10000}}, "…": "…"}}},
   "notice": {}, "…": {}},
 "channels": {"settlement:3c1f…": 1790724836123}}
```

Every entity of the kinds asked (all by default; an unknown kind is
`400 bad_request`), current to `pts`: applying `GET /updates?since=pts`
on top gives the state exactly. The server brings the player's log up to
date before answering. `channels` is the current `seq` of each settlement
channel on the realtime token (5.4), so the first publication there is
compared with something.

#### `GET /api/v1/updates?since=<pts>[&limit=<n>][&epoch=<e>]`

```json
{"pts": 18240, "updates": [ {…}, … ], "more": false, "epoch": "1"}
{"pts": 18240, "updates": [], "more": false, "reset": true, "reason": "too_long", "epoch": "1"}
```

The records after `since`, oldest first, at most `limit` (and
`state_sync.pull_limit`, 500). `more: true`: ask again from the last pts
(Telegram's `differenceSlice`). `reset: true`: drop the local copy and read
`GET /state` — `reason` is `too_long` (behind by more than
`state_sync.reset_threshold`, 2000, or behind the oldest record kept),
`epoch` (the log was rebuilt; `epoch` was sent and differs) or `ahead`
(`since` beyond anything written). `since` equal to the current pts is an
empty answer and costs one indexed read. The cursor **is** the
acknowledgement: there is nothing else to acknowledge, and two devices each
keep their own. `400 bad_request` for a `since` that is not a whole number
≥ 0. Both reads are bounded per player (`state_sync.pulls_per_minute`).

**A record:**

```json
{"pts": 18235, "type": "wallet.set", "entity": "wallet", "id": "SUP", "v": 56, "op": "set",
 "data": {"currency": "SUP", "cash": 125000, "bank": 480000, "premium": false, "primary": true},
 "at": "2026-10-02T10:00:00Z", "cause": "0b9f…"}
```

`op` is `set` (the whole entity) or `del` (no `data`); `patch` is reserved
(a JSON merge patch valid only on top of version `v - 1`; not sent yet).
`cause` is the `request_id` of the command that made the change, when one
did. Records are full entity states, not events: a burst of changes to one
entity between two projections is one record.

#### The push — on `player:<id>`

```json
{"type": "updates", "from": 18236, "to": 18238, "updates": [ {…}, {…}, {…} ]}
{"type": "updates_too_long", "from": 18236, "to": 18338}
```

`updates_too_long` replaces a batch too big to push
(`state_sync.push_max_records`, `push_max_bytes`): pull. Pushes are best
effort; the log is the guarantee.

#### A command's own records

`POST /api/v1/command` answers (when records are ready within
`state_sync.command_wait`, 300 ms) with

```json
"updates": {"pts": 18241, "records": [ {…} ]}
```

the records whose `cause` is this command's `request_id`. Apply them like
any others; the same records arriving later by push are duplicates. Absent
when the wait ran out: the push brings them.

#### The client's rules

1. **Start:** subscribe to the realtime channel, then `GET /state` (or, with
   a stored copy of the same player and epoch, `GET /updates?since=`), then
   apply what the socket delivered meanwhile. Subscribe → pull → apply, so
   nothing committed in between is lost.
2. **Apply** a list of records in pts order: `pts <= local` is a duplicate
   (drop); `pts == local + 1` applies and becomes the local pts; anything
   further is a **gap**. Within an applied `set`, the entity changes only if
   `v` is newer than the one held; a `del` removes it.
3. **Gap:** keep the out-of-order records, wait up to 0.5 s (a reordered
   push usually arrives), then `GET /updates?since=<local>` and apply, then
   the kept records (now mostly duplicates).
4. **Reconnect and return:** after the socket reconnects, and whenever the
   app comes back (`visibilitychange`, the Mini App's `activated`, `focus`,
   `online`), pull `since` even if the socket says it is connected.
5. **Reset:** discard the copy (keep pending optimistic changes) and read
   `GET /state`.
6. **Notices:** show a toast once per notice id, only for an `instant`
   notice, and only for one that arrived live (a record with a pts beyond
   what the copy held when the session started), never for a snapshot or a
   catch-up of history. The others raise the inbox count.
7. **Fallback:** with no socket, pull `since` every 20 s while visible.

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
| 404 | `state_sync_off` | (1.6) state sync is not served (`bootstrap.features.updates` is false) |
| 200 | `founding_*` | (1.3) a refused founding form, `ok: false` with `screen: "founding_refusal"`; see 4.4 |
| 409 | `relink` | the device's bot is gone; link again |
| 429 | `rate_limited` | too many sign-ins, link codes, commands or state pulls |
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
world_recheck_interval, photo_ttl, photo_missing_ttl, photos_per_minute) and `realtime.*` (api_url, publish_timeout). The
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

**Migrations**: `0054_settlement_grid_growth` (1.5: `cities.grid_growth`, the
expansions a village has bought); `0051_founding_form` (1.3: founding drafts, the emblem, motto and
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

### Storage and market additions (fix/storage-market)

| command | args | answer |
|---|---|---|
| `inventory.store` / `inventory.fetch` / `inventory.claim` | `item` (code or serial), `qty`, `nonce`? | `inventory`: goods move between the bags, «انبار من» (`home`) and the holding slot (`claims`); a full bag answers `no_room` (`need_space`, `free_space`, `short`, `need_g`, `free_g`, `heavy`); `home.here` says whether the player stands where they hold a storing building |
| `settlement.stock.donate` / `settlement.stock.take` | `item`, `qty` | `village_materials`; donate: any resident; take: the head's office; `village_not_enough`, `village_storage_full` (with `missing`) |

Views: `inventory` gained `carry.reserved`, `home {capacity, used, here, lines}` and `claims`; `item_detail.can_store`; `village_materials` gained `classes [{class, used, capacity, reserved}]`, `stores [{building, kept}]`, `wage`, `spoil_bps`; `market`, `book` and `market_refusal` gained `unavailable` (no market post) and `village {stalls, stalls_used, per_player, mine, listing_bps, dues_bps, market_day}`; `order_placed.listing_fee`; market refusals `stalls_full`, `stall_limit`, `unavailable`. The village shop views are named `VillageShopView`, `VillageShopLine`, `VillageShopCheckoutView`, `VillageShopRefusalView` (the city shop keeps the plain names). The Economy hub lists entry `storehouse` (command `settlement.materials`).

## Build wait

`build_time` on the build menu, the lot confirm, the upgrade lines and the private-building menu is worker effort (worker-minutes of labour), not a wait. The same views carry `expected_wait` `{seconds, shifts, crew, shift_seconds}`: the estimated real wait with the crew the settlement has now (`ceil(shifts / crew) * shift_seconds`). Show `expected_wait.seconds`.

## A settlement's own money: `response.money` (2026-10-06)

Every neutral command response may carry `money`, the viewer's display currency: their home settlement's own money when it is chartered; absent otherwise.

```json
"money": {"code": "MKP", "name": "مارک پولو", "symbol": "MKP", "r0": 10, "x_ref_ppm": 1000000, "rate_num": 10000000, "rate_den": 1000000}
```

Every amount in every view is an integer in SUP minor units. Show it in the viewer's money as `round_half_up(sup * rate_num / rate_den)` (integer math, a negative amount keeps its sign), with the SUP amount beside it. `x_ref_ppm` is the live reference rate in parts per million (1000000 is 1.00 until a book trades); it is never a peg. The state-sync `wallet` entity of a settlement's money has `local: true`, `name`, `currency` its code and `cash` the balance in units of that money. The head's command `settlement.currency.charter` (args `r0`, `deposit`, `confirm`, `settlement`) charters a money by hand when the automatic charter could not be paid.

## Paying in a settlement's own money: the village desk, the offer, local payments (2026-10-08)

Phases 2 and 2b of the local currency (ADR 0033 sections 6.9 to 6.11). The rule for every client: **what a player owes a settlement with a chartered money is paid in that money when the payer holds the units; a payer who holds SUP instead is never blocked** (the confirm offers the village desk inside the same step, and doing nothing settles in SUP as before). Nothing converts silently and nothing is minted.

### The village desk and its fee

| Command | Args | Answer |
|---|---|---|
| `settlement.currency.desk` | `side`? (`buy` or `sell`), `amount`?, `quote`?, `confirm`?, `settlement`? | `village_currency_desk`, view `DeskView {stage: menu\|ask\|done, side, amount, sup, units, fee, fee_bps, r0, x_ref_ppm, cash_sup, cash_units, desk_units, desk_sup, slippage_bps, presets_sup[], presets_units[], can_set_fee, can_buy, can_sell, min_fee_bps, max_fee_bps}`. Resident only, and only once the money exists. A buy: the player pays `amount` SUP and receives `units = floor((amount - fee) * rate)`, `fee = ceil(amount * fee_bps / 10000)` SUP kept by the treasury. A sell: the player gives `amount` units and receives `sup = floor((amount - fee) / rate)`, `fee = ceil(amount * fee_bps / 10000)` units kept. One atomic confirm with price protection: `quote` is the `units` (buy) or `sup` (sell) that was shown; the desk refuses `desk_moved` when the result is worse by more than `slippage_bps`. Units come from the treasury's own holding and the SUP it takes goes to the treasury; an empty desk refuses `desk_empty` (never a mint), a treasury short of SUP for a sell refuses `desk_no_sup`, a payer short of funds `desk_funds` |
| `settlement.currency.fee` | `bps`, `settlement`? | the holder of the `currency.charter` permission sets the desk's fee (10 to 300 basis points, default 30). Answers the desk menu. Refusal `not_office_holder` otherwise |

### `response.offer`: the confirm of an obligation to a settlement with its own money

A confirm screen of the commands listed below carries `offer` when the settlement it pays has a chartered money:

```json
"offer": {"settlement": "…", "code": "MKP", "name": "مارک پولو", "sup": 100, "units": 1000, "holds": 0,
          "local": false, "can_convert": true, "convert_sup": 1011, "convert_fee": 4, "convert_units": 1000, "fee_bps": 30,
          "convert": {"id": "convert", "command": "settlement.donate", "args": ["100", "confirm", "", "1", "1011"], "params": {"convert": "1", "max_sup": "1011", "amount": "100", "confirm": "confirm"}, "role": "primary"}}
```

- `sup` is the obligation, `units` what it comes to in the money (rounded up: the payer owes the rounding), `holds` the units the payer holds now (every amount in units of the money, not SUP; show them with the money's `name`).
- `local: true`: the payer holds enough, the confirm pays in the money. Say so ("paid in {name}: {units}").
- `can_convert: true`: the payer is short and the desk can fill the gap right now. Show the gap (`convert_units`), the price in SUP (`convert_sup`, of which `convert_fee` is the desk's fee) and a button that sends `offer.convert` (the confirm's own command and arguments plus `convert` = `1` and `max_sup` = `convert_sup`, the price protection: the desk refuses `desk_moved` if it now asks more). The conversion and the payment are one transaction group: both happen or neither.
- neither: the confirm settles in SUP as before; show nothing about the money.
- `offer` is absent when the settlement has no chartered money.

The commands that take the pair `convert` and `max_sup` (named arguments, and the last two positionals):

| Command | Where `convert`, `max_sup` stand | What they pay |
|---|---|---|
| `settlement.donate` | after `settlement` | the gift |
| `settlement.lot.buy` | after `road` | the lot's price (the road fee stays SUP) |
| `settlement.private.place` | after `confirm` | the building permit (construction and materials stay SUP) |
| `settlement.shop.buy` | after `nonce` | the shelf, see below |
| `education.enroll` | after `method` | the class fee (school) or the tuition (home teacher, the tax share going to the treasury) |
| `life.sleep` | after `method` | a night's lodging |

A desk refusal met while converting inside a confirm of the village commands is the refusal `desk_empty`, `desk_no_sup`, `desk_funds` or `desk_moved` (the village refusal kinds); `education.enroll` and `life.sleep` fail with the conflict errors `application.ErrDeskEmpty`, `application.ErrDeskNoSUP`, `application.ErrDeskFunds`, `application.ErrDeskMoved`, rolled back whole.

### What is paid in the money without a confirm of its own

- A **player's wage** at a workplace shift, at a construction or repair shift paid by the treasury and as a player teacher; the **fee of a training session**; a **property tax** row of a period (`settlement.tax.pay` and the village's tick); a **fine** imposed by a city that has its own money (crime fines, the faction operation's fine): paid from the holder's units when they hold enough, otherwise SUP exactly as before. The flow's own row (shift, seat, tax row, report) keeps its SUP amount.
- The **village shelf** (`settlement.shop.buy`): when the buyer holds the units (or converts at the desk in the same confirm), the **price is paid to the NPC economy, so its units are burnt** (the supply of the money falls and the backing basis by the burnt units' share; the reserve pot keeps its SUP) and the **sales tax goes to the treasury in units**. The sale row keeps the SUP total and tax; `method` on the answer is `local`.

### Paying a neighbour: `bank.pay` and `bank.pay.send` with `method: "local"`

Two players who live in the same settlement, when it has chartered its money, may pay each other in it. The pay screen (`pay`, view `PayView`) then carries `local_name`, `local` (the payer's units), `local_options[]` and `can_local`; `economy.pay` adds a third group of buttons with `method` = `local`. The `amount` of a local payment is in **units** of the money, there is no fee and no need to stand together. `bank.pay` with `method: "local"` answers `pay_confirm` whose `currency` names the money and `amount`, `total` and `after` are units; `bank.pay.send` pays (the `nonce` makes a second press a replay) and answers `pay_sent` with `currency` set. A payer short of units goes back to the pay screen with the notice `short_local` (`needed`, `available` in units, `name`). The payee's notice (`payment_received`) carries `method: "local"` and `currency`. The payer chooses; nothing is converted for them.

### Ledger reasons (ADR 0009 section 2)

`local_wage`, `local_payment`, `local_transfer` (a player to a player, with a treasury share for the tax of a tuition fee), `fx_desk_sup` and `fx_desk_local` (the two legs of a conversion), `currency_burn`. A holder's history shows them in the currency of the account.

### Not paid in the money

NPC wages, the storekeeper's wage and the citizen employer's escrow, construction, materials, research and upkeep (all SUP-indexed), the road fee of a lot, a lot's refund, and the bag repair at the shop counter. Property rent, property tax and upkeep of the city module (`property.*`) belong to cities and do not run in a founded settlement.

## The floating VC/SUP book: `fx.*` (2026-10-08)

Phase 3 of the local currency (ADR 0033 sections 6.8 to 6.10). Each chartered money has one order book against SUP. **Prices are micro-SUP per unit of the money** (`1000000` micro-SUP is one SUP): show `price / 1000000` SUP per unit, or its reciprocal as units per SUP; the book of a money worth a tenth of a SUP a unit quotes `100000`. Quantities are units, SUP amounts SUP minor units. A **buy** order buys units with SUP (it sells SUP); a **sell** order sells units for SUP. The offered side is set aside when the order is placed and given back when it ends. All of these screens are private (the player's own orders and balances) and need the money to be chartered (`fx_no_market` otherwise).

| Command | Args | Answer |
|---|---|---|
| `fx.book` | `settlement`? (default: the viewer's home) | `fx_book`, view `FXBookView {settlement, village, code, name, symbol, r0, x_ref_ppm, ref_price, last_price, band_low, band_high, bids[{price, units, orders}], asks[…], reserve_fee_bps, village_fee_bps, max_move_bps, min_order_sup, cash_sup, units, escrow_sup, escrow_units, my_orders[{id, no, side, units, filled, price, escrow_left, status, created_at, expires_at}], trades[{price, units, at}], preset_units[], min_trades, window_periods, notice?}`. `bids` highest price first, `asks` lowest first, ten levels each; `ref_price` is `x_ref_ppm / r0` (the price the reference rate stands for) and `last_price` the latest fill (0 before the first); orders may only be placed in `[band_low, band_high]` (the circuit breaker) |
| `fx.place` | `settlement`?, `side` (`buy`/`sell`), `units`, `price` (micro-SUP, or SUP with a decimal point), `confirm`? | without `confirm`: `fx_order` stage `ask`, view `FXOrderView {…, side, units, price, worth_sup, escrow, fee_bps, crosses, can_place, band_low, band_high, cash_sup, units_held}`: what the order would set aside (`escrow`: SUP with the fee for a buy, units for a sell), whether it crosses the best opposite order (`crosses`: it fills at once, at the RESTING price, at least in part), nothing changes. With `confirm: "confirm"`: places it, matches it, answers stage `done` with `order` (the order as it stands), `rested` (a remainder is on the book), `units_moved`/`sup_moved` (what it did as taker: units received net of the village fee or sold, SUP paid with the fee or received) and the balances after. A repeated confirm (same request key) shows the book and places nothing twice |
| `fx.cancel` | `order`, `settlement`? | cancels the viewer's open order and gives back what it still holds; answers `fx_book` with `notice: "cancelled"`. Cancelling an order that is already over does nothing. Somebody else's order: `fx_not_yours` |
| `fx.history` | `settlement`? | `fx_history`, view `FXHistoryView {…, r0, x_ref_ppm, min_trades, window_periods, period_seconds, periods[{period_no, trades, volume_units, vwap, value_ppm, window_trades, x_ref_before, x_ref_after, at}]}`, newest period first. `vwap` is a price (micro-SUP per unit); `x_ref_*` are the reference rate (ppm) |
| `fx.convert` | `from`, `to` (currency codes or `SUP`), `amount` (units of `from`), `quote`?, `confirm`? | `fx_convert`. Without `from`/`to`/`amount`: stage `menu` (`cash_sup`, `holdings[{…, units}]`). With them and no `confirm`: stage `ask`: `legs[]` (a sale of units for SUP, then a purchase with the SUP it brings; between two moneys both legs, each with its own fee), `out` (what would come out now, in `to`), `min_out` (the least the confirm accepts: the quote less `slippage_bps`), `complete` (the books can fill it). With `confirm: "confirm"` and `quote` = the `out` that was shown: converts in ONE step and answers stage `done` with `gave`, `got`; the whole is refused (`fx_moved` or `fx_no_liquidity`) and nothing moves when the books now give less than `min_out` or cannot fill a leg |

**The reference rate.** `x_ref_ppm` (1000000 is 1.00) is the average of the last `window_periods` (7) period readings of the book and does **not move until the window holds `min_trades` (8) fills**; so it moves a window's share of the gap per period of sustained trading, not at once. The village desk (`settlement.currency.desk`) and every display (`response.money`) read it. Orders are refused when their price is further than `max_move_bps` from the reference price (`fx_out_of_band`, with `min`/`max` the band).

**Fees.** Selling SUP (buying units): `reserve_fee_bps`, paid by the buyer on top of the SUP price to Support's treasury. Selling units: `village_fee_bps` (the head's lever), taken from the units sold and kept by the village treasury. The `escrow` of the ask already includes the buyer's fee.

**Refusals** (`response.refusal.code`, screen `fx_refusal`): `fx_no_market`, `fx_invalid`, `fx_out_of_band` (`args.min`, `args.max` the band), `fx_too_small` (`args.min` the least an order must be worth in SUP), `fx_funds`, `fx_too_many` (open orders cap), `fx_not_yours`, `fx_not_found`, `fx_moved`, `fx_no_liquidity`.

**Ledger reasons** (finance screens): `fx_escrow` (set aside), `fx_release` (given back), `fx_trade_sup` (the buyer's SUP to the seller and the fee to Support), `fx_trade_vc` (the seller's units to the buyer and the fee to the village treasury). The escrow is a separate account per owner and currency, kind `fx_escrow`; the state-sync wallet does not list it (what an open order holds is in `my_orders[].escrow_left`).

**Entry points.** The village money panel (`village_money`) gains the action `fx.book`; `fx.book` links `fx.history`, `fx.convert` and `fx.place`.

## The reserve of a settlement's money: `settlement.currency.reserve` (2026-10-09)

Phase 4 of the local currency (ADR 0033 sections 6.3 to 6.8, 6.11 to 6.13 and 7). One command, screen `village_reserve`, view `ReserveView` (see `api/views.gen.ts`).

| Command | Args | Answer |
|---|---|---|
| `settlement.currency.reserve` | `action`?, `amount`?, `price`?, `id`?, `confirm`?, `settlement`? | Without `action`: stage `menu`: `status` (`chartered`, `wind_down`, `retired`), `pot_sup`, `basis`, `excess` (pot minus basis, the only part that can leave), `supply`, `stabilisation` (units the head's purchases hold), `market_cap_sup`, `coverage_bps` with `coverage_known` (false with no supply; coverage is a measure of confidence, never a promise), the counters (`minted`, `burnt`, `deposited`, `released`, `intervention_out`, `intervention_in`), the Reserve Bank's levers in force (`mint_fee_bps`, `reserve_fee_bps`, `max_move_bps`, `withdraw_notice_hours`), the head's limits (`cap_bps`, `floor_bps`, `delay_hours`, `buy_budget_sup`, `sell_budget_units`: what is allowed now), `treasury_sup`, `treasury_units`, `cash_sup`, `my_units`, `my_share` and `can_claim` (a wind-down), `can_issue` (permission `currency.issue`) and `can_policy` (`bank.policy`), `presets[]` (SUP amounts), `interventions[]` and `withdrawals[]` (the public log), `macro` and `trend[]` (macro readings), `wind_down_at` / `wind_down_ends_at`. With `action` and no `confirm`: stage `ask`, the figures the act would have (`amount`, `price`, `out`, `execute_after`) and `reason` when it cannot be confirmed (`funds`, `no_units`, `budget`, `no_excess`); nothing changes. With `confirm: "confirm"`: carries it out once (a repeated confirm shows stage `done` and does nothing twice) |

Actions: `issue {amount: SUP}` (mint more against a deposit from the treasury; `out` is the units that reach the treasury after the fee), `burn {amount: units}` (the treasury burns units it holds), `buy {amount: units, price}` and `sell {amount: units, price}` (a public request that executes `delay_hours` later, at the first period close, within the limits; clipped to them), `withdraw {amount: SUP}` (announce taking excess; executes after `withdraw_notice_hours` if the excess is still there), `cancel {id}` (a pending request or withdrawal), `retire` (begin the wind-down; cannot be undone) and `claim` (a holder burns all their units for their share of the pot while the window is open). `price` is micro-SUP per unit, or SUP with a decimal point; omitted, it is the reference price.

**Money panel.** `village_money` (`chartered`) gains `status`, `basis_sup`, `excess_sup`, `market_cap_sup`, `coverage_bps`, `coverage_known`, `stabilisation_units`, `macro` and `trend[]` (the last seven readings, oldest first) and the action `currency.reserve`. A macro reading is `{period_no, x_ref_ppm, tradable_ppm, nontradable_ppm, price_ppm (1000000 is 1.00 at charter), pi_local_bps, supply_growth_bps, supply_units, m_sup, y_sup, coverage_bps, coverage_known, at}`; none exists until the first period closes.

**Refusals** (screen `village_refusal` kinds): `reserve_funds`, `reserve_no_units`, `reserve_no_excess`, `reserve_budget`, `reserve_wind_down`, `reserve_not_wind`, `reserve_nothing`, `reserve_invalid`, `reserve_not_found`, and `not_office_holder` for a viewer without the permission. The head cannot place orders on the pair while a request of theirs is pending: `fx_blackout`.

**Ledger reasons** (finance screens): `intervention_buy` (the pot's SUP set aside for the head's purchase), `reserve_release` (excess to the treasury, or the remainder at retirement), `wind_down_claim` (a holder's share of the pot); the burn of a holder's units in a claim is `currency_burn`.

**Entry points.** The village money panel gains the action `currency.reserve`.

## Manage my lot: `settlement.lot.manage` (2026-10-09)

Phase B1 of ADR 0045 (section 3, the function-and-content model; ADR 0044 5.5 for the fee). The owner of a lot chooses the FUNCTION of the building on it and what is INSIDE; the game builds it by the hiring board's shifts and generates the look. One command, screen `lot_manage`, view `LotManageView` (`api/views.gen.ts`). It is the popup «مدیریت قطعهٔ من».

| Command | Args | Answer |
|---|---|---|
| `settlement.lot.manage` | `building`?, `action`?, `code`?, `n`?, `name`?, `confirm`?, `convert`?, `max_sup`? | Without `building` and with one building of mine: its detail; with several: stage `menu` and `buildings[{id, building, x, y, function, function_name, level, storeys, built, has_order}]`. With `building`: stage `detail`. With `action` and no `confirm`: stage `ask`, `quote` and `reason`; nothing changes. With `confirm: "confirm"`: carries it out once (a repeated confirm shows stage `done` and does nothing twice) |

**Detail** (stage `detail`, also inside `done`): `id`, `building`, `x`, `y`, `w`, `d`, `mine`, `public` (the settlement's own building and I hold `public.build`), `can_manage` (I may order now), `built`, `function {code, name, family, level, max_level, status, permit}`, `storeys`, `max_storeys` (the knowledge's support table), `stability_bps` (10000 for one storey, falling as the storeys near the most allowed), `area_used` of `area_capacity` (floor area: a footprint cell gives `settlement.building_area_per_cell` per storey), `modules[{module, count, included, max, effect, area_each, housing_capacity, personal_storage, stall_slots, removable}]` (`included` came with the level and cannot be removed), `additions[{module, left, materials[{item, qty}], shifts, area_each, can, reason, needs}]` (what could be added, with the reason when not), `upgrade {to, building, cost_money, materials, shifts, adds, can, reason, needs}` (the next level; null on the top or on a settlement's own building), `storey_up {to, materials, shifts, can, reason}`, `functions[{function, family, current, available, needs, cost_money, materials, shifts, fee_sup, permit_fee, effects}]` (the functions a resident can build, with what a change costs; the footprint must match), `work {id, adds, level_to, storeys_to, convert_to, shifts_total, work_done, work_needed, progress_bps, job_open, paused, status}` (the open order; `job_open` false means its job is not on the hiring board: post it again with `settlement.labor.post {id: building}`), `staff[{role, slots}]`, `if_unstaffed`, what the lot gives `housing_capacity`, `personal_storage`, `stall_slots`, `condition_bps`, `look`, `templates[{id, name, code, function, level, storeys, modules, mine, applicable, reason}]`, `cash`.

**Actions** (the `action` argument; each is ask then confirm):

| action | code | n | does |
|---|---|---|---|
| `add` | module | count (default 1, at most 20) | builds modules (an order) |
| `remove` | module | count | takes modules beyond the level out at once; `building_salvage_bps` percent of the materials come back to the holding slot |
| `level` | | | raises the function one level (an order); the catalogue building follows the level |
| `storey` | | | builds one storey (an order) |
| `function` | function code | | changes the use of the lot (an order); costs the new first level and the use-change fee |
| `template_save` | | | saves the building's composition; `name` (1 to 60 characters; empty: «قالب n» in the player's language); answers `share_code` |
| `template_apply` | template id (mine) or share code (anyone's) | | the order that reaches the template: never removes, never bypasses a gate; modules no rule reads yet are skipped (`quote.skipped`) |
| `template_delete` | template id | | deletes one of mine |

**Quote** (`quote`): `materials[{item, need, have}]` (from the home store, then the bags), `money` (to the trade), `fee_sup` (the use-change fee), `wages` (the labour at today's wage, paid shift by shift), `shifts`, `cash`, `total` (money plus fee, paid now), `adds`, `level_to`, `storeys_to`, `convert_to`, `salvage` (what a removal gives back), `skipped`. `reason` (ask stage) says why the confirm is not offered: `materials` (with `needs`: the VillageNeed list, where each missing material comes from), `cash`, `requires` (`needs`: the knowledge or the building), `area`, `slot`, `storeys`, `busy`, `not_built`. The fee of a change of use settles in the settlement's own money where it is chartered: the response's `offer` (as for every confirm that pays a settlement) carries the desk's price, and `convert` / `max_sup` convert inside the confirm.

**Look** (`look`): the descriptor the client draws. `version` (kit 1), `function`, `level`, `w`, `d`, `storeys`, `material` (`timber` `stone`), `roof` (`gable` `hip` `shed` `flat`), `modules` (counts that show), `condition`, `seed`, `palette` (the biome code), `wobble` (-2..2 tenths of a cell), `windows`, `door` (`n` `e` `s` `w`), `hue` (-15..15 degrees), `prop` (`none` `woodpile` `barrels` `cart` `crates` `bench`), `chimney` (a hearth or a fire), `awning` (a stall's shelves). It is a pure function of the building and its contents, the same on every replica, and changes only when the contents do. The client builds the geometry from a small kit of instanced parts; no geometry crosses the wire.

**Refusals** (`response.refusal.code`, screen `village_refusal`): `village_lot_not_yours`, `village_lot_not_built`, `village_lot_busy` (an order is already being built), `village_lot_no_function`, `village_lot_no_module`, `village_lot_no_area`, `village_lot_slot_full`, `village_lot_not_buildable`, `village_lot_storeys`, `village_lot_nothing`, `village_lot_templates_full`, `village_lot_no_template`, `village_lot_invalid`, plus `village_materials` / `village_prerequisite` with the needs list, `village_citizen_no_cash`.

**Labour.** An order posts a job of kind `fitout` on the hiring board (`settlement.labor.board`, `settlement.labor.site`): `LaborJobLine.kind` is `fitout`, its progress is the order's. The owner works his own shifts, hires NPC labourers (`settlement.labor.hire`) or lets a neighbour take the job; the wage is the employer's, shift by shift.

**Events** (outbox): `settlement.lot_ordered {settlement_id, building_id, work_id, function, convert_to, level_to, storeys_to, adds, shifts, player_id}`, `settlement.lot_built {settlement_id, building_id, work_id?, function, level, storeys, modules, removed?}` (the layout of a building changed: refetch its look), `settlement.shift_done` with `kind: "fitout"`.

**Entry points.** `settlement.mine` (`village_mine`) gains the action `lot.manage`; the own-lot ring of the 3D view opens it for the building under the finger.


## Research capacity and speed: `settlement.research` and `settlement.knowledge.research` (2026-10-09)

ADR 0048, migration 0134. A settlement runs as many research projects at once as it has slots: one free slot for everybody and the slots of its research buildings (library, laboratory, higher school) that worked today. A building works on a local day when enough scholars are on its posts (players who took a post, then town scholars of the labour pool up to the number it needs), the treasury pays them and the day's upkeep items are in the stock. Every project records the quote it started on (slot, pace, ahead-of-era surcharge, breakthrough discount, sharing bonus); later changes of staff or config never move its finish time. A project already running is never slowed or stopped when its building lapses: only a new project needs a working slot.

| Command | Args | What it does |
|---|---|---|
| `settlement.research` | `action`?, `code`? | Without `action`: the research desk (screen `research`, view `ResearchBoardView`). With `action` (`post`, `leave`, `propose`, `accept`, `decline`, `end`) and `code` (the building id of a post, the settlement code to offer a pact to, or the pact id): does it once, then shows the desk. `post` and `leave` are for any resident (one post per person); the pact acts need the charter permission `research.share` |
| `settlement.knowledge.research` | `code`, `slot`? | As before, now into a free slot: `slot` is `free` or a research building id from the desk's `slots[]`; without it the settlement takes the slot the project finishes soonest in. The knowledge list's lines carry the `slot` of their quote, so a client sends back exactly what it showed |

Views (in `api/views.gen.ts`, regenerated):
- `ResearchBoardView {name, capacity, running, frontier, literacy_percent, slots[], buildings[], projects[], pacts[], neighbours[], experience[], may_share, share_cap_bps}`. `slots[]`: `{ref, building, capacity, used, bonus_bps, staff_bps}` (`ref` is `free` or a building id). `buildings[]`: `{id, building, open, idle, slots, needed, posts, players, npcs, wage, bonus_bps, upkeep[{item, qty, have}], mine, can_take}` with `idle` one of `no_scholars`, `no_wage`, `no_upkeep`. `projects[]`: `{knowledge, slot, building, speed_bps, ahead_bps, discount_bps, share_bps, finish_at, left}`. `pacts[]`: `{id, partner, state}` with `state` `active`, `incoming` (offered to us) or `outgoing`. `neighbours[]`: settlements a pact may be offered to (`code` is the act's argument). `experience[]`: `{field, points, per, max_bps}`: a project of depth d in that field gets `min(points, d * per) / (d * per) * max_bps` off its price and time, and spends those points.
- `KnowledgeListView` gains `projects[]` (every project in progress, with `slot` and `speed_bps`) and `capacity`; `running` stays the oldest. `KnowledgeLine` gains `speed_bps`, `ahead_bps`, `discount_bps`, `share_bps`, `slot`, `field`; its `research_cost` and `research_time` are now the quote of a project started now. Basis points: 10000 is the plain pace or price; `ahead_bps` above 10000 is the surcharge for being ahead of the world; `speed_bps` is the pace of the slot (10000 plus the building, the scholars, the literacy, the catch-up of a settlement behind and the sharing).
- `village_refusal` gains the kinds `research_no_post`, `research_post_held`, `research_no_post_held`, `research_pact_open`, `research_pact_self`, `research_pact_not_found`, `research_no_slot` (codes `village_` + kind). A research start with every slot busy is the old `village_busy`.

Ledger (finance screens): new reasons `scholar_wage` (treasury to the scholar's cash) and `scholar_wage_npc` (treasury to the sink), once per settlement per local day; the day's upkeep is an item movement with reason `research_upkeep` (an end). Events: `research_post_taken`, `research_post_left`, `research_pact_proposed`, `research_pact_active`, `research_pact_declined`, `research_pact_ended`; `research_started` gains `slot` and `speed_bps`.


## Upgrade views: every upgrade lists what it needs and gives (2026-10-09)

`BuildingView.upgrades[]` (`BuildingUpgradeLine`, mode `up`, civic hall and every role) and `KnowledgeListView.lines[]` now carry the structured prerequisites. New fields on an upgrade line: `materials[{item, qty}]`, `shifts` (crew shifts), `ready` (nothing at all missing, materials and money included; `available` is the old "knowledge, building and literacy are met"), `needs[]`, `staff[{role, slots, wage_bps, shift_hours}]`, `upkeep_money`, `consumes[{item, qty}]` (inputs and fuel of a working day), `effects[{target, value}]`, `capacity[{kind: research_slots|storage_room, code, value}]`. `missing[]` stays for old clients. `needs_tier` is removed (it was never sent). `KnowledgeLine.needs[]` has the same shape for a researchable or locked item (the knowledge it stands on, the land, the literacy, the treasury for the price now).

`needs[]` item (`Prerequisite`): `kind` (`knowledge`, `building`, `item`, `money`, `literacy`, `terrain`; `staff`, `permission`, `personal` are reserved), `item` (code and name), `role` and `tier` (a building asked by role), `have` and `need` (units of a material, SUP of the treasury, basis points of literacy; 0 and 1 for knowledge and buildings), `how` (`research`, `build`, `train`, `buy`, `travel`, `donate`), `where`, `options[]` (alternatives: the knowledge that provides a capability, the buildings of a role), `makers[]` and `price` (a material: the workplaces that make it, Support's price per unit, 0 when not for sale). Construction is paid in SUP only (phase 2b leaves construction and materials out of the settlement's money), so there is no local-money offer on an upgrade.

## The national levy is stopped and refunded (2026-10-09)

The defence period of ADR 0022 no longer levies founded settlements (`Diplomacy.LevyCitiesOf`). `admin settlement refund-national-levy --reason ...` returns to each settlement exactly what `national_levy` took from its treasury (migration 0135, ledger reason `levy_refund`: from the country's state treasury and defence fund in proportion, the rest from the system source), once per settlement. Finance screens: a new reason `levy_refund` (credit to a city treasury).

## The market day: `settlement.trade` (2026-10-09)

ADR 0049, migration 0136. Once a local day a travelling trader buys the surplus of the goods the head put on sale, if the barter post stands with its clerk of the market (NPC seat) and the treasury can pay him.

| Command | Args | What it does |
|---|---|---|
| `settlement.trade` | `action`?, `code`?, `n`? | Without `action`: the trade desk (screen `trade`, view `TradeDeskView`); reading it also settles today's market day. `action` `keep` with `code` (an item with `village_sell`) and `n` (units the head keeps, 0 or more) puts the item on sale above `n`; `off` with `code` takes it off. Needs the charter permission `trade.export` |

View `TradeDeskView {name, has_post, cap, price_bps, prospect, may_order, keep_presets[], items[], last}` (in `api/views.gen.ts`). `items[]`: `{item, stock, reference, unit, on, keep, surplus}`. `last`: `{outcome, gross, wage, at, lines[{item, qty, unit}]}`; outcomes `sold`, `no_clerk`, `no_wage`, `nothing`, `too_little`, `no_road`. Without the post the view has `has_post: false` (show «not available here» and what to build).

New charter permission `trade.export` (group treasury); the head office of stored charters gets it with the migration. Ledger reasons for finance screens: `export_sale` (system source to treasury) and `market_clerk_wage` (treasury to sink); item reason `exported`. Config: `settlement.export_price_bps`, `export_cap_base`, `export_cap_per_resident`, `export_keep_presets`. Refusals use the existing village refusal kinds (permission, unknown good).

## Real village goods and tool wear (2026-10-09)

ADR 0050, no migration. Content: new components `firewood`, `clay`, `hide`, `rag`, `paper`, `tools`, `pots`; new workplaces `clay_pit`, `tool_workshop`, `paper_mill`; new knowledge `papermaking`. The woodcutter also yields firewood, the pasture hides, the weaving shed rags, the pottery kiln pots.

Views (in `api/views.gen.ts`, regenerated):
- `MaterialsView` gains `stand_ins[{item, stand}]` and `stand_in_until`: while the grace lasts, `stand` still does in place of `item` (wool for paper, timber for firewood); empty afterwards.
- `ResearchBoardView` gains `stand_in_until`; each `ResearchUpkeepLine` gains `stand_in` (a named item, empty code when none) and `stand_in_have`. The research buildings now ask for `paper` and `firewood`.
- `WorkNode` (the building panel's work block) gains `tool_wear_bps` (the share of a tool one shift wears, 0 = needs none), `tools_have`, `bare_hands` (the next shift works at `bare_hands_bps` of its output because its tool is worn out and the stock has none).
- `TradeDeskView` gains `next_at` (the next market day: the settlement's next local midnight) and `clerk {seat_building, filled, wage, staffed_by}` (`staffed_by` is `npc`: a worker of the settlement's pool).

Config: `settlement.real_items_rule_at`, `real_items_grace_days`, `tool_bare_hands_bps`. Event `shift_started` carries `tool_used` and `bare_handed`.

## Function rows are working buildings: A4a (2026-10-09)

ADR 0051, no migration, no view change. New buildings in the build menu (codes = function codes): `well`, `mill`, `bakery`, `charcoal_clamp`, `iron_pit`, `bloomery`; the existing `smithy` now works (bloom + charcoal gives tools). New knowledge `charcoal_burning` and `bloomery`. New stock items `flour_sack`, `charcoal`, `bloom`, `spring_water` (barrels from the well); `bread` is now made in a village. Every one is an ordinary workplace: `BuildingView.work` / `WorkNode` shows its staff, inputs (with stock and what is missing), outputs and where they go. A workplace that needs tools shows `tool_wear_bps`. Item names come from `component.<code>` in the locales; `bread` is a good and its name comes from the item catalogue.

Content (not API): a function row with a `workplace` block generates its building; see `configs/content/building_functions.yml`.

## A4b: tannery, brickworks, the daily services and the inn (2026-10-09)

ADR 0052, migration 0137. New buildings in the build menu: `tannery`, `brickworks`, `teahouse_inn` (the inn). New knowledge `tanning`. New items `bark` (from the woodcutter) and `leather`. The bakery and the bloomery now cost bricks instead of stone.

- `VillageOverviewView` gains `watch[]` (`WatchLine {building, held, idle}`): one line per standing watch post; `idle` is `no_staff`, `no_wage` or `no_supplies` (or empty when held). An idle post adds nothing to `security_percent`.
- `life.sleep` with `spot: hostel` in a founded settlement: the bed exists only where an inn stands (availability `sleep_spot/hostel` requires `teahouse_inn`) and only on a day the inn is open; otherwise the refusal view `LifeRefusalView.kind = "closed"` (text: the inn is closed today and lacks staff, bread, water or firewood).
- Ledger (finance screens): reason `service_wage` (treasury to sink), once per settlement per local day; item reason `service_upkeep` (an end).
- Events: none new.

## The pace of work (2026-10-09)

ADR 0053, no migration, no view change. Every workplace shift is now 15 to 60 real minutes (`WorkNode.shift_seconds` and `WorkplaceLine.shift` carry the length); wages per shift are about 100 SUP per worker-hour. The woodcutter, carpenter, mason, weaving shed and kiln keep their building codes. Show shift lengths in minutes and hours, not seconds.

## Breakthrough fields are fed by real work (2026-10-09)

ADR 0054, migration 0138. No new command. `VillageOverviewView.watch[]` (A4b) is renamed `services[]`: `{building, service, held, idle}` for every daily service post (watch post: `local_security`, health house: `primary_care`, inn: `lodging_and_tea`); `idle` is `no_staff`, `no_wage` or `no_supplies`. A post that was not open today adds nothing to the coverage numbers. The health house now needs a health worker (a seat of the labour pool), cloth 1 and water 2 a day. `ServiceLine` also carries `grace` (open only because of the grace of the rule date), `grace_until` and `needs[]` (what the post uses a day): show a calm note «تا ... همین‌طور باز می‌ماند، از آن روز ... می‌خواهد». Config `settlement.service_rule_at`, `service_grace_days`. Experience (the research desk's `experience[]`) now grows in all eight fields.

## Personal requirements of posts (2026-10-10)

ADR 0055, no migration. New type `PersonalNeed {kind, item, have, need, how}` (`kind`: `level`, `skill`, `certificate`, `literacy`, `rank`; `item` names the skill or the course). Where it appears: `WorkplaceLine.personal` and `WorkView.personal_until` (what the viewer lacks for a shift there and until when he may go on); `ResearchBoardView.personal` and `personal_until` (the scholar's post); `VillageRefusalView.personal` with the new refusal kind `personal` (a shift or a scholar's post refused after the grace). Config `settlement.personal_rule_at`, `personal_grace_days`. A finished class a certified teacher gave now counts for education experience.

## Owner decisions of 2026-10-10 (personal requirements, pay, NPC hours)

- `Requirement` (course and job views) gains `until` and the kind `literacy`: a village class warns of the student's literacy during the grace (the requirement carries `until`, the literacy class in `course_code`, the nearest place and the trip) and refuses after it.
- Carpentry and masonry no longer ask a level (`PersonalNeed` only appears for the smith's level 3 and the scholar's literacy).
- Wages per shift follow the wage classes: raw trades 50 per half hour, skilled ones 65 to 75 (`WorkplaceLine.wage`).
- Config: `labor.npc_hours_per_slot_day` (8) replaces `labor.npc_shifts_per_slot_day`.

## The remaining stand-ins (2026-10-10)

ADR 0057, no migration, no view change. New skills (names from the `skill.*` locale): forestry, herding, weaving, pottery, milling, baking, teaching; a shift in the matching workplace adds experience (the skills screen). The literacy a class moves follows the teacher's teaching level. Config `settlement.teacher_base_bps`, `teacher_per_level_bps`, `teacher_xp_per_class`.

## Dead levers (2026-10-10)

ADR 0058, no migration. The work block of a building view (`village.NodeWork`) has `knowledge_bps` (omitted when 0): the share of every shift's output that the settlement's knowledge of the craft adds. The building panel may show it beside the tool wear line.

## The car asks its licence (2026-10-10)

ADR 0059, no migration. `TravelOptionsView` has `licence`: a list of `{mode_code, course, until}` for the private modes that ask a certificate the rider lacks. `until` set: the mode is still in `options` and the certificate is needed from that time; `until` unset: the mode is left out of `options`. Empty for a rider who needs nothing.

## The stall keeper (2026-10-10)

ADR 0062, migration 0139. `settlement.lot.manage` on a stall the viewer owns carries `keeper` ({hired, share_bps, can, reason, seats_free}); the acts `keeper_hire` and `keeper_end` (ask, then confirm like the others). Refusals `lot_no_keeper` (the building has no keeper) and `lot_keeper_none` (already hired, or nobody free). While a keeper is hired the owner's asks stay on the village book when he is away; the config is `trade.stall_keeper_share_bps`.

## Buildings that wait for their mechanic (2026-10-10)

ADR 0063, no migration, no view change. The build menus (`settlement.build`, the citizen build menu, the lot's function choices) no longer list home_workshop, bank, clinic, airport, port, factory, manufactory, mine, militia_camp, retainer_hall, constable_post, police_post, canal_channel, shaft_well, terrace_works, paddy_banks and barracks; asking for one by code is refused as not found. Buildings of these types that already stand keep standing.

## The stall keeper's pay (2026-10-10)

ADR 0062 addendum, migration 0140. `keeper_hire` takes `code` = `share` or `wage` and `name` = the number (basis points for a share, minor units for a wage; empty: the default). The `keeper` block carries `pay` (`share` or `wage` when hired), `share_bps`, `wage`, the ranges `share_min_bps`, `share_max_bps`, `wage_min`, `wage_max` (the defaults when not hired) and `left` (`wage_unpaid` for seven days after a keeper left because the owner could not pay). New refusal `lot_keeper_terms` (pay outside the range). Config `trade.stall_keeper_share_min_bps`, `_max_bps`, `stall_keeper_wage`, `_min`, `_max`.

## The keeper's figures and the hidden permissions (2026-10-10)

ADR 0064, migration 0141. `LotKeeperLine` has `sold_away`, `cut_total`, `sold_away_today`, `cut_today` (minor units; zero when not hired): the sales made while the owner was away since the hire (today: the settlement's local day so far) and what the keeper took of them, share and day wages together. The charter view no longer lists the permissions no act asks for: `permissions[]`, `mine[]` and `offices[].grants[]` carry only the active ones (the founder's office still holds the rest in the server).
