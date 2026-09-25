package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/domain/military"
	"github.com/mrjvadi/torncity/internal/domain/technology"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Defence licences on the company side (docs/adr/0022-military-and-diplomacy.md
// section 2.14): who may found a company of the defence sector, the licence
// its founding records, a civilian company's application for a contractor
// licence, and the licence as the management screen shows it. The minister's
// side is military_licences.go.

// floorHelper is the production handler the company screens borrow for a
// company's next step: the same rules, the same clock.
func (h *CompaniesHandler) floorHelper() *ProductionHandler {
	return &ProductionHandler{uow: h.uow, ids: h.ids, msgs: h.msgs, content: h.content, cities: h.cities, policy: h.policy,
		scale: h.scale, rules: ProductionRules{MaxRunningOrders: max(h.rules.MaxRunningOrders, 1),
			QuickUnits: h.rules.QuickUnits, Citizens: h.rules.Citizens, Limits: h.rules.Limits},
		idempotencyTTL: h.idempotencyTTL, now: h.now}
}

// techStanding is a company's standing in technology: how many it owns, and
// the deepest tier among them.
func techStanding(ctx context.Context, tx application.Tx, snap *content.Snapshot, companyID string) (int, int, error) {
	owned, err := tx.Production().Technologies(ctx, companyID)
	if err != nil {
		return 0, 0, err
	}
	codes := make([]string, 0, len(owned))
	for _, t := range owned {
		codes = append(codes, t.Tech)
	}
	return len(codes), technology.TopTier(snap.TechTiers(), codes), nil
}

// licenceEntry is a licence for a screen, its status settled as of now.
func licenceEntry(snap *content.Snapshot, l application.DefenceLicence, c application.Company, now time.Time) screens.LicenceEntry {
	e := screens.LicenceEntry{No: l.No, Company: companyRef(snap, c), Kind: l.Kind, Basis: l.Basis,
		Status: string(licenceRule(l).Settled(now))}
	if l.EffectiveAt != nil {
		e.EffectiveAt = *l.EffectiveAt
	}
	return e
}

// defenceBadge is where a company stands on a defence licence, nil when the
// content has none or licences do not concern it.
func (h *CompaniesHandler) defenceBadge(ctx context.Context, tx application.Tx, snap *content.Snapshot, c application.Company,
) (*screens.DefenceBadge, error) {
	d, ok := snap.DefenceLicence()
	if !ok {
		return nil, nil
	}
	def, _, err := companyType(snap, c)
	if err != nil {
		return nil, err
	}
	l, err := companyLicence(ctx, tx, c.ID, false)
	if err != nil {
		return nil, err
	}
	now := h.now()
	b := &screens.DefenceBadge{Contractor: def.SectorCode() != d.Sector}
	if l != nil {
		b.Status = string(licenceRule(*l).Settled(now))
		if l.EffectiveAt != nil {
			b.EffectiveAt = *l.EffectiveAt
		}
	}
	if b.Contractor && (l == nil || !licenceRule(*l).Open(now)) {
		owned, tier, err := techStanding(ctx, tx, snap, c.ID)
		if err != nil {
			return nil, err
		}
		b.Eligible = d.Contractor.Rule().Eligible(owned, tier) == nil
	}
	if b.Status == "" && !b.Eligible {
		return nil, nil
	}
	return b, nil
}

// founderStanding is what founding a defence company needs to know of a
// player: their post in the armed forces, and whether a company of theirs
// holds a contractor licence in force.
func founderStanding(ctx context.Context, tx application.Tx, d content.DefenceLicenceDef, p *application.Player,
	now time.Time,
) (military.Founder, error) {
	var f military.Founder
	emp, err := tx.Employment().Current(ctx, p.ID)
	switch {
	case err == nil:
		if emp.CareerCode == d.Career {
			f.Serving, f.RankTier = true, emp.Tier
		}
	case !isSentinel(err, application.ErrNotEmployed):
		return f, err
	}
	companies, err := tx.Companies().Of(ctx, p.ID)
	if err != nil {
		return f, err
	}
	for _, c := range companies {
		if c.OwnerID != p.ID || !c.Active() {
			continue
		}
		l, err := companyLicence(ctx, tx, c.ID, false)
		if err != nil {
			return f, err
		}
		if l != nil && l.Kind == application.LicenceContractor && licenceRule(*l).InForce(now) {
			f.Contractor = true
		}
	}
	return f, nil
}

// defenceGate works out whether p may found a company of kind def: the
// basis a licence would rest on, or the block.
func (h *CompaniesHandler) defenceGate(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.CompanyTypeDef,
	p *application.Player, f *foundable,
) error {
	if !snap.LicensedSector(def) {
		return nil
	}
	d, _ := snap.DefenceLicence()
	who, err := founderStanding(ctx, tx, d, p, h.now())
	if err != nil {
		return err
	}
	basis, err := military.MayFound(who, snap.DefenceRankTier())
	if err != nil {
		if f.blocked == "" {
			f.blocked = screens.CompanyBlockedDefence
		}
		if career, ok := snap.CareerDef(d.Career); ok {
			f.rank = jobRef(career, snap.DefenceRankTier())
		}
		return nil
	}
	f.basis = basis
	return nil
}

// recordFoundingLicence records the licence a defence company is founded
// on, in the founding's transaction.
func (h *CompaniesHandler) recordFoundingLicence(ctx context.Context, tx application.Tx, f foundable, c application.Company,
	p *application.Player, now time.Time,
) error {
	if f.basis == "" {
		return nil
	}
	_, err := tx.Military().CreateLicence(ctx, application.DefenceLicence{ID: h.ids.NewID(), CompanyID: c.ID,
		Kind: application.LicenceManufacturer, Basis: string(f.basis), Status: string(military.LicenceActive),
		AppliedBy: p.ID, AppliedAt: now, UpdatedAt: now})
	return err
}

// Defence handles company.defence: a company's defence licence — its
// status, and for a civilian company its standing in technology against
// what a contractor licence asks — and, confirmed by the owner, the
// application for one, which goes to the defence minister.
func (h *CompaniesHandler) Defence(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	apply := strings.TrimSpace(req.Confirm) == screens.ProductionConfirm
	var view screens.CompanyDefenceView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		fresh := false
		if apply {
			if fresh, err = h.reserve(ctx, tx, p.ID, meta); err != nil {
				return err
			}
		}
		c, role, err := h.managed(ctx, tx, snap, p, req.code(), company.RightViewBooks)
		if err != nil {
			return err
		}
		d, ok := snap.DefenceLicence()
		if !ok {
			return refuseCompany(screens.CompanyRefusedNotFound, c, snap)
		}
		def, _, err := companyType(snap, *c)
		if err != nil {
			return err
		}
		now := h.now()
		l, err := companyLicence(ctx, tx, c.ID, apply)
		if err != nil {
			return err
		}
		view = screens.CompanyDefenceView{Ref: companyRef(snap, *c), Manufacturer: def.SectorCode() == d.Sector,
			MinTechs: d.Contractor.MinTechnologies, MinTier: d.Contractor.MinTier}
		if view.Owned, view.Tier, err = techStanding(ctx, tx, snap, c.ID); err != nil {
			return err
		}
		open := l != nil && licenceRule(*l).Open(now)
		eligible := !view.Manufacturer && !open && d.Contractor.Rule().Eligible(view.Owned, view.Tier) == nil
		view.CanApply = eligible && role == company.RoleOwner
		if apply && fresh && view.CanApply {
			if l != nil && l.Status == string(military.LicenceRevoking) {
				// A revocation past its notice lapses before a new
				// application takes its place.
				l.Status, l.UpdatedAt = string(military.LicenceRevoked), now
				if err := tx.Military().SaveLicence(ctx, *l); err != nil {
					return err
				}
			}
			created, err := tx.Military().CreateLicence(ctx, application.DefenceLicence{ID: h.ids.NewID(), CompanyID: c.ID,
				Kind: application.LicenceContractor, Basis: string(military.BasisMinister),
				Status: string(military.LicencePending), AppliedBy: p.ID, AppliedAt: now, UpdatedAt: now})
			if err != nil && !isSentinel(err, application.ErrDefenceLicenceOpen) {
				return err
			}
			if err == nil {
				l, view.Applied, view.CanApply = &created, true, false
				nobody, err := h.tellMinister(ctx, tx, meta, snap, *c, created)
				if err != nil {
					return err
				}
				view.NoMinister = nobody
			}
		}
		if l != nil {
			e := licenceEntry(snap, *l, *c, now)
			view.Licence = &e
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.CompanyDefence(h.screen(meta, lang), view), nil
}

// tellMinister tells whoever decides for the defence minister of the
// company's country that an application waits; nobody reports that the
// seat is empty and nobody acts for it — the application waits for one.
func (h *CompaniesHandler) tellMinister(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	c application.Company, l application.DefenceLicence,
) (bool, error) {
	countryID, err := tx.Diplomacy().CountryOfCity(ctx, c.CityID)
	if err != nil || countryID == "" {
		return true, err
	}
	office := actionOffice(snap, content.ActionDefenceLicence)
	if office == "" {
		return true, nil
	}
	chain, err := tx.Governance().ActingChain(ctx, office, countryID)
	if err != nil {
		return false, err
	}
	acting := application.ActingForChain(chain)
	if acting == nil || len(acting.Holders) == 0 {
		return true, nil
	}
	country, err := tx.Governance().Jurisdiction(ctx, countryID)
	if err != nil {
		return false, err
	}
	for _, s := range acting.Holders {
		if err := appendDomainEvent(ctx, tx, meta, "military", "licence_applied", l.ID, map[string]any{
			"player_id": s.HolderPlayerID, "company_code": c.Code, "company_name": c.Name, "type": c.TypeCode,
			"country_code": country.Code, "country_name": country.Name, "no": l.No,
		}); err != nil {
			return false, err
		}
	}
	return false, nil
}
