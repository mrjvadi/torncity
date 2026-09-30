package notification

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
)

// The settlement channel (docs/adr/0030-realtime-interest-and-presence.md
// R2): what happens in one village, as small typed JSON on
// settlement:<settlement_id>, for the game clients of its residents and of
// whoever stands there.
//
//	{"type": "<kind>", "settlement_id": "<id>", "seq": 1234, "at": "<RFC3339>", ...fields}
//
// # Publishing
//
// A village event is written to the outbox in the same transaction as the
// change it announces, so it exists only if the change committed; this worker
// reads it from the event stream and publishes, from whichever replica
// happens to take it. Nothing is published inside a database transaction.
//
// # Sequence and gaps
//
// Every publication carries seq, one number sequence per settlement
// (internal/infrastructure/redis.SettlementVersions). The seq of an event is
// decided once, keyed on the event's id, so a redelivery is stamped the same
// and the realtime server (given the same idempotency key) publishes it
// once. Replicas race, so two publications may arrive out of order and a
// failed publish leaves a hole; a client that sees a seq that is not the last
// plus one, or lower, has missed something and fetches the layout and the
// player list again instead of trusting its picture. The player list carries
// the seq it is current to.
//
// # The layout's version
//
// The layout's own version (GET /settlements/{id}/layout, "version") is a
// hash of the picture, so it cannot order or count anything; what it can do
// is say whether a picture is current. Every publication that changes the
// picture (a building placed, finished, cancelled or pulled down) therefore
// also carries layout_version, the version the layout will have once the
// change has committed, for each kind of viewer (head, member, public): the
// event's writer computes it with the very code the layout endpoint does
// (application.LayoutVersionsOf). A client that holds a layout whose version
// equals its own kind's layout_version is already current; any other value
// means fetch it again.

// SettlementVersions stamps a settlement's publications.
type SettlementVersions interface {
	// Assign returns the version of eventID in settlementID's channel: the
	// same number every time it is asked about the same event.
	Assign(ctx context.Context, settlementID, eventID string) (int64, error)
}

// SettlementPublication is one publication on a settlement's channel before
// it is stamped.
type SettlementPublication struct {
	SettlementID string
	// Type is the publication's kind ("build_started", "member_joined", ...).
	Type string
	// Fields are the kind's own fields, merged into the envelope.
	Fields map[string]any
}

// SettlementEvents turns one event into the publications it makes on
// settlement channels: none, one, or several (a journey is an arrival in one
// settlement and a departure from another).
type SettlementEvents func(ctx context.Context, deps Deps, env *envelope.Envelope) ([]SettlementPublication, error)

// The settlement channel's publication kinds.
const (
	SettlementBuildStarted   = "build_started"
	SettlementLotBought      = "lot_bought"
	SettlementBuildFinished  = "build_finished"
	SettlementBuildCancelled = "build_cancelled"
	SettlementBuildSalvaged  = "build_salvaged"
	SettlementRelocated      = "relocated"
	SettlementResearchStart  = "research_started"
	SettlementResearchDone   = "research_finished"
	SettlementKnowledgeBuy   = "knowledge_bought"
	SettlementLiteracy       = "literacy_changed"
	SettlementHeadChanged    = "head_changed"
	SettlementMemberJoined   = "member_joined"
	SettlementMemberLeft     = "member_left"
	settlementEventKeySuffix = ":settlement"
)

// settlementChannel spells the channel as internal/infrastructure/centrifugo
// does (not imported: this package knows the realtime server only through
// the Realtime port).
func settlementChannel(id string) string { return "settlement:" + id }

// publishSettlement stamps and publishes what one event makes.
//
// A failure to stamp or publish is returned, so the event is retried: both
// steps are idempotent for the same event, and a settlement channel with a
// silent hole is worse than a late publication. The Telegram side of the
// same events is a different consumer and never waits on this.
func (w *Worker) publishSettlement(ctx context.Context, route Route, env *envelope.Envelope, now time.Time, log *slog.Logger) error {
	if w.cfg.Realtime == nil || w.cfg.SettlementVersions == nil {
		return nil
	}
	pubs, err := route.Settlement(ctx, w.cfg.Deps, env)
	if err != nil {
		if apperrors.CodeOf(err) == apperrors.CodeInternal {
			log.Error("cannot shape the settlement publication", slog.String("error", err.Error()))
			return err
		}
		log.Warn("event makes no settlement publication", slog.String("error", err.Error()))
		return nil
	}
	if w.cfg.Deps.Founded != nil && len(pubs) > 0 {
		ids := make([]string, 0, len(pubs))
		for _, p := range pubs {
			ids = append(ids, p.SettlementID)
		}
		keep, err := w.cfg.Deps.Founded.FoundedAmong(ctx, ids)
		if err != nil {
			log.Warn("cannot tell founded settlements from content cities", slog.String("error", err.Error()))
			return err
		}
		founded := make(map[string]bool, len(keep))
		for _, id := range keep {
			founded[id] = true
		}
		// Indexes stay those of the event, so a publication that is skipped
		// does not renumber the ones after it (their stamps are keyed on it).
		for i := range pubs {
			if !founded[pubs[i].SettlementID] {
				pubs[i].SettlementID = ""
			}
		}
	}
	for i, p := range pubs {
		if p.SettlementID == "" {
			continue
		}
		eventKey := env.Metadata.MessageID() + ":" + strconv.Itoa(i)
		version, err := w.cfg.SettlementVersions.Assign(ctx, p.SettlementID, eventKey)
		if err != nil {
			log.Warn("cannot stamp the settlement publication", slog.String("error", err.Error()))
			return err
		}
		msg := make(map[string]any, len(p.Fields)+4)
		for k, v := range p.Fields {
			msg[k] = v
		}
		msg["type"] = p.Type
		msg["settlement_id"] = p.SettlementID
		msg["seq"] = version
		msg["at"] = now.UTC().Format(time.RFC3339)
		if err := w.cfg.Realtime.Publish(ctx, settlementChannel(p.SettlementID), msg, eventKey+settlementEventKeySuffix); err != nil {
			log.Warn("cannot publish to the settlement channel",
				slog.String("settlement_id", p.SettlementID), slog.String("error", err.Error()))
			return err
		}
	}
	return nil
}

// villageEvent is the union of the fields the settlement.* outbox payloads
// carry (internal/application/handlers/village*.go).
type villageEvent struct {
	SettlementID    string `json:"settlement_id"`
	BuildingID      string `json:"building_id"`
	TypeCode        string `json:"type_code"`
	Name            string `json:"name"`
	LotX            int    `json:"lot_x"`
	LotY            int    `json:"lot_y"`
	Rotated         bool   `json:"rotated"`
	FinishAt        string `json:"finish_at"`
	// LayoutVersion is the layout's version after the change, for each kind
	// of viewer; only building events carry it.
	LayoutVersion json.RawMessage `json:"layout_version"`
	ResearchID      string `json:"research_id"`
	Code            string `json:"code"`
	LiteracyShareBP int    `json:"literacy_share_bps"`
}

func decodeVillage(env *envelope.Envelope, name string) (villageEvent, error) {
	var ev villageEvent
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return ev, apperrors.InvalidInput("settlement." + name + " payload is unreadable").WithCause(err)
	}
	if ev.SettlementID == "" {
		return ev, apperrors.InvalidInput("settlement." + name + " names no settlement")
	}
	return ev, nil
}

func one(id, kind string, fields map[string]any) []SettlementPublication {
	return []SettlementPublication{{SettlementID: id, Type: kind, Fields: fields}}
}

// villageBuildStarted: a building was placed and its construction began.
func villageBuildStarted(_ context.Context, _ Deps, env *envelope.Envelope) ([]SettlementPublication, error) {
	ev, err := decodeVillage(env, "build_started")
	if err != nil {
		return nil, err
	}
	f := map[string]any{"building_id": ev.BuildingID, "type_code": ev.TypeCode,
		"lot_x": ev.LotX, "lot_y": ev.LotY, "rotated": ev.Rotated}
	if ev.FinishAt != "" {
		f["finish_at"] = ev.FinishAt
	}
	return one(ev.SettlementID, SettlementBuildStarted, withLayout(f, ev)), nil
}

// villageLotBought: a resident bought a lot (the citizen loop). The
// publication carries the layout versions that include who owns what, so a
// client that holds the old layout fetches the new one.
func villageLotBought(_ context.Context, _ Deps, env *envelope.Envelope) ([]SettlementPublication, error) {
	ev, err := decodeVillage(env, "lot_bought")
	if err != nil {
		return nil, err
	}
	return one(ev.SettlementID, SettlementLotBought, withLayout(map[string]any{"lot_x": ev.LotX, "lot_y": ev.LotY}, ev)), nil
}

// villageBuilt: a building finished construction.
func villageBuilt(_ context.Context, _ Deps, env *envelope.Envelope) ([]SettlementPublication, error) {
	ev, err := decodeVillage(env, "built")
	if err != nil {
		return nil, err
	}
	return one(ev.SettlementID, SettlementBuildFinished, withLayout(map[string]any{"building_id": ev.BuildingID, "type_code": ev.TypeCode}, ev)), nil
}

// villageDemolished: a building was pulled down and its scrap credited.
func villageDemolished(_ context.Context, _ Deps, env *envelope.Envelope) ([]SettlementPublication, error) {
	ev, err := decodeVillage(env, "building_demolished")
	if err != nil {
		return nil, err
	}
	return one(ev.SettlementID, SettlementBuildSalvaged, withLayout(map[string]any{"building_id": ev.BuildingID, "type_code": ev.TypeCode}, ev)), nil
}

// villageCancelled: a building still going up was called off.
func villageCancelled(_ context.Context, _ Deps, env *envelope.Envelope) ([]SettlementPublication, error) {
	ev, err := decodeVillage(env, "build_cancelled")
	if err != nil {
		return nil, err
	}
	return one(ev.SettlementID, SettlementBuildCancelled, withLayout(map[string]any{"building_id": ev.BuildingID, "type_code": ev.TypeCode}, ev)), nil
}

// villageRelocated: an operator moved the village to a better site. The
// picture is different in every way (terrain, the kit's lots), so the
// publication carries no layout_version: a client holding a layout fetches it
// again.
func villageRelocated(_ context.Context, _ Deps, env *envelope.Envelope) ([]SettlementPublication, error) {
	ev, err := decodeVillage(env, "relocated")
	if err != nil {
		return nil, err
	}
	return one(ev.SettlementID, SettlementRelocated, map[string]any{}), nil
}

// withLayout adds the layout's version after the change, when the event
// carries it.
func withLayout(f map[string]any, ev villageEvent) map[string]any {
	if len(ev.LayoutVersion) > 0 && string(ev.LayoutVersion) != "null" {
		f["layout_version"] = ev.LayoutVersion
	}
	return f
}

// villageResearchStarted: the village began researching something.
func villageResearchStarted(_ context.Context, _ Deps, env *envelope.Envelope) ([]SettlementPublication, error) {
	ev, err := decodeVillage(env, "research_started")
	if err != nil {
		return nil, err
	}
	f := map[string]any{"research_id": ev.ResearchID, "code": ev.Code}
	if ev.FinishAt != "" {
		f["finish_at"] = ev.FinishAt
	}
	return one(ev.SettlementID, SettlementResearchStart, f), nil
}

// villageResearched: a research finished and the village knows the item.
func villageResearched(_ context.Context, _ Deps, env *envelope.Envelope) ([]SettlementPublication, error) {
	ev, err := decodeVillage(env, "knowledge_researched")
	if err != nil {
		return nil, err
	}
	return one(ev.SettlementID, SettlementResearchDone, map[string]any{"code": ev.Code}), nil
}

// villageBought: the village bought an item from Support.
func villageBought(_ context.Context, _ Deps, env *envelope.Envelope) ([]SettlementPublication, error) {
	ev, err := decodeVillage(env, "knowledge_bought")
	if err != nil {
		return nil, err
	}
	return one(ev.SettlementID, SettlementKnowledgeBuy, map[string]any{"code": ev.Code}), nil
}

// villageLiteracy: one teaching step finished and literacy moved.
func villageLiteracy(_ context.Context, _ Deps, env *envelope.Envelope) ([]SettlementPublication, error) {
	ev, err := decodeVillage(env, "literacy_advanced")
	if err != nil {
		return nil, err
	}
	return one(ev.SettlementID, SettlementLiteracy, map[string]any{"literacy_share_bps": ev.LiteracyShareBP}), nil
}

// headOffices are the offices that head a settlement (ADR 0028 section 4):
// a change of holder in one of them is "the head changed".
var headOffices = map[string]bool{"village_head": true, "town_head": true, "mayor": true}

// governanceHead: a settlement's head office was filled or vacated
// (governance.appointed, governance.dismissed).
func governanceHead(vacated bool) SettlementEvents {
	return func(_ context.Context, _ Deps, env *envelope.Envelope) ([]SettlementPublication, error) {
		var ev electionEvent
		if err := json.Unmarshal(env.Payload, &ev); err != nil {
			return nil, apperrors.InvalidInput("governance payload is unreadable").WithCause(err)
		}
		if !headOffices[ev.Office] {
			return nil, nil
		}
		return headPublications(ev, ev.PlayerID, ev.PlayerName, vacated), nil
	}
}

// electionHead: an election for a settlement's head office was counted.
func electionHead(_ context.Context, _ Deps, env *envelope.Envelope) ([]SettlementPublication, error) {
	ev, err := decodeElection(env, "counted")
	if err != nil {
		return nil, err
	}
	if !headOffices[ev.Office] {
		return nil, nil
	}
	var out []SettlementPublication
	for _, c := range ev.Candidates {
		if c.Elected {
			out = append(out, headPublications(ev, "", c.PlayerName, false)...)
		}
	}
	return out, nil
}

func headPublications(ev electionEvent, playerID, name string, vacated bool) []SettlementPublication {
	ids := ev.CityIDs
	if len(ids) == 0 && ev.CityID != "" {
		ids = []string{ev.CityID}
	}
	var out []SettlementPublication
	for _, id := range ids {
		// Who may place is part of the head's picture of the layout, so the
		// layout is stale for whoever gained or lost the office.
		f := map[string]any{"office": ev.Office, "vacated": vacated, "layout_stale": true}
		if playerID != "" {
			f["player_id"] = playerID
		}
		if name != "" {
			f["player_name"] = name
		}
		out = append(out, SettlementPublication{SettlementID: id, Type: SettlementHeadChanged, Fields: f})
	}
	return out
}

// travelMembers: a journey landed, which is an arrival in one settlement and,
// when the origin is known, a departure from another.
func travelMembers(ctx context.Context, deps Deps, env *envelope.Envelope) ([]SettlementPublication, error) {
	var ev struct {
		PlayerID   string `json:"player_id"`
		FromCityID string `json:"from_city_id"`
		ToCityID   string `json:"to_city_id"`
	}
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("travel.completed payload is unreadable").WithCause(err)
	}
	return memberPublications(ctx, deps, ev.PlayerID, ev.FromCityID, ev.ToCityID, "travel")
}

// residenceMembers: a player moved house, which joins one settlement and
// leaves another.
func residenceMembers(ctx context.Context, deps Deps, env *envelope.Envelope) ([]SettlementPublication, error) {
	var ev struct {
		PlayerID   string `json:"player_id"`
		FromCityID string `json:"from_city_id"`
		ToCityID   string `json:"to_city_id"`
	}
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("residence.changed payload is unreadable").WithCause(err)
	}
	return memberPublications(ctx, deps, ev.PlayerID, ev.FromCityID, ev.ToCityID, "residence")
}

func memberPublications(ctx context.Context, deps Deps, playerID, from, to, via string) ([]SettlementPublication, error) {
	if playerID == "" || (from == "" && to == "") || from == to {
		return nil, nil
	}
	name := ""
	if deps.Players != nil {
		p, err := deps.Players.GetByID(ctx, playerID)
		switch {
		case err == nil:
			name = shownName(p)
		case apperrors.CodeOf(err) != apperrors.CodeNotFound:
			return nil, err
		}
	}
	fields := func() map[string]any {
		f := map[string]any{"player_id": playerID, "via": via}
		if name != "" {
			f["player_name"] = name
		}
		return f
	}
	var out []SettlementPublication
	if to != "" {
		out = append(out, SettlementPublication{SettlementID: to, Type: SettlementMemberJoined, Fields: fields()})
	}
	if from != "" {
		out = append(out, SettlementPublication{SettlementID: from, Type: SettlementMemberLeft, Fields: fields()})
	}
	return out, nil
}
