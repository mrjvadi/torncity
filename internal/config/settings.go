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
	EphemeralReplyWindow  *string  `yaml:"ephemeral_reply_window"`
	EphemeralRefusalTTL   *string  `yaml:"ephemeral_refusal_ttl"`
	CallbackAlertMaxRunes *int     `yaml:"callback_alert_max_runes"`
	Menu                  []string `yaml:"menu"`
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
}

type travelSettings struct {
	ArrivalXP *int `yaml:"arrival_xp"`
	TimeScale *int `yaml:"time_scale"`
}

type playerSettings struct {
	DefaultLanguage *string `yaml:"default_language"`
}

type economySettings struct {
	StartingCash  *int64 `yaml:"starting_cash"`
	BankMinAmount *int64 `yaml:"bank_min_amount"`
	BankMaxAmount *int64 `yaml:"bank_max_amount"`
}

type governanceSettings struct {
	FineStepDivisor   *int `yaml:"fine_step_divisor"`
	CoarseStepDivisor *int `yaml:"coarse_step_divisor"`
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
	durationSetting("groups", "ephemeral_reply_window",
		func(c *Config) *time.Duration { return &c.Groups.EphemeralReplyWindow },
		func(f *fileConfig) *string { return f.Groups.EphemeralReplyWindow }),
	durationSetting("groups", "ephemeral_refusal_ttl",
		func(c *Config) *time.Duration { return &c.Groups.EphemeralRefusalTTL },
		func(f *fileConfig) *string { return f.Groups.EphemeralRefusalTTL }),
	limitSetting("groups", "callback_alert_max_runes",
		func(c *Config) *int { return &c.Groups.CallbackAlertMaxRunes },
		func(f *fileConfig) *int { return f.Groups.CallbackAlertMaxRunes }),
	stringListSetting("groups", "menu",
		func(c *Config) *[]string { return &c.Groups.Menu },
		func(f *fileConfig) []string { return f.Groups.Menu }),

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

	limitSetting("travel", "arrival_xp",
		func(c *Config) *int { return &c.Travel.ArrivalXP },
		func(f *fileConfig) *int { return f.Travel.ArrivalXP }),
	limitSetting("travel", "time_scale",
		func(c *Config) *int { return &c.Travel.TimeScale },
		func(f *fileConfig) *int { return f.Travel.TimeScale }),

	stringSetting("player", "default_language",
		func(c *Config) *string { return &c.Player.DefaultLanguage },
		func(f *fileConfig) *string { return f.Player.DefaultLanguage }),

	moneySetting("economy", "starting_cash",
		func(c *Config) *int64 { return &c.Economy.StartingCash },
		func(f *fileConfig) *int64 { return f.Economy.StartingCash }),
	moneySetting("economy", "bank_min_amount",
		func(c *Config) *int64 { return &c.Economy.BankMinAmount },
		func(f *fileConfig) *int64 { return f.Economy.BankMinAmount }),
	moneySetting("economy", "bank_max_amount",
		func(c *Config) *int64 { return &c.Economy.BankMaxAmount },
		func(f *fileConfig) *int64 { return f.Economy.BankMaxAmount }),

	limitSetting("governance", "fine_step_divisor",
		func(c *Config) *int { return &c.Governance.FineStepDivisor },
		func(f *fileConfig) *int { return f.Governance.FineStepDivisor }),
	limitSetting("governance", "coarse_step_divisor",
		func(c *Config) *int { return &c.Governance.CoarseStepDivisor },
		func(f *fileConfig) *int { return f.Governance.CoarseStepDivisor }),
}
