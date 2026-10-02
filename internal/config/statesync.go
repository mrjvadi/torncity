package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// StateSync is client state sync (docs/adr/0034-client-state-sync.md): the
// per-player update log, its endpoints and its push. The projector runs in
// cmd/notifier, the endpoints in cmd/clientapi; both read this section.
type StateSync struct {
	// Enabled turns the whole feature on: the projector, the endpoints, the
	// command answers' updates and bootstrap.features.updates (the switch a
	// client reads to use the store instead of polling).
	Enabled bool // state_sync.enabled
	// Epoch is the server's log epoch. Changing it voids every client's
	// cursor: each one resets to a fresh snapshot.
	Epoch string // state_sync.epoch
	// ResetThreshold: a client further behind than this many records is
	// told to reset rather than replay (Telegram's differenceTooLong).
	ResetThreshold int // state_sync.reset_threshold
	// PullLimit caps the records of one GET /updates answer.
	PullLimit int // state_sync.pull_limit
	// PullsPerMinute bounds GET /state and GET /updates per player.
	PullsPerMinute int // state_sync.pulls_per_minute
	// CommandWait is how long a command's answer waits for its own records
	// before it goes without them (the push brings them).
	CommandWait time.Duration // state_sync.command_wait
	// PushMaxRecords and PushMaxBytes: a batch bigger than either is not
	// pushed; a too_long poke is, and the client pulls.
	PushMaxRecords int // state_sync.push_max_records
	PushMaxBytes   int // state_sync.push_max_bytes
	// MaxEventAge: an older domain event is acknowledged without projecting.
	MaxEventAge time.Duration // state_sync.max_event_age
	// PlayerKeys are payload keys, besides any containing "player", that
	// name a player an event concerns.
	PlayerKeys []string // state_sync.player_keys
	// FanoutLimit bounds the players one settlement event re-projects.
	FanoutLimit int // state_sync.fanout_limit
	// CauseWindow is how many recent records are searched for a command's.
	CauseWindow int // state_sync.cause_window
	// NoticesKept is how many of the newest notices a client holds.
	NoticesKept int // state_sync.notices_kept
	// LockTimeout bounds a projection's wait for the player's lock.
	LockTimeout time.Duration // state_sync.lock_timeout
	// Retention: a record is kept while younger than RetentionAge or among
	// the player's last RetentionRecords, whichever keeps more, and never
	// fewer than RetentionMin. TrimInterval is how often the trim runs
	// (one replica at a time) and TrimBatch how many players one run takes.
	RetentionAge     time.Duration // state_sync.retention_age
	RetentionRecords int           // state_sync.retention_records
	RetentionMin     int           // state_sync.retention_min
	TrimInterval     time.Duration // state_sync.trim_interval
	TrimBatch        int           // state_sync.trim_batch
	// MetricsInterval is how often each process logs its counters.
	MetricsInterval time.Duration // state_sync.metrics_interval
}

type stateSyncSettings struct {
	Enabled          *bool    `yaml:"enabled"`
	Epoch            *string  `yaml:"epoch"`
	ResetThreshold   *int     `yaml:"reset_threshold"`
	PullLimit        *int     `yaml:"pull_limit"`
	PullsPerMinute   *int     `yaml:"pulls_per_minute"`
	CommandWait      *string  `yaml:"command_wait"`
	PushMaxRecords   *int     `yaml:"push_max_records"`
	PushMaxBytes     *int     `yaml:"push_max_bytes"`
	MaxEventAge      *string  `yaml:"max_event_age"`
	PlayerKeys       []string `yaml:"player_keys"`
	FanoutLimit      *int     `yaml:"fanout_limit"`
	CauseWindow      *int     `yaml:"cause_window"`
	NoticesKept      *int     `yaml:"notices_kept"`
	LockTimeout      *string  `yaml:"lock_timeout"`
	RetentionAge     *string  `yaml:"retention_age"`
	RetentionRecords *int     `yaml:"retention_records"`
	RetentionMin     *int     `yaml:"retention_min"`
	TrimInterval     *string  `yaml:"trim_interval"`
	TrimBatch        *int     `yaml:"trim_batch"`
	MetricsInterval  *string  `yaml:"metrics_interval"`
}

// defaultStateSync is what configs/config.yml says (the ADR's defaults).
func defaultStateSync() StateSync {
	return StateSync{
		Enabled:        true,
		Epoch:          "1",
		ResetThreshold: 2000,
		PullLimit:      500,
		PullsPerMinute: 240,
		CommandWait:    300 * time.Millisecond,
		PushMaxRecords: 100,
		PushMaxBytes:   32768,
		MaxEventAge:    10 * time.Minute,
		PlayerKeys: []string{"payee_id", "payer_id", "victim_id", "target_id", "buyer_id", "seller_id", "winner_id",
			"member_id", "holder_id", "recipient_id", "sender_id", "attacker_id", "defender_id", "candidate_id", "voter_id"},
		FanoutLimit:      500,
		CauseWindow:      200,
		NoticesKept:      50,
		LockTimeout:      2 * time.Second,
		RetentionAge:     7 * 24 * time.Hour,
		RetentionRecords: 5000,
		RetentionMin:     500,
		TrimInterval:     time.Hour,
		TrimBatch:        200,
		MetricsInterval:  5 * time.Minute,
	}
}

// ErrStateSync is a state sync setting that cannot work.
var ErrStateSync = errors.New("config: invalid state_sync setting")

func (s StateSync) validate() error {
	if s.RetentionMin > s.RetentionRecords {
		return fmt.Errorf("%w: state_sync.retention_min %d is above retention_records %d", ErrStateSync, s.RetentionMin, s.RetentionRecords)
	}
	if s.ResetThreshold > s.RetentionRecords {
		// a client allowed to replay more than is kept would be told "replay"
		// and then find the records gone
		return fmt.Errorf("%w: state_sync.reset_threshold %d is above retention_records %d", ErrStateSync, s.ResetThreshold, s.RetentionRecords)
	}
	return nil
}

// boolSetting wires a switch. "true"/"false" (and the other spellings
// strconv.ParseBool reads) from the environment; any value is valid.
func boolSetting(section, key string, field func(*Config) *bool, raw func(*fileConfig) *bool) setting {
	s := setting{section: section, key: key}
	s.fromFile = func(c *Config, f *fileConfig) error {
		if p := raw(f); p != nil {
			*field(c) = *p
		}
		return nil
	}
	s.fromEnv = func(c *Config, text string) error {
		v, err := strconv.ParseBool(strings.TrimSpace(text))
		if err != nil {
			return fmt.Errorf("%w: %s=%q is not true or false", ErrInvalidValue, s.envName(), text)
		}
		*field(c) = v
		return nil
	}
	s.check = func(*Config) error { return nil }
	return s
}

func stateSyncSettingsTable() []setting {
	sec := "state_sync"
	return []setting{
		boolSetting(sec, "enabled",
			func(c *Config) *bool { return &c.StateSync.Enabled },
			func(f *fileConfig) *bool { return f.StateSync.Enabled }),
		stringSetting(sec, "epoch",
			func(c *Config) *string { return &c.StateSync.Epoch },
			func(f *fileConfig) *string { return f.StateSync.Epoch }),
		limitSetting(sec, "reset_threshold",
			func(c *Config) *int { return &c.StateSync.ResetThreshold },
			func(f *fileConfig) *int { return f.StateSync.ResetThreshold }),
		limitSetting(sec, "pull_limit",
			func(c *Config) *int { return &c.StateSync.PullLimit },
			func(f *fileConfig) *int { return f.StateSync.PullLimit }),
		limitSetting(sec, "pulls_per_minute",
			func(c *Config) *int { return &c.StateSync.PullsPerMinute },
			func(f *fileConfig) *int { return f.StateSync.PullsPerMinute }),
		durationSetting(sec, "command_wait",
			func(c *Config) *time.Duration { return &c.StateSync.CommandWait },
			func(f *fileConfig) *string { return f.StateSync.CommandWait }),
		limitSetting(sec, "push_max_records",
			func(c *Config) *int { return &c.StateSync.PushMaxRecords },
			func(f *fileConfig) *int { return f.StateSync.PushMaxRecords }),
		limitSetting(sec, "push_max_bytes",
			func(c *Config) *int { return &c.StateSync.PushMaxBytes },
			func(f *fileConfig) *int { return f.StateSync.PushMaxBytes }),
		durationSetting(sec, "max_event_age",
			func(c *Config) *time.Duration { return &c.StateSync.MaxEventAge },
			func(f *fileConfig) *string { return f.StateSync.MaxEventAge }),
		stringListSetting(sec, "player_keys",
			func(c *Config) *[]string { return &c.StateSync.PlayerKeys },
			func(f *fileConfig) []string { return f.StateSync.PlayerKeys }),
		limitSetting(sec, "fanout_limit",
			func(c *Config) *int { return &c.StateSync.FanoutLimit },
			func(f *fileConfig) *int { return f.StateSync.FanoutLimit }),
		limitSetting(sec, "cause_window",
			func(c *Config) *int { return &c.StateSync.CauseWindow },
			func(f *fileConfig) *int { return f.StateSync.CauseWindow }),
		limitSetting(sec, "notices_kept",
			func(c *Config) *int { return &c.StateSync.NoticesKept },
			func(f *fileConfig) *int { return f.StateSync.NoticesKept }),
		durationSetting(sec, "lock_timeout",
			func(c *Config) *time.Duration { return &c.StateSync.LockTimeout },
			func(f *fileConfig) *string { return f.StateSync.LockTimeout }),
		durationSetting(sec, "retention_age",
			func(c *Config) *time.Duration { return &c.StateSync.RetentionAge },
			func(f *fileConfig) *string { return f.StateSync.RetentionAge }),
		limitSetting(sec, "retention_records",
			func(c *Config) *int { return &c.StateSync.RetentionRecords },
			func(f *fileConfig) *int { return f.StateSync.RetentionRecords }),
		limitSetting(sec, "retention_min",
			func(c *Config) *int { return &c.StateSync.RetentionMin },
			func(f *fileConfig) *int { return f.StateSync.RetentionMin }),
		durationSetting(sec, "trim_interval",
			func(c *Config) *time.Duration { return &c.StateSync.TrimInterval },
			func(f *fileConfig) *string { return f.StateSync.TrimInterval }),
		limitSetting(sec, "trim_batch",
			func(c *Config) *int { return &c.StateSync.TrimBatch },
			func(f *fileConfig) *int { return f.StateSync.TrimBatch }),
		durationSetting(sec, "metrics_interval",
			func(c *Config) *time.Duration { return &c.StateSync.MetricsInterval },
			func(f *fileConfig) *string { return f.StateSync.MetricsInterval }),
	}
}
