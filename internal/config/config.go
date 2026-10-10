// Package config is the single place every operational value in this project
// is declared.
//
// # Why it exists
//
// Timeouts, batch sizes, retry counts and TTLs used to live as Go constants
// spread across the packages that happened to need them. That has two costs
// that only show up in production. Changing one means a rebuild and a deploy,
// which is the wrong response to "the broker is slow tonight, widen the ack
// wait". And nobody can answer "what is this environment actually running"
// without reading nine files, because the values are not in one place and the
// binary does not report them.
//
// So the values move here: declared in configs/config.yml, overridable per
// process by environment variable, validated on load.
//
// # Precedence
//
// Environment beats file, file beats defaults. Defaults exist so that a key
// missing from the yaml is the value the code used to hardcode, never the zero
// value: a zero duration means "no timeout" to every Go standard library API
// that takes one, and a silently unlimited timeout is a production incident,
// not a mild misconfiguration.
//
// # Environment override naming
//
// Every field is reachable as:
//
//	TORN_<SECTION>_<FIELD>
//
// where SECTION is the yaml section name and FIELD is the yaml key, both
// upper-cased with their underscores kept. So gateway.poll_timeout is
// TORN_GATEWAY_POLL_TIMEOUT, worker.batch_size is TORN_WORKER_BATCH_SIZE, and
// nats.duplicate_window is TORN_NATS_DUPLICATE_WINDOW. Durations are written
// the way Go writes them (250ms, 30s, 2m, 24h); nats.backoff, being a list,
// is a comma-separated one (TORN_NATS_BACKOFF=1s,5s,15s,60s).
//
// A variable that is set but empty is an error rather than a silent fallback.
// Someone wrote it on purpose and deserves to be told it did nothing.
//
// # Strictness
//
// Unknown and misspelled keys are rejected by the decoder, not ignored. A
// config file that quietly drops the line it cannot read is the worst kind of
// config file: the operator believes the value they wrote is the value
// running, and it is not. The same applies to values: a malformed duration is
// an error naming the field and the text, never a zero.
//
// # Secrets
//
// No secret is read from the yaml, and no field here holds one. Bot tokens,
// database passwords and broker credentials come from the environment alone,
// per docs/adr/0002-secret-management.md. The yaml is meant to be committed;
// nothing in it should ever need to be.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	// The zone database is embedded so a player's clock time renders in
	// player.default_timezone even in a container image with no tzdata.
	_ "time/tzdata"

	"gopkg.in/yaml.v3"
)

// DefaultPath is where the committed configuration lives, relative to the
// repository root.
const DefaultPath = "configs/config.yml"

// maxTimeScale mirrors gametime.MaxScale (one game day per real second). It
// is restated rather than imported so this package stays free of the domain.
const maxTimeScale = 86_400

// Load failures.
var (
	// ErrNoPath means Load was called without a file to read.
	ErrNoPath = errors.New("config: a configuration file path is required")

	// ErrMultipleDocuments means the file holds more than one yaml document.
	// Only the first would ever be read, so the rest would be invisible.
	ErrMultipleDocuments = errors.New("config: the file must hold exactly one yaml document")

	// ErrInvalidValue means a value could not be read as the type its field
	// requires. It always names the field and the text that failed.
	ErrInvalidValue = errors.New("config: invalid value")
)

// Validation failures. Each is wrapped with the offending field's name, so
// errors.Is answers "what kind of mistake" and the message answers "where".
var (
	// ErrNotPositive rejects any duration or limit at or below zero. Zero is
	// never the value somebody meant: for a timeout it means "wait forever",
	// for a batch size it means "do nothing".
	ErrNotPositive = errors.New("config: value must be greater than zero")

	// ErrEmpty rejects a string setting with nothing in it.
	ErrEmpty = errors.New("config: value must not be empty")

	// ErrEmptyList rejects a list setting with no entries.
	ErrEmptyList = errors.New("config: list must have at least one entry")

	// ErrNotIncreasing rejects a backoff schedule that does not grow. A flat
	// or shrinking schedule retries a struggling dependency harder the longer
	// it struggles.
	ErrNotIncreasing = errors.New("config: values must be strictly increasing")

	// ErrRenewDivisorTooSmall rejects a lease renewed at or after its own TTL,
	// which is not renewing: the lease expires in the gap and the bot flaps
	// between gateway instances.
	ErrRenewDivisorTooSmall = errors.New("config: lease.renew_divisor must be at least 2")

	// ErrRequestTimeoutTooLong rejects the inversion that silently kills long
	// polling. The HTTP client used for getUpdates must be allowed to wait
	// longer than the poll it is making; if the ordinary request timeout is
	// the larger of the two, every poll is aborted just before Telegram would
	// have answered and the bot stops receiving updates while every metric
	// says the requests "worked".
	ErrRequestTimeoutTooLong = errors.New("config: telegram.request_timeout must be shorter than telegram.max_poll_timeout + telegram.poll_timeout_grace")

	// ErrPollTimeoutTooLong rejects a long-poll window the Bot API client
	// will refuse, which would take every bot off the air at startup.
	ErrPollTimeoutTooLong = errors.New("config: gateway.poll_timeout must not exceed telegram.max_poll_timeout")

	// ErrIdempotencyTTLTooShort rejects an idempotency key that expires before
	// the broker has finished redelivering. A redelivery arriving after the
	// key is gone is executed a second time, and the player is charged twice.
	ErrIdempotencyTTLTooShort = errors.New("config: game.idempotency_ttl must outlast the nats redelivery schedule")

	// ErrCompanyNameBounds rejects company name bounds that cross, or a
	// longest name a typed answer (input.max_length) cannot carry.
	ErrCompanyNameBounds = errors.New("config: company name bounds must be ordered and fit input.max_length")

	// ErrClaimTimeoutTooShort rejects a claim lease that can expire while the
	// batch holding it is still allowed to run. The reaper would then hand a
	// row a live scheduler is still dispatching to another tick, and the
	// action would be published twice for no reason but a mis-set number.
	ErrClaimTimeoutTooShort = errors.New("config: scheduler.claim_timeout must exceed scheduler.shutdown_timeout")

	// ErrBankLimitsInverted rejects a bank minimum above the maximum, which
	// would refuse every amount.
	ErrBankLimitsInverted = errors.New("config: economy.bank_min_amount must not exceed economy.bank_max_amount")

	// ErrSendBudgetTooLong rejects a notice delivery allowed to outlast the
	// ack wait. The broker would hand the still-running event to a second
	// delivery, and the player would be told twice.
	ErrSendBudgetTooLong = errors.New("config: notifier.send_budget must be shorter than nats.ack_wait")

	// ErrReceiptMarginTooLong rejects a receipt margin that leaves the
	// gateway no time to send: every notice would arrive already expired.
	ErrReceiptMarginTooLong = errors.New("config: notifier.receipt_margin must be shorter than notifier.send_budget")

	// ErrInvalidTimeScale means game.time_scale is outside 1..86400: one game
	// day per real second is the most the game clock can mean.
	ErrInvalidTimeScale = errors.New("config: game.time_scale must be between 1 and 86400")

	// ErrBPSTooLarge means a basis-point value is above 10000 (100%).
	ErrBPSTooLarge = errors.New("config: a basis-point value must not exceed 10000")

	// ErrUnknownTimezone means player.default_timezone is not an IANA zone
	// name the embedded zone database knows.
	ErrUnknownTimezone = errors.New("config: player.default_timezone is not a known time zone")
)

// Config is every operational value, grouped the way configs/config.yml is.
//
// Durations are real time.Durations here, not strings: decoding and
// validation happen once, in Load, so no caller has to parse or re-check
// anything. A Config handed out by Load is complete and valid.
type Config struct {
	Gateway    Gateway
	Lease      Lease
	RateLimit  RateLimit
	Telegram   Telegram
	Groups     Groups
	Menu       Menu
	Dedup      Dedup
	NATS       NATS
	Worker     Worker
	Scheduler  Scheduler
	Notifier   Notifier
	Game       Game
	Travel     Travel
	Player     Player
	Economy    Economy
	Governance Governance
	Crime      Crime
	Trade      Trade
	Company    Company
	Military   Military
	Diplomacy  Diplomacy
	War        War
	Missions   Missions
	Factions   Factions
	AntiCheat  AntiCheat
	Input      Input
	Announce   Announce

	// Stage F (docs/adr/0024-property-and-politics.md).
	Legislature Legislature
	// Currency is a settlement's own money (ADR 0033 section 6).
	Currency Currency
	// Labor is the labour market (ADR 0037).
	Labor        Labor
	Education    Education
	Training     Training
	City         City
	Property     Property
	Achievements Achievements

	// Postgres is every service's connection pool (the deadlock fix: a
	// bounded pool, and no transaction left idle holding its locks).
	Postgres Postgres

	// The operators' web panel (cmd/panel).
	Panel Panel

	// The game client API (cmd/clientapi) and the realtime server it and
	// the notifier publish through; see client.go.
	Client   Client
	Realtime Realtime
	// StateSync is client state sync (docs/adr/0034).
	StateSync StateSync

	// Notifications is the inbox badge (migrations/0037_notification_
	// inbox): which of cmd/notifier's own tuning is not content (delivery
	// mode per kind lives in configs/notifications/delivery.yml instead;
	// see internal/workers/notification.DeliveryModes).
	Notifications Notifications

	// WorldGen is the world generator's tuning (internal/domain/worldgen.
	// Params): how many cells, how much land, how the noise and climate
	// simulations are shaped. What biomes and resources exist, and the
	// geology that places them, is CONTENT (configs/content/world.yml,
	// docs/adr/0004-content-system.md's distinction); these are the
	// coefficients that content, not a code change, should be able to move.
	WorldGen WorldGen

	// Settlement is the tuning of group founding (docs/adr/0028-world-and-
	// settlements.md): the starting-spot algorithm's search bounds and the
	// beginner-protection window. What a founded settlement's buildings
	// cost and unlock is CONTENT (configs/content/buildings.yml, a later
	// phase); these are the coefficients the founding algorithm itself
	// runs on.
	Settlement Settlement

	// Growth is the switch and tuning of organic settlement growth (docs/adr/
	// 0044-organic-growth-alliances-countries.md, phases G0 and G1): the
	// capability computation that replaces the tier label, run beside it.
	Growth Growth

	// Bag, Merchant and Premium are the tuning of ADR 0046: what a player can
	// carry and how a bag wears, the village shop's daily delivery and prices,
	// and the constant a village currency is quoted in Nil by.
	Bag      Bag
	Merchant Merchant
	Premium  Premium
}

// Bag is what a player can carry (docs/adr/0046 section 4; internal/domain/
// carry). Space is in «جا», loads in kilograms.
type Bag struct {
	CarryBase      int64 // bag.carry_base: the space hands and pockets give
	BaseComfortKG  int64 // bag.base_comfort_kg: the comfortable load with no bag
	BaseHardKG     int64 // bag.base_hard_kg: the most a player with no bag can carry
	FullShareBPS   int64 // bag.full_share_bps: how full counts as "carried half full" for wear
	TornSpaceBPS   int64 // bag.torn_space_bps: the share of its space a torn bag still gives
	RepairShareBPS int64 // bag.repair_share_bps: a full repair costs this share of the price
	WearPerDay     int64 // bag.wear_per_day: points a bag loses per game day carried half full
}

// Merchant is the village shop's tuning (docs/adr/0046 section 5.2; internal/
// domain/vshop). What it sells, at what reference price and how fast is
// CONTENT (configs/content/village_shop.yml); these are the coefficients that
// content is run with.
type Merchant struct {
	RestockHour      int64   // merchant.restock_hour: the game hour of the morning delivery
	MarkupMinBPS     int64   // merchant.markup_min_bps: no price under this share of the reference
	MarkupMaxBPS     int64   // merchant.markup_max_bps: no price over this share of the reference
	StockDays        int64   // merchant.stock_days: a shelf holds this many days of delivery
	FoodShareBPS     int64   // merchant.food_share_bps: the share of a resident's daily food need the shop covers
	OtherShareBPS    int64   // merchant.other_share_bps: the same for every other line
	PlayerDayFood    int64   // merchant.player_day_food: a player's daily cap on a food line, in multiples of a head's daily need
	PlayerDayOther   int64   // merchant.player_day_other: a player's daily cap on any other line, in units
	SupplyValueFood  int64   // merchant.supply_value_per_resident_day: the most goods value a resident's day draws, reference minor units
	BuildingBoostBPS int64   // merchant.building_boost_bps: the delivery of a village with a shop building, over the stall's
	CapPresets       []int64 // merchant.cap_presets: the price caps the head's buttons offer
	BuyPresets       []int64 // merchant.buy_presets: the quantities the buy buttons offer
	TaxDefaultBPS    int64   // merchant.tax_default_bps: a village's sales tax until its head sets one
	TaxMaxBPS        int64   // merchant.tax_max_bps: the most the head may set
	TaxPresets       []int64 // merchant.tax_presets: the taxes the head's buttons offer
	OutputDays       int64   // merchant.output_days: the game days the money panel reads a village's output over
}

// Premium is the display constant of the Nil quote (docs/adr/0046 section 7.4).
// Nothing converts at it: it only says how many neutral-currency units one Nil
// stands for.
type Premium struct {
	NilUnitSup  int64   // premium.nil_unit_sup
	NilExamples []int64 // premium.nil_examples: the amounts the money panel shows in Nil
}

// The values of growth.capabilities (ADR 0044 section 11, flag
// growth.capabilities).
const (
	// GrowthOff computes nothing: the tier is the only answer. The default.
	GrowthOff = "off"
	// GrowthShadow computes the capability answer beside every tier gate and
	// meters each disagreement; the tier stays authoritative.
	GrowthShadow = "shadow"
	// GrowthAuthoritative lets the capability answer decide. Reserved for
	// phase G4; G1 ships the value so the switch-over is a config change.
	GrowthAuthoritative = "authoritative"
)

// Growth is organic settlement growth's tuning.
type Growth struct {
	// Capabilities is growth.capabilities: off, shadow or authoritative.
	Capabilities string
	// CacheTTL is how long, REAL time, a settlement's computed capabilities
	// are reused by the gates that read them outside a transaction
	// (growth.cache_ttl; ADR 0044 section 5.1 "pure, cached").
	CacheTTL time.Duration
	// RuinedBPS is the damage, basis points, from which a building no longer
	// stands for the capability computation (growth.ruined_bps; ADR 0044
	// section 5.1 "condition"). 10000 is a ruin.
	RuinedBPS int
	// FlushInterval is how often each process writes the disagreements it
	// metered to growth_disagreements (growth.flush_interval).
	FlushInterval time.Duration
}

// Postgres bounds every service's connection pool.
type Postgres struct {
	// MaxConns is the most connections one service's pool opens; a command
	// holds exactly one (its unit of work), so it bounds the commands run
	// at once, not a guess of pgx's.
	MaxConns int // postgres.max_conns
	// IdleInTransactionTimeout makes the server end a session left idle
	// inside a transaction this long, releasing its locks, so a stuck
	// process can never freeze everyone else.
	IdleInTransactionTimeout time.Duration // postgres.idle_in_transaction_timeout
}

// Gateway paces the Telegram polling loop and its shutdown.
type Gateway struct {
	PollTimeout      time.Duration // gateway.poll_timeout
	PollErrorBackoff time.Duration // gateway.poll_error_backoff
	ShutdownTimeout  time.Duration // gateway.shutdown_timeout
	SendAttempts     int           // gateway.send_attempts
	// RedirectCooldown is how often one Telegram user is answered with the
	// "play on the web" redirect (switch.telegram_play off) rather than
	// silently dropped: a player pressing a stale button many times gets one
	// message per cooldown, not a flood.
	RedirectCooldown time.Duration // gateway.redirect_cooldown
	// WebAppPrivateCooldown is how often one player may press «send it to my
	// private chat» under a group screen's web-app button.
	WebAppPrivateCooldown time.Duration // gateway.webapp_private_cooldown
}

// Lease governs the exclusive right to poll one bot.
type Lease struct {
	TTL            time.Duration // lease.ttl
	RenewDivisor   int           // lease.renew_divisor
	ReleaseTimeout time.Duration // lease.release_timeout
}

// RenewEvery is the renewal interval implied by the TTL and the divisor.
//
// It is derived rather than configured so the two can never be set to
// contradict one another, which is the mistake that lets a lease expire
// between renewals.
func (l Lease) RenewEvery() time.Duration {
	return l.TTL / time.Duration(l.RenewDivisor)
}

// RateLimit is the per-bot outbound allowance.
type RateLimit struct {
	DefaultRate  int // ratelimit.default_rate
	DefaultBurst int // ratelimit.default_burst
}

// Telegram bounds Bot API calls.
type Telegram struct {
	RequestTimeout   time.Duration // telegram.request_timeout
	MaxPollTimeout   time.Duration // telegram.max_poll_timeout
	PollTimeoutGrace time.Duration // telegram.poll_timeout_grace
	DefaultFloodWait time.Duration // telegram.default_flood_wait
}

// PollHTTPTimeout is the transport timeout the long-polling HTTP client must
// be given.
//
// It exists so no caller has to remember to add the grace: handing the
// polling client RequestTimeout instead is the bug ErrRequestTimeoutTooLong
// describes, and the surest way to prevent it is to make the correct value
// the easy one to reach for.
func (t Telegram) PollHTTPTimeout() time.Duration {
	return t.MaxPollTimeout + t.PollTimeoutGrace
}

// Groups tunes how the game is delivered in Telegram groups, where a private
// screen must reach only the player who asked for it (internal/gateway/groups).
type Groups struct {
	CallbackAlertMaxRunes int // groups.callback_alert_max_runes
	// DeepLinkTTL is how long a deep link to the private chat that carries
	// more than a start parameter holds (a payee and an amount that do not
	// fit Telegram's 64 characters) stays valid; groups.deep_link_ttl.
	DeepLinkTTL time.Duration
}

// Menu is the bot's command menu (the "/" button). It is registered for
// private chats only; groups are left without one.
type Menu struct {
	// Commands are the slash-commands offered, in order. Each needs a
	// description under command_menu.<command> in every locale.
	Commands []string // menu.commands
}

// Dedup is how long a Telegram update id is remembered.
type Dedup struct {
	TTL time.Duration // dedup.ttl
}

// NATS is stream retention and redelivery.
type NATS struct {
	CommandMaxAge   time.Duration   // nats.command_max_age
	EventMaxAge     time.Duration   // nats.event_max_age
	DuplicateWindow time.Duration   // nats.duplicate_window
	AckWait         time.Duration   // nats.ack_wait
	MaxDeliver      int             // nats.max_deliver
	NakDelay        time.Duration   // nats.nak_delay
	Backoff         []time.Duration // nats.backoff
}

// RedeliveryWindow is the longest a message can stay in the retry loop: every
// backoff interval, plus the last one reused for any deliveries the schedule
// does not name.
func (n NATS) RedeliveryWindow() time.Duration {
	if len(n.Backoff) == 0 {
		return 0
	}

	var total time.Duration
	last := n.Backoff[len(n.Backoff)-1]
	for i := 1; i < n.MaxDeliver; i++ {
		if i-1 < len(n.Backoff) {
			total += n.Backoff[i-1]
			continue
		}
		total += last
	}
	return total
}

// Worker drains the transactional outbox.
type Worker struct {
	PollInterval    time.Duration // worker.poll_interval
	BatchSize       int           // worker.batch_size
	ShutdownTimeout time.Duration // worker.shutdown_timeout
	NoisyAttempts   int           // worker.noisy_attempts
}

// Scheduler turns the durable schedule into published commands.
//
// It is a separate section from Worker even though the four values line up,
// because the two processes are paced by different things. The outbox worker
// is bounded by how fast a committed event should reach the broker; the
// scheduler is bounded by how late a player may notice their travel landing.
// One number serving both would be tuned for whichever incident happened last.
type Scheduler struct {
	TickInterval    time.Duration // scheduler.tick_interval
	BatchSize       int           // scheduler.batch_size
	ShutdownTimeout time.Duration // scheduler.shutdown_timeout
	NoisyAttempts   int           // scheduler.noisy_attempts

	// ClaimTimeout is the lease on a claimed action. A row still running
	// this long after its claim is presumed to belong to a scheduler that
	// died, and is returned to scheduled so another tick can dispatch it.
	ClaimTimeout time.Duration // scheduler.claim_timeout
}

// Notifier paces the delivery of notifications (cmd/notifier).
type Notifier struct {
	SendBudget      time.Duration // notifier.send_budget
	ReceiptMargin   time.Duration // notifier.receipt_margin
	MaxAge          time.Duration // notifier.max_age
	ShutdownTimeout time.Duration // notifier.shutdown_timeout
}

// Game is the command-side service.
type Game struct {
	ShutdownTimeout time.Duration // game.shutdown_timeout
	IdempotencyTTL  time.Duration // game.idempotency_ttl

	// ContentReloadInterval is how often the service checks whether a newer
	// content version was loaded, and swaps it in without a restart.
	ContentReloadInterval time.Duration // game.content_reload_interval

	// TimeScale is the game clock (docs/adr/0018-game-clock.md): how many
	// seconds of game time pass in one real second. Every gameplay duration
	// content writes — a journey, a course, a shift, a promotion's time in
	// tier, a fatigue window — is game time, and the player waits it
	// divided by this. At 1 (the default since 2026-10-05) game time IS real time;
	// at 60 a 24h course would be 24 real minutes.
	//
	// The legacy key travel.time_scale (and TORN_TRAVEL_TIME_SCALE) still
	// fills it, from before the clock was the whole game's; game.time_scale
	// wins where both are set.
	TimeScale int // game.time_scale

	// ClockEpoch is the real instant (RFC 3339) at which game day 0 began at
	// 00:00: with the time scale it makes the game's day and hour (gametime.
	// Clock), so "once per game day at 06:00" means the same on every replica.
	// Changing it, or the scale, renumbers the days.
	ClockEpoch string // game.clock_epoch

	// ClockLegacyScale and ClockCutover are the cut-over from the compressed clock
	// (a game day was 24 real minutes at 60) to real time: until ClockCutover (a
	// UTC midnight, RFC 3339) days are counted by the legacy rule - ClockEpoch
	// and this scale - and from it a game day is a real day of the settlement's own zone, the day number
	// continuing upward. Empty ClockCutover: no cut-over. Set once, never moved.
	ClockLegacyScale int    // game.clock_legacy_scale
	ClockCutover     string // game.clock_cutover

	// TravelTimeScale is the scale of a journey between places (game time to the
	// wait): a trip is a thing a player waits through in one sitting, so it stays
	// compressed while the rest of the clock is real. 1 makes a 3-hour bus ride take
	// 3 real hours.
	TravelTimeScale int // game.travel_time_scale

	// CommandTimeout is the most one command may run: its context is
	// cancelled after it, the transaction rolled back and the message
	// redelivered, rather than a stuck handler holding its connection and
	// its locks forever.
	CommandTimeout time.Duration // game.command_timeout
}

// Travel is the tuning of a journey that is not content: what arriving pays.
// How game time maps to the wall clock is the game clock, Game.TimeScale.
//
// The RULES — how a distance and a transport mode become a duration and a
// fare — live in internal/domain/travel. The modes themselves (speed, fare,
// energy, which cities serve them) are content, in
// configs/content/transport.yml. A public mode's fare multiplier is a city
// policy (city.transit_fare), read only through the policy resolver.
type Travel struct {
	ArrivalXP int // travel.arrival_xp

	// CityLocations are "code=lat:lon" entries: where a content city (Support)
	// stands on the generated world, in degrees. A founded settlement's place
	// is its own world cell; a content city has none, so its spot is tuning
	// (ADR 0034). Journeys between places without a content route are priced
	// by the great-circle distance between these spots.
	CityLocations []string // travel.city_locations
	// WorldReach are "mode=km" entries: the transport modes that serve a
	// world-derived journey and the longest one each will make. A mode not
	// listed serves content routes only.
	WorldReach []string // travel.world_reach
}

// CityLocation is a content city's spot on the world.
type CityLocation struct{ LatDeg, LonDeg float64 }

// CityLocationMap parses travel.city_locations ("code=lat:lon") by city code.
func (t Travel) CityLocationMap() (map[string]CityLocation, error) {
	out := make(map[string]CityLocation, len(t.CityLocations))
	for i, entry := range t.CityLocations {
		code, val, ok := strings.Cut(entry, "=")
		code = strings.TrimSpace(code)
		latText, lonText, ok2 := strings.Cut(val, ":")
		lat, err1 := strconv.ParseFloat(strings.TrimSpace(latText), 64)
		lon, err2 := strconv.ParseFloat(strings.TrimSpace(lonText), 64)
		if !ok || !ok2 || code == "" || err1 != nil || err2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
			return nil, fmt.Errorf("%w: travel.city_locations[%d] %q is not code=lat:lon (degrees)", ErrInvalidValue, i, entry)
		}
		if _, dup := out[code]; dup {
			return nil, fmt.Errorf("%w: travel.city_locations repeats %q", ErrInvalidValue, code)
		}
		out[code] = CityLocation{LatDeg: lat, LonDeg: lon}
	}
	return out, nil
}

// WorldReachMap parses travel.world_reach ("mode=km") by mode code.
func (t Travel) WorldReachMap() (map[string]int, error) {
	out := make(map[string]int, len(t.WorldReach))
	for i, entry := range t.WorldReach {
		code, val, ok := strings.Cut(entry, "=")
		code = strings.TrimSpace(code)
		km, err := strconv.Atoi(strings.TrimSpace(val))
		if !ok || code == "" || err != nil || km < 1 || km > 100_000 {
			return nil, fmt.Errorf("%w: travel.world_reach[%d] %q is not mode=km (1..100000)", ErrInvalidValue, i, entry)
		}
		if _, dup := out[code]; dup {
			return nil, fmt.Errorf("%w: travel.world_reach repeats %q", ErrInvalidValue, code)
		}
		out[code] = km
	}
	return out, nil
}

// Player holds player-facing defaults.
type Player struct {
	DefaultLanguage string // player.default_language

	// DefaultTimezone is the IANA zone a clock time is shown in ("arrives at
	// 14:32") for a player who has not chosen one. Never UTC by accident:
	// the game's players live somewhere.
	DefaultTimezone string // player.default_timezone
}

// Location returns the zone DefaultTimezone names. Validate has already
// refused a name the zone database does not know, so this cannot fail on a
// loaded Config; on an unvalidated one it falls back to UTC.
func (p Player) Location() *time.Location {
	loc, err := time.LoadLocation(p.DefaultTimezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Economy is the tuning of the money core (docs/adr/0009-economic-control.md).
// Every amount is int64 minor units, the same as a ledger entry, so no value
// here can pass through a float on its way into the ledger.
type Economy struct {
	// StartingCash is what every player receives once, from system_source,
	// through a reward grant with source starting_grant.
	StartingCash int64 // economy.starting_cash

	// BankMinAmount and BankMaxAmount bound the amount of ONE deposit,
	// withdrawal or payment between players. Limits, not policy: what the
	// bank charges is a city's lever, read through the policy resolver.
	BankMinAmount int64 // economy.bank_min_amount
	BankMaxAmount int64 // economy.bank_max_amount

	// BankQuickAmounts are the round amounts the bank offers as buttons,
	// smallest first. A screen shows the ones the player can move, plus
	// "all of it" and a typed amount.
	BankQuickAmounts []int64 // economy.bank_quick_amounts
}

// Input tunes free-text input: a button that asks the player to type a value
// (internal/gateway/input).
type Input struct {
	// TTL is how long a prompt waits for its answer.
	TTL time.Duration // input.ttl
	// Cooldown is the least time between two prompts to one player in one
	// chat, so a button pressed repeatedly cannot flood the chat.
	Cooldown time.Duration // input.cooldown
	// MaxLength caps the typed value, in characters.
	MaxLength int // input.max_length
}

// Announce tunes the public lines posted in a city's group (a player
// arrived, a player was jailed).
type Announce struct {
	// Window and MaxPerWindow bound how many lines one group receives: at
	// most MaxPerWindow in any Window; the rest are folded into a count on
	// the next line that goes out.
	Window       time.Duration // announce.window
	MaxPerWindow int           // announce.max_per_window

	// The village news (internal/workers/notification/village_news.go): a
	// burst of things finishing in one village is merged into one short
	// post. Nothing is posted before the oldest waiting item is
	// VillageMergeWindow old, and a village's group gets at most one post
	// per VillageMinGap; a background tick every VillageFlushInterval
	// looks for what is due.
	VillageMergeWindow   time.Duration // announce.village_merge_window
	VillageMinGap        time.Duration // announce.village_min_gap
	VillageFlushInterval time.Duration // announce.village_flush_interval
	// VillageLiteracyStep is how many percentage points literacy must climb
	// to be worth a post: teaching steps come every few minutes.
	VillageLiteracyStep int // announce.village_literacy_step_percent
}

// Notifications tunes the inbox badge (migrations/0037_notification_inbox)
// and the one urgent need alert this feature adds (hunger). Which
// notification KIND sends at once instead of joining the badge is content,
// not tuning — configs/notifications/delivery.yml — so an operator changes
// it without touching this file.
type Notifications struct {
	// InboxPageSize is how many items one /inbox category page shows.
	InboxPageSize int // notifications.inbox_page_size

	// EditThrottle is the least real time between two edits of one
	// player's badge message; an item that arrives sooner only updates the
	// stored count, and the next edit (or the player opening /inbox)
	// catches the message up.
	EditThrottle time.Duration // notifications.edit_throttle

	// ReminderDelay is how long a badge may sit unread before the 24h nudge
	// fires; ReminderCheckInterval is how often cmd/notifier looks for one
	// due. Both are REAL time — a policy's notice and cooldown, never game
	// time (see game.time_scale).
	ReminderDelay         time.Duration // notifications.reminder_delay
	ReminderCheckInterval time.Duration // notifications.reminder_check_interval

	// Retention is how long a READ item is kept before PruneInterval's
	// sweep removes it.
	Retention     time.Duration // notifications.retention
	PruneInterval time.Duration // notifications.prune_interval

	// HungerAlertCooldown is the least real time between two "you are
	// hungry" instant notices to the same player, so a hunger hovering at
	// the content-defined threshold (life.yml needs.high) cannot resend it
	// every time a command happens to catch the life up.
	HungerAlertCooldown time.Duration // notifications.hunger_alert_cooldown

	// VitalsMinInterval bounds how often cmd/notifier reads and publishes
	// one player's realtime vitals snapshot (cash, bank, energy, health,
	// xp, level, unread — api/client-api.md section 5): at most once per
	// this long, even when several events for the same player land inside
	// it. A player mid-crime-spree or travelling with a crew fires many
	// notices in a burst; without this, each one would cost a stats read
	// and two ledger reads for a number a client is about to overwrite
	// again a moment later.
	VitalsMinInterval time.Duration // notifications.vitals_min_interval
}

// WorldGen is the world generator's tuning
// (internal/domain/worldgen.Params). Field-for-field the same values, in
// the same units (Permille fields are parts-per-1000 ints, matching the
// domain type of the same name) — this struct exists only because
// internal/config stays free of any internal/domain import (no other
// section here imports one either), so the CLI/service that actually calls
// worldgen.Generate copies these into a worldgen.Params itself.
type WorldGen struct {
	CellCount              int     // worldgen.cell_count
	NeighborK              int     // worldgen.neighbor_k
	PlateCount             int     // worldgen.plate_count
	OceanicPlateFraction   int     // worldgen.oceanic_plate_fraction_permille
	LandFraction           int     // worldgen.land_fraction_permille
	NoiseOctaves           int     // worldgen.noise_octaves
	NoiseBaseFrequency     float64 // worldgen.noise_base_frequency
	NoisePersistence       int     // worldgen.noise_persistence_permille
	WarpAmplitude          float64 // worldgen.warp_amplitude
	WarpFrequency          float64 // worldgen.warp_frequency
	BoundaryInfluenceSteps int     // worldgen.boundary_influence_steps
	MoistureBands          int     // worldgen.moisture_bands
	RiverFlowThreshold     int     // worldgen.river_flow_threshold
	LakeMinDepth           int     // worldgen.lake_min_depth
	LakeMinAreaCells       int     // worldgen.lake_min_area_cells

	PlanetRadiusKm              float64 // worldgen.planet_radius_km
	ChunkBaseLOD                int     // worldgen.chunk_base_lod
	ChunkTileEdge               int     // worldgen.chunk_tile_edge
	ChunkDetailFrequency        float64 // worldgen.chunk_detail_frequency
	ChunkDetailAmplitude        int     // worldgen.chunk_detail_amplitude
	ChunkStreamFrequency        float64 // worldgen.chunk_stream_frequency
	ChunkStreamAmplitude        int     // worldgen.chunk_stream_amplitude
	ChunkDepositTilesPerDeposit int     // worldgen.chunk_deposit_tiles_per_deposit
}

// Settlement is the tuning of a Telegram group founding a village
// (docs/adr/0028-world-and-settlements.md section 3.2): the starting-spot
// algorithm's search bounds and the beginner-protection window a founded
// settlement enjoys.
type Settlement struct {
	// ProtectionWindow is how long, REAL time, a freshly founded settlement
	// cannot be claimed, contested or struck (section 3.1). It ends early
	// the moment the settlement's own side takes an aggressive action; that
	// rule is enforced where wars are declared, not here.
	ProtectionWindow time.Duration // settlement.protection_window

	// ResidenceCooldown is how long, REAL time, a player who moved their
	// home (joined a village, went back) must wait before the next
	// settlement.join / settlement.leave. It runs from players.residence_since,
	// which every change of residence stamps.
	ResidenceCooldown time.Duration // settlement.residence_cooldown
	// TimezoneCooldown is how long, REAL time, a charter must wait before it changes
	// the settlement's time zone again: a zone change moves the settlement's day
	// number by at most a day, and this keeps it from being played with.
	TimezoneCooldown time.Duration // settlement.timezone_cooldown

	// HomeCityCode is the content city a player returns to when they leave
	// a village (settlement.leave): the neutral city, "support".
	HomeCityCode string // settlement.home_city_code

	// PropertyHubMinStage is the smallest settlement stage (village, town,
	// city) from which the Economy hub lists «ملک»: below it lots and houses
	// live in the village's own menu (web-structure 5.3), not the property
	// market (settlement.property_hub_min_stage).
	PropertyHubMinStage string // settlement.property_hub_min_stage

	// MinSpawnDistanceKm is the least great-circle distance a candidate
	// spot must keep from every existing settlement's centre — more than
	// twice a village's territory radius, so a spawn can never land inside
	// another settlement's automatic claim (section 3.2 step 2).
	MinSpawnDistanceKm float64 // settlement.min_spawn_distance_km

	// ThreatRadiusKm is how far a stronger neighbour's weight still steers
	// a new spawn away, as a preference rather than a wall (section 3.2
	// step 3).
	ThreatRadiusKm float64 // settlement.threat_radius_km

	// SearchMaxCells bounds the outward walk from a lattice point's nearest
	// cell: a search that finds no eligible cell within this many visited
	// cells gives up on this lattice point and the caller advances to the
	// next one (section 3.2 step 4), rather than walking the whole planet.
	SearchMaxCells int // settlement.search_max_cells

	// SearchMaxAttempts bounds how many lattice points (section 3.2 step 1)
	// one founding may try before giving up outright — only reachable if
	// the world is, implausibly, entirely ineligible.
	SearchMaxAttempts int // settlement.search_max_attempts

	// SpawnCircleRadiusKm, SpawnCircleCapacity, SpawnCircleFillBandKm and SpawnCircleMaxAdvance place new
	// foundings close together (ADR 0028 section 3.2, amendment 2026-10-09): the radius of one circle, how many
	// settlements it takes before the next opens, the distance band the circle fills outward in, and how many
	// circles one founding may skip.
	SpawnCircleRadiusKm   float64 // settlement.spawn_circle_radius_km
	SpawnCircleCapacity   int     // settlement.spawn_circle_capacity
	SpawnCircleFillBandKm float64 // settlement.spawn_circle_fill_band_km
	SpawnCircleMaxAdvance int     // settlement.spawn_circle_max_advance

	// The lot a resident builds (ADR 0045 phase B1, section 3): BuildingAreaPerCell is the floor area a footprint
	// cell gives per storey; the storey_* fields the materials and the work of a storey per footprint cell (timber,
	// stone from BuildingStoreyStoneFrom up) and the support table "knowledge=storeys"; BuildingSalvageBPS the
	// share of a removed module's materials that comes back; UseChangeFeeBPS the fee of a change of use (basis
	// points of the building's assessed value, at most 1000); BuildingLookRerolls how often the generated look is
	// re-rolled to differ from a neighbour; BuildingTemplatesMax how many saved layouts a player keeps.
	BuildingAreaPerCell         int      // settlement.building_area_per_cell
	BuildingStoreyTimberPerCell int      // settlement.building_storey_timber_per_cell
	BuildingStoreyStonePerCell  int      // settlement.building_storey_stone_per_cell
	BuildingStoreyStoneFrom     int      // settlement.building_storey_stone_from
	BuildingStoreyShiftsPerCell int      // settlement.building_storey_shifts_per_cell
	BuildingStoreyKnowledge     []string // settlement.building_storey_knowledge
	BuildingSalvageBPS          int      // settlement.building_salvage_bps
	UseChangeFeeBPS             int      // settlement.use_change_fee_bps
	BuildingLookRerolls         int      // settlement.building_look_rerolls
	BuildingTemplatesMax        int      // settlement.building_templates_max

	// ExcludedBiomes are biome codes a village may never be placed on: only
	// truly uninhabitable land (polar_ice). Harsh but livable biomes are
	// penalised through BiomePenalties instead (section 3.2 step 3).
	ExcludedBiomes []string // settlement.excluded_biomes

	// MaxAbsLatitudeDeg is the highest |latitude| a spawn may have; the
	// spawn lattice is squeezed into that band.
	MaxAbsLatitudeDeg float64 // settlement.max_abs_latitude_deg

	// BiomePenalties are "biome_code=points" entries: the score a spawn
	// loses for standing on that biome (desert, tundra, ...).
	BiomePenalties []string // settlement.biome_penalties

	// VillageGridLots is a village's local placement grid, per side, in
	// lots (ADR 0028 section 4: 5x5). Town and city sizes are a later
	// phase's own tuning once building placement (W5) ships.
	VillageGridLots int // settlement.village_grid_lots

	// MinBuildableLotShareBps is the least share of a village's lot grid
	// (basis points, 10000 = every lot) that must be buildable land - not
	// lake, sea or river - for a spot to be chosen for a village. Judged on
	// the very grid the village would get, by the placement rules' own
	// sampler.
	MinBuildableLotShareBps int // settlement.min_buildable_lot_share_bps
	// GridShiftMaxLots is how many lots, in each direction, the grid may
	// slide from its cell's centre to find a placement that meets the share.
	GridShiftMaxLots int // settlement.grid_shift_max_lots

	// AutoRoadCost is what the treasury pays for each lot of road the game
	// lays by itself to connect a new building: a small fee (a road laid by
	// hand costs 50), so the game never lays roads for nothing yet a road is
	// far cheaper than the building it serves.
	AutoRoadCost int64 // settlement.auto_road_cost
	// Lot access (docs/adr/0043): a lot is only sold when a road can reach it.
	// LotAccessCrossingCost is the price of one lot of culvert or footbridge
	// over water (a road lot costs AutoRoadCost); LotAccessMaxCrossing is the
	// most water lots one route may cross. A village whose grid side is at least
	// StreetPlanMinGrid keeps a street plan, a street every StreetPitch lots,
	// as reserved right-of-way.
	LotAccessCrossingCost int64 // settlement.lot_access_crossing_cost
	LotAccessMaxCrossing  int   // settlement.lot_access_max_crossing
	StreetPitch           int   // settlement.street_pitch
	StreetPlanMinGrid     int   // settlement.street_plan_min_grid
	// How many lots deep a road opens the land along it (the frontage band, ADR 0044 5.5/5.6).
	RoadFrontageDepthLots int // settlement.road_frontage_depth_lots
	// The longest single road plan, in lots (a TECHNICAL bound on one plan's size, about 45 km; the real limits are terrain, other settlements' land and the cost of building it when lots are bought).
	RoadPlanMaxLots int // settlement.road_plan_max_lots
	// The most unsold lots a settlement's roads may hold open at once (a TECHNICAL bound against claiming a whole continent with free plans; selling lots or cancelling an unbought plan frees room).
	RoadOpenLotsMax int // settlement.road_open_lots_max
	// How many tiles round another settlement's first grid are closed to our roads (its land; ADR 0044 5.5 claim rule).
	RoadForeignBufferTiles int // settlement.road_foreign_buffer_tiles
	// A lot whose ground differs from a side neighbour by more than this many metres cannot be built on (shown as steep).
	RoadSteepSlopeM int // settlement.road_steep_slope_m
	// How many tiles the lot router may stray from the world router's line.
	RoadCorridorRingTiles int // settlement.road_corridor_ring_tiles
	// Price of a lot of the researched track class against a footpath lot, in basis points of auto_road_cost.
	RoadTrackCostBPS int // settlement.road_track_cost_bps

	// The founding form: a group's «ساخت روستا» opens a draft the founder
	// completes in the game client (name, currency, emblem) before the
	// village exists.

	// FoundingDraftTTL is how long, REAL time, an open founding draft waits
	// for its founder; after it nothing is founded and the group may ask again.
	FoundingDraftTTL time.Duration // settlement.founding_draft_ttl
	// FoundingNameMin and FoundingNameMax bound a village's name, in
	// characters; FoundingMottoMax its motto.
	FoundingNameMin  int // settlement.founding_name_min
	FoundingNameMax  int // settlement.founding_name_max
	FoundingMottoMax int // settlement.founding_motto_max
	// FoundingCurrencyNameMin and FoundingCurrencyNameMax bound the name of
	// the currency a village reserves for the day it becomes a country;
	// FoundingCurrencyCodeLen is the exact length of its code (letters A-Z)
	// and FoundingCurrencySymbolMax the longest symbol, in characters.
	FoundingCurrencyNameMin   int // settlement.founding_currency_name_min
	FoundingCurrencyNameMax   int // settlement.founding_currency_name_max
	FoundingCurrencyCodeLen   int // settlement.founding_currency_code_len
	FoundingCurrencySymbolMax int // settlement.founding_currency_symbol_max

	// K2/W5 (docs/adr/0031-knowledge-and-village-progression.md): the
	// literacy diffusion tick's own period and rate, and the scarcity
	// price curve's shared knobs. base_cost per item is content
	// (settlement_knowledge.yml); k/floor/cap here are the curve's own
	// coefficients, one set for the whole game (section 10 point 3).

	// TeachPeriod is how often a settlement's own literacy tick runs, GAME
	// time (section 4.4).
	TeachPeriod time.Duration // settlement.teach_period
	// TeachRateBPS is the diffusion formula's own teach_rate term, bps.
	TeachRateBPS int64 // settlement.teach_rate_bps
	// BaseSchoolCapacityBPS is school_capacity_factor while any complete
	// education-role building stands (v1's own simplified stand-in for a
	// real capacity-against-population ratio; see
	// internal/application/handlers/village_teach.go).
	BaseSchoolCapacityBPS int64 // settlement.base_school_capacity_bps

	// ScarcityKBPS, ScarcityFloorBPS and ScarcityCapBPS are
	// settlementknowledge.ScarcityPrice's own k/floor/cap, basis-point
	// multipliers of an item's base_cost (10000 = 1x).
	ScarcityKBPS     int64 // settlement.scarcity_k_bps
	ScarcityFloorBPS int64 // settlement.scarcity_floor_bps
	ScarcityCapBPS   int64 // settlement.scarcity_cap_bps
	// SellerBandBPS bounds a settlement-to-settlement sale's own deviation
	// from the reference price, either way (section 10 point 3's own
	// resolved open question).
	SellerBandBPS int64 // settlement.seller_band_bps

	// DemolitionSalvageBPS is the share of a demolished building's own
	// money cost credited back to the settlement's treasury (ADR 0028
	// section 6.2), basis points; default 2000 (20%).
	DemolitionSalvageBPS int64 // settlement.demolition_salvage_bps

	// MaterialMarkupBPS is what a village pays Support for a material it buys
	// (ADR 0033 section 4.1): the component's reference price times this over
	// 10000, so 12000 is the reference price plus 20 %.
	MaterialMarkupBPS int64 // settlement.material_markup_bps
	// StockBaseCapacity is how many units of goods, in all, a village's
	// stock holds without a granary; every standing building's `storage`
	// (settlement_buildings.yml) adds to it.
	StockBaseCapacity int64 // settlement.stock_base_capacity
	// BuildHomesPerCrew is how many homes the settlement needs for each building
	// it may raise at once beyond the first (no tier decides the cap any more).
	BuildHomesPerCrew     int64 // settlement.build_homes_per_crew
	CharterMaxOffices     int64 // settlement.charter_max_offices (the caps of rail R4, ADR 0044 6.4)
	CharterMaxSeats       int64 // settlement.charter_max_seats (the caps of rail R4, ADR 0044 6.4)
	CharterMaxPermissions int64 // settlement.charter_max_permissions (the caps of rail R4, ADR 0044 6.4)
	CharterTitleMin       int64 // settlement.charter_title_min (the caps of rail R4, ADR 0044 6.4)
	CharterTitleMax       int64 // settlement.charter_title_max (the caps of rail R4, ADR 0044 6.4)
	// The term of an elected seat, REAL days
	CharterElectionTermDays int64 // settlement.charter_election_term_days
	// How long residents may stand in an election, REAL hours
	CharterCandidacyHours int64 // settlement.charter_candidacy_hours
	// How long an election votes, REAL hours
	CharterVotingHours int64 // settlement.charter_voting_hours
	// Days a holder must have served before a recall petition may start
	CharterRecallMinTenureDays int64 // settlement.charter_recall_min_tenure_days
	// Share of eligible residents that must sign a recall petition (2000 = 20 percent)
	CharterRecallSignatureBPS int64 // settlement.charter_recall_signature_bps
	// The floor of signatures for a tiny town (never more than everyone)
	CharterRecallMinSignatures int64 // settlement.charter_recall_min_signatures
	// How long a recall vote runs, REAL hours
	CharterRecallVoteHours int64 // settlement.charter_recall_vote_hours
	// Days a holder is left alone after a recall vote and cannot be re-appointed to that office if removed
	CharterRecallCooldownDays int64 // settlement.charter_recall_cooldown_days
	// How long an amendment vote runs, REAL hours
	CharterAmendVoteHours int64 // settlement.charter_amend_vote_hours
	// Share of eligible residents that must vote for an amendment to count
	CharterAmendQuorumBPS int64 // settlement.charter_amend_quorum_bps
	// Residents from which a structural change goes to a vote (below it the office holder decides)
	CharterAmendVoteMinResidents int64 // settlement.charter_amend_vote_min_residents
	// REAL days an acting head may act while the head seat is vacant
	CharterActingDays int64 // settlement.charter_acting_days
	// The most one spend of an acting head may be, minor units
	CharterActingSpendCap int64 // settlement.charter_acting_spend_cap
	// Days a resident must have lived in the settlement to vote or stand
	CharterMinResidencyDays int64 // settlement.charter_min_residency_days
	// StorageSpoilKeptBPS and StorageSpoilUnkeptBPS are the share of the food in
	// a settlement's stock that spoils per game day, in basis points of the
	// food: with a staffed granary keeping it, and with none (the open yard).
	// A real grain store loses little when it is kept dry and aired, a lot
	// when it is not (docs/adr/0041 6.4 and building_functions.yml granary).
	StorageSpoilKeptBPS   int64 // settlement.storage_spoil_kept_bps
	StorageSpoilUnkeptBPS int64 // settlement.storage_spoil_unkept_bps
	// StorageKeeperRuleAt (RFC 3339) is when the storekeeper rule began: a store that
	// stood complete before it keeps counting its full room for StorageKeeperGraceDays
	// real days after it, so no town loses room overnight; 0 days switches the grace off.
	StorageKeeperRuleAt    string // settlement.storage_keeper_rule_at
	StorageKeeperGraceDays int64  // settlement.storage_keeper_grace_days
	// The real village goods (docs/adr/0050, plan A2). RealItemsRuleAt (RFC 3339) is when firewood, paper and tools became
	// real goods: for RealItemsGraceDays real days after it the older stand-in (timber for firewood, wool for paper) still
	// works for a settlement with none of the real item, and a workplace without tools keeps its full output; after it a shift
	// without tools yields ToolBareHandsBPS of its output. 0 days switches the grace off.
	RealItemsRuleAt    string // settlement.real_items_rule_at
	RealItemsGraceDays int64  // settlement.real_items_grace_days
	ToolBareHandsBPS   int64  // settlement.tool_bare_hands_bps
	// ServiceRuleAt (RFC 3339) is when the daily services (watch post, health house, inn) began to ask for staff, a wage
	// and supplies: a post that stood complete before it keeps counting as open for ServiceGraceDays real days after it
	// as long as a person of the pool staffs it, however thin the store, so no live settlement loses its coverage
	// overnight; 0 days switches the grace off.
	ServiceRuleAt    string // settlement.service_rule_at
	ServiceGraceDays int64  // settlement.service_grace_days
	// PersonalRuleAt (RFC 3339) is when a post asks its holder for the personal prerequisites of its staff role (a
	// level, a skill, a certificate, literacy): a player who lacks them may still work or take the post for
	// PersonalGraceDays real days after it, with a notice of what will be needed; 0 days switches the grace off.
	PersonalRuleAt    string // settlement.personal_rule_at
	PersonalGraceDays int64  // settlement.personal_grace_days
	// The teacher's skill in the literacy tick (docs/adr/0057): a class taught by a player with the teaching skill moves
	// literacy at TeacherBaseBPS plus TeacherPerLevelBPS for each level (never above 10000); a class with no player teacher
	// at TeacherBaseBPS. A finished class teaches its player teacher TeacherXPPerClass of the teaching skill.
	TeacherBaseBPS     int64 // settlement.teacher_base_bps
	TeacherPerLevelBPS int64 // settlement.teacher_per_level_bps
	TeacherXPPerClass  int64 // settlement.teacher_xp_per_class
	// Research capacity and speed (ADR 0048, internal/domain/research).
	ResearchFreeSlots                int64 // settlement.research_free_slots
	ResearchSpeedFloorBPS            int64 // settlement.research_speed_floor_bps
	ResearchScholarFloorBPS          int64 // settlement.research_scholar_floor_bps
	ResearchSkillBPSPerLevel         int64 // settlement.research_skill_bps_per_level
	ResearchScholarCapBPS            int64 // settlement.research_scholar_cap_bps
	ResearchNPCScholarLevel          int64 // settlement.research_npc_scholar_level
	ResearchLiteracyBonusBPS         int64 // settlement.research_literacy_bonus_bps
	ResearchCatchUpBPS               int64 // settlement.research_catch_up_bps
	ResearchEraBaseDepth             int64 // settlement.research_era_base_depth
	ResearchEraShareBPS              int64 // settlement.research_era_share_bps
	ResearchAheadPerStepBPS          int64 // settlement.research_ahead_per_step_bps
	ResearchAheadCapBPS              int64 // settlement.research_ahead_cap_bps
	ResearchSharePerPartnerBPS       int64 // settlement.research_share_per_partner_bps
	ResearchShareCapBPS              int64 // settlement.research_share_cap_bps
	ResearchBreakthroughNeedPerDepth int64 // settlement.research_breakthrough_need_per_depth
	ResearchBreakthroughMaxBPS       int64 // settlement.research_breakthrough_max_bps
	ResearchExperiencePerShift       int64 // settlement.research_experience_per_shift
	ResearchScholarXP                int64 // settlement.research_scholar_xp
	// The market day (ADR 0049): what a travelling trader pays for the surplus a settlement puts on sale.
	ExportPriceBPS       int64 // settlement.export_price_bps
	ExportCapBase        int64 // settlement.export_cap_base
	ExportCapPerResident int64 // settlement.export_cap_per_resident
	// ExportKeepPresets are the amounts the head's trade desk offers to keep back of an item (zero, sell all, is always offered).
	ExportKeepPresets []int64 // settlement.export_keep_presets
	// MaterialBuyMax is the most units of one material a village may buy from
	// Support in one purchase.
	MaterialBuyMax int64 // settlement.material_buy_max
	// MaterialBuyPresets are the quantities the buy buttons offer.
	MaterialBuyPresets []int64 // settlement.material_buy_presets

	// FoundingGrant is the treasury a freshly founded village starts with,
	// minted once from system_source (ledger reason settlement_grant): a
	// village has no income until its abstract sales exist (ADR 0028
	// section 8.3), so without a start it could pay for neither its first
	// buildings nor its first research.
	FoundingGrant int64 // settlement.founding_grant
	// DonationMin and DonationMax bound one settlement.donate, in minor
	// units; DonationPresets are the amounts the donate buttons offer.
	DonationMin     int64   // settlement.donation_min
	DonationMax     int64   // settlement.donation_max
	DonationPresets []int64 // settlement.donation_presets

	// The citizen loop (docs/adr/0033 sections 4.4 and 4.5, migration 0058): what a
	// resident pays to buy a lot and to build a private building on it, and the
	// property tax. Money is minor units. The head's levers (lot price, permit
	// fee, tax) move inside the Min..Max bounds; the defaults apply until the head
	// sets one. Which private buildings exist is content (citizen_buildings.yml).
	// LotPrice is the price of one free lot; LotPriceMin and LotPriceMax
	// bound the head's lever.
	CitizenLotPrice    int64 // settlement.citizen_lot_price
	CitizenLotPriceMin int64 // settlement.citizen_lot_price_min
	CitizenLotPriceMax int64 // settlement.citizen_lot_price_max
	// PermitFee is the permit a private building costs, paid to the
	// treasury; PermitFeeMax bounds the head's lever (the least is zero).
	CitizenPermitFee    int64 // settlement.citizen_permit_fee
	CitizenPermitFeeMax int64 // settlement.citizen_permit_fee_max
	// TaxBPS is the property tax each period, on the assessed value (the
	// lot's price plus the building's cost), in basis points; TaxBPSMax
	// bounds the head's lever.
	CitizenTaxBPS    int // settlement.citizen_tax_bps
	CitizenTaxBPSMax int // settlement.citizen_tax_bps_max
	// TaxPeriod is how often the tax falls due, GAME time; the tax is
	// collected on the village's own periodic tick.
	CitizenTaxPeriod time.Duration // settlement.citizen_tax_period
	// MaterialMarkupBPS is what a missing building material costs over its
	// reference price (items.yml base_price), 10000 = the price itself.
	CitizenMaterialMarkupBPS int // settlement.citizen_material_markup_bps
	// MaxLotsPerPlayer bounds how many lots one player may own in a village,
	// so one wallet cannot buy the village; PrivateShareMaxBPS is the zoning
	// cap: at most this share of the buildable lots may be private.
	CitizenMaxLotsPerPlayer   int // settlement.citizen_max_lots_per_player
	CitizenPrivateShareMaxBPS int // settlement.citizen_private_share_max_bps
	// HomeRestCooldown is how long, REAL time, before the owner may rest at
	// home again; HomeRestHealth and HomeRestHappiness are what one rest
	// gives (the small comfort of living in one's own house).
	CitizenHomeRestCooldown  time.Duration // settlement.citizen_home_rest_cooldown
	CitizenHomeRestHealth    int           // settlement.citizen_home_rest_health
	CitizenHomeRestHappiness int           // settlement.citizen_home_rest_happiness
}

// Governance is the tuning of the office holder's screens
// (docs/adr/0015-player-held-offices.md). It never holds a policy value or a
// bound: those are content and the office holder's decision.
type Governance struct {
	// FineStepDivisor and CoarseStepDivisor size the step buttons of the
	// policy screen as fractions of a lever's range: a range of 2500 with
	// 100 and 10 steps by 25 and 250.
	FineStepDivisor   int // governance.fine_step_divisor
	CoarseStepDivisor int // governance.coarse_step_divisor
	// AllocationStepBPS is how far one press moves a budget line's share on
	// the allocation screen, bps; 10000 must be a whole number of steps of
	// at most 35 (one callback character each).
	AllocationStepBPS int // governance.allocation_step_bps
}

// Crime is the tuning of the crime engine (docs/adr/0019-crime-engine.md).
// The crimes are content and the justice levers a city's policy; none of
// that is here. Durations are REAL time except InvestigationDuration, which
// is GAME time waited through the game clock.
type Crime struct {
	NerveMax           int           // crime.nerve_max
	NerveRegenAmount   int           // crime.nerve_regen_amount
	NerveRegenInterval time.Duration // crime.nerve_regen_interval

	HeatMax          int // crime.heat_max
	HeatDecayPerHour int // crime.heat_decay_per_hour

	ProtectMinLevel int           // crime.protect_min_level
	ProtectMinAge   time.Duration // crime.protect_min_age

	ActiveWindow  time.Duration // crime.active_window
	ArrivalLinger time.Duration // crime.arrival_linger

	VictimCooldown time.Duration // crime.victim_cooldown
	ThiefCooldown  time.Duration // crime.thief_cooldown
	ReportWindow   time.Duration // crime.report_window

	InvestigationDuration        time.Duration // crime.investigation_duration (game time)
	InvestigationBaseBPS         int           // crime.investigation_base_bps
	InvestigationPerHeatBPS      int           // crime.investigation_per_heat_bps
	InvestigationWitnessBonusBPS int           // crime.investigation_witness_bonus_bps
	InvestigationEffortWeightBPS int           // crime.investigation_effort_weight_bps

	// NPCDailyCap is the most NPC crime pays into the economy per UTC day,
	// minor units.
	NPCDailyCap int64 // crime.npc_daily_cap

	// The caps on what carried gear may add to one attempt, by magnitude
	// (crime.Combine): basis points, and nerve points for the cost.
	GearMaxSuccessBPS int // crime.gear_max_success_bps
	GearMaxCatchBPS   int // crime.gear_max_catch_bps
	GearMaxWitnessBPS int // crime.gear_max_witness_bps
	GearMaxSolveBPS   int // crime.gear_max_solve_bps
	GearMaxRewardBPS  int // crime.gear_max_reward_bps
	GearMaxNerve      int // crime.gear_max_nerve
}

// Trade is the tuning of the player market and the auction house. The fee a
// trade pays is each city's policy (city.market_fee), never a number here.
type Trade struct {
	// MarketOrderTTL is how long, REAL time, a resting order lives before
	// it expires and its escrow comes back.
	MarketOrderTTL time.Duration // trade.market_order_ttl
	// MarketMaxOpenOrders bounds one player's resting orders.
	MarketMaxOpenOrders int // trade.market_max_open_orders
	// VillageStallsPost and VillageStallsHall are the stalls (open orders at
	// once) a village book has for each market post and each market hall that
	// stands; VillageStallsPerPlayerPost and VillageStallsPerPlayerHall how
	// many of them one player may hold, with only a post and with a hall
	// (docs/adr/0040 section 5.1).
	VillageStallsPost, VillageStallsHall                   int // trade.village_stalls_post, .village_stalls_hall
	VillageStallsPerPlayerPost, VillageStallsPerPlayerHall int // trade.village_stalls_per_player_post, .village_stalls_per_player_hall
	// MarketDayEveryDays is how often a settlement that has researched the
	// periodic market holds a market day, in game days (0 for never): on it the
	// listing fee is nothing and the dues are halved.
	MarketDayEveryDays int // trade.market_day_every_days
	// StallKeeperShareBPS is the share of a sale a hired stall keeper takes while the owner is away (docs/adr/0062).
	StallKeeperShareBPS int // trade.stall_keeper_share_bps
	// The owner chooses within these when he hires (ADR 0062 addendum): a share of sales, StallKeeperShareMinBPS to
	// StallKeeperShareMaxBPS (the default is StallKeeperShareBPS), or a fixed day wage, StallKeeperWageMin to StallKeeperWageMax
	// minor units (the default is StallKeeperWage).
	StallKeeperShareMinBPS, StallKeeperShareMaxBPS          int // trade.stall_keeper_share_min_bps, .stall_keeper_share_max_bps
	StallKeeperWage, StallKeeperWageMin, StallKeeperWageMax int // trade.stall_keeper_wage, .stall_keeper_wage_min, .stall_keeper_wage_max
	// MarketMaxQuantity and MarketMaxPrice bound one order.
	MarketMaxQuantity int   // trade.market_max_quantity
	MarketMaxPrice    int64 // trade.market_max_price
	// AuctionDurations are the lengths, GAME time, a seller may choose.
	AuctionDurations []time.Duration // trade.auction_durations
	// AuctionMaxReserve bounds a reserve; AuctionStepBPS and AuctionMinStep
	// are how much a bid must beat the standing one by.
	AuctionMaxReserve int64 // trade.auction_max_reserve
	AuctionStepBPS    int   // trade.auction_step_bps
	AuctionMinStep    int64 // trade.auction_min_step
	// AuctionMaxOpen bounds one seller's open auctions.
	AuctionMaxOpen int // trade.auction_max_open
	// AuctionReservesBPS are the reserves a seller is offered, as shares
	// of the good's reference price in basis points.
	AuctionReservesBPS []int64 // trade.auction_reserves_bps
}

// Company is the tuning of player companies (docs/adr/0020-companies.md).
// What a kind of business costs and sells is content (companies.yml); what a
// city charges a company is policy (city.company_registration,
// city.corporate_tax, city.minimum_wage, city.sales_tax), never a number
// here.
type Company struct {
	// Period is one settlement of a city's companies — NPC revenue and
	// upkeep — in GAME time, waited through the game clock.
	Period time.Duration // company.period
	// MaxPerPlayer bounds the companies one player owns at a time.
	MaxPerPlayer int // company.max_per_player
	// NameMinLength and NameMaxLength bound a name, in characters. The
	// longest must fit a typed answer (input.max_length).
	NameMinLength int // company.name_min_length
	NameMaxLength int // company.name_max_length
	// FoundingShares is how many shares a company is founded with, all of
	// them the founder's.
	FoundingShares int64 // company.founding_shares
	// InsolvencyPeriods is how many settlements in a row a company may end
	// owing upkeep before it is dissolved.
	InsolvencyPeriods int // company.insolvency_periods
	// NPCCityPeriodCap is the most one city's NPC population may pay its
	// companies in one period, whatever the content says: the operator's
	// last bound on the faucet, minor units.
	NPCCityPeriodCap int64 // company.npc_city_period_cap
	// MaxOpenings bounds one company's open job openings.
	MaxOpenings int // company.max_openings
	// PriceStepBPS is how far one press of «cheaper» or «dearer» moves a
	// company's price level.
	PriceStepBPS int // company.price_step_bps
	// Citizen labour (docs/adr/0020-companies.md): the job openings no
	// player has taken are worked by the city's citizens.
	//
	// CitizenShiftsPerPeriod is how many shifts one citizen works in a full
	// period.
	CitizenShiftsPerPeriod int // company.citizen_shifts_per_period
	// CitizenProductivityBPS is what one citizen shift counts for against
	// a player's shift, so a player is always the better hire.
	CitizenProductivityBPS int // company.citizen_productivity_bps
	// CitizenLabourShareBPS is the share of a city's NPC population its
	// companies may employ at once.
	CitizenLabourShareBPS int // company.citizen_labour_share_bps

	// The production economy (docs/adr/0021-production-economy.md).
	//
	// MaxRunningOrders bounds one company's production orders running at
	// once.
	MaxRunningOrders int // company.max_running_orders
	// MaxDesigns bounds one company's designs, drafts included.
	MaxDesigns int // company.max_designs
	// MaxListings bounds one company's open listings.
	MaxListings int // company.max_listings
	// DesignMinSkill is the level the company's best member must have in an
	// archetype's craft skill to author a design of it.
	DesignMinSkill int // company.design_min_skill
	// QuickOrderUnits is the size of a quick order: the one-tap order a
	// company's next step offers, and the size an order screen opens at.
	QuickOrderUnits int // company.quick_order_units
	// ReverseTime is how long taking a sample apart takes, GAME time.
	ReverseTime time.Duration // company.reverse_time

	// Product generations (the owner's 2026 request: keep making new
	// versions, keep researching better ones).
	//
	// ImprovementTime is how long one improvement project takes, GAME time;
	// ImprovementCost is its flat fee, minor units, paid whatever attribute
	// is chosen (internal/domain/item.NextImprovementBPS bounds what it
	// buys, not what it costs).
	ImprovementTime time.Duration // company.improvement_time
	ImprovementCost int64         // company.improvement_cost
	// RetrofitTime is how long applying one upgrade kit to one unit takes,
	// GAME time.
	RetrofitTime time.Duration // company.retrofit_time
	// ObsolescenceDecayBPS is how many basis points of market value a
	// design version loses for every generation the newest of its lineage
	// is ahead of it; ObsolescenceFloorBPS is the least it may ever fall to
	// (internal/domain/item.ObsolescenceFactorBPS enforces both as bounds,
	// not as a formula content or config could distort).
	ObsolescenceDecayBPS int // company.obsolescence_decay_bps
	ObsolescenceFloorBPS int // company.obsolescence_floor_bps

	// Specialist recruitment (docs/adr/0027-specialist-recruitment.md).
	//
	// RecruitCheckEvery is the GAME time between two checks of a campaign,
	// and RecruitChecks how many checks one campaign runs.
	RecruitCheckEvery time.Duration // company.recruit_check_every
	RecruitChecks     int           // company.recruit_checks
	// RecruitMaxCampaigns bounds one company's campaigns running at once;
	// RecruitMaxPositions the specialists one campaign may seek.
	RecruitMaxCampaigns int // company.recruit_max_campaigns
	RecruitMaxPositions int // company.recruit_max_positions
	// RecruitMaxCandidates bounds the candidates one check brings.
	RecruitMaxCandidates int // company.recruit_max_candidates
	// RecruitPatience is how long, GAME time, a candidate waits for an
	// answer before taking another job.
	RecruitPatience time.Duration // company.recruit_patience
	// RecruitMaxStaff bounds one company's specialists.
	RecruitMaxStaff int // company.recruit_max_staff
}

// Military is the tuning of the armed forces
// (docs/adr/0022-military-and-diplomacy.md). What a force costs, who decides
// its budget and who may buy its arms are content and policy; this is how
// the clock and the readiness rule run.
type Military struct {
	// Period is one defence period — the cities' share of their revenue,
	// the defence appropriation, the forces' upkeep — in GAME time, waited
	// through the game clock.
	Period time.Duration // military.period
	// ReadinessLossBPS is what a period whose upkeep was not paid in full
	// costs the forces' readiness; ReadinessRecoveryBPS what a period paid
	// in full gives back.
	ReadinessLossBPS     int // military.readiness_loss_bps
	ReadinessRecoveryBPS int // military.readiness_recovery_bps
	// ReferenceRadarKM is the radar the forces screen measures how far each
	// design is seen by: kilometres at which it sees a 1 m² target.
	ReferenceRadarKM int // military.reference_radar_km
	// LicenceRevokeNotice is how long a defence licence the minister
	// revokes stays in force, REAL time: a governance promise, like a
	// lever's notice (docs/adr/0022, section 2.14).
	LicenceRevokeNotice time.Duration // military.licence_revoke_notice
	// EndedLicencesShown is how many ended defence licences the public
	// registry lists.
	EndedLicencesShown int // military.ended_licences_shown
}

// War is the tuning of war (docs/adr/0022-military-and-diplomacy.md, part
// two). How battles are fought is content (military.yml war); these are the
// promises and the page sizes. DeclarationNotice and ProposalTTL are REAL
// time: a governance promise (docs/adr/0018-game-clock.md).
type War struct {
	// DeclarationNotice is how long after a declaration — or a ceasefire's
	// end — the war may be fought.
	DeclarationNotice time.Duration // war.declaration_notice
	// ProposalTTL is how long a ceasefire or a peace waits for an answer.
	ProposalTTL time.Duration // war.proposal_ttl
	// EndedShownFor is how long an ended war stays on the war board.
	EndedShownFor time.Duration // war.ended_shown_for
	// BoardOperations is how many operations the war board lists.
	BoardOperations int // war.board_operations
	// NoticeCap is the most players of a struck city told privately.
	NoticeCap int // war.notice_cap
}

// Missions is the tuning of missions (docs/adr/0023). What each mission asks
// and gives is content (configs/content/missions.yml); these are the caps on
// the faucet and the page sizes. The day is a UTC day.
type Missions struct {
	// MaxActive is how many missions a player may run at once.
	MaxActive int // missions.max_active
	// PlayerDailyCap is the most mission cash one player receives in a day.
	PlayerDailyCap int64 // missions.player_daily_cap
	// EconomyDailyCap is the most mission cash all players together receive
	// in a day.
	EconomyDailyCap int64 // missions.economy_daily_cap
}

// Education
// tuning of teaching (docs/research/2026-10-03-activities-audit.md section 7).
type Education struct {
	TeacherWageBPS     int64 // education.teacher_wage_bps: a teacher earns this share of the course's listed fee for each student, paid from the treasury when the class ends
	TeacherMinWage     int64 // education.teacher_min_wage: the least a class pays its teacher, minor units
	TeacherMaxStudents int64 // education.teacher_max_students: students one teacher teaches at once
}

// Training
// tuning of the training activity (same audit, section 7).
type Training struct {
	EnergyCost          int64 // training.energy_cost: energy one session spends
	StaminaGain         int64 // training.stamina_gain: stamina a session gives at full efficiency, before diminishing returns
	StrengthXP          int64 // training.strength_xp: strength skill experience a session gives at full efficiency
	DiminishStamina     int64 // training.diminish_stamina: every this much stamina above the starting 100 halves the next gains (1/(1+extra/this))
	StaminaPerMaxEnergy int64 // training.stamina_per_max_energy: stamina that adds one point of max energy
	MaxEnergyBonusCap   int64 // training.max_energy_bonus_cap: the most max energy training can add
	YardBPS             int64 // training.yard_bps: efficiency of bodyweight training on open ground (no building)
	GroundBPS           int64 // training.ground_bps: efficiency at a settlement training ground (ADR 0038 4.6: 60 percent)
	GymBPS              int64 // training.gym_bps: efficiency at the neutral city gym
	GroundFee           int64 // training.ground_fee: session fee at a training ground, to the settlement treasury, minor units
	GymFee              int64 // training.gym_fee: session fee at the neutral city gym, minor units
}

// Labor is the village labour market's tuning (docs/adr/0037-labor-market.md):
// construction is done by workers, and an NPC labourer's wage follows how
// scarce labour is. Wages and minimums are minor units per construction shift;
// every share is in basis points.
type Labor struct {
	ShiftMinutes       int64 // labor.shift_minutes
	ShiftRealMinutes   int64 // labor.shift_real_minutes
	ReferenceCrew      int64 // labor.reference_crew
	BaseWage           int64 // labor.base_wage
	MinWageVillage     int64 // labor.min_wage_village
	MinWageTown        int64 // labor.min_wage_town
	MinWageCity        int64 // labor.min_wage_city
	ParticipationBPS   int64 // labor.participation_bps
	BaseHousing        int64 // labor.base_housing
	NPCProductivityBPS int64 // labor.npc_productivity_bps
	FeeBPS             int64 // labor.fee_bps
	BudgetSlackBPS     int64 // labor.budget_slack_bps
	// NPCHoursPerSlotDay is how many hours of work an NPC crew may do at one post per local day.
	NPCHoursPerSlotDay int64 // labor.npc_hours_per_slot_day
	// HungryOutputBPS is a hungry player's output at a workplace (they ate nothing);
	// HungryShiftHunger the hunger points such a shift adds to their own need.
	HungryOutputBPS        int64   // labor.hungry_output_bps
	HungryShiftHunger      int64   // labor.hungry_shift_hunger
	RepairMaterialShareBPS int64   // labor.repair_material_share_bps
	RepairShiftsFull       int64   // labor.repair_shifts_full
	WornOutputBPS          int64   // labor.worn_output_bps
	ClosedBPS              int64   // labor.closed_bps
	WornBPS                int64   // labor.worn_bps
	RepairBelowBPS         int64   // labor.repair_below_bps
	DecayBPSPerDay         int64   // labor.decay_bps_per_day
	JourneymanShifts       int64   // labor.journeyman_shifts
	MasterShifts           int64   // labor.master_shifts
	ApprenticeBPS          int64   // labor.apprentice_bps
	JourneymanBPS          int64   // labor.journeyman_bps
	MasterBPS              int64   // labor.master_bps
	TightBalancedBPS       int64   // labor.tight_balanced_bps
	TightTightBPS          int64   // labor.tight_tight_bps
	TightShortBPS          int64   // labor.tight_short_bps
	WageSlackBPS           int64   // labor.wage_slack_bps
	WageBalancedBPS        int64   // labor.wage_balanced_bps
	WageTightBPS           int64   // labor.wage_tight_bps
	WageShortBPS           int64   // labor.wage_short_bps
	HirePresets            []int64 // labor.hire_presets
	WagePresets            []int64 // labor.wage_presets
}

// Legislature is the tuning of votes of a body
// (docs/adr/0024-property-and-politics.md): which bodies exist and what they
// confirm is content (governance.yml).
type Legislature struct {
	// VoteWindow is how long a proposal stays open for the body's votes,
	// REAL time: a governance promise (docs/adr/0018-game-clock.md).
	VoteWindow time.Duration // legislature.vote_window
	// ListSize is how many proposals one screen lists.
	ListSize int // legislature.list_size
}

// City is the tuning of a city's period (docs/adr/0024): each period, once,
// the city's budget is spent by its allocation and property pays its upkeep,
// tax and rent.
type City struct {
	// Period is one city period, GAME time, waited through the game clock.
	Period time.Duration // city.period
}

// Property is the tuning of property (docs/adr/0024). What each kind of
// property costs, where and how many are for sale is content
// (configs/content/property.yml); a city's property tax is policy.
type Property struct {
	// ForeclosurePeriods is how many city periods in a row an owner may end
	// owing upkeep or tax before the property is repossessed.
	ForeclosurePeriods int // property.foreclosure_periods
	// EvictionPeriods is how many periods of rent in a row a tenant may owe
	// before the lease ends.
	EvictionPeriods int // property.eviction_periods
	// MaxOwned is how many properties one player may own at once.
	MaxOwned int // property.max_owned
	// MaxPrice is the highest asking price of a listing; MaxRent the highest
	// rent a landlord may ask per period. Minor units.
	MaxPrice int64 // property.max_price
	MaxRent  int64 // property.max_rent
	// RestCooldown is how long after resting at home a player may rest
	// again, GAME time.
	RestCooldown time.Duration // property.rest_cooldown
}

// Achievements is the tuning of achievements (docs/adr/0024). What each one
// asks and gives is content (configs/content/achievements.yml); its cash is a
// faucet (ADR 0009 achievement_reward), capped twice a UTC day. What a cap
// withholds is not paid later.
type Achievements struct {
	PlayerDailyCap  int64 // achievements.player_daily_cap
	EconomyDailyCap int64 // achievements.economy_daily_cap
}

// Factions is the tuning of factions (docs/adr/0023). What founding one costs
// and what each rank may do is content (configs/content/factions.yml).
type Factions struct {
	NameMinLength int // factions.name_min_length
	NameMaxLength int // factions.name_max_length
	// MaxMembers caps a faction's members, leader included.
	MaxMembers int // factions.max_members
	// MaxPending caps a faction's invitations and applications waiting.
	MaxPending int // factions.max_pending
	// MinFounders is how many people must live in the settlement (the same
	// group) before one of them may found a faction here.
	MinFounders int // factions.min_founders
	// ListSize is how many factions or members one screen lists.
	ListSize int // factions.list_size
}

// AntiCheat is the watch's tuning (docs/adr/0023, internal/domain/watch).
// Every threshold is behavioural; nothing here bans anyone. Window is REAL
// time.
type AntiCheat struct {
	Window                time.Duration // anticheat.window
	OneWayCount           int           // anticheat.one_way_count
	OneWayMinTotal        int64         // anticheat.one_way_min_total
	OneWayRatioBPS        int           // anticheat.one_way_ratio_bps
	OffMarketBPS          int           // anticheat.off_market_bps
	OffMarketMinValue     int64         // anticheat.off_market_min_value
	SinglePartnerMinCount int           // anticheat.single_partner_min_count
	SinglePartnerShareBPS int           // anticheat.single_partner_share_bps
	CommandsPerMinute     int           // anticheat.commands_per_minute
	WashTradeCount        int           // anticheat.wash_trade_count
	// HoldAbove is the smallest payment held for review between accounts a
	// flag links.
	HoldAbove int64 // anticheat.hold_above
}

// Diplomacy is the tuning of sanctions and treaties
// (docs/adr/0022-military-and-diplomacy.md). Every duration here is REAL
// time: a governance promise, like a lever's notice
// (docs/adr/0018-game-clock.md).
type Diplomacy struct {
	// SanctionNotice is how long after it is announced a sanction binds.
	SanctionNotice time.Duration // diplomacy.sanction_notice
	// SanctionMinDuration is the least a sanction stands before it may be
	// lifted.
	SanctionMinDuration time.Duration // diplomacy.sanction_min_duration
	// TreatyOfferTTL is how long a proposal waits for its answer.
	TreatyOfferTTL time.Duration // diplomacy.treaty_offer_ttl
	// EndedShownFor is how long an ended treaty stays on the treaties
	// board.
	EndedShownFor time.Duration // diplomacy.ended_shown_for
	// HistoryPageSize is the entries of the public record on one page.
	HistoryPageSize int // diplomacy.history_page_size
}

// Defaults returns every field at the value it was hardcoded to before this
// package existed.
//
// This is not decoration. It is what makes a partial config file safe: a key
// nobody wrote keeps the behaviour the code shipped with, instead of
// collapsing to a zero that every Go API reads as "unbounded".
func Defaults() *Config {
	return &Config{
		Gateway: Gateway{
			PollTimeout:           30 * time.Second,
			PollErrorBackoff:      2 * time.Second,
			ShutdownTimeout:       20 * time.Second,
			SendAttempts:          2,
			RedirectCooldown:      time.Minute,
			WebAppPrivateCooldown: 5 * time.Second,
		},
		Lease: Lease{
			TTL:            30 * time.Second,
			RenewDivisor:   3,
			ReleaseTimeout: 5 * time.Second,
		},
		RateLimit: RateLimit{
			DefaultRate:  25,
			DefaultBurst: 1,
		},
		Telegram: Telegram{
			RequestTimeout:   30 * time.Second,
			MaxPollTimeout:   120 * time.Second,
			PollTimeoutGrace: 15 * time.Second,
			DefaultFloodWait: 5 * time.Second,
		},
		Groups: Groups{
			CallbackAlertMaxRunes: 200,
			DeepLinkTTL:           15 * time.Minute,
		},
		Menu: Menu{
			Commands: []string{"profile", "map", "job", "study", "bank", "city", "crime", "skills", "social", "find", "settings", "help"},
		},
		Dedup: Dedup{
			TTL: 24 * time.Hour,
		},
		NATS: NATS{
			CommandMaxAge:   24 * time.Hour,
			EventMaxAge:     30 * 24 * time.Hour,
			DuplicateWindow: 2 * time.Minute,
			AckWait:         30 * time.Second,
			MaxDeliver:      5,
			NakDelay:        5 * time.Second,
			Backoff: []time.Duration{
				1 * time.Second,
				5 * time.Second,
				15 * time.Second,
				60 * time.Second,
			},
		},
		Worker: Worker{
			PollInterval:    250 * time.Millisecond,
			BatchSize:       100,
			ShutdownTimeout: 15 * time.Second,
			NoisyAttempts:   5,
		},
		Scheduler: Scheduler{
			TickInterval:    1 * time.Second,
			BatchSize:       100,
			ShutdownTimeout: 15 * time.Second,
			NoisyAttempts:   5,
			ClaimTimeout:    2 * time.Minute,
		},
		Notifier: Notifier{
			SendBudget:      15 * time.Second,
			ReceiptMargin:   3 * time.Second,
			MaxAge:          24 * time.Hour,
			ShutdownTimeout: 15 * time.Second,
		},
		Game: Game{
			ShutdownTimeout: 20 * time.Second,
			IdempotencyTTL:  24 * time.Hour,

			ContentReloadInterval: 30 * time.Second,
			TimeScale:             1,
			ClockEpoch:            "2026-01-01T00:00:00Z",
			ClockLegacyScale:      60,
			ClockCutover:          "2026-10-06T00:00:00Z",
			TravelTimeScale:       1,
			CommandTimeout:        30 * time.Second,
		},
		Travel: Travel{
			ArrivalXP: 25,
			// Support, on the seed-42 world: a temperate lowland cell of the
			// great continent, 940 km from any sea (ADR 0034).
			CityLocations: []string{"support=32.30:-47.70"},
			WorldReach:    []string{"walk=40", "cart=150", "car=21000"},
		},
		Player: Player{
			DefaultLanguage: "fa",
			DefaultTimezone: "Asia/Tehran",
		},
		Economy: Economy{
			StartingCash:     5000,
			BankMinAmount:    1,
			BankMaxAmount:    1000000000,
			BankQuickAmounts: []int64{1000, 5000, 10000, 50000, 100000, 500000, 1000000},
		},
		Company: Company{
			Period:                 24 * time.Hour,
			MaxPerPlayer:           2,
			NameMinLength:          3,
			NameMaxLength:          24,
			FoundingShares:         1000,
			InsolvencyPeriods:      3,
			NPCCityPeriodCap:       50000,
			MaxOpenings:            5,
			PriceStepBPS:           1000,
			CitizenShiftsPerPeriod: 2,
			CitizenProductivityBPS: 7000,
			CitizenLabourShareBPS:  500,
			MaxRunningOrders:       3,
			MaxDesigns:             20,
			MaxListings:            10,
			DesignMinSkill:         1,
			QuickOrderUnits:        5,
			ReverseTime:            6 * time.Hour,
			ImprovementTime:        4 * time.Hour,
			ImprovementCost:        20000,
			RetrofitTime:           2 * time.Hour,
			ObsolescenceDecayBPS:   800,
			ObsolescenceFloorBPS:   3000,
			RecruitCheckEvery:      6 * time.Hour,
			RecruitChecks:          4,
			RecruitMaxCampaigns:    2,
			RecruitMaxPositions:    5,
			RecruitMaxCandidates:   6,
			RecruitPatience:        24 * time.Hour,
			RecruitMaxStaff:        10,
		},
		Military: Military{
			Period:               24 * time.Hour,
			ReadinessLossBPS:     1000,
			ReadinessRecoveryBPS: 500,
			ReferenceRadarKM:     150,
			LicenceRevokeNotice:  24 * time.Hour,
			EndedLicencesShown:   5,
		},
		Diplomacy: Diplomacy{
			SanctionNotice:      time.Hour,
			SanctionMinDuration: 24 * time.Hour,
			TreatyOfferTTL:      72 * time.Hour,
			EndedShownFor:       168 * time.Hour,
			HistoryPageSize:     8,
		},
		War: War{
			DeclarationNotice: 24 * time.Hour,
			ProposalTTL:       48 * time.Hour,
			EndedShownFor:     168 * time.Hour,
			BoardOperations:   8,
			NoticeCap:         200,
		},
		Missions: Missions{
			MaxActive:       5,
			PlayerDailyCap:  5_000,
			EconomyDailyCap: 1_000_000,
		},
		Factions: Factions{
			NameMinLength: 3,
			NameMaxLength: 24,
			MaxMembers:    30,
			MaxPending:    20,
			MinFounders:   20,
			ListSize:      10,
		},
		AntiCheat: AntiCheat{
			Window:                24 * time.Hour,
			OneWayCount:           4,
			OneWayMinTotal:        20_000,
			OneWayRatioBPS:        9_000,
			OffMarketBPS:          5_000,
			OffMarketMinValue:     5_000,
			SinglePartnerMinCount: 6,
			SinglePartnerShareBPS: 9_000,
			CommandsPerMinute:     90,
			WashTradeCount:        3,
			HoldAbove:             50_000,
		},
		Input: Input{
			TTL:       5 * time.Minute,
			Cooldown:  3 * time.Second,
			MaxLength: 32,
		},
		Announce: Announce{
			Window:       time.Minute,
			MaxPerWindow: 6,

			VillageMergeWindow:   20 * time.Second,
			VillageMinGap:        90 * time.Second,
			VillageFlushInterval: 5 * time.Second,
			VillageLiteracyStep:  10,
		},
		Notifications: Notifications{
			InboxPageSize:         5,
			EditThrottle:          8 * time.Second,
			ReminderDelay:         24 * time.Hour,
			ReminderCheckInterval: 15 * time.Minute,
			Retention:             30 * 24 * time.Hour,
			PruneInterval:         24 * time.Hour,
			HungerAlertCooldown:   2 * time.Hour,
			VitalsMinInterval:     2 * time.Second,
		},
		WorldGen: WorldGen{
			CellCount:              40_000,
			NeighborK:              6,
			PlateCount:             16,
			OceanicPlateFraction:   550,
			LandFraction:           450,
			NoiseOctaves:           6,
			NoiseBaseFrequency:     2.0,
			NoisePersistence:       520,
			WarpAmplitude:          0.45,
			WarpFrequency:          1.1,
			BoundaryInfluenceSteps: 9,
			MoistureBands:          90,
			RiverFlowThreshold:     12,
			LakeMinDepth:           40,
			LakeMinAreaCells:       20,

			PlanetRadiusKm:              6371,
			ChunkBaseLOD:                10,
			ChunkTileEdge:               32,
			ChunkDetailFrequency:        2000,
			ChunkDetailAmplitude:        300,
			ChunkStreamFrequency:        1200,
			ChunkStreamAmplitude:        60,
			ChunkDepositTilesPerDeposit: 5,
		},
		Growth: Growth{
			Capabilities:  GrowthAuthoritative,
			CacheTTL:      5 * time.Second,
			RuinedBPS:     10000,
			FlushInterval: 30 * time.Second,
		},
		Settlement: Settlement{
			ProtectionWindow:            168 * time.Hour,
			ResidenceCooldown:           72 * time.Hour,
			TimezoneCooldown:            168 * time.Hour,
			HomeCityCode:                "support",
			PropertyHubMinStage:         "town",
			MinSpawnDistanceKm:          30,
			ThreatRadiusKm:              150,
			SearchMaxCells:              2000,
			SearchMaxAttempts:           200,
			SpawnCircleRadiusKm:         300,
			SpawnCircleCapacity:         12,
			SpawnCircleFillBandKm:       120,
			SpawnCircleMaxAdvance:       200,
			BuildingAreaPerCell:         6,
			BuildingStoreyTimberPerCell: 4,
			BuildingStoreyStonePerCell:  6,
			BuildingStoreyStoneFrom:     3,
			BuildingStoreyShiftsPerCell: 4,
			BuildingStoreyKnowledge:     []string{"carpentry_ii=2", "masonry=2", "masonry_ii=3", "masonry_iii=5"},
			BuildingSalvageBPS:          3000,
			UseChangeFeeBPS:             200,
			BuildingLookRerolls:         8,
			BuildingTemplatesMax:        20,
			ExcludedBiomes:              []string{"polar_ice"},
			MaxAbsLatitudeDeg:           70,
			BiomePenalties:              []string{"desert=4", "tundra=6", "boreal_forest=1"},
			VillageGridLots:             5,

			MinBuildableLotShareBps: 7000,
			GridShiftMaxLots:        3,
			AutoRoadCost:            10,
			LotAccessCrossingCost:   60,
			LotAccessMaxCrossing:    2,
			StreetPitch:             5,
			StreetPlanMinGrid:       12,
			RoadFrontageDepthLots:   3,
			RoadPlanMaxLots:         1500,
			RoadOpenLotsMax:         30000,
			RoadForeignBufferTiles:  3,
			RoadSteepSlopeM:         30,
			RoadCorridorRingTiles:   1,
			RoadTrackCostBPS:        15000,

			FoundingDraftTTL:          30 * time.Minute,
			FoundingNameMin:           3,
			FoundingNameMax:           24,
			FoundingMottoMax:          60,
			FoundingCurrencyNameMin:   3,
			FoundingCurrencyNameMax:   24,
			FoundingCurrencyCodeLen:   3,
			FoundingCurrencySymbolMax: 3,

			TeachPeriod:                      24 * time.Hour,
			TeachRateBPS:                     1500,
			BaseSchoolCapacityBPS:            8000,
			ScarcityKBPS:                     10000,
			ScarcityFloorBPS:                 3000,
			ScarcityCapBPS:                   80000,
			SellerBandBPS:                    500,
			DemolitionSalvageBPS:             2000,
			MaterialMarkupBPS:                12000,
			StockBaseCapacity:                60,
			BuildHomesPerCrew:                16,
			CharterMaxOffices:                24,
			CharterMaxSeats:                  15,
			CharterMaxPermissions:            40,
			CharterTitleMin:                  2,
			CharterTitleMax:                  32,
			CharterElectionTermDays:          14,
			CharterCandidacyHours:            48,
			CharterVotingHours:               72,
			CharterRecallMinTenureDays:       5,
			CharterRecallSignatureBPS:        2000,
			CharterRecallMinSignatures:       3,
			CharterRecallVoteHours:           72,
			CharterRecallCooldownDays:        14,
			CharterAmendVoteHours:            72,
			CharterAmendQuorumBPS:            3000,
			CharterAmendVoteMinResidents:     6,
			CharterActingDays:                7,
			CharterActingSpendCap:            2000,
			CharterMinResidencyDays:          3,
			StorageSpoilKeptBPS:              5,
			StorageSpoilUnkeptBPS:            30,
			StorageKeeperRuleAt:              "2026-10-03T00:00:00Z",
			StorageKeeperGraceDays:           14,
			RealItemsRuleAt:                  "2026-10-10T18:00:00Z",
			RealItemsGraceDays:               7,
			ToolBareHandsBPS:                 6000,
			ServiceRuleAt:                    "2026-10-10T12:00:00Z",
			ServiceGraceDays:                 7,
			PersonalRuleAt:                   "2026-10-10T00:00:00Z",
			PersonalGraceDays:                7,
			TeacherBaseBPS:                   6000,
			TeacherPerLevelBPS:               150,
			TeacherXPPerClass:                20,
			ResearchFreeSlots:                1,
			ResearchSpeedFloorBPS:            10000,
			ResearchScholarFloorBPS:          500,
			ResearchSkillBPSPerLevel:         100,
			ResearchScholarCapBPS:            2000,
			ResearchNPCScholarLevel:          3,
			ResearchLiteracyBonusBPS:         2000,
			ResearchCatchUpBPS:               4000,
			ResearchEraBaseDepth:             4,
			ResearchEraShareBPS:              1600,
			ResearchAheadPerStepBPS:          3000,
			ResearchAheadCapBPS:              10000,
			ResearchSharePerPartnerBPS:       1000,
			ResearchShareCapBPS:              5000,
			ResearchBreakthroughNeedPerDepth: 100,
			ResearchBreakthroughMaxBPS:       4000,
			ResearchExperiencePerShift:       10,
			ResearchScholarXP:                40,
			ExportPriceBPS:                   9000,
			ExportCapBase:                    400,
			ExportCapPerResident:             40,
			ExportKeepPresets:                []int64{10, 25, 50, 100},
			MaterialBuyMax:                   200,
			MaterialBuyPresets:               []int64{5, 20, 50},
			FoundingGrant:                    10_000,
			DonationMin:                      100,
			DonationMax:                      100_000,
			DonationPresets:                  []int64{250, 1000, 5000},
			CitizenLotPrice:                  400, CitizenLotPriceMin: 100, CitizenLotPriceMax: 5000,
			CitizenPermitFee: 100, CitizenPermitFeeMax: 1000,
			CitizenTaxBPS: 200, CitizenTaxBPSMax: 500, CitizenTaxPeriod: 24 * time.Hour,
			CitizenMaterialMarkupBPS: 12000,
			CitizenMaxLotsPerPlayer:  6, CitizenPrivateShareMaxBPS: 6000,
			CitizenHomeRestCooldown: 6 * time.Hour, CitizenHomeRestHealth: 10, CitizenHomeRestHappiness: 5,
		},
		Governance: Governance{
			FineStepDivisor:   100,
			CoarseStepDivisor: 10,
			AllocationStepBPS: 500,
		},
		Bag: Bag{CarryBase: 8, BaseComfortKG: 7, BaseHardKG: 20, FullShareBPS: 5000, TornSpaceBPS: 5000,
			RepairShareBPS: 2500, WearPerDay: 1},
		Merchant: Merchant{RestockHour: 6, MarkupMinBPS: 10000, MarkupMaxBPS: 15000, StockDays: 2,
			FoodShareBPS: 4000, OtherShareBPS: 3000, PlayerDayFood: 3, PlayerDayOther: 2, SupplyValueFood: 600,
			BuildingBoostBPS: 15000, CapPresets: []int64{10000, 11000, 12500, 15000}, BuyPresets: []int64{1, 3, 5},
			TaxDefaultBPS: 300, TaxMaxBPS: 1500, TaxPresets: []int64{100, 300, 500, 1000}, OutputDays: 7},
		Premium:     Premium{NilUnitSup: 100, NilExamples: []int64{100, 1000, 10000}},
		Legislature: Legislature{VoteWindow: 48 * time.Hour, ListSize: 8},
		Education:   Education{TeacherWageBPS: 6000, TeacherMinWage: 40, TeacherMaxStudents: 12},
		Training:    Training{EnergyCost: 10, StaminaGain: 6, StrengthXP: 30, DiminishStamina: 400, StaminaPerMaxEnergy: 50, MaxEnergyBonusCap: 30, YardBPS: 4000, GroundBPS: 6000, GymBPS: 10000, GroundFee: 20, GymFee: 60},
		Currency: Currency{
			CharterR0:           10,
			CharterFee:          1000,
			CharterMinDeposit:   5000,
			MintFeeBPS:          50,
			AutoCharterShareBPS: 5000,
			DeskSlippageBPS:     100, DeskPresets: []int64{100, 500, 2000},
			AutoCharterFloor:           500,
			FXReserveFeeBPS:            30,
			FXMaxMoveBPS:               2000,
			FXMinTrades:                8,
			FXWindowPeriods:            7,
			FXMinOrderSUP:              10,
			FXBookLimit:                200,
			FXMaxOpenOrders:            20,
			FXConvertSlippageBPS:       100,
			InterventionDelay:          24 * time.Hour,
			ReserveGoldHaircutBPS:      1000,
			ReserveWithdrawNoticeHours: 72,
			ReservePolicyRateBPS:       1200,
			InterventionCapBPS:         800,
			InterventionPotFloorBPS:    3000,
			WindDownDays:               60,
			MacroMNormBPS:              2000,
			MacroKappaBPS:              200,
			MacroPiMaxBPS:              300,
			MacroWTradableBPS:          6000,
			FXOrderTTL:                 7 * 24 * time.Hour, FXPeriod: 24 * time.Hour, FXUnitPresets: []int64{1000, 5000, 20000},
		},
		Labor: Labor{
			ShiftMinutes:           60,
			ShiftRealMinutes:       1,
			ReferenceCrew:          4,
			BaseWage:               30,
			MinWageVillage:         10,
			MinWageTown:            15,
			MinWageCity:            25,
			ParticipationBPS:       6000,
			BaseHousing:            8,
			NPCProductivityBPS:     8500,
			FeeBPS:                 500,
			BudgetSlackBPS:         5000,
			NPCHoursPerSlotDay:     8,
			HungryOutputBPS:        5000,
			HungryShiftHunger:      5,
			RepairMaterialShareBPS: 2000,
			RepairShiftsFull:       10,
			WornOutputBPS:          7500,
			ClosedBPS:              2500,
			WornBPS:                5000,
			RepairBelowBPS:         7000,
			DecayBPSPerDay:         50,
			JourneymanShifts:       6,
			MasterShifts:           30,
			ApprenticeBPS:          7000,
			JourneymanBPS:          10000,
			MasterBPS:              13000,
			TightBalancedBPS:       5000,
			TightTightBPS:          10000,
			TightShortBPS:          20000,
			WageSlackBPS:           7000,
			WageBalancedBPS:        10000,
			WageTightBPS:           15000,
			WageShortBPS:           25000,
			HirePresets:            []int64{1, 2, 4},
			WagePresets:            []int64{100, 125, 150, 200},
		},
		City: City{Period: 24 * time.Hour},
		Property: Property{ForeclosurePeriods: 3, EvictionPeriods: 2, MaxOwned: 5, MaxPrice: 100_000_000,
			MaxRent: 1_000_000, RestCooldown: 8 * time.Hour},
		Achievements: Achievements{PlayerDailyCap: 5000, EconomyDailyCap: 500_000},
		Postgres:     Postgres{MaxConns: 16, IdleInTransactionTimeout: 60 * time.Second},
		Panel:        defaultPanel(),
		Client:       defaultClient(),
		Realtime:     defaultRealtime(),
		StateSync:    defaultStateSync(),
		Crime: Crime{
			NerveMax:                     20,
			NerveRegenAmount:             1,
			NerveRegenInterval:           5 * time.Minute,
			HeatMax:                      100,
			HeatDecayPerHour:             4,
			ProtectMinLevel:              3,
			ProtectMinAge:                72 * time.Hour,
			ActiveWindow:                 30 * time.Minute,
			ArrivalLinger:                20 * time.Minute,
			VictimCooldown:               6 * time.Hour,
			ThiefCooldown:                24 * time.Hour,
			ReportWindow:                 24 * time.Hour,
			InvestigationDuration:        6 * time.Hour,
			InvestigationBaseBPS:         2500,
			InvestigationPerHeatBPS:      40,
			InvestigationWitnessBonusBPS: 3500,
			InvestigationEffortWeightBPS: 3000,
			NPCDailyCap:                  500000,
			GearMaxSuccessBPS:            2500,
			GearMaxCatchBPS:              2500,
			GearMaxWitnessBPS:            3000,
			GearMaxSolveBPS:              3000,
			GearMaxRewardBPS:             5000,
			GearMaxNerve:                 5,
		},
		Trade: Trade{
			MarketOrderTTL:      168 * time.Hour,
			MarketMaxOpenOrders: 20,
			VillageStallsPost:   6, VillageStallsHall: 20,
			VillageStallsPerPlayerPost: 3, VillageStallsPerPlayerHall: 6,
			MarketDayEveryDays: 7, StallKeeperShareBPS: 1000, StallKeeperShareMinBPS: 500, StallKeeperShareMaxBPS: 2000,
			StallKeeperWage: 600, StallKeeperWageMin: 300, StallKeeperWageMax: 1500,
			MarketMaxQuantity:  10000,
			MarketMaxPrice:     100_000_000,
			AuctionDurations:   []time.Duration{time.Hour, 6 * time.Hour, 24 * time.Hour},
			AuctionMaxReserve:  100_000_000,
			AuctionStepBPS:     500,
			AuctionMinStep:     10,
			AuctionMaxOpen:     5,
			AuctionReservesBPS: []int64{5000, 10000, 15000},
		},
	}
}

// Load reads path, applies environment overrides and validates the result.
//
// It returns a complete, valid Config or an error. There is deliberately no
// third outcome: a half-filled struct handed back alongside an error is how a
// service ends up running with one field at its zero value.
func Load(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		return nil, ErrNoPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: reading %s: %w", path, err)
	}

	cfg, err := parse(data)
	if err != nil {
		// The path leads, because the first thing an operator needs is which
		// file to open; the wrapped error already names the package, the
		// field and, for a decode failure, the line.
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	if err := applyEnv(cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// parse decodes yaml over a copy of the defaults.
func parse(data []byte) (*Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))

	// Without this, yaml.v3 ignores a key it does not recognise. A typo would
	// then leave the default in place while the file plainly shows the value
	// the operator intended, and the two would disagree forever in silence.
	dec.KnownFields(true)

	var file fileConfig
	if err := dec.Decode(&file); err != nil {
		if errors.Is(err, io.EOF) {
			// An empty file is legitimate: everything defaults.
			return Defaults(), nil
		}
		// yaml.v3's errors already carry line numbers and the offending field
		// name, so they are passed through rather than summarised away.
		return nil, err
	}

	// A second document would be silently ignored, and a reader of the file
	// would have no way to know which half was in force.
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, ErrMultipleDocuments
	}

	cfg := Defaults()
	for _, s := range settings {
		if err := s.fromFile(cfg, &file); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// applyEnv lets the environment win over the file, which is what makes a
// single committed yaml usable across environments that differ in one value.
func applyEnv(cfg *Config) error {
	for _, s := range settings {
		name := s.envName()

		raw, ok := os.LookupEnv(name)
		if !ok {
			continue
		}
		if strings.TrimSpace(raw) == "" {
			return fmt.Errorf("%w: %s is set but empty; unset it to fall back to the file", ErrInvalidValue, name)
		}
		if err := s.fromEnv(cfg, raw); err != nil {
			return err
		}
	}
	return nil
}

// Validate rejects a configuration that cannot work.
//
// Two layers: every field is checked for the mistake its type allows (a
// duration or a limit at or below zero, an empty string, an empty list), and
// then the handful of real invariants between fields are checked, because
// those are the ones that are individually plausible and jointly fatal.
func (c *Config) Validate() error {
	for _, s := range settings {
		if err := s.check(c); err != nil {
			return err
		}
	}
	if cur := c.Currency; cur.MintFeeBPS > 10_000 || cur.AutoCharterShareBPS > 10_000 || cur.CharterR0 < 1 || cur.CharterMinDeposit < 1 {
		return fmt.Errorf("config: currency: the fee and the share are at most 10000 bps, the charter rate and the minimum deposit at least 1")
	}
	if err := c.Panel.validate(); err != nil {
		return err
	}
	if err := c.Client.validate(); err != nil {
		return err
	}
	if err := c.Realtime.validate(); err != nil {
		return err
	}
	if err := c.StateSync.validate(); err != nil {
		return err
	}
	switch c.Growth.Capabilities {
	case GrowthOff, GrowthShadow, GrowthAuthoritative:
	default:
		return fmt.Errorf("%w: growth.capabilities is %q (off, shadow or authoritative)", ErrInvalidValue, c.Growth.Capabilities)
	}
	if c.Growth.RuinedBPS < 1 || c.Growth.RuinedBPS > 10000 {
		return fmt.Errorf("%w: growth.ruined_bps is %d (1..10000)", ErrInvalidValue, c.Growth.RuinedBPS)
	}

	// Renewing at or after the TTL is not renewing. A divisor of one renews
	// exactly when the lease has already expired.
	if c.Lease.RenewDivisor < 2 {
		return fmt.Errorf("%w: lease.renew_divisor is %d", ErrRenewDivisorTooSmall, c.Lease.RenewDivisor)
	}

	// The invariant that keeps long polling alive.
	if c.Telegram.RequestTimeout >= c.Telegram.PollHTTPTimeout() {
		return fmt.Errorf("%w: request_timeout is %s but max_poll_timeout + poll_timeout_grace is %s",
			ErrRequestTimeoutTooLong, c.Telegram.RequestTimeout, c.Telegram.PollHTTPTimeout())
	}

	// A poll longer than the client's ceiling is refused per call, so every
	// bot would fail to poll from the first attempt.
	if c.Gateway.PollTimeout > c.Telegram.MaxPollTimeout {
		return fmt.Errorf("%w: gateway.poll_timeout is %s, telegram.max_poll_timeout is %s",
			ErrPollTimeoutTooLong, c.Gateway.PollTimeout, c.Telegram.MaxPollTimeout)
	}

	// A flat or shrinking schedule retries a struggling dependency hardest
	// when it is least able to answer.
	for i := 1; i < len(c.NATS.Backoff); i++ {
		if c.NATS.Backoff[i] <= c.NATS.Backoff[i-1] {
			return fmt.Errorf("%w: nats.backoff entry %d (%s) does not exceed entry %d (%s)",
				ErrNotIncreasing, i+1, c.NATS.Backoff[i], i, c.NATS.Backoff[i-1])
		}
	}

	// A name must fit the typed answer that carries it.
	if c.Company.NameMinLength > c.Company.NameMaxLength || c.Company.NameMaxLength > c.Input.MaxLength {
		return fmt.Errorf("%w: company.name_min_length %d, company.name_max_length %d, input.max_length %d",
			ErrCompanyNameBounds, c.Company.NameMinLength, c.Company.NameMaxLength, c.Input.MaxLength)
	}
	if c.Company.PriceStepBPS > 10000 {
		return fmt.Errorf("%w: company.price_step_bps is %d", ErrNotPositive, c.Company.PriceStepBPS)
	}
	if c.Company.ObsolescenceDecayBPS > 10000 {
		return fmt.Errorf("%w: company.obsolescence_decay_bps is %d", ErrNotPositive, c.Company.ObsolescenceDecayBPS)
	}
	if c.Company.ObsolescenceFloorBPS > 10000 {
		return fmt.Errorf("%w: company.obsolescence_floor_bps is %d", ErrNotPositive, c.Company.ObsolescenceFloorBPS)
	}
	if step := c.Governance.AllocationStepBPS; step > 10000 || 10000%step != 0 || 10000/step > 35 {
		return fmt.Errorf("%w: governance.allocation_step_bps is %d; it must divide 10000 into at most 35 steps",
			ErrInvalidValue, step)
	}
	if c.Military.ReadinessLossBPS > 10000 || c.Military.ReadinessRecoveryBPS > 10000 {
		return fmt.Errorf("%w: military.readiness_loss_bps is %d and military.readiness_recovery_bps %d; each at most 10000",
			ErrNotPositive, c.Military.ReadinessLossBPS, c.Military.ReadinessRecoveryBPS)
	}
	if c.Company.CitizenProductivityBPS > 10000 || c.Company.CitizenLabourShareBPS > 10000 {
		return fmt.Errorf("%w: company.citizen_productivity_bps %d, company.citizen_labour_share_bps %d, above 10000",
			ErrNotPositive, c.Company.CitizenProductivityBPS, c.Company.CitizenLabourShareBPS)
	}
	if c.Factions.NameMinLength > c.Factions.NameMaxLength || c.Factions.NameMaxLength > c.Input.MaxLength {
		return fmt.Errorf("%w: factions.name_min_length %d, factions.name_max_length %d, input.max_length %d",
			ErrCompanyNameBounds, c.Factions.NameMinLength, c.Factions.NameMaxLength, c.Input.MaxLength)
	}
	if c.AntiCheat.OneWayRatioBPS < 5000 || c.AntiCheat.OneWayRatioBPS > 10000 ||
		c.AntiCheat.SinglePartnerShareBPS < 5000 || c.AntiCheat.SinglePartnerShareBPS > 10000 ||
		c.AntiCheat.OneWayCount < 2 || c.AntiCheat.SinglePartnerMinCount < 2 {
		return fmt.Errorf("%w: anticheat ratios must lie in 5000..10000 bps and counts be at least 2", ErrNotPositive)
	}
	if c.Missions.PlayerDailyCap > c.Missions.EconomyDailyCap {
		return fmt.Errorf("%w: missions.player_daily_cap %d is above missions.economy_daily_cap %d",
			ErrNotPositive, c.Missions.PlayerDailyCap, c.Missions.EconomyDailyCap)
	}
	if c.Postgres.MaxConns < 2 || c.Postgres.MaxConns > 500 {
		return fmt.Errorf("%w: postgres.max_conns is %d, outside 2..500", ErrNotPositive, c.Postgres.MaxConns)
	}
	if c.Postgres.IdleInTransactionTimeout < time.Second {
		return fmt.Errorf("%w: postgres.idle_in_transaction_timeout is %s, under a second", ErrNotPositive,
			c.Postgres.IdleInTransactionTimeout)
	}
	if c.Game.CommandTimeout >= c.Postgres.IdleInTransactionTimeout {
		return fmt.Errorf("%w: game.command_timeout %s must be shorter than postgres.idle_in_transaction_timeout %s",
			ErrNotPositive, c.Game.CommandTimeout, c.Postgres.IdleInTransactionTimeout)
	}
	if c.Company.DesignMinSkill > 100 {
		return fmt.Errorf("%w: company.design_min_skill is %d, above the skill scale", ErrNotPositive, c.Company.DesignMinSkill)
	}

	if err := c.validateCarry(); err != nil {
		return err
	}

	// The idempotency key has to outlive the last redelivery, or the last
	// redelivery is executed as if it were new.
	if window := c.NATS.RedeliveryWindow(); c.Game.IdempotencyTTL <= window {
		return fmt.Errorf("%w: game.idempotency_ttl is %s, the redelivery schedule spans %s",
			ErrIdempotencyTTLTooShort, c.Game.IdempotencyTTL, window)
	}

	// A lease shorter than the batch budget reclaims rows that are still
	// being dispatched.
	if c.Scheduler.ClaimTimeout <= c.Scheduler.ShutdownTimeout {
		return fmt.Errorf("%w: claim_timeout is %s, shutdown_timeout is %s",
			ErrClaimTimeoutTooShort, c.Scheduler.ClaimTimeout, c.Scheduler.ShutdownTimeout)
	}

	// A minimum above the maximum refuses every deposit and payment.
	if c.Economy.BankMinAmount > c.Economy.BankMaxAmount {
		return fmt.Errorf("%w: bank_min_amount is %d, bank_max_amount is %d",
			ErrBankLimitsInverted, c.Economy.BankMinAmount, c.Economy.BankMaxAmount)
	}

	// A delivery still running when the ack wait ends is redelivered while
	// it runs, and the notice goes out twice.
	if c.Notifier.SendBudget >= c.NATS.AckWait {
		return fmt.Errorf("%w: send_budget is %s, nats.ack_wait is %s",
			ErrSendBudgetTooLong, c.Notifier.SendBudget, c.NATS.AckWait)
	}

	// The game clock's own bound: past one game day per real second every
	// duration collapses into the one-second floor.
	if c.Game.TimeScale > maxTimeScale {
		return fmt.Errorf("%w: it is %d", ErrInvalidTimeScale, c.Game.TimeScale)
	}

	// A zone the database does not know would show players UTC.
	if _, err := time.LoadLocation(c.Player.DefaultTimezone); err != nil {
		return fmt.Errorf("%w: %q", ErrUnknownTimezone, c.Player.DefaultTimezone)
	}

	// A latitude cap outside (0,90] or a malformed biome penalty would
	// silently misplace or block every founding.
	if c.Settlement.MaxAbsLatitudeDeg <= 0 || c.Settlement.MaxAbsLatitudeDeg > 90 {
		return fmt.Errorf("%w: settlement.max_abs_latitude_deg is %v, want 0 < x <= 90",
			ErrInvalidValue, c.Settlement.MaxAbsLatitudeDeg)
	}
	if _, err := c.Travel.CityLocationMap(); err != nil {
		return err
	}
	if _, err := c.Travel.WorldReachMap(); err != nil {
		return err
	}
	penalties, err := c.Settlement.BiomePenaltyMap()
	if err != nil {
		return err
	}
	for _, code := range c.Settlement.ExcludedBiomes {
		if _, both := penalties[code]; both {
			return fmt.Errorf("%w: settlement biome %q is both excluded and penalised", ErrInvalidValue, code)
		}
	}

	if v := c.Settlement.MinBuildableLotShareBps; v > 10_000 {
		return fmt.Errorf("%w: settlement.min_buildable_lot_share_bps is %d, want 1 to 10000", ErrBPSTooLarge, v)
	}
	if v := c.Settlement.GridShiftMaxLots; v > 10 {
		return fmt.Errorf("%w: settlement.grid_shift_max_lots is %d, want 1 to 10", ErrInvalidValue, v)
	}

	// The founding form's bounds must leave room for a name and a code the
	// database accepts (currencies.code is 2-6 capital letters).
	if st := c.Settlement; st.FoundingNameMin > st.FoundingNameMax || st.FoundingCurrencyNameMin > st.FoundingCurrencyNameMax {
		return fmt.Errorf("%w: settlement.founding_*_min is above its max", ErrInvalidValue)
	}
	if c.Settlement.FoundingCurrencyCodeLen < 2 || c.Settlement.FoundingCurrencyCodeLen > 6 {
		return fmt.Errorf("%w: settlement.founding_currency_code_len is %d, want 2 to 6",
			ErrInvalidValue, c.Settlement.FoundingCurrencyCodeLen)
	}
	if c.Settlement.FoundingNameMax > 60 || c.Settlement.FoundingMottoMax > 200 || c.Settlement.FoundingCurrencyNameMax > 60 || c.Settlement.FoundingCurrencySymbolMax > 8 {
		return fmt.Errorf("%w: a settlement.founding_* bound is beyond what the database column holds", ErrInvalidValue)
	}

	// A donation window that is upside down would refuse every donation.
	if c.Settlement.DonationMin > c.Settlement.DonationMax {
		return fmt.Errorf("%w: settlement.donation_min %d is above settlement.donation_max %d",
			ErrInvalidValue, c.Settlement.DonationMin, c.Settlement.DonationMax)
	}
	for i, v := range c.Settlement.DonationPresets {
		if v < c.Settlement.DonationMin || v > c.Settlement.DonationMax {
			return fmt.Errorf("%w: settlement.donation_presets[%d] %d is outside donation_min..donation_max",
				ErrInvalidValue, i, v)
		}
	}

	if z := c.Settlement; z.CitizenLotPriceMin < 1 || z.CitizenLotPrice < z.CitizenLotPriceMin || z.CitizenLotPrice > z.CitizenLotPriceMax ||
		z.CitizenPermitFee < 0 || z.CitizenPermitFee > z.CitizenPermitFeeMax || z.CitizenTaxBPS < 0 || z.CitizenTaxBPS > z.CitizenTaxBPSMax || z.CitizenTaxBPSMax > 10_000 ||
		z.CitizenTaxPeriod <= 0 || z.CitizenMaterialMarkupBPS < 10_000 || z.CitizenMaxLotsPerPlayer < 1 ||
		z.CitizenPrivateShareMaxBPS < 1 || z.CitizenPrivateShareMaxBPS > 10_000 || z.CitizenHomeRestCooldown <= 0 {
		return fmt.Errorf("%w: settlement.citizen.* is out of order (a default outside its bounds, a period at zero, a markup below the reference price)", ErrInvalidValue)
	}

	// A basis-point weight above 100% is a typo, not a tuning.
	for name, v := range map[string]int{
		"crime.investigation_base_bps":          c.Crime.InvestigationBaseBPS,
		"crime.investigation_per_heat_bps":      c.Crime.InvestigationPerHeatBPS,
		"crime.investigation_witness_bonus_bps": c.Crime.InvestigationWitnessBonusBPS,
		"crime.investigation_effort_weight_bps": c.Crime.InvestigationEffortWeightBPS,
	} {
		if v > 10_000 {
			return fmt.Errorf("%w: %s is %d", ErrBPSTooLarge, name, v)
		}
	}

	// The gateway needs some of the budget to send in.
	if c.Notifier.ReceiptMargin >= c.Notifier.SendBudget {
		return fmt.Errorf("%w: receipt_margin is %s, send_budget is %s",
			ErrReceiptMarginTooLong, c.Notifier.ReceiptMargin, c.Notifier.SendBudget)
	}

	return nil
}

// BiomePenaltyMap parses settlement.biome_penalties ("code=points" entries)
// into a map by biome code. A malformed entry, a negative or non-finite
// penalty, or a repeated code is an error naming the entry.
func (s Settlement) BiomePenaltyMap() (map[string]float64, error) {
	out := make(map[string]float64, len(s.BiomePenalties))
	for i, entry := range s.BiomePenalties {
		code, val, ok := strings.Cut(entry, "=")
		code = strings.TrimSpace(code)
		if !ok || code == "" {
			return nil, fmt.Errorf("%w: settlement.biome_penalties[%d] %q is not code=points", ErrInvalidValue, i, entry)
		}
		pts, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err != nil || pts < 0 || pts > 1e6 {
			return nil, fmt.Errorf("%w: settlement.biome_penalties[%d] %q has no valid non-negative number", ErrInvalidValue, i, entry)
		}
		if _, dup := out[code]; dup {
			return nil, fmt.Errorf("%w: settlement.biome_penalties repeats %q", ErrInvalidValue, code)
		}
		out[code] = pts
	}
	return out, nil
}

// parseDuration reads a duration, naming the field and the text that failed.
//
// The field name is the whole point. "invalid duration" tells an operator
// nothing at three in the morning; "nats.ack_wait: \"30\" is not a duration"
// tells them exactly which line to fix and what is wrong with it.
func parseDuration(field, raw string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %q is not a duration; write it as 250ms, 30s, 2m or 24h", ErrInvalidValue, field, raw)
	}
	return d, nil
}

func parseInt(field, raw string) (int, error) {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %q is not a whole number", ErrInvalidValue, field, raw)
	}
	return v, nil
}

// parseFloat reads a decimal number, for the handful of worldgen fields a
// whole number cannot express (a noise frequency, a domain-warp amplitude).
func parseFloat(field, raw string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %q is not a number", ErrInvalidValue, field, raw)
	}
	return v, nil
}

// parseInt64 reads a 64-bit whole number, for money in minor units. It goes
// through strconv.ParseInt, never ParseFloat, so "5000.5" or "5e3" is refused
// rather than rounded.
func parseInt64(field, raw string) (int64, error) {
	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %q is not a whole number", ErrInvalidValue, field, raw)
	}
	return v, nil
}

// parseDurationList reads a comma-separated schedule, which is how a list
// reaches the process through a single environment variable.
func parseDurationList(field, raw string) ([]time.Duration, error) {
	parts := strings.Split(raw, ",")
	out := make([]time.Duration, 0, len(parts))

	for i, part := range parts {
		d, err := parseDuration(fmt.Sprintf("%s[%d]", field, i), part)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// Currency is a settlement's own money (docs/adr/0033 section 6, roadmap 2.19): what a charter costs
// and how it is made. The rate is never configured: demand sets it; CharterR0 is only the scale of
// the numbers at charter.
type Currency struct {
	CharterR0           int64 // currency.charter_r0: the starting scale of a chartered settlement currency: units per SUP at charter (a scale, never a peg)
	CharterFee          int64 // currency.charter_fee: the charter fee in SUP, a sink
	CharterMinDeposit   int64 // currency.charter_min_deposit: the smallest first deposit in SUP
	MintFeeBPS          int64 // currency.mint_fee_bps: the issuance fee, kept in the reserve pot
	AutoCharterShareBPS int64 // currency.auto_charter_share_bps: the share of the treasury beyond the fee an automatic charter of an existing settlement may deposit
	AutoCharterFloor    int64 // currency.auto_charter_floor: the least deposit an automatic charter is worth making for
	// DeskSlippageBPS is how far the desk's price may move between the quote and the confirm.
	DeskSlippageBPS int64 // currency.desk_slippage_bps
	// DeskPresets are the SUP amounts the desk's menu offers.
	DeskPresets []int64 // currency.desk_presets

	// The floating VC/SUP book (docs/adr/0033 6.8, roadmap 2.19 phase 3).
	FXReserveFeeBPS      int64         // currency.fx_reserve_fee_bps: reserve.fx_fee_bps: the fee on selling SUP into a money, paid to Support's treasury
	FXMaxMoveBPS         int64         // currency.fx_max_move_bps: reserve.max_move_bps: the circuit breaker, how far from the reference rate a price may be placed
	FXMinTrades          int64         // currency.fx_min_trades: the fills a window needs before the reference rate moves
	FXWindowPeriods      int64         // currency.fx_window_periods: the periods the reference rate averages
	FXMinOrderSUP        int64         // currency.fx_min_order_sup: the least an order must be worth, in SUP
	FXBookLimit          int64         // currency.fx_book_limit: how many resting orders of a side one match looks at
	FXMaxOpenOrders      int64         // currency.fx_max_open_orders: an owner's open orders in one book
	FXConvertSlippageBPS int64         // currency.fx_convert_slippage_bps: how far a conversion may move between its quote and its confirm
	FXOrderTTL           time.Duration // currency.fx_order_ttl: how long a limit order lives
	FXPeriod             time.Duration // currency.fx_period: one period of the reference rate
	FXUnitPresets        []int64       // currency.fx_unit_presets: the quantities the book screen offers

	// The Reserve Bank's fallbacks and the head's tools over the reserve (roadmap 2.19 phase 4).
	ReserveGoldHaircutBPS      int64         // currency.reserve_gold_haircut_bps: reserve.gold_haircut_bps fallback (the lever wins once the content has it)
	ReserveWithdrawNoticeHours int64         // currency.reserve_withdraw_notice_hours: reserve.withdraw_notice fallback, hours
	ReservePolicyRateBPS       int64         // currency.reserve_policy_rate_bps: reserve.policy_rate fallback
	InterventionCapBPS         int64         // currency.intervention_cap_bps: the share of the pot a period of the head's purchases may take
	InterventionPotFloorBPS    int64         // currency.intervention_pot_floor_bps: the share of the basis the pot may not fall below by intervention
	WindDownDays               int64         // currency.wind_down_days: the claim window of a money winding down, days
	MacroMNormBPS              int64         // currency.macro_m_norm_bps: periods of output the money supply should be (2000 is 0.2)
	MacroKappaBPS              int64         // currency.macro_kappa_bps: how far a gap between money and output moves local prices
	MacroPiMaxBPS              int64         // currency.macro_pi_max_bps: the bound of local inflation per period
	MacroWTradableBPS          int64         // currency.macro_w_tradable_bps: the weight of tradable goods in the basket
	InterventionDelay          time.Duration // currency.intervention_delay: how long a request of the head waits
}
