package notification

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The armed forces, diplomacy and appointments
// (docs/adr/0022-military-and-diplomacy.md): privately, a commander hears
// that equipment reached its garrison, a foreign minister that a treaty is
// proposed to their country, a player that they were appointed to or
// removed from office; in the groups of every city of the countries
// concerned, a sanction imposed or lifted, a treaty signed or ended, arms
// acquired (in a band, never a count or a price) and an appointment.

// stateEvent is the payload the military, diplomacy and appointment
// handlers write; each event fills its own fields.
type stateEvent struct {
	PlayerID    string   `json:"player_id"`
	PlayerName  string   `json:"player_name"`
	CityIDs     []string `json:"city_ids"`
	CountryCode string   `json:"country_code"`
	CountryName string   `json:"country_name"`
	OtherCode   string   `json:"other_code"`
	OtherName   string   `json:"other_name"`

	// The armed forces.
	Class      string `json:"class"`
	ClassName  string `json:"class_name"`
	Band       string `json:"band"`
	Item       string `json:"item"`
	Design     string `json:"design"`
	DesignNo   int64  `json:"design_no"`
	Qty        int64  `json:"qty"`
	CityCode   string `json:"city_code"`
	CityName   string `json:"city_name"`
	Branch     string `json:"branch"`
	BranchName string `json:"branch_name"`

	// Diplomacy.
	ImposerCode string   `json:"imposer_code"`
	ImposerName string   `json:"imposer_name"`
	TargetCode  string   `json:"target_code"`
	TargetName  string   `json:"target_name"`
	Measures    []string `json:"measures"`
	Ground      string   `json:"ground"`
	ACode       string   `json:"a_code"`
	AName       string   `json:"a_name"`
	BCode       string   `json:"b_code"`
	BName       string   `json:"b_name"`
	ByCode      string   `json:"by_code"`
	ByName      string   `json:"by_name"`
	Kind        string   `json:"kind"`
	KindName    string   `json:"kind_name"`
	No          int64    `json:"no"`
	TTLSeconds  int64    `json:"ttl_seconds"`

	// Appointments.
	Office    string `json:"office"`
	PlaceKind string `json:"place_kind"`
	PlaceCode string `json:"place_code"`
	PlaceName string `json:"place_name"`
	ByOffice  string `json:"by_office"`
}

func decodeState(env *envelope.Envelope, name string) (stateEvent, error) {
	var ev stateEvent
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return ev, apperrors.InvalidInput(name + " payload is unreadable").WithCause(err)
	}
	return ev, nil
}

func country(code, name string) screens.GovPlace {
	return screens.GovPlace{Kind: "country", Code: code, Name: name}
}

// renderMoveArrived: equipment the player ordered reached its garrison.
func renderMoveArrived(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeState(env, "military.arrived")
	if err != nil || ev.PlayerID == "" {
		return nil, err
	}
	view := screens.MilitaryNoticeView{Country: country(ev.CountryCode, ev.CountryName),
		Good: screens.Good{Item: screens.Named{Code: ev.Item, Name: ev.Item}, Design: ev.Design, DesignNo: ev.DesignNo},
		Qty:  ev.Qty, CityCode: ev.CityCode, City: ev.CityName, Branch: screens.Named{Code: ev.Branch, Name: ev.BranchName}}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.MoveArrivedNotice(c, view)
	}}, nil
}

// renderTreatyProposed: a treaty was proposed to the player's country.
func renderTreatyProposed(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeState(env, "diplomacy.treaty_proposed")
	if err != nil || ev.PlayerID == "" {
		return nil, err
	}
	view := screens.TreatyNoticeView{Country: country(ev.CountryCode, ev.CountryName),
		Other: country(ev.OtherCode, ev.OtherName), Kind: screens.Named{Code: ev.Kind, Name: ev.KindName}, No: ev.No,
		TTL: time.Duration(ev.TTLSeconds) * time.Second}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.TreatyProposedNotice(c, view)
	}}, nil
}

// renderOffice: the player was appointed to or removed from office.
func renderOffice(dismissed bool) Renderer {
	return func(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
		ev, err := decodeState(env, "governance.appointed")
		if err != nil || ev.PlayerID == "" {
			return nil, err
		}
		view := screens.OfficeNoticeView{Office: ev.Office,
			Place: screens.GovPlace{Kind: ev.PlaceKind, Code: ev.PlaceCode, Name: ev.PlaceName},
			By:    screens.GovPlayer{Name: ev.ByName, Code: ev.ByCode}, ByOffice: ev.ByOffice, Dismissed: dismissed}
		return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
			return screens.OfficeNotice(c, view)
		}}, nil
	}
}

// countryLine is an announcement in the groups of the cities an event names.
func countryLine(name string, line func(ev stateEvent, c screens.Context, player string) string) Announcer {
	return func(_ context.Context, _ Deps, env *envelope.Envelope) (*Announcement, error) {
		ev, err := decodeState(env, name)
		if err != nil || len(ev.CityIDs) == 0 {
			return nil, err
		}
		return &Announcement{CityIDs: ev.CityIDs, PlayerID: ev.PlayerID, Name: ev.PlayerName,
			Line: func(c screens.Context, player string) string { return line(ev, c, player) }}, nil
	}
}

var (
	procuredAnnouncement = countryLine("military.procured", func(ev stateEvent, c screens.Context, _ string) string {
		return screens.ProcurementAnnouncement(c, country(ev.CountryCode, ev.CountryName),
			screens.Named{Code: ev.Class, Name: ev.ClassName}, ev.Band)
	})
	sanctionImposedAnnouncement = countryLine("diplomacy.sanction_imposed", func(ev stateEvent, c screens.Context, _ string) string {
		return screens.SanctionImposedAnnouncement(c, country(ev.ImposerCode, ev.ImposerName),
			country(ev.TargetCode, ev.TargetName), ev.Measures, ev.Ground)
	})
	sanctionLiftedAnnouncement = countryLine("diplomacy.sanction_lifted", func(ev stateEvent, c screens.Context, _ string) string {
		return screens.SanctionLiftedAnnouncement(c, country(ev.ImposerCode, ev.ImposerName), country(ev.TargetCode, ev.TargetName))
	})
	treatySignedAnnouncement = countryLine("diplomacy.treaty_signed", func(ev stateEvent, c screens.Context, _ string) string {
		return screens.TreatySignedAnnouncement(c, country(ev.ACode, ev.AName), country(ev.BCode, ev.BName),
			screens.Named{Code: ev.Kind, Name: ev.KindName})
	})
	treatyEndedAnnouncement = countryLine("diplomacy.treaty_terminated", func(ev stateEvent, c screens.Context, _ string) string {
		return screens.TreatyEndedAnnouncement(c, country(ev.ByCode, ev.ByName), country(ev.OtherCode, ev.OtherName),
			screens.Named{Code: ev.Kind, Name: ev.KindName})
	})
	appointedAnnouncement = countryLine("governance.appointed", func(ev stateEvent, c screens.Context, player string) string {
		return screens.AppointedAnnouncement(c, player, ev.Office,
			screens.GovPlace{Kind: ev.PlaceKind, Code: ev.PlaceCode, Name: ev.PlaceName})
	})
)
