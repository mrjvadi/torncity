package notification

import (
	"context"
	"encoding/json"

	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Companies (docs/adr/0020-companies.md): privately, an owner or a manager
// hears of an application, an applicant or an employee of what a company
// did to them, a new manager of the appointment and an owner of each
// period's books; in a city's groups, a company founded and a company
// closed. No public line carries an amount.

// companyEvent is the payload shape the companies handler writes; each
// event reads the fields it needs.
type companyEvent struct {
	CompanyID   string `json:"company_id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	TypeName    string `json:"type_name"`
	CityID      string `json:"city_id"`
	PlayerID    string `json:"player_id"`
	PlayerName  string `json:"player_name"`
	PlayerCode  string `json:"player_code"`
	RecipientID string `json:"recipient_id"`
	OwnerID     string `json:"owner_id"`
	OwnerName   string `json:"owner_name"`
	OwnerCode   string `json:"owner_code"`
	Kind        string `json:"kind"`
	Reason      string `json:"reason"`
	No          int64  `json:"no"`
	Level       int    `json:"level"`
	Career      string `json:"career"`
	CareerName  string `json:"career_name"`
	Rank        string `json:"rank"`
	Title       string `json:"title"`
	Wage        int64  `json:"wage"`

	Revenue    int64 `json:"revenue"`
	SalesTax   int64 `json:"sales_tax"`
	Wages      int64 `json:"wages"`
	UpkeepDue  int64 `json:"upkeep_due"`
	UpkeepPaid int64 `json:"upkeep_paid"`
	Debt       int64 `json:"debt"`
	Arrears    int   `json:"arrears"`
	Grace      int   `json:"grace"`
	Dissolved  bool  `json:"dissolved"`
	Shifts     int   `json:"shifts"`
	QualityBPS int   `json:"quality_bps"`
	Sold       int64 `json:"sold"`
	Wanted     int64 `json:"wanted"`
	Capacity   int64 `json:"capacity"`
	Balance    int64 `json:"balance"`

	CitizenWorkers int   `json:"citizen_workers"`
	CitizenShifts  int   `json:"citizen_shifts"`
	CitizenWages   int64 `json:"citizen_wages"`
}

func decodeCompany(env *envelope.Envelope, name string) (companyEvent, error) {
	var ev companyEvent
	if err := json.Unmarshal(env.Payload, &ev); err != nil {
		return ev, apperrors.InvalidInput("company." + name + " payload is unreadable").WithCause(err)
	}
	if ev.CompanyID == "" {
		return ev, apperrors.InvalidInput("company." + name + " names no company")
	}
	return ev, nil
}

func (e companyEvent) ref() screens.CompanyRef {
	return screens.CompanyRef{Code: e.Code, Name: e.Name, Type: screens.Named{Code: e.Type, Name: e.TypeName}}
}

func (e companyEvent) job() screens.JobRef {
	return screens.JobRef{CareerCode: e.Career, CareerName: e.CareerName, Rank: e.Rank, Title: e.Title}
}

// renderCompanyApplied tells an owner or a manager of an application.
func renderCompanyApplied(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeCompany(env, "applied")
	if err != nil {
		return nil, err
	}
	if ev.RecipientID == "" {
		return nil, apperrors.InvalidInput("company.applied names nobody to tell")
	}
	view := screens.CompanyApplicationNoticeView{No: ev.No, Company: ev.ref(), Job: ev.job(), Level: ev.Level,
		Player: screens.GovPlayer{Name: ev.PlayerName, Code: ev.PlayerCode}}
	return &Draft{PlayerID: ev.RecipientID, Screen: func(c screens.Context) *presenter.Response {
		return screens.CompanyApplicationNotice(c, view)
	}}, nil
}

// renderCompanyEmployee tells an applicant or an employee what a company
// did: hired, turned down, fired, or closed.
func renderCompanyEmployee(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeCompany(env, "employee")
	if err != nil {
		return nil, err
	}
	if ev.PlayerID == "" || ev.Kind == "" {
		return nil, apperrors.InvalidInput("company.employee names no player or no kind")
	}
	view := screens.CompanyEmployeeNoticeView{Kind: ev.Kind, Company: ev.ref(), Job: ev.job(), Wage: ev.Wage}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.CompanyEmployeeNotice(c, view)
	}}, nil
}

// renderCompanyManager tells a player they were made a company's manager.
func renderCompanyManager(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeCompany(env, "manager_appointed")
	if err != nil {
		return nil, err
	}
	if ev.PlayerID == "" {
		return nil, apperrors.InvalidInput("company.manager_appointed names no manager")
	}
	view := screens.CompanyEmployeeNoticeView{Kind: screens.CompanyEmployeeManager, Company: ev.ref(),
		Owner: screens.GovPlayer{Name: ev.OwnerName, Code: ev.OwnerCode}}
	return &Draft{PlayerID: ev.PlayerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.CompanyEmployeeNotice(c, view)
	}}, nil
}

// renderCompanyPeriod is an owner's report of a settled period.
func renderCompanyPeriod(_ context.Context, _ Deps, env *envelope.Envelope) (*Draft, error) {
	ev, err := decodeCompany(env, "period_settled")
	if err != nil {
		return nil, err
	}
	if ev.OwnerID == "" {
		return nil, apperrors.InvalidInput("company.period_settled names no owner")
	}
	view := screens.CompanyPeriodNoticeView{Company: ev.ref(), Arrears: ev.Arrears, Grace: ev.Grace, Dissolved: ev.Dissolved,
		Period: screens.CompanyPeriodSummary{Revenue: ev.Revenue, SalesTax: ev.SalesTax, Wages: ev.Wages,
			Upkeep: ev.UpkeepDue, UpkeepPaid: ev.UpkeepPaid, Debt: ev.Debt, Shifts: ev.Shifts, QualityBPS: ev.QualityBPS,
			Sold: ev.Sold, Wanted: ev.Wanted, Capacity: ev.Capacity, Balance: ev.Balance,
			CitizenWorkers: ev.CitizenWorkers, CitizenShifts: ev.CitizenShifts, CitizenWages: ev.CitizenWages}}
	return &Draft{PlayerID: ev.OwnerID, Screen: func(c screens.Context) *presenter.Response {
		return screens.CompanyPeriodNotice(c, view)
	}}, nil
}

// companyFoundedAnnouncement: a company was founded in a city.
func companyFoundedAnnouncement(ctx context.Context, deps Deps, env *envelope.Envelope) (*Announcement, error) {
	ev, err := decodeCompany(env, "founded")
	if err != nil || ev.CityID == "" {
		return nil, err
	}
	city, err := deps.Cities.ByID(ctx, ev.CityID)
	if err != nil {
		return nil, err
	}
	return &Announcement{CityID: ev.CityID, PlayerID: ev.PlayerID, Name: ev.PlayerName,
		Line: func(c screens.Context, name string) string {
			return screens.CompanyFoundedAnnouncement(c, name, ev.ref(), city.Code, city.Name)
		}}, nil
}

// companyClosedAnnouncement: a company in a city closed, or was dissolved.
func companyClosedAnnouncement(ctx context.Context, deps Deps, env *envelope.Envelope) (*Announcement, error) {
	ev, err := decodeCompany(env, "closed")
	if err != nil || ev.CityID == "" {
		return nil, err
	}
	city, err := deps.Cities.ByID(ctx, ev.CityID)
	if err != nil {
		return nil, err
	}
	insolvent := ev.Reason == "insolvent"
	return &Announcement{CityID: ev.CityID, Line: func(c screens.Context, _ string) string {
		return screens.CompanyClosedAnnouncement(c, ev.ref(), city.Code, city.Name, insolvent)
	}}, nil
}
