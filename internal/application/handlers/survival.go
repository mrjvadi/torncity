package handlers

import (
	"context"
	stderrors "errors"
	"hash/fnv"
	"sort"
	"sync"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/climate"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/health"
	"github.com/mrjvadi/torncity/internal/domain/survival"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
)

// Survival (docs/adr/0070, plan B6): thirst, warmth, garments and sickness in a founded settlement.
//
//   - WHO WORKS THERE. The well is filled by its labourers; the tailor makes the garments; the warming house's keeper and the
//     health house's and clinic's staff are seats of the labour pool (daily services). A player drinks, dresses and warms himself.
//   - WHAT IT CONSUMES. A drink at the well is one unit of the settlement's water (item reason drunk), a drink from the bag one of
//     his own; boiling river water burns a firewood (boiled); a garment is a unit that leaves the bag when it is put on
//     (garment_worn) and wears out by the minutes it is worn; a treatment takes a medicine from the settlement stock (medicine_used).
//   - WHAT IT PROVIDES. Thirst and warmth bars that cost a shift part of its output when they run high, a collapse into a
//     hospital stay (never death) at the extreme, and a sickness for unsafe water or a long cold.
//   - NEVER A TICK. A need is stored with the instant it describes and read forward by internal/domain/survival over the
//     environment of internal/domain/climate, a pure function; any replica gets the same answer.
//   - EXEMPT. The neutral city and the content cities (no founded settlement) have no thirst or warmth; before the rule date plus
//     its grace nothing changes.

// WithSurvival gives the handler the rules and the readers of the village.
type survivalDeps struct {
	rules application.SurvivalRules
	home  HomeWarmer
	care  application.VillageCarer
	clock gametime.Clock
}

// HomeWarmer is what the village handler tells the survival handler of a player's home: the shelter it gives through the night.
type HomeWarmer interface {
	// HomeWarmth is the insulation a player's dwelling in the settlement gives at night: the dwelling's own, and a lit hearth's
	// when he has the wood. burn says the nights since the last read are to be burnt now (a command); false only looks.
	HomeWarmth(ctx context.Context, tx application.Tx, s application.FoundedSettlement, playerID string, nights int, def content.SurvivalDef, burn bool, now time.Time) (int, error)
}

// SurvivalHandler serves the needs screen and the commands of survival.
type SurvivalHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	worlds  *application.WorldCache
	cities  application.CityRepository
	scale   gametime.Scale
	deps    survivalDeps
	now     func() time.Time

	cacheOnce sync.Once
	cache     *climate.Cache
}

// NewSurvivalHandler wires the handler.
func NewSurvivalHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource, worlds *application.WorldCache,
	cities application.CityRepository, scale gametime.Scale, now func() time.Time,
) *SurvivalHandler {
	if uow == nil || ids == nil || source == nil || worlds == nil || cities == nil {
		panic("handlers: NewSurvivalHandler requires a unit of work, ids, content, a world and cities")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &SurvivalHandler{uow: uow, ids: ids, msgs: msgs, content: source, worlds: worlds, cities: cities, scale: scale, now: now}
}

// WithRules sets the rule date, the village's home reader, the care reader and the game clock.
func (h *SurvivalHandler) WithRules(r application.SurvivalRules, home HomeWarmer, care application.VillageCarer, clock gametime.Clock) *SurvivalHandler {
	h.deps = survivalDeps{rules: r, home: home, care: care, clock: clock}
	return h
}

// sctx is where a player stands and the numbers that apply there.
type sctx struct {
	s     application.FoundedSettlement
	def   content.ClimateDef
	sp    survival.Params
	cp    climate.Params
	cal   climate.Calendar
	place climate.Place
	seed  int64
	river bool
}

func (h *SurvivalHandler) envCache(def content.ClimateDef) *climate.Cache {
	h.cacheOnce.Do(func() { h.cache = climate.NewCache(max(def.Survival.CacheEntries, 1)) })
	return h.cache
}

func (c *sctx) envAt(cache *climate.Cache) func(time.Time) climate.Env {
	return func(t time.Time) climate.Env { return cache.At(c.seed, c.cal, c.cp, c.place, t) }
}

// where reads where the player stands: nil when survival does not apply (no rule yet, no climate, not a founded settlement).
func (h *SurvivalHandler) where(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, now time.Time) (*sctx, error) {
	if !h.deps.rules.On(now) || p.CityID == nil {
		return nil, nil
	}
	def, ok := snap.Climate()
	if !ok || len(def.Garments) == 0 {
		return nil, nil
	}
	s, err := tx.Settlements().ByID(ctx, *p.CityID)
	switch {
	case stderrors.Is(err, application.ErrCityNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}
	_, w, err := h.worlds.Active(ctx)
	if err != nil {
		return nil, err
	}
	if int(s.WorldCellID) >= len(w.Cells) || s.WorldCellID < 0 {
		return nil, nil
	}
	cell := w.Cells[s.WorldCellID]
	c := &sctx{s: s, def: def, sp: def.SurvivalParams(), cp: def.ClimateParams(), cal: climate.Calendar{Epoch: h.deps.clock.Epoch}, seed: int64(w.Seed),
		place: climate.Place{Key: int64(s.WorldCellID), BaseC100: int(cell.Temperature), LatDeg: cell.Point.LatDeg, PrecipMM: int(cell.Precipitation), Zone: s.Zone()}}
	c.river = nearWater(w, cell)
	return c, nil
}

// nearWater says a river or a lake lies in the cell or beside it.
func nearWater(w *worldgen.World, cell worldgen.Cell) bool {
	if cell.IsLake || cell.RiverFlow > 0 {
		return true
	}
	ids, _ := w.NearestCells(cell.Point.LatDeg, cell.Point.LonDeg, 7)
	for _, id := range ids {
		if int(id) < len(w.Cells) && (w.Cells[id].IsLake || w.Cells[id].RiverFlow > 0) {
			return true
		}
	}
	return false
}

// bodyOf is what the integration needs of the player: the garments worn (with their wear counted up to now), the shelter.
func (h *SurvivalHandler) bodyOf(ctx context.Context, tx application.Tx, c *sctx, p *application.Player, st *application.SurvivalState,
	now time.Time, save bool, burn bool,
) (survival.Body, []application.Garment, error) {
	var b survival.Body
	gs, err := tx.Survival().Garments(ctx, p.ID)
	if err != nil {
		return b, nil, err
	}
	kept := gs[:0:0]
	for _, g := range gs {
		if g.Worn {
			used := int(now.Sub(g.CountedAt) / time.Minute)
			if used > 0 {
				g.UsesLeft -= used
				g.CountedAt = g.CountedAt.Add(time.Duration(used) * time.Minute)
				if save {
					if err := tx.Survival().SetUses(ctx, g.ID, g.UsesLeft, g.CountedAt); err != nil {
						return b, nil, err
					}
				}
			}
			if g.UsesLeft <= 0 {
				continue // worn out
			}
			if d, ok := c.def.Garment(g.Item); ok {
				b.Worn = append(b.Worn, survival.Garment{Warmth: d.Warmth, WetBPS: d.WetBPS, HeatCutBPS: d.HeatCutBPS})
			}
		}
		kept = append(kept, g)
	}
	if st != nil {
		b.ShelterUntil, b.ShelterBonus = st.ShelterUntil, st.ShelterBonus
	}
	if h.deps.home != nil {
		from := now
		if st != nil {
			from = st.WarmthAt
		}
		nights := localNights(from, now, c.place.Zone, c.sp.NightFrom)
		bonus, err := h.deps.home.HomeWarmth(ctx, tx, c.s, p.ID, nights, c.def.Survival, burn && save, now)
		if err != nil {
			return b, nil, err
		}
		b.HomeBonus = bonus
	}
	return b, kept, nil
}

// localNights counts the local nights (their start at nightFrom o'clock) that began between from and to, at most two.
func localNights(from, to time.Time, zone time.Duration, nightFrom int) int {
	day := func(t time.Time) int64 {
		l := t.UTC().Add(zone).Add(-time.Duration(nightFrom) * time.Hour)
		return l.Unix() / 86400
	}
	n := int(day(to) - day(from))
	return min(max(n, 0), 2)
}

// state reads a player's survival state, a fresh one when there is none.
func (h *SurvivalHandler) state(ctx context.Context, tx application.Tx, p *application.Player, now time.Time) (*application.SurvivalState, error) {
	st, err := tx.Survival().State(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if st == nil {
		st = &application.SurvivalState{PlayerID: p.ID, ThirstAt: now, WarmthAt: now, ColdArmed: true, ThirstArmed: true}
	}
	return st, nil
}

// touched is the player's state brought to now, with what the survival rules did on the way.
type touched struct {
	c         *sctx
	st        *application.SurvivalState
	body      survival.Body
	env       climate.Env
	collapsed string
	kept      []application.Garment
}

// touch brings the player's thirst and warmth to now and writes them, ends the sicknesses that are over, makes the long cold a
// sickness, and collapses the player into a hospital stay if a need has given out. nil when survival does not apply.
func (h *SurvivalHandler) touch(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, meta envelope.Metadata,
	now time.Time, burn bool,
) (*touched, error) {
	c, err := h.where(ctx, tx, snap, p, now)
	if err != nil || c == nil {
		return nil, err
	}
	if _, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, now)); err != nil {
		return nil, err // the stats row first: the lock every activity takes
	}
	st, err := h.state(ctx, tx, p, now)
	if err != nil {
		return nil, err
	}
	cache := h.envCache(c.def)
	out := &touched{c: c, st: st, env: cache.At(c.seed, c.cal, c.cp, c.place, now)}
	// in a hospital bed: warm, watered, resting. The needs stand at a calm level and wait.
	if stay, err := hospitalised(ctx, tx, p.ID, now); err != nil {
		return nil, err
	} else if stay != nil {
		calm := int64(c.sp.Chilly-5) * survival.Milli
		st.ThirstMilli, st.WarmthMilli = min(st.ThirstMilli, calm), min(st.WarmthMilli, calm)
		st.ThirstAt, st.WarmthAt = now, now
		out.body, out.kept, err = h.bodyOf(ctx, tx, c, p, st, now, true, false)
		if err != nil {
			return nil, err
		}
		return out, tx.Survival().SaveState(ctx, *st)
	}
	body, kept, err := h.bodyOf(ctx, tx, c, p, st, now, true, burn)
	if err != nil {
		return nil, err
	}
	out.body, out.kept = body, kept
	rd := c.sp.Read(survival.Needs{Thirst: st.ThirstMilli, Warmth: st.WarmthMilli, ThirstAt: st.ThirstAt, WarmthAt: st.WarmthAt}, body, now, c.place.Zone, c.envAt(cache))
	st.ThirstMilli, st.WarmthMilli, st.ThirstAt, st.WarmthAt = rd.Thirst, rd.Warmth, now, now
	// sicknesses: those that are over end; a long cold becomes a chill
	ills, err := tx.Survival().Illnesses(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	for _, i := range ills {
		if !i.EndsAt.After(now) {
			if err := tx.Survival().End(ctx, i.ID, now); err != nil {
				return nil, err
			}
		}
	}
	if rd.ColdHours >= c.sp.ChillAfterHours {
		if _, err := tx.Survival().Fall(ctx, application.Illness{ID: h.ids.NewID(), PlayerID: p.ID, Kind: application.IllnessChill, OnsetAt: now,
			EndsAt: now.Add(time.Duration(c.def.Survival.ChillCourseHours) * time.Hour)}); err != nil {
			return nil, err
		}
	}
	// re-arm below the severe line; collapse at the extreme
	if c.sp.WarmthLevel(st.WarmthMilli) < survival.Freezing {
		st.ColdArmed = true
	}
	if c.sp.ThirstLevel(st.ThirstMilli) < 2 {
		st.ThirstArmed = true
	}
	cause := ""
	switch {
	case c.sp.Collapsed(st.WarmthMilli) && st.ColdArmed:
		cause, st.ColdArmed = application.CauseExposure, false
		st.WarmthMilli = int64(c.sp.Chilly-5) * survival.Milli
	case c.sp.Collapsed(st.ThirstMilli) && st.ThirstArmed:
		cause, st.ThirstArmed = application.CauseDehydration, false
		st.ThirstMilli = int64(c.sp.Chilly-5) * survival.Milli
	}
	if cause != "" {
		if _, err := injure(ctx, tx, snap, h.ids, h.scale, meta, hurt{playerID: p.ID, cityID: c.s.CityID, cause: cause,
			causeRef: "", damage: c.def.Survival.CollapseDamage}, now); err != nil {
			return nil, err
		}
		out.collapsed = cause
	}
	return out, tx.Survival().SaveState(ctx, *st)
}

// ShiftFactor is the share of a shift's output (basis points of 10000) the player's thirst, warmth and sickness leave them. It
// writes nothing: it reads the state forward to now. Survival not applying leaves the whole output.
func (h *SurvivalHandler) ShiftFactor(ctx context.Context, tx application.Tx, p *application.Player, now time.Time) (int64, error) {
	snap := h.content.Current()
	c, err := h.where(ctx, tx, snap, p, now)
	if err != nil || c == nil {
		return 10_000, err
	}
	st, err := h.state(ctx, tx, p, now)
	if err != nil {
		return 10_000, err
	}
	body, _, err := h.bodyOf(ctx, tx, c, p, st, now, false, false)
	if err != nil {
		return 10_000, err
	}
	rd := c.sp.Read(survival.Needs{Thirst: st.ThirstMilli, Warmth: st.WarmthMilli, ThirstAt: st.ThirstAt, WarmthAt: st.WarmthAt}, body, now, c.place.Zone,
		c.envAt(h.envCache(c.def)))
	ills, err := tx.Survival().Illnesses(ctx, p.ID)
	if err != nil {
		return 10_000, err
	}
	return survivalFactor(c.def.Survival, c.sp.WarmthLevel(rd.Warmth), c.sp.ThirstLevel(rd.Thirst), len(ills) > 0), nil
}

func survivalFactor(s content.SurvivalDef, w survival.Level, thirst int, ill bool) int64 {
	cut := 0
	switch w {
	case survival.Chilly:
		cut += s.ChillyBPS
	case survival.Cold:
		cut += s.ColdBPS
	case survival.Freezing:
		cut += s.FreezingBPS
	}
	switch thirst {
	case 1:
		cut += s.ThirstHighBPS
	case 2:
		cut += s.ThirstSevereBPS
	}
	if ill {
		cut += s.IllnessBPS
	}
	return int64(max(10_000-cut, 0))
}

func warmthName(l survival.Level) string {
	switch l {
	case survival.Chilly:
		return plife.WarmthChilly
	case survival.Cold:
		return plife.WarmthCold
	case survival.Freezing:
		return plife.WarmthFreezing
	}
	return plife.WarmthFine
}

// survivalRefusal carries a refusal out of a unit of work.
type survivalRefusal struct{ kind string }

func (r *survivalRefusal) Error() string { return "handlers: survival refused: " + r.kind }

// The refusal kinds survival adds to the health ones.
const (
	HealthRefusedNotFounded = "not_founded"
	HealthRefusedNoWater    = "no_water"
	HealthRefusedDrinkWait  = "drink_wait"
	HealthRefusedNoRiver    = "no_river"
	HealthRefusedNoFuel     = "no_fuel"
	HealthRefusedNoGarment  = "no_garment"
	HealthRefusedNotOpen    = "not_open"
	HealthRefusedNotIll     = "not_ill"
)

func (h *SurvivalHandler) finish(meta envelope.Metadata, lang string, err error) (*presentation.Response, error) {
	if err == nil {
		return nil, nil
	}
	var r *survivalRefusal
	if stderrors.As(err, &r) {
		return plife.HealthRefusal(presentation.Ctx{Lang: lang}, plife.HealthRefusalView{Kind: r.kind}), nil
	}
	return nil, err
}

// viewOf builds the needs screen from a touched player.
func (h *SurvivalHandler) viewOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, t *touched, notice string, now time.Time) (plife.SurvivalView, error) {
	v := plife.SurvivalView{Notice: notice}
	if t == nil {
		return v, nil
	}
	c := t.c
	v.Founded = true
	v.Thirst, v.Warmth = int(t.st.ThirstMilli/survival.Milli), int(t.st.WarmthMilli/survival.Milli)
	v.ThirstLevel, v.WarmthLevel = c.sp.ThirstLevel(t.st.ThirstMilli), warmthName(c.sp.WarmthLevel(t.st.WarmthMilli))
	if v.WarmthLevel == plife.WarmthFine && t.env.TempC100 > c.sp.HotAboveC100 {
		v.WarmthLevel = plife.WarmthHot // the hot band applies: water and shade, not wool
		if t.env.TempC100 > c.def.Survival.HeatstrokeAboveC*100 && v.ThirstLevel == 2 {
			v.WarmthLevel = plife.WarmthHeatstroke
		}
	}
	e := t.env
	v.Env = &plife.EnvLine{TempC: e.TempC100 / 100, Rain: e.Rain, Wind: e.Wind, Season: e.Season, Hour: e.Hour}
	cache := h.envCache(c.def)
	for _, d := range climate.Forecast(c.seed, c.cal, c.cp, c.place, now, 3, cache) {
		v.Env.Forecast = append(v.Env.Forecast, plife.ForecastDay{Offset: d.Offset, MinC: d.MinC100 / 100, MaxC: d.MaxC100 / 100,
			Rain: d.Rain, Wind: d.Wind, Snap: d.Snap, Spell: d.Spell})
	}
	if end, ok := climate.SnapEnd(c.seed, c.cal, c.cp, c.place, now, cache); ok {
		v.Env.ColdSnapUntil = &end
	}
	v.Sheltered = now.Before(t.st.ShelterUntil)
	line := func(g application.Garment) plife.GarmentLine {
		d, _ := c.def.Garment(g.Item)
		return plife.GarmentLine{ID: g.ID, Item: itemNamed(snap, g.Item), Slot: g.Slot, Warmth: d.Warmth, Left: time.Duration(g.UsesLeft) * time.Minute}
	}
	for _, g := range t.kept {
		if g.Worn {
			v.Worn = append(v.Worn, line(g))
		} else {
			v.Kept = append(v.Kept, line(g))
		}
	}
	stacks, _, err := tx.Items().Holdings(ctx, p.ID, application.HoldCarried)
	if err != nil {
		return v, err
	}
	for _, st := range stacks {
		if st.Item == c.def.Survival.WaterItem {
			v.Carried += st.Qty
		}
		if d, ok := c.def.Garment(st.Item); ok && st.Qty > 0 {
			v.Bag = append(v.Bag, plife.GarmentLine{Item: itemNamed(snap, st.Item), Slot: d.Slot, Warmth: d.Warmth, Qty: st.Qty})
		}
	}
	sort.Slice(v.Bag, func(i, j int) bool { return v.Bag[i].Item.Code < v.Bag[j].Item.Code })
	ills, err := tx.Survival().Illnesses(ctx, p.ID)
	if err != nil {
		return v, err
	}
	for _, i := range ills {
		v.Illness = append(v.Illness, plife.IllnessLine{Kind: i.Kind, EndsAt: i.EndsAt, Treated: i.MedicineUnits > 0})
	}
	buildings, err := tx.SettlementBuildings().List(ctx, c.s.CityID)
	if err != nil {
		return v, err
	}
	for _, b := range buildings {
		if b.Status == "complete" && b.TypeCode == "well" {
			v.WellOpen = true
		}
	}
	v.River = c.river
	if h.deps.care != nil {
		care, err := h.deps.care.CareHere(ctx, tx, c.s.CityID)
		if err != nil {
			return v, err
		}
		if care != nil {
			v.WarmingOpen = care.Warming.Open
			if len(ills) > 0 && (care.House.Open || care.Clinic.Open) && medicineFor(c.def.Survival, care.Stock) != "" {
				v.CanCure = untreated(ills)
			}
		}
	}
	return v, nil
}

func untreated(ills []application.Illness) bool {
	for _, i := range ills {
		if i.MedicineUnits == 0 {
			return true
		}
	}
	return false
}

// medicineFor is the first medicine of the list the stock holds.
func medicineFor(s content.SurvivalDef, stock map[string]int64) string {
	for _, m := range s.Medicines {
		if stock[m] >= 1 {
			return m
		}
	}
	return ""
}

// Needs handles health.needs: thirst, warmth, the weather, the dress and what can be done.
func (h *SurvivalHandler) Needs(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	return h.run(ctx, meta, "", func(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, t *touched, now time.Time) (string, error) {
		return "", nil
	})
}

// run is the frame of every command: the player, the touch, the action, the screen.
func (h *SurvivalHandler) run(ctx context.Context, meta envelope.Metadata, _ string,
	act func(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, t *touched, now time.Time) (string, error),
) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view plife.SurvivalView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		now := h.now()
		t, err := h.touch(ctx, tx, snap, p, meta, now, true)
		if err != nil {
			return err
		}
		if t == nil {
			view = plife.SurvivalView{Notice: plife.NoticeNotFounded}
			if r := h.deps.rules; r.Enabled() && !r.On(now) {
				at := r.StartsAt()
				view.StartsAt = &at // survival starts counting on this day (the rule date plus its grace)
			}
			return nil
		}
		notice := ""
		if t.collapsed != "" {
			notice = plife.NoticeCollapsed
		} else if notice, err = act(ctx, tx, snap, p, t, now); err != nil {
			return err
		}
		// the screen shows the state after the action
		st, err := tx.Survival().State(ctx, p.ID)
		if err != nil {
			return err
		}
		if st != nil {
			t.st = st
		}
		if t.kept, err = tx.Survival().Garments(ctx, p.ID); err != nil {
			return err
		}
		view, err = h.viewOf(ctx, tx, snap, p, t, notice, now)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return plife.Survival(presentation.Ctx{Lang: lang}, view), nil
}

// SurvivalRequest is the payload of the survival commands; the names are those internal/gateway/routing gives the arguments.
type SurvivalRequest struct {
	// Source is where a drink comes from: well, bag, river or boiled.
	Source string `json:"source,omitempty"`
	// Item is a garment's item code, or the id of one in the wardrobe; Slot the slot to undress.
	Item string `json:"item,omitempty"`
	Slot string `json:"slot,omitempty"`
}

func dieOf(seed int64, playerID string, at time.Time) int64 {
	f := fnv.New64a()
	_, _ = f.Write([]byte(playerID))
	return health.Die(seed, int64(f.Sum64()>>1), at.Unix())
}

// Drink handles health.drink.
func (h *SurvivalHandler) Drink(ctx context.Context, meta envelope.Metadata, req SurvivalRequest) (*presentation.Response, error) {
	return h.run(ctx, meta, "", func(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, t *touched, now time.Time) (string, error) {
		c := t.c
		s := c.def.Survival
		if last, err := tx.Items().LastUsed(ctx, p.ID, "drink"); err != nil {
			return "", err
		} else if !last.IsZero() && now.Sub(last) < s.DrinkEvery() {
			return "", &survivalRefusal{HealthRefusedDrinkWait}
		}
		sick := false
		note := plife.NoticeDrank
		switch req.Source {
		case plife.DrinkWell:
			buildings, err := tx.SettlementBuildings().List(ctx, c.s.CityID)
			if err != nil {
				return "", err
			}
			has := false
			for _, b := range buildings {
				has = has || (b.Status == "complete" && b.TypeCode == "well")
			}
			if !has {
				return "", &survivalRefusal{HealthRefusedNoWater}
			}
			org := application.SettlementOrg(c.s.CityID)
			if err := tx.Items().LockOrg(ctx, org); err != nil {
				return "", err
			}
			if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: s.WaterItem, Qty: 1, FromOrg: org, FromHolding: application.HoldWarehouse,
				Reason: application.ItemDrunk, ReferenceType: application.SurvivalReference, ReferenceID: p.ID, At: now}); err != nil {
				if isSentinel(err, application.ErrNotEnoughItems) {
					return "", &survivalRefusal{HealthRefusedNoWater}
				}
				return "", err
			}
		case plife.DrinkBag:
			if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
				return "", err
			}
			if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: s.WaterItem, Qty: 1, From: p.ID, FromHolding: application.HoldCarried,
				Reason: application.ItemDrunk, ReferenceType: application.SurvivalReference, ReferenceID: p.ID, At: now}); err != nil {
				if isSentinel(err, application.ErrNotEnoughItems) {
					return "", &survivalRefusal{HealthRefusedNoWater}
				}
				return "", err
			}
		case plife.DrinkRiver, plife.DrinkBoiled:
			if !c.river {
				return "", &survivalRefusal{HealthRefusedNoRiver}
			}
			if req.Source == plife.DrinkBoiled {
				if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
					return "", err
				}
				if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: s.BoilItem, Qty: 1, From: p.ID, FromHolding: application.HoldCarried,
					Reason: application.ItemBoiled, ReferenceType: application.SurvivalReference, ReferenceID: p.ID, At: now}); err != nil {
					if isSentinel(err, application.ErrNotEnoughItems) {
						return "", &survivalRefusal{HealthRefusedNoFuel}
					}
					return "", err
				}
				note = plife.NoticeRiverOK
			} else {
				chance := s.WaterborneBPS
				if t.env.TempC100 > s.WaterborneWarmAboveC*100 {
					chance = s.WaterborneWarmBPS
				}
				if dieOf(c.seed, p.ID, now) < int64(chance) {
					sick = true
				}
				note = plife.NoticeRiverOK
			}
		default:
			return "", &survivalRefusal{HealthRefusedNoWater}
		}
		if sick {
			if _, err := tx.Survival().Fall(ctx, application.Illness{ID: h.ids.NewID(), PlayerID: p.ID, Kind: application.IllnessWaterborne, OnsetAt: now,
				EndsAt: now.Add(time.Duration(s.WaterborneCourseHours) * time.Hour)}); err != nil {
				return "", err
			}
			note = plife.NoticeRiverSick
		}
		t.st.ThirstMilli = c.sp.Drink(t.st.ThirstMilli)
		if c.sp.ThirstLevel(t.st.ThirstMilli) < 2 {
			t.st.ThirstArmed = true
		}
		if err := tx.Survival().SaveState(ctx, *t.st); err != nil {
			return "", err
		}
		return note, tx.Items().MarkUsed(ctx, p.ID, "drink", now)
	})
}

// Wear handles health.wear: a garment from the bag or from the wardrobe is put on, and the one on that slot is taken off.
func (h *SurvivalHandler) Wear(ctx context.Context, meta envelope.Metadata, req SurvivalRequest) (*presentation.Response, error) {
	return h.run(ctx, meta, "", func(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, t *touched, now time.Time) (string, error) {
		c := t.c
		var g application.Garment
		found := false
		for _, k := range t.kept {
			if k.ID == req.Item {
				g, found = k, true
			}
		}
		if !found {
			d, ok := c.def.Garment(req.Item)
			if !ok {
				return "", &survivalRefusal{HealthRefusedNoGarment}
			}
			if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
				return "", err
			}
			if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: d.Item, Qty: 1, From: p.ID, FromHolding: application.HoldCarried,
				Reason: application.ItemGarmentWorn, ReferenceType: application.SurvivalReference, ReferenceID: p.ID, At: now}); err != nil {
				if isSentinel(err, application.ErrNotEnoughItems) {
					return "", &survivalRefusal{HealthRefusedNoGarment}
				}
				return "", err
			}
			g = application.Garment{ID: h.ids.NewID(), PlayerID: p.ID, Item: d.Item, Slot: d.Slot, UsesLeft: d.DurabilityDays * 24 * 60,
				CountedAt: now, CreatedAt: now}
		}
		if g.Worn {
			return plife.NoticeDressed, nil
		}
		for _, k := range t.kept {
			if k.Worn && k.Slot == g.Slot {
				if err := tx.Survival().SetWorn(ctx, k.ID, false, now); err != nil {
					return "", err
				}
			}
		}
		if !found {
			g.Worn = true
			if err := tx.Survival().AddGarment(ctx, g); err != nil {
				return "", err
			}
		} else if err := tx.Survival().SetWorn(ctx, g.ID, true, now); err != nil {
			return "", err
		}
		return plife.NoticeDressed, nil
	})
}

// Remove handles health.remove: the garment on a slot is taken off and kept in the wardrobe.
func (h *SurvivalHandler) Remove(ctx context.Context, meta envelope.Metadata, req SurvivalRequest) (*presentation.Response, error) {
	return h.run(ctx, meta, "", func(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, t *touched, now time.Time) (string, error) {
		for _, k := range t.kept {
			if k.Worn && k.Slot == req.Slot {
				return plife.NoticeUndressed, tx.Survival().SetWorn(ctx, k.ID, false, now)
			}
		}
		return "", &survivalRefusal{HealthRefusedNoGarment}
	})
}

// Warm handles health.warm: on a day the warming house is open the player warms himself at its hearth and is sheltered for some hours.
func (h *SurvivalHandler) Warm(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	return h.run(ctx, meta, "", func(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, t *touched, now time.Time) (string, error) {
		if h.deps.care == nil {
			return "", &survivalRefusal{HealthRefusedNotOpen}
		}
		care, err := h.deps.care.CareHere(ctx, tx, t.c.s.CityID)
		if err != nil {
			return "", err
		}
		if care == nil || !care.Warming.Open {
			return "", &survivalRefusal{HealthRefusedNotOpen}
		}
		until := now.Add(time.Duration(t.c.def.Survival.ShelterHours) * time.Hour)
		if until.After(t.st.ShelterUntil) || t.c.def.Survival.WarmingBonus > t.st.ShelterBonus {
			t.st.ShelterUntil, t.st.ShelterBonus = until, t.c.def.Survival.WarmingBonus
		}
		// what the fire gave counts from now: the warmth already read is kept, the drift from here sees the shelter
		return plife.NoticeWarmed, tx.Survival().SaveState(ctx, *t.st)
	})
}

// Cure handles health.cure: a health house or a clinic that is open treats a sickness with a medicine of the settlement stock.
func (h *SurvivalHandler) Cure(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	return h.run(ctx, meta, "", func(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, t *touched, now time.Time) (string, error) {
		ills, err := tx.Survival().Illnesses(ctx, p.ID)
		if err != nil {
			return "", err
		}
		var ill *application.Illness
		for i := range ills {
			if ills[i].MedicineUnits == 0 {
				ill = &ills[i]
				break
			}
		}
		if ill == nil {
			return "", &survivalRefusal{HealthRefusedNotIll}
		}
		if h.deps.care == nil {
			return "", &survivalRefusal{HealthRefusedNotOpen}
		}
		org := application.SettlementOrg(t.c.s.CityID)
		if err := tx.Items().LockOrg(ctx, org); err != nil {
			return "", err
		}
		care, err := h.deps.care.CareHere(ctx, tx, t.c.s.CityID)
		if err != nil {
			return "", err
		}
		if care == nil || !(care.House.Open || care.Clinic.Open) {
			return "", &survivalRefusal{HealthRefusedNotOpen}
		}
		building := care.House.BuildingID
		if care.Clinic.Open {
			building = care.Clinic.BuildingID
		}
		med := medicineFor(t.c.def.Survival, care.Stock)
		if med == "" {
			return "", &survivalRefusal{plife.HealthRefusedNoMedicine}
		}
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: med, Qty: 1, FromOrg: org, FromHolding: application.HoldWarehouse,
			Reason: application.ItemMedicineUsed, ReferenceType: application.SurvivalReference, ReferenceID: ill.ID, At: now}); err != nil {
			if isSentinel(err, application.ErrNotEnoughItems) {
				return "", &survivalRefusal{plife.HealthRefusedNoMedicine}
			}
			return "", err
		}
		left := ill.EndsAt.Sub(now)
		ends := now.Add(left * time.Duration(100-t.c.def.Survival.TreatCutPercent) / 100)
		return plife.NoticeCured, tx.Survival().Treat(ctx, ill.ID, ends, med, 1, t.c.s.CityID, building, now)
	})
}
