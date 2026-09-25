package notification

import (
	"context"
	"encoding/json"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Specialist recruitment (docs/adr/0027-specialist-recruitment.md):
// privately, a company's owner hears that candidates applied, that one was
// hired by auto-hire, that a campaign ended or filled, that a specialist's
// pay was missed, that a contract was completed, and that a specialist
// left; in a target city's groups, the job advertisement — with no amount.

// recruitEvent is the payload the recruitment handlers write.
type recruitEvent struct {
	CompanyID  string `json:"company_id"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	OwnerID    string `json:"owner_id"`
	CityID     string `json:"city_id"`
	Kind       string `json:"kind"`
	CampaignNo int64  `json:"campaign_no"`
	Count      int    `json:"count"`
	NameSeed   int    `json:"name_seed"`
	Skill      string `json:"skill"`
	Level      int    `json:"level"`
	Reason     string `json:"reason"`
	Amount     int64  `json:"amount"`
}

func decodeRecruit(env *envelope.Envelope, name string) (recruitEvent, error) {
	var ev recruitEvent
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return ev, apperrors.InvalidInput("company." + name + " payload is unreadable").WithCause(err)
	}
	if ev.CompanyID == "" {
		return ev, apperrors.InvalidInput("company." + name + " names no company")
	}
	return ev, nil
}

func (e recruitEvent) ref() screens.CompanyRef {
	return screens.CompanyRef{Code: e.Code, Name: e.Name, Type: screens.Named{Code: e.Type, Name: e.Type}}
}

// renderRecruit renders a recruitment notice to the company's owner.
func renderRecruit(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeRecruit(env, "recruit")
	if err != nil || ev.OwnerID == "" || ev.Kind == "" {
		return nil, err
	}
	view := screens.RecruitNoticeView{Kind: ev.Kind, Company: ev.ref(), CampaignNo: ev.CampaignNo, Count: ev.Count,
		NameSeed: ev.NameSeed, Skill: ev.Skill, Level: ev.Level, Reason: ev.Reason, Amount: ev.Amount}
	return &Draft{PlayerID: ev.OwnerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.RecruitNotice(c, view)
	}}, nil
}

// recruitAdAnnouncement: a company advertises for a specialist in a city.
func recruitAdAnnouncement(ctx context.Context, deps Deps, env *envelope.Envelope) (*Announcement, error) {
	ev, err := decodeRecruit(env, "recruit_ad")
	if err != nil || ev.CityID == "" || ev.Skill == "" {
		return nil, err
	}
	city, err := deps.Cities.ByID(ctx, ev.CityID)
	if err != nil {
		return nil, err
	}
	return &Announcement{CityID: ev.CityID, Line: func(c screens.Context, _ string) string {
		return screens.RecruitAdAnnouncement(c, ev.ref(), ev.Skill, ev.Level, city.Code, city.Name)
	}}, nil
}
