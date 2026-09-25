# Game client API — v1

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
4. [Bootstrap](#4-bootstrap)
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
    "city_code": "calderis", "city": "Calderis", "travelling": false, "no_city": false, "jailed": false,
    "cash": 125000, "bank": 480000, "withdrawal_fee_bps": 100,
    "deposits": [{"amount": 100000, "nonce": "n1", "all": false}, {"amount": 125000, "nonce": "n2", "all": true}],
    "withdrawals": null, "can_deposit": true, "can_withdraw": true, "notice": ""
  },
  "actions": [
    {"label": "📥 Deposit 100,000 Nil", "command": "bank.deposit", "args": {"amount": "100000", "nonce": "n1"}, "row": 0},
    {"label": "📥 Deposit all - 125,000 Nil", "command": "bank.deposit", "args": {"amount": "125000", "nonce": "n2"}, "row": 0},
    {"label": "✏️ Deposit any amount", "command": "bank.deposit", "input": {"field": "amount"}, "row": 1},
    {"label": "✏️ Withdraw any amount", "command": "bank.withdraw", "input": {"field": "amount"}, "row": 2},
    {"label": "💸 Pay a player", "command": "bank.pay", "row": 3},
    {"label": "🏛 National bank", "command": "loan.hub", "row": 3},
    {"label": "🔙 Back", "command": "player.profile.get", "row": 4},
    {"label": "🔄 Refresh", "command": "bank.show", "row": 4}
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
{"name": "Sara", "code": "K7Q2M9A", "avatar": "🦊", "city_code": "calderis", "city": "Calderis",
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
{"city_code": "calderis", "city": "Calderis", "no_city": false, "travelling": false, "travelling_to_code": "", "travelling_to": "",
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

---

## 4. Bootstrap

### `GET /api/v1/bootstrap`

Load once after signing in (and after a content change).

```json
{
  "player": {"id": "8a4e…", "code": "K7Q2M9A", "name": "Sara", "lang": "fa", "city_code": "calderis", "city": "کالدریس"},
  "content_version": 42,
  "languages": [{"code": "en", "name": "English"}, {"code": "fa", "name": "فارسی"}],
  "cities": [{"code": "calderis", "name": "کالدریس"}, {"code": "ostmarch", "name": "اوستمارش"}],
  "places": [{"code": "old_town", "name": "شهر قدیم"}, {"code": "harbour", "name": "بندر"}],
  "server_time": "2026-09-26T10:00:00Z",
  "realtime": true
}
```

Names are in the player's language. `places` are the places of the player's
current city. `realtime` says whether section 5 is available.

---

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
{"token": "eyJhbGciOi…", "expires_at": "2026-09-26T10:15:00Z", "channel": "city:calderis"}
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
| 403 | `forbidden_channel` | not the player's city channel |
| 404 | `not_found` | |
| 409 | `relink` | the device's bot is gone; link again |
| 429 | `rate_limited` | too many sign-ins, link codes or commands |
| 503 | `realtime_unavailable` | realtime is not configured |
| 504 | `timeout` | the command was not answered in time (retry with the same idempotency key) |
| 500 | `internal` | |

A command the game **refuses** (not enough energy, travelling, not enough
money…) is not an HTTP error: it is `200` with `screen: "error"` and the
refusal in `text`, as in the bot.

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
realtime_token_ttl, group_commands, mini_app_url) and `realtime.*` (api_url,
publish_timeout).

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

**Migration**: `0033_client_devices` (tables `client_devices`,
`client_refresh_tokens`).

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
