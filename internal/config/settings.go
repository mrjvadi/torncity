package config

import (
	"fmt"
	"strings"
	"time"
)

// This file holds the two things that have to stay in step: the shape of the
// yaml, and the table that maps every yaml key to the Config field behind it.
//
// The table is deliberately the only route into a field. Defaults, file
// values, environment overrides and validation all walk it, so a field cannot
// be file-settable but not environment-settable, or settable but never
// checked. Adding a value to the project means adding one line here and one
// line to configs/config.yml, and everything else follows.

// fileConfig mirrors configs/config.yml exactly.
//
// It is separate from Config for two reasons. Durations arrive as strings such
// as "30s", which no numeric Go type will decode on its own, and converting
// them in one named place is what lets a bad one be reported as
// "gateway.poll_timeout: \"3zz\" is not a duration" rather than as a line
// number alone. And every field is a pointer, so "the key is absent" and "the
// key was set to zero" are different facts: the first keeps the default, the
// second is a validation error, and conflating them would turn a typo into an
// unbounded timeout.
type fileConfig struct {
	Gateway    gatewaySettings    `yaml:"gateway"`
	Lease      leaseSettings      `yaml:"lease"`
	RateLimit  ratelimitSettings  `yaml:"ratelimit"`
	Telegram   telegramSettings   `yaml:"telegram"`
	Groups     groupsSettings     `yaml:"groups"`
	Menu       menuSettings       `yaml:"menu"`
	Dedup      dedupSettings      `yaml:"dedup"`
	NATS       natsSettings       `yaml:"nats"`
	Worker     workerSettings     `yaml:"worker"`
	Scheduler  schedulerSettings  `yaml:"scheduler"`
	Notifier   notifierSettings   `yaml:"notifier"`
	Game       gameSettings       `yaml:"game"`
	Travel     travelSettings     `yaml:"travel"`
	Player     playerSettings     `yaml:"player"`
	Economy    economySettings    `yaml:"economy"`
	Governance governanceSettings `yaml:"governance"`
	Crime      crimeSettings      `yaml:"crime"`
	Trade      tradeSettings      `yaml:"trade"`
	Company    companySettings    `yaml:"company"`
	Military   militarySettings   `yaml:"military"`
	Diplomacy  diplomacySettings  `yaml:"diplomacy"`
	Input      inputSettings      `yaml:"input"`
	Announce   announceSettings   `yaml:"announce"`
}

type gatewaySettings struct {
	PollTimeout      *string `yaml:"poll_timeout"`
	PollErrorBackoff *string `yaml:"poll_error_backoff"`
	ShutdownTimeout  *string `yaml:"shutdown_timeout"`
	SendAttempts     *int    `yaml:"send_attempts"`
}

type leaseSettings struct {
	TTL            *string `yaml:"ttl"`
	RenewDivisor   *int    `yaml:"renew_divisor"`
	ReleaseTimeout *string `yaml:"release_timeout"`
}

type ratelimitSettings struct {
	DefaultRate  *int `yaml:"default_rate"`
	DefaultBurst *int `yaml:"default_burst"`
}

type telegramSettings struct {
	RequestTimeout   *string `yaml:"request_timeout"`
	MaxPollTimeout   *string `yaml:"max_poll_timeout"`
	PollTimeoutGrace *string `yaml:"poll_timeout_grace"`
	DefaultFloodWait *string `yaml:"default_flood_wait"`
}

type groupsSettings struct {
	CallbackAlertMaxRunes *int    `yaml:"callback_alert_max_runes"`
	DeepLinkTTL           *string `yaml:"deep_link_ttl"`
}

type menuSettings struct {
	Commands []string `yaml:"commands"`
}

type dedupSettings struct {
	TTL *string `yaml:"ttl"`
}

type natsSettings struct {
	CommandMaxAge   *string  `yaml:"command_max_age"`
	EventMaxAge     *string  `yaml:"event_max_age"`
	DuplicateWindow *string  `yaml:"duplicate_window"`
	AckWait         *string  `yaml:"ack_wait"`
	MaxDeliver      *int     `yaml:"max_deliver"`
	NakDelay        *string  `yaml:"nak_delay"`
	Backoff         []string `yaml:"backoff"`
}

type workerSettings struct {
	PollInterval    *string `yaml:"poll_interval"`
	BatchSize       *int    `yaml:"batch_size"`
	ShutdownTimeout *string `yaml:"shutdown_timeout"`
	NoisyAttempts   *int    `yaml:"noisy_attempts"`
}

type schedulerSettings struct {
	TickInterval    *string `yaml:"tick_interval"`
	BatchSize       *int    `yaml:"batch_size"`
	ShutdownTimeout *string `yaml:"shutdown_timeout"`
	NoisyAttempts   *int    `yaml:"noisy_attempts"`
	ClaimTimeout    *string `yaml:"claim_timeout"`
}

type notifierSettings struct {
	SendBudget      *string `yaml:"send_budget"`
	ReceiptMargin   *string `yaml:"receipt_margin"`
	MaxAge          *string `yaml:"max_age"`
	ShutdownTimeout *string `yaml:"shutdown_timeout"`
}

type gameSettings struct {
	ShutdownTimeout *string `yaml:"shutdown_timeout"`
	IdempotencyTTL  *string `yaml:"idempotency_ttl"`

	ContentReloadInterval *string `yaml:"content_reload_interval"`
	TimeScale             *int    `yaml:"time_scale"`
}

type travelSettings struct {
	ArrivalXP *int `yaml:"arrival_xp"`
	// TimeScale is the legacy spelling of game.time_scale; see Game.
	TimeScale *int `yaml:"time_scale"`
}

type playerSettings struct {
	DefaultLanguage *string `yaml:"default_language"`
	DefaultTimezone *string `yaml:"default_timezone"`
}

type economySettings struct {
	StartingCash     *int64  `yaml:"starting_cash"`
	BankMinAmount    *int64  `yaml:"bank_min_amount"`
	BankMaxAmount    *int64  `yaml:"bank_max_amount"`
	BankQuickAmounts []int64 `yaml:"bank_quick_amounts"`
}

type inputSettings struct {
	TTL       *string `yaml:"ttl"`
	Cooldown  *string `yaml:"cooldown"`
	MaxLength *int    `yaml:"max_length"`
}

type announceSettings struct {
	Window       *string `yaml:"window"`
	MaxPerWindow *int    `yaml:"max_per_window"`
}

type governanceSettings struct {
	FineStepDivisor   *int `yaml:"fine_step_divisor"`
	CoarseStepDivisor *int `yaml:"coarse_step_divisor"`
}

type crimeSettings struct {
	NerveMax                     *int    `yaml:"nerve_max"`
	NerveRegenAmount             *int    `yaml:"nerve_regen_amount"`
	NerveRegenInterval           *string `yaml:"nerve_regen_interval"`
	HeatMax                      *int    `yaml:"heat_max"`
	HeatDecayPerHour             *int    `yaml:"heat_decay_per_hour"`
	ProtectMinLevel              *int    `yaml:"protect_min_level"`
	ProtectMinAge                *string `yaml:"protect_min_age"`
	ActiveWindow                 *string `yaml:"active_window"`
	ArrivalLinger                *string `yaml:"arrival_linger"`
	VictimCooldown               *string `yaml:"victim_cooldown"`
	ThiefCooldown                *string `yaml:"thief_cooldown"`
	ReportWindow                 *string `yaml:"report_window"`
	InvestigationDuration        *string `yaml:"investigation_duration"`
	InvestigationBaseBPS         *int    `yaml:"investigation_base_bps"`
	InvestigationPerHeatBPS      *int    `yaml:"investigation_per_heat_bps"`
	InvestigationWitnessBonusBPS *int    `yaml:"investigation_witness_bonus_bps"`
	InvestigationEffortWeightBPS *int    `yaml:"investigation_effort_weight_bps"`
	NPCDailyCap                  *int64  `yaml:"npc_daily_cap"`
	GearMaxSuccessBPS            *int    `yaml:"gear_max_success_bps"`
	GearMaxCatchBPS              *int    `yaml:"gear_max_catch_bps"`
	GearMaxWitnessBPS            *int    `yaml:"gear_max_witness_bps"`
	GearMaxSolveBPS              *int    `yaml:"gear_max_solve_bps"`
	GearMaxRewardBPS             *int    `yaml:"gear_max_reward_bps"`
	GearMaxNerve                 *int    `yaml:"gear_max_nerve"`
}

type tradeSettings struct {
	MarketOrderTTL      *string  `yaml:"market_order_ttl"`
	MarketMaxOpenOrders *int     `yaml:"market_max_open_orders"`
	MarketMaxQuantity   *int     `yaml:"market_max_quantity"`
	MarketMaxPrice      *int64   `yaml:"market_max_price"`
	AuctionDurations    []string `yaml:"auction_durations"`
	AuctionMaxReserve   *int64   `yaml:"auction_max_reserve"`
	AuctionStepBPS      *int     `yaml:"auction_step_bps"`
	AuctionMinStep      *int64   `yaml:"auction_min_step"`
	AuctionMaxOpen      *int     `yaml:"auction_max_open"`
	AuctionReservesBPS  []int64  `yaml:"auction_reserves_bps"`
}

type companySettings struct {
	Period                 *string `yaml:"period"`
	MaxPerPlayer           *int    `yaml:"max_per_player"`
	NameMinLength          *int    `yaml:"name_min_length"`
	NameMaxLength          *int    `yaml:"name_max_length"`
	FoundingShares         *int64  `yaml:"founding_shares"`
	InsolvencyPeriods      *int    `yaml:"insolvency_periods"`
	NPCCityPeriodCap       *int64  `yaml:"npc_city_period_cap"`
	MaxOpenings            *int    `yaml:"max_openings"`
	PriceStepBPS           *int    `yaml:"price_step_bps"`
	CitizenShiftsPerPeriod *int    `yaml:"citizen_shifts_per_period"`
	CitizenProductivityBPS *int    `yaml:"citizen_productivity_bps"`
	CitizenLabourShareBPS  *int    `yaml:"citizen_labour_share_bps"`
	MaxRunningOrders       *int    `yaml:"max_running_orders"`
	MaxDesigns             *int    `yaml:"max_designs"`
	MaxListings            *int    `yaml:"max_listings"`
	DesignMinSkill         *int    `yaml:"design_min_skill"`
	ReverseTime            *string `yaml:"reverse_time"`
}

type militarySettings struct {
	Period               *string `yaml:"period"`
	ReadinessLossBPS     *int    `yaml:"readiness_loss_bps"`
	ReadinessRecoveryBPS *int    `yaml:"readiness_recovery_bps"`
	ReferenceRadarKM     *int    `yaml:"reference_radar_km"`
}

type diplomacySettings struct {
	SanctionNotice      *string `yaml:"sanction_notice"`
	SanctionMinDuration *string `yaml:"sanction_min_duration"`
	TreatyOfferTTL      *string `yaml:"treaty_offer_ttl"`
	EndedShownFor       *string `yaml:"ended_shown_for"`
	HistoryPageSize     *int    `yaml:"history_page_size"`
}

// setting is one configurable value, from its yaml key to the field it fills.
type setting struct {
	section string
	key     string

	// fromFile copies the decoded yaml into the Config, doing nothing when
	// the key was absent so the default survives.
	fromFile func(*Config, *fileConfig) error

	// fromEnv reads the same field out of an environment variable.
	fromEnv func(*Config, string) error

	// check is the field-level sanity Validate runs on it.
	check func(*Config) error
}

// envName is the override variable: TORN_<SECTION>_<FIELD>, upper-cased, with
// the yaml key's underscores kept. See the package doc.
func (s setting) envName() string {
	return "TORN_" + strings.ToUpper(s.section) + "_" + strings.ToUpper(s.key)
}

func (s setting) name() string { return s.section + "." + s.key }

// durationSetting wires a duration field. Its check rejects zero and below,
// because every Go API that takes a duration reads zero as "no limit".
func durationSetting(section, key string, field func(*Config) *time.Duration, raw func(*fileConfig) *string) setting {
	s := setting{section: section, key: key}
	name := s.name()

	s.fromFile = func(c *Config, f *fileConfig) error {
		p := raw(f)
		if p == nil {
			return nil
		}
		d, err := parseDuration(name, *p)
		if err != nil {
			return err
		}
		*field(c) = d
		return nil
	}
	s.fromEnv = func(c *Config, text string) error {
		d, err := parseDuration(s.envName(), text)
		if err != nil {
			return err
		}
		*field(c) = d
		return nil
	}
	s.check = func(c *Config) error {
		if v := *field(c); v <= 0 {
			return fmt.Errorf("%w: %s is %s", ErrNotPositive, name, v)
		}
		return nil
	}
	return s
}

// limitSetting wires a whole-number field: a count, a size, a divisor. Its
// check rejects zero and below, because a limit of zero means the loop it
// bounds does nothing at all.
func limitSetting(section, key string, field func(*Config) *int, raw func(*fileConfig) *int) setting {
	s := setting{section: section, key: key}
	name := s.name()

	s.fromFile = func(c *Config, f *fileConfig) error {
		p := raw(f)
		if p == nil {
			return nil
		}
		*field(c) = *p
		return nil
	}
	s.fromEnv = func(c *Config, text string) error {
		v, err := parseInt(s.envName(), text)
		if err != nil {
			return err
		}
		*field(c) = v
		return nil
	}
	s.check = func(c *Config) error {
		if v := *field(c); v <= 0 {
			return fmt.Errorf("%w: %s is %d", ErrNotPositive, name, v)
		}
		return nil
	}
	return s
}

// moneySetting wires an amount of money in minor units. It is int64, like
// every money value in the project, and never passes through a float: the
// yaml decoder fills an int64 directly and the environment is parsed with
// strconv.ParseInt. Its check rejects zero and below, for the same reason
// limitSetting does: an amount of zero means the thing it pays does nothing.
func moneySetting(section, key string, field func(*Config) *int64, raw func(*fileConfig) *int64) setting {
	s := setting{section: section, key: key}
	name := s.name()

	s.fromFile = func(c *Config, f *fileConfig) error {
		p := raw(f)
		if p == nil {
			return nil
		}
		*field(c) = *p
		return nil
	}
	s.fromEnv = func(c *Config, text string) error {
		v, err := parseInt64(s.envName(), text)
		if err != nil {
			return err
		}
		*field(c) = v
		return nil
	}
	s.check = func(c *Config) error {
		if v := *field(c); v <= 0 {
			return fmt.Errorf("%w: %s is %d", ErrNotPositive, name, v)
		}
		return nil
	}
	return s
}

func stringSetting(section, key string, field func(*Config) *string, raw func(*fileConfig) *string) setting {
	s := setting{section: section, key: key}
	name := s.name()

	s.fromFile = func(c *Config, f *fileConfig) error {
		p := raw(f)
		if p == nil {
			return nil
		}
		*field(c) = *p
		return nil
	}
	s.fromEnv = func(c *Config, text string) error {
		*field(c) = text
		return nil
	}
	s.check = func(c *Config) error {
		if strings.TrimSpace(*field(c)) == "" {
			return fmt.Errorf("%w: %s", ErrEmpty, name)
		}
		return nil
	}
	return s
}

// stringListSetting wires a list of words. From the environment it is
// comma-separated. Its check rejects an empty list and an empty entry.
func stringListSetting(section, key string, field func(*Config) *[]string, raw func(*fileConfig) []string) setting {
	s := setting{section: section, key: key}
	name := s.name()

	s.fromFile = func(c *Config, f *fileConfig) error {
		items := raw(f)
		if items == nil {
			return nil
		}
		*field(c) = append([]string(nil), items...)
		return nil
	}
	s.fromEnv = func(c *Config, text string) error {
		parts := strings.Split(text, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			out = append(out, strings.TrimSpace(p))
		}
		*field(c) = out
		return nil
	}
	s.check = func(c *Config) error {
		values := *field(c)
		if len(values) == 0 {
			return fmt.Errorf("%w: %s", ErrEmptyList, name)
		}
		for i, v := range values {
			if strings.TrimSpace(v) == "" {
				return fmt.Errorf("%w: %s[%d]", ErrEmpty, name, i)
			}
		}
		return nil
	}
	return s
}

// durationListSetting wires a schedule. The ordering invariant lives in
// Validate; this check covers only what is wrong with the entries themselves.
func durationListSetting(section, key string, field func(*Config) *[]time.Duration, raw func(*fileConfig) []string) setting {
	s := setting{section: section, key: key}
	name := s.name()

	s.fromFile = func(c *Config, f *fileConfig) error {
		items := raw(f)
		if items == nil {
			return nil
		}

		out := make([]time.Duration, 0, len(items))
		for i, item := range items {
			d, err := parseDuration(fmt.Sprintf("%s[%d]", name, i), item)
			if err != nil {
				return err
			}
			out = append(out, d)
		}
		*field(c) = out
		return nil
	}
	s.fromEnv = func(c *Config, text string) error {
		out, err := parseDurationList(s.envName(), text)
		if err != nil {
			return err
		}
		*field(c) = out
		return nil
	}
	s.check = func(c *Config) error {
		values := *field(c)
		if len(values) == 0 {
			return fmt.Errorf("%w: %s", ErrEmptyList, name)
		}
		for i, v := range values {
			if v <= 0 {
				return fmt.Errorf("%w: %s[%d] is %s", ErrNotPositive, name, i, v)
			}
		}
		return nil
	}
	return s
}

// moneyListSetting wires a list of amounts in minor units, such as the quick
// amounts on the bank's buttons. From the environment it is comma-separated.
// Its check rejects an empty list and any amount of zero or below.
func moneyListSetting(section, key string, field func(*Config) *[]int64, raw func(*fileConfig) []int64) setting {
	s := setting{section: section, key: key}
	name := s.name()

	s.fromFile = func(c *Config, f *fileConfig) error {
		items := raw(f)
		if items == nil {
			return nil
		}
		*field(c) = append([]int64(nil), items...)
		return nil
	}
	s.fromEnv = func(c *Config, text string) error {
		parts := strings.Split(text, ",")
		out := make([]int64, 0, len(parts))
		for i, p := range parts {
			v, err := parseInt64(fmt.Sprintf("%s[%d]", s.envName(), i), strings.TrimSpace(p))
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		*field(c) = out
		return nil
	}
	s.check = func(c *Config) error {
		values := *field(c)
		if len(values) == 0 {
			return fmt.Errorf("%w: %s", ErrEmptyList, name)
		}
		for i, v := range values {
			if v <= 0 {
				return fmt.Errorf("%w: %s[%d] is %d", ErrNotPositive, name, i, v)
			}
		}
		return nil
	}
	return s
}

// aliasSetting marks a legacy key that still fills a field a current key
// owns. It reads the file and the environment like the setting it wraps, and
// checks nothing of its own: the current key's setting checks the field.
func aliasSetting(s setting) setting {
	s.check = func(*Config) error { return nil }
	return s
}

// settings is the whole configurable surface of this project, in the order
// configs/config.yml declares it.
var settings = []setting{
	durationSetting("gateway", "poll_timeout",
		func(c *Config) *time.Duration { return &c.Gateway.PollTimeout },
		func(f *fileConfig) *string { return f.Gateway.PollTimeout }),
	durationSetting("gateway", "poll_error_backoff",
		func(c *Config) *time.Duration { return &c.Gateway.PollErrorBackoff },
		func(f *fileConfig) *string { return f.Gateway.PollErrorBackoff }),
	durationSetting("gateway", "shutdown_timeout",
		func(c *Config) *time.Duration { return &c.Gateway.ShutdownTimeout },
		func(f *fileConfig) *string { return f.Gateway.ShutdownTimeout }),
	limitSetting("gateway", "send_attempts",
		func(c *Config) *int { return &c.Gateway.SendAttempts },
		func(f *fileConfig) *int { return f.Gateway.SendAttempts }),

	durationSetting("lease", "ttl",
		func(c *Config) *time.Duration { return &c.Lease.TTL },
		func(f *fileConfig) *string { return f.Lease.TTL }),
	limitSetting("lease", "renew_divisor",
		func(c *Config) *int { return &c.Lease.RenewDivisor },
		func(f *fileConfig) *int { return f.Lease.RenewDivisor }),
	durationSetting("lease", "release_timeout",
		func(c *Config) *time.Duration { return &c.Lease.ReleaseTimeout },
		func(f *fileConfig) *string { return f.Lease.ReleaseTimeout }),

	limitSetting("ratelimit", "default_rate",
		func(c *Config) *int { return &c.RateLimit.DefaultRate },
		func(f *fileConfig) *int { return f.RateLimit.DefaultRate }),
	limitSetting("ratelimit", "default_burst",
		func(c *Config) *int { return &c.RateLimit.DefaultBurst },
		func(f *fileConfig) *int { return f.RateLimit.DefaultBurst }),

	durationSetting("telegram", "request_timeout",
		func(c *Config) *time.Duration { return &c.Telegram.RequestTimeout },
		func(f *fileConfig) *string { return f.Telegram.RequestTimeout }),
	durationSetting("telegram", "max_poll_timeout",
		func(c *Config) *time.Duration { return &c.Telegram.MaxPollTimeout },
		func(f *fileConfig) *string { return f.Telegram.MaxPollTimeout }),
	durationSetting("telegram", "poll_timeout_grace",
		func(c *Config) *time.Duration { return &c.Telegram.PollTimeoutGrace },
		func(f *fileConfig) *string { return f.Telegram.PollTimeoutGrace }),
	durationSetting("telegram", "default_flood_wait",
		func(c *Config) *time.Duration { return &c.Telegram.DefaultFloodWait },
		func(f *fileConfig) *string { return f.Telegram.DefaultFloodWait }),
	limitSetting("groups", "callback_alert_max_runes",
		func(c *Config) *int { return &c.Groups.CallbackAlertMaxRunes },
		func(f *fileConfig) *int { return f.Groups.CallbackAlertMaxRunes }),
	durationSetting("groups", "deep_link_ttl",
		func(c *Config) *time.Duration { return &c.Groups.DeepLinkTTL },
		func(f *fileConfig) *string { return f.Groups.DeepLinkTTL }),
	stringListSetting("menu", "commands",
		func(c *Config) *[]string { return &c.Menu.Commands },
		func(f *fileConfig) []string { return f.Menu.Commands }),

	durationSetting("dedup", "ttl",
		func(c *Config) *time.Duration { return &c.Dedup.TTL },
		func(f *fileConfig) *string { return f.Dedup.TTL }),

	durationSetting("nats", "command_max_age",
		func(c *Config) *time.Duration { return &c.NATS.CommandMaxAge },
		func(f *fileConfig) *string { return f.NATS.CommandMaxAge }),
	durationSetting("nats", "event_max_age",
		func(c *Config) *time.Duration { return &c.NATS.EventMaxAge },
		func(f *fileConfig) *string { return f.NATS.EventMaxAge }),
	durationSetting("nats", "duplicate_window",
		func(c *Config) *time.Duration { return &c.NATS.DuplicateWindow },
		func(f *fileConfig) *string { return f.NATS.DuplicateWindow }),
	durationSetting("nats", "ack_wait",
		func(c *Config) *time.Duration { return &c.NATS.AckWait },
		func(f *fileConfig) *string { return f.NATS.AckWait }),
	limitSetting("nats", "max_deliver",
		func(c *Config) *int { return &c.NATS.MaxDeliver },
		func(f *fileConfig) *int { return f.NATS.MaxDeliver }),
	durationSetting("nats", "nak_delay",
		func(c *Config) *time.Duration { return &c.NATS.NakDelay },
		func(f *fileConfig) *string { return f.NATS.NakDelay }),
	durationListSetting("nats", "backoff",
		func(c *Config) *[]time.Duration { return &c.NATS.Backoff },
		func(f *fileConfig) []string { return f.NATS.Backoff }),

	durationSetting("worker", "poll_interval",
		func(c *Config) *time.Duration { return &c.Worker.PollInterval },
		func(f *fileConfig) *string { return f.Worker.PollInterval }),
	limitSetting("worker", "batch_size",
		func(c *Config) *int { return &c.Worker.BatchSize },
		func(f *fileConfig) *int { return f.Worker.BatchSize }),
	durationSetting("worker", "shutdown_timeout",
		func(c *Config) *time.Duration { return &c.Worker.ShutdownTimeout },
		func(f *fileConfig) *string { return f.Worker.ShutdownTimeout }),
	limitSetting("worker", "noisy_attempts",
		func(c *Config) *int { return &c.Worker.NoisyAttempts },
		func(f *fileConfig) *int { return f.Worker.NoisyAttempts }),

	durationSetting("scheduler", "tick_interval",
		func(c *Config) *time.Duration { return &c.Scheduler.TickInterval },
		func(f *fileConfig) *string { return f.Scheduler.TickInterval }),
	limitSetting("scheduler", "batch_size",
		func(c *Config) *int { return &c.Scheduler.BatchSize },
		func(f *fileConfig) *int { return f.Scheduler.BatchSize }),
	durationSetting("scheduler", "shutdown_timeout",
		func(c *Config) *time.Duration { return &c.Scheduler.ShutdownTimeout },
		func(f *fileConfig) *string { return f.Scheduler.ShutdownTimeout }),
	limitSetting("scheduler", "noisy_attempts",
		func(c *Config) *int { return &c.Scheduler.NoisyAttempts },
		func(f *fileConfig) *int { return f.Scheduler.NoisyAttempts }),
	durationSetting("scheduler", "claim_timeout",
		func(c *Config) *time.Duration { return &c.Scheduler.ClaimTimeout },
		func(f *fileConfig) *string { return f.Scheduler.ClaimTimeout }),

	durationSetting("notifier", "send_budget",
		func(c *Config) *time.Duration { return &c.Notifier.SendBudget },
		func(f *fileConfig) *string { return f.Notifier.SendBudget }),
	durationSetting("notifier", "receipt_margin",
		func(c *Config) *time.Duration { return &c.Notifier.ReceiptMargin },
		func(f *fileConfig) *string { return f.Notifier.ReceiptMargin }),
	durationSetting("notifier", "max_age",
		func(c *Config) *time.Duration { return &c.Notifier.MaxAge },
		func(f *fileConfig) *string { return f.Notifier.MaxAge }),
	durationSetting("notifier", "shutdown_timeout",
		func(c *Config) *time.Duration { return &c.Notifier.ShutdownTimeout },
		func(f *fileConfig) *string { return f.Notifier.ShutdownTimeout }),

	durationSetting("game", "shutdown_timeout",
		func(c *Config) *time.Duration { return &c.Game.ShutdownTimeout },
		func(f *fileConfig) *string { return f.Game.ShutdownTimeout }),
	durationSetting("game", "idempotency_ttl",
		func(c *Config) *time.Duration { return &c.Game.IdempotencyTTL },
		func(f *fileConfig) *string { return f.Game.IdempotencyTTL }),
	durationSetting("game", "content_reload_interval",
		func(c *Config) *time.Duration { return &c.Game.ContentReloadInterval },
		func(f *fileConfig) *string { return f.Game.ContentReloadInterval }),

	// The legacy spelling of the game clock comes BEFORE game.time_scale, so
	// the current key wins wherever both are written.
	aliasSetting(limitSetting("travel", "time_scale",
		func(c *Config) *int { return &c.Game.TimeScale },
		func(f *fileConfig) *int { return f.Travel.TimeScale })),
	limitSetting("game", "time_scale",
		func(c *Config) *int { return &c.Game.TimeScale },
		func(f *fileConfig) *int { return f.Game.TimeScale }),

	limitSetting("travel", "arrival_xp",
		func(c *Config) *int { return &c.Travel.ArrivalXP },
		func(f *fileConfig) *int { return f.Travel.ArrivalXP }),

	stringSetting("player", "default_language",
		func(c *Config) *string { return &c.Player.DefaultLanguage },
		func(f *fileConfig) *string { return f.Player.DefaultLanguage }),
	stringSetting("player", "default_timezone",
		func(c *Config) *string { return &c.Player.DefaultTimezone },
		func(f *fileConfig) *string { return f.Player.DefaultTimezone }),

	moneySetting("economy", "starting_cash",
		func(c *Config) *int64 { return &c.Economy.StartingCash },
		func(f *fileConfig) *int64 { return f.Economy.StartingCash }),
	moneySetting("economy", "bank_min_amount",
		func(c *Config) *int64 { return &c.Economy.BankMinAmount },
		func(f *fileConfig) *int64 { return f.Economy.BankMinAmount }),
	moneySetting("economy", "bank_max_amount",
		func(c *Config) *int64 { return &c.Economy.BankMaxAmount },
		func(f *fileConfig) *int64 { return f.Economy.BankMaxAmount }),
	moneyListSetting("economy", "bank_quick_amounts",
		func(c *Config) *[]int64 { return &c.Economy.BankQuickAmounts },
		func(f *fileConfig) []int64 { return f.Economy.BankQuickAmounts }),

	limitSetting("governance", "fine_step_divisor",
		func(c *Config) *int { return &c.Governance.FineStepDivisor },
		func(f *fileConfig) *int { return f.Governance.FineStepDivisor }),
	limitSetting("governance", "coarse_step_divisor",
		func(c *Config) *int { return &c.Governance.CoarseStepDivisor },
		func(f *fileConfig) *int { return f.Governance.CoarseStepDivisor }),

	limitSetting("crime", "nerve_max",
		func(c *Config) *int { return &c.Crime.NerveMax },
		func(f *fileConfig) *int { return f.Crime.NerveMax }),
	limitSetting("crime", "nerve_regen_amount",
		func(c *Config) *int { return &c.Crime.NerveRegenAmount },
		func(f *fileConfig) *int { return f.Crime.NerveRegenAmount }),
	durationSetting("crime", "nerve_regen_interval",
		func(c *Config) *time.Duration { return &c.Crime.NerveRegenInterval },
		func(f *fileConfig) *string { return f.Crime.NerveRegenInterval }),
	limitSetting("crime", "heat_max",
		func(c *Config) *int { return &c.Crime.HeatMax },
		func(f *fileConfig) *int { return f.Crime.HeatMax }),
	limitSetting("crime", "heat_decay_per_hour",
		func(c *Config) *int { return &c.Crime.HeatDecayPerHour },
		func(f *fileConfig) *int { return f.Crime.HeatDecayPerHour }),
	limitSetting("crime", "protect_min_level",
		func(c *Config) *int { return &c.Crime.ProtectMinLevel },
		func(f *fileConfig) *int { return f.Crime.ProtectMinLevel }),
	durationSetting("crime", "protect_min_age",
		func(c *Config) *time.Duration { return &c.Crime.ProtectMinAge },
		func(f *fileConfig) *string { return f.Crime.ProtectMinAge }),
	durationSetting("crime", "active_window",
		func(c *Config) *time.Duration { return &c.Crime.ActiveWindow },
		func(f *fileConfig) *string { return f.Crime.ActiveWindow }),
	durationSetting("crime", "arrival_linger",
		func(c *Config) *time.Duration { return &c.Crime.ArrivalLinger },
		func(f *fileConfig) *string { return f.Crime.ArrivalLinger }),
	durationSetting("crime", "victim_cooldown",
		func(c *Config) *time.Duration { return &c.Crime.VictimCooldown },
		func(f *fileConfig) *string { return f.Crime.VictimCooldown }),
	durationSetting("crime", "thief_cooldown",
		func(c *Config) *time.Duration { return &c.Crime.ThiefCooldown },
		func(f *fileConfig) *string { return f.Crime.ThiefCooldown }),
	durationSetting("crime", "report_window",
		func(c *Config) *time.Duration { return &c.Crime.ReportWindow },
		func(f *fileConfig) *string { return f.Crime.ReportWindow }),
	durationSetting("crime", "investigation_duration",
		func(c *Config) *time.Duration { return &c.Crime.InvestigationDuration },
		func(f *fileConfig) *string { return f.Crime.InvestigationDuration }),
	limitSetting("crime", "investigation_base_bps",
		func(c *Config) *int { return &c.Crime.InvestigationBaseBPS },
		func(f *fileConfig) *int { return f.Crime.InvestigationBaseBPS }),
	limitSetting("crime", "investigation_per_heat_bps",
		func(c *Config) *int { return &c.Crime.InvestigationPerHeatBPS },
		func(f *fileConfig) *int { return f.Crime.InvestigationPerHeatBPS }),
	limitSetting("crime", "investigation_witness_bonus_bps",
		func(c *Config) *int { return &c.Crime.InvestigationWitnessBonusBPS },
		func(f *fileConfig) *int { return f.Crime.InvestigationWitnessBonusBPS }),
	limitSetting("crime", "investigation_effort_weight_bps",
		func(c *Config) *int { return &c.Crime.InvestigationEffortWeightBPS },
		func(f *fileConfig) *int { return f.Crime.InvestigationEffortWeightBPS }),
	moneySetting("crime", "npc_daily_cap",
		func(c *Config) *int64 { return &c.Crime.NPCDailyCap },
		func(f *fileConfig) *int64 { return f.Crime.NPCDailyCap }),
	limitSetting("crime", "gear_max_success_bps",
		func(c *Config) *int { return &c.Crime.GearMaxSuccessBPS },
		func(f *fileConfig) *int { return f.Crime.GearMaxSuccessBPS }),
	limitSetting("crime", "gear_max_catch_bps",
		func(c *Config) *int { return &c.Crime.GearMaxCatchBPS },
		func(f *fileConfig) *int { return f.Crime.GearMaxCatchBPS }),
	limitSetting("crime", "gear_max_witness_bps",
		func(c *Config) *int { return &c.Crime.GearMaxWitnessBPS },
		func(f *fileConfig) *int { return f.Crime.GearMaxWitnessBPS }),
	limitSetting("crime", "gear_max_solve_bps",
		func(c *Config) *int { return &c.Crime.GearMaxSolveBPS },
		func(f *fileConfig) *int { return f.Crime.GearMaxSolveBPS }),
	limitSetting("crime", "gear_max_reward_bps",
		func(c *Config) *int { return &c.Crime.GearMaxRewardBPS },
		func(f *fileConfig) *int { return f.Crime.GearMaxRewardBPS }),
	limitSetting("crime", "gear_max_nerve",
		func(c *Config) *int { return &c.Crime.GearMaxNerve },
		func(f *fileConfig) *int { return f.Crime.GearMaxNerve }),

	durationSetting("trade", "market_order_ttl",
		func(c *Config) *time.Duration { return &c.Trade.MarketOrderTTL },
		func(f *fileConfig) *string { return f.Trade.MarketOrderTTL }),
	limitSetting("trade", "market_max_open_orders",
		func(c *Config) *int { return &c.Trade.MarketMaxOpenOrders },
		func(f *fileConfig) *int { return f.Trade.MarketMaxOpenOrders }),
	limitSetting("trade", "market_max_quantity",
		func(c *Config) *int { return &c.Trade.MarketMaxQuantity },
		func(f *fileConfig) *int { return f.Trade.MarketMaxQuantity }),
	moneySetting("trade", "market_max_price",
		func(c *Config) *int64 { return &c.Trade.MarketMaxPrice },
		func(f *fileConfig) *int64 { return f.Trade.MarketMaxPrice }),
	durationListSetting("trade", "auction_durations",
		func(c *Config) *[]time.Duration { return &c.Trade.AuctionDurations },
		func(f *fileConfig) []string { return f.Trade.AuctionDurations }),
	moneySetting("trade", "auction_max_reserve",
		func(c *Config) *int64 { return &c.Trade.AuctionMaxReserve },
		func(f *fileConfig) *int64 { return f.Trade.AuctionMaxReserve }),
	limitSetting("trade", "auction_step_bps",
		func(c *Config) *int { return &c.Trade.AuctionStepBPS },
		func(f *fileConfig) *int { return f.Trade.AuctionStepBPS }),
	moneySetting("trade", "auction_min_step",
		func(c *Config) *int64 { return &c.Trade.AuctionMinStep },
		func(f *fileConfig) *int64 { return f.Trade.AuctionMinStep }),
	limitSetting("trade", "auction_max_open",
		func(c *Config) *int { return &c.Trade.AuctionMaxOpen },
		func(f *fileConfig) *int { return f.Trade.AuctionMaxOpen }),
	moneyListSetting("trade", "auction_reserves_bps",
		func(c *Config) *[]int64 { return &c.Trade.AuctionReservesBPS },
		func(f *fileConfig) []int64 { return f.Trade.AuctionReservesBPS }),

	durationSetting("company", "period",
		func(c *Config) *time.Duration { return &c.Company.Period },
		func(f *fileConfig) *string { return f.Company.Period }),
	limitSetting("company", "max_per_player",
		func(c *Config) *int { return &c.Company.MaxPerPlayer },
		func(f *fileConfig) *int { return f.Company.MaxPerPlayer }),
	limitSetting("company", "name_min_length",
		func(c *Config) *int { return &c.Company.NameMinLength },
		func(f *fileConfig) *int { return f.Company.NameMinLength }),
	limitSetting("company", "name_max_length",
		func(c *Config) *int { return &c.Company.NameMaxLength },
		func(f *fileConfig) *int { return f.Company.NameMaxLength }),
	moneySetting("company", "founding_shares",
		func(c *Config) *int64 { return &c.Company.FoundingShares },
		func(f *fileConfig) *int64 { return f.Company.FoundingShares }),
	limitSetting("company", "insolvency_periods",
		func(c *Config) *int { return &c.Company.InsolvencyPeriods },
		func(f *fileConfig) *int { return f.Company.InsolvencyPeriods }),
	moneySetting("company", "npc_city_period_cap",
		func(c *Config) *int64 { return &c.Company.NPCCityPeriodCap },
		func(f *fileConfig) *int64 { return f.Company.NPCCityPeriodCap }),
	limitSetting("company", "max_openings",
		func(c *Config) *int { return &c.Company.MaxOpenings },
		func(f *fileConfig) *int { return f.Company.MaxOpenings }),
	limitSetting("company", "price_step_bps",
		func(c *Config) *int { return &c.Company.PriceStepBPS },
		func(f *fileConfig) *int { return f.Company.PriceStepBPS }),
	limitSetting("company", "citizen_shifts_per_period",
		func(c *Config) *int { return &c.Company.CitizenShiftsPerPeriod },
		func(f *fileConfig) *int { return f.Company.CitizenShiftsPerPeriod }),
	limitSetting("company", "citizen_productivity_bps",
		func(c *Config) *int { return &c.Company.CitizenProductivityBPS },
		func(f *fileConfig) *int { return f.Company.CitizenProductivityBPS }),
	limitSetting("company", "citizen_labour_share_bps",
		func(c *Config) *int { return &c.Company.CitizenLabourShareBPS },
		func(f *fileConfig) *int { return f.Company.CitizenLabourShareBPS }),
	limitSetting("company", "max_running_orders",
		func(c *Config) *int { return &c.Company.MaxRunningOrders },
		func(f *fileConfig) *int { return f.Company.MaxRunningOrders }),
	limitSetting("company", "max_designs",
		func(c *Config) *int { return &c.Company.MaxDesigns },
		func(f *fileConfig) *int { return f.Company.MaxDesigns }),
	limitSetting("company", "max_listings",
		func(c *Config) *int { return &c.Company.MaxListings },
		func(f *fileConfig) *int { return f.Company.MaxListings }),
	limitSetting("company", "design_min_skill",
		func(c *Config) *int { return &c.Company.DesignMinSkill },
		func(f *fileConfig) *int { return f.Company.DesignMinSkill }),
	durationSetting("company", "reverse_time",
		func(c *Config) *time.Duration { return &c.Company.ReverseTime },
		func(f *fileConfig) *string { return f.Company.ReverseTime }),

	durationSetting("military", "period",
		func(c *Config) *time.Duration { return &c.Military.Period },
		func(f *fileConfig) *string { return f.Military.Period }),
	limitSetting("military", "readiness_loss_bps",
		func(c *Config) *int { return &c.Military.ReadinessLossBPS },
		func(f *fileConfig) *int { return f.Military.ReadinessLossBPS }),
	limitSetting("military", "readiness_recovery_bps",
		func(c *Config) *int { return &c.Military.ReadinessRecoveryBPS },
		func(f *fileConfig) *int { return f.Military.ReadinessRecoveryBPS }),
	limitSetting("military", "reference_radar_km",
		func(c *Config) *int { return &c.Military.ReferenceRadarKM },
		func(f *fileConfig) *int { return f.Military.ReferenceRadarKM }),

	durationSetting("diplomacy", "sanction_notice",
		func(c *Config) *time.Duration { return &c.Diplomacy.SanctionNotice },
		func(f *fileConfig) *string { return f.Diplomacy.SanctionNotice }),
	durationSetting("diplomacy", "sanction_min_duration",
		func(c *Config) *time.Duration { return &c.Diplomacy.SanctionMinDuration },
		func(f *fileConfig) *string { return f.Diplomacy.SanctionMinDuration }),
	durationSetting("diplomacy", "treaty_offer_ttl",
		func(c *Config) *time.Duration { return &c.Diplomacy.TreatyOfferTTL },
		func(f *fileConfig) *string { return f.Diplomacy.TreatyOfferTTL }),
	durationSetting("diplomacy", "ended_shown_for",
		func(c *Config) *time.Duration { return &c.Diplomacy.EndedShownFor },
		func(f *fileConfig) *string { return f.Diplomacy.EndedShownFor }),
	limitSetting("diplomacy", "history_page_size",
		func(c *Config) *int { return &c.Diplomacy.HistoryPageSize },
		func(f *fileConfig) *int { return f.Diplomacy.HistoryPageSize }),

	durationSetting("input", "ttl",
		func(c *Config) *time.Duration { return &c.Input.TTL },
		func(f *fileConfig) *string { return f.Input.TTL }),
	durationSetting("input", "cooldown",
		func(c *Config) *time.Duration { return &c.Input.Cooldown },
		func(f *fileConfig) *string { return f.Input.Cooldown }),
	limitSetting("input", "max_length",
		func(c *Config) *int { return &c.Input.MaxLength },
		func(f *fileConfig) *int { return f.Input.MaxLength }),

	durationSetting("announce", "window",
		func(c *Config) *time.Duration { return &c.Announce.Window },
		func(f *fileConfig) *string { return f.Announce.Window }),
	limitSetting("announce", "max_per_window",
		func(c *Config) *int { return &c.Announce.MaxPerWindow },
		func(f *fileConfig) *int { return f.Announce.MaxPerWindow }),
}
