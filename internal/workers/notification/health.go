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

// Health's notices (docs/adr/0023-health-missions-factions.md): taken to
// hospital, discharged, and — to a clinic's owner — a patient treated. Each
// is private. A city's groups read that a player was taken to hospital there,
// never why nor for how long.

// hospitalised is the payload of health.hospitalised.
type hospitalised struct {
	StayID     string    `json:"stay_id"`
	PlayerID   string    `json:"player_id"`
	PlayerName string    `json:"player_name"`
	CityID     string    `json:"city_id"`
	Cause      string    `json:"cause"`
	Damage     int       `json:"damage"`
	Health     int       `json:"health"`
	MaxHealth  int       `json:"max_health"`
	EndsAt     time.Time `json:"ends_at"`
}

func decodeHospitalised(env *envelope.Envelope) (hospitalised, error) {
	var ev hospitalised
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return ev, apperrors.InvalidInput("health.hospitalised payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" || ev.CityID == "" {
		return ev, apperrors.InvalidInput("health.hospitalised names no player or no city")
	}
	return ev, nil
}

// renderHospitalised tells a player they were taken to hospital.
func renderHospitalised(ctx context.Context, deps Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeHospitalised(env)
	if err != nil {
		return nil, err
	}
	city, err := deps.Cities.ByID(ctx, ev.CityID)
	if err != nil {
		return nil, err
	}
	view := screens.HospitalisedNoticeView{CityCode: city.Code, City: city.Name, Cause: ev.Cause, Damage: ev.Damage,
		Health: ev.Health, Max: ev.MaxHealth, EndsAt: ev.EndsAt, Remaining: time.Until(ev.EndsAt)}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.HospitalisedNotice(c, view)
	}}, nil
}

// hospitalisedAnnouncement: the city's groups read who was taken to
// hospital there.
func hospitalisedAnnouncement(ctx context.Context, deps Deps, env *envelope.Envelope) (*Announcement, error) {
	ev, err := decodeHospitalised(env)
	if err != nil {
		return nil, err
	}
	city, err := deps.Cities.ByID(ctx, ev.CityID)
	if err != nil {
		return nil, err
	}
	return &Announcement{CityID: city.ID, PlayerID: ev.PlayerID, Name: ev.PlayerName,
		Line: func(c screens.Context, name string) string {
			return screens.HospitalisedLine(c, name, city.Code, city.Name)
		}}, nil
}

// discharged is the payload of health.discharged.
type discharged struct {
	PlayerID  string `json:"player_id"`
	CityCode  string `json:"city_code"`
	CityName  string `json:"city_name"`
	Health    int    `json:"health"`
	MaxHealth int    `json:"max_health"`
}

// renderDischarged tells a player they left hospital.
func renderDischarged(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	var ev discharged
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("health.discharged payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" {
		return nil, apperrors.InvalidInput("health.discharged names no player")
	}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.DischargedNotice(c, ev.CityCode, ev.CityName, ev.Health, ev.MaxHealth)
	}}, nil
}

// clinicTreated is the payload of health.clinic_treated.
type clinicTreated struct {
	PlayerID      string `json:"player_id"`
	CompanyCode   string `json:"company_code"`
	CompanyName   string `json:"company_name"`
	PatientName   string `json:"patient_name"`
	Price         int64  `json:"price"`
	Medicine      string `json:"medicine"`
	MedicineUnits int    `json:"medicine_units"`
	SavedSeconds  int64  `json:"saved_seconds"`
}

// renderClinicTreated tells a clinic's owner it treated a patient, and what
// it earned.
func renderClinicTreated(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	var ev clinicTreated
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return nil, apperrors.InvalidInput("health.clinic_treated payload is unreadable").WithCause(err)
	}
	if ev.PlayerID == "" || ev.CompanyCode == "" {
		return nil, apperrors.InvalidInput("health.clinic_treated names no owner or no clinic")
	}
	view := screens.ClinicTreatedNoticeView{Ref: screens.CompanyRef{Code: ev.CompanyCode, Name: ev.CompanyName},
		Patient: ev.PatientName, Price: ev.Price, Item: screens.Named{Code: ev.Medicine, Name: ev.Medicine},
		Units: ev.MedicineUnits, Saved: time.Duration(ev.SavedSeconds) * time.Second}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.ClinicTreatedNotice(c, view)
	}}, nil
}
