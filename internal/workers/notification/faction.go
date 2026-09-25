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

// Factions' notices and lines (docs/adr/0023-health-missions-factions.md). A
// notice is private: an invitation or an application with the buttons to
// answer it, an answer, a kick, and each crew member's own end of an
// organised crime with their share. A faction's news — who joined or left,
// a crime planned, launched and how it ended — is posted in the Telegram
// group the faction is linked to, never with an amount; that a faction was
// founded is posted in its city's groups.

// factionEvent is the payload of a faction event; each reads its own.
type factionEvent struct {
	PlayerID     string    `json:"player_id"`
	PlayerName   string    `json:"player_name"`
	FactionCode  string    `json:"faction_code"`
	FactionName  string    `json:"faction_name"`
	ChatID       int64     `json:"chat_id"`
	BotID        string    `json:"bot_id"`
	ChatLanguage string    `json:"chat_language"`
	CityID       string    `json:"city_id"`
	No           int64     `json:"no"`
	Kind         string    `json:"kind"`
	Accepted     bool      `json:"accepted"`
	ByName       string    `json:"by_name"`
	ByCode       string    `json:"by_code"`
	JoinerName   string    `json:"joiner_name"`
	JoinerCode   string    `json:"joiner_code"`
	Crime        string    `json:"crime"`
	CrimeName    string    `json:"crime_name"`
	Place        string    `json:"place"`
	PlaceName    string    `json:"place_name"`
	Result       string    `json:"result"`
	Rank         string    `json:"rank"`
	Share        int64     `json:"share"`
	Take         int64     `json:"take"`
	Cut          int64     `json:"cut"`
	XP           int64     `json:"xp"`
	JailSeconds  int64     `json:"jail_seconds"`
	JailEndsAt   time.Time `json:"jail_ends_at"`
	Fine         int64     `json:"fine"`
	FinePaid     int64     `json:"fine_paid"`

	Injury *injuryPayload `json:"injury"`
}

func (e factionEvent) ref() screens.FactionRef {
	return screens.FactionRef{Code: e.FactionCode, Name: e.FactionName}
}

func decodeFaction(env *envelope.Envelope, name string) (factionEvent, error) {
	var ev factionEvent
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return ev, apperrors.InvalidInput("faction." + name + " payload is unreadable").WithCause(err)
	}
	if ev.FactionCode == "" {
		return ev, apperrors.InvalidInput("faction." + name + " names no faction")
	}
	return ev, nil
}

// renderFactionRequest tells a player they were invited, or a member whose
// rank decides that someone applied.
func renderFactionRequest(kind string) Renderer {
	return func(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
		ev, err := decodeFaction(env, kind)
		if err != nil {
			return nil, err
		}
		if ev.PlayerID == "" || ev.No <= 0 {
			return nil, apperrors.InvalidInput("a faction request names nobody or no request")
		}
		view := screens.FactionRequestNoticeView{No: ev.No, Kind: kind, Ref: ev.ref(),
			Player: screens.GovPlayer{Name: ev.ByName, Code: ev.ByCode}}
		return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
			return screens.FactionRequestNotice(c, view)
		}}, nil
	}
}

// renderFactionAnswer tells the other side of a request how it was answered.
func renderFactionAnswer(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeFaction(env, "answered")
	if err != nil {
		return nil, err
	}
	if ev.PlayerID == "" {
		return nil, apperrors.InvalidInput("faction.answered names nobody")
	}
	view := screens.FactionAnsweredView{Ref: ev.ref(), Kind: ev.Kind, Accepted: ev.Accepted,
		Player: screens.GovPlayer{Name: ev.JoinerName, Code: ev.JoinerCode}}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.FactionAnswerNotice(c, view)
	}}, nil
}

// renderFactionKicked tells a player they were removed.
func renderFactionKicked(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeFaction(env, "kicked")
	if err != nil {
		return nil, err
	}
	if ev.PlayerID == "" {
		return nil, apperrors.InvalidInput("faction.kicked names nobody")
	}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.FactionKickedNotice(c, ev.ref(), ev.ByName)
	}}, nil
}

// renderFactionCrime tells one crew member how an organised crime ended.
func renderFactionCrime(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeFaction(env, "crime_settled")
	if err != nil {
		return nil, err
	}
	if ev.PlayerID == "" || ev.Result == "" {
		return nil, apperrors.InvalidInput("faction.crime_settled names nobody or no result")
	}
	view := screens.FactionCrimeNoticeView{Ref: ev.ref(), Crime: screens.Named{Code: ev.Crime, Name: ev.CrimeName},
		Result: ev.Result, Share: ev.Share, Take: ev.Take, Cut: ev.Cut, XP: ev.XP, Fine: ev.Fine, FinePaid: ev.FinePaid,
		Injury: ev.Injury.view()}
	if ev.JailSeconds > 0 {
		view.Jail = &screens.CrimeProgress{Remaining: time.Duration(ev.JailSeconds) * time.Second, EndsAt: ev.JailEndsAt}
	}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.FactionCrimeNotice(c, view)
	}}, nil
}

// factionGroupLine is a line in the faction's own group; nothing when the
// faction has none.
func factionGroupLine(kind string) Announcer {
	return func(_ context.Context, _ Deps, env *envelope.Envelope) (*Announcement, error) {
		ev, err := decodeFaction(env, kind)
		if err != nil || ev.ChatID >= 0 {
			return nil, err
		}
		name := ev.PlayerName
		return &Announcement{ChatID: ev.ChatID, BotID: ev.BotID, Language: ev.ChatLanguage, Name: name,
			PlayerID: ev.PlayerID,
			Line: func(c screens.Context, shown string) string {
				return screens.FactionGroupLine(c, kind, ev.ref(), shown, screens.Named{Code: ev.Crime, Name: ev.CrimeName},
					screens.Named{Code: ev.Place, Name: ev.PlaceName}, ev.Result, ev.Rank)
			}}, nil
	}
}

// factionFoundedLine: the city's groups read that a faction was founded
// there.
func factionFoundedLine(ctx context.Context, deps Deps, env *envelope.Envelope) (*Announcement, error) {
	ev, err := decodeFaction(env, "founded")
	if err != nil || ev.CityID == "" {
		return nil, err
	}
	city, err := deps.Cities.ByID(ctx, ev.CityID)
	if err != nil {
		return nil, err
	}
	return &Announcement{CityID: city.ID, PlayerID: ev.PlayerID, Name: ev.PlayerName,
		Line: func(c screens.Context, shown string) string {
			return screens.FactionGroupLine(c, "founded", ev.ref(), shown, screens.Named{}, screens.Named{}, "", "")
		}}, nil
}
