package handlers

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/military"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Defence licences on the minister's side (docs/adr/0022-military-and-diplomacy.md
// section 2.14): a country's public registry of licences, and the defence
// minister — or whoever acts for the seat, by the chain of ADR 0015 —
// approving or rejecting an application once, and revoking a licence with
// notice. The defence minister has no deputy: while the seat is empty,
// applications wait.

// pendingLicences counts the applications waiting in a country.
func pendingLicences(ctx context.Context, tx application.Tx, countryID string, now time.Time) (int, error) {
	lines, err := countryLicences(ctx, tx, countryID, 0)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, l := range lines {
		if licenceRule(l.Licence).Settled(now) == military.LicencePending && l.Company.Active() {
			n++
		}
	}
	return n, nil
}

// countryLicences lists the licences of a country's companies: every one
// open, and the ended ones latest first, at most ended of them.
func countryLicences(ctx context.Context, tx application.Tx, countryID string, ended int) ([]application.LicenceLine, error) {
	cities, err := tx.Diplomacy().CitiesOf(ctx, countryID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(cities))
	for _, c := range cities {
		ids = append(ids, c.ID)
	}
	return tx.Military().Licences(ctx, ids, ended)
}

// registry builds the registry of a country for a viewer.
func (h *MilitaryHandler) registry(ctx context.Context, tx application.Tx, snap *content.Snapshot, meta envelope.Metadata,
	country *application.Jurisdiction, p *application.Player,
) (screens.LicencesView, error) {
	now := h.now()
	v := screens.LicencesView{Country: countryPlace(*country), RevokeNotice: h.rules.LicenceRevokeNotice}
	lines, err := countryLicences(ctx, tx, country.ID, h.rules.EndedLicencesShown)
	if err != nil {
		return v, err
	}
	for _, l := range lines {
		e := licenceEntry(snap, l.Licence, l.Company, now)
		switch {
		case e.Status == string(military.LicencePending):
			if l.Company.Active() {
				v.Pending = append(v.Pending, e)
			}
		case licenceRule(l.Licence).InForce(now) && l.Company.Active():
			v.InForce = append(v.InForce, e)
		default:
			v.Ended = append(v.Ended, e)
		}
	}
	if !meta.InGroup() {
		if _, v.CanDecide, err = mayAct(ctx, tx, snap, country.ID, content.ActionDefenceLicence, p); err != nil {
			return v, err
		}
	}
	return v, nil
}

// Licences handles military.licences: a country's public registry of
// defence licences; in private, to whoever decides for the defence
// minister, with the buttons that decide.
func (h *MilitaryHandler) Licences(ctx context.Context, meta envelope.Metadata, req MilitaryRequest) (*presenter.Response, error) {
	return h.licencesWith(ctx, meta, req.Country, "", "", nil)
}

func (h *MilitaryHandler) licencesWith(ctx context.Context, meta envelope.Metadata, code, notice, company string,
	confirm *screens.LicenceEntry,
) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.LicencesView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		country, err := h.country(ctx, tx, p, code)
		if err != nil {
			return err
		}
		view, err = h.registry(ctx, tx, snap, meta, country, p)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	view.Notice, view.NoticeCompany, view.Confirm = notice, company, confirm
	return screens.Licences(h.screen(meta, lang), view), nil
}

// Licence handles military.licence: the defence minister's verdict on a
// licence — approve or reject an application, once; revoke a licence in
// force, confirmed, with notice. Everything else about it is public: the
// owner is told, and a grant or a revocation is announced in the groups of
// every city of the country.
func (h *MilitaryHandler) Licence(ctx context.Context, meta envelope.Metadata, req MilitaryRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	no, ok := number(req.No)
	verdict := strings.TrimSpace(req.Verdict)
	if !ok || (verdict != screens.LicenceApprove && verdict != screens.LicenceReject && verdict != screens.LicenceRevoke) {
		return h.Licences(ctx, meta, req)
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		countryCode string
		notice      string
		company     string
		confirm     *screens.LicenceEntry
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		write := verdict != screens.LicenceRevoke || req.confirmed()
		fresh := false
		if write {
			if fresh, err = h.reserve(ctx, tx, p.ID, meta); err != nil {
				return err
			}
		}
		l, err := tx.Military().LicenceByNo(ctx, no, false)
		if isSentinel(err, application.ErrDefenceLicenceNotFound) {
			return refuseMilitary(screens.MilitaryRefusedNotFound, nil)
		}
		if err != nil {
			return err
		}
		// The company first, then its licence: the order every licence
		// command takes them in.
		c, err := tx.Companies().Lock(ctx, l.CompanyID)
		if err != nil {
			return err
		}
		if l, err = tx.Military().LicenceByNo(ctx, no, true); err != nil {
			return err
		}
		countryID, err := tx.Diplomacy().CountryOfCity(ctx, c.CityID)
		if err != nil {
			return err
		}
		if countryID == "" {
			return refuseMilitary(screens.MilitaryRefusedNoCountry, nil)
		}
		country, err := tx.Governance().Jurisdiction(ctx, countryID)
		if err != nil {
			return err
		}
		countryCode = country.Code
		back := []string{screens.AddrLicences, country.Code}
		seat, may, err := mayAct(ctx, tx, snap, countryID, content.ActionDefenceLicence, p)
		if err != nil {
			return err
		}
		if !may {
			r := refuseMilitary(screens.MilitaryRefusedNotHolder, &country).back(back...)
			r.view.Office = actionOffice(snap, content.ActionDefenceLicence)
			return r
		}
		now := h.now()
		if !write {
			e := licenceEntry(snap, *l, *c, now)
			confirm = &e
			return nil
		}
		if !fresh {
			return nil
		}
		rule := licenceRule(*l)
		payload := map[string]any{"player_id": c.OwnerID, "company_code": c.Code, "company_name": c.Name, "type": c.TypeCode,
			"country_code": country.Code, "country_name": country.Name, "no": l.No}
		switch verdict {
		case screens.LicenceApprove, screens.LicenceReject:
			approve := verdict == screens.LicenceApprove && c.Active()
			next, err := military.Decide(rule, approve)
			if stderrors.Is(err, military.ErrLicenceState) {
				return refuseMilitary(screens.MilitaryRefusedLicenceState, &country).back(back...)
			}
			if err != nil {
				return err
			}
			l.Status, l.DecidedBy, l.DecidedOffice, l.DecidedAt, l.UpdatedAt = string(next.Status), p.ID, seat.OfficeCode, &now, now
			if err := tx.Military().SaveLicence(ctx, *l); err != nil {
				return err
			}
			kind := "rejected"
			if approve {
				kind = "approved"
			}
			notice, company = verdict, c.Name
			payload["kind"] = kind
			if err := appendDomainEvent(ctx, tx, meta, "military", "licence_decided", l.ID, payload); err != nil {
				return err
			}
			if !approve {
				return nil
			}
			return h.announceLicence(ctx, tx, meta, "licence_granted", l.ID, countryID, payload)
		default:
			next, err := military.Revoke(rule, now, h.rules.LicenceRevokeNotice)
			if stderrors.Is(err, military.ErrLicenceState) {
				return refuseMilitary(screens.MilitaryRefusedLicenceState, &country).back(back...)
			}
			if err != nil {
				return err
			}
			effective := next.EffectiveAt
			l.Status, l.RevokedBy, l.RevokedOffice, l.RevokedAt, l.EffectiveAt, l.UpdatedAt =
				string(next.Status), p.ID, seat.OfficeCode, &now, &effective, now
			if err := tx.Military().SaveLicence(ctx, *l); err != nil {
				return err
			}
			notice, company = verdict, c.Name
			payload["kind"], payload["effective_at"] = "revoked", effective
			if err := appendDomainEvent(ctx, tx, meta, "military", "licence_decided", l.ID, payload); err != nil {
				return err
			}
			return h.announceLicence(ctx, tx, meta, "licence_revoked", l.ID, countryID, payload)
		}
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.licencesWith(ctx, meta, countryCode, notice, company, confirm)
}

// announceLicence puts a grant or a revocation in the groups of every city
// of the country.
func (h *MilitaryHandler) announceLicence(ctx context.Context, tx application.Tx, meta envelope.Metadata, event, id,
	countryID string, payload map[string]any,
) error {
	cities, err := cityIDsOf(ctx, tx, countryID)
	if err != nil {
		return err
	}
	out := map[string]any{"city_ids": cities}
	for k, v := range payload {
		if k != "player_id" {
			out[k] = v
		}
	}
	return appendDomainEvent(ctx, tx, meta, "military", event, id, out)
}
