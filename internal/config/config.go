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
	Input      Input
	Announce   Announce
}

// Gateway paces the Telegram polling loop and its shutdown.
type Gateway struct {
	PollTimeout      time.Duration // gateway.poll_timeout
	PollErrorBackoff time.Duration // gateway.poll_error_backoff
	ShutdownTimeout  time.Duration // gateway.shutdown_timeout
	SendAttempts     int           // gateway.send_attempts
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
	// divided by this. At 60 a 24h course is 24 real minutes.
	//
	// The legacy key travel.time_scale (and TORN_TRAVEL_TIME_SCALE) still
	// fills it, from before the clock was the whole game's; game.time_scale
	// wins where both are set.
	TimeScale int // game.time_scale
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

// Defaults returns every field at the value it was hardcoded to before this
// package existed.
//
// This is not decoration. It is what makes a partial config file safe: a key
// nobody wrote keeps the behaviour the code shipped with, instead of
// collapsing to a zero that every Go API reads as "unbounded".
func Defaults() *Config {
	return &Config{
		Gateway: Gateway{
			PollTimeout:      30 * time.Second,
			PollErrorBackoff: 2 * time.Second,
			ShutdownTimeout:  20 * time.Second,
			SendAttempts:     2,
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
			TimeScale:             60,
		},
		Travel: Travel{
			ArrivalXP: 25,
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
		Input: Input{
			TTL:       5 * time.Minute,
			Cooldown:  3 * time.Second,
			MaxLength: 32,
		},
		Announce: Announce{
			Window:       time.Minute,
			MaxPerWindow: 6,
		},
		Governance: Governance{
			FineStepDivisor:   100,
			CoarseStepDivisor: 10,
		},
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
			MarketMaxQuantity:   10000,
			MarketMaxPrice:      100_000_000,
			AuctionDurations:    []time.Duration{time.Hour, 6 * time.Hour, 24 * time.Hour},
			AuctionMaxReserve:   100_000_000,
			AuctionStepBPS:      500,
			AuctionMinStep:      10,
			AuctionMaxOpen:      5,
			AuctionReservesBPS:  []int64{5000, 10000, 15000},
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
