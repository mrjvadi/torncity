package application

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/presence"
	"github.com/mrjvadi/torncity/internal/shared/errors"
)

// This file is ADR 0030's R1/R2 (docs/adr/0030-realtime-interest-and-
// presence.md sections 3 and 6): who is online, what they are doing, who may
// see it, and the settlement player list built from it.

// ErrNotInSettlement means the viewer neither lives in nor stands in the
// settlement whose players they asked for (ADR 0030 section 1.1: a
// settlement's channel and its roster are for its residents and whoever is
// physically there).
var ErrNotInSettlement = errors.Sentinel(errors.CodeUnauthorized,
	"application.ErrNotInSettlement", "you are not in this settlement")

// ErrUnsupportedPresenceVisibility refuses a "last seen" setting that is not
// one of everyone, contacts, nobody.
var ErrUnsupportedPresenceVisibility = errors.Sentinel(errors.CodeInvalidInput,
	"application.ErrUnsupportedPresenceVisibility", "that visibility is not supported")

// PresenceStore is the heartbeat half of presence, in Redis (ADR 0030
// section 3.1): a key per player that expires by itself. Nothing is written
// when a player goes away.
type PresenceStore interface {
	// Beat marks the player online, refreshing the key's TTL.
	Beat(ctx context.Context, playerID string, at time.Time) error
	// Online says which of playerIDs have a live key. An id absent from the
	// answer is offline.
	Online(ctx context.Context, playerIDs []string) (map[string]bool, error)
}

// PresenceFacts is what the database knows about one player that presence
// needs: nothing here is a client's claim.
type PresenceFacts struct {
	PlayerID    string
	DisplayName string
	PublicCode  string
	Visibility  presence.Visibility
	// CityID is where the player is now, ResidenceCityID where they live;
	// either may be empty.
	CityID          string
	ResidenceCityID string
	// PlaceCode is the place inside the city they stand at ("" for the
	// city's default place).
	PlaceCode string
	// OpenActions are the action types of the player's scheduled or running
	// game_actions that mean something a player is visibly doing.
	OpenActions []string
}

// PresenceRepository is the database half. It is reached through Tx.Presence
// inside a unit of work and through the pool (postgres.NewPresenceRepository)
// outside one; every method is a read except SetVisibility.
type PresenceRepository interface {
	// Visibility is the player's setting, or ErrPlayerNotFound.
	Visibility(ctx context.Context, playerID string) (presence.Visibility, error)
	// SetVisibility stores the setting, or ErrPlayerNotFound.
	SetVisibility(ctx context.Context, playerID string, v presence.Visibility) error
	// Facts reads the facts of each player named; unknown ids are absent.
	Facts(ctx context.Context, playerIDs []string) (map[string]PresenceFacts, error)
	// ContactsAmong says which of playerIDs are the viewer's accepted friends
	// or share the viewer's faction.
	ContactsAmong(ctx context.Context, viewerID string, playerIDs []string) (map[string]bool, error)
	// CountryOf maps each city id to the id of the country jurisdiction it
	// sits under; a city under none is absent.
	CountryOf(ctx context.Context, cityIDs []string) (map[string]string, error)
	// Roster lists the ids of the players who live in or stand in the
	// settlement, by display name, at most limit of them.
	Roster(ctx context.Context, settlementID string, limit int) ([]string, error)
	// Member says whether the player lives in or stands in the settlement.
	Member(ctx context.Context, playerID, settlementID string) (bool, error)
}

// PlayerStatus is what one viewer may see of one player, already shaped by
// presence.Resolve. It is the payload of GET /players/{id}/status.
type PlayerStatus struct {
	PlayerID    string
	DisplayName string
	PublicCode  string
	presence.Detail
}

// SettlementPlayers is the settlement's player list as one viewer may see it.
type SettlementPlayers struct {
	SettlementID string
	Players      []PlayerStatus
	// Online counts the rows the viewer may see online.
	Online int
	// Hidden is true when the viewer's own setting is "nobody", so no row
	// carries presence: a screen says why instead of showing a list of
	// unexplained blanks.
	Hidden bool
}

// PresenceService is the one place the presence rules meet the data: the
// status endpoint, the settlement player list, the Telegram group screen and
// the realtime delta all read through it, so the rule cannot drift between
// them (ADR 0030 section 3.3, last paragraph).
type PresenceService struct {
	Store PresenceStore
	Repo  PresenceRepository
	// RosterLimit bounds one settlement list (presence.roster_limit).
	RosterLimit int
}

// Beat records that the player is connected now. A failure is the caller's
// to log and ignore: presence is best effort and never blocks a command.
func (s *PresenceService) Beat(ctx context.Context, playerID string, at time.Time) error {
	return s.Store.Beat(ctx, playerID, at)
}

// Status is what viewerID may see of targetID. The same player is always
// themself; an unknown target is ErrPlayerNotFound.
func (s *PresenceService) Status(ctx context.Context, viewerID, targetID string) (PlayerStatus, error) {
	rows, err := s.resolve(ctx, viewerID, []string{targetID}, "")
	if err != nil {
		return PlayerStatus{}, err
	}
	if len(rows) == 0 {
		return PlayerStatus{}, ErrPlayerNotFound
	}
	return rows[0], nil
}

// Players is the settlement's list as viewerID may see it. A viewer who is
// not in the settlement is ErrNotInSettlement.
func (s *PresenceService) Players(ctx context.Context, viewerID, settlementID string) (SettlementPlayers, error) {
	ok, err := s.Repo.Member(ctx, viewerID, settlementID)
	if err != nil {
		return SettlementPlayers{}, err
	}
	if !ok {
		return SettlementPlayers{}, ErrNotInSettlement
	}
	limit := s.RosterLimit
	if limit <= 0 {
		limit = 200
	}
	ids, err := s.Repo.Roster(ctx, settlementID, limit)
	if err != nil {
		return SettlementPlayers{}, err
	}
	rows, err := s.resolve(ctx, viewerID, ids, settlementID)
	if err != nil {
		return SettlementPlayers{}, err
	}
	out := SettlementPlayers{SettlementID: settlementID, Players: rows}
	viewerVis, err := s.Repo.Visibility(ctx, viewerID)
	if err != nil {
		return SettlementPlayers{}, err
	}
	out.Hidden = viewerVis == presence.Nobody
	for _, r := range rows {
		if r.Visible && r.Online {
			out.Online++
		}
	}
	return out, nil
}

// resolve reads the facts, the heartbeats and the ties for ids and shapes
// each row for the viewer. inSettlement, when set, is a settlement the
// caller already established both the viewer and every id are in, so the
// relation of every row is the settlement's.
func (s *PresenceService) resolve(ctx context.Context, viewerID string, ids []string, inSettlement string) ([]PlayerStatus, error) {
	all := append([]string{viewerID}, ids...)
	facts, err := s.Repo.Facts(ctx, all)
	if err != nil {
		return nil, err
	}
	viewer, ok := facts[viewerID]
	if !ok {
		return nil, ErrPlayerNotFound
	}
	online, err := s.Store.Online(ctx, ids)
	if err != nil {
		// Presence is best effort: with Redis down everyone reads offline
		// rather than the whole screen failing.
		online = nil
	}
	contacts, err := s.Repo.ContactsAmong(ctx, viewerID, ids)
	if err != nil {
		return nil, err
	}
	var cities []string
	for _, f := range facts {
		if f.CityID != "" {
			cities = append(cities, f.CityID)
		}
		if f.ResidenceCityID != "" {
			cities = append(cities, f.ResidenceCityID)
		}
	}
	countries, err := s.Repo.CountryOf(ctx, cities)
	if err != nil {
		return nil, err
	}

	rows := make([]PlayerStatus, 0, len(ids))
	for _, id := range ids {
		f, ok := facts[id]
		if !ok {
			continue
		}
		rel := presence.Stranger
		switch {
		case id == viewerID:
			rel = presence.Self
		case inSettlement != "" || sharesSettlement(viewer, f):
			rel = presence.Settlement
		case contacts[id]:
			rel = presence.Contact
		case sharesCountry(viewer, f, countries):
			rel = presence.Citizen
		}
		activities := make([]presence.Activity, 0, len(f.OpenActions))
		for _, t := range f.OpenActions {
			if a, ok := presence.ActivityFor(t); ok {
				activities = append(activities, a)
			}
		}
		d := presence.Resolve(presence.Input{
			Relation: rel, Target: f.Visibility, Viewer: viewer.Visibility,
			Online: online[id], Activity: presence.Strongest(activities), Place: f.PlaceCode,
		})
		rows = append(rows, PlayerStatus{PlayerID: id, DisplayName: f.DisplayName, PublicCode: f.PublicCode, Detail: d})
	}
	return rows, nil
}

func sharesSettlement(a, b PresenceFacts) bool {
	for _, x := range []string{a.CityID, a.ResidenceCityID} {
		if x == "" {
			continue
		}
		if x == b.CityID || x == b.ResidenceCityID {
			return true
		}
	}
	return false
}

func sharesCountry(a, b PresenceFacts, countries map[string]string) bool {
	for _, x := range []string{a.CityID, a.ResidenceCityID} {
		cx := countries[x]
		if x == "" || cx == "" {
			continue
		}
		for _, y := range []string{b.CityID, b.ResidenceCityID} {
			if y != "" && countries[y] == cx {
				return true
			}
		}
	}
	return false
}

// Civic is the settlement's roster as its own group reads it: every member
// with the settlement-tier detail, whatever the person pressing the button
// chose for themself. A group screen is shared, so it cannot be shaped by
// one viewer's personal setting, and it must not reveal that setting either
// (the group-privacy rule); a settlement's roster is a civic fact (ADR 0030
// section 3.2, last paragraph, and the owner's default of 2026-09-28).
func (s *PresenceService) Civic(ctx context.Context, settlementID string) (SettlementPlayers, error) {
	limit := s.RosterLimit
	if limit <= 0 {
		limit = 200
	}
	ids, err := s.Repo.Roster(ctx, settlementID, limit)
	if err != nil {
		return SettlementPlayers{}, err
	}
	facts, err := s.Repo.Facts(ctx, ids)
	if err != nil {
		return SettlementPlayers{}, err
	}
	online, err := s.Store.Online(ctx, ids)
	if err != nil {
		online = nil
	}
	out := SettlementPlayers{SettlementID: settlementID}
	for _, id := range ids {
		f, ok := facts[id]
		if !ok {
			continue
		}
		activities := make([]presence.Activity, 0, len(f.OpenActions))
		for _, t := range f.OpenActions {
			if a, ok := presence.ActivityFor(t); ok {
				activities = append(activities, a)
			}
		}
		d := presence.Resolve(presence.Input{
			Relation: presence.Settlement, Target: f.Visibility, Viewer: presence.Everyone,
			Online: online[id], Activity: presence.Strongest(activities), Place: f.PlaceCode,
		})
		out.Players = append(out.Players, PlayerStatus{PlayerID: id, DisplayName: f.DisplayName, PublicCode: f.PublicCode, Detail: d})
		if d.Online {
			out.Online++
		}
	}
	return out, nil
}
