package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/diplomacy"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// DiplomacyRules is the tuning of sanctions and treaties (config
// diplomacy). Every duration is REAL time: a governance promise.
type DiplomacyRules struct {
	SanctionNotice      time.Duration
	SanctionMinDuration time.Duration
	TreatyOfferTTL      time.Duration
	EndedShownFor       time.Duration
	HistoryPageSize     int
}

// DiplomacyHandler serves sanctions and treaties
// (docs/adr/0022-military-and-diplomacy.md §2.7–2.8): the boards and the
// public record, anyone's to read; imposing and lifting a sanction, the
// holder of country.sanction's; proposing, answering, withdrawing and ending
// a treaty, the holder of country.treaty's — each authorised by
// application.Authorize, each recorded in diplomacy_events, each announced
// in both countries' groups.
type DiplomacyHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	rules   DiplomacyRules

	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewDiplomacyHandler wires the handler.
func NewDiplomacyHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	rules DiplomacyRules, idempotencyTTL time.Duration, now func() time.Time,
) *DiplomacyHandler {
	if source == nil || ids == nil {
		panic("handlers: NewDiplomacyHandler requires content and ids")
	}
	if idempotencyTTL <= 0 || rules.SanctionNotice < 0 || rules.SanctionMinDuration < 0 || rules.TreatyOfferTTL <= 0 ||
		rules.EndedShownFor < 0 || rules.HistoryPageSize < 1 {
		panic("handlers: NewDiplomacyHandler requires an idempotency ttl and valid rules")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &DiplomacyHandler{uow: uow, ids: ids, msgs: msgs, content: source, rules: rules,
		idempotencyTTL: idempotencyTTL, now: now}
}

// DiplomacyRequest is the payload of the diplomacy commands.
type DiplomacyRequest struct {
	Country string `json:"country,omitempty"`
	Target  string `json:"target,omitempty"`
	Mask    string `json:"mask,omitempty"`
	Ground  string `json:"ground,omitempty"`
	Kind    string `json:"kind,omitempty"`
	No      string `json:"no,omitempty"`
	Verdict string `json:"verdict,omitempty"`
	Confirm string `json:"confirm,omitempty"`
	Page    string `json:"page,omitempty"`
}

func (r DiplomacyRequest) confirmed() bool {
	return strings.TrimSpace(r.Confirm) == screens.DiplomacyConfirm
}

func (h *DiplomacyHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// diplomacyRefusal carries a refused diplomacy command out of a unit of
// work.
type diplomacyRefusal struct{ view screens.DiplomacyRefusalView }

func (r *diplomacyRefusal) Error() string { return "handlers: diplomacy refused: " + r.view.Kind }

func refuseDiplomacy(kind string, country *application.Jurisdiction, back ...string) *diplomacyRefusal {
	r := &diplomacyRefusal{view: screens.DiplomacyRefusalView{Kind: kind, Back: back}}
	if country != nil {
		r.view.Country = countryPlace(*country)
	}
	return r
}

func (h *DiplomacyHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	if err == nil {
		return nil, nil
	}
	c := h.screen(meta, lang)
	var r *diplomacyRefusal
	if stderrors.As(err, &r) {
		return screens.DiplomacyRefusal(c, r.view), nil
	}
	if isSentinel(err, application.ErrJurisdictionNotFound) {
		return screens.DiplomacyRefusal(c, screens.DiplomacyRefusalView{Kind: screens.DiplomacyRefusedNotFound}), nil
	}
	return nil, err
}

func (h *DiplomacyHandler) player(ctx context.Context, tx application.Tx, meta envelope.Metadata, lang *string) (*application.Player, error) {
	p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
	if err != nil {
		return nil, err
	}
	*lang = RenderLanguage(meta, p)
	return p, nil
}

func (h *DiplomacyHandler) reserve(ctx context.Context, tx application.Tx, playerID string, meta envelope.Metadata) (bool, error) {
	key := idempotency.Derive(playerID, meta.RequestID, meta.IdempotencyKey)
	return tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

func (h *DiplomacyHandler) country(ctx context.Context, tx application.Tx, p *application.Player, code string) (*application.Jurisdiction, error) {
	j, err := countryFor(ctx, tx, p, code)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, refuseDiplomacy(screens.DiplomacyRefusedNoCountry, nil)
	}
	return j, nil
}

// authorize is the holder of a country's action, or a refusal naming its
// office.
func (h *DiplomacyHandler) authorize(ctx context.Context, tx application.Tx, snap *content.Snapshot, country *application.Jurisdiction,
	action string, p *application.Player, back ...string,
) (application.Office, error) {
	seat, ok, err := mayAct(ctx, tx, snap, country.ID, action, p)
	if err != nil {
		return seat, err
	}
	if !ok {
		r := refuseDiplomacy(screens.DiplomacyRefusedNotHolder, country, back...)
		r.view.Office = actionOffice(snap, action)
		return seat, r
	}
	return seat, nil
}

// actingCountry is the country the player takes an action for — their own
// first, then any other whose office they hold or act for — with the seat
// they act from; a player who acts for none is refused, naming the office.
func (h *DiplomacyHandler) actingCountry(ctx context.Context, tx application.Tx, snap *content.Snapshot,
	p *application.Player, action string,
) (*application.Jurisdiction, application.Office, error) {
	home, err := countryFor(ctx, tx, p, "")
	if err != nil {
		return nil, application.Office{}, err
	}
	countries, err := tx.Diplomacy().Countries(ctx)
	if err != nil {
		return nil, application.Office{}, err
	}
	var order []application.Jurisdiction
	if home != nil {
		order = append(order, *home)
	}
	for _, c := range countries {
		if home == nil || c.ID != home.ID {
			order = append(order, c)
		}
	}
	for i := range order {
		seat, ok, err := mayAct(ctx, tx, snap, order[i].ID, action, p)
		if err != nil {
			return nil, application.Office{}, err
		}
		if ok {
			return &order[i], seat, nil
		}
	}
	var back []string
	if home != nil {
		back = []string{screens.AddrSanctions, home.Code}
	}
	r := refuseDiplomacy(screens.DiplomacyRefusedNotHolder, home, back...)
	r.view.Office = actionOffice(snap, action)
	return nil, application.Office{}, r
}

// lockPair takes both countries' diplomacy locks, the lower id first.
func lockPair(ctx context.Context, tx application.Tx, a, b string) error {
	first, second := a, b
	if second < first {
		first, second = second, first
	}
	if err := tx.Diplomacy().LockCountry(ctx, first); err != nil {
		return err
	}
	return tx.Diplomacy().LockCountry(ctx, second)
}

// measureCodes renders measures as their codes.
func measureCodes(ms []diplomacy.Measure) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = string(m)
	}
	return out
}

// sanctionLine reads a sanction for a board.
func (h *DiplomacyHandler) sanctionLine(ctx context.Context, tx application.Tx, s application.Sanction, now time.Time) (screens.SanctionLine, error) {
	imposer, err := placeOf(ctx, tx, s.ImposerID)
	if err != nil {
		return screens.SanctionLine{}, err
	}
	target, err := placeOf(ctx, tx, s.TargetID)
	if err != nil {
		return screens.SanctionLine{}, err
	}
	line := screens.SanctionLine{No: s.No, Imposer: imposer, Target: target, Measures: measureCodes(s.Measures),
		Ground: s.Ground, Office: s.ImposedOffice, Since: max(now.Sub(s.EffectiveAt), time.Second)}
	if s.EffectiveAt.After(now) {
		line.InForceIn = s.EffectiveAt.Sub(now)
	}
	if by, err := playerNamed(ctx, tx, s.ImposedBy); err != nil {
		return line, err
	} else if by.Code != "" {
		line.By = &by
	}
	if at := diplomacy.LiftableAt(s.Rule(), h.rules.SanctionMinDuration); now.Before(at) {
		line.LiftableIn = at.Sub(now)
	} else {
		line.Liftable = true
	}
	return line, nil
}

// Sanctions handles diplomacy.sanctions: a country's sanctions board.
func (h *DiplomacyHandler) Sanctions(ctx context.Context, meta envelope.Metadata, req DiplomacyRequest) (*presenter.Response, error) {
	return h.sanctionsWith(ctx, meta, req, "")
}

func (h *DiplomacyHandler) sanctionsWith(ctx context.Context, meta envelope.Metadata, req DiplomacyRequest, notice string) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.SanctionsView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		country, err := h.country(ctx, tx, p, req.Country)
		if err != nil {
			return err
		}
		now := h.now()
		view = screens.SanctionsView{Country: countryPlace(*country), Notice: notice}
		standing, err := tx.Diplomacy().StandingSanctions(ctx, country.ID)
		if err != nil {
			return err
		}
		for _, s := range standing {
			line, err := h.sanctionLine(ctx, tx, s, now)
			if err != nil {
				return err
			}
			if s.ImposerID == country.ID {
				view.Imposed = append(view.Imposed, line)
			} else {
				view.Suffered = append(view.Suffered, line)
			}
		}
		if !meta.InGroup() {
			_, view.CanImpose, err = mayAct(ctx, tx, snap, country.ID, content.ActionSanction, p)
		}
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Sanctions(h.screen(meta, lang), view), nil
}

// Impose handles diplomacy.impose: the flow that imposes a sanction — the
// target, the measures (a mask a button toggles), the ground, then confirm;
// on confirm, once, the sanction is recorded, announced now and binds after
// its notice.
func (h *DiplomacyHandler) Impose(ctx context.Context, meta envelope.Metadata, req DiplomacyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view    screens.ImposeView
		done    bool
		country *application.Jurisdiction
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		confirm := req.confirmed()
		if confirm {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			confirm = fresh
		}
		var seat application.Office
		if country, seat, err = h.actingCountry(ctx, tx, snap, p, content.ActionSanction); err != nil {
			return err
		}
		view = screens.ImposeView{Country: countryPlace(*country), Notice: h.rules.SanctionNotice,
			MinDuration: h.rules.SanctionMinDuration}
		targetCode := strings.ToLower(strings.TrimSpace(req.Target))
		if targetCode == "" {
			countries, err := tx.Diplomacy().Countries(ctx)
			if err != nil {
				return err
			}
			for _, c := range countries {
				if c.ID != country.ID {
					view.Targets = append(view.Targets, countryPlace(c))
				}
			}
			return nil
		}
		target, err := tx.Diplomacy().CountryByCode(ctx, targetCode)
		if err != nil {
			return err
		}
		if target.ID == country.ID {
			return refuseDiplomacy(screens.DiplomacyRefusedSelf, country, screens.AddrImpose)
		}
		place := countryPlace(target)
		view.Target = &place
		mask := 0
		if raw := strings.TrimSpace(req.Mask); raw != "" {
			if mask, err = strconv.Atoi(raw); err != nil {
				mask = 0
			}
		}
		chosen, err := diplomacy.FromMask(mask)
		if err != nil {
			chosen, mask = nil, 0
		}
		view.Mask, view.Chosen = mask, measureCodes(chosen)
		ground := strings.TrimSpace(req.Ground)
		if ground == "" || mask == 0 {
			for _, m := range diplomacy.Measures() {
				// Pressing a measure flips it: the mask it leads to is below
				// this one exactly when the measure is chosen now.
				next, _ := diplomacy.Toggle(mask, m)
				view.Measures = append(view.Measures, screens.MeasureToggle{Code: string(m), On: next < mask, Mask: next})
			}
			return nil
		}
		if ground == screens.ChooseGround || !hasCode(snap.SanctionGrounds(), ground) {
			view.Grounds = snap.SanctionGrounds()
			return nil
		}
		view.Ground = ground
		if !confirm {
			return nil
		}
		if err := lockPair(ctx, tx, country.ID, target.ID); err != nil {
			return err
		}
		standing, err := tx.Diplomacy().StandingSanctions(ctx, country.ID)
		if err != nil {
			return err
		}
		rules := make([]diplomacy.Sanction, len(standing))
		for i, s := range standing {
			rules[i] = s.Rule()
		}
		if err := diplomacy.CheckImpose(country.ID, target.ID, chosen, rules); err != nil {
			if stderrors.Is(err, diplomacy.ErrAlreadySanctioned) {
				return refuseDiplomacy(screens.DiplomacyRefusedStanding, country, screens.AddrSanctions, country.Code)
			}
			return errors.InvalidInput("the sanction cannot be imposed").WithCause(err)
		}
		now := h.now()
		s, err := tx.Diplomacy().ImposeSanction(ctx, application.Sanction{ID: h.ids.NewID(), ImposerID: country.ID,
			TargetID: target.ID, Measures: chosen, Ground: ground, ImposedBy: p.ID, ImposedOffice: seat.OfficeCode,
			ImposedAt: now, EffectiveAt: now.Add(h.rules.SanctionNotice)})
		if isSentinel(err, application.ErrAlreadySanctioned) {
			return refuseDiplomacy(screens.DiplomacyRefusedStanding, country, screens.AddrSanctions, country.Code)
		}
		if err != nil {
			return err
		}
		if err := tx.Diplomacy().RecordEvent(ctx, application.DiplomacyEvent{ID: h.ids.NewID(),
			Kind: application.EventSanctionImposed, CountryID: country.ID, OtherCountryID: target.ID, SanctionID: s.ID,
			PlayerID: p.ID, OfficeCode: seat.OfficeCode, At: now}); err != nil {
			return err
		}
		cities, err := cityIDsOf(ctx, tx, country.ID, target.ID)
		if err != nil {
			return err
		}
		done = true
		return appendDomainEvent(ctx, tx, meta, "diplomacy", "sanction_imposed", s.ID, map[string]any{
			"imposer_code": country.Code, "imposer_name": country.Name, "target_code": target.Code,
			"target_name": target.Name, "measures": view.Chosen, "ground": ground, "city_ids": cities})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if done {
		c := h.screen(meta, lang)
		notice := c.T("diplomacy.impose.done", map[string]any{"target": c.PlaceName(*view.Target),
			"in": screens.FormatSpan(c, h.rules.SanctionNotice)})
		return h.sanctionsWith(ctx, meta, DiplomacyRequest{Country: country.Code}, notice)
	}
	return screens.Impose(h.screen(meta, lang), view), nil
}

// Lift handles diplomacy.lift: the confirmation, then lifting one of the
// country's sanctions, once, no sooner than its least duration.
func (h *DiplomacyHandler) Lift(ctx context.Context, meta envelope.Metadata, req DiplomacyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view    screens.LiftView
		done    bool
		country *application.Jurisdiction
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		confirm := req.confirmed()
		if confirm {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			confirm = fresh
		}
		no, ok := number(req.No)
		if !ok {
			return refuseDiplomacy(screens.DiplomacyRefusedNotFound, nil)
		}
		s, err := tx.Diplomacy().SanctionByNo(ctx, no, false)
		if isSentinel(err, application.ErrSanctionNotFound) {
			return refuseDiplomacy(screens.DiplomacyRefusedNotFound, nil)
		}
		if err != nil {
			return err
		}
		j, err := tx.Governance().Jurisdiction(ctx, s.ImposerID)
		if err != nil {
			return err
		}
		country = &j
		seat, err := h.authorize(ctx, tx, snap, country, content.ActionSanction, p, screens.AddrSanctions, country.Code)
		if err != nil {
			return err
		}
		now := h.now()
		line, err := h.sanctionLine(ctx, tx, *s, now)
		if err != nil {
			return err
		}
		view = screens.LiftView{Country: countryPlace(*country), Sanction: line}
		if s.LiftedAt != nil {
			return refuseDiplomacy(screens.DiplomacyRefusedNotFound, country, screens.AddrSanctions, country.Code)
		}
		if err := diplomacy.CheckLift(s.Rule(), country.ID, now, h.rules.SanctionMinDuration); err != nil {
			if stderrors.Is(err, diplomacy.ErrTooSoon) {
				r := refuseDiplomacy(screens.DiplomacyRefusedTooSoon, country, screens.AddrSanctions, country.Code)
				r.view.In = line.LiftableIn
				return r
			}
			return errors.InvalidInput("the sanction cannot be lifted").WithCause(err)
		}
		if !confirm {
			return nil
		}
		if err := lockPair(ctx, tx, s.ImposerID, s.TargetID); err != nil {
			return err
		}
		if s, err = tx.Diplomacy().SanctionByNo(ctx, no, true); err != nil {
			return err
		}
		if s.LiftedAt != nil {
			return nil
		}
		if err := tx.Diplomacy().LiftSanction(ctx, s.ID, p.ID, seat.OfficeCode, now); err != nil {
			return err
		}
		if err := tx.Diplomacy().RecordEvent(ctx, application.DiplomacyEvent{ID: h.ids.NewID(),
			Kind: application.EventSanctionLifted, CountryID: s.ImposerID, OtherCountryID: s.TargetID, SanctionID: s.ID,
			PlayerID: p.ID, OfficeCode: seat.OfficeCode, At: now}); err != nil {
			return err
		}
		target, err := tx.Governance().Jurisdiction(ctx, s.TargetID)
		if err != nil {
			return err
		}
		cities, err := cityIDsOf(ctx, tx, s.ImposerID, s.TargetID)
		if err != nil {
			return err
		}
		done = true
		return appendDomainEvent(ctx, tx, meta, "diplomacy", "sanction_lifted", s.ID, map[string]any{
			"imposer_code": country.Code, "imposer_name": country.Name, "target_code": target.Code,
			"target_name": target.Name, "city_ids": cities})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if done {
		c := h.screen(meta, lang)
		notice := c.T("diplomacy.lift.done", map[string]any{"target": c.PlaceName(view.Sanction.Target)})
		return h.sanctionsWith(ctx, meta, DiplomacyRequest{Country: country.Code}, notice)
	}
	return screens.Lift(h.screen(meta, lang), view), nil
}

// treatyLine reads a treaty for a board, from the country's side.
func (h *DiplomacyHandler) treatyLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, t application.Treaty,
	countryID string, now time.Time,
) (screens.TreatyLine, error) {
	rule := t.Rule()
	other, err := placeOf(ctx, tx, rule.Other(countryID))
	if err != nil {
		return screens.TreatyLine{}, err
	}
	kind := named(t.Kind, t.Kind)
	if def, ok := snap.TreatyType(t.Kind); ok {
		kind.Name = def.Name
	}
	status := rule.StatusAt(now)
	line := screens.TreatyLine{No: t.No, Kind: kind, Other: other, Status: string(status),
		Incoming: t.PartnerID == countryID}
	switch status {
	case diplomacy.Proposed:
		line.ExpiresIn = t.ExpiresAt.Sub(now)
	case diplomacy.Expired:
		line.Since = now.Sub(t.ExpiresAt)
	default:
		at := t.ProposedAt
		if t.DecidedAt != nil {
			at = *t.DecidedAt
		}
		if t.EndedAt != nil {
			at = *t.EndedAt
		}
		line.Since = now.Sub(at)
	}
	line.Since = max(line.Since, time.Second)
	return line, nil
}

// Treaties handles diplomacy.treaties: a country's treaties board.
func (h *DiplomacyHandler) Treaties(ctx context.Context, meta envelope.Metadata, req DiplomacyRequest) (*presenter.Response, error) {
	return h.treatiesWith(ctx, meta, req, "")
}

func (h *DiplomacyHandler) treatiesWith(ctx context.Context, meta envelope.Metadata, req DiplomacyRequest, notice string) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.TreatiesView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		country, err := h.country(ctx, tx, p, req.Country)
		if err != nil {
			return err
		}
		now := h.now()
		view = screens.TreatiesView{Country: countryPlace(*country), Notice: notice}
		treaties, err := tx.Diplomacy().Treaties(ctx, country.ID, now.Add(-h.rules.EndedShownFor))
		if err != nil {
			return err
		}
		for _, t := range treaties {
			line, err := h.treatyLine(ctx, tx, snap, t, country.ID, now)
			if err != nil {
				return err
			}
			view.Treaties = append(view.Treaties, line)
		}
		if !meta.InGroup() {
			_, view.CanAct, err = mayAct(ctx, tx, snap, country.ID, content.ActionTreaty, p)
		}
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Treaties(h.screen(meta, lang), view), nil
}

// Propose handles diplomacy.propose: the partner, the kind, then confirm;
// on confirm, once, the proposal is recorded and the partner's acting
// foreign minister is told.
func (h *DiplomacyHandler) Propose(ctx context.Context, meta envelope.Metadata, req DiplomacyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view    screens.ProposeView
		done    bool
		country *application.Jurisdiction
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		confirm := req.confirmed()
		if confirm {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			confirm = fresh
		}
		var seat application.Office
		if country, seat, err = h.actingCountry(ctx, tx, snap, p, content.ActionTreaty); err != nil {
			return err
		}
		view = screens.ProposeView{Country: countryPlace(*country), TTL: h.rules.TreatyOfferTTL}
		partnerCode := strings.ToLower(strings.TrimSpace(req.Target))
		if partnerCode == "" {
			countries, err := tx.Diplomacy().Countries(ctx)
			if err != nil {
				return err
			}
			for _, c := range countries {
				if c.ID != country.ID {
					view.Partners = append(view.Partners, countryPlace(c))
				}
			}
			return nil
		}
		partner, err := tx.Diplomacy().CountryByCode(ctx, partnerCode)
		if err != nil {
			return err
		}
		if partner.ID == country.ID {
			return refuseDiplomacy(screens.DiplomacyRefusedSelf, country, screens.AddrPropose)
		}
		place := countryPlace(partner)
		view.Partner = &place
		def, ok := snap.TreatyType(strings.TrimSpace(req.Kind))
		if !ok {
			for _, t := range snap.TreatyTypes() {
				view.Kinds = append(view.Kinds, named(t.Code, t.Name))
			}
			return nil
		}
		kind := named(def.Code, def.Name)
		view.Kind = &kind
		if !confirm {
			return nil
		}
		if err := lockPair(ctx, tx, country.ID, partner.ID); err != nil {
			return err
		}
		now := h.now()
		if err := tx.Diplomacy().ExpireTreaties(ctx, country.ID, partner.ID, now); err != nil {
			return err
		}
		existing, err := tx.Diplomacy().TreatiesBetween(ctx, country.ID, partner.ID)
		if err != nil {
			return err
		}
		rules := make([]diplomacy.Treaty, len(existing))
		for i, t := range existing {
			rules[i] = t.Rule()
		}
		if err := diplomacy.CheckPropose(country.ID, partner.ID, def.Code, rules, now); err != nil {
			return refuseDiplomacy(screens.DiplomacyRefusedOpen, country, screens.AddrTreaties, country.Code)
		}
		t, err := tx.Diplomacy().ProposeTreaty(ctx, application.Treaty{ID: h.ids.NewID(), Kind: def.Code,
			ProposerID: country.ID, PartnerID: partner.ID, ProposedBy: p.ID, ProposedOffice: seat.OfficeCode,
			ProposedAt: now, ExpiresAt: now.Add(h.rules.TreatyOfferTTL)})
		if isSentinel(err, application.ErrTreatyOpen) {
			return refuseDiplomacy(screens.DiplomacyRefusedOpen, country, screens.AddrTreaties, country.Code)
		}
		if err != nil {
			return err
		}
		if err := tx.Diplomacy().RecordEvent(ctx, application.DiplomacyEvent{ID: h.ids.NewID(),
			Kind: application.EventTreatyProposed, CountryID: country.ID, OtherCountryID: partner.ID, TreatyID: t.ID,
			PlayerID: p.ID, OfficeCode: seat.OfficeCode, At: now}); err != nil {
			return err
		}
		// The partner's foreign minister — or whoever acts for the seat —
		// hears of it privately.
		chain, err := tx.Governance().ActingChain(ctx, actionOffice(snap, content.ActionTreaty), partner.ID)
		if err != nil {
			return err
		}
		if acting := application.ActingForChain(chain); acting != nil {
			for _, s := range acting.Holders {
				if err := appendDomainEvent(ctx, tx, meta, "diplomacy", "treaty_proposed", t.ID, map[string]any{
					"player_id": s.HolderPlayerID, "no": t.No, "kind": def.Code, "kind_name": def.Name,
					"country_code": partner.Code, "country_name": partner.Name, "other_code": country.Code,
					"other_name": country.Name, "ttl_seconds": int64(h.rules.TreatyOfferTTL / time.Second)}); err != nil {
					return err
				}
			}
		}
		done = true
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if done {
		c := h.screen(meta, lang)
		notice := c.T("diplomacy.propose.done", map[string]any{"partner": c.PlaceName(*view.Partner),
			"kind": c.TreatyName(*view.Kind)})
		return h.treatiesWith(ctx, meta, DiplomacyRequest{Country: country.Code}, notice)
	}
	return screens.Propose(h.screen(meta, lang), view), nil
}

// treatyFor reads a treaty by number and the side of it the player acts
// for: the country of theirs that is a party and whose treaty action they
// hold. It refuses a treaty they act for no party of.
func (h *DiplomacyHandler) treatyFor(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
	rawNo string, prefer func(t application.Treaty) []string,
) (*application.Treaty, *application.Jurisdiction, application.Office, error) {
	no, ok := number(rawNo)
	if !ok {
		return nil, nil, application.Office{}, refuseDiplomacy(screens.DiplomacyRefusedNotFound, nil)
	}
	t, err := tx.Diplomacy().TreatyByNo(ctx, no, false)
	if isSentinel(err, application.ErrTreatyNotFound) {
		return nil, nil, application.Office{}, refuseDiplomacy(screens.DiplomacyRefusedNotFound, nil)
	}
	if err != nil {
		return nil, nil, application.Office{}, err
	}
	var first *application.Jurisdiction
	for _, id := range prefer(*t) {
		j, err := tx.Governance().Jurisdiction(ctx, id)
		if err != nil {
			return nil, nil, application.Office{}, err
		}
		if first == nil {
			first = &j
		}
		seat, ok, err := mayAct(ctx, tx, snap, id, content.ActionTreaty, p)
		if err != nil {
			return nil, nil, application.Office{}, err
		}
		if ok {
			return t, &j, seat, nil
		}
	}
	r := refuseDiplomacy(screens.DiplomacyRefusedNotHolder, first, screens.AddrTreaties, first.Code)
	r.view.Office = actionOffice(snap, content.ActionTreaty)
	return nil, nil, application.Office{}, r
}

// Answer handles diplomacy.answer: the partner's acting foreign minister
// accepts or declines a proposal — once: the row is locked and must still
// be a proposal in time.
func (h *DiplomacyHandler) Answer(ctx context.Context, meta envelope.Metadata, req DiplomacyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	accept := strings.TrimSpace(req.Verdict) == screens.AnswerAccept
	if !accept && strings.TrimSpace(req.Verdict) != screens.AnswerDecline {
		return nil, errors.InvalidInput("an answer is accept or decline")
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		country *application.Jurisdiction
		answer  diplomacy.Status
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
		t, c, seat, err := h.treatyFor(ctx, tx, snap, p, req.No, func(t application.Treaty) []string { return []string{t.PartnerID} })
		if err != nil {
			return err
		}
		country = c
		if err := lockPair(ctx, tx, t.ProposerID, t.PartnerID); err != nil {
			return err
		}
		if t, err = tx.Diplomacy().TreatyByNo(ctx, t.No, true); err != nil {
			return err
		}
		now := h.now()
		status, err := diplomacy.Answer(t.Rule(), c.ID, accept, now)
		if err != nil {
			return refuseDiplomacy(screens.DiplomacyRefusedState, c, screens.AddrTreaties, c.Code)
		}
		t.Status, t.DecidedBy, t.DecidedOffice, t.DecidedAt = status, p.ID, seat.OfficeCode, &now
		if err := tx.Diplomacy().SaveTreaty(ctx, *t); err != nil {
			if isSentinel(err, application.ErrTreatyOpen) {
				return refuseDiplomacy(screens.DiplomacyRefusedOpen, c, screens.AddrTreaties, c.Code)
			}
			return err
		}
		kind := application.EventTreatyDeclined
		if accept {
			kind = application.EventTreatySigned
		}
		if err := tx.Diplomacy().RecordEvent(ctx, application.DiplomacyEvent{ID: h.ids.NewID(), Kind: kind,
			CountryID: c.ID, OtherCountryID: t.ProposerID, TreatyID: t.ID, PlayerID: p.ID, OfficeCode: seat.OfficeCode,
			At: now}); err != nil {
			return err
		}
		answer = status
		if !accept {
			return nil
		}
		proposer, err := tx.Governance().Jurisdiction(ctx, t.ProposerID)
		if err != nil {
			return err
		}
		cities, err := cityIDsOf(ctx, tx, t.ProposerID, t.PartnerID)
		if err != nil {
			return err
		}
		kindName := t.Kind
		if def, ok := snap.TreatyType(t.Kind); ok {
			kindName = def.Name
		}
		return appendDomainEvent(ctx, tx, meta, "diplomacy", "treaty_signed", t.ID, map[string]any{
			"a_code": proposer.Code, "a_name": proposer.Name, "b_code": c.Code, "b_name": c.Name, "kind": t.Kind,
			"kind_name": kindName, "city_ids": cities})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	notice := ""
	if answer != "" {
		c := h.screen(meta, lang)
		notice = c.T("diplomacy.answer."+string(answer), nil)
	}
	code := ""
	if country != nil {
		code = country.Code
	}
	return h.treatiesWith(ctx, meta, DiplomacyRequest{Country: code}, notice)
}

// End handles diplomacy.end: the confirmation, then withdrawing a proposal
// (the proposer) or ending a treaty in force (either party), once.
func (h *DiplomacyHandler) End(ctx context.Context, meta envelope.Metadata, req DiplomacyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view    screens.EndTreatyView
		country *application.Jurisdiction
		ended   diplomacy.Status
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		confirm := req.confirmed()
		if confirm {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			confirm = fresh
		}
		t, c, seat, err := h.treatyFor(ctx, tx, snap, p, req.No, func(t application.Treaty) []string {
			return []string{t.ProposerID, t.PartnerID}
		})
		if err != nil {
			return err
		}
		country = c
		now := h.now()
		line, err := h.treatyLine(ctx, tx, snap, *t, c.ID, now)
		if err != nil {
			return err
		}
		view = screens.EndTreatyView{Country: countryPlace(*c), Treaty: line}
		if _, err := diplomacy.End(t.Rule(), c.ID, now); err != nil {
			return refuseDiplomacy(screens.DiplomacyRefusedState, c, screens.AddrTreaties, c.Code)
		}
		if !confirm {
			return nil
		}
		if err := lockPair(ctx, tx, t.ProposerID, t.PartnerID); err != nil {
			return err
		}
		if t, err = tx.Diplomacy().TreatyByNo(ctx, t.No, true); err != nil {
			return err
		}
		status, err := diplomacy.End(t.Rule(), c.ID, now)
		if err != nil {
			return refuseDiplomacy(screens.DiplomacyRefusedState, c, screens.AddrTreaties, c.Code)
		}
		t.Status, t.EndedBy, t.EndedOffice, t.EndedAt = status, p.ID, seat.OfficeCode, &now
		if err := tx.Diplomacy().SaveTreaty(ctx, *t); err != nil {
			return err
		}
		kind := application.EventTreatyWithdrawn
		if status == diplomacy.Terminated {
			kind = application.EventTreatyTerminated
		}
		other := t.Rule().Other(c.ID)
		if err := tx.Diplomacy().RecordEvent(ctx, application.DiplomacyEvent{ID: h.ids.NewID(), Kind: kind,
			CountryID: c.ID, OtherCountryID: other, TreatyID: t.ID, PlayerID: p.ID, OfficeCode: seat.OfficeCode,
			At: now}); err != nil {
			return err
		}
		ended = status
		if status != diplomacy.Terminated {
			return nil
		}
		otherJ, err := tx.Governance().Jurisdiction(ctx, other)
		if err != nil {
			return err
		}
		cities, err := cityIDsOf(ctx, tx, c.ID, other)
		if err != nil {
			return err
		}
		kindName := t.Kind
		if def, ok := snap.TreatyType(t.Kind); ok {
			kindName = def.Name
		}
		return appendDomainEvent(ctx, tx, meta, "diplomacy", "treaty_terminated", t.ID, map[string]any{
			"by_code": c.Code, "by_name": c.Name, "other_code": otherJ.Code, "other_name": otherJ.Name, "kind": t.Kind,
			"kind_name": kindName, "city_ids": cities})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if ended != "" {
		c := h.screen(meta, lang)
		return h.treatiesWith(ctx, meta, DiplomacyRequest{Country: country.Code}, c.T("diplomacy.end.done_"+string(ended), nil))
	}
	return screens.EndTreaty(h.screen(meta, lang), view), nil
}

// History handles diplomacy.history: one page of the public record
// concerning a country.
func (h *DiplomacyHandler) History(ctx context.Context, meta envelope.Metadata, req DiplomacyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.DiplomacyHistoryView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		country, err := h.country(ctx, tx, p, req.Country)
		if err != nil {
			return err
		}
		page := 1
		if n, ok := number(req.Page); ok {
			page = int(n)
		}
		size := h.rules.HistoryPageSize
		entries, total, err := tx.Diplomacy().Events(ctx, country.ID, size, (page-1)*size)
		if err != nil {
			return err
		}
		now := h.now()
		view = screens.DiplomacyHistoryView{Country: countryPlace(*country), Page: page, Pages: max((total+size-1)/size, 1)}
		for _, e := range entries {
			a, err := placeOf(ctx, tx, e.CountryID)
			if err != nil {
				return err
			}
			b, err := placeOf(ctx, tx, e.OtherCountryID)
			if err != nil {
				return err
			}
			entry := screens.DiplomacyEntry{Kind: e.Kind, Country: a, Other: b, Measures: measureCodes(e.Measures),
				Ground: e.Ground, Treaty: named(e.TreatyKind, e.TreatyKind), No: e.SanctionNo + e.TreatyNo,
				Office: e.OfficeCode, Ago: max(now.Sub(e.At), time.Second)}
			if def, ok := snap.TreatyType(e.TreatyKind); ok {
				entry.Treaty.Name = def.Name
			}
			if by, err := playerNamed(ctx, tx, e.PlayerID); err != nil {
				return err
			} else if by.Code != "" {
				entry.By = &by
			}
			view.Entries = append(view.Entries, entry)
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.DiplomacyHistory(h.screen(meta, lang), view), nil
}
