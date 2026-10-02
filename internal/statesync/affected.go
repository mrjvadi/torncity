package statesync

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// Which players an event concerns.
//
// The projector does not map every event to the entities it changed: it
// treats an event as "these players' state may have changed", reads their
// state and diffs (record.go). So all it needs from an event is who: the
// player who ran the command (the envelope's player id), and every player
// the payload names. A payload names a player under a key that says so
// ("player_id", "to_player_id", "payee_id"...); the keys besides those
// containing "player" are configuration (state_sync.player_keys), not code.
// Naming a player who did not change costs one empty projection; naming
// something that is not a player costs one projection that finds nothing.

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// maxPayloadDepth bounds the walk of a payload: what names a player sits at
// the top or one or two objects down; a deeper walk only costs time.
const maxPayloadDepth = 4

// maxAffected bounds the players one event projects (a crew, a village's
// residents told together); beyond it the rest catch up at their next
// projection.
const maxAffected = 256

// Affected is the players an event concerns, the actor first, without
// repeats.
func Affected(meta envelope.Metadata, payload json.RawMessage, extraKeys []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.ToLower(strings.TrimSpace(id))
		if !uuidPattern.MatchString(id) || seen[id] || len(out) >= maxAffected {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	add(meta.PlayerID)
	add(meta.ReplyToPlayerID)
	extra := map[string]bool{}
	for _, k := range extraKeys {
		extra[strings.ToLower(k)] = true
	}
	var body any
	if len(payload) > 0 && json.Unmarshal(payload, &body) == nil {
		walkPlayers(body, "", 0, extra, add)
	}
	return out
}

// walkPlayers calls add for every string value whose key names a player,
// and for every string in a list under such a key ("player_ids").
func walkPlayers(v any, key string, depth int, extra map[string]bool, add func(string)) {
	if depth > maxPayloadDepth {
		return
	}
	names := key != "" && (strings.Contains(key, "player") || extra[key])
	switch x := v.(type) {
	case string:
		if names {
			add(x)
		}
	case []any:
		for _, e := range x {
			walkPlayers(e, key, depth+1, extra, add)
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walkPlayers(x[k], strings.ToLower(k), depth+1, extra, add)
		}
	}
}

// Settlements is the settlements an event is about: the values under
// "settlement_id" (top level or one object down). Their residents' and
// visitors' settlement summaries are projected again, because a building or
// a research changes what every one of them sees.
func Settlements(payload json.RawMessage) []string {
	var body any
	if len(payload) == 0 || json.Unmarshal(payload, &body) != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	var walk func(v any, key string, depth int)
	walk = func(v any, key string, depth int) {
		if depth > 2 {
			return
		}
		switch x := v.(type) {
		case string:
			id := strings.ToLower(x)
			if key == "settlement_id" && uuidPattern.MatchString(id) && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(x[k], strings.ToLower(k), depth+1)
			}
		}
	}
	walk(body, "", 0)
	return out
}
