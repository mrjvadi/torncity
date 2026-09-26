package groups

import "sync/atomic"

// Button colour.
//
// Bot API 9.4 (9 February 2026) added a "style" field to InlineKeyboardButton
// (and KeyboardButton): a button can be drawn "primary" (blue), "success"
// (green) or "danger" (red) instead of Telegram's plain default, with no
// special account or purchase required — unlike custom emoji (style.go's
// neighbour, internal/telegram/screens' locale comment on format.digits,
// carries no such prerequisite; see plans/telegram-ux.md for the emoji
// side of Bot API 9.4).
//
// configs/actions.yml already grades every player command by kind, for the
// Godot client (internal/clientapi) to pick a colour and a role instead of
// drawing every button the same way. Rather than deciding a colour per
// screen — which the owner explicitly ruled out — the Telegram gateway
// reuses exactly that grading:
//
//	kind primary               -> style primary  (the main thing to do here)
//	kind confirm                -> style success  (answers a yes/no the game is waiting on)
//	kind danger                 -> style danger   (ends or loses something: quit, sell all, cancel)
//	kind secondary, navigation,
//	  back, or no grading found -> no style (Telegram's plain button)
//
// The grading itself is injected once at startup (SetKindResolver, from
// configs/actions.yml through internal/clientapi.ActionMetadata.Of) rather
// than loaded here, because internal/clientapi already imports this package
// for SplitOwner and a Go import cannot run the other way. A resolver that
// is never set — a test, or a gateway that failed to load actions.yml —
// renders every button in Telegram's plain style, which is a worse-looking
// keyboard, never a broken one.
var kindResolver atomic.Pointer[func(command string) string]

// SetKindResolver installs the function Markup asks for a command's
// configs/actions.yml kind ("primary", "danger", "confirm", ...). A nil
// resolver is ignored, so a failed or skipped load simply leaves buttons
// unstyled instead of racing SetKindResolver's caller.
func SetKindResolver(resolve func(command string) string) {
	if resolve != nil {
		kindResolver.Store(&resolve)
	}
}

// buttonStyle returns the Telegram "style" value for the button that runs
// command, or "" to leave the field out and get Telegram's ordinary button.
func buttonStyle(command string) string {
	if command == "" {
		return ""
	}
	p := kindResolver.Load()
	if p == nil {
		return ""
	}
	switch (*p)(command) {
	case "primary":
		return "primary"
	case "confirm":
		return "success"
	case "danger":
		return "danger"
	}
	return ""
}
