package handlers

import (
	"context"
	stderrors "errors"
	"github.com/mrjvadi/torncity/internal/domain/budget"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/health"
	"github.com/mrjvadi/torncity/internal/domain/payment"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// HealthHandler serves health and hospitals
// (docs/adr/0023-health-missions-factions.md): the hospital screen — a
// player's health, their stay and who can treat them — a treatment at the
// city hospital or a player clinic, a clinic's desk for its owner and
// manager, and, from the scheduler, a stay's discharge.
//
// # Money and medicine
//
// A treatment is paid cash or card (payments.yml service hospital): to the
// city's treasury at the city hospital (hospital_fee), to the clinic's
// company treasury at a clinic (treatment_fee). A clinic treats only with
// medicine from its own warehouse, which it bought from a pharmaceutical
// lab's listings or made itself; the units used leave the world through the
// item journal (treatment). One treatment per stay; it takes a share of the
// stay's remaining time off, the better the doctor the larger.
type HealthHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	scale   gametime.Scale
	limits  bank.Limits

	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewHealthHandler wires the handler. A missing dependency or a game clock
// outside 1..gametime.MaxScale is a wiring mistake and panics.
func NewHealthHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, scale gametime.Scale, limits bank.Limits, idempotencyTTL time.Duration,
	now func() time.Time,
) *HealthHandler {
	if uow == nil || ids == nil || source == nil || cities == nil {
		panic("handlers: NewHealthHandler requires a unit of work, ids, content and cities")
	}
	if scale.Validate() != nil || idempotencyTTL <= 0 || limits.Max.Minor() <= 0 {
		panic("handlers: NewHealthHandler requires a game clock, an idempotency ttl and bank limits")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &HealthHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, scale: scale,
		limits: limits, idempotencyTTL: idempotencyTTL, now: now}
}

// HealthRequest is the payload of the health commands; which fields a
// command reads is its own. The names are those internal/gateway/routing
// gives the arguments.
type HealthRequest struct {
	// Provider is who treats: "city", or a clinic's public code.
	Provider string `json:"provider,omitempty"`
	Method   string `json:"method,omitempty"`
	Company  string `json:"company,omitempty"`
	Price    string `json:"price,omitempty"`
	On       string `json:"on,omitempty"`
}

// HealthScheduledRequest is the scheduler's payload for health.discharge.
type HealthScheduledRequest = CrimeScheduledRequest

func (h *HealthHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// healthRefusal carries a refused health command out of a unit of work.
type healthRefusal struct{ view screens.HealthRefusalView }

func (r *healthRefusal) Error() string { return "handlers: health refused: " + r.view.Kind }

func refuseHealth(kind string) *healthRefusal {
	return &healthRefusal{view: screens.HealthRefusalView{Kind: kind}}
}

// finish turns a refusal into its screen.
func (h *HealthHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	if err == nil {
		return nil, nil
	}
	c := h.screen(meta, lang)
	var r *healthRefusal
	if stderrors.As(err, &r) {
		return screens.HealthRefusal(c, r.view), nil
	}
	if v, ok := asDeclined(err, screens.PaymentDeclinedView{}); ok {
		return screens.PaymentDeclined(c, v), nil
	}
	return nil, err
}

// healthDef is the content's health section, or a refusal when it has none.
func healthDef(snap *content.Snapshot) (content.HealthDef, error) {
	def, ok := snap.Health()
	if !ok {
		return def, refuseHealth(screens.HealthRefusedNoHospitals)
	}
	return def, nil
}

// careKinds lists the kinds of company that treat patients.
func careKinds(snap *content.Snapshot) []string {
	var out []string
	for _, t := range snap.CompanyTypes() {
		if t.Care != nil {
			out = append(out, t.Code)
		}
	}
	return out
}

// medicineStock is what a clinic holds of the medicine category, by good in
// the content's order, and the good a treatment would use (units of it):
// the first one it has enough of. The caller holds the clinic's goods lock
// when it will take them.
func medicineStock(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.HealthDef,
	c application.Company,
) (total int64, use string, err error) {
	stacks, _, err := tx.Items().OrgHoldings(ctx, application.Org{Kind: application.OrgCompany, ID: c.ID}, application.HoldWarehouse)
	if err != nil {
		return 0, "", err
	}
	held := map[string]int64{}
	for _, s := range stacks {
		held[s.Item] += s.Qty
	}
	for _, it := range snap.Items() {
		if it.Category != def.Medicine {
			continue
		}
		total += held[it.Code]
		if use == "" && held[it.Code] >= int64(def.MedicineUnits) {
			use = it.Code
		}
	}
	return total, use, nil
}

// doctorLevel is the best medicine skill among a clinic's owner, manager
// and staff: its doctor.
func doctorLevel(ctx context.Context, tx application.Tx, c application.Company) (int, error) {
	staff, err := tx.Companies().Staff(ctx, c.ID)
	if err != nil {
		return 0, err
	}
	best := 0
	for _, id := range append([]string{c.OwnerID, c.ManagerID}, staffIDs(staff)...) {
		if id == "" {
			continue
		}
		s, err := tx.Skills().Get(ctx, id, "medicine")
		switch {
		case err == nil:
			best = max(best, s.Level)
		case !isSentinel(err, application.ErrSkillNotFound):
			return 0, err
		}
	}
	return best, nil
}

// remainingGame is the GAME time left of a stay, for the city's price.
func (h *HealthHandler) remainingGame(remaining time.Duration) time.Duration {
	if remaining <= 0 {
		return 0
	}
	return remaining * time.Duration(h.scale)
}

// option is one way to be treated, worked out.
type option struct {
	provider   string
	clinic     *application.Company
	price      int64
	reduction  int
	doctor     int
	saves      time.Duration
	medicine   string
	stock      int64
	open       bool
	treatsHere bool
}

// cityOption is the city hospital's offer for a stay with remaining left.
// subsidyBPS is what the city's budget takes off its hospital's price
// (docs/adr/0024-property-and-politics.md).
func (h *HealthHandler) cityOption(def content.HealthDef, remaining time.Duration, subsidyBPS int64) option {
	ch := def.CityHospital
	red := ch.Care.Care().Reduction(0)
	price := budget.Lower(health.CityPrice(ch.BasePrice, ch.PerHour, h.remainingGame(remaining)), subsidyBPS)
	return option{provider: application.ProviderCity, open: true, treatsHere: true,
		price: price, reduction: red, saves: remaining - health.Shorten(remaining, red)}
}

// clinicOption is a clinic's offer for a stay with remaining left.
func (h *HealthHandler) clinicOption(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.HealthDef,
	l application.ClinicListing, remaining time.Duration,
) (option, error) {
	c := l.Company
	o := option{provider: application.ProviderClinic, clinic: &c, price: l.Service.Price, open: l.Service.Open}
	tdef, _, ok := snap.CompanyType(c.TypeCode)
	if !ok || tdef.Care == nil {
		return o, nil
	}
	var err error
	if o.stock, o.medicine, err = medicineStock(ctx, tx, snap, def, c); err != nil {
		return o, err
	}
	if o.doctor, err = doctorLevel(ctx, tx, c); err != nil {
		return o, err
	}
	o.reduction = tdef.Care.Care().Reduction(o.doctor)
	o.saves = remaining - health.Shorten(remaining, o.reduction)
	o.treatsHere = o.open && o.medicine != ""
	return o, nil
}

func (o option) view(snap *content.Snapshot) screens.TreatOption {
	v := screens.TreatOption{Provider: o.provider, Price: o.price, Saves: o.saves, Doctor: o.doctor,
		Stock: o.stock, Open: o.open, CanTreat: o.treatsHere}
	if o.clinic != nil {
		v.Clinic = companyRef(snap, *o.clinic)
	}
	return v
}

// Hospital handles health.hospital: the player's health, their stay if
// they are in hospital, and who can treat them.
func (h *HealthHandler) Hospital(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.HospitalView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := healthDef(snap)
		if err != nil {
			return err
		}
		view, err = h.hospitalView(ctx, tx, snap, def, p, h.now())
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Hospital(h.screen(meta, lang), view), nil
}

// hospitalView reads everything the hospital screen shows.
func (h *HealthHandler) hospitalView(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.HealthDef,
	p *application.Player, now time.Time,
) (screens.HospitalView, error) {
	var view screens.HospitalView
	row, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, now))
	if err != nil {
		return view, err
	}
	hp, stay, _, err := currentHealth(ctx, tx, def, h.scale, *row, now)
	if err != nil {
		return view, err
	}
	view.Health, view.Max = hp, row.MaxHealth
	if stay != nil && !stay.Admitted(now) {
		// Its discharge is on its way: they are as good as out.
		view.Health, stay = max(hp, stay.HealthOut), nil
	}
	cityID := ""
	if stay != nil {
		cityID = stay.CityID
	} else if p.CityID != nil {
		cityID = *p.CityID
	}
	if stay == nil {
		if r := def.Rules(); r.RestPerHour > 0 && view.Health < view.Max {
			missing := view.Max - view.Health
			view.FullIn = h.scale.RealWait(time.Duration((int64(missing)*3600+int64(r.RestPerHour)-1)/int64(r.RestPerHour)) * time.Second)
		}
	}
	if cityID == "" {
		return view, nil
	}
	city, err := h.cities.ByID(ctx, cityID)
	if err != nil {
		return view, err
	}
	view.CityCode, view.City = city.Code, city.Name
	var remaining time.Duration
	if stay != nil {
		remaining = stay.EndsAt.Sub(now)
		view.InHospital, view.Cause, view.Remaining, view.EndsAt = true, stay.Cause, remaining, stay.EndsAt
		t, err := tx.Health().TreatmentOf(ctx, stay.ID)
		switch {
		case err == nil:
			view.Treated = true
			view.TreatedBy = screens.TreatOption{Provider: t.Provider}
			if t.CompanyID != "" {
				if c, err := tx.Companies().ByID(ctx, t.CompanyID); err == nil {
					view.TreatedBy.Clinic = companyRef(snap, *c)
				}
			}
		case !isSentinel(err, application.ErrTreatmentNotFound):
			return view, err
		}
		if !view.Treated {
			subsidy, err := budgetEffect(ctx, tx, stay.CityID, budget.EffectHospitalPrice)
			if err != nil {
				return view, err
			}
			v := h.cityOption(def, remaining, subsidy).view(snap)
			view.CityHospital = &v
		}
	}
	clinics, err := tx.Health().Clinics(ctx, city.ID, careKinds(snap))
	if err != nil {
		return view, err
	}
	for _, l := range clinics {
		o, err := h.clinicOption(ctx, tx, snap, def, l, remaining)
		if err != nil {
			return view, err
		}
		v := o.view(snap)
		v.CanTreat = v.CanTreat && stay != nil && !view.Treated
		view.Clinics = append(view.Clinics, v)
	}
	return view, nil
}

// Treat handles health.treat: a treatment, at the city hospital or a
// clinic. Without a method it shows the price and the ways to pay; with one
// it pays, uses the clinic's medicine, and takes its share off the stay —
// once, whatever is pressed twice.
func (h *HealthHandler) Treat(ctx context.Context, meta envelope.Metadata, req HealthRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		return h.Hospital(ctx, meta)
	}
	free := strings.TrimSpace(req.Method) == screens.MethodFree
	var (
		method payment.Method
		chosen bool
		err    error
	)
	if free {
		chosen = true
	} else if method, chosen, err = chosenMethod(req.Method); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		confirm  *screens.TreatConfirmView
		done     *screens.TreatedView
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := healthDef(snap)
		if err != nil {
			return err
		}
		if chosen {
			fresh, err := tx.Idempotency().Reserve(ctx, string(idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)),
				p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		now := h.now()
		// The stats row first, as every activity: a stay only changes under
		// it.
		if _, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, now)); err != nil {
			return err
		}
		active, err := hospitalised(ctx, tx, p.ID, now)
		if err != nil {
			return err
		}
		if active == nil {
			return refuseHealth(screens.HealthRefusedNotHospitalised)
		}
		stay, err := tx.Health().Stay(ctx, active.ID)
		if err != nil {
			return err
		}
		if _, err := tx.Health().TreatmentOf(ctx, stay.ID); err == nil {
			return refuseHealth(screens.HealthRefusedTreated)
		} else if !isSentinel(err, application.ErrTreatmentNotFound) {
			return err
		}
		remaining := stay.EndsAt.Sub(now)
		o, err := h.provider(ctx, tx, snap, def, provider, stay, remaining)
		if err != nil {
			return err
		}
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		plan := wallet.Plan(money.FromMinor(o.price), snap.Accepts(content.ServiceHospital))
		if free && o.price > 0 {
			// The price rose since the free confirmation was drawn: show it.
			chosen = false
		}
		if !chosen && o.price > 0 {
			choice := paymentChoice(plan, wallet)
			confirm = &screens.TreatConfirmView{Option: o.view(snap), Remaining: remaining,
				EndsAt: now.Add(remaining - o.saves), Payment: &choice}
			return nil
		}
		if !chosen {
			// A free treatment is confirmed with its own button.
			confirm = &screens.TreatConfirmView{Option: o.view(snap), Remaining: remaining, EndsAt: now.Add(remaining - o.saves)}
			return nil
		}
		t := application.Treatment{ID: h.ids.NewID(), StayID: stay.ID, PlayerID: p.ID, CityID: stay.CityID,
			Provider: o.provider, Price: o.price, Method: "free", DoctorLevel: o.doctor, ReductionBPS: o.reduction,
			CreatedAt: now}
		if o.price > 0 {
			if err := checkMethod(plan, method, wallet, "health.button.hospital", screens.AddrHospital); err != nil {
				return err
			}
			var to application.Account
			reason := application.ReasonHospitalFee
			if o.clinic != nil {
				reason = application.ReasonTreatmentFee
				to, err = tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, o.clinic.ID)
			} else {
				to, err = tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, stay.CityID)
			}
			if err != nil {
				return err
			}
			txID, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{
				Method: method, Accepted: plan.Accepted, Reason: reason,
				ReferenceType: application.HospitalReference, ReferenceID: stay.ID,
				To: []application.LedgerEntry{{AccountID: to.ID, Amount: money.FromMinor(o.price)}}, CreatedAt: now,
			})
			if err != nil {
				if stderrors.Is(err, application.ErrPaymentDeclined) {
					return declined(plan, wallet, "health.button.hospital", screens.AddrHospital)
				}
				return err
			}
			t.Method, t.LedgerTxID = string(method), txID
		}
		if o.clinic != nil {
			if err := tx.Items().Move(ctx, application.ItemMove{
				ID: h.ids.NewID(), Item: o.medicine, Qty: int64(def.MedicineUnits),
				FromOrg: application.Org{Kind: application.OrgCompany, ID: o.clinic.ID}, FromHolding: application.HoldWarehouse,
				Reason: application.ItemTreatment, ReferenceType: application.HospitalReference, ReferenceID: stay.ID, At: now,
			}); err != nil {
				if isSentinel(err, application.ErrNotEnoughItems) {
					return refuseHealth(screens.HealthRefusedNoMedicine)
				}
				return err
			}
			t.CompanyID, t.MedicineItem, t.MedicineUnits = o.clinic.ID, o.medicine, def.MedicineUnits
		}
		left := health.Shorten(remaining, o.reduction)
		ends := now.Add(max(left, time.Second))
		t.Saved = stay.EndsAt.Sub(ends)
		shortened := *stay
		shortened.EndsAt = ends
		actionID, err := scheduleDischarge(ctx, tx, h.ids, shortened, now)
		if err != nil {
			return err
		}
		if err := tx.Health().Reschedule(ctx, stay.ID, ends, actionID); err != nil {
			return err
		}
		if err := tx.Health().RecordTreatment(ctx, t); err != nil {
			if isSentinel(err, application.ErrAlreadyTreated) {
				return refuseHealth(screens.HealthRefusedTreated)
			}
			return err
		}
		// A health policy pays its share of what the treatment cost, once
		// per stay (docs/adr/0026).
		if t.Price > 0 {
			if _, err := ClaimHospital(ctx, tx, snap, h.ids, meta, p.ID, stay.ID, t.Price, now); err != nil {
				return err
			}
		}
		done = &screens.TreatedView{Option: o.view(snap), Paid: t.Price, Method: t.Method, Saved: t.Saved,
			Remaining: ends.Sub(now), EndsAt: ends}
		if o.clinic != nil {
			return appendDomainEvent(ctx, tx, meta, "health", "clinic_treated", t.ID, map[string]any{
				"player_id": o.clinic.OwnerID, "company_code": o.clinic.Code, "company_name": o.clinic.Name,
				"patient_name": shownName(p), "price": t.Price, "medicine": t.MedicineItem,
				"medicine_units": t.MedicineUnits, "saved_seconds": int64(t.Saved / time.Second),
			})
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	switch {
	case replayed:
		return h.Hospital(ctx, meta)
	case confirm != nil:
		return screens.TreatConfirm(h.screen(meta, lang), *confirm), nil
	case done != nil:
		return screens.Treated(h.screen(meta, lang), *done), nil
	}
	return nil, errors.Internal(stderrors.New("handlers: a treatment produced nothing"))
}

// provider works out the offer of the provider a press names, refusing one
// that cannot treat this stay.
func (h *HealthHandler) provider(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.HealthDef,
	provider string, stay *application.HospitalStay, remaining time.Duration,
) (option, error) {
	if provider == application.ProviderCity {
		subsidy, err := budgetEffect(ctx, tx, stay.CityID, budget.EffectHospitalPrice)
		if err != nil {
			return option{}, err
		}
		return h.cityOption(def, remaining, subsidy), nil
	}
	code := playercode.Normalize(provider)
	if !playercode.Valid(code) {
		return option{}, refuseHealth(screens.HealthRefusedNoClinic)
	}
	c, err := tx.Companies().ByCode(ctx, code)
	if isSentinel(err, application.ErrCompanyNotFound) {
		return option{}, refuseHealth(screens.HealthRefusedNoClinic)
	}
	if err != nil {
		return option{}, err
	}
	if c, err = tx.Companies().Lock(ctx, c.ID); err != nil {
		return option{}, err
	}
	tdef, _, ok := snap.CompanyType(c.TypeCode)
	if !ok || tdef.Care == nil || !c.Active() {
		return option{}, refuseHealth(screens.HealthRefusedNoClinic)
	}
	if c.CityID != stay.CityID {
		return option{}, refuseHealth(screens.HealthRefusedElsewhere)
	}
	if err := tx.Items().LockOrg(ctx, application.Org{Kind: application.OrgCompany, ID: c.ID}); err != nil {
		return option{}, err
	}
	svc, err := tx.Health().Clinic(ctx, c.ID)
	if err != nil {
		return option{}, err
	}
	o, err := h.clinicOption(ctx, tx, snap, def, application.ClinicListing{Company: *c, Service: svc}, remaining)
	if err != nil {
		return option{}, err
	}
	switch {
	case !o.open:
		return option{}, refuseHealth(screens.HealthRefusedClosed)
	case o.medicine == "":
		return option{}, refuseHealth(screens.HealthRefusedNoMedicine)
	}
	return o, nil
}

// Discharge ends a stay whose time is up. It arrives from the SCHEDULER and
// runs once: the key is derived from the stay and its action, the stats row
// is locked first, and only a stay still admitted and still ended by this
// action moves. The patient leaves with the health the stay promised, or
// more.
func (h *HealthHandler) Discharge(ctx context.Context, meta envelope.Metadata, req HealthScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	playerID, stayID, err := req.ids()
	if err != nil {
		return nil, err
	}
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		key := idempotency.Derive(playerID, meta.Command, stayID+":"+req.ActionID)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil || !fresh {
			return err
		}
		now := h.now()
		row, err := tx.Stats().EnsureDefaults(ctx, playerID, defaultStats(playerID, now))
		if err != nil {
			return err
		}
		s, err := tx.Health().Stay(ctx, stayID)
		if isSentinel(err, application.ErrStayNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if s.Status != application.StayAdmitted || (req.ActionID != "" && s.GameActionID != req.ActionID) {
			return nil
		}
		if now.Before(s.EndsAt) {
			return errors.Internal(stderrors.New("handlers: discharge before the stay ends"))
		}
		if err := tx.Health().Discharge(ctx, s.ID, now); err != nil {
			if isSentinel(err, application.ErrNotHospitalised) {
				return nil
			}
			return err
		}
		next := *row
		next.Health = min(max(row.Health, s.HealthOut), row.MaxHealth)
		if err := tx.Stats().Save(ctx, next); err != nil {
			return err
		}
		if err := tx.Health().SetRestSince(ctx, playerID, s.EndsAt); err != nil {
			return err
		}
		// The course resumes from the stay's end, unless jail still holds
		// them (docs/adr/0024).
		if err := resumeStudies(ctx, tx, h.ids, playerID, s.EndsAt); err != nil {
			return err
		}
		city, err := h.cities.ByID(ctx, s.CityID)
		if err != nil {
			return err
		}
		return appendDomainEvent(ctx, tx, meta, "health", "discharged", s.ID, map[string]any{
			"stay_id": s.ID, "player_id": playerID, "city_code": city.Code, "city_name": city.Name,
			"health": next.Health, "max_health": next.MaxHealth,
		})
	})
}

// clinicOf reads a company the player runs that treats patients.
func (h *HealthHandler) clinicOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
	code string, right company.Right,
) (*application.Company, content.CompanyTypeDef, error) {
	code = playercode.Normalize(code)
	if !playercode.Valid(code) {
		return nil, content.CompanyTypeDef{}, refuseHealth(screens.HealthRefusedNoClinic)
	}
	c, err := tx.Companies().ByCode(ctx, code)
	if isSentinel(err, application.ErrCompanyNotFound) {
		return nil, content.CompanyTypeDef{}, refuseHealth(screens.HealthRefusedNoClinic)
	}
	if err != nil {
		return nil, content.CompanyTypeDef{}, err
	}
	if c, err = tx.Companies().Lock(ctx, c.ID); err != nil {
		return nil, content.CompanyTypeDef{}, err
	}
	tdef, _, ok := snap.CompanyType(c.TypeCode)
	if !ok || tdef.Care == nil || !c.Active() {
		return nil, tdef, refuseHealth(screens.HealthRefusedNoClinic)
	}
	role := company.RoleOf(p.ID, c.OwnerID, c.ManagerID)
	if role == company.RoleNone || role.Check(right) != nil {
		return nil, tdef, refuseHealth(screens.HealthRefusedNotYours)
	}
	return c, tdef, nil
}

// Clinic handles health.clinic: a clinic's desk — its price, whether it
// takes patients, its medicine, its doctor and what it has earned.
func (h *HealthHandler) Clinic(ctx context.Context, meta envelope.Metadata, req HealthRequest) (*presenter.Response, error) {
	return h.clinicChange(ctx, meta, req, company.RightViewBooks, nil)
}

// Price handles health.price: the price a clinic charges, typed.
func (h *HealthHandler) Price(ctx context.Context, meta envelope.Metadata, req HealthRequest) (*presenter.Response, error) {
	raw := strings.TrimSpace(req.Price)
	if raw == "" {
		return h.Clinic(ctx, meta, req)
	}
	amount, err := bank.ParseAmount(raw)
	if err != nil && raw != "0" {
		return nil, application.ErrInvalidMoneyAmount
	}
	if amount.Minor() > h.limits.Max.Minor() {
		return nil, application.ErrAmountAboveMaximum.WithDetail("max", h.limits.Max.Minor())
	}
	return h.clinicChange(ctx, meta, req, company.RightSetPrice, func(s *application.ClinicService) {
		s.Price = amount.Minor()
	})
}

// Open handles health.open: a clinic takes patients, or stops.
func (h *HealthHandler) Open(ctx context.Context, meta envelope.Metadata, req HealthRequest) (*presenter.Response, error) {
	on := strings.TrimSpace(req.On) == "on"
	return h.clinicChange(ctx, meta, req, company.RightSetPrice, func(s *application.ClinicService) { s.Open = on })
}

// clinicChange reads a clinic's desk, applying a change first when there
// is one.
func (h *HealthHandler) clinicChange(ctx context.Context, meta envelope.Metadata, req HealthRequest, right company.Right,
	apply func(*application.ClinicService),
) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.ClinicDeskView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := healthDef(snap)
		if err != nil {
			return err
		}
		c, tdef, err := h.clinicOf(ctx, tx, snap, p, req.Company, right)
		if err != nil {
			return err
		}
		svc, err := tx.Health().Clinic(ctx, c.ID)
		if err != nil {
			return err
		}
		if apply != nil {
			apply(&svc)
			svc.UpdatedAt = h.now()
			if err := tx.Health().SaveClinic(ctx, svc); err != nil {
				return err
			}
		}
		stock, use, err := medicineStock(ctx, tx, snap, def, *c)
		if err != nil {
			return err
		}
		level, err := doctorLevel(ctx, tx, *c)
		if err != nil {
			return err
		}
		paid, treated, err := tx.Health().ClinicEarnings(ctx, c.ID)
		if err != nil {
			return err
		}
		view = screens.ClinicDeskView{Ref: companyRef(snap, *c), Price: svc.Price, Open: svc.Open, Stock: stock,
			Stocked: use != "", Units: def.MedicineUnits, Doctor: level, ReductionBPS: tdef.Care.Care().Reduction(level),
			Treated: treated, Earned: paid}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.ClinicDesk(h.screen(meta, lang), view), nil
}
