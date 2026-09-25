package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// Client is the game client API (cmd/clientapi): where it listens, how long
// what it hands out lasts, and how hard it may be pushed. The signing
// secrets are environment (CLIENT_JWT_SECRET, CENTRIFUGO_TOKEN_HMAC_SECRET),
// never configuration. See api/client-api.md.
type Client struct {
	Listen string // client.listen
	// TrustedProxies are the direct peers whose X-Forwarded-For names the
	// client, for rate limiting.
	TrustedProxies []string      // client.trusted_proxies
	AccessTTL      time.Duration // client.access_ttl
	RefreshTTL     time.Duration // client.refresh_ttl
	// LinkCodeTTL is how long a /link code may be redeemed;
	// LinkCodesPerHour how many one player may ask for in an hour.
	LinkCodeTTL      time.Duration // client.link_code_ttl
	LinkCodesPerHour int           // client.link_codes_per_hour
	// SignInsPerMinute is how many sign-in attempts (a link code or Mini
	// App data) one address may make per minute: a code is guessed only by
	// trying.
	SignInsPerMinute int // client.sign_ins_per_minute
	// MaxDevices is how many clients one player may have linked at once;
	// linking one more signs the oldest out.
	MaxDevices int // client.max_devices
	// CommandTimeout is how long a command's answer is waited for.
	CommandTimeout    time.Duration // client.command_timeout
	CommandsPerMinute int           // client.commands_per_minute
	MaxBodyBytes      int           // client.max_body_bytes
	// TelegramAuthMaxAge is how old a Mini App's signed data may be.
	TelegramAuthMaxAge time.Duration // client.telegram_auth_max_age
	// RealtimeTokenTTL is how long a realtime connection or subscription
	// token lasts.
	RealtimeTokenTTL time.Duration // client.realtime_token_ttl
	// GroupCommands says what a command that is played only in a Telegram
	// group does from a client: "refuse" (answered group_only) or "allow".
	GroupCommands string // client.group_commands
	// MiniAppURL is the address of the Telegram Mini App build of the
	// client, empty until it is published.
	MiniAppURL string // client.mini_app_url
}

// Realtime is the realtime server (Centrifugo) the client API and the
// notifier publish through. The API key is environment
// (CENTRIFUGO_API_KEY); without it nothing is published.
type Realtime struct {
	APIURL         string        // realtime.api_url
	PublishTimeout time.Duration // realtime.publish_timeout
}

type clientSettings struct {
	Listen             *string  `yaml:"listen"`
	TrustedProxies     []string `yaml:"trusted_proxies"`
	AccessTTL          *string  `yaml:"access_ttl"`
	RefreshTTL         *string  `yaml:"refresh_ttl"`
	LinkCodeTTL        *string  `yaml:"link_code_ttl"`
	LinkCodesPerHour   *int     `yaml:"link_codes_per_hour"`
	SignInsPerMinute   *int     `yaml:"sign_ins_per_minute"`
	MaxDevices         *int     `yaml:"max_devices"`
	CommandTimeout     *string  `yaml:"command_timeout"`
	CommandsPerMinute  *int     `yaml:"commands_per_minute"`
	MaxBodyBytes       *int     `yaml:"max_body_bytes"`
	TelegramAuthMaxAge *string  `yaml:"telegram_auth_max_age"`
	RealtimeTokenTTL   *string  `yaml:"realtime_token_ttl"`
	GroupCommands      *string  `yaml:"group_commands"`
	MiniAppURL         *string  `yaml:"mini_app_url"`
}

type realtimeSettings struct {
	APIURL         *string `yaml:"api_url"`
	PublishTimeout *string `yaml:"publish_timeout"`
}

// The two answers client.group_commands takes.
const (
	GroupCommandsRefuse = "refuse"
	GroupCommandsAllow  = "allow"
)

// defaultClient is what configs/config.yml says.
func defaultClient() Client {
	return Client{
		Listen:             "127.0.0.1:8081",
		TrustedProxies:     []string{"127.0.0.1/32", "::1/128"},
		AccessTTL:          15 * time.Minute,
		RefreshTTL:         30 * 24 * time.Hour,
		LinkCodeTTL:        10 * time.Minute,
		LinkCodesPerHour:   5,
		SignInsPerMinute:   10,
		MaxDevices:         10,
		CommandTimeout:     15 * time.Second,
		CommandsPerMinute:  120,
		MaxBodyBytes:       16384,
		TelegramAuthMaxAge: time.Hour,
		RealtimeTokenTTL:   15 * time.Minute,
		GroupCommands:      GroupCommandsRefuse,
		MiniAppURL:         "",
	}
}

func defaultRealtime() Realtime {
	return Realtime{APIURL: "http://tc-centrifugo:8000/api", PublishTimeout: 2 * time.Second}
}

// ErrClient is a client API or realtime setting that cannot work.
var ErrClient = errors.New("config: invalid client setting")

func (c Client) validate() error {
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return fmt.Errorf("%w: client.listen %q is not host:port", ErrClient, c.Listen)
	}
	for _, cidr := range c.TrustedProxies {
		if _, _, err := net.ParseCIDR(strings.TrimSpace(cidr)); err != nil {
			return fmt.Errorf("%w: client.trusted_proxies %q is not a CIDR range", ErrClient, cidr)
		}
	}
	if c.AccessTTL >= c.RefreshTTL {
		return fmt.Errorf("%w: client.access_ttl %s must be shorter than client.refresh_ttl %s", ErrClient, c.AccessTTL, c.RefreshTTL)
	}
	if c.GroupCommands != GroupCommandsRefuse && c.GroupCommands != GroupCommandsAllow {
		return fmt.Errorf("%w: client.group_commands %q is neither %q nor %q", ErrClient, c.GroupCommands, GroupCommandsRefuse, GroupCommandsAllow)
	}
	if c.MiniAppURL != "" {
		u, err := url.Parse(c.MiniAppURL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("%w: client.mini_app_url %q is not an https address", ErrClient, c.MiniAppURL)
		}
	}
	return nil
}

func (r Realtime) validate() error {
	u, err := url.Parse(r.APIURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: realtime.api_url %q is not an http(s) address", ErrClient, r.APIURL)
	}
	return nil
}

// optional lets a string setting be empty.
func optional(s setting) setting {
	s.check = func(*Config) error { return nil }
	return s
}

// clientSettingsTable wires every client and realtime key; settings.go
// appends it.
func clientSettingsTable() []setting {
	return []setting{
		stringSetting("client", "listen",
			func(c *Config) *string { return &c.Client.Listen },
			func(f *fileConfig) *string { return f.Client.Listen }),
		stringListSetting("client", "trusted_proxies",
			func(c *Config) *[]string { return &c.Client.TrustedProxies },
			func(f *fileConfig) []string { return f.Client.TrustedProxies }),
		durationSetting("client", "access_ttl",
			func(c *Config) *time.Duration { return &c.Client.AccessTTL },
			func(f *fileConfig) *string { return f.Client.AccessTTL }),
		durationSetting("client", "refresh_ttl",
			func(c *Config) *time.Duration { return &c.Client.RefreshTTL },
			func(f *fileConfig) *string { return f.Client.RefreshTTL }),
		durationSetting("client", "link_code_ttl",
			func(c *Config) *time.Duration { return &c.Client.LinkCodeTTL },
			func(f *fileConfig) *string { return f.Client.LinkCodeTTL }),
		limitSetting("client", "link_codes_per_hour",
			func(c *Config) *int { return &c.Client.LinkCodesPerHour },
			func(f *fileConfig) *int { return f.Client.LinkCodesPerHour }),
		limitSetting("client", "sign_ins_per_minute",
			func(c *Config) *int { return &c.Client.SignInsPerMinute },
			func(f *fileConfig) *int { return f.Client.SignInsPerMinute }),
		limitSetting("client", "max_devices",
			func(c *Config) *int { return &c.Client.MaxDevices },
			func(f *fileConfig) *int { return f.Client.MaxDevices }),
		durationSetting("client", "command_timeout",
			func(c *Config) *time.Duration { return &c.Client.CommandTimeout },
			func(f *fileConfig) *string { return f.Client.CommandTimeout }),
		limitSetting("client", "commands_per_minute",
			func(c *Config) *int { return &c.Client.CommandsPerMinute },
			func(f *fileConfig) *int { return f.Client.CommandsPerMinute }),
		limitSetting("client", "max_body_bytes",
			func(c *Config) *int { return &c.Client.MaxBodyBytes },
			func(f *fileConfig) *int { return f.Client.MaxBodyBytes }),
		durationSetting("client", "telegram_auth_max_age",
			func(c *Config) *time.Duration { return &c.Client.TelegramAuthMaxAge },
			func(f *fileConfig) *string { return f.Client.TelegramAuthMaxAge }),
		durationSetting("client", "realtime_token_ttl",
			func(c *Config) *time.Duration { return &c.Client.RealtimeTokenTTL },
			func(f *fileConfig) *string { return f.Client.RealtimeTokenTTL }),
		stringSetting("client", "group_commands",
			func(c *Config) *string { return &c.Client.GroupCommands },
			func(f *fileConfig) *string { return f.Client.GroupCommands }),
		optional(stringSetting("client", "mini_app_url",
			func(c *Config) *string { return &c.Client.MiniAppURL },
			func(f *fileConfig) *string { return f.Client.MiniAppURL })),
		stringSetting("realtime", "api_url",
			func(c *Config) *string { return &c.Realtime.APIURL },
			func(f *fileConfig) *string { return f.Realtime.APIURL }),
		durationSetting("realtime", "publish_timeout",
			func(c *Config) *time.Duration { return &c.Realtime.PublishTimeout },
			func(f *fileConfig) *string { return f.Realtime.PublishTimeout }),
	}
}
