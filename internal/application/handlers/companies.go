package handlers

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/payment"
	"github.com/mrjvadi/torncity/internal/domain/place"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The company levers (configs/content/governance.yml), read only through
// the policy resolver. The minimum wage (leverMinimumWage) and the sales tax
// (LeverSalesTax) are the city's own, shared with work and the shops.
const (
	// LeverCompanyRegistration scales a kind of business's founding fee,
	// in percent.
	LeverCompanyRegistration = "city.company_registration"
	// LeverCorporateTax is the tax on profits taken out of a company, bps.
	LeverCorporateTax = "city.corporate_tax"
)

// companyReference is ledger_entries.reference_type of every company money
// movement; the reference id is the company's.
const companyReference = "companies"

// CompanyRules is the tuning of companies (config company, and the bank's
// amount bounds for money put in or taken out).
type CompanyRules struct {
	// Period is one settlement, GAME time.
	Period            time.Duration
	MaxPerPlayer      int
	NameMin, NameMax  int
	FoundingShares    int64
	InsolvencyPeriods int
	NPCCityPeriodCap  int64
	MaxOpenings       int
	PriceStepBPS      int
	// Citizens is the tuning of citizen labour: the openings no player has
	// taken are worked by the city's citizens, at most CitizenLabourShareBPS
	// of its NPC population at once.
	Citizens              company.CitizenRules
	CitizenLabourShareBPS int
	Limits                bank.Limits
}

// CompaniesHandler serves player companies (docs/adr/0020-companies.md):
// the city's registry and a company's public page, founding one at city
// hall, running it — money in and out, prices, openings, staff, a manager,
// closing — applying to one, and, from the scheduler, the settlement of each
// city's companies once per period: NPC revenue in, upkeep out.
//
// # Money
//
// A company's treasury is a company_treasury account. The founder pays the
// registration fee to the city (company_registration); money goes in from a
// player's cash or card (company_deposit) and out to the owner's bank, taxed
// (company_withdrawal + corporate_tax); a shift worked for it is paid from it
// (company_wage, in JobsHandler); the city's population pays it
// (npc_purchase, a faucet bounded per city and period) and it pays its upkeep
// (maintenance, a drain). Nothing it chooses to pay may touch the wages its
// running shifts reserved, so it never owes a worker money it has not got.
type CompaniesHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	policy  application.PolicyReader
	search  application.PlayerSearch
	scale   gametime.Scale
	rules   CompanyRules

	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewCompaniesHandler wires the handler.
func NewCompaniesHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, policy application.PolicyReader, search application.PlayerSearch,
	scale gametime.Scale, rules CompanyRules, idempotencyTTL time.Duration, now func() time.Time,
) *CompaniesHandler {
	if source == nil || cities == nil || policy == nil || search == nil || ids == nil {
		panic("handlers: NewCompaniesHandler requires content, cities, a policy reader, a player search and ids")
	}
	if scale.Validate() != nil || idempotencyTTL <= 0 || rules.Period <= 0 || rules.MaxPerPlayer < 1 ||
		rules.NameMin < 1 || rules.NameMax < rules.NameMin || rules.FoundingShares < 1 || rules.InsolvencyPeriods < 1 ||
		rules.MaxOpenings < 1 || rules.Limits.Min.Minor() <= 0 || rules.Limits.Max.Minor() < rules.Limits.Min.Minor() {
		panic("handlers: NewCompaniesHandler requires a game clock, an idempotency ttl and valid company rules")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &CompaniesHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, policy: policy,
		search: search, scale: scale, rules: rules, idempotencyTTL: idempotencyTTL, now: now}
}

// CompanyRequest names a company and what to do with it; which fields a
// command reads is its own. The names are those internal/gateway/routing
// gives the arguments.
type CompanyRequest struct {
	Company   string `json:"company,omitempty"`
	Code      string `json:"code,omitempty"`
	Type      string `json:"type,omitempty"`
	Method    string `json:"method,omitempty"`
	Name      string `json:"name,omitempty"`
	Amount    string `json:"amount,omitempty"`
	Price     string `json:"price,omitempty"`
	Career    string `json:"career,omitempty"`
	Wage      string `json:"wage,omitempty"`
	No        string `json:"no,omitempty"`
	Positions string `json:"positions,omitempty"`
	Verdict   string `json:"verdict,omitempty"`
	Player    string `json:"player,omitempty"`
	To        string `json:"to,omitempty"`
	On        string `json:"on,omitempty"`
	Confirm   string `json:"confirm,omitempty"`
	Page      string `json:"page,omitempty"`
}

// code is the company a request names, as a code is written.
func (r CompanyRequest) code() string {
	if r.Company != "" {
		return playercode.Normalize(r.Company)
	}
	return playercode.Normalize(r.Code)
}

func (h *CompaniesHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// companyRefusal carries a refused company command out of a unit of work.
type companyRefusal struct{ view screens.CompanyRefusalView }

func (r *companyRefusal) Error() string { return "handlers: company refused: " + r.view.Kind }

// refuseCompany is a refusal about c (nil when there is none yet).
func refuseCompany(kind string, c *application.Company, snap *content.Snapshot) *companyRefusal {
	r := &companyRefusal{view: screens.CompanyRefusalView{Kind: kind}}
	if c != nil {
		r.view.Ref = companyRef(snap, *c)
	}
	return r
}

func asCompanyRefusal(err error) (screens.CompanyRefusalView, bool) {
	var r *companyRefusal
	if stderrors.As(err, &r) {
		return r.view, true
	}
	return screens.CompanyRefusalView{}, false
}

// finish turns a refusal into its screen.
func (h *CompaniesHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	if err == nil {
		return nil, nil
	}
	c := h.screen(meta, lang)
	if v, ok := asCompanyRefusal(err); ok {
		return screens.CompanyRefusal(c, v), nil
	}
	if v, ok := asNotHere(err); ok {
		return screens.NotHere(c, v), nil
	}
	if r, ok := asRefusal(err); ok {
		return screens.Refusal(c, r.view), nil
	}
	if v, ok := asDeclined(err, screens.PaymentDeclinedView{}); ok {
		return screens.PaymentDeclined(c, v), nil
	}
	return nil, err
}

// reserve takes the idempotency key of a command that writes: a typed
// command or a press, keyed on its update.
func (h *CompaniesHandler) reserve(ctx context.Context, tx application.Tx, playerID string, meta envelope.Metadata) (bool, error) {
	key := idempotency.Derive(playerID, meta.RequestID, meta.IdempotencyKey)
	return tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

// companyRef names a company for a screen.
func companyRef(snap *content.Snapshot, c application.Company) screens.CompanyRef {
	ref := screens.CompanyRef{Code: c.Code, Name: c.Name, Type: screens.Named{Code: c.TypeCode, Name: c.TypeCode}}
	if def, _, ok := snap.CompanyType(c.TypeCode); ok {
		ref.Type.Name = def.Name
	}
	return ref
}

// govPlayerOf names a player for a screen.
func govPlayerOf(p *application.Player) screens.GovPlayer {
	if p == nil {
		return screens.GovPlayer{}
	}
	return screens.GovPlayer{Name: shownName(p), Code: p.PublicCode}
}

// playerNamed reads a player to name on a screen; an unknown one is blank.
func playerNamed(ctx context.Context, tx application.Tx, id string) (screens.GovPlayer, error) {
	if id == "" {
		return screens.GovPlayer{}, nil
	}
	p, err := tx.Players().GetByID(ctx, id)
	if isSentinel(err, application.ErrPlayerNotFound) {
		return screens.GovPlayer{}, nil
	}
	if err != nil {
		return screens.GovPlayer{}, err
	}
	return govPlayerOf(p), nil
}

// lever reads one of a city's levers through the resolver.
func (h *CompaniesHandler) lever(ctx context.Context, city application.City, code string) (int64, error) {
	v, err := h.policy.Get(ctx, city.JurisdictionID, code)
	if err != nil {
		return 0, err
	}
	return v.Value, nil
}

// periodWait is one period on the wall clock.
func (h *CompaniesHandler) periodWait() time.Duration { return h.scale.RealWait(h.rules.Period) }

// byCode reads a company by its public code; lock takes its row lock.
func (h *CompaniesHandler) byCode(ctx context.Context, tx application.Tx, snap *content.Snapshot, code string, lock bool) (*application.Company, error) {
	if !playercode.Valid(code) {
		return nil, refuseCompany(screens.CompanyRefusedNotFound, nil, snap)
	}
	c, err := tx.Companies().ByCode(ctx, code)
	if isSentinel(err, application.ErrCompanyNotFound) {
		return nil, refuseCompany(screens.CompanyRefusedNotFound, nil, snap)
	}
	if err != nil || !lock {
		return c, err
	}
	return tx.Companies().Lock(ctx, c.ID)
}

// managed reads (and locks) an active company the player may use right on.
func (h *CompaniesHandler) managed(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
	code string, right company.Right,
) (*application.Company, company.Role, error) {
	c, err := h.byCode(ctx, tx, snap, code, true)
	if err != nil {
		return nil, company.RoleNone, err
	}
	role := company.RoleOf(p.ID, c.OwnerID, c.ManagerID)
	if role == company.RoleNone {
		return nil, role, refuseCompany(screens.CompanyRefusedNotAllowed, c, snap)
	}
	if !c.Active() {
		return nil, role, refuseCompany(screens.CompanyRefusedDissolved, c, snap)
	}
	if err := role.Check(right); err != nil {
		return nil, role, refuseCompany(screens.CompanyRefusedNotAllowed, c, snap)
	}
	return c, role, nil
}

// companyBooks reads a company's money: its treasury, the wages reserved by
// its running shifts, and its debt. Read under the company's lock.
func companyBooks(ctx context.Context, tx application.Tx, c application.Company) (company.Books, application.Account, error) {
	acct, err := tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, c.ID)
	if err != nil {
		return company.Books{}, acct, err
	}
	reserved, err := tx.Companies().Reserved(ctx, c.ID)
	if err != nil {
		return company.Books{}, acct, err
	}
	return company.Books{Balance: acct.Balance, Reserved: money.FromMinor(reserved), Debt: money.FromMinor(c.Debt)}, acct, nil
}

// companyType reads a company's kind from the content, or a fault: a stored
// company of a kind the content dropped is a content load that should have
// been refused.
func companyType(snap *content.Snapshot, c application.Company) (content.CompanyTypeDef, company.Type, error) {
	def, t, ok := snap.CompanyType(c.TypeCode)
	if !ok {
		return def, t, errors.Internal(stderrors.New("handlers: stored company of a kind the content does not have: " + c.TypeCode))
	}
	return def, t, nil
}

// appendCompanyEvent writes a company event to the outbox.
func appendCompanyEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata, name, aggregateID string, payload map[string]any) error {
	ev, err := events.New("company."+name, "company", aggregateID, payload)
	if err != nil {
		return err
	}
	return tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID: ev.ID, Subject: subjects.Event("company", name), Metadata: meta, Payload: ev.Payload,
	})
}

// cityOf is the city a player stands in, or nil while travelling or
// nowhere.
func (h *CompaniesHandler) cityOf(ctx context.Context, tx application.Tx, p *application.Player) (*application.City, error) {
	if p.CityID == nil || *p.CityID == "" {
		return nil, nil
	}
	if _, err := tx.Travels().Active(ctx, p.ID); err == nil {
		return nil, nil
	} else if !isSentinel(err, application.ErrNoActiveTravel) {
		return nil, err
	}
	return h.cities.ByID(ctx, *p.CityID)
}

// List handles company.list: the companies of the player's city.
func (h *CompaniesHandler) List(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CompanyRegistryView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		mine, err := tx.Companies().Of(ctx, p.ID)
		if err != nil {
			return err
		}
		view.Mine = len(mine)
		city, err := h.cityOf(ctx, tx, p)
		if err != nil {
			return err
		}
		if city == nil {
			view.NoCity = true
			return nil
		}
		view.CityCode, view.City = city.Code, city.Name
		list, err := tx.Companies().InCity(ctx, city.ID)
		if err != nil {
			return err
		}
		for _, c := range list {
			line, err := h.line(ctx, tx, snap, c, p.ID)
			if err != nil {
				return err
			}
			view.Companies = append(view.Companies, line)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return screens.CompanyRegistry(h.screen(meta, lang), view), nil
}

// line is one company of a list.
func (h *CompaniesHandler) line(ctx context.Context, tx application.Tx, snap *content.Snapshot, c application.Company, viewer string) (screens.CompanyLine, error) {
	staff, err := tx.Companies().Staff(ctx, c.ID)
	if err != nil {
		return screens.CompanyLine{}, err
	}
	openings, err := tx.Companies().Openings(ctx, c.ID)
	if err != nil {
		return screens.CompanyLine{}, err
	}
	free := 0
	for _, o := range openings {
		free += o.Free()
	}
	_, lastErr := tx.Companies().LastPeriod(ctx, c.ID)
	rated := lastErr == nil
	if lastErr != nil && !isSentinel(lastErr, application.ErrNoCompanyPeriod) {
		return screens.CompanyLine{}, lastErr
	}
	return screens.CompanyLine{
		Ref: companyRef(snap, c), Stars: company.Stars(c.RatingBPS), Rated: rated, Staff: len(staff), Openings: free,
		Mine: company.RoleOf(viewer, c.OwnerID, c.ManagerID) != company.RoleNone,
	}, nil
}

// View handles company.view: a company's public page.
func (h *CompaniesHandler) View(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CompanyPageView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		c, err := h.byCode(ctx, tx, snap, req.code(), false)
		if err != nil {
			return err
		}
		view, err = h.page(ctx, tx, snap, *c, p.ID)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.CompanyPage(h.screen(meta, lang), view), nil
}

// page builds a company's public page.
func (h *CompaniesHandler) page(ctx context.Context, tx application.Tx, snap *content.Snapshot, c application.Company, viewer string) (screens.CompanyPageView, error) {
	city, err := h.cities.ByID(ctx, c.CityID)
	if err != nil {
		return screens.CompanyPageView{}, err
	}
	v := screens.CompanyPageView{Ref: companyRef(snap, c), CityCode: city.Code, City: city.Name,
		Stars: company.Stars(c.RatingBPS), Dissolved: !c.Active(),
		CanManage: company.RoleOf(viewer, c.OwnerID, c.ManagerID) != company.RoleNone}
	if def, t, ok := snap.CompanyType(c.TypeCode); ok {
		v.Place, v.MaxStaff = placeNamed(snap, def.Place), t.MaxStaff
	}
	if v.Owner, err = playerNamed(ctx, tx, c.OwnerID); err != nil {
		return v, err
	}
	if c.ManagerID != "" {
		m, err := playerNamed(ctx, tx, c.ManagerID)
		if err != nil {
			return v, err
		}
		v.Manager = &m
	}
	staff, err := tx.Companies().Staff(ctx, c.ID)
	if err != nil {
		return v, err
	}
	v.Staff = len(staff)
	if _, err := tx.Companies().LastPeriod(ctx, c.ID); err == nil {
		v.Rated = true
	} else if !isSentinel(err, application.ErrNoCompanyPeriod) {
		return v, err
	}
	if err := publicProducts(ctx, tx, snap, c, &v); err != nil {
		return v, err
	}
	if c.Active() {
		openings, err := tx.Companies().Openings(ctx, c.ID)
		if err != nil {
			return v, err
		}
		for _, o := range openings {
			if o.Free() == 0 {
				continue
			}
			v.Openings = append(v.Openings, openingLine(snap, o))
		}
	}
	return v, nil
}

// openingLine is an opening for a screen.
func openingLine(snap *content.Snapshot, o application.CompanyOpening) screens.CompanyOpeningLine {
	line := screens.CompanyOpeningLine{No: o.No, Wage: o.Wage, Positions: o.Positions, Filled: o.Filled,
		Job: screens.JobRef{CareerCode: o.CareerCode, CareerName: o.CareerCode}}
	if def, ok := snap.CareerDef(o.CareerCode); ok {
		line.Job = jobRef(def, 0)
	}
	return line
}

// Register handles company.register: the kinds of business a player may
// found in their city, with what each costs there.
func (h *CompaniesHandler) Register(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CompanyTypesView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		city, err := h.cityOf(ctx, tx, p)
		if err != nil {
			return err
		}
		if city == nil {
			view.NoCity = true
			return nil
		}
		view.CityCode, view.City, view.Max = city.Code, city.Name, h.rules.MaxPerPlayer
		if view.Owned, err = tx.Companies().OwnedCount(ctx, p.ID); err != nil {
			return err
		}
		multiplier, err := h.lever(ctx, *city, LeverCompanyRegistration)
		if err != nil {
			return err
		}
		cmap := snap.CityMap(city.Code)
		for _, def := range snap.CompanyTypes() {
			if len(cmap.Places) > 0 {
				if _, ok := cmap.Find(def.Place); !ok {
					continue
				}
			}
			fee, err := company.RegistrationFee(def.Type(), int(multiplier)*100)
			if err != nil {
				return errors.Internal(err)
			}
			view.Types = append(view.Types, screens.CompanyTypeLine{
				Type: screens.Named{Code: def.Code, Name: def.Name}, Fee: fee.Minor(), Upkeep: def.Upkeep,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return screens.CompanyTypes(h.screen(meta, lang), view), nil
}

// foundable is what founding a kind of business in the player's city needs,
// worked out once for the type screen and for the founding itself.
type foundable struct {
	city    *application.City
	def     content.CompanyTypeDef
	ty      company.Type
	fee     money.Amount
	blocked string
	where   whereabouts
}

// foundability works out whether p may found a company of kind code here.
func (h *CompaniesHandler) foundability(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, code string) (foundable, error) {
	var f foundable
	def, ty, ok := snap.CompanyType(strings.ToLower(strings.TrimSpace(code)))
	if !ok {
		return f, refuseCompany(screens.CompanyRefusedNoPlace, nil, snap)
	}
	f.def, f.ty = def, ty
	city, err := h.cityOf(ctx, tx, p)
	if err != nil {
		return f, err
	}
	if city == nil {
		f.blocked = screens.CompanyBlockedNoCity
		return f, nil
	}
	f.city = city
	if f.where, err = locate(ctx, tx, h.cities, snap, p); err != nil {
		return f, err
	}
	if f.where.placed() {
		if _, ok := f.where.cmap.Find(def.Place); !ok {
			f.blocked = screens.CompanyBlockedNoPlace
			return f, nil
		}
	}
	owned, err := tx.Companies().OwnedCount(ctx, p.ID)
	if err != nil {
		return f, err
	}
	if owned >= h.rules.MaxPerPlayer {
		f.blocked = screens.CompanyBlockedLimit
	}
	multiplier, err := h.lever(ctx, *city, LeverCompanyRegistration)
	if err != nil {
		return f, err
	}
	if f.fee, err = company.RegistrationFee(ty, int(multiplier)*100); err != nil {
		return f, errors.Internal(err)
	}
	return f, nil
}

// Type handles company.type: one kind of business, what it costs in the
// player's city, and — at city hall — the buttons that found one; away from
// it, the walk there.
func (h *CompaniesHandler) Type(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CompanyTypeView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		view, err = h.typeView(ctx, tx, snap, p, req.Type)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.CompanyTypeDetail(h.screen(meta, lang), view), nil
}

func (h *CompaniesHandler) typeView(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, code string) (screens.CompanyTypeView, error) {
	f, err := h.foundability(ctx, tx, snap, p, code)
	if err != nil {
		return screens.CompanyTypeView{}, err
	}
	v := screens.CompanyTypeView{
		Type: screens.Named{Code: f.def.Code, Name: f.def.Name}, Place: placeNamed(snap, f.def.Place),
		Fee: f.fee.Minor(), Upkeep: f.def.Upkeep, MaxStaff: f.def.MaxStaff, Period: h.periodWait(),
		NameMin: h.rules.NameMin, NameMax: h.rules.NameMax, Blocked: f.blocked, Max: h.rules.MaxPerPlayer,
	}
	for _, c := range f.def.Careers {
		if def, ok := snap.CareerDef(c); ok {
			v.Careers = append(v.Careers, jobRef(def, 0))
		}
	}
	if f.city != nil {
		v.CityCode, v.City = f.city.Code, f.city.Name
	}
	if f.blocked != "" {
		return v, nil
	}
	if way := wayTo(f.where, snap, place.ServiceCityHall, h.scale); way != nil {
		v.Way = way
		return v, nil
	}
	wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
	if err != nil {
		return v, err
	}
	choice := paymentChoice(wallet.Plan(f.fee, snap.Accepts(content.ServiceCompany)), wallet)
	v.Payment = &choice
	return v, nil
}

// Found handles company.found: registering a company at city hall, the fee
// paid to the city by the method chosen, the name typed by the founder.
//
// Everything commits together or not at all: the company, its founder's
// shares, its treasury account, the fee, its city's settlement clock
// started if it was idle, and the event the city's groups read. A second
// delivery of the same update is a replay; two names typed in a row found
// one company each only while the player may own more, and a name taken in
// the city is refused by the database whatever raced for it.
func (h *CompaniesHandler) Found(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Name) == "" {
		return h.Type(ctx, meta, req)
	}
	method, chosen, err := chosenMethod(req.Method)
	if err != nil {
		return nil, err
	}
	if !chosen {
		return h.Type(ctx, meta, req)
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		founded  screens.CompanyFoundedView
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}
		now := h.now()
		if err := RefuseDetained(ctx, tx, p.ID, now); err != nil {
			return err
		}
		f, err := h.foundability(ctx, tx, snap, p, req.Type)
		if err != nil {
			return err
		}
		switch f.blocked {
		case screens.CompanyBlockedLimit:
			r := refuseCompany(screens.CompanyRefusedLimit, nil, snap)
			r.view.Max = int64(h.rules.MaxPerPlayer)
			return r
		case screens.CompanyBlockedNoPlace:
			return refuseCompany(screens.CompanyRefusedNoPlace, nil, snap)
		case screens.CompanyBlockedNoCity:
			return application.ErrCityNotFound
		}
		if err := needService(f.where, snap, place.ServiceCityHall, h.scale, now); err != nil {
			return thenFor(err, "company.type", f.def.Code)
		}
		name, err := company.CheckName(req.Name, company.NameRules{
			MinRunes: h.rules.NameMin, MaxRunes: h.rules.NameMax, Reserved: snap.CompanyReservedNames(),
		})
		if err != nil {
			return nameRefusal(err, h.rules, snap)
		}
		code, err := h.freeCode(ctx, tx)
		if err != nil {
			return err
		}
		c := application.Company{
			ID: h.ids.NewID(), Code: code, Name: name, NameKey: company.NameKey(name), TypeCode: f.def.Code,
			CityID: f.city.ID, OwnerID: p.ID, Status: application.CompanyActive, PriceBPS: 10000,
			TotalShares: h.rules.FoundingShares, RegistrationFee: f.fee.Minor(), ContentVersion: snap.Version(),
			FoundedAt: now, UpdatedAt: now,
		}
		if err := tx.Companies().Create(ctx, c, application.CompanyShareholder{
			CompanyID: c.ID, PlayerID: p.ID, Shares: h.rules.FoundingShares, AcquiredAt: now,
		}); err != nil {
			if isSentinel(err, application.ErrCompanyNameTaken) {
				return refuseCompany(screens.CompanyRefusedNameTaken, nil, snap)
			}
			return err
		}
		if !f.fee.IsZero() {
			wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
			if err != nil {
				return err
			}
			plan := wallet.Plan(f.fee, snap.Accepts(content.ServiceCompany))
			back := []string{screens.AddrCompanyType, f.def.Code}
			if err := checkMethod(plan, method, wallet, "company.button.type_back", back...); err != nil {
				return err
			}
			treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, f.city.ID)
			if err != nil {
				return err
			}
			txID, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{
				Method: method, Accepted: plan.Accepted, Reason: application.ReasonCompanyRegistration,
				ReferenceType: companyReference, ReferenceID: c.ID,
				To: []application.LedgerEntry{{AccountID: treasury.ID, Amount: f.fee}}, CreatedAt: now,
			})
			if err != nil {
				if stderrors.Is(err, application.ErrPaymentDeclined) {
					return declined(plan, wallet, "company.button.type_back", back...)
				}
				return err
			}
			c.RegistrationTransactionID = txID
			if err := tx.Companies().Save(ctx, c); err != nil {
				return err
			}
		}
		// The treasury exists from the first moment, at zero.
		if _, err := tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, c.ID); err != nil {
			return err
		}
		if err := h.startClock(ctx, tx, f.city.ID, now); err != nil {
			return err
		}
		founded = screens.CompanyFoundedView{Ref: companyRef(snap, c), CityCode: f.city.Code, City: f.city.Name,
			Fee: f.fee.Minor(), Method: string(method)}
		return appendCompanyEvent(ctx, tx, meta, "founded", c.ID, map[string]any{
			"company_id": c.ID, "code": c.Code, "name": c.Name, "type": c.TypeCode, "type_name": f.def.Name,
			"city_id": f.city.ID, "player_id": p.ID, "player_name": shownName(p), "fee": f.fee.Minor(),
			"method": string(method), "content_version": snap.Version(),
		})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replayed {
		return h.Mine(ctx, meta)
	}
	return screens.CompanyFounded(h.screen(meta, lang), founded), nil
}

// nameRefusal is the refusal of a name the rules turned down.
func nameRefusal(err error, rules CompanyRules, snap *content.Snapshot) error {
	switch {
	case stderrors.Is(err, company.ErrNameLength):
		r := refuseCompany(screens.CompanyRefusedNameLength, nil, snap)
		r.view.Min, r.view.Max = int64(rules.NameMin), int64(rules.NameMax)
		return r
	case stderrors.Is(err, company.ErrNameReserved):
		return refuseCompany(screens.CompanyRefusedNameReserved, nil, snap)
	case stderrors.Is(err, company.ErrNameCharset):
		return refuseCompany(screens.CompanyRefusedNameCharset, nil, snap)
	}
	return errors.Internal(err)
}

// freeCode draws a public code no company has.
func (h *CompaniesHandler) freeCode(ctx context.Context, tx application.Tx) (string, error) {
	for range 16 {
		code, err := playercode.New()
		if err != nil {
			return "", errors.Internal(err)
		}
		if _, err := tx.Companies().ByCode(ctx, code); isSentinel(err, application.ErrCompanyNotFound) {
			return code, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.Internal(stderrors.New("handlers: no free company code after 16 draws"))
}

// Mine handles company.mine: the player's companies — straight to the one
// they run when there is only one.
func (h *CompaniesHandler) Mine(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view screens.CompanyMineView
		only string
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		list, err := tx.Companies().Of(ctx, p.ID)
		if err != nil {
			return err
		}
		if len(list) == 1 {
			only = list[0].Code
			return nil
		}
		for _, c := range list {
			line, err := h.line(ctx, tx, snap, c, p.ID)
			if err != nil {
				return err
			}
			view.Companies = append(view.Companies, line)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if only != "" {
		return h.Manage(ctx, meta, CompanyRequest{Company: only})
	}
	return screens.CompanyMine(h.screen(meta, lang), view), nil
}

// Manage handles company.manage: the owner's or the manager's screen.
func (h *CompaniesHandler) Manage(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	return h.manageWith(ctx, meta, req.code(), nil)
}

// manageWith renders the management screen with a line about what was just
// done.
func (h *CompaniesHandler) manageWith(ctx context.Context, meta envelope.Metadata, code string, notice *screens.CompanyNotice) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CompanyManageView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		c, role, err := h.managed(ctx, tx, snap, p, code, company.RightViewBooks)
		if err != nil {
			return err
		}
		view, err = h.manageView(ctx, tx, snap, *c, role)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	view.Notice = notice
	return screens.CompanyManage(h.screen(meta, lang), view), nil
}

func (h *CompaniesHandler) manageView(ctx context.Context, tx application.Tx, snap *content.Snapshot, c application.Company, role company.Role) (screens.CompanyManageView, error) {
	city, err := h.cities.ByID(ctx, c.CityID)
	if err != nil {
		return screens.CompanyManageView{}, err
	}
	_, ty, err := companyType(snap, c)
	if err != nil {
		return screens.CompanyManageView{}, err
	}
	books, _, err := companyBooks(ctx, tx, c)
	if err != nil {
		return screens.CompanyManageView{}, err
	}
	tax, err := h.lever(ctx, *city, LeverCorporateTax)
	if err != nil {
		return screens.CompanyManageView{}, err
	}
	v := screens.CompanyManageView{
		Ref: companyRef(snap, c), CityCode: city.Code, City: city.Name, Owner: role == company.RoleOwner,
		Balance: books.Balance.Minor(), Reserved: books.Reserved.Minor(), Available: books.Available().Minor(),
		Debt: c.Debt, Upkeep: ty.Upkeep.Minor(), Arrears: c.Arrears, Grace: h.rules.InsolvencyPeriods,
		PriceBPS: c.PriceBPS, PriceMin: ty.PriceMinBPS, PriceMax: ty.PriceMaxBPS, PriceStep: h.rules.PriceStepBPS,
		MaxStaff: ty.MaxStaff, AutoAccept: c.AutoAccept, TaxBPS: int(tax),
	}
	if c.ManagerID != "" {
		m, err := playerNamed(ctx, tx, c.ManagerID)
		if err != nil {
			return v, err
		}
		v.Manager = &m
	}
	staff, err := tx.Companies().Staff(ctx, c.ID)
	if err != nil {
		return v, err
	}
	v.Staff = len(staff)
	openings, err := tx.Companies().Openings(ctx, c.ID)
	if err != nil {
		return v, err
	}
	v.Openings = len(openings)
	pending, err := tx.Companies().Pending(ctx, c.ID)
	if err != nil {
		return v, err
	}
	v.Pending = len(pending)
	last, err := tx.Companies().LastPeriod(ctx, c.ID)
	switch {
	case err == nil:
		v.Last = periodSummary(*last)
	case !isSentinel(err, application.ErrNoCompanyPeriod):
		return v, err
	}
	next, err := tx.Companies().NextSettlement(ctx, c.CityID)
	if err != nil {
		return v, err
	}
	if next != nil {
		v.NextAt, v.NextIn = *next, max(next.Sub(h.now()), 0)
	}
	return v, nil
}

// periodSummary is a settled period for a screen.
func periodSummary(p application.CompanyPeriod) *screens.CompanyPeriodSummary {
	return &screens.CompanyPeriodSummary{
		Revenue: p.Revenue, SalesTax: p.SalesTax, Wages: p.Wages, Upkeep: p.UpkeepDue, UpkeepPaid: p.UpkeepPaid,
		Debt: p.Debt, Shifts: p.Shifts, QualityBPS: p.QualityBPS, Sold: p.SoldUnits, Wanted: p.WantedUnits,
		Capacity: p.CapacityUnits, Balance: p.BalanceAfter,
	}
}

// parseCompanyAmount reads and bounds a typed amount.
func (h *CompaniesHandler) parseAmount(raw string, snap *content.Snapshot, c *application.Company) (money.Amount, error) {
	amount, err := bank.ParseAmount(raw)
	if err != nil {
		r := refuseCompany(screens.CompanyRefusedInvalidAmount, c, snap)
		return money.Amount{}, r
	}
	switch err := h.rules.Limits.Check(amount); {
	case stderrors.Is(err, bank.ErrBelowMinimum):
		return money.Amount{}, application.ErrAmountBelowMinimum.WithDetail("min", h.rules.Limits.Min.Minor())
	case stderrors.Is(err, bank.ErrAboveMaximum):
		return money.Amount{}, application.ErrAmountAboveMaximum.WithDetail("max", h.rules.Limits.Max.Minor())
	case err != nil:
		return money.Amount{}, refuseCompany(screens.CompanyRefusedInvalidAmount, c, snap)
	}
	return amount, nil
}

// Deposit handles company.deposit: money from the owner's or the manager's
// cash or card into the company's account. Cash is handed over in the
// company's city; a card works from anywhere.
func (h *CompaniesHandler) Deposit(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	method, chosen, err := chosenMethod(req.Method)
	if err != nil {
		return nil, err
	}
	if !chosen {
		method = payment.Card
	}
	var (
		notice   *screens.CompanyNotice
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}
		c, _, err := h.managed(ctx, tx, snap, p, req.code(), company.RightDeposit)
		if err != nil {
			return err
		}
		amount, err := h.parseAmount(req.Amount, snap, c)
		if err != nil {
			return err
		}
		now := h.now()
		accepted := snap.Accepts(content.ServiceCompany)
		if method == payment.Cash {
			here, err := h.cityOf(ctx, tx, p)
			if err != nil {
				return err
			}
			if here == nil || here.ID != c.CityID {
				city, err := h.cities.ByID(ctx, c.CityID)
				if err != nil {
					return err
				}
				r := refuseCompany(screens.CompanyRefusedCashAway, c, snap)
				r.view.CityCode, r.view.City = city.Code, city.Name
				return r
			}
		}
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		plan := wallet.Plan(amount, accepted)
		back := []string{screens.AddrCompanyManage, c.Code}
		if err := checkMethod(plan, method, wallet, "company.button.manage", back...); err != nil {
			return err
		}
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, c.ID)
		if err != nil {
			return err
		}
		txID, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{
			Method: method, Accepted: plan.Accepted, Reason: application.ReasonCompanyDeposit,
			ReferenceType: companyReference, ReferenceID: c.ID,
			To: []application.LedgerEntry{{AccountID: acct.ID, Amount: amount}}, CreatedAt: now,
		})
		if err != nil {
			if stderrors.Is(err, application.ErrPaymentDeclined) {
				return declined(plan, wallet, "company.button.manage", back...)
			}
			return err
		}
		req.Company = c.Code
		notice = &screens.CompanyNotice{Kind: screens.CompanyNoticeDeposited, Amount: amount.Minor()}
		return appendCompanyEvent(ctx, tx, meta, "deposited", c.ID, map[string]any{
			"company_id": c.ID, "player_id": p.ID, "amount": amount.Minor(), "method": string(method), "transaction_id": txID,
		})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replayed {
		notice = nil
	}
	return h.manageWith(ctx, meta, req.code(), notice)
}

// Withdraw handles company.withdraw: profit out of the company to its
// owner's bank account, the city's corporate tax taken from it. Only the
// owner, only from the money not promised to running shifts, and only while
// the company owes no upkeep.
func (h *CompaniesHandler) Withdraw(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		notice   *screens.CompanyNotice
		replayed bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}
		c, _, err := h.managed(ctx, tx, snap, p, req.code(), company.RightWithdraw)
		if err != nil {
			return err
		}
		amount, err := h.parseAmount(req.Amount, snap, c)
		if err != nil {
			return err
		}
		city, err := h.cities.ByID(ctx, c.CityID)
		if err != nil {
			return err
		}
		taxBPS, err := h.lever(ctx, *city, LeverCorporateTax)
		if err != nil {
			return err
		}
		books, acct, err := companyBooks(ctx, tx, *c)
		if err != nil {
			return err
		}
		w, err := company.Withdraw(books, amount, int(taxBPS))
		switch {
		case stderrors.Is(err, company.ErrInDebt):
			return refuseCompany(screens.CompanyRefusedInDebt, c, snap)
		case stderrors.Is(err, company.ErrNotEnoughAvailable):
			r := refuseCompany(screens.CompanyRefusedNotEnough, c, snap)
			r.view.Need, r.view.Have = amount.Minor(), books.Available().Minor()
			return r
		case err != nil:
			return errors.Internal(err)
		}
		now := h.now()
		txID, err := h.payOut(ctx, tx, *c, acct, w, now)
		if err != nil {
			return err
		}
		req.Company = c.Code
		notice = &screens.CompanyNotice{Kind: screens.CompanyNoticeWithdrawn, Amount: w.Gross.Minor(), Tax: w.Tax.Minor(), Net: w.Net.Minor()}
		return appendCompanyEvent(ctx, tx, meta, "withdrawn", c.ID, map[string]any{
			"company_id": c.ID, "player_id": p.ID, "gross": w.Gross.Minor(), "tax": w.Tax.Minor(), "net": w.Net.Minor(),
			"transaction_id": txID,
		})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replayed {
		notice = nil
	}
	return h.manageWith(ctx, meta, req.code(), notice)
}

// payOut moves a withdrawal out of a company: the corporate tax to its
// city's treasury, the rest to its owner's bank account. It returns the
// transaction of the owner's part.
func (h *CompaniesHandler) payOut(ctx context.Context, tx application.Tx, c application.Company, acct application.Account,
	w company.Withdrawal, now time.Time,
) (string, error) {
	ledger := tx.Ledger()
	if !w.Tax.IsZero() {
		treasury, err := ledger.AccountFor(ctx, application.AccountCityTreasury, c.CityID)
		if err != nil {
			return "", err
		}
		if _, err := post(ctx, ledger, application.ReasonCorporateTax, c.ID, acct.ID, treasury.ID, w.Tax, now); err != nil {
			return "", err
		}
	}
	if w.Net.IsZero() {
		return "", nil
	}
	owner, err := ledger.AccountFor(ctx, application.AccountPlayerBank, c.OwnerID)
	if err != nil {
		return "", err
	}
	return post(ctx, ledger, application.ReasonCompanyWithdrawal, c.ID, acct.ID, owner.ID, w.Net, now)
}

// post moves amount from one account to another under reason, referring to
// the company.
func post(ctx context.Context, ledger application.LedgerRepository, reason application.Reason, companyID, from, to string,
	amount money.Amount, now time.Time,
) (string, error) {
	neg, err := amount.Neg()
	if err != nil {
		return "", errors.Internal(err)
	}
	return ledger.Post(ctx, application.LedgerTransaction{
		Reason: reason, ReferenceType: companyReference, ReferenceID: companyID,
		Entries:   []application.LedgerEntry{{AccountID: from, Amount: neg}, {AccountID: to, Amount: amount}},
		CreatedAt: now,
	})
}

// Price handles company.price: the company's price level, within its kind's
// bounds.
func (h *CompaniesHandler) Price(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	bps, err := strconv.Atoi(strings.TrimSpace(req.Price))
	if err != nil {
		return nil, errors.InvalidInput("a price level is a whole number of basis points")
	}
	return h.change(ctx, meta, req, company.RightSetPrice, func(snap *content.Snapshot, c *application.Company) (*screens.CompanyNotice, error) {
		_, ty, err := companyType(snap, *c)
		if err != nil {
			return nil, err
		}
		if err := ty.CheckPrice(bps); err != nil {
			return nil, refuseCompany(screens.CompanyRefusedPrice, c, snap)
		}
		c.PriceBPS = bps
		return &screens.CompanyNotice{Kind: screens.CompanyNoticePrice, PriceBPS: bps}, nil
	})
}

// Auto handles company.auto: hiring qualified applicants as they apply, or
// not.
func (h *CompaniesHandler) Auto(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	on := req.On == screens.CompanyAutoOn
	if !on && req.On != screens.CompanyAutoOff {
		return nil, errors.InvalidInput("automatic hiring is on or off")
	}
	return h.change(ctx, meta, req, company.RightManageStaff, func(_ *content.Snapshot, c *application.Company) (*screens.CompanyNotice, error) {
		c.AutoAccept = on
		if on {
			return &screens.CompanyNotice{Kind: screens.CompanyNoticeAutoOn}, nil
		}
		return &screens.CompanyNotice{Kind: screens.CompanyNoticeAutoOff}, nil
	})
}

// change applies one setting of a company under its lock and shows the
// management screen with what changed.
func (h *CompaniesHandler) change(ctx context.Context, meta envelope.Metadata, req CompanyRequest, right company.Right,
	apply func(snap *content.Snapshot, c *application.Company) (*screens.CompanyNotice, error),
) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var notice *screens.CompanyNotice
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		c, _, err := h.managed(ctx, tx, snap, p, req.code(), right)
		if err != nil {
			return err
		}
		if notice, err = apply(snap, c); err != nil {
			return err
		}
		c.UpdatedAt = h.now()
		return tx.Companies().Save(ctx, *c)
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.manageWith(ctx, meta, req.code(), notice)
}

// Manager handles company.manager: the owner names a manager — anybody the
// player search finds — or, with "none", removes the one there is.
func (h *CompaniesHandler) Manager(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	var target *application.Player
	remove := h.noManager(meta.Language, req.To)
	if !remove {
		q, ok := ClassifyPlayerQuery(req.To)
		if ok {
			found, err := h.search.Find(ctx, q)
			if err != nil && !isSentinel(err, application.ErrPlayerNotFound) {
				return nil, err
			}
			target = found
		}
	}
	snap := h.content.Current()
	var appointed *application.Player
	resp, err := h.change(ctx, meta, req, company.RightAppoint, func(snap *content.Snapshot, c *application.Company) (*screens.CompanyNotice, error) {
		if remove {
			c.ManagerID = ""
			return &screens.CompanyNotice{Kind: screens.CompanyNoticeManagerRemoved}, nil
		}
		if target == nil || target.Status != playerActive {
			return nil, refuseCompany(screens.CompanyRefusedNoPlayer, c, snap)
		}
		if target.ID == c.OwnerID {
			return nil, refuseCompany(screens.CompanyRefusedSelf, c, snap)
		}
		c.ManagerID, appointed = target.ID, target
		return &screens.CompanyNotice{Kind: screens.CompanyNoticeManagerSet, Player: govPlayerOf(target)}, nil
	})
	if err != nil || appointed == nil {
		return resp, err
	}
	// The new manager hears of it; a failure here does not undo the
	// appointment, which has committed.
	_ = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		c, err := tx.Companies().ByCode(ctx, req.code())
		if err != nil || c.ManagerID != appointed.ID {
			return err
		}
		owner, err := tx.Players().GetByID(ctx, c.OwnerID)
		if err != nil {
			return err
		}
		return appendCompanyEvent(ctx, tx, meta, "manager_appointed", c.ID, map[string]any{
			"company_id": c.ID, "code": c.Code, "name": c.Name, "type": c.TypeCode, "player_id": appointed.ID,
			"owner_name": shownName(owner), "owner_code": owner.PublicCode, "content_version": snap.Version(),
		})
	})
	return resp, nil
}

// noManager reports whether a typed answer to «who manages the company»
// means nobody: the button's word, or the word the player's language uses
// (company.none_words in the locales, alternatives separated by «|»).
func (h *CompaniesHandler) noManager(lang, typed string) bool {
	typed = strings.TrimSpace(typed)
	if strings.EqualFold(typed, screens.CompanyNoManager) {
		return true
	}
	for _, w := range strings.Split(h.msgs.T(lang, "company.none_words", nil), "|") {
		if w = strings.TrimSpace(w); w != "" && strings.EqualFold(typed, w) {
			return true
		}
	}
	return false
}

// Close handles company.close: without confirmation it shows what closing
// would do; with it, the company closes — its debt paid as far as it goes,
// the rest to its owner taxed, its staff let go and told, its openings
// closed — unless shifts are being worked for it.
func (h *CompaniesHandler) Close(ctx context.Context, meta envelope.Metadata, req CompanyRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	confirmed := req.Confirm == screens.CompanyConfirm
	var (
		view     screens.CompanyCloseView
		replayed bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		if confirmed {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		// The staff's jobs first, then the company: the order a shift takes
		// them in, so a shift starting now and this closing wait for each
		// other instead of deadlocking.
		if confirmed {
			if found, err := h.byCode(ctx, tx, snap, req.code(), false); err == nil {
				staff, err := tx.Companies().Staff(ctx, found.ID)
				if err != nil {
					return err
				}
				for _, e := range staff {
					if _, err := tx.Employment().Current(ctx, e.PlayerID); err != nil && !isSentinel(err, application.ErrNotEmployed) {
						return err
					}
				}
			}
		}
		c, _, err := h.managed(ctx, tx, snap, p, req.code(), company.RightClose)
		if err != nil {
			return err
		}
		city, err := h.cities.ByID(ctx, c.CityID)
		if err != nil {
			return err
		}
		taxBPS, err := h.lever(ctx, *city, LeverCorporateTax)
		if err != nil {
			return err
		}
		books, acct, err := companyBooks(ctx, tx, *c)
		if err != nil {
			return err
		}
		closing, err := company.Close(books, int(taxBPS))
		if stderrors.Is(err, company.ErrShiftsRunning) {
			return refuseCompany(screens.CompanyRefusedShiftsRunning, c, snap)
		}
		if err != nil {
			return errors.Internal(err)
		}
		staff, err := tx.Companies().Staff(ctx, c.ID)
		if err != nil {
			return err
		}
		view = screens.CompanyCloseView{Ref: companyRef(snap, *c), DebtPaid: closing.DebtPaid.Minor(),
			Tax: closing.Payout.Tax.Minor(), Net: closing.Payout.Net.Minor(), Staff: len(staff), Done: confirmed}
		if !confirmed {
			return nil
		}
		return h.dissolve(ctx, tx, meta, snap, c, acct, closing, company.ClosedByOwner, h.now())
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replayed {
		return h.List(ctx, meta)
	}
	return screens.CompanyClose(h.screen(meta, lang), view), nil
}

// dissolve closes a company: the debt paid as closing says, the payout
// made, the staff let go and told, its openings closed and applications
// withdrawn, the company marked dissolved, and the city's groups told.
func (h *CompaniesHandler) dissolve(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	c *application.Company, acct application.Account, closing company.Closing, reason string, now time.Time,
) error {
	ledger := tx.Ledger()
	if !closing.DebtPaid.IsZero() {
		if _, err := post(ctx, ledger, application.ReasonMaintenance, c.ID, acct.ID, application.SystemSinkAccountID,
			closing.DebtPaid, now); err != nil {
			return err
		}
	}
	if _, err := h.payOut(ctx, tx, *c, acct, closing.Payout, now); err != nil {
		return err
	}
	staff, err := tx.Companies().Staff(ctx, c.ID)
	if err != nil {
		return err
	}
	for _, e := range staff {
		if err := tx.Employment().End(ctx, e.EmploymentID, application.EndCompanyClosed, now); err != nil &&
			!isSentinel(err, application.ErrNotEmployed) {
			return err
		}
		if err := h.employeeEvent(ctx, tx, meta, snap, *c, e.PlayerID, e.CareerCode, e.Tier, e.Rate,
			screens.CompanyEmployeeClosed); err != nil {
			return err
		}
	}
	if err := tx.Companies().CloseOpenings(ctx, c.ID, now); err != nil {
		return err
	}
	if _, err := tx.Companies().WithdrawCompanyApplications(ctx, c.ID, now); err != nil {
		return err
	}
	c.Status, c.ClosedAt, c.CloseReason, c.UpdatedAt = application.CompanyDissolved, &now, reason, now
	c.Debt, c.Arrears = 0, 0
	if err := tx.Companies().Save(ctx, *c); err != nil {
		return err
	}
	return appendCompanyEvent(ctx, tx, meta, "closed", c.ID, map[string]any{
		"company_id": c.ID, "code": c.Code, "name": c.Name, "type": c.TypeCode, "city_id": c.CityID,
		"owner_id": c.OwnerID, "reason": reason, "debt_paid": closing.DebtPaid.Minor(),
		"written_off": closing.WrittenOff.Minor(), "payout": closing.Payout.Net.Minor(), "tax": closing.Payout.Tax.Minor(),
	})
}

// employeeEvent tells an applicant or an employee what a company did.
func (h *CompaniesHandler) employeeEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata, snap *content.Snapshot,
	c application.Company, playerID, career string, tier int, wage int64, kind string,
) error {
	ref := screens.JobRef{CareerCode: career, CareerName: career}
	if def, ok := snap.CareerDef(career); ok {
		ref = jobRef(def, tier)
	}
	return appendCompanyEvent(ctx, tx, meta, "employee", c.ID, map[string]any{
		"kind": kind, "company_id": c.ID, "code": c.Code, "name": c.Name, "player_id": playerID,
		"career": ref.CareerCode, "career_name": ref.CareerName, "rank": ref.Rank, "title": ref.Title, "wage": wage,
	})
}

// publicProducts adds to a company's public page what it makes — its own
// final designs, by name and kind — and the technologies it published.
func publicProducts(ctx context.Context, tx application.Tx, snap *content.Snapshot, c application.Company, v *screens.CompanyPageView) error {
	designs, err := tx.Production().Designs(ctx, c.ID)
	if err != nil {
		return err
	}
	for _, d := range designs {
		if d.Status == application.DesignFinal && d.Origin == "authored" {
			v.Products = append(v.Products, designGood(snap, d))
		}
	}
	techs, err := tx.Production().Technologies(ctx, c.ID)
	if err != nil {
		return err
	}
	for _, t := range techs {
		if t.Mode == "published" {
			def, _ := snap.Technology(t.Tech)
			v.Published = append(v.Published, named(def.Code, def.Name))
		}
	}
	return nil
}
