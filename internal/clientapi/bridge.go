package clientapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/gateway/routing"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// A client's command goes through the very pipeline a Telegram chat's does:
// the command envelope is published to the command stream with the player's
// identity, the game core runs it and publishes the presentation model on
// the request's response subject, and the client API, which subscribed to
// that subject before publishing, reads it and answers the HTTP request.
// The envelope's chat type is envelope.ChatTypeClient, which the game
// serves like a private chat and the gateway leaves alone.

// Bus publishes a command and waits for its response.
type Bus interface {
	Request(ctx context.Context, subject string, env *envelope.Envelope) (*envelope.Envelope, error)
}

// ErrTimeout means the command's answer did not come in time. The command
// may still run: a retry with the same idempotency key does not run it
// twice.
var ErrTimeout = errors.New("clientapi: the command was not answered in time")

// NATSBus is the Bus over NATS: JetStream for the command, core NATS for the
// answer, as the gateway.
type NATSBus struct {
	nc  *natsgo.Conn
	pub application.Publisher
}

// NewNATSBus returns the bus.
func NewNATSBus(nc *natsgo.Conn, pub application.Publisher) *NATSBus {
	return &NATSBus{nc: nc, pub: pub}
}

// Request subscribes to the response subject, publishes, and waits.
func (b *NATSBus) Request(ctx context.Context, subject string, env *envelope.Envelope) (*envelope.Envelope, error) {
	sub, err := b.nc.SubscribeSync(subjects.Response(env.Metadata.RequestID))
	if err != nil {
		return nil, fmt.Errorf("clientapi: subscribing to the response: %w", err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	// The subscription must reach the server before the command can be
	// answered, or a fast answer is published to nobody.
	if err := b.nc.FlushWithContext(ctx); err != nil {
		return nil, fmt.Errorf("clientapi: registering the response subscription: %w", err)
	}
	if err := b.pub.Publish(ctx, subject, env); err != nil {
		return nil, err
	}
	msg, err := sub.NextMsgWithContext(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ErrTimeout
		}
		return nil, fmt.Errorf("clientapi: waiting for the response: %w", err)
	}
	var out envelope.Envelope
	if err := json.Unmarshal(msg.Data, &out); err != nil {
		return nil, fmt.Errorf("clientapi: the response is not decodable: %w", err)
	}
	return &out, nil
}

// CommandRequest is the body of POST /api/v1/command.
type CommandRequest struct {
	Command        string                     `json:"command"`
	Args           map[string]json.RawMessage `json:"args"`
	IdempotencyKey string                     `json:"idempotency_key"`
}

// Command refusals.
var (
	ErrUnknownCommand = errors.New("clientapi: no such command")
	ErrGroupOnly      = errors.New("clientapi: this command is played in a Telegram group")
	ErrBadArgs        = errors.New("clientapi: the arguments are not usable")
)

// Limits on what a command may carry.
const (
	maxArgs      = 16
	maxArgLength = 512
)

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

// Bridge turns a client's command into an envelope and its answer into a
// screen.
type Bridge struct {
	Bus    Bus
	Policy *groups.Policy
	// AllowGroupCommands is client.group_commands = allow.
	AllowGroupCommands bool
	Timeout            time.Duration
	InstanceID         string
	NewID              func() string
	Now                func() time.Time
}

// Screen is the answer to a command.
type Screen struct {
	OK        bool            `json:"ok"`
	RequestID string          `json:"request_id,omitempty"`
	Screen    string          `json:"screen,omitempty"`
	Text      string          `json:"text,omitempty"`
	View      json.RawMessage `json:"view,omitempty"`
	Actions   []Action        `json:"actions"`
	Notice    *Notice         `json:"notice,omitempty"`
	Error     *APIError       `json:"error,omitempty"`
}

// Notice is a short message that does not replace the screen (what
// Telegram shows as a toast on a pressed button).
type Notice struct {
	Text  string `json:"text"`
	Alert bool   `json:"alert,omitempty"`
}

// Run runs one command for the principal.
func (b *Bridge) Run(ctx context.Context, pr Principal, req CommandRequest) (Screen, error) {
	command := strings.TrimSpace(req.Command)
	if !commands.FromPlayerCommand(command) {
		return Screen{}, ErrUnknownCommand
	}
	domain, action, err := routing.SplitCommand(command)
	if err != nil {
		return Screen{}, ErrUnknownCommand
	}
	if b.Policy.Channel(command) == groups.ChannelGroup && !b.AllowGroupCommands {
		return Screen{}, ErrGroupOnly
	}
	if pr.BotID == "" {
		return Screen{}, ErrNoBot
	}
	payload, err := NormalizeArgs(req.Args)
	if err != nil {
		return Screen{}, err
	}
	key := strings.TrimSpace(req.IdempotencyKey)
	if key != "" && !idempotencyKeyPattern.MatchString(key) {
		return Screen{}, fmt.Errorf("%w: idempotency_key", ErrBadArgs)
	}

	requestID := b.NewID()
	meta := envelope.Metadata{
		RequestID:         requestID,
		TraceID:           b.NewID(),
		PlayerID:          pr.PlayerID,
		TelegramUserID:    pr.TelegramUserID,
		TelegramChatID:    pr.TelegramUserID,
		BotID:             pr.BotID,
		GatewayInstanceID: b.InstanceID,
		ChatType:          envelope.ChatTypeClient,
		UpdateType:        "client_command",
		Command:           command,
		Action:            action,
		Language:          pr.Lang,
		ReceivedAt:        b.Now().UTC(),
		SchemaVersion:     envelope.SchemaVersion,
	}
	// Scoped to the device, so one client's key never collides with
	// another's; without one, every request is its own.
	if key != "" {
		meta.IdempotencyKey = "client:" + pr.DeviceID + ":" + key
	} else {
		meta.IdempotencyKey = "client:" + requestID
	}
	env, err := envelope.New(meta, payload)
	if err != nil {
		return Screen{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, b.Timeout)
	defer cancel()
	reply, err := b.Bus.Request(ctx, subjects.Command(domain, action), env)
	if err != nil {
		return Screen{}, err
	}
	var resp presenter.Response
	if err := reply.Decode(&resp); err != nil {
		return Screen{}, fmt.Errorf("clientapi: the response is not a screen: %w", err)
	}
	out := ScreenOf(&resp, command, b.Policy)
	out.RequestID = requestID
	return out, nil
}

// ScreenOf is what the client is shown for a response.
func ScreenOf(resp *presenter.Response, command string, policy *groups.Policy) Screen {
	if resp.Type == presenter.ActionAnswerCallback {
		return Screen{OK: true, Screen: "notice", Actions: []Action{},
			Notice: &Notice{Text: resp.Text, Alert: resp.Alert}}
	}
	screen := resp.Screen
	if screen == "" {
		screen = command
	}
	return Screen{OK: true, Screen: screen, Text: resp.Text, View: resp.View, Actions: Actions(resp.Keyboard, policy)}
}

// NormalizeArgs turns a client's arguments into the payload a command takes:
// every value a string, as a button's would be, or a list of strings.
// Numbers keep their exact digits; an object is refused.
func NormalizeArgs(args map[string]json.RawMessage) (map[string]any, error) {
	out := make(map[string]any, len(args))
	if len(args) > maxArgs {
		return nil, fmt.Errorf("%w: too many arguments", ErrBadArgs)
	}
	for k, raw := range args {
		if k == "" || len(k) > 32 {
			return nil, fmt.Errorf("%w: argument name %q", ErrBadArgs, k)
		}
		v, err := scalar(raw)
		if err == nil {
			if v != "" || string(raw) == `""` {
				out[k] = v
			}
			continue
		}
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) != nil || len(list) > maxArgs {
			return nil, fmt.Errorf("%w: %s", ErrBadArgs, k)
		}
		items := make([]string, 0, len(list))
		for _, item := range list {
			s, err := scalar(item)
			if err != nil {
				return nil, fmt.Errorf("%w: %s", ErrBadArgs, k)
			}
			items = append(items, s)
		}
		out[k] = items
	}
	return out, nil
}

// scalar reads a string, a number or a boolean as a string; null is "".
func scalar(raw json.RawMessage) (string, error) {
	text := strings.TrimSpace(string(raw))
	switch {
	case text == "null":
		return "", nil
	case text == "true" || text == "false":
		return text, nil
	case strings.HasPrefix(text, `"`):
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		if len(s) > maxArgLength {
			return "", ErrBadArgs
		}
		return s, nil
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return "", ErrBadArgs
	}
	if _, err := strconv.ParseInt(n.String(), 10, 64); err != nil {
		return "", ErrBadArgs
	}
	return n.String(), nil
}
