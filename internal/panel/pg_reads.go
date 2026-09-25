package panel

import (
	"context"
	"errors"
	"time"

	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
)

// PG is the Backend over the game's database.
type PG struct {
	Pool *postgres.Pool
	Ops  operator.Ops
	// ContentDir is where `content load` reads the authored files.
	ContentDir string
	// Config gives the daily caps the ledger checks compare against.
	Config *config.Config
}

var _ Backend = (*PG)(nil)

func (p *PG) admin() *postgres.EconomyAdmin { return postgres.NewEconomyAdmin(p.Pool) }

func (p *PG) snapshot(ctx context.Context) (*content.Snapshot, error) {
	pack, err := postgres.NewContentStore(p.Pool).LoadActive(ctx)
	if err != nil {
		return nil, err
	}
	return content.BuildSnapshot(pack.Version, pack)
}

func flows(in []postgres.Flow) []Flow {
	out := make([]Flow, 0, len(in))
	for _, f := range in {
		out = append(out, Flow{Reason: f.Reason, Amount: f.Amount})
	}
	return out
}

// Overview reads the dashboard over the last days.
func (p *PG) Overview(ctx context.Context, days int, now time.Time) (Overview, error) {
	snap, err := p.snapshot(ctx)
	if err != nil {
		return Overview{}, err
	}
	d, err := p.admin().EconomyDashboard(ctx, time.Duration(days)*24*time.Hour, snap, now)
	if err != nil {
		return Overview{}, err
	}
	counts, err := p.admin().PanelCounts(ctx, now)
	if err != nil {
		return Overview{}, err
	}
	return Overview{Days: days, Since: d.Since, Supply: flows(d.Supply), Total: d.Total, Faucets: flows(d.Faucets),
		Drains: flows(d.Drains), PriceIndex: d.PriceIndex, PriorIndex: d.PriorIndex, Counts: counts}, nil
}

// SearchPlayers finds players.
func (p *PG) SearchPlayers(ctx context.Context, query string, limit int) ([]postgres.PlayerHit, error) {
	return p.admin().SearchPlayers(ctx, query, limit)
}

// Player reads one player's card and more.
func (p *PG) Player(ctx context.Context, code string) (PlayerDetail, error) {
	c, err := p.admin().PlayerCard(ctx, code)
	if err != nil {
		return PlayerDetail{}, err
	}
	state := "here"
	switch {
	case c.Jailed:
		state = "jail"
	case c.InHospital:
		state = "hospital"
	case c.Travelling:
		state = "travelling"
	}
	extra, err := p.admin().PlayerExtra(ctx, c.ID, 50)
	if err != nil {
		return PlayerDetail{}, err
	}
	return PlayerDetail{ID: c.ID, Code: c.Code, Name: c.Name, CreatedAt: c.CreatedAt, City: c.City, Place: c.Place,
		Residence: c.Residence, State: state, Job: c.Job, Renting: c.Renting, Balances: flows(c.Balances),
		Companies: c.Companies, Properties: c.Properties, Offices: c.Offices, Achievements: c.Achievements,
		OpenFlags: c.OpenFlags, Extra: extra}, nil
}

// Cities lists the cities.
func (p *PG) Cities(ctx context.Context) ([]postgres.CityLine, error) {
	return p.admin().CityLines(ctx)
}

// City reads one city: its card, the allocation in force, its groups and
// its seats.
func (p *PG) City(ctx context.Context, code string) (CityDetail, error) {
	c, err := p.admin().CityCard(ctx, code)
	if err != nil {
		return CityDetail{}, err
	}
	d := CityDetail{Code: c.Code, Name: c.Name, Country: c.Country, Treasury: c.Treasury, Residents: c.Residents,
		Present: c.Present, Companies: c.Companies, Properties: c.Properties, DamageBPS: c.DamageBPS,
		LastBudget: c.LastBudget, LastBudgetLines: flows(c.LastBudgetLines)}
	if snap, err := p.snapshot(ctx); err == nil && c.JurisdictionID != "" {
		if def, ok := snap.Budget(); ok {
			v, err := postgres.NewPolicyReader(p.Pool, nil).Get(ctx, c.JurisdictionID, def.Lever)
			if err != nil {
				return d, err
			}
			d.Allocation = v.Allocation
		}
	}
	groups, err := postgres.NewCityGroupRepository(p.Pool).ForCity(ctx, c.ID, c.Code)
	if err != nil {
		return d, err
	}
	for _, g := range groups {
		d.Groups = append(d.Groups, Group{ChatID: g.ChatID, Language: g.Language, LinkedBy: g.LinkedBy, LinkedAt: g.LinkedAt})
	}
	d.Seats, err = p.Seats(ctx, "city", c.Code)
	return d, err
}

// Bots lists the bots a group may be served by.
func (p *PG) Bots(ctx context.Context) ([]string, error) { return p.admin().BotKeys(ctx) }

// Companies lists companies, newest first.
func (p *PG) Companies(ctx context.Context, limit int) ([]CompanyLine, error) {
	r := p.companyReads()
	list, err := r.companies.All(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]CompanyLine, 0, len(list))
	for _, c := range list {
		line, err := r.line(ctx, c)
		if err != nil {
			return nil, err
		}
		out = append(out, line)
	}
	return out, nil
}

// Company reads one company in full.
func (p *PG) Company(ctx context.Context, code string) (CompanyDetail, error) {
	r := p.companyReads()
	c, err := r.companies.ByCode(ctx, playercode.Normalize(code))
	if err != nil {
		return CompanyDetail{}, errors.Join(postgres.ErrNotFound, err)
	}
	line, err := r.line(ctx, *c)
	if err != nil {
		return CompanyDetail{}, err
	}
	d := CompanyDetail{CompanyLine: line, FoundedAt: c.FoundedAt, ClosedAt: c.ClosedAt, CloseReason: c.CloseReason,
		PriceBPS: int64(c.PriceBPS), RatingBPS: int64(c.RatingBPS), Arrears: int64(c.Arrears), TotalShares: c.TotalShares}
	if c.ManagerID != "" {
		d.Manager = r.code(ctx, c.ManagerID)
	}
	if d.Reserved, err = r.companies.Reserved(ctx, c.ID); err != nil {
		return d, err
	}
	holders, err := r.companies.Shareholders(ctx, c.ID)
	if err != nil {
		return d, err
	}
	for _, h := range holders {
		d.Shares = append(d.Shares, Holding{Player: r.code(ctx, h.PlayerID), Shares: h.Shares})
	}
	staff, err := r.companies.Staff(ctx, c.ID)
	if err != nil {
		return d, err
	}
	for _, e := range staff {
		d.StaffList = append(d.StaffList, Employee{Player: r.code(ctx, e.PlayerID), Career: e.CareerCode, Tier: e.Tier,
			Wage: e.Rate, Shifts: e.TotalShifts, Working: e.Working})
	}
	periods, err := r.companies.Periods(ctx, c.ID, 10)
	if err != nil {
		return d, err
	}
	for _, pr := range periods {
		d.Periods = append(d.Periods, Period{No: pr.PeriodNo, Revenue: pr.Revenue, Wages: pr.Wages, Upkeep: pr.UpkeepPaid,
			Balance: pr.BalanceAfter, Insolvent: pr.Insolvent})
	}
	d.Licences, err = p.admin().CompanyLicences(ctx, c.Code)
	return d, err
}
