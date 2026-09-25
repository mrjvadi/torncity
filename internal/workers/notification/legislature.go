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

// Legislatures (docs/adr/0024-property-and-politics.md): in the groups of
// every city of the place, a proposal put to a body's vote and the outcome;
// privately, the member who proposed it hears how it went. A legislator's
// vote is public record; the lines carry the count, never an amount of
// anyone's money.

// billEvent is the payload the legislature handler writes.
type billEvent struct {
	No            int64            `json:"no"`
	Status        string           `json:"status"`
	Lapse         string           `json:"lapse"`
	Yes           int              `json:"yes"`
	Nay           int              `json:"nay"`
	PlaceKind     string           `json:"place_kind"`
	PlaceCode     string           `json:"place_code"`
	PlaceName     string           `json:"place_name"`
	SubjectKind   string           `json:"subject_kind"`
	Subject       string           `json:"subject"`
	LeverType     string           `json:"lever_type"`
	Value         int64            `json:"value"`
	Allocation    map[string]int64 `json:"allocation"`
	Categories    []string         `json:"categories"`
	Office        string           `json:"office"`
	ByName        string           `json:"by_name"`
	ByCode        string           `json:"by_code"`
	Body          string           `json:"body"`
	TargetCode    string           `json:"target_code"`
	TargetName    string           `json:"target_name"`
	CityIDs       []string         `json:"city_ids"`
	PlayerID      string           `json:"player_id"`
	WindowSeconds int64            `json:"window_seconds"`
}

func decodeBill(env *envelope.Envelope, name string) (billEvent, error) {
	var ev billEvent
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return ev, apperrors.InvalidInput("legislature." + name + " payload is unreadable").WithCause(err)
	}
	if ev.No < 1 {
		return ev, apperrors.InvalidInput("legislature." + name + " names no proposal")
	}
	return ev, nil
}

func (e billEvent) view() screens.BillView {
	v := screens.BillView{No: e.No, Status: e.Status, LapsedWhy: e.Lapse, Yes: e.Yes, Nay: e.Nay,
		Place:  screens.GovPlace{Kind: e.PlaceKind, Code: e.PlaceCode, Name: e.PlaceName},
		Office: e.Office, By: screens.GovPlayer{Name: e.ByName, Code: e.ByCode}, Body: e.Body,
		Subject: screens.BillSubject{Kind: e.SubjectKind, Code: e.Subject, LeverType: e.LeverType, Value: e.Value,
			Allocation: e.Allocation, Categories: e.Categories}}
	if e.TargetCode != "" {
		v.Subject.Target = &screens.GovPlace{Kind: "country", Code: e.TargetCode, Name: e.TargetName}
	}
	return v
}

// billOpenedAnnouncement: a proposal was put to a body's vote.
func billOpenedAnnouncement(_ context.Context, _ Deps, env *envelope.Envelope) (*Announcement, error) {
	ev, err := decodeBill(env, "proposed")
	if err != nil || len(ev.CityIDs) == 0 {
		return nil, err
	}
	window := time.Duration(ev.WindowSeconds) * time.Second
	return &Announcement{CityIDs: ev.CityIDs, Line: func(c screens.Context, _ string) string {
		return screens.BillOpenedAnnouncement(c, ev.view(), window)
	}}, nil
}

// billDecidedAnnouncement: a proposal passed, failed or lapsed.
func billDecidedAnnouncement(_ context.Context, _ Deps, env *envelope.Envelope) (*Announcement, error) {
	ev, err := decodeBill(env, "decided")
	if err != nil || len(ev.CityIDs) == 0 {
		return nil, err
	}
	return &Announcement{CityIDs: ev.CityIDs, Line: func(c screens.Context, _ string) string {
		return screens.BillDecidedAnnouncement(c, ev.view())
	}}, nil
}

// renderBillDecided tells the member who proposed it how it went.
func renderBillDecided(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeBill(env, "decided")
	if err != nil || ev.PlayerID == "" {
		return nil, err
	}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.BillDecidedNotice(c, ev.view())
	}}, nil
}
