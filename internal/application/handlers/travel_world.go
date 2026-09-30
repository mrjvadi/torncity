package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
	"github.com/mrjvadi/torncity/internal/domain/travel"
	"github.com/mrjvadi/torncity/internal/domain/world"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// World-derived travel (docs/adr/0034-world-travel.md).
//
// A founded village has no route in routes.yml: it stands on a cell of the
// generated world. So a journey to or from one is priced from where the two
// places stand — the great-circle distance between their spots — by the modes
// config travel.world_reach lets serve such a journey, at those modes' own
// speed and fare per kilometre from transport.yml. A content city (Support)
// has no cell either; config travel.city_locations gives it a spot.
//
// Every founded village is somewhere anyone may go: a traveller is not a
// settler (residence is settlement.join, a different act) and visiting takes
// nothing from the village, so beginner protection does not close its door.

// GeoPoint is a spot on the planet, in degrees.
type GeoPoint struct{ LatDeg, LonDeg float64 }

// ActiveWorld is the planet a founded village stands on
// (*application.WorldCache).
type ActiveWorld interface {
	Active(ctx context.Context) (application.World, *worldgen.World, error)
}

// WorldTransport is the live content a derived journey is priced from,
// read per request like every other content answer.
type WorldTransport interface {
	// Derived is every mode that serves a journey of distanceKM (config
	// travel.world_reach) and the content version it answers from.
	Derived(distanceKM int) (options []TransportOption, contentVersion int)
	// EmblemText is a village's emblem as emoji.
	EmblemText(e application.EmblemCodes) string
}

// WorldRoutes places cities on the world and derives journeys between them.
type WorldRoutes struct {
	world     ActiveWorld
	locations map[string]GeoPoint
	radiusKM  float64
	timeScale int
	transport WorldTransport
}

// NewWorldRoutes wires world-derived travel. locations are the content
// cities' spots by code (config travel.city_locations), radiusKM the planet's
// radius, timeScale the game clock.
func NewWorldRoutes(w ActiveWorld, locations map[string]GeoPoint, radiusKM float64, timeScale int, t WorldTransport) *WorldRoutes {
	if w == nil || t == nil {
		panic("handlers: NewWorldRoutes requires a world and a transport")
	}
	if radiusKM <= 0 {
		panic("handlers: NewWorldRoutes requires a positive planet radius")
	}
	if timeScale < 1 || timeScale > travel.MaxTimeScale {
		panic("handlers: NewWorldRoutes requires a time scale within 1..travel.MaxTimeScale")
	}
	return &WorldRoutes{world: w, locations: locations, radiusKM: radiusKM, timeScale: timeScale, transport: t}
}

// spotOf is where a founded settlement's lot grid is centred.
func spotOf(wg *worldgen.World, s application.FoundedSettlement) (GeoPoint, bool) {
	if wg == nil || s.WorldCellID < 0 || int(s.WorldCellID) >= len(wg.Cells) {
		return GeoPoint{}, false
	}
	pt := wg.Cells[s.WorldCellID].Point
	lat, lon := wsettle.GridCentre(wg, pt.LatDeg, pt.LonDeg, s.GridShiftX, s.GridShiftY)
	return GeoPoint{LatDeg: lat, LonDeg: lon}, true
}

// activeWorld is the planet, or nil when none has been created.
func (w *WorldRoutes) activeWorld(ctx context.Context) (*worldgen.World, error) {
	_, wg, err := w.world.Active(ctx)
	if stderrors.Is(err, application.ErrNoActiveWorld) {
		return nil, nil
	}
	return wg, err
}

// Locate says where a city stands: a content city by its configured spot, a
// founded settlement by its cell. false when it stands nowhere on the world.
func (w *WorldRoutes) Locate(ctx context.Context, tx application.Tx, c application.City) (GeoPoint, bool, error) {
	if p, ok := w.locations[c.Code]; ok {
		return p, true, nil
	}
	s, err := tx.Settlements().ByID(ctx, c.ID)
	if stderrors.Is(err, application.ErrCityNotFound) {
		return GeoPoint{}, false, nil
	}
	if err != nil {
		return GeoPoint{}, false, err
	}
	wg, err := w.activeWorld(ctx)
	if err != nil || wg == nil {
		return GeoPoint{}, false, err
	}
	p, ok := spotOf(wg, s)
	return p, ok, nil
}

func (w *WorldRoutes) distance(a, b GeoPoint) int {
	d := world.GreatCircleKM(a.LatDeg, a.LonDeg, b.LatDeg, b.LonDeg, w.radiusKM)
	return max(d, 1)
}

// Options are the ways to make the journey between two cities the world
// puts apart: none when either stands nowhere or no listed mode reaches so
// far.
func (w *WorldRoutes) Options(ctx context.Context, tx application.Tx, from, to application.City) ([]TransportOption, int, error) {
	a, ok, err := w.Locate(ctx, tx, from)
	if err != nil || !ok {
		return nil, 0, err
	}
	b, ok, err := w.Locate(ctx, tx, to)
	if err != nil || !ok {
		return nil, 0, err
	}
	opts, version := w.transport.Derived(w.distance(a, b))
	return opts, version, nil
}

// Destination is one place a traveller may go, priced from where they stand.
type Destination struct {
	City       application.City
	Settlement *application.FoundedSettlement
	DistanceKM int
	Point      GeoPoint
	// Fare is the cheapest way there and Wait the fastest.
	Fare   int64
	Wait   time.Duration
	Emblem string
}

// Destinations lists every place reachable from origin over the world:
// content cities that have a spot, then founded villages, each group nearest
// first. Places no listed mode reaches are left out.
func (w *WorldRoutes) Destinations(ctx context.Context, tx application.Tx, origin application.City, cities []application.City) ([]Destination, error) {
	from, ok, err := w.Locate(ctx, tx, origin)
	if err != nil || !ok {
		return nil, err
	}
	var out []Destination
	add := func(d Destination) {
		opts, _ := w.transport.Derived(d.DistanceKM)
		if len(opts) == 0 {
			return
		}
		for i, o := range opts {
			q, err := travel.QuoteJourney(worldCity(origin), worldCity(d.City), o.Mode, o.DistanceKM,
				travel.Pricing{PolicyBPS: travel.BasisPoints}, w.timeScale)
			if err != nil {
				continue
			}
			if i == 0 || q.Fare.Minor() < d.Fare {
				d.Fare = q.Fare.Minor()
			}
			if d.Wait == 0 || q.Wait < d.Wait {
				d.Wait = q.Wait
			}
		}
		if d.Wait > 0 {
			out = append(out, d)
		}
	}
	for _, c := range cities {
		if c.ID == origin.ID {
			continue
		}
		if p, ok := w.locations[c.Code]; ok {
			add(Destination{City: c, Point: p, DistanceKM: w.distance(from, p)})
		}
	}
	founded, err := tx.Settlements().Founded(ctx)
	if err != nil {
		return nil, err
	}
	if len(founded) > 0 {
		wg, err := w.activeWorld(ctx)
		if err != nil {
			return nil, err
		}
		for i := range founded {
			s := founded[i]
			if s.CityID == origin.ID {
				continue
			}
			p, ok := spotOf(wg, s)
			if !ok {
				continue
			}
			add(Destination{
				City:       application.City{ID: s.CityID, Code: s.Code, Name: s.Name, Tier: s.Tier},
				Settlement: &s, Point: p, DistanceKM: w.distance(from, p),
				Emblem: w.transport.EmblemText(s.Emblem),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Settlement == nil) != (out[j].Settlement == nil) {
			return out[i].Settlement == nil
		}
		if out[i].DistanceKM != out[j].DistanceKM {
			return out[i].DistanceKM < out[j].DistanceKM
		}
		return out[i].City.Code < out[j].City.Code
	})
	return out, nil
}

// WithWorld makes the handler price journeys the content has no route for
// from where the two cities stand on the world.
func (h *TravelHandler) WithWorld(w *WorldRoutes) *TravelHandler {
	h.world = w
	return h
}

// WithWorld lists, beside the cities a content route reaches, every place the
// world lets a traveller go.
func (h *MapHandler) WithWorld(w *WorldRoutes) *MapHandler {
	h.world = w
	return h
}

// worldOptions is planTrip's fallback when no content route joins two
// cities.
func (h *TravelHandler) worldOptions(ctx context.Context, tx application.Tx, from, to application.City) ([]TransportOption, int, error) {
	if h.world == nil {
		return nil, 0, nil
	}
	return h.world.Options(ctx, tx, from, to)
}

// Here handles travel.here: «سفر به این روستا» typed in a village's own group.
// The player is offered the ways to that group's village straight away, from
// wherever they stand, with no list to search.
//
// It never posts to the group. The fares are the player's own (their
// vehicle, their purse), so the answer goes to their private chat and the
// group sees one neutral line (configs/commands.yml, reply: private).
func (h *TravelHandler) Here(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	if meta.TelegramUserID == 0 {
		return nil, errors.InvalidInput("request carries no telegram user")
	}
	var (
		view    screens.TravelHereView
		village application.FoundedSettlement
		lang    = meta.Language
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		if !meta.InGroup() {
			view.Reason = screens.TravelHereGroupOnly
			return nil
		}
		village, err = tx.Settlements().ByFoundingGroup(ctx, meta.TelegramChatID)
		if stderrors.Is(err, application.ErrCityNotFound) {
			view.Reason = screens.TravelHereNoVillage
			return nil
		}
		if err != nil {
			return err
		}
		if p.CityID != nil && *p.CityID == village.CityID {
			view.Reason, view.Village, view.VillageCode = screens.TravelHereAlreadyThere, village.Name, village.Code
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	private := func(r *presenter.Response) *presenter.Response { return r.MarkPrivate() }
	if view.Reason != "" {
		return private(screens.TravelHere(screens.Context{Msgs: h.msgs, Lang: lang}, view)), nil
	}
	resp, err := h.options(ctx, meta, TravelOptionsRequest{City: village.Code}, false)
	if err != nil {
		return nil, err
	}
	return private(resp), nil
}

// emblemText is a village's emblem as emoji, as the founding form writes it:
// the shape's, the icon's, then a dot per colour.
func emblemText(def content.FoundingDef, e application.EmblemCodes) string {
	emoji := func(list []content.FoundingChoiceDef, code string) string {
		for _, c := range list {
			if c.Code == code {
				return c.Emoji
			}
		}
		return ""
	}
	var parts []string
	for _, p := range []string{
		emoji(def.Shapes, e.Shape), emoji(def.Icons, e.Icon), emoji(def.Palette, e.ColorA) + emoji(def.Palette, e.ColorB),
	} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}

// EmblemTextOf renders an emblem from a content snapshot; "" when the content
// has no founding section.
func EmblemTextOf(snap *content.Snapshot, e application.EmblemCodes) string {
	def, ok := snap.Founding()
	if !ok {
		return ""
	}
	return emblemText(def, e)
}
