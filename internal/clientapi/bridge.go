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
	"github.com/mrjvadi/torncity/internal/gateway/input"
	"github.com/mrjvadi/torncity/internal/gateway/moderation"
	"github.com/mrjvadi/torncity/internal/gateway/routing"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/statesync"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
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
	// ErrBanned refuses a command from a player an operator has banned
	// (internal/gateway/moderation).
	ErrBanned = errors.New("clientapi: this account is banned")
)

// Limits on what a command may carry.
const (
	maxArgs = 16
	// maxListItems bounds a list argument (a batch of lots).
	maxListItems = 64
	maxArgLength = 512
)

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

// Bridge turns a client's command into an envelope and its answer into a
// screen.
type Bridge struct {
	Bus    Bus
	Policy *groups.Policy
	// ActionMeta is configs/actions.yml, loaded: the kind, icon and group
	// each action carries for a client that draws its own UI. Nil gives
	// every action the safe default (ActionMetadata.Of).
	ActionMeta *ActionMetadata
	// AllowGroupCommands is client.group_commands = allow.
	AllowGroupCommands bool
	// Moderation refuses a banned player's commands, as the gateway does.
	// Nil lets every command through; so does a failure to read it.
	Moderation *moderation.Checker
	// LegacyText, when set, gives a neutral answer the Telegram-rendered
	// `text` (and the button labels) a client deployed before it draws the
	// screen from the view still reads, for the screens
	// client.legacy_text_screens lists. It is the one place the client API
	// reaches for Telegram's wording, it is wired by cmd/clientapi alone, and
	// it goes away with the compatibility period (docs/adr/0037).
	LegacyText LegacyText
	// LegacyScreens are client.legacy_text_screens: "*" is every screen.
	LegacyScreens []string
	Timeout       time.Duration
	InstanceID    string
	NewID         func() string
	Now           func() time.Time
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
	// Updates are the state sync records this command caused, when they
	// were ready in time (contract 1.4, docs/adr/0034); the same records
	// also arrive by push and are dropped there by pts.
	Updates *statesync.CommandUpdates `json:"updates,omitempty"`
}

// Notice is a short message that does not replace the screen (what
// Telegram shows as a toast on a pressed button). A neutral notice is a code
// and its arguments; the client words it. Text is the legacy sentence.
type Notice struct {
	Code  string         `json:"code,omitempty"`
	Args  map[string]any `json:"args,omitempty"`
	Text  string         `json:"text,omitempty"`
	Alert bool           `json:"alert,omitempty"`
}

// LegacyRendering is what the compatibility hook returns: the screen's text,
// and the labels of its buttons by the address each opens.
type LegacyRendering struct {
	Text   string
	Labels map[string]string
}

// LegacyText renders a neutral response the way Telegram shows it.
type LegacyText func(ctx context.Context, resp *presentation.Response) (LegacyRendering, error)

func (b *Bridge) legacyFor(screen string) bool {
	if b.LegacyText == nil {
		return false
	}
	for _, s := range b.LegacyScreens {
		if s == "*" || s == screen {
			return true
		}
	}
	return false
}

// Run runs one command for the principal.
func (b *Bridge) Run(ctx context.Context, pr Principal, req CommandRequest) (Screen, error) {
	command := strings.TrimSpace(req.Command)
	command, req.Args = clientAlias(command, req.Args)
	if !commands.FromPlayerCommand(command) {
		return Screen{}, ErrUnknownCommand
	}
	domain, action, err := routing.SplitCommand(command)
	if err != nil {
		return Screen{}, ErrUnknownCommand
	}
	if b.Policy.Channel(command) == groups.ChannelGroup && !b.AllowGroupCommands && !b.Policy.ClientMay(command) {
		return Screen{}, ErrGroupOnly
	}
	if pr.BotID == "" {
		return Screen{}, ErrNoBot
	}
	if v, err := b.Moderation.Blocks(ctx, pr.TelegramUserID, false); err == nil && v == moderation.Banned {
		return Screen{}, ErrBanned
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
	out := ScreenOf(&resp, command, b.Policy, b.ActionMeta)
	if resp.Neutral() && b.legacyFor(out.Screen) {
		if lr, err := b.LegacyText(ctx, &resp); err == nil {
			out.Text = lr.Text
			if out.Error != nil && out.Error.Message == "" {
				out.Error.Message = lr.Text
			}
			if out.Notice != nil && out.Notice.Text == "" {
				out.Notice.Text = lr.Text
			}
			for i := range out.Actions {
				a := &out.Actions[i]
				if a.Label == "" {
					a.Label = lr.Labels[presentation.Action{Command: a.Command, Args: positional(a)}.Address()]
				}
				if a.Label == "" && a.Input != nil {
					// a button that asks for a value carries "ask:<command>:<fixed arguments>"
					a.Label = lr.Labels[b.askAddress(a)]
				}
			}
		}
	}
	if b.LegacyText != nil && !b.legacyFor(out.Screen) {
		// The compatibility period is over for this screen: whatever Telegram
		// wording a handler still produced stays at the Telegram edge.
		stripTelegramText(&out)
	}
	out.RequestID = requestID
	return out, nil
}

// stripTelegramText removes the Telegram-rendered text and labels from a
// screen: the client words every screen from its view and its actions.
func stripTelegramText(s *Screen) {
	s.Text = ""
	for i := range s.Actions {
		s.Actions[i].Label = ""
		s.Actions[i].Row = nil
	}
	if s.Error != nil {
		s.Error.Message = ""
	}
	if s.Notice != nil {
		s.Notice.Text = ""
	}
}

// askAddress is the callback data of the Telegram button an input action
// stands for: "ask", the command and its fixed arguments in the order the
// command's input table names them, trailing empty ones dropped.
func (b *Bridge) askAddress(a *Action) string {
	parts := []string{input.AskPrefix, a.Command}
	if spec, ok := b.Policy.Input(a.Command); ok {
		for _, name := range spec.Args {
			v, ok := a.Args[name]
			if !ok {
				break
			}
			parts = append(parts, fmt.Sprint(v))
		}
	}
	for len(parts) > 2 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, ":")
}

// positional is an action's arguments back in the positional order the game
// parses, for looking up its legacy label by address.
func positional(a *Action) []string {
	names := routing.ArgNames(a.Command)
	out := make([]string, 0, len(names))
	for _, n := range names {
		v, ok := a.Args[n]
		if !ok {
			break
		}
		out = append(out, fmt.Sprint(v))
	}
	return out
}

// ScreenOf is what the client is shown for a response.
func ScreenOf(resp *presenter.Response, command string, policy *groups.Policy, meta *ActionMetadata) Screen {
	if resp.Neutral() {
		return neutralScreenOf(resp, command, policy, meta)
	}
	if resp.Type == presenter.ActionAnswerCallback {
		return Screen{OK: true, Screen: "notice", Actions: []Action{},
			Notice: &Notice{Text: resp.Text, Alert: resp.Alert}}
	}
	screen := resp.Screen
	if screen == "" {
		screen = command
	}
	out := Screen{OK: true, Screen: screen, Text: resp.Text, View: resp.View, Actions: Actions(resp.Keyboard, policy, meta)}
	if screen == screens.ScreenVillageRefusal {
		// A refused village command is an error a client can act on: the
		// code names why (village_occupied, village_unbuildable, ...), the
		// text is the same sentence Telegram shows, in the player's language.
		var v struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(resp.View, &v) == nil && v.Kind != "" {
			out.OK = false
			out.Error = &APIError{Code: "village_" + v.Kind, Message: resp.Text}
		}
	}
	return out
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
		if json.Unmarshal(raw, &list) != nil || len(list) > maxListItems {
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

// clientAlias maps a command a client sends by the client contract's name
// onto the game's own: the city map's company plot sends company.show {id}
// (docs/architecture.md in the client), which the game serves as
// company.view {code}.
func clientAlias(command string, args map[string]json.RawMessage) (string, map[string]json.RawMessage) {
	if command == "company.show" {
		out := map[string]json.RawMessage{}
		if id, ok := args["id"]; ok {
			out["code"] = id
		}
		return "company.view", out
	}
	if command == "settlement.build.place" || command == "settlement.lot.buy" || command == "settlement.private.place" ||
		command == "settlement.lot.access" || command == "settlement.lot.repair" {
		return command, placeArgs(args)
	}
	if command == "settlement.build.place_many" {
		return command, placeManyArgs(args)
	}
	return command, args
}

// placeArgs lets a client name a placement by its numbers, {code, x, y,
// rotated, confirm}, instead of the lot token Telegram's buttons carry
// ("3-1" or "3-1-r"); the handler reads the token either way, so both go
// through one validation.
func placeArgs(args map[string]json.RawMessage) map[string]json.RawMessage {
	xr, okx := args["x"]
	yr, oky := args["y"]
	if _, has := args["lot"]; has || !okx || !oky {
		return args
	}
	var x, y int
	if json.Unmarshal(xr, &x) != nil || json.Unmarshal(yr, &y) != nil {
		return args
	}
	var rotated bool
	if raw, ok := args["rotated"]; ok {
		_ = json.Unmarshal(raw, &rotated)
	}
	out := make(map[string]json.RawMessage, len(args))
	for k, v := range args {
		if k != "x" && k != "y" && k != "rotated" {
			out[k] = v
		}
	}
	out["lot"], _ = json.Marshal(screens.LotToken(x, y, rotated))
	return out
}

// placeManyArgs lets a client name the lots of a batch as objects, {x, y,
// rotated?}, the way place names one; the handler reads the same lot tokens
// Telegram's buttons carry, so both go through one validation. A lots list
// that is already tokens, or a from/to line, is left alone.
func placeManyArgs(args map[string]json.RawMessage) map[string]json.RawMessage {
	raw, ok := args["lots"]
	if !ok {
		return args
	}
	var lots []struct {
		X       *int `json:"x"`
		Y       *int `json:"y"`
		Rotated bool `json:"rotated"`
	}
	if json.Unmarshal(raw, &lots) != nil {
		return args
	}
	tokens := make([]string, 0, len(lots))
	for _, l := range lots {
		if l.X == nil || l.Y == nil {
			return args
		}
		tokens = append(tokens, screens.LotToken(*l.X, *l.Y, l.Rotated))
	}
	out := make(map[string]json.RawMessage, len(args))
	for k, v := range args {
		out[k] = v
	}
	out["lots"], _ = json.Marshal(tokens)
	return out
}

// neutralScreenOf is what the client is shown for a neutral response
// (docs/adr/0037): the screen, its view, the actions by meaning, and a
// refusal or a notice as a code with its arguments. There is no text.
func neutralScreenOf(resp *presentation.Response, command string, policy *groups.Policy, meta *ActionMetadata) Screen {
	out := Screen{OK: true, Screen: resp.Screen, View: resp.View, Actions: NeutralActions(resp.Actions, policy, meta)}
	if out.Screen == "" {
		out.Screen = command
	}
	if n := resp.Notice; n != nil {
		out.Screen = "notice"
		out.Notice = &Notice{Code: n.Code, Args: n.Args, Alert: n.Alert || resp.Alert}
	}
	if r := resp.Refusal; r != nil {
		out.OK = false
		out.Error = &APIError{Code: r.Code, Args: r.Args}
	}
	return out
}
