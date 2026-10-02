package statesync

import (
	"encoding/json"
	"sort"
	"time"
)

// Snapshot is the answer of GET /api/v1/state: every entity the player's
// client holds, as of one pts.
type Snapshot struct {
	PTS        int64                                `json:"pts"`
	Epoch      string                               `json:"epoch"`
	ServerTime time.Time                            `json:"server_time"`
	Entities   map[string]map[string]SnapshotEntity `json:"entities"`
	// Channels are the current seq of each settlement channel the player is
	// subscribed to, so the first publication on it is compared with
	// something (client-api.md section 5.4).
	Channels map[string]int64 `json:"channels"`
}

// SnapshotEntity is one entity in a snapshot.
type SnapshotEntity struct {
	V int64           `json:"v"`
	D json.RawMessage `json:"d"`
}

// Difference is the answer of GET /api/v1/updates?since= (Telegram's
// getDifference): the records after since, or a reset.
type Difference struct {
	// PTS is the highest pts of the log when it was read.
	PTS     int64    `json:"pts"`
	Updates []Record `json:"updates"`
	// More means the answer was cut at limit: ask again from the last pts.
	More bool `json:"more"`
	// Reset means the records cannot be had (trimmed, too many, or a new
	// epoch): drop the local copy and read GET /state.
	Reset  bool   `json:"reset,omitempty"`
	Reason string `json:"reason,omitempty"`
	Epoch  string `json:"epoch"`
}

// The reasons of a reset.
const (
	// ResetTooLong: the client is further behind than the server replays
	// (records trimmed, or more than the reset threshold).
	ResetTooLong = "too_long"
	// ResetEpoch: the log was rebuilt; every cursor before it is void.
	ResetEpoch = "epoch"
	// ResetAhead: the client holds a pts the log never reached (a restored
	// database); its copy cannot be trusted.
	ResetAhead = "ahead"
)

// Publication is what a player's realtime channel carries for new records.
type Publication struct {
	// Type is PublicationUpdates, or PublicationTooLong when the batch was
	// too big to push (Telegram's updatesTooLong, Replicache's poke): the
	// client pulls from its own pts.
	Type    string   `json:"type"`
	From    int64    `json:"from"`
	To      int64    `json:"to"`
	Updates []Record `json:"updates,omitempty"`
}

// Publication types.
const (
	PublicationUpdates = "updates"
	PublicationTooLong = "updates_too_long"
)

// CommandUpdates is the field a command's answer gains: the records the
// command itself caused, when they were ready in time.
type CommandUpdates struct {
	PTS     int64    `json:"pts"`
	Records []Record `json:"records"`
}

// HeldEntity is one entity in a client's copy.
type HeldEntity struct {
	V    int64
	Data json.RawMessage
}

// ClientState is a client's copy, applied by the rules every client follows
// (api/client-api.md "State sync", src/state/store.ts on the web). The
// server uses it to check itself: the consistency harness folds the log
// into it and compares with the snapshot.
type ClientState struct {
	PTS      int64
	Epoch    string
	Entities map[Key]HeldEntity
}

// NewClientState starts a copy from a snapshot.
func NewClientState(s Snapshot) *ClientState {
	c := &ClientState{PTS: s.PTS, Epoch: s.Epoch, Entities: map[Key]HeldEntity{}}
	for kind, byID := range s.Entities {
		for id, e := range byID {
			c.Entities[Key{kind, id}] = HeldEntity{V: e.V, Data: e.D}
		}
	}
	return c
}

// ApplyResult says what Apply did.
type ApplyResult struct {
	Applied    int
	Duplicates int
	// Gap is set when a record's pts was beyond the next one expected: the
	// copy stopped there and must pull since PTS.
	Gap bool
}

// Apply applies records in pts order. A record at or below the copy's pts
// is a duplicate; the next pts applies; anything further is a gap, and
// nothing after it is applied (the caller pulls since PTS and calls again).
// Within an applied record, a set changes the entity only when its version
// is newer than the one held, so a set the copy already has (from a
// snapshot read a moment later than the record) changes nothing. A delete
// removes the entity outright: records apply strictly in pts order, so no
// older set can follow it, and an entity created again later may start its
// versions again.
func (c *ClientState) Apply(records []Record) ApplyResult {
	recs := append([]Record(nil), records...)
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].PTS < recs[j].PTS })
	var res ApplyResult
	for _, r := range recs {
		switch {
		case r.PTS <= c.PTS:
			res.Duplicates++
			continue
		case r.PTS > c.PTS+1:
			res.Gap = true
			return res
		}
		c.PTS = r.PTS
		res.Applied++
		k := Key{r.Entity, r.ID}
		switch r.Op {
		case OpDel:
			delete(c.Entities, k)
		case OpSet:
			if held, ok := c.Entities[k]; ok && held.V >= r.V {
				continue
			}
			c.Entities[k] = HeldEntity{V: r.V, Data: r.Data}
		}
	}
	return res
}

// Live is the copy's entities.
func (c *ClientState) Live() map[Key]HeldEntity {
	out := make(map[Key]HeldEntity, len(c.Entities))
	for k, e := range c.Entities {
		out[k] = e
	}
	return out
}

// SnapshotLive is a snapshot's entities as a client would hold them.
func SnapshotLive(s Snapshot) map[Key]HeldEntity {
	return NewClientState(s).Live()
}
