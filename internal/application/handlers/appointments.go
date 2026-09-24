package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// AppointmentHandler serves appointments by office holders
// (docs/adr/0022-military-and-diplomacy.md §2.2): the holder of the office
// another is appointed by — or the deputy acting for it — seats a player in
// a vacant seat of it (gov.appoint, then gov.seat), and a holder whose
// office may remove its holder vacates it (gov.dismiss, then gov.unseat).
// The seating itself is application.AppointToOffice and VacateOffice, the
// same rules the operator's appointment keeps: incompatible offices, one
// seat of a body, no silent unseating.
type AppointmentHandler struct {
	uow    application.UnitOfWork
	ids    IDGenerator
	msgs   Translator
	search application.PlayerSearch

	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewAppointmentHandler wires the handler.
func NewAppointmentHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, search application.PlayerSearch,
	idempotencyTTL time.Duration, now func() time.Time,
) *AppointmentHandler {
	if ids == nil || search == nil || idempotencyTTL <= 0 {
		panic("handlers: NewAppointmentHandler requires ids, a player search and an idempotency ttl")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &AppointmentHandler{uow: uow, ids: ids, msgs: msgs, search: search, idempotencyTTL: idempotencyTTL, now: now}
}

// AppointRequest is the payload of gov.appoint, gov.seat, gov.dismiss and
// gov.unseat: the office, the place's code, and the player (a code or a
// username) or the seat.
type AppointRequest struct {
	Office string `json:"office,omitempty"`
	Place  string `json:"place,omitempty"`
	To     string `json:"to,omitempty"`
	Seat   string `json:"seat,omitempty"`
}

func (h *AppointmentHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta)}
}

// appointRefusal carries a refused appointment out of a unit of work.
type appointRefusal struct{ view screens.AppointRefusalView }

func (r *appointRefusal) Error() string { return "handlers: appointment refused: " + r.view.Kind }

func (h *AppointmentHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	if err == nil {
		return nil, nil
	}
	c := h.screen(meta, lang)
	var r *appointRefusal
	if stderrors.As(err, &r) {
		return screens.AppointRefusal(c, r.view), nil
	}
	if screens.IsGovernanceRefusal(err) {
		return screens.AppointRefusal(c, screens.AppointRefusalView{Err: err}), nil
	}
	return nil, err
}

// target is an office of a place and the definition of it, with the office
// that appoints to it.
type appointTarget struct {
	def   application.OfficeDefinition
	place application.Jurisdiction
}

// resolve reads the office and the place a request names. The place is a
// city's code or a jurisdiction's, whichever level the office is of.
func (h *AppointmentHandler) resolve(ctx context.Context, tx application.Tx, req AppointRequest) (appointTarget, error) {
	defs, err := tx.Governance().OfficeDefinitions(ctx)
	if err != nil {
		return appointTarget{}, err
	}
	code := strings.TrimSpace(req.Office)
	for _, d := range defs {
		if d.Code != code {
			continue
		}
		place := strings.ToLower(strings.TrimSpace(req.Place))
		var j application.Jurisdiction
		if d.Jurisdiction == "country" {
			if j, err = tx.Diplomacy().CountryByCode(ctx, place); err != nil {
				return appointTarget{}, err
			}
		} else if j, err = cityJurisdiction(ctx, tx, place); err != nil {
			return appointTarget{}, err
		}
		if j.Kind != d.Jurisdiction {
			return appointTarget{}, application.ErrWrongJurisdiction
		}
		return appointTarget{def: d, place: j}, nil
	}
	return appointTarget{}, application.ErrOfficeNotFound
}

// cityJurisdiction reads a city's own jurisdiction by the city's code.
func cityJurisdiction(ctx context.Context, tx application.Tx, code string) (application.Jurisdiction, error) {
	countries, err := tx.Diplomacy().Countries(ctx)
	if err != nil {
		return application.Jurisdiction{}, err
	}
	for _, c := range countries {
		cities, err := tx.Diplomacy().CitiesOf(ctx, c.ID)
		if err != nil {
			return application.Jurisdiction{}, err
		}
		for _, city := range cities {
			if city.Code == code && city.JurisdictionID != "" {
				return tx.Governance().Jurisdiction(ctx, city.JurisdictionID)
			}
		}
	}
	return application.Jurisdiction{}, application.ErrJurisdictionNotFound
}

// appointer authorises the player to appoint to the target's office: the
// holder of the office it is appointed by, or the deputy acting for it.
func appointer(ctx context.Context, tx application.Tx, t appointTarget, playerID string) (application.Office, error) {
	if t.def.AppointedBy == "" {
		return application.Office{}, &appointRefusal{view: screens.AppointRefusalView{Kind: screens.AppointRefusedNotAppointer,
			Office: t.def.Code}}
	}
	seat, err := application.Authorize(ctx, tx, t.place.ID, t.def.AppointedBy, playerID)
	if stderrors.Is(err, application.ErrNotOfficeHolder) {
		return seat, &appointRefusal{view: screens.AppointRefusalView{Kind: screens.AppointRefusedNotAppointer,
			Office: t.def.AppointedBy}}
	}
	return seat, err
}

// remover authorises the player to remove the target's holder: the holder
// of an office its can_be_removed_by names, in the same place.
func remover(ctx context.Context, tx application.Tx, t appointTarget, playerID string) (application.Office, error) {
	for _, office := range t.def.CanBeRemovedBy {
		seat, err := application.Authorize(ctx, tx, t.place.ID, office, playerID)
		if err == nil {
			return seat, nil
		}
		if !stderrors.Is(err, application.ErrNotOfficeHolder) && !stderrors.Is(err, application.ErrJurisdictionNotFound) {
			return seat, err
		}
	}
	office := ""
	if len(t.def.CanBeRemovedBy) > 0 {
		office = t.def.CanBeRemovedBy[0]
	}
	return application.Office{}, &appointRefusal{view: screens.AppointRefusalView{Kind: screens.AppointRefusedNotAppointer,
		Office: office}}
}

// vacantSeat is the first vacant seat of the target's office.
func vacantSeat(ctx context.Context, tx application.Tx, t appointTarget) (int, error) {
	for seat := 1; seat <= t.def.Seats; seat++ {
		s, err := tx.Governance().Seat(ctx, t.def.Code, t.place.ID, seat)
		if err != nil {
			return 0, err
		}
		if s.Vacant() {
			return seat, nil
		}
	}
	return 0, &appointRefusal{view: screens.AppointRefusalView{Kind: screens.AppointRefusedNoSeat, Office: t.def.Code}}
}

// Appoint handles gov.appoint: the player the appointer typed, found and
// shown for confirmation.
func (h *AppointmentHandler) Appoint(ctx context.Context, meta envelope.Metadata, req AppointRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	var candidate *application.Player
	if q, ok := ClassifyPlayerQuery(req.To); ok {
		found, err := h.search.Find(ctx, q)
		if err != nil && !isSentinel(err, application.ErrPlayerNotFound) {
			return nil, err
		}
		candidate = found
	}
	lang := meta.Language
	var view screens.AppointView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		t, err := h.resolve(ctx, tx, req)
		if err != nil {
			return err
		}
		if _, err := appointer(ctx, tx, t, p.ID); err != nil {
			return err
		}
		if _, err := vacantSeat(ctx, tx, t); err != nil {
			return err
		}
		if candidate == nil || candidate.Status != playerActive {
			return &appointRefusal{view: screens.AppointRefusalView{Kind: screens.AppointRefusedNoPlayer}}
		}
		view = screens.AppointView{Office: t.def.Code, Place: govPlace(t.place), Player: govPlayerOf(candidate)}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.AppointConfirm(h.screen(meta, lang), view), nil
}

// Seat handles gov.seat: the confirmed appointment, once. The seat is the
// first vacant one of the office, under its lock.
func (h *AppointmentHandler) Seat(ctx context.Context, meta envelope.Metadata, req AppointRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	var candidate *application.Player
	if q, ok := ClassifyPlayerQuery(req.To); ok {
		found, err := h.search.Find(ctx, q)
		if err != nil && !isSentinel(err, application.ErrPlayerNotFound) {
			return nil, err
		}
		candidate = found
	}
	lang := meta.Language
	var (
		view     screens.AppointDoneView
		replayed bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}
		t, err := h.resolve(ctx, tx, req)
		if err != nil {
			return err
		}
		by, err := appointer(ctx, tx, t, p.ID)
		if err != nil {
			return err
		}
		if candidate == nil || candidate.Status != playerActive {
			return &appointRefusal{view: screens.AppointRefusalView{Kind: screens.AppointRefusedNoPlayer}}
		}
		seat, err := vacantSeat(ctx, tx, t)
		if err != nil {
			return err
		}
		now := h.now()
		_, after, err := application.AppointToOffice(ctx, tx, t.def.Code, t.place.ID, seat, candidate.ID, now)
		if err != nil {
			return err
		}
		view = screens.AppointDoneView{Office: t.def.Code, Place: govPlace(t.place), Player: govPlayerOf(candidate)}
		if after.TermEndsAt != nil {
			view.TermEndsIn = after.TermEndsAt.Sub(now)
		}
		return h.announce(ctx, tx, meta, t, candidate, p, by.OfficeCode, false)
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replayed {
		return screens.AppointRefusal(h.screen(meta, lang), screens.AppointRefusalView{Kind: screens.AppointRefusedNoSeat,
			Office: strings.TrimSpace(req.Office)}), nil
	}
	return screens.AppointDone(h.screen(meta, lang), view), nil
}

// announce tells the player seated or removed, and — for an appointment —
// the groups of the place.
func (h *AppointmentHandler) announce(ctx context.Context, tx application.Tx, meta envelope.Metadata, t appointTarget,
	who, by *application.Player, byOffice string, dismissed bool,
) error {
	var cities []string
	var err error
	if t.place.Kind == "country" {
		if cities, err = cityIDsOf(ctx, tx, t.place.ID); err != nil {
			return err
		}
	} else {
		all, err := tx.Diplomacy().Countries(ctx)
		if err != nil {
			return err
		}
		for _, c := range all {
			list, err := tx.Diplomacy().CitiesOf(ctx, c.ID)
			if err != nil {
				return err
			}
			for _, city := range list {
				if city.JurisdictionID == t.place.ID {
					cities = append(cities, city.ID)
				}
			}
		}
	}
	name := "appointed"
	if dismissed {
		name = "dismissed"
	}
	return appendDomainEvent(ctx, tx, meta, "governance", name, t.place.ID, map[string]any{
		"player_id": who.ID, "player_name": shownName(who), "office": t.def.Code, "place_kind": t.place.Kind,
		"place_code": t.place.Code, "place_name": t.place.Name, "by_name": shownName(by), "by_code": by.PublicCode,
		"by_office": byOffice, "city_ids": cities})
}

// Dismiss handles gov.dismiss: the removal shown for confirmation.
func (h *AppointmentHandler) Dismiss(ctx context.Context, meta envelope.Metadata, req AppointRequest) (*presenter.Response, error) {
	return h.dismiss(ctx, meta, req, false)
}

// Unseat handles gov.unseat: the confirmed removal, once.
func (h *AppointmentHandler) Unseat(ctx context.Context, meta envelope.Metadata, req AppointRequest) (*presenter.Response, error) {
	return h.dismiss(ctx, meta, req, true)
}

func (h *AppointmentHandler) dismiss(ctx context.Context, meta envelope.Metadata, req AppointRequest, confirm bool) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	lang := meta.Language
	var (
		confirmView screens.DismissView
		done        *screens.AppointDoneView
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		if confirm {
			key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
			fresh, err := tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
			if err != nil {
				return err
			}
			confirm = fresh
		}
		t, err := h.resolve(ctx, tx, req)
		if err != nil {
			return err
		}
		by, err := remover(ctx, tx, t, p.ID)
		if err != nil {
			return err
		}
		seatNo, err := strconv.Atoi(strings.TrimSpace(req.Seat))
		if err != nil || seatNo < 1 {
			return application.ErrOfficeNotFound
		}
		seat, err := tx.Governance().Seat(ctx, t.def.Code, t.place.ID, seatNo)
		if err != nil {
			return err
		}
		if seat.Vacant() {
			return application.ErrOfficeVacant
		}
		holder, err := tx.Players().GetByID(ctx, seat.HolderPlayerID)
		if err != nil {
			return err
		}
		confirmView = screens.DismissView{Office: t.def.Code, Place: govPlace(t.place), Seat: seatNo, Holder: govPlayerOf(holder)}
		if !confirm {
			return nil
		}
		if _, _, err := application.VacateOffice(ctx, tx, t.def.Code, t.place.ID, seatNo, h.now()); err != nil {
			return err
		}
		done = &screens.AppointDoneView{Office: t.def.Code, Place: govPlace(t.place), Player: govPlayerOf(holder), Dismissed: true}
		return h.announce(ctx, tx, meta, t, holder, p, by.OfficeCode, true)
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if done != nil {
		return screens.AppointDone(h.screen(meta, lang), *done), nil
	}
	return screens.DismissConfirm(h.screen(meta, lang), confirmView), nil
}
