package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// Panel is the operators' web panel (cmd/panel): where it listens, the one
// public address it answers for, whom it trusts to name the client, and how
// sign-in and sessions are bounded. Accounts are rows (migrations/0030),
// created from the server only; no password or key is configuration.
type Panel struct {
	Listen             string        // panel.listen
	PublicURL          string        // panel.public_url
	TrustedProxies     []string      // panel.trusted_proxies
	SessionIdle        time.Duration // panel.session_idle
	SessionAbsolute    time.Duration // panel.session_absolute
	LoginPerMinute     int           // panel.login_per_minute
	LockoutAfter       int           // panel.lockout_after
	LockoutBase        time.Duration // panel.lockout_base
	LockoutMax         time.Duration // panel.lockout_max
	MutationsPerMinute int           // panel.mutations_per_minute
	MaxBodyBytes       int           // panel.max_body_bytes
	IdempotencyTTL     time.Duration // panel.idempotency_ttl
	PasswordMinLength  int           // panel.password_min_length
	TOTPIssuer         string        // panel.totp_issuer
	RequestTimeout     time.Duration // panel.request_timeout
	// The console's reads and live feed.
	ReadTimeout          time.Duration // panel.read_timeout
	ExportMaxRows        int           // panel.export_max_rows
	ModerationCacheTTL   time.Duration // panel.moderation_cache_ttl
	FeedInterval         time.Duration // panel.feed_interval
	KPIInterval          time.Duration // panel.kpi_interval
	RealtimeAPIURL       string        // panel.realtime_api_url
	RealtimeWebSocketURL string        // panel.realtime_websocket_url
	RealtimeTokenTTL     time.Duration // panel.realtime_token_ttl
	NATSMonitorURL       string        // panel.nats_monitor_url
}

type panelSettings struct {
	Listen             *string  `yaml:"listen"`
	PublicURL          *string  `yaml:"public_url"`
	TrustedProxies     []string `yaml:"trusted_proxies"`
	SessionIdle        *string  `yaml:"session_idle"`
	SessionAbsolute    *string  `yaml:"session_absolute"`
	LoginPerMinute     *int     `yaml:"login_per_minute"`
	LockoutAfter       *int     `yaml:"lockout_after"`
	LockoutBase        *string  `yaml:"lockout_base"`
	LockoutMax         *string  `yaml:"lockout_max"`
	MutationsPerMinute *int     `yaml:"mutations_per_minute"`
	MaxBodyBytes       *int     `yaml:"max_body_bytes"`
	IdempotencyTTL     *string  `yaml:"idempotency_ttl"`
	PasswordMinLength  *int     `yaml:"password_min_length"`
	TOTPIssuer         *string  `yaml:"totp_issuer"`
	RequestTimeout     *string  `yaml:"request_timeout"`

	ReadTimeout          *string `yaml:"read_timeout"`
	ExportMaxRows        *int    `yaml:"export_max_rows"`
	ModerationCacheTTL   *string `yaml:"moderation_cache_ttl"`
	FeedInterval         *string `yaml:"feed_interval"`
	KPIInterval          *string `yaml:"kpi_interval"`
	RealtimeAPIURL       *string `yaml:"realtime_api_url"`
	RealtimeWebSocketURL *string `yaml:"realtime_websocket_url"`
	RealtimeTokenTTL     *string `yaml:"realtime_token_ttl"`
	NATSMonitorURL       *string `yaml:"nats_monitor_url"`
}

// defaultPanel is what configs/config.yml says.
func defaultPanel() Panel {
	return Panel{
		Listen:             "127.0.0.1:8090",
		PublicURL:          "https://panelomm.ir404.site",
		TrustedProxies:     []string{"127.0.0.1/32", "::1/128"},
		SessionIdle:        30 * time.Minute,
		SessionAbsolute:    12 * time.Hour,
		LoginPerMinute:     10,
		LockoutAfter:       5,
		LockoutBase:        time.Minute,
		LockoutMax:         time.Hour,
		MutationsPerMinute: 30,
		MaxBodyBytes:       65536,
		IdempotencyTTL:     24 * time.Hour,
		PasswordMinLength:  12,
		TOTPIssuer:         "torncity",
		RequestTimeout:     2 * time.Minute,

		ReadTimeout:          8 * time.Second,
		ExportMaxRows:        10000,
		ModerationCacheTTL:   30 * time.Second,
		FeedInterval:         3 * time.Second,
		KPIInterval:          30 * time.Second,
		RealtimeAPIURL:       "http://tc-centrifugo:8000/api",
		RealtimeWebSocketURL: "/connection/websocket",
		RealtimeTokenTTL:     10 * time.Minute,
		NATSMonitorURL:       "http://tc-nats:8222",
	}
}

// ErrPanel is a panel setting that cannot work.
var ErrPanel = errors.New("config: invalid panel setting")

// validate checks what the field table cannot: the address, the URL, the
// proxy ranges and the order of the bounds.
func (p Panel) validate() error {
	if _, _, err := net.SplitHostPort(p.Listen); err != nil {
		return fmt.Errorf("%w: panel.listen %q is not host:port", ErrPanel, p.Listen)
	}
	u, err := url.Parse(p.PublicURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("%w: panel.public_url %q is not an http(s) origin", ErrPanel, p.PublicURL)
	}
	for _, cidr := range p.TrustedProxies {
		if _, _, err := net.ParseCIDR(strings.TrimSpace(cidr)); err != nil {
			return fmt.Errorf("%w: panel.trusted_proxies %q is not a CIDR range", ErrPanel, cidr)
		}
	}
	if p.SessionIdle > p.SessionAbsolute {
		return fmt.Errorf("%w: panel.session_idle %s exceeds panel.session_absolute %s", ErrPanel, p.SessionIdle, p.SessionAbsolute)
	}
	if p.LockoutBase > p.LockoutMax {
		return fmt.Errorf("%w: panel.lockout_base %s exceeds panel.lockout_max %s", ErrPanel, p.LockoutBase, p.LockoutMax)
	}
	if p.PasswordMinLength < 10 {
		return fmt.Errorf("%w: panel.password_min_length %d is below 10", ErrPanel, p.PasswordMinLength)
	}
	return nil
}

// panelSettingsTable wires every panel key; settings.go appends it.
func panelSettingsTable() []setting {
	return []setting{
		stringSetting("panel", "listen",
			func(c *Config) *string { return &c.Panel.Listen },
			func(f *fileConfig) *string { return f.Panel.Listen }),
		stringSetting("panel", "public_url",
			func(c *Config) *string { return &c.Panel.PublicURL },
			func(f *fileConfig) *string { return f.Panel.PublicURL }),
		stringListSetting("panel", "trusted_proxies",
			func(c *Config) *[]string { return &c.Panel.TrustedProxies },
			func(f *fileConfig) []string { return f.Panel.TrustedProxies }),
		durationSetting("panel", "session_idle",
			func(c *Config) *time.Duration { return &c.Panel.SessionIdle },
			func(f *fileConfig) *string { return f.Panel.SessionIdle }),
		durationSetting("panel", "session_absolute",
			func(c *Config) *time.Duration { return &c.Panel.SessionAbsolute },
			func(f *fileConfig) *string { return f.Panel.SessionAbsolute }),
		limitSetting("panel", "login_per_minute",
			func(c *Config) *int { return &c.Panel.LoginPerMinute },
			func(f *fileConfig) *int { return f.Panel.LoginPerMinute }),
		limitSetting("panel", "lockout_after",
			func(c *Config) *int { return &c.Panel.LockoutAfter },
			func(f *fileConfig) *int { return f.Panel.LockoutAfter }),
		durationSetting("panel", "lockout_base",
			func(c *Config) *time.Duration { return &c.Panel.LockoutBase },
			func(f *fileConfig) *string { return f.Panel.LockoutBase }),
		durationSetting("panel", "lockout_max",
			func(c *Config) *time.Duration { return &c.Panel.LockoutMax },
			func(f *fileConfig) *string { return f.Panel.LockoutMax }),
		limitSetting("panel", "mutations_per_minute",
			func(c *Config) *int { return &c.Panel.MutationsPerMinute },
			func(f *fileConfig) *int { return f.Panel.MutationsPerMinute }),
		limitSetting("panel", "max_body_bytes",
			func(c *Config) *int { return &c.Panel.MaxBodyBytes },
			func(f *fileConfig) *int { return f.Panel.MaxBodyBytes }),
		durationSetting("panel", "idempotency_ttl",
			func(c *Config) *time.Duration { return &c.Panel.IdempotencyTTL },
			func(f *fileConfig) *string { return f.Panel.IdempotencyTTL }),
		limitSetting("panel", "password_min_length",
			func(c *Config) *int { return &c.Panel.PasswordMinLength },
			func(f *fileConfig) *int { return f.Panel.PasswordMinLength }),
		stringSetting("panel", "totp_issuer",
			func(c *Config) *string { return &c.Panel.TOTPIssuer },
			func(f *fileConfig) *string { return f.Panel.TOTPIssuer }),
		durationSetting("panel", "request_timeout",
			func(c *Config) *time.Duration { return &c.Panel.RequestTimeout },
			func(f *fileConfig) *string { return f.Panel.RequestTimeout }),
		durationSetting("panel", "read_timeout",
			func(c *Config) *time.Duration { return &c.Panel.ReadTimeout },
			func(f *fileConfig) *string { return f.Panel.ReadTimeout }),
		limitSetting("panel", "export_max_rows",
			func(c *Config) *int { return &c.Panel.ExportMaxRows },
			func(f *fileConfig) *int { return f.Panel.ExportMaxRows }),
		durationSetting("panel", "moderation_cache_ttl",
			func(c *Config) *time.Duration { return &c.Panel.ModerationCacheTTL },
			func(f *fileConfig) *string { return f.Panel.ModerationCacheTTL }),
		durationSetting("panel", "feed_interval",
			func(c *Config) *time.Duration { return &c.Panel.FeedInterval },
			func(f *fileConfig) *string { return f.Panel.FeedInterval }),
		durationSetting("panel", "kpi_interval",
			func(c *Config) *time.Duration { return &c.Panel.KPIInterval },
			func(f *fileConfig) *string { return f.Panel.KPIInterval }),
		stringSetting("panel", "realtime_api_url",
			func(c *Config) *string { return &c.Panel.RealtimeAPIURL },
			func(f *fileConfig) *string { return f.Panel.RealtimeAPIURL }),
		stringSetting("panel", "realtime_websocket_url",
			func(c *Config) *string { return &c.Panel.RealtimeWebSocketURL },
			func(f *fileConfig) *string { return f.Panel.RealtimeWebSocketURL }),
		durationSetting("panel", "realtime_token_ttl",
			func(c *Config) *time.Duration { return &c.Panel.RealtimeTokenTTL },
			func(f *fileConfig) *string { return f.Panel.RealtimeTokenTTL }),
		stringSetting("panel", "nats_monitor_url",
			func(c *Config) *string { return &c.Panel.NATSMonitorURL },
			func(f *fileConfig) *string { return f.Panel.NATSMonitorURL }),
	}
}
