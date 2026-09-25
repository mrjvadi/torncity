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

// The crime engine's notices (docs/adr/0019-crime-engine.md). Every one is
// private: a notice always goes to the player's own chat with the bot, and
// what it tells — a theft suffered, a take, a sentence — is theirs alone.
// Each payload carries codes and authored names, never ids to look up, so a
// notice renders in the reader's language from the catalogue alone.

// crimeResult is the payload CrimeHandler writes for a take told privately
// (crime.take) and for a timed crime's end (crime.resolved).
type crimeResult struct {
	PlayerID     string `json:"player_id"`
	Player       string `json:"player"`
	Crime        string `json:"crime"`
	CrimeName    string `json:"crime_name"`
	Venue        string `json:"venue"`
	VenueName    string `json:"venue_name"`
	CityCode     string `json:"city_code"`
	CityName     string `json:"city_name"`
	Result       string `json:"result"`
	VictimPlayer bool   `json:"victim_player"`
	Take         int64  `json:"take"`
	Dry          bool   `json:"dry"`
	XP           int64  `json:"xp"`
	CriminalXP   int64  `json:"criminal_xp"`
	Skills       []struct {
		Skill string `json:"skill"`
		XP    int64  `json:"xp"`
		Level int    `json:"level"`
	} `json:"skills"`
	Level          int       `json:"level"`
	Heat           int       `json:"heat"`
	HeatMax        int       `json:"heat_max"`
	Wanted         int       `json:"wanted"`
	Stars          int       `json:"stars"`
	Nerve          int       `json:"nerve"`
	NerveMax       int       `json:"nerve_max"`
	NerveFullInSec int64     `json:"nerve_full_in_seconds"`
	Fine           int64     `json:"fine"`
	FinePaid       int64     `json:"fine_paid"`
	JailSeconds    int64     `json:"jail_seconds"`
	JailEndsAt     time.Time `json:"jail_ends_at"`
	Loot           []struct {
		Item     string `json:"item"`
		ItemName string `json:"item_name"`
		Qty      int64  `json:"qty"`
	} `json:"loot"`
	StolenItem     string `json:"stolen_item"`
	StolenItemName string `json:"stolen_item_name"`
	Confiscated    []struct {
		Item     string `json:"item"`
		ItemName string `json:"item_name"`
	} `json:"confiscated"`
	Injury *injuryPayload `json:"injury"`
}

// injuryPayload is an injury as an event carries it (handlers.injured).
type injuryPayload struct {
	Damage    int       `json:"damage"`
	Health    int       `json:"health"`
	MaxHealth int       `json:"max_health"`
	Hospital  bool      `json:"hospital"`
	EndsAt    time.Time `json:"ends_at"`
}

// view is the injury as a screen shows it; nil for none.
func (p *injuryPayload) view() *screens.InjuryView {
	if p == nil || p.Damage <= 0 {
		return nil
	}
	return &screens.InjuryView{Damage: p.Damage, Health: p.Health, Max: p.MaxHealth, Hospital: p.Hospital, EndsAt: p.EndsAt}
}

// renderCrimeResult tells a thief how an attempt ended: the take of a
// success a group only heard about, or the end of a timed crime.
func renderCrimeResult(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	var ev crimeResult
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("crime result payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" || ev.Crime == "" {
		return nil, apperrors.InvalidInput("crime result names no player or no crime")
	}
	view := screens.CrimeResultView{
		Player: ev.Player, Crime: screens.Named{Code: ev.Crime, Name: ev.CrimeName},
		Venue: screens.Named{Code: ev.Venue, Name: ev.VenueName}, CityCode: ev.CityCode, City: ev.CityName,
		Result: ev.Result, VictimPlayer: ev.VictimPlayer, Take: ev.Take, DrySpell: ev.Dry,
		XP: ev.XP, CriminalXP: ev.CriminalXP, Level: ev.Level,
		Heat:  screens.HeatView{Heat: ev.Heat, Max: ev.HeatMax, Wanted: ev.Wanted, Stars: ev.Stars},
		Nerve: screens.NerveView{Nerve: ev.Nerve, Max: ev.NerveMax, FullIn: time.Duration(ev.NerveFullInSec) * time.Second},
		Fine:  ev.Fine, FinePaid: ev.FinePaid, Notice: true, Injury: ev.Injury.view(),
	}
	for _, s := range ev.Skills {
		view.Skills = append(view.Skills, screens.SkillGain{Skill: s.Skill, XP: s.XP, Level: s.Level})
	}
	if ev.JailSeconds > 0 {
		view.Jail = &screens.CrimeProgress{Remaining: time.Duration(ev.JailSeconds) * time.Second, EndsAt: ev.JailEndsAt}
	}
	for _, l := range ev.Loot {
		view.Loot = append(view.Loot, screens.LootLine{Item: screens.Named{Code: l.Item, Name: l.ItemName}, Qty: l.Qty})
	}
	if ev.StolenItem != "" {
		view.Stolen = &screens.Named{Code: ev.StolenItem, Name: ev.StolenItemName}
	}
	for _, c := range ev.Confiscated {
		view.Confiscated = append(view.Confiscated, screens.Named{Code: c.Item, Name: c.ItemName})
	}
	return &Draft{
		PlayerID: ev.PlayerID,
		Screen: func(c screens.Context) *presenter.Response {
			c.Shared = false
			return screens.CrimeResult(c, view).MarkPrivate()
		},
	}, nil
}

// victimised is the payload of crime.victimised.
type victimised struct {
	VictimID  string `json:"victim_id"`
	AttemptID string `json:"attempt_id"`
	Crime     string `json:"crime"`
	CrimeName string `json:"crime_name"`
	Venue     string `json:"venue"`
	VenueName string `json:"venue_name"`
	CityCode  string `json:"city_code"`
	CityName  string `json:"city_name"`
	Amount    int64  `json:"amount"`
	ThiefName string `json:"thief_name"`
	ThiefCode string `json:"thief_code"`
	ReportFee int64  `json:"report_fee"`
	// Item and ItemName are a good taken beside the money, if any.
	Item     string `json:"item"`
	ItemName string `json:"item_name"`
	// ReportWindowSeconds is how long the victim has to report, from the
	// theft.
	ReportWindowSeconds int64 `json:"report_window_seconds"`
}

// renderVictimised tells a player they were robbed, names the thief only if
// a witness saw them, and offers the report.
func renderVictimised(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	var ev victimised
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("crime.victimised payload is unreadable").WithCause(err)
	}
	if ev.VictimID == "" || ev.AttemptID == "" || (ev.Amount <= 0 && ev.Item == "") {
		return nil, apperrors.InvalidInput("crime.victimised names no victim, no theft or nothing taken")
	}
	view := screens.VictimNoticeView{
		Crime: screens.Named{Code: ev.Crime, Name: ev.CrimeName}, Venue: screens.Named{Code: ev.Venue, Name: ev.VenueName},
		CityCode: ev.CityCode, City: ev.CityName, Amount: ev.Amount, ThiefName: ev.ThiefName, ThiefCode: ev.ThiefCode,
		CrimeID: ev.AttemptID, ReportFee: ev.ReportFee,
		ReportWithin: time.Duration(ev.ReportWindowSeconds) * time.Second,
	}
	if ev.Item != "" {
		view.Item = &screens.Named{Code: ev.Item, Name: ev.ItemName}
	}
	return &Draft{
		PlayerID: ev.VictimID,
		Screen:   func(c screens.Context) *presenter.Response { return screens.VictimNotice(c, view) },
	}, nil
}

// released is the payload of crime.released.
type released struct {
	PlayerID string `json:"player_id"`
	CityCode string `json:"city_code"`
	CityName string `json:"city_name"`
}

// renderReleased tells a player their sentence is served.
func renderReleased(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	var ev released
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("crime.released payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" {
		return nil, apperrors.InvalidInput("crime.released names no player")
	}
	city := screens.Named{Code: ev.CityCode, Name: ev.CityName}
	return &Draft{
		PlayerID: ev.PlayerID,
		Screen:   func(c screens.Context) *presenter.Response { return screens.ReleasedNotice(c, city) },
	}, nil
}

// caseOutcome is the payload of crime.case_solved, crime.case_closed and
// crime.convicted.
type caseOutcome struct {
	VictimID    string `json:"victim_id"`
	ThiefID     string `json:"thief_id"`
	ThiefName   string `json:"thief_name"`
	ThiefCode   string `json:"thief_code"`
	Crime       string `json:"crime"`
	CrimeName   string `json:"crime_name"`
	CityCode    string `json:"city_code"`
	CityName    string `json:"city_name"`
	Stolen      int64  `json:"stolen"`
	Restored    int64  `json:"restored"`
	Shortfall   int64  `json:"shortfall"`
	Fine        int64  `json:"fine"`
	FinePaid    int64  `json:"fine_paid"`
	TermSeconds int64  `json:"term_seconds"`
	// ItemReturned is a stolen good given back to the victim.
	ItemReturned     string `json:"item_returned"`
	ItemReturnedName string `json:"item_returned_name"`
}

func (e caseOutcome) view(solved bool) screens.CaseOutcomeView {
	return screens.CaseOutcomeView{
		Crime: screens.Named{Code: e.Crime, Name: e.CrimeName}, CityCode: e.CityCode, City: e.CityName,
		Solved: solved, Thief: e.ThiefName, ThiefCode: e.ThiefCode, Stolen: e.Stolen, Restored: e.Restored,
		Shortfall: e.Shortfall, Fine: e.Fine, FinePaid: e.FinePaid, Term: time.Duration(e.TermSeconds) * time.Second,
		Returned: returned(e.ItemReturned, e.ItemReturnedName),
	}
}

// returned is a stolen good given back, nil for none.
func returned(code, name string) *screens.Named {
	if code == "" {
		return nil
	}
	return &screens.Named{Code: code, Name: name}
}

func decodeCase(env *envelope.Envelope, name string) (caseOutcome, error) {
	var ev caseOutcome
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return ev, apperrors.InvalidInput("crime." + name + " payload is unreadable").WithCause(err)
	}
	return ev, nil
}

// renderCaseSolved tells a victim the thief was caught and what came back.
func renderCaseSolved(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeCase(env, "case_solved")
	if err != nil {
		return nil, err
	}
	if ev.VictimID == "" {
		return nil, apperrors.InvalidInput("crime.case_solved names no victim")
	}
	view := ev.view(true)
	return &Draft{
		PlayerID: ev.VictimID,
		Screen:   func(c screens.Context) *presenter.Response { return screens.CaseSolvedNotice(c, view) },
	}, nil
}

// renderCaseClosed tells a victim the police found nobody.
func renderCaseClosed(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeCase(env, "case_closed")
	if err != nil {
		return nil, err
	}
	if ev.VictimID == "" {
		return nil, apperrors.InvalidInput("crime.case_closed names no victim")
	}
	view := ev.view(false)
	return &Draft{
		PlayerID: ev.VictimID,
		Screen:   func(c screens.Context) *presenter.Response { return screens.CaseSolvedNotice(c, view) },
	}, nil
}

// renderConvicted tells a thief a reported theft was traced to them.
func renderConvicted(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeCase(env, "convicted")
	if err != nil {
		return nil, err
	}
	if ev.ThiefID == "" {
		return nil, apperrors.InvalidInput("crime.convicted names no thief")
	}
	view := ev.view(true)
	return &Draft{
		PlayerID: ev.ThiefID,
		Screen:   func(c screens.Context) *presenter.Response { return screens.ConvictedNotice(c, view) },
	}, nil
}
