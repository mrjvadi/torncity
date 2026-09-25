package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/domain/diplomacy"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/technology"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The research lab: researching a technology (paid when it starts, owned
// once when its scheduled action runs), choosing how an owned technology is
// shared — private, licensed at a price, or published for good — and buying
// a license from another company.

// researchReference is ledger and journal reference_type of a research.
const researchReference = "company_research"

// ProductionActionPayload is the jsonb a production economy action carries:
// the row it finishes.
type ProductionActionPayload struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
}

// schedule puts a production economy action on the game clock.
func (h *ProductionHandler) schedule(ctx context.Context, tx application.Tx, actionType, refType, refID, companyID string,
	now, finish time.Time,
) (string, error) {
	payload, err := json.Marshal(ProductionActionPayload{ID: refID, CompanyID: companyID})
	if err != nil {
		return "", err
	}
	id := h.ids.NewID()
	return id, tx.GameActions().Schedule(ctx, application.GameAction{
		ID: id, ActionType: actionType, ActorType: "system", ReferenceType: refType, ReferenceID: refID,
		Payload: payload, StartedAt: now, FinishAt: finish,
	})
}

// techState is where a company stands on a technology.
func techState(f *floor, running *application.Research, code string) string {
	switch {
	case f.ownSet.Has(code):
		return screens.TechOwned
	case running != nil && running.Tech == code:
		return screens.TechRunning
	case f.pubSet.Has(code):
		return screens.TechPublic
	case f.access.Licensed.Has(code):
		return screens.TechLicense
	}
	return screens.TechAvailable
}

// researchStanding is the rules' view of a company for research.
func (h *ProductionHandler) researchStanding(ctx context.Context, tx application.Tx, snap *content.Snapshot, f *floor,
	running *application.Research,
) (technology.Standing, error) {
	buyer, err := f.buyer(ctx, tx, snap, h.now())
	if err != nil {
		return technology.Standing{}, err
	}
	levels := map[string]int{}
	var firstErr error
	s := technology.Standing{CompanyType: f.c.TypeCode, Owned: f.ownSet, Published: f.pubSet, Researching: running != nil,
		Buyer: buyer,
		SkillLevel: func(skill string) int {
			if l, ok := levels[skill]; ok {
				return l
			}
			l, _, err := f.best(ctx, tx, skill)
			if err != nil && firstErr == nil {
				firstErr = err
			}
			levels[skill] = l
			return l
		}}
	return s, firstErr
}

// blockedKind maps a research refusal of the rules to a screen's kind.
func blockedKind(err error) string {
	switch {
	case err == nil:
		return ""
	case stderrors.Is(err, technology.ErrBusy):
		return screens.ProductionRefusedBusy
	case stderrors.Is(err, technology.ErrWrongCompanyType):
		return screens.ProductionRefusedWrongType
	case stderrors.Is(err, technology.ErrSkillTooLow):
		return screens.ProductionRefusedSkill
	case stderrors.Is(err, technology.ErrAlreadyOwned):
		return screens.ProductionRefusedOwned
	case stderrors.Is(err, technology.ErrNotCleared):
		return screens.ProductionRefusedNotCleared
	}
	return screens.ProductionRefusedPrerequisite
}

// Lab handles company.lab: the technology tree as the company stands on it,
// or one technology when the request names one.
func (h *ProductionHandler) Lab(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if strings.TrimSpace(req.Tech) != "" {
		return h.techWith(ctx, meta, req, nil, false, nil)
	}
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.LabView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, req.code(), company.RightResearch)
		if err != nil {
			return err
		}
		f, err := readFloor(ctx, tx, snap, c)
		if err != nil {
			return err
		}
		running, err := tx.Production().RunningResearch(ctx, c.ID)
		if err != nil {
			return err
		}
		b, _, err := f.books(ctx, tx)
		if err != nil {
			return err
		}
		view = screens.LabView{Ref: companyRef(snap, *c), Available: b.Available().Minor()}
		now := h.now()
		if running != nil {
			def, _ := snap.Technology(running.Tech)
			view.Running = &screens.ResearchLine{Tech: named(def.Code, def.Name), FinishAt: running.FinishAt,
				Left: countdownTo(running.FinishAt, now)}
		}
		owned := map[string]application.CompanyTech{}
		for _, t := range f.owned {
			owned[t.Tech] = t
		}
		stg, err := h.stageOf(ctx, tx, snap, f)
		if err != nil {
			return err
		}
		st := stg.standing
		needed := techsNeeded(snap, c.TypeCode)
		// Staged, basic first: what the company may use or research now,
		// and what is one step beyond; the rest is counted, not shown
		// (docs/adr/0021, section 14).
		var codes []string
		for _, def := range snap.Technologies() {
			codes = append(codes, def.Code)
		}
		for _, code := range byTier(snap, codes) {
			def, _ := snap.Technology(code)
			if !hasCode(def.CompanyTypes, c.TypeCode) && !f.ownSet.Has(def.Code) && !needed[def.Code] {
				continue
			}
			usable := f.access.Allows(def.Code) || (running != nil && running.Tech == def.Code)
			if !stg.labShown(def.Tech(), usable) {
				if ctl := def.Tech().Control; !ctl.Restricted || technology.Cleared(ctl, st.Buyer) == nil {
					view.Hidden++
				}
				continue
			}
			if err := *stg.err; err != nil {
				return err
			}
			line := screens.TechLine{Tech: named(def.Code, def.Name), State: techState(f, running, def.Code), Cost: def.Cost}
			if t, ok := owned[def.Code]; ok {
				line.Mode, line.Price = t.Mode, t.LicensePrice
			}
			if line.State == screens.TechAvailable {
				for _, m := range st.Missing(def.Tech()) {
					md, _ := snap.Technology(m)
					line.Missing = append(line.Missing, named(md.Code, md.Name))
				}
				if len(line.Missing) > 0 {
					line.State = screens.TechLocked
				}
			}
			if line.State == screens.TechAvailable || line.State == screens.TechLocked {
				offers, err := tx.Production().Offers(ctx, def.Code)
				if err != nil {
					return err
				}
				for _, o := range offers {
					if o.Company.ID != c.ID && o.Tech.Mode == technology.Licensed.String() {
						line.Offers++
					}
				}
			}
			view.Techs = append(view.Techs, line)
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Lab(h.screen(meta, lang), view), nil
}

// countdownTo is how long until at, never below a second.
func countdownTo(at, now time.Time) time.Duration {
	if d := at.Sub(now); d > time.Second {
		return d
	}
	return time.Second
}

// techView builds one technology's screen for a company.
func (h *ProductionHandler) techView(ctx context.Context, tx application.Tx, snap *content.Snapshot, c *application.Company,
	code string,
) (screens.TechView, *floor, error) {
	def, ok := snap.Technology(strings.TrimSpace(code))
	if !ok {
		return screens.TechView{}, nil, refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(screens.AddrLab, c.Code)
	}
	f, err := readFloor(ctx, tx, snap, c)
	if err != nil {
		return screens.TechView{}, nil, err
	}
	running, err := tx.Production().RunningResearch(ctx, c.ID)
	if err != nil {
		return screens.TechView{}, nil, err
	}
	b, _, err := f.books(ctx, tx)
	if err != nil {
		return screens.TechView{}, nil, err
	}
	v := screens.TechView{Ref: companyRef(snap, *c), Tech: named(def.Code, def.Name), State: techState(f, running, def.Code),
		Cost: def.Cost, Time: h.scale.RealWait(def.ResearchTime()), Skill: def.Skill, Level: def.Level,
		Available: b.Available().Minor()}
	if def.Skill != "" {
		if v.Best, _, err = f.best(ctx, tx, def.Skill); err != nil {
			return v, f, err
		}
	}
	for _, r := range def.Requires {
		rd, _ := snap.Technology(r)
		v.Requires = append(v.Requires, screens.TechRequirement{Tech: named(rd.Code, rd.Name),
			Met: f.ownSet.Has(r) || f.pubSet.Has(r)})
	}
	for _, comp := range snap.ComponentDefs() {
		if hasCode(comp.RequiresTechnology, def.Code) {
			v.Unlocks = append(v.Unlocks, named(comp.Code, comp.Name))
		}
	}
	if running != nil && running.Tech == def.Code {
		v.Running = &screens.ResearchLine{Tech: v.Tech, FinishAt: running.FinishAt, Left: countdownTo(running.FinishAt, h.now())}
	}
	switch v.State {
	case screens.TechOwned:
		t, err := tx.Production().Technology(ctx, c.ID, def.Code)
		if err != nil {
			return v, f, err
		}
		v.Mode, v.Price = t.Mode, t.LicensePrice
		if v.Sold, err = tx.Production().LicensesSold(ctx, c.ID, def.Code); err != nil {
			return v, f, err
		}
	case screens.TechAvailable:
		st, err := h.researchStanding(ctx, tx, snap, f, running)
		if err != nil {
			return v, f, err
		}
		cerr := technology.CanResearch(def.Tech(), st)
		v.Blocked = blockedKind(cerr)
		if stderrors.Is(cerr, technology.ErrPrerequisiteMissing) {
			v.State = screens.TechLocked
		}
		if v.Blocked == screens.ProductionRefusedSkill {
			v.Gap = skillGapOf(snap, c.Code, def.Skill, def.Level)
		}
		if v.Blocked == "" && b.Available().Minor() < def.Cost {
			v.Blocked = screens.ProductionRefusedFunds
		}
	}
	if v.State != screens.TechOwned {
		offers, err := tx.Production().Offers(ctx, def.Code)
		if err != nil {
			return v, f, err
		}
		for _, o := range offers {
			if o.Company.ID == c.ID {
				continue
			}
			price := int64(0)
			if o.Tech.Mode == technology.Licensed.String() {
				price = o.Tech.LicensePrice
			}
			v.Offers = append(v.Offers, screens.TechOffer{Company: companyRef(snap, o.Company), Price: price})
		}
	}
	return v, f, nil
}

// techWith renders one technology after an optional change.
func (h *ProductionHandler) techWith(ctx context.Context, meta envelope.Metadata, req ProductionRequest, notice *screens.TechNotice,
	confirmPublish bool, confirmLicense *screens.TechOffer,
) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.TechView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, req.code(), company.RightResearch)
		if err != nil {
			return err
		}
		view, _, err = h.techView(ctx, tx, snap, c, req.Tech)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	view.Notice, view.ConfirmPublish, view.ConfirmLicense = notice, confirmPublish, confirmLicense
	return screens.Tech(h.screen(meta, lang), view), nil
}

// Research handles company.research: starting to research a technology. Its
// cost leaves the company's free money (research, a drain); the technology
// is the company's once, when its scheduled action runs.
func (h *ProductionHandler) Research(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var notice *screens.TechNotice
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, req.code(), company.RightResearch)
		if err != nil {
			return err
		}
		def, ok := snap.Technology(strings.TrimSpace(req.Tech))
		if !ok {
			return refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(screens.AddrLab, c.Code)
		}
		f, err := readFloor(ctx, tx, snap, c)
		if err != nil {
			return err
		}
		running, err := tx.Production().RunningResearch(ctx, c.ID)
		if err != nil {
			return err
		}
		st, err := h.researchStanding(ctx, tx, snap, f, running)
		if err != nil {
			return err
		}
		if cerr := technology.CanResearch(def.Tech(), st); cerr != nil {
			r := refuseProduction(blockedKind(cerr), c, snap).back(screens.AddrLab, c.Code, def.Code)
			r.view.Skill, r.view.Level = def.Skill, def.Level
			for _, m := range st.Missing(def.Tech()) {
				md, _ := snap.Technology(m)
				r.view.Techs = append(r.view.Techs, named(md.Code, md.Name))
			}
			if running != nil {
				rd, _ := snap.Technology(running.Tech)
				r.view.Techs = []screens.Named{named(rd.Code, rd.Name)}
			}
			return r
		}
		now := h.now()
		id := h.ids.NewID()
		txID, err := h.spend(ctx, tx, snap, f, application.ReasonResearch, application.SystemSinkAccountID,
			money.FromMinor(def.Cost), now)
		if err != nil {
			var r *productionRefusal
			if stderrors.As(err, &r) {
				r.back(screens.AddrLab, c.Code, def.Code)
			}
			return err
		}
		finish := now.Add(h.scale.RealWait(def.ResearchTime()))
		actionID, err := h.schedule(ctx, tx, application.ResearchActionType, researchReference, id, c.ID, now, finish)
		if err != nil {
			return err
		}
		if err := tx.Production().StartResearch(ctx, application.Research{ID: id, CompanyID: c.ID, Tech: def.Code,
			Cost: def.Cost, LedgerTransactionID: txID, GameActionID: actionID, StartedBy: p.ID, StartedAt: now,
			FinishAt: finish}); err != nil {
			switch {
			case isSentinel(err, application.ErrResearchBusy):
				return refuseProduction(screens.ProductionRefusedBusy, c, snap).back(screens.AddrLab, c.Code)
			case isSentinel(err, application.ErrAlreadyResearched):
				return refuseProduction(screens.ProductionRefusedOwned, c, snap).back(screens.AddrLab, c.Code)
			}
			return err
		}
		notice = &screens.TechNotice{Kind: screens.TechNoticeStarted}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.techWith(ctx, meta, req, notice, false, nil)
}

// Researched handles company.researched from the SCHEDULER: a research
// finishing. Exactly once: the research row, locked, must still be running
// under the action that finishes it; the technology row's key is the
// backstop.
func (h *ProductionHandler) Researched(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	in, err := productionPayload(meta, req)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		rs, err := tx.Production().Research(ctx, in.ID)
		if isSentinel(err, application.ErrResearchNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if rs.Status != application.ResearchRunning || (req.ActionID != "" && rs.GameActionID != req.ActionID) {
			return nil
		}
		now := h.now()
		if now.Before(rs.FinishAt) {
			return errors.Internal(stderrors.New("handlers: a research finished before its time"))
		}
		c, err := tx.Companies().Lock(ctx, rs.CompanyID)
		if err != nil {
			return err
		}
		if err := tx.Production().FinishResearch(ctx, rs.ID, now); err != nil {
			return err
		}
		if !c.Active() {
			// A company dissolved while researching owns nothing.
			return nil
		}
		if err := tx.Production().AddTechnology(ctx, application.CompanyTech{CompanyID: c.ID, Tech: rs.Tech,
			Mode: string(technology.Private), ResearchID: rs.ID, AcquiredAt: now}); err != nil {
			return err
		}
		def, _ := snap.Technology(rs.Tech)
		return appendCompanyEvent(ctx, tx, meta, "researched", c.ID, map[string]any{
			"company_id": c.ID, "code": c.Code, "name": c.Name, "type": c.TypeCode, "owner_id": c.OwnerID,
			"city_id": c.CityID, "tech": rs.Tech, "tech_name": def.Name,
		})
	})
}

// productionPayload reads a scheduled action's payload.
func productionPayload(meta envelope.Metadata, req CrimeScheduledRequest) (ProductionActionPayload, error) {
	if err := meta.Validate(); err != nil {
		return ProductionActionPayload{}, errors.InvalidInput("malformed request context").WithCause(err)
	}
	var in ProductionActionPayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return in, errors.InvalidInput("production payload is unreadable").WithCause(err)
		}
	}
	if in.ID == "" {
		in.ID = req.ReferenceID
	}
	if in.ID == "" {
		return in, errors.InvalidInput("production action names no row")
	}
	return in, nil
}

// TechMode handles company.techmode: how an owned technology is shared.
// Publishing is confirmed first, since it is for good.
func (h *ProductionHandler) TechMode(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	mode := technology.Mode(strings.TrimSpace(req.Mode))
	if mode == technology.Published && !req.confirmed() {
		return h.techWith(ctx, meta, req, nil, true, nil)
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		notice    *screens.TechNotice
		published bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil || !fresh {
			return err
		}
		c, err := h.managed(ctx, tx, snap, p, req.code(), company.RightResearch)
		if err != nil {
			return err
		}
		back := []string{screens.AddrLab, c.Code, strings.TrimSpace(req.Tech)}
		t, err := tx.Production().Technology(ctx, c.ID, strings.TrimSpace(req.Tech))
		if isSentinel(err, application.ErrTechnologyNotOwned) {
			return refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(back...)
		}
		if err != nil {
			return err
		}
		var price int64
		if mode == technology.Licensed {
			var ok bool
			if price, ok = quantityArg(req.Price); !ok {
				return refuseProduction(screens.ProductionRefusedAmount, c, snap).back(back...)
			}
		}
		if err := technology.ChangeMode(technology.Mode(t.Mode), mode, price, h.rules.Limits.Max.Minor()); err != nil {
			switch {
			case stderrors.Is(err, technology.ErrPublishedForever):
				return refuseProduction(screens.ProductionRefusedPublished, c, snap).back(back...)
			case stderrors.Is(err, technology.ErrInvalidPrice):
				return refuseProduction(screens.ProductionRefusedAmount, c, snap).back(back...)
			}
			return errors.InvalidInput("unknown sharing mode").WithCause(err)
		}
		now := h.now()
		published = mode == technology.Published && t.Mode != string(technology.Published)
		t.Mode, t.LicensePrice, t.UpdatedAt = string(mode), price, now
		if published {
			t.PublishedAt = &now
		}
		if err := tx.Production().SaveTechnology(ctx, *t); err != nil {
			return err
		}
		notice = &screens.TechNotice{Kind: string(mode), Price: price}
		if !published {
			return nil
		}
		def, _ := snap.Technology(t.Tech)
		return appendCompanyEvent(ctx, tx, meta, "tech_published", c.ID, map[string]any{
			"company_id": c.ID, "code": c.Code, "name": c.Name, "type": c.TypeCode, "city_id": c.CityID,
			"tech": t.Tech, "tech_name": def.Name,
		})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.techWith(ctx, meta, req, notice, false, nil)
}

// License handles company.license: buying a license for a technology from
// the company that owns it, at its price, once. The money moves from the
// buyer's free money to the owner's treasury (technology_license); export
// control is checked first.
func (h *ProductionHandler) License(ctx context.Context, meta envelope.Metadata, req ProductionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		notice  *screens.TechNotice
		confirm *screens.TechOffer
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		if req.confirmed() {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil || !fresh {
				return err
			}
		}
		c, err := h.managed(ctx, tx, snap, p, req.code(), company.RightResearch)
		if err != nil {
			return err
		}
		def, ok := snap.Technology(strings.TrimSpace(req.Tech))
		if !ok {
			return refuseProduction(screens.ProductionRefusedNotFound, c, snap).back(screens.AddrLab, c.Code)
		}
		back := []string{screens.AddrLab, c.Code, def.Code}
		f, err := readFloor(ctx, tx, snap, c)
		if err != nil {
			return err
		}
		offers, err := tx.Production().Offers(ctx, def.Code)
		if err != nil {
			return err
		}
		var offer *application.TechOffer
		for i := range offers {
			if offers[i].Company.Code == strings.ToUpper(strings.TrimSpace(req.From)) {
				offer = &offers[i]
			}
		}
		if offer == nil {
			return refuseProduction(screens.ProductionRefusedNotForSale, c, snap).back(back...)
		}
		lerr := technology.CheckLicense(c.ID, technology.Offer{OwnerID: offer.Company.ID,
			Mode: technology.Mode(offer.Tech.Mode), Price: offer.Tech.LicensePrice}, f.access, def.Code)
		switch {
		case stderrors.Is(lerr, technology.ErrNoNeed):
			return refuseProduction(screens.ProductionRefusedLicensed, c, snap).back(back...)
		case lerr != nil:
			return refuseProduction(screens.ProductionRefusedNotForSale, c, snap).back(back...)
		}
		// Export control: the one place a license sale is cleared.
		buyer, err := f.buyer(ctx, tx, snap, h.now())
		if err != nil {
			return err
		}
		if err := technology.Cleared(def.Tech().Control, buyer); err != nil {
			return refuseProduction(screens.ProductionRefusedNotCleared, c, snap).back(back...)
		}
		// Across a border (docs/adr/0022): a technology ban between the two
		// companies' countries — the one sanctions check — and, for a
		// controlled technology, the licensor's arms export policy.
		licensee, err := tx.Diplomacy().CountryOfCity(ctx, c.CityID)
		if err != nil {
			return err
		}
		licensorCountry, err := tx.Diplomacy().CountryOfCity(ctx, offer.Company.CityID)
		if err != nil {
			return err
		}
		if err := checkSanctions(ctx, tx, application.CheckSanctions(ctx, tx, diplomacy.Technology, licensee,
			licensorCountry, h.now()), back...); err != nil {
			return err
		}
		if def.Tech().Control.Restricted {
			denied, err := armsExportDenied(ctx, tx, h.policy, snap, licensee, licensorCountry, h.now())
			if err != nil {
				return err
			}
			if denied {
				return refuseProduction(screens.ProductionRefusedNotCleared, c, snap).back(back...)
			}
		}
		if !req.confirmed() {
			confirm = &screens.TechOffer{Company: companyRef(snap, offer.Company), Price: offer.Tech.LicensePrice}
			return nil
		}
		now := h.now()
		licensor, err := tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, offer.Company.ID)
		if err != nil {
			return err
		}
		txID, err := h.spend(ctx, tx, snap, f, application.ReasonTechnologyLicense, licensor.ID,
			money.FromMinor(offer.Tech.LicensePrice), now)
		if err != nil {
			var r *productionRefusal
			if stderrors.As(err, &r) {
				r.back(back...)
			}
			return err
		}
		if err := tx.Production().GrantLicense(ctx, application.License{ID: h.ids.NewID(), Tech: def.Code,
			LicensorID: offer.Company.ID, LicenseeID: c.ID, Price: offer.Tech.LicensePrice, LedgerTransactionID: txID,
			BoughtBy: p.ID, GrantedAt: now}); err != nil {
			if isSentinel(err, application.ErrAlreadyLicensed) {
				return refuseProduction(screens.ProductionRefusedLicensed, c, snap).back(back...)
			}
			return err
		}
		notice = &screens.TechNotice{Kind: screens.TechNoticeBought, Price: offer.Tech.LicensePrice,
			Company: companyRef(snap, offer.Company)}
		return appendCompanyEvent(ctx, tx, meta, "license_sold", offer.Company.ID, map[string]any{
			"company_id": offer.Company.ID, "code": offer.Company.Code, "name": offer.Company.Name,
			"type": offer.Company.TypeCode, "owner_id": offer.Company.OwnerID, "tech": def.Code, "tech_name": def.Name,
			"buyer": c.Name, "price": offer.Tech.LicensePrice,
		})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.techWith(ctx, meta, req, notice, false, confirm)
}

// techAccessOf is the access a floor gives, for a design.
func techAccessOf(f *floor) item.TechAccess { return f.access }

// techsNeeded are the technologies a kind of company needs without being
// able to research them all: those of the components it makes, and of the
// components that fit the goods it designs. Such a technology is in its lab
// for the licenses on offer.
func techsNeeded(snap *content.Snapshot, companyType string) map[string]bool {
	out := map[string]bool{}
	for _, comp := range snap.MadeBy(companyType) {
		for _, t := range comp.RequiresTechnology {
			out[t] = true
		}
	}
	for _, d := range snap.DesignableItems(companyType) {
		a, ok := snap.Archetype(d.Archetype)
		if !ok {
			continue
		}
		for _, sl := range a.Slots {
			for _, comp := range snap.SlotCandidates(a, sl.Name) {
				for _, t := range comp.RequiresTechnology {
					out[t] = true
				}
			}
		}
	}
	return out
}
