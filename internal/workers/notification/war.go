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

// War (docs/adr/0022-military-and-diplomacy.md, part two): privately, a
// commander and the defender's head of state read an operation's exact
// report, an ally's head of state that its ally was attacked, a head of
// state that a ceasefire or a peace is offered, and the players standing in
// a struck city that it was hit; in the groups of every city of the
// countries at war, a declaration, an ally joining, a ceasefire, a peace, a
// resumption, a strike (in bands) and a city changing hands.

// warEvent is the payload the war handler writes; each event fills its own
// fields.
type warEvent struct {
	PlayerID      string   `json:"player_id"`
	CityIDs       []string `json:"city_ids"`
	Kind          string   `json:"kind"`
	Result        string   `json:"result"`
	CountryCode   string   `json:"country_code"`
	CountryName   string   `json:"country_name"`
	OtherCode     string   `json:"other_code"`
	OtherName     string   `json:"other_name"`
	AllyCode      string   `json:"ally_code"`
	AllyName      string   `json:"ally_name"`
	CityCode      string   `json:"city_code"`
	CityName      string   `json:"city_name"`
	Ground        string   `json:"ground"`
	Band          string   `json:"band"`
	NoticeSeconds int64    `json:"notice_seconds"`
	TTLSeconds    int64    `json:"ttl_seconds"`
	Broke         bool     `json:"broke"`
	Liberated     bool     `json:"liberated"`
	WarNo         int64    `json:"war_no"`
	ProposalNo    int64    `json:"proposal_no"`
	ProposalKind  string   `json:"proposal_kind"`

	Report *screens.StrikeReportView `json:"report"`
}

func decodeWar(env *envelope.Envelope, name string) (warEvent, error) {
	var ev warEvent
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return ev, apperrors.InvalidInput(name + " payload is unreadable").WithCause(err)
	}
	return ev, nil
}

// renderWarNotice: a private notice of war — an ally called, a proposal
// offered, a struck city.
func renderWarNotice(name string) Renderer {
	return func(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
		ev, err := decodeWar(env, name)
		if err != nil || ev.PlayerID == "" {
			return nil, err
		}
		view := screens.WarNoticeView{Kind: ev.Kind, Country: country(ev.CountryCode, ev.CountryName),
			Other: country(ev.OtherCode, ev.OtherName), Ally: country(ev.AllyCode, ev.AllyName), CityCode: ev.CityCode,
			City: ev.CityName, WarNo: ev.WarNo, ProposalNo: ev.ProposalNo, ProposalKind: ev.ProposalKind, Band: ev.Band,
			In: time.Duration(ev.TTLSeconds) * time.Second}
		return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
			return screens.WarNotice(c, view)
		}}, nil
	}
}

// renderWarReport: an operation's exact report, to one player.
func renderWarReport(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeWar(env, "war.report")
	if err != nil || ev.PlayerID == "" || ev.Report == nil {
		return nil, err
	}
	view := *ev.Report
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.StrikeReport(c, view)
	}}, nil
}

// warLine is an announcement of war in the groups of the cities an event
// names.
func warLine(name string, line func(ev warEvent, c screens.Context) string) Announcer {
	return func(_ context.Context, _ Deps, env *envelope.Envelope) (*Announcement, error) {
		ev, err := decodeWar(env, name)
		if err != nil || len(ev.CityIDs) == 0 {
			return nil, err
		}
		return &Announcement{CityIDs: ev.CityIDs, Line: func(c screens.Context, _ string) string { return line(ev, c) }}, nil
	}
}

var (
	warDeclaredAnnouncement = warLine("war.declared", func(ev warEvent, c screens.Context) string {
		return screens.WarDeclaredAnnouncement(c, country(ev.CountryCode, ev.CountryName), country(ev.OtherCode, ev.OtherName),
			ev.Ground, time.Duration(ev.NoticeSeconds)*time.Second, ev.Broke)
	})
	warJoinedAnnouncement = warLine("war.joined", func(ev warEvent, c screens.Context) string {
		return screens.WarJoinedAnnouncement(c, country(ev.CountryCode, ev.CountryName), country(ev.AllyCode, ev.AllyName),
			country(ev.OtherCode, ev.OtherName))
	})
	warSettledAnnouncement = warLine("war.settled", func(ev warEvent, c screens.Context) string {
		return screens.WarSettledAnnouncement(c, ev.Kind, country(ev.CountryCode, ev.CountryName),
			country(ev.OtherCode, ev.OtherName), time.Duration(ev.NoticeSeconds)*time.Second)
	})
	warStruckAnnouncement = warLine("war.struck", func(ev warEvent, c screens.Context) string {
		return screens.StrikeAnnouncement(c, country(ev.CountryCode, ev.CountryName), ev.Kind, ev.Result, ev.CityCode,
			ev.CityName, country(ev.OtherCode, ev.OtherName), ev.Band)
	})
	warTakenAnnouncement = warLine("war.taken", func(ev warEvent, c screens.Context) string {
		return screens.CityTakenAnnouncement(c, ev.Liberated, country(ev.CountryCode, ev.CountryName), ev.CityCode,
			ev.CityName, country(ev.OtherCode, ev.OtherName))
	})
)
