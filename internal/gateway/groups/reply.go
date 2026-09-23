package groups

import "strings"

// Aiming a command with Telegram's reply.
//
// In a group the natural way to point at another player is to reply to one of
// their messages: "/pay 5000 cash" as a reply pays the person replied to. The
// gateway resolves who that is (envelope.Metadata.ReplyToPlayerID); this file
// only rearranges the typed words so the payload says it. Routing has already
// read the words positionally, as if the first one named the payee, so a
// reply shifts them: the replied-to player fills the payee, and the words
// move up to the arguments after it.
//
// A first word starting with "@" names a payee explicitly, and the reply is
// then not used: what the player typed wins over what they replied to.

// replyAim describes one command a reply can aim.
type replyAim struct {
	// names are the command's positional argument names as routing assigns
	// them, the payee first.
	names []string
	// target is the payload field a player record id goes in.
	target string
}

// replyAimed lists the commands a reply can aim.
var replyAimed = map[string]replyAim{
	// bank.pay takes the payee by record id in "player" (the bank handler's
	// PayRequest); typed, it is "/pay <to> <amount> <method>".
	"bank.pay": {names: []string{"to", "amount", "method"}, target: "player"},
}

// AimAtReply returns the payload of command aimed at the replied-to player.
// It returns payload unchanged when the command cannot be aimed, when there
// is no replied-to player, or when the words name a payee themselves.
func AimAtReply(command string, payload map[string]any, replyPlayerID string) map[string]any {
	aim, ok := replyAimed[command]
	if !ok || replyPlayerID == "" || payload == nil {
		return payload
	}
	if _, set := payload[aim.target]; set {
		return payload
	}

	var words []string
	for _, name := range aim.names {
		if w, ok := payload[name].(string); ok && w != "" {
			words = append(words, w)
		}
	}
	if rest, ok := payload["args"].([]string); ok {
		words = append(words, rest...)
	}
	if len(words) > 0 && strings.HasPrefix(words[0], "@") {
		return payload
	}

	out := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		out[k] = v
	}
	for _, name := range aim.names {
		delete(out, name)
	}
	delete(out, "args")
	out[aim.target] = replyPlayerID

	i := 0
	for _, name := range aim.names[1:] {
		if i >= len(words) {
			break
		}
		out[name] = words[i]
		i++
	}
	if i < len(words) {
		out["args"] = append([]string(nil), words[i:]...)
	}
	return out
}
