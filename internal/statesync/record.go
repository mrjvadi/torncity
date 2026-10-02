// Package statesync is client state sync (docs/adr/0034-client-state-sync.md):
// a per-player ordered log of entity changes, a versioned snapshot, and the
// rules that keep a client's copy equal to the server's.
//
// The model, in one paragraph. A client holds entities: (kind, id) with a
// version v and neutral data (numbers, codes, ids, instants; never wording,
// docs/adr/0039). The server keeps, per player, the versions it has told
// that client about (entity_versions, Replicache's "client view record") and
// a log of every change it told (player_updates), numbered pts = 1, 2, 3...
// per player with no holes. A projection reads the player's current state,
// diffs it against the held versions (Plan), and appends one record per
// entity that differs, all under one per-player lock in one transaction. A
// client applies records in pts order and, per entity, only a version newer
// than the one it holds (Apply), so duplicates and re-sent history change
// nothing; a pts it did not expect is a gap and it pulls the log from its
// own pts. GET /state answers the held versions plus the log's pts, so
// snapshot + updates-after-pts is exactly the state, by construction.
//
// This package holds the rules and the shapes only: it imports no database,
// no broker and no presentation edge.
package statesync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

// The kinds of entity a client holds. Their data is neutral (codes, numbers,
// ids, RFC 3339 instants): each edge words them itself.
const (
	// KindPlayer is the player's identity and progress: name, code, language,
	// level, xp, rank code. Id: the player's id.
	KindPlayer = "player"
	// KindVitals is energy, nerve and health as value + as_of + regen rule,
	// so the client counts regen locally and no tick update is ever sent.
	// Id: the player's id.
	KindVitals = "vitals"
	// KindWallet is one currency's cash and bank. Id: the currency code.
	KindWallet = "wallet"
	// KindInventory is one item code's holdings. Id: the item code.
	KindInventory = "inventory"
	// KindSkill is one skill's level and xp. Id: the skill code.
	KindSkill = "skill"
	// KindTimedAction is one running timed thing (a journey, a course, a
	// shift, a stay, a sentence, a walk). Id: the game action's id.
	KindTimedAction = "timed_action"
	// KindLocation is where the player is. Id: "self".
	KindLocation = "location"
	// KindInbox is the unread count and the ids of the latest notices.
	// Id: "self".
	KindInbox = "inbox"
	// KindNotice is one notice, as data (screen and view). Id: its id.
	KindNotice = "notice"
	// KindResidence is the settlement the player belongs to. Id: "self".
	KindResidence = "residence"
	// KindSettlement is a settlement's summary as this player may see it
	// (viewer-tiered: head, member, public), with its layout version. Id:
	// the settlement's id.
	KindSettlement = "settlement"
	// KindRelations is friends, faction and the presence setting. Id: "self".
	KindRelations = "relations"
)

// SelfID is the id of a kind that exists once per player.
const SelfID = "self"

// CoreKinds are the kinds of phase P1, in the order a projection reads them.
var CoreKinds = []string{KindPlayer, KindVitals, KindWallet, KindInventory, KindSkill, KindTimedAction,
	KindLocation, KindInbox, KindNotice}

// VillageKinds are the kinds of phase P3.
var VillageKinds = []string{KindResidence, KindSettlement, KindRelations}

// AllKinds are every kind, core first.
var AllKinds = append(append([]string{}, CoreKinds...), VillageKinds...)

// KnownKind reports whether k is a kind this build projects.
func KnownKind(k string) bool {
	for _, x := range AllKinds {
		if x == k {
			return true
		}
	}
	return false
}

// The operations a record carries.
const (
	// OpSet replaces the entity with Data (the default, and the only one the
	// server writes today).
	OpSet = "set"
	// OpPatch is a JSON merge patch on the entity, valid only on top of
	// version v-1; a client holding anything else pulls instead. Reserved by
	// the contract for big entities; not written yet.
	OpPatch = "patch"
	// OpDel removes the entity.
	OpDel = "del"
)

// Entity is one entity as the server sees it now.
type Entity struct {
	Kind string
	ID   string
	Data json.RawMessage
}

// Key names an entity.
type Key struct{ Kind, ID string }

// Held is what the server last told the client about one entity.
type Held struct {
	V       int64
	Hash    string
	Deleted bool
}

// Change is one entity whose current state differs from what is held: the
// record to append, before it has a pts.
type Change struct {
	Kind, ID string
	Op       string
	V        int64
	Hash     string
	Data     json.RawMessage
}

// Record is one entry of a player's log, as the contract carries it
// (api/client-api.md, "State sync").
type Record struct {
	PTS    int64           `json:"pts"`
	Type   string          `json:"type"`
	Entity string          `json:"entity"`
	ID     string          `json:"id"`
	V      int64           `json:"v"`
	Op     string          `json:"op"`
	Data   json.RawMessage `json:"data,omitempty"`
	At     time.Time       `json:"at"`
	// Cause is the request id of the command that made the change, when one
	// did; a client drops its optimistic overlay when it sees its own.
	Cause string `json:"cause,omitempty"`
}

// TypeOf is a record's type: "wallet.set", "notice.del".
func TypeOf(kind, op string) string { return kind + "." + op }

// Hash is the fingerprint of an entity's data. Data is produced by
// encoding/json from Go values, which writes struct fields in declaration
// order and map keys sorted, so equal content always hashes equal.
func Hash(data json.RawMessage) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:16])
}

// KindSet is a set of kinds; nil or empty means every kind.
type KindSet map[string]bool

// Kinds makes a set; no kinds is every kind.
func Kinds(kinds ...string) KindSet {
	if len(kinds) == 0 {
		return nil
	}
	s := KindSet{}
	for _, k := range kinds {
		s[k] = true
	}
	return s
}

// Has reports whether k is in the set (an empty set has every kind).
func (s KindSet) Has(k string) bool { return len(s) == 0 || s[k] }

// List is the set's kinds in AllKinds order.
func (s KindSet) List() []string {
	out := []string{}
	for _, k := range AllKinds {
		if s.Has(k) {
			out = append(out, k)
		}
	}
	return out
}

// kindRank orders kinds as AllKinds does, unknown last.
func kindRank(k string) int {
	for i, x := range AllKinds {
		if x == k {
			return i
		}
	}
	return len(AllKinds)
}

// Plan compares the current entities with what is held and returns the
// changes, in a fixed order (kind as AllKinds, then id), so two projections
// of the same state append the same records.
//
// An entity held but no longer current is deleted, but only for kinds in
// scope: a projection that read only some kinds says nothing about the
// others. A current entity that was held deleted comes back with the next
// version, never at 1, so a client that saw the delete takes it.
func Plan(current []Entity, held map[Key]Held, scope KindSet) []Change {
	seen := make(map[Key]bool, len(current))
	var out []Change
	for _, e := range current {
		if !scope.Has(e.Kind) {
			continue
		}
		k := Key{e.Kind, e.ID}
		if seen[k] {
			continue // a reader that returned one entity twice: the first wins
		}
		seen[k] = true
		h := Hash(e.Data)
		prev, ok := held[k]
		switch {
		case !ok:
			out = append(out, Change{Kind: e.Kind, ID: e.ID, Op: OpSet, V: 1, Hash: h, Data: e.Data})
		case prev.Deleted || prev.Hash != h:
			out = append(out, Change{Kind: e.Kind, ID: e.ID, Op: OpSet, V: prev.V + 1, Hash: h, Data: e.Data})
		}
	}
	for k, prev := range held {
		if prev.Deleted || seen[k] || !scope.Has(k.Kind) {
			continue
		}
		out = append(out, Change{Kind: k.Kind, ID: k.ID, Op: OpDel, V: prev.V + 1})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := kindRank(a.Kind), kindRank(b.Kind); ra != rb {
			return ra < rb
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
	return out
}

// CauseKey is the idempotency key of a change: one source (an outbox event,
// a command's request) appends one entity at most once.
func CauseKey(source, kind, id string) string { return source + ":" + kind + ":" + id }

// Source names for CauseKey.
func EventSource(eventID string) string     { return "evt:" + eventID }
func RequestSource(requestID string) string { return "req:" + requestID }
func RefreshSource(id string) string        { return "ref:" + id }
