package panel

import (
	"context"
	"errors"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
)

// companyReads are the reads a company line needs.
type companyReads struct {
	companies *postgres.CompanyRepository
	ledger    *postgres.LedgerRepository
	cities    *postgres.CityRepository
	players   *postgres.PlayerRepository
}

func (p *PG) companyReads() companyReads {
	return companyReads{
		companies: postgres.NewCompanyRepository(p.Pool),
		ledger:    postgres.NewLedgerRepository(p.Pool),
		cities:    postgres.NewCityRepository(p.Pool),
		players:   postgres.NewPlayerRepository(p.Pool, p.Ops.Language),
	}
}

// code names a player by their public code, or by id when unknown.
func (r companyReads) code(ctx context.Context, id string) string {
	if p, err := r.players.GetByID(ctx, id); err == nil {
		return p.PublicCode
	}
	return id
}

func (r companyReads) line(ctx context.Context, c application.Company) (CompanyLine, error) {
	l := CompanyLine{Code: c.Code, Name: c.Name, Kind: c.TypeCode, City: c.CityID, Status: c.Status,
		Owner: r.code(ctx, c.OwnerID), Debt: c.Debt}
	if row, err := r.cities.ByID(ctx, c.CityID); err == nil {
		l.City = row.Code
	}
	acct, err := r.ledger.AccountFor(ctx, application.AccountCompanyTreasury, c.ID)
	if err != nil {
		return l, err
	}
	l.Treasury = acct.Balance.Minor()
	staff, err := r.companies.Staff(ctx, c.ID)
	if err != nil {
		return l, err
	}
	l.Staff = len(staff)
	return l, nil
}

// Seats lists the seats of one place, or of every place when kind is empty.
func (p *PG) Seats(ctx context.Context, kind, code string) ([]Seat, error) {
	admin := postgres.NewGovernanceAdmin(p.Pool)
	var ids []string
	if kind != "" {
		j, err := admin.JurisdictionByCode(ctx, kind, code)
		if err != nil {
			return nil, err
		}
		ids = []string{j.ID}
	}
	seats, err := admin.Seats(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Seat, 0, len(seats))
	for _, s := range seats {
		out = append(out, seatOf(s))
	}
	return out, nil
}

func seatOf(s postgres.SeatView) Seat {
	out := Seat{JurisdictionKind: s.JurisdictionKind, JurisdictionCode: s.JurisdictionCode, Office: s.OfficeCode,
		Seat: s.Seat, TermEndsAt: s.TermEndsAt}
	if !s.Vacant() {
		since := s.Since
		out.Holder, out.AcquiredBy, out.Since = s.HolderLabel, s.AcquiredBy, &since
	}
	return out
}

// Policy reads every lever in force at a place and above it, through the one
// resolver every reader uses.
func (p *PG) Policy(ctx context.Context, kind, code string) ([]PolicyPlace, error) {
	admin := postgres.NewGovernanceAdmin(p.Pool)
	policy := postgres.NewPolicyReader(p.Pool, nil)
	j, err := admin.JurisdictionByCode(ctx, kind, code)
	if err != nil {
		return nil, err
	}
	ancestry, err := admin.Ancestry(ctx, j.ID)
	if err != nil {
		return nil, err
	}
	levers, err := admin.ActiveLevers(ctx)
	if err != nil {
		return nil, err
	}
	var out []PolicyPlace
	for _, place := range ancestry {
		pp := PolicyPlace{Kind: place.Kind, Code: place.Code, Name: place.Name}
		for _, l := range levers {
			if l.Jurisdiction != place.Kind {
				continue
			}
			lv, err := p.lever(ctx, admin, policy, place, l)
			if err != nil {
				return nil, err
			}
			pp.Levers = append(pp.Levers, lv)
		}
		out = append(out, pp)
	}
	return out, nil
}

func (p *PG) lever(ctx context.Context, admin *postgres.GovernanceAdmin, policy application.PolicyReader,
	place application.Jurisdiction, l application.LeverDefinition,
) (Lever, error) {
	lv := Lever{Code: l.Code, Type: l.Type, Min: l.Min, Max: l.Max, HeldBy: l.HeldBy, Decision: l.DecisionRule,
		Cooldown: l.ChangeCooldown.String(), Notice: l.Notice.String(), CityDefault: l.CityDefault, Source: "default"}
	v, err := policy.Get(ctx, place.ID, l.Code)
	if errors.Is(err, application.ErrLeverKindUnsupported) {
		return lv, nil
	}
	if err != nil {
		return lv, err
	}
	lv.Supported, lv.Value, lv.Clamped, lv.Source = true, v.Value, v.Clamped, string(v.Source)
	var ids []string
	if v.InForce != nil {
		ids = append(ids, v.InForce.SetByPlayerID)
	}
	if v.Pending != nil {
		ids = append(ids, v.Pending.SetByPlayerID)
	}
	if v.Acting != nil {
		for _, h := range v.Acting.Holders {
			ids = append(ids, h.HolderPlayerID)
		}
	}
	names, err := admin.PlayerLabels(ctx, ids)
	if err != nil {
		return lv, err
	}
	if v.Source == application.PolicyFromOffice && v.InForce != nil {
		at := v.InForce.EffectiveAt
		lv.SetBy, lv.Since = names[v.InForce.SetByPlayerID], &at
	}
	if v.Pending != nil {
		val, at := v.Pending.Value, v.Pending.EffectiveAt
		lv.Pending, lv.PendingAt = &val, &at
	}
	if v.Acting != nil {
		for _, h := range v.Acting.Holders {
			lv.DecidedBy = append(lv.DecidedBy, names[h.HolderPlayerID])
		}
	}
	return lv, nil
}

// Elections lists elections.
func (p *PG) Elections(ctx context.Context, limit int) ([]postgres.ElectionLine, error) {
	return p.admin().ElectionLines(ctx, limit)
}

// verifyLimit bounds the violations of each kind listed, as the command line.
const verifyLimit = 20

// Verify runs the ledger's invariants.
func (p *PG) Verify(ctx context.Context) (Verification, error) {
	v, err := p.admin().VerifyLedger(ctx, verifyLimit)
	if err != nil {
		return Verification{}, err
	}
	checks := operator.VerifyChecks(v, p.Config)
	return Verification{OK: operator.AllHold(v, checks), Accounts: v.Accounts, Transactions: v.Transactions,
		Entries: v.Entries, MoneySupply: v.MoneySupply, Checks: checks}, nil
}

// Content reads the content in force and compares the files on the server.
// With nothing loaded yet it reports the files alone (version 0).
func (p *PG) Content(ctx context.Context) (ContentStatus, error) {
	var s ContentStatus
	store := postgres.NewContentStore(p.Pool)
	row, err := store.Active(ctx)
	switch {
	case errors.Is(err, postgres.ErrNoActiveVersion):
	case err != nil:
		return s, err
	default:
		pack, err := store.LoadActive(ctx)
		if err != nil {
			return s, err
		}
		s = ContentStatus{Version: pack.Version, VersionID: row.ID, LoadedAt: row.LoadedAt, LoadedBy: row.LoadedBy,
			Reason: row.Notes, Checksum: row.Checksum, Counts: packCounts(pack)}
	}
	if local, err := operator.LoadAndValidate(p.ContentDir); err != nil {
		s.LocalError = err.Error()
	} else {
		s.LocalChecksum, s.Matches, s.Warnings = local.Checksum, local.Checksum == s.Checksum, local.Warnings()
	}
	return s, nil
}

func packCounts(p *content.Pack) map[string]int {
	return map[string]int{"cities": len(p.Cities), "routes": len(p.Routes), "skills": len(p.Skills),
		"levels": len(p.Levels), "jurisdictions": len(p.Jurisdictions), "levers": len(p.Levers),
		"offices": len(p.Offices), "careers": len(p.Careers), "courses": len(p.Courses)}
}

// Flags lists watch flags of a status.
func (p *PG) Flags(ctx context.Context, status string, limit int) ([]postgres.FlagLine, error) {
	return p.admin().FlagLines(ctx, status, limit)
}

// Flag reads one flag.
func (p *PG) Flag(ctx context.Context, no int64) (postgres.FlagLine, error) {
	return p.admin().FlagLine(ctx, no)
}

// Holds lists held payments.
func (p *PG) Holds(ctx context.Context, limit int) ([]postgres.HoldLine, error) {
	return p.admin().HoldLines(ctx, limit)
}

// Audit lists recent audit rows.
func (p *PG) Audit(ctx context.Context, prefix string, limit int) ([]postgres.AuditLine, error) {
	return p.admin().AuditLines(ctx, prefix, limit)
}
