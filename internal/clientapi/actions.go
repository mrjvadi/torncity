package clientapi

import (
	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/gateway/input"
	"github.com/mrjvadi/torncity/internal/gateway/routing"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Action is one button of a screen, as a command the client can send back
// verbatim: {command, args}. The keyboard's callback data is read by the
// same parser the gateway reads a pressed button with
// (routing.ParseCallbackData), so an action is exactly what pressing the
// button in Telegram would run.
//
// Kind, Icon and Group are the semantic layer a native client draws from
// instead of a Telegram-shaped button: configs/actions.yml, looked up by
// Command. A URL button (Command empty) carries none of the three — it is
// not a game command, and a client renders it as a plain link.
type Action struct {
	Label   string         `json:"label"`
	Command string         `json:"command,omitempty"`
	Args    map[string]any `json:"args,omitempty"`
	// Input, when set, means the button asks the player to type a value:
	// the client asks, and sends Command with Args plus the value under
	// Input.Field.
	Input *ActionInput `json:"input,omitempty"`
	// URL is a link button's address; it has no command.
	URL string `json:"url,omitempty"`
	// Row is the keyboard row the button is on, from 0, for a client that
	// lays buttons out as Telegram does.
	Row int `json:"row"`
	// Kind says how to draw the button: primary, secondary, danger,
	// navigation, back or confirm (internal/clientapi's Kind* constants).
	Kind string `json:"kind,omitempty"`
	// Icon is a stable asset key the client maps to its own picture.
	Icon string `json:"icon,omitempty"`
	// Group names the flow this action is one step of (a wizard, a
	// proposal and its answer), for a client that wants to present the
	// steps together. Most actions have none.
	Group string `json:"group,omitempty"`
}

// ActionInput is the value an asking button wants typed.
type ActionInput struct {
	Field string `json:"field"`
	// Text says the value is words (a name); otherwise it is an amount.
	Text bool `json:"text,omitempty"`
}

// Actions translates a keyboard into actions. A button whose data does not
// parse, or names a command the game does not serve to players, is left
// out: it is nothing the client could send. meta may be nil, which gives
// every action the safe default kind and icon (ActionMetadata.Of).
func Actions(kb *presenter.Keyboard, policy *groups.Policy, meta *ActionMetadata) []Action {
	out := []Action{}
	if kb == nil {
		return out
	}
	for row, buttons := range kb.Rows {
		for _, b := range buttons {
			if a, ok := action(b, policy, meta); ok {
				a.Row = row
				out = append(out, a)
			}
		}
	}
	return out
}

func action(b presenter.Button, policy *groups.Policy, meta *ActionMetadata) (Action, bool) {
	if b.URL != "" {
		return Action{Label: b.Text, URL: b.URL}, true
	}
	_, data, _ := groups.SplitOwner(b.CallbackData)
	if data == "" {
		return Action{}, false
	}
	if command, args, ok := input.ParseAsk(data); ok {
		spec, ok := policy.Input(command)
		if !ok || len(args) > len(spec.Args) || !commands.FromPlayerCommand(command) {
			return Action{}, false
		}
		payload := make(map[string]any, len(args))
		for i, a := range args {
			payload[spec.Args[i]] = a
		}
		am := meta.Of(command)
		return Action{Label: b.Text, Command: command, Args: payload,
			Input: &ActionInput{Field: spec.Field, Text: spec.Text},
			Kind:  am.Kind, Icon: am.Icon, Group: am.Group}, true
	}
	command, payload, err := routing.ParseCallbackData(data)
	if err != nil || !commands.FromPlayerCommand(command) {
		return Action{}, false
	}
	am := meta.Of(command)
	return Action{Label: b.Text, Command: command, Args: payload,
		Kind: am.Kind, Icon: am.Icon, Group: am.Group}, true
}
