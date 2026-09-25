package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/diplomacy"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/military"
	"github.com/mrjvadi/torncity/internal/domain/war"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// WarRules is the tuning of war (config war). The notice and the proposal's
// life are REAL time: promises to players, like a lever's notice.
type WarRules struct {
	// DeclarationNotice is how long after a declaration (or a resumption)
	// the war may be fought.
	DeclarationNotice time.Duration
	// ProposalTTL is how long a ceasefire or a peace waits for an answer.
	ProposalTTL time.Duration
	// EndedShownFor is how long an ended war stays on the board.
	EndedShownFor time.Duration
	// BoardOperations is how many operations the board lists.
	BoardOperations int
	// NoticeCap is the most players of a struck city told privately.
	NoticeCap int
}

// WarHandler serves war (docs/adr/0022-military-and-diplomacy.md, part
// two): the war board, anyone's to read; declaring a war, joining an ally's,
// proposing and answering a ceasefire or a peace and resuming a war, the
// holder of country.war's; the war room and launching an operation, the
// command of the branch whose equipment goes; and — from the scheduler — an
// operation reaching its target, resolved once.
type WarHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	policy  application.PolicyReader
	scale   gametime.Scale
	rules   WarRules

	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewWarHandler wires the handler.
func NewWarHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, policy application.PolicyReader, scale gametime.Scale, rules WarRules,
	idempotencyTTL time.Duration, now func() time.Time,
) *WarHandler {
	if source == nil || cities == nil || policy == nil || ids == nil {
		panic("handlers: NewWarHandler requires content, cities, a policy reader and ids")
	}
	if scale.Validate() != nil || idempotencyTTL <= 0 || rules.DeclarationNotice < 0 || rules.ProposalTTL <= 0 ||
		rules.EndedShownFor < 0 || rules.BoardOperations < 1 || rules.NoticeCap < 0 {
		panic("handlers: NewWarHandler requires a game clock, an idempotency ttl and valid rules")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &WarHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, policy: policy, scale: scale,
		rules: rules, idempotencyTTL: idempotencyTTL, now: now}
}

// WarRequest is the payload of the war commands; which fields a command
// reads is its own.
type WarRequest struct {
	Country   string `json:"country,omitempty"`
	Target    string `json:"target,omitempty"`
	Ground    string `json:"ground,omitempty"`
	No        string `json:"no,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Verdict   string `json:"verdict,omitempty"`
	City      string `json:"city,omitempty"`
	Class     string `json:"class,omitempty"`
	Objective string `json:"objective,omitempty"`
	Qty       string `json:"qty,omitempty"`
	Confirm   string `json:"confirm,omitempty"`
}

func (r WarRequest) confirmed() bool { return strings.TrimSpace(r.Confirm) == screens.WarConfirm }

func (h *WarHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// warRefusal carries a refused decision of war out of a unit of work.
type warRefusal struct{ view screens.WarRefusalView }

func (r *warRefusal) Error() string { return "handlers: war refused: " + r.view.Kind }

func refuseWar(kind string, country *application.Jurisdiction, back ...string) *warRefusal {
	r := &warRefusal{view: screens.WarRefusalView{Kind: kind, Back: back}}
	if country != nil {
		r.view.Country = countryPlace(*country)
	}
	return r
}

func (h *WarHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	if err == nil {
		return nil, nil
	}
	c := h.screen(meta, lang)
	var r *warRefusal
	if stderrors.As(err, &r) {
		return screens.WarRefusal(c, r.view), nil
	}
	if isSentinel(err, application.ErrJurisdictionNotFound) || isSentinel(err, application.ErrWarNotFound) ||
		isSentinel(err, application.ErrProposalNotFound) || isSentinel(err, application.ErrCityNotFound) {
		return screens.WarRefusal(c, screens.WarRefusalView{Kind: screens.WarRefusedNotFound}), nil
	}
	return nil, err
}

func (h *WarHandler) player(ctx context.Context, tx application.Tx, meta envelope.Metadata, lang *string) (*application.Player, error) {
	p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
	if err != nil {
		return nil, err
	}
	*lang = RenderLanguage(meta, p)
	return p, nil
}

func (h *WarHandler) reserve(ctx context.Context, tx application.Tx, playerID string, meta envelope.Metadata) (bool, error) {
	key := idempotency.Derive(playerID, meta.RequestID, meta.IdempotencyKey)
	return tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

// confirm reserves the idempotency key of a confirmed request: a second
// delivery of the same press reads as not confirmed and changes nothing.
func (h *WarHandler) confirm(ctx context.Context, tx application.Tx, p *application.Player, meta envelope.Metadata,
	req WarRequest,
) (bool, error) {
	if !req.confirmed() {
		return false, nil
	}
	return h.reserve(ctx, tx, p.ID, meta)
}

func (h *WarHandler) country(ctx context.Context, tx application.Tx, p *application.Player, code string) (*application.Jurisdiction, error) {
	j, err := countryFor(ctx, tx, p, code)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return nil, refuseWar(screens.WarRefusedNoCountry, nil)
	}
	return j, nil
}

// actingFor is the country the player takes an action for — their own
// first, then any other whose office they hold or act for — with the seat;
// nil when they act for none.
func actingFor(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, action string,
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
	return nil, application.Office{}, nil
}

// headOf refuses a player who decides war for no country.
func (h *WarHandler) headOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
) (*application.Jurisdiction, application.Office, error) {
	country, seat, err := actingFor(ctx, tx, snap, p, content.ActionWar)
	if err != nil || country != nil {
		return country, seat, err
	}
	home, err := countryFor(ctx, tx, p, "")
	if err != nil {
		return nil, seat, err
	}
	r := refuseWar(screens.WarRefusedNotHolder, home)
	r.view.Office = actionOffice(snap, content.ActionWar)
	return nil, seat, r
}

// lockCountries takes the diplomacy locks of several countries in id
// order, each once.
func lockCountries(ctx context.Context, tx application.Tx, ids ...string) error {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	last := ""
	for _, id := range sorted {
		if id == "" || id == last {
			continue
		}
		last = id
		if err := tx.Diplomacy().LockCountry(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// warBetween is the war not over in which a and b are on opposite sides.
func warBetween(ctx context.Context, tx application.Tx, a, b string, now time.Time) (*application.War, error) {
	wars, err := tx.War().Wars(ctx, a, now)
	if err != nil {
		return nil, err
	}
	for i := range wars {
		if r := wars[i].Rule(); r.Open() && r.Opposed(a, b) {
			return &wars[i], nil
		}
	}
	return nil, nil
}

// warPlaces names a war's principals and allies.
func warPlaces(ctx context.Context, tx application.Tx, w application.War) (att, def screens.GovPlace, attAllies,
	defAllies []screens.GovPlace, err error,
) {
	if att, err = placeOf(ctx, tx, w.AttackerID); err != nil {
		return
	}
	if def, err = placeOf(ctx, tx, w.DefenderID); err != nil {
		return
	}
	for _, p := range w.Parties {
		if p.CountryID == w.AttackerID || p.CountryID == w.DefenderID {
			continue
		}
		place, perr := placeOf(ctx, tx, p.CountryID)
		if perr != nil {
			return att, def, nil, nil, perr
		}
		if p.Side == war.Attacker {
			attAllies = append(attAllies, place)
		} else {
			defAllies = append(defAllies, place)
		}
	}
	return
}

// allCityIDs lists the cities of every party of a war, for an announcement.
func allCityIDs(ctx context.Context, tx application.Tx, w application.War) ([]string, error) {
	ids := []string{w.AttackerID, w.DefenderID}
	for _, p := range w.Parties {
		ids = append(ids, p.CountryID)
	}
	return cityIDsOf(ctx, tx, ids...)
}

// damageNow is a city's damage at now, healed on the game clock.
func (h *WarHandler) damageNow(snap *content.Snapshot, d *application.CityDamage, now time.Time) int64 {
	def, ok := snap.War()
	if !ok || d == nil {
		return 0
	}
	return def.CityRules().DamageAt(d.DamageBPS, d.AsOf, now, int64(h.scale))
}

// ---------------------------------------------------------------------------
// The board.

// Board handles war.board: a country's wars, what is on the table, the
// cities occupied and damaged, and the latest operations in bands.
func (h *WarHandler) Board(ctx context.Context, meta envelope.Metadata, req WarRequest) (*presenter.Response, error) {
	return h.boardWith(ctx, meta, req, "")
}

func (h *WarHandler) boardWith(ctx context.Context, meta envelope.Metadata, req WarRequest, notice string) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.WarBoardView
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
		view = screens.WarBoardView{Country: countryPlace(*country), Notice: notice}
		canWar := false
		if !meta.InGroup() {
			if _, canWar, err = mayAct(ctx, tx, snap, country.ID, content.ActionWar, p); err != nil {
				return err
			}
			if view.CanCommand, err = cleared(ctx, tx, snap, country.ID, p); err != nil {
				return err
			}
		}
		view.CanDeclare = canWar
		wars, err := tx.War().Wars(ctx, country.ID, now.Add(-h.rules.EndedShownFor))
		if err != nil {
			return err
		}
		var warIDs []string
		for _, w := range wars {
			warIDs = append(warIDs, w.ID)
			line, err := h.warLine(ctx, tx, w, country.ID, canWar, now)
			if err != nil {
				return err
			}
			view.Wars = append(view.Wars, line)
		}
		if err := h.joinable(ctx, tx, snap, country, &view, now); err != nil {
			return err
		}
		if err := h.cityLines(ctx, tx, snap, country.ID, &view, now); err != nil {
			return err
		}
		ops, err := tx.War().Operations(ctx, warIDs, h.rules.BoardOperations)
		if err != nil {
			return err
		}
		for _, o := range ops {
			line, err := h.operationLine(ctx, tx, snap, o, now)
			if err != nil {
				return err
			}
			view.Operations = append(view.Operations, line)
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.WarBoard(h.screen(meta, lang), view), nil
}

// warLine reads a war for the board, from the country's side.
func (h *WarHandler) warLine(ctx context.Context, tx application.Tx, w application.War, countryID string, canWar bool,
	now time.Time,
) (screens.WarLine, error) {
	att, def, attAllies, defAllies, err := warPlaces(ctx, tx, w)
	if err != nil {
		return screens.WarLine{}, err
	}
	r := w.Rule()
	status := r.StatusAt(now)
	line := screens.WarLine{No: w.No, Attacker: att, Defender: def, AttackerAllies: attAllies, DefenderAllies: defAllies,
		Ground: w.Ground, Status: string(status), Broke: len(w.BrokeTreaties) > 0,
		Since: max(now.Sub(w.DeclaredAt), time.Second)}
	switch status {
	case war.Declared:
		line.ActiveIn, line.ActiveAt = w.ActiveAt.Sub(now), w.ActiveAt
	case war.Active:
		line.Since = max(now.Sub(w.ActiveAt), time.Second)
	case war.Ceasefire, war.Ended:
		line.Since = max(now.Sub(w.UpdatedAt), time.Second)
	}
	principal := countryID == w.AttackerID || countryID == w.DefenderID
	if principal && status != war.Ended {
		line.CanPropose = canWar
		line.CanResume = canWar && status == war.Ceasefire
		props, err := tx.War().Proposals(ctx, w.ID)
		if err != nil {
			return line, err
		}
		for _, pr := range props {
			if pr.Rule().StatusAt(now) != war.ProposalOpen {
				continue
			}
			other := pr.ProposerID
			if other == countryID {
				other = pr.PartnerID
			}
			place, err := placeOf(ctx, tx, other)
			if err != nil {
				return line, err
			}
			line.Proposals = append(line.Proposals, screens.ProposalLine{No: pr.No, Kind: string(pr.Kind), Other: place,
				Incoming: pr.PartnerID == countryID, ExpiresIn: pr.ExpiresAt.Sub(now)})
		}
	}
	return line, nil
}

// joinable lists the wars the country may join: an ally of a principal by a
// treaty of mutual defence, not a party yet.
func (h *WarHandler) joinable(ctx context.Context, tx application.Tx, snap *content.Snapshot, country *application.Jurisdiction,
	view *screens.WarBoardView, now time.Time,
) error {
	all, err := tx.War().Wars(ctx, "", now)
	if err != nil {
		return err
	}
	for _, w := range all {
		r := w.Rule()
		if !r.Open() {
			continue
		}
		if _, party := r.SideOf(country.ID); party {
			continue
		}
		side, ok, err := allySide(ctx, tx, snap, w, country.ID, now)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		ally, err := placeOf(ctx, tx, r.Principal(side))
		if err != nil {
			return err
		}
		enemy, err := placeOf(ctx, tx, r.Principal(side.Other()))
		if err != nil {
			return err
		}
		view.Joinable = append(view.Joinable, screens.JoinLine{WarNo: w.No, Ally: ally, Enemy: enemy})
	}
	return nil
}

// allySide is the side a country would join a war on: that of a principal
// it has a treaty of mutual defence with — the defender's first, since an
// alliance answers an attack (UN Charter art. 51; North Atlantic Treaty
// art. 5: each ally takes "such action as it deems necessary").
func allySide(ctx context.Context, tx application.Tx, snap *content.Snapshot, w application.War, country string,
	now time.Time,
) (war.Side, bool, error) {
	types := snap.TreatyRules()
	for _, side := range []war.Side{war.Defender, war.Attacker} {
		principal := w.DefenderID
		if side == war.Attacker {
			principal = w.AttackerID
		}
		treaties, err := tx.Diplomacy().TreatiesBetween(ctx, country, principal)
		if err != nil {
			return "", false, err
		}
		rules := make([]diplomacy.Treaty, len(treaties))
		for i, t := range treaties {
			rules[i] = t.Rule()
		}
		if diplomacy.Partners(rules, types, country, principal, now, func(t diplomacy.TreatyType) bool { return t.MutualDefence }) {
			return side, true, nil
		}
	}
	return "", false, nil
}

// cityLines fills the occupied and damaged cities the country is concerned
// with: its own, and those it holds or lost.
func (h *WarHandler) cityLines(ctx context.Context, tx application.Tx, snap *content.Snapshot, countryID string,
	view *screens.WarBoardView, now time.Time,
) error {
	controls, err := tx.War().Controls(ctx)
	if err != nil {
		return err
	}
	for _, c := range controls {
		if c.ControllerCountryID != countryID && c.DeJureCountryID != countryID {
			continue
		}
		city, err := h.cities.ByID(ctx, c.CityID)
		if err != nil {
			return err
		}
		controller, err := placeOf(ctx, tx, c.ControllerCountryID)
		if err != nil {
			return err
		}
		deJure, err := placeOf(ctx, tx, c.DeJureCountryID)
		if err != nil {
			return err
		}
		view.Occupied = append(view.Occupied, screens.OccupationLine{CityCode: city.Code, City: city.Name,
			Controller: controller, DeJure: deJure, Since: max(now.Sub(c.Since), time.Second)})
	}
	def, ok := snap.War()
	if !ok {
		return nil
	}
	damaged, err := tx.War().DamagedCities(ctx)
	if err != nil {
		return err
	}
	for _, d := range damaged {
		owner, err := tx.Diplomacy().CountryOfCity(ctx, d.CityID)
		if err != nil {
			return err
		}
		if owner != countryID {
			continue
		}
		dmg := def.CityRules().DamageAt(d.DamageBPS, d.AsOf, now, int64(h.scale))
		band := war.BandOf(def.Bands(), dmg)
		if band == "" {
			continue
		}
		city, err := h.cities.ByID(ctx, d.CityID)
		if err != nil {
			return err
		}
		line := screens.DamageLine{CityCode: city.Code, City: city.Name, Band: band}
		if d.ClosedUntil.After(now) {
			line.ClosedIn = d.ClosedUntil.Sub(now)
		}
		view.Damaged = append(view.Damaged, line)
	}
	return nil
}

// operationLine reads an operation for the board, in bands.
func (h *WarHandler) operationLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, o application.WarOperation,
	now time.Time,
) (screens.OperationLine, error) {
	country, err := placeOf(ctx, tx, o.CountryID)
	if err != nil {
		return screens.OperationLine{}, err
	}
	target, err := placeOf(ctx, tx, o.TargetCountryID)
	if err != nil {
		return screens.OperationLine{}, err
	}
	city, err := h.cities.ByID(ctx, o.TargetCityID)
	if err != nil {
		return screens.OperationLine{}, err
	}
	line := screens.OperationLine{No: o.No, Kind: o.Kind, Objective: o.Objective, Country: country, CityCode: city.Code,
		City: city.Name, Target: target, Captured: o.Captured}
	switch o.Status {
	case application.OperationLaunched:
		line.Pending, line.StrikesIn = true, max(o.StrikesAt.Sub(now), time.Second)
	case application.OperationCalledOff:
		line.CalledOff = true
	}
	if o.ResolvedAt != nil {
		line.Ago = max(now.Sub(*o.ResolvedAt), time.Second)
	}
	bands := snap.StrengthBands()
	line.LostBand = military.BandOf(bands, int64(o.AttackerLost))
	line.EnemyLostBand = military.BandOf(bands, int64(o.DefenderLost))
	if def, ok := snap.War(); ok {
		line.DamageBand = war.BandOf(def.Bands(), int64(o.DamageBPS))
	}
	return line, nil
}

// ---------------------------------------------------------------------------
// Declaring.

// Declare handles war.declare: the target, the ground, then confirm; on
// confirm, once, the war is recorded, the treaties between the two ended,
// both countries' groups told, and the target's allies called. It may be
// fought after the notice (Hague Convention III, 1907).
func (h *WarHandler) Declare(ctx context.Context, meta envelope.Metadata, req WarRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, hasWar := snap.War()
	lang := meta.Language
	var (
		view    screens.DeclareView
		done    bool
		country *application.Jurisdiction
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		confirm, err := h.confirm(ctx, tx, p, meta, req)
		if err != nil {
			return err
		}
		var seat application.Office
		if country, seat, err = h.headOf(ctx, tx, snap, p); err != nil {
			return err
		}
		if !hasWar {
			return refuseWar(screens.WarRefusedState, country)
		}
		view = screens.DeclareView{Country: countryPlace(*country), Notice: h.rules.DeclarationNotice}
		now := h.now()
		targetCode := strings.ToLower(strings.TrimSpace(req.Target))
		if targetCode == "" {
			countries, err := tx.Diplomacy().Countries(ctx)
			if err != nil {
				return err
			}
			for _, c := range countries {
				if c.ID == country.ID {
					continue
				}
				if w, err := warBetween(ctx, tx, country.ID, c.ID, now); err != nil {
					return err
				} else if w == nil {
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
			return refuseWar(screens.WarRefusedSelf, country, screens.AddrWarDeclare)
		}
		place := countryPlace(target)
		view.Target = &place
		ground := strings.TrimSpace(req.Ground)
		if !hasCode(def.Grounds, ground) {
			view.Grounds = def.Grounds
			return nil
		}
		view.Ground = ground
		treaties, err := tx.Diplomacy().TreatiesBetween(ctx, country.ID, target.ID)
		if err != nil {
			return err
		}
		var breaking []application.Treaty
		for _, t := range treaties {
			if t.Rule().StatusAt(now) == diplomacy.Active {
				breaking = append(breaking, t)
				kind := named(t.Kind, t.Kind)
				if td, ok := snap.TreatyType(t.Kind); ok {
					kind.Name = td.Name
				}
				view.Breaks = append(view.Breaks, kind)
			}
		}
		allies, err := h.alliesOf(ctx, tx, snap, target.ID, country.ID, now)
		if err != nil {
			return err
		}
		for _, a := range allies {
			view.Allies = append(view.Allies, countryPlace(a))
		}
		if !confirm {
			return nil
		}
		if err := lockCountries(ctx, tx, country.ID, target.ID); err != nil {
			return err
		}
		open, err := tx.War().Wars(ctx, country.ID, now)
		if err != nil {
			return err
		}
		rules := make([]war.War, len(open))
		for i, w := range open {
			rules[i] = w.Rule()
		}
		if err := war.CheckDeclare(country.ID, target.ID, rules); err != nil {
			return refuseWar(screens.WarRefusedAtWar, country, screens.AddrWarBoard, country.Code)
		}
		w := application.War{ID: h.ids.NewID(), AttackerID: country.ID, DefenderID: target.ID, Ground: ground,
			DeclaredBy: p.ID, DeclaredOffice: seat.OfficeCode, DeclaredAt: now, ActiveAt: now.Add(h.rules.DeclarationNotice),
			BorderClosed: def.Economy.CloseBorder}
		// A declaration ends every treaty in force between the two: an
		// alliance or a pact of non-aggression cannot survive a war, and
		// the record says who broke it.
		for _, t := range breaking {
			locked, err := tx.Diplomacy().TreatyByNo(ctx, t.No, true)
			if err != nil {
				return err
			}
			if locked.Rule().StatusAt(now) != diplomacy.Active {
				continue
			}
			locked.Status, locked.EndedBy, locked.EndedOffice, locked.EndedAt = diplomacy.Terminated, p.ID, seat.OfficeCode, &now
			if err := tx.Diplomacy().SaveTreaty(ctx, *locked); err != nil {
				return err
			}
			if err := tx.Diplomacy().RecordEvent(ctx, application.DiplomacyEvent{ID: h.ids.NewID(),
				Kind: application.EventTreatyTerminated, CountryID: country.ID, OtherCountryID: target.ID, TreatyID: t.ID,
				PlayerID: p.ID, OfficeCode: seat.OfficeCode, At: now}); err != nil {
				return err
			}
			w.BrokeTreaties = append(w.BrokeTreaties, t.No)
		}
		w, err = tx.War().DeclareWar(ctx, w)
		if isSentinel(err, application.ErrAtWar) {
			return refuseWar(screens.WarRefusedAtWar, country, screens.AddrWarBoard, country.Code)
		}
		if err != nil {
			return err
		}
		if err := tx.War().RecordEvent(ctx, application.WarEvent{ID: h.ids.NewID(), Kind: application.WarEventDeclared,
			WarID: w.ID, CountryID: country.ID, OtherCountryID: target.ID, PlayerID: p.ID, OfficeCode: seat.OfficeCode,
			At: now}); err != nil {
			return err
		}
		if len(w.BrokeTreaties) > 0 {
			if err := tx.War().RecordEvent(ctx, application.WarEvent{ID: h.ids.NewID(), Kind: application.WarEventTreatyBroken,
				WarID: w.ID, CountryID: country.ID, OtherCountryID: target.ID, PlayerID: p.ID, OfficeCode: seat.OfficeCode,
				At: now}); err != nil {
				return err
			}
		}
		cities, err := cityIDsOf(ctx, tx, append([]string{country.ID, target.ID}, idsOf(allies)...)...)
		if err != nil {
			return err
		}
		if err := appendDomainEvent(ctx, tx, meta, "war", "declared", w.ID, map[string]any{
			"country_code": country.Code, "country_name": country.Name, "other_code": target.Code,
			"other_name": target.Name, "ground": ground, "notice_seconds": int64(h.rules.DeclarationNotice / time.Second),
			"broke": len(w.BrokeTreaties) > 0, "city_ids": cities}); err != nil {
			return err
		}
		// Each ally of the target by mutual defence hears privately, and
		// decides for itself whether to join.
		for _, a := range allies {
			if err := h.tellHead(ctx, tx, snap, meta, a, "ally_called", w.ID, map[string]any{"kind": "ally",
				"country_code": a.Code, "country_name": a.Name, "other_code": country.Code, "other_name": country.Name,
				"ally_code": target.Code, "ally_name": target.Name, "war_no": w.No}); err != nil {
				return err
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
		notice := c.T("war.declare.done", map[string]any{"target": c.PlaceName(*view.Target),
			"in": screens.FormatSpan(c, h.rules.DeclarationNotice)})
		return h.boardWith(ctx, meta, WarRequest{Country: country.Code}, notice)
	}
	return screens.Declare(h.screen(meta, lang), view), nil
}

// alliesOf lists the countries with a treaty of mutual defence in force with
// country, except the one named.
func (h *WarHandler) alliesOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, country, except string,
	now time.Time,
) ([]application.Jurisdiction, error) {
	countries, err := tx.Diplomacy().Countries(ctx)
	if err != nil {
		return nil, err
	}
	types := snap.TreatyRules()
	var out []application.Jurisdiction
	for _, c := range countries {
		if c.ID == country || c.ID == except {
			continue
		}
		treaties, err := tx.Diplomacy().TreatiesBetween(ctx, country, c.ID)
		if err != nil {
			return nil, err
		}
		rules := make([]diplomacy.Treaty, len(treaties))
		for i, t := range treaties {
			rules[i] = t.Rule()
		}
		if diplomacy.Partners(rules, types, country, c.ID, now, func(t diplomacy.TreatyType) bool { return t.MutualDefence }) {
			out = append(out, c)
		}
	}
	return out, nil
}

func idsOf(js []application.Jurisdiction) []string {
	out := make([]string, len(js))
	for i, j := range js {
		out[i] = j.ID
	}
	return out
}

// tellHead sends a private notice to whoever holds (or acts for) a country's
// war decisions.
func (h *WarHandler) tellHead(ctx context.Context, tx application.Tx, snap *content.Snapshot, meta envelope.Metadata,
	country application.Jurisdiction, name, aggregate string, payload map[string]any,
) error {
	chain, err := tx.Governance().ActingChain(ctx, actionOffice(snap, content.ActionWar), country.ID)
	if err != nil {
		return err
	}
	acting := application.ActingForChain(chain)
	if acting == nil {
		return nil
	}
	for _, s := range acting.Holders {
		body := map[string]any{"player_id": s.HolderPlayerID}
		for k, v := range payload {
			body[k] = v
		}
		if err := appendDomainEvent(ctx, tx, meta, "war", name, aggregate, body); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Joining, proposing, answering, resuming.

// Join handles war.join: an ally by mutual defence joins a war on its
// ally's side, once.
func (h *WarHandler) Join(ctx context.Context, meta envelope.Metadata, req WarRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view    screens.WarDecisionView
		done    bool
		country *application.Jurisdiction
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		confirm, err := h.confirm(ctx, tx, p, meta, req)
		if err != nil {
			return err
		}
		var seat application.Office
		if country, seat, err = h.headOf(ctx, tx, snap, p); err != nil {
			return err
		}
		no, ok := number(req.No)
		if !ok {
			return refuseWar(screens.WarRefusedNotFound, country)
		}
		w, err := tx.War().WarByNo(ctx, no, false)
		if err != nil {
			return err
		}
		now := h.now()
		side, ok, err := allySide(ctx, tx, snap, *w, country.ID, now)
		if err != nil {
			return err
		}
		if !ok {
			return refuseWar(screens.WarRefusedNoAlly, country, screens.AddrWarBoard, country.Code)
		}
		r := w.Rule()
		ally, err := placeOf(ctx, tx, r.Principal(side))
		if err != nil {
			return err
		}
		other, err := placeOf(ctx, tx, r.Principal(side.Other()))
		if err != nil {
			return err
		}
		view = screens.WarDecisionView{Kind: "join", Country: countryPlace(*country), WarNo: w.No, Ally: ally, Other: other}
		if err := war.CheckJoin(r, country.ID, side, now); err != nil {
			return refuseWar(screens.WarRefusedState, country, screens.AddrWarBoard, country.Code)
		}
		if !confirm {
			return nil
		}
		if err := lockCountries(ctx, tx, country.ID, w.AttackerID, w.DefenderID); err != nil {
			return err
		}
		if w, err = tx.War().WarByNo(ctx, no, true); err != nil {
			return err
		}
		if err := war.CheckJoin(w.Rule(), country.ID, side, now); err != nil {
			return refuseWar(screens.WarRefusedState, country, screens.AddrWarBoard, country.Code)
		}
		if err := tx.War().AddParty(ctx, application.WarParty{WarID: w.ID, CountryID: country.ID, Side: side, JoinedBy: p.ID,
			OfficeCode: seat.OfficeCode, JoinedAt: now}); err != nil {
			if isSentinel(err, application.ErrAtWar) {
				return refuseWar(screens.WarRefusedState, country, screens.AddrWarBoard, country.Code)
			}
			return err
		}
		if err := tx.War().RecordEvent(ctx, application.WarEvent{ID: h.ids.NewID(), Kind: application.WarEventJoined,
			WarID: w.ID, CountryID: country.ID, OtherCountryID: w.Rule().Principal(side.Other()), PlayerID: p.ID,
			OfficeCode: seat.OfficeCode, At: now}); err != nil {
			return err
		}
		w.Parties = append(w.Parties, application.WarParty{CountryID: country.ID, Side: side})
		cities, err := allCityIDs(ctx, tx, *w)
		if err != nil {
			return err
		}
		done = true
		return appendDomainEvent(ctx, tx, meta, "war", "joined", w.ID, map[string]any{
			"country_code": country.Code, "country_name": country.Name, "ally_code": ally.Code, "ally_name": ally.Name,
			"other_code": other.Code, "other_name": other.Name, "city_ids": cities})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if done {
		c := h.screen(meta, lang)
		return h.boardWith(ctx, meta, WarRequest{Country: country.Code}, c.T("war.join.done", map[string]any{
			"ally": c.PlaceName(view.Ally)}))
	}
	return screens.WarDecision(h.screen(meta, lang), view), nil
}

// principalFor reads a war by number and refuses a player who does not
// decide war for one of its principals.
func (h *WarHandler) principalFor(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
	rawNo string,
) (*application.War, *application.Jurisdiction, application.Office, error) {
	no, ok := number(rawNo)
	if !ok {
		return nil, nil, application.Office{}, refuseWar(screens.WarRefusedNotFound, nil)
	}
	w, err := tx.War().WarByNo(ctx, no, false)
	if err != nil {
		return nil, nil, application.Office{}, err
	}
	var first *application.Jurisdiction
	for _, id := range []string{w.AttackerID, w.DefenderID} {
		j, err := tx.Governance().Jurisdiction(ctx, id)
		if err != nil {
			return nil, nil, application.Office{}, err
		}
		if first == nil {
			first = &j
		}
		seat, ok, err := mayAct(ctx, tx, snap, id, content.ActionWar, p)
		if err != nil {
			return nil, nil, application.Office{}, err
		}
		if ok {
			return w, &j, seat, nil
		}
	}
	r := refuseWar(screens.WarRefusedNotHolder, first)
	r.view.Office = actionOffice(snap, content.ActionWar)
	return nil, nil, application.Office{}, r
}

// Propose handles war.propose: a principal offers the other a ceasefire or
// a peace, once; the other's head of state is told.
func (h *WarHandler) Propose(ctx context.Context, meta envelope.Metadata, req WarRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	kind := war.ProposalKind(strings.TrimSpace(req.Kind))
	if !kind.Valid() {
		return nil, errors.InvalidInput("a proposal is a ceasefire or a peace")
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view    screens.WarDecisionView
		done    bool
		country *application.Jurisdiction
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		confirm, err := h.confirm(ctx, tx, p, meta, req)
		if err != nil {
			return err
		}
		w, c, seat, err := h.principalFor(ctx, tx, snap, p, req.No)
		if err != nil {
			return err
		}
		country = c
		now := h.now()
		r := w.Rule()
		otherID := r.Other(c.ID)
		other, err := placeOf(ctx, tx, otherID)
		if err != nil {
			return err
		}
		view = screens.WarDecisionView{Kind: string(kind), Country: countryPlace(*c), WarNo: w.No, Other: other,
			TTL: h.rules.ProposalTTL}
		props, err := tx.War().Proposals(ctx, w.ID)
		if err != nil {
			return err
		}
		rules := make([]war.Proposal, len(props))
		for i, pr := range props {
			rules[i] = pr.Rule()
		}
		if err := war.CheckPropose(r, c.ID, kind, rules, now); err != nil {
			if stderrors.Is(err, war.ErrProposalOpen) {
				return refuseWar(screens.WarRefusedOpen, c, screens.AddrWarBoard, c.Code)
			}
			return refuseWar(screens.WarRefusedState, c, screens.AddrWarBoard, c.Code)
		}
		if !confirm {
			return nil
		}
		if err := lockCountries(ctx, tx, w.AttackerID, w.DefenderID); err != nil {
			return err
		}
		if err := tx.War().ExpireProposals(ctx, w.ID, now); err != nil {
			return err
		}
		pr, err := tx.War().Propose(ctx, application.WarProposal{ID: h.ids.NewID(), WarID: w.ID, Kind: kind, ProposerID: c.ID,
			PartnerID: otherID, ProposedBy: p.ID, ProposedOffice: seat.OfficeCode, ProposedAt: now,
			ExpiresAt: now.Add(h.rules.ProposalTTL)})
		if isSentinel(err, application.ErrProposalOpen) {
			return refuseWar(screens.WarRefusedOpen, c, screens.AddrWarBoard, c.Code)
		}
		if err != nil {
			return err
		}
		if err := tx.War().RecordEvent(ctx, application.WarEvent{ID: h.ids.NewID(), Kind: application.WarEventProposed,
			WarID: w.ID, CountryID: c.ID, OtherCountryID: otherID, ProposalID: pr.ID, PlayerID: p.ID,
			OfficeCode: seat.OfficeCode, At: now}); err != nil {
			return err
		}
		otherJ, err := tx.Governance().Jurisdiction(ctx, otherID)
		if err != nil {
			return err
		}
		done = true
		return h.tellHead(ctx, tx, snap, meta, otherJ, "proposed", pr.ID, map[string]any{"kind": "proposal",
			"country_code": otherJ.Code, "country_name": otherJ.Name, "other_code": c.Code, "other_name": c.Name,
			"war_no": w.No, "proposal_no": pr.No, "proposal_kind": string(kind),
			"ttl_seconds": int64(h.rules.ProposalTTL / time.Second)})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if done {
		c := h.screen(meta, lang)
		return h.boardWith(ctx, meta, WarRequest{Country: country.Code}, c.T("war.propose.done", map[string]any{
			"kind": c.T("war.proposal."+string(kind), nil), "other": c.PlaceName(view.Other)}))
	}
	return screens.WarDecision(h.screen(meta, lang), view), nil
}

// Answer handles war.answer: the other principal accepts or declines a
// ceasefire or a peace, once. An accepted ceasefire suspends the war — the
// operations under way are called off when they arrive — and a peace ends
// it; both are announced in every party's groups.
func (h *WarHandler) Answer(ctx context.Context, meta envelope.Metadata, req WarRequest) (*presenter.Response, error) {
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
		answer  string
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
		no, ok := number(req.No)
		if !ok {
			return refuseWar(screens.WarRefusedNotFound, nil)
		}
		pr, err := tx.War().ProposalByNo(ctx, no, false)
		if err != nil {
			return err
		}
		partner, err := tx.Governance().Jurisdiction(ctx, pr.PartnerID)
		if err != nil {
			return err
		}
		country = &partner
		seat, ok, err := mayAct(ctx, tx, snap, partner.ID, content.ActionWar, p)
		if err != nil {
			return err
		}
		if !ok {
			r := refuseWar(screens.WarRefusedNotHolder, &partner)
			r.view.Office = actionOffice(snap, content.ActionWar)
			return r
		}
		w, err := tx.War().WarByID(ctx, pr.WarID, false)
		if err != nil {
			return err
		}
		if err := lockCountries(ctx, tx, w.AttackerID, w.DefenderID); err != nil {
			return err
		}
		if w, err = tx.War().WarByID(ctx, pr.WarID, true); err != nil {
			return err
		}
		if pr, err = tx.War().ProposalByNo(ctx, no, true); err != nil {
			return err
		}
		now := h.now()
		ps, ws, err := war.Answer(w.Rule(), pr.Rule(), partner.ID, accept, now)
		if err != nil {
			return refuseWar(screens.WarRefusedState, &partner, screens.AddrWarBoard, partner.Code)
		}
		pr.Status, pr.DecidedBy, pr.DecidedOffice, pr.DecidedAt = ps, p.ID, seat.OfficeCode, &now
		if err := tx.War().SaveProposal(ctx, *pr); err != nil {
			return err
		}
		kind := application.WarEventDeclined
		if accept {
			kind = application.WarEventCeasefire
			if ws == war.Ended {
				kind = application.WarEventPeace
			}
		}
		if err := tx.War().RecordEvent(ctx, application.WarEvent{ID: h.ids.NewID(), Kind: kind, WarID: w.ID,
			CountryID: partner.ID, OtherCountryID: pr.ProposerID, ProposalID: pr.ID, PlayerID: p.ID,
			OfficeCode: seat.OfficeCode, At: now}); err != nil {
			return err
		}
		answer = string(ps)
		if !accept {
			return nil
		}
		w.Status, w.UpdatedAt = ws, now
		if ws == war.Ended {
			w.EndedAt = &now
		}
		if err := tx.War().SaveWar(ctx, *w); err != nil {
			return err
		}
		answer = kind
		a, err := placeOf(ctx, tx, w.AttackerID)
		if err != nil {
			return err
		}
		b, err := placeOf(ctx, tx, w.DefenderID)
		if err != nil {
			return err
		}
		cities, err := allCityIDs(ctx, tx, *w)
		if err != nil {
			return err
		}
		return appendDomainEvent(ctx, tx, meta, "war", "settled", w.ID, map[string]any{"kind": kind,
			"country_code": a.Code, "country_name": a.Name, "other_code": b.Code, "other_name": b.Name, "city_ids": cities})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	notice := ""
	if answer != "" {
		notice = h.screen(meta, lang).T("war.answer."+answer, nil)
	}
	code := ""
	if country != nil {
		code = country.Code
	}
	return h.boardWith(ctx, meta, WarRequest{Country: code}, notice)
}

// Resume handles war.resume: a principal ends a ceasefire; the war may be
// fought again after the same notice as a declaration (Hague Regulations
// art. 36: the enemy is warned).
func (h *WarHandler) Resume(ctx context.Context, meta envelope.Metadata, req WarRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view    screens.WarDecisionView
		done    bool
		country *application.Jurisdiction
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		confirm, err := h.confirm(ctx, tx, p, meta, req)
		if err != nil {
			return err
		}
		w, c, seat, err := h.principalFor(ctx, tx, snap, p, req.No)
		if err != nil {
			return err
		}
		country = c
		other, err := placeOf(ctx, tx, w.Rule().Other(c.ID))
		if err != nil {
			return err
		}
		view = screens.WarDecisionView{Kind: "resume", Country: countryPlace(*c), WarNo: w.No, Other: other,
			Notice: h.rules.DeclarationNotice}
		if err := war.CheckResume(w.Rule(), c.ID); err != nil {
			return refuseWar(screens.WarRefusedState, c, screens.AddrWarBoard, c.Code)
		}
		if !confirm {
			return nil
		}
		if err := lockCountries(ctx, tx, w.AttackerID, w.DefenderID); err != nil {
			return err
		}
		if w, err = tx.War().WarByID(ctx, w.ID, true); err != nil {
			return err
		}
		if err := war.CheckResume(w.Rule(), c.ID); err != nil {
			return refuseWar(screens.WarRefusedState, c, screens.AddrWarBoard, c.Code)
		}
		now := h.now()
		w.Status, w.ActiveAt, w.UpdatedAt = war.Declared, now.Add(h.rules.DeclarationNotice), now
		if err := tx.War().SaveWar(ctx, *w); err != nil {
			return err
		}
		if err := tx.War().RecordEvent(ctx, application.WarEvent{ID: h.ids.NewID(), Kind: application.WarEventResumed,
			WarID: w.ID, CountryID: c.ID, OtherCountryID: w.Rule().Other(c.ID), PlayerID: p.ID, OfficeCode: seat.OfficeCode,
			At: now}); err != nil {
			return err
		}
		cities, err := allCityIDs(ctx, tx, *w)
		if err != nil {
			return err
		}
		done = true
		return appendDomainEvent(ctx, tx, meta, "war", "settled", w.ID, map[string]any{"kind": application.WarEventResumed,
			"country_code": c.Code, "country_name": c.Name, "other_code": other.Code, "other_name": other.Name,
			"notice_seconds": int64(h.rules.DeclarationNotice / time.Second), "city_ids": cities})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if done {
		c := h.screen(meta, lang)
		return h.boardWith(ctx, meta, WarRequest{Country: country.Code}, c.T("war.resume.done", map[string]any{
			"in": screens.FormatSpan(c, h.rules.DeclarationNotice)}))
	}
	return screens.WarDecision(h.screen(meta, lang), view), nil
}
