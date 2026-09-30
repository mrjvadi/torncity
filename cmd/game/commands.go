package main

import (
	"context"
	"fmt"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/world"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// commandFunc runs one command and returns what the player should see.
//
// A nil response with a nil error is legitimate: a replayed arrival, for one,
// has nothing to announce twice.
type commandFunc func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error)

// phaseHandlers is every use case this process serves.
type phaseHandlers struct {
	profile     *handlers.ProfileHandler
	travel      *handlers.TravelHandler
	skills      *handlers.SkillsHandler
	social      *handlers.SocialHandler
	worldMap    *handlers.MapHandler
	settings    *handlers.SettingsHandler
	bank        *handlers.BankHandler
	gov         *handlers.GovernanceHandler
	settlements *handlers.SettlementsHandler
	village     *handlers.VillageHandler
	presence    *handlers.PresenceHandler

	jobs      *handlers.JobsHandler
	education *handlers.EducationHandler

	crime *handlers.CrimeHandler

	places *handlers.PlacesHandler

	goods goodsHandlers

	companies  *handlers.CompaniesHandler
	production *handlers.ProductionHandler
	recruit    *handlers.RecruitHandler

	military     *handlers.MilitaryHandler
	diplomacy    *handlers.DiplomacyHandler
	appointments *handlers.AppointmentHandler
	war          *handlers.WarHandler

	stageE stageEHandlers
	stageF stageFHandlers

	stageG1 lifeHandlers
	stageG2 financeHandlers

	clients deviceHandlers

	inbox inboxHandlers
}

// bind maps every subscribed command to the handler method that serves it.
//
// The map is keyed by the same domain.action spelling the subscription table
// uses, and bindAll checks the two cover each other exactly: a subscription
// with no binding would ack messages it never ran, and a binding with no
// subscription is a command nobody can reach.
func (h phaseHandlers) bind() map[string]commandFunc {
	bound := map[string]commandFunc{
		"player.profile.get": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.profile.Handle(ctx, env.Metadata)
		},
		"player.settings": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.settings.Show(ctx, env.Metadata)
		},
		"player.presence.set": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.PresenceRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.settings.SetPresence(ctx, env.Metadata, req)
		},
		"settlement.who": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.presence.Who(ctx, env.Metadata)
		},
		"player.language.set": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.LanguageRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.settings.SetLanguage(ctx, env.Metadata, req)
		},

		"travel.start": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.StartTravelRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.travel.Start(ctx, env.Metadata, req)
		},
		"travel.options": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.TravelOptionsRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.travel.Options(ctx, env.Metadata, req)
		},
		"travel.status": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.travel.Status(ctx, env.Metadata)
		},
		"travel.here": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.travel.Here(ctx, env.Metadata)
		},
		// The scheduler's dispatch payload, decoded into the request the
		// handler declares for it. Both sides name the same json fields; the
		// handler's tests pin its half and the scheduler's tests pin theirs.
		"travel.arrive": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.ArriveTravelRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.travel.Complete(ctx, env.Metadata, req)
		},

		"skills.list": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.skills.List(ctx, env.Metadata)
		},
		"map.list": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.places.Map(ctx, env.Metadata)
		},
		"map.cities": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.PageRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.worldMap.List(ctx, env.Metadata, req)
		},
		"place.go": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.PlaceRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.places.Go(ctx, env.Metadata, req)
		},
		"place.arrive": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.PlaceScheduledRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.places.Arrive(ctx, env.Metadata, req)
		},

		"social.search": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.SearchRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.social.Search(ctx, env.Metadata, req)
		},
		"social.friend.add": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.FriendRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.social.FriendAdd(ctx, env.Metadata, req)
		},
		"social.friend.accept": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.FriendRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.social.FriendAccept(ctx, env.Metadata, req)
		},
		"social.friend.list": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.PageRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.social.FriendList(ctx, env.Metadata, req)
		},

		"bank.show": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.bank.Show(ctx, env.Metadata)
		},
		"bank.deposit": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.BankAmountRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.bank.Deposit(ctx, env.Metadata, req)
		},
		"bank.withdraw": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.BankAmountRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.bank.Withdraw(ctx, env.Metadata, req)
		},
		"bank.pay": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.PayRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.bank.Pay(ctx, env.Metadata, req)
		},
		"bank.pay.send": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.PayRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.bank.PaySend(ctx, env.Metadata, req)
		},

		"gov.city": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.GovCityRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			// The village is the home of a group that has one: its «شهر»
			// is the village, not a city hall it does not have.
			if req.City == "" {
				if resp, ok, err := h.village.HomeIfVillage(ctx, env.Metadata); err != nil || ok {
					return resp, err
				}
			}
			return h.gov.City(ctx, env.Metadata, req)
		},
		"gov.history": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.GovHistoryRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.gov.History(ctx, env.Metadata, req)
		},
		"gov.office": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.gov.Office(ctx, env.Metadata)
		},
		"gov.lever": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.GovLeverRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.gov.Lever(ctx, env.Metadata, req)
		},
		"gov.confirm": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.GovLeverRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.gov.Confirm(ctx, env.Metadata, req)
		},
		"gov.set": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.GovLeverRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.gov.Set(ctx, env.Metadata, req)
		},

		"settlement.found": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.settlements.Found(ctx, env.Metadata)
		},
		"settlement.found.draft": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.FoundDraftRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.settlements.FoundDraft(ctx, env.Metadata, req)
		},
		"settlement.found.submit": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.FoundSubmitRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.settlements.Submit(ctx, env.Metadata, req)
		},

		// Village-level knowledge and construction (docs/adr/0031), K2/W5.
		"settlement.home": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.village.Home(ctx, env.Metadata)
		},
		"settlement.overview": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.village.Overview(ctx, env.Metadata)
		},
		"settlement.join": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageJoinRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.Join(ctx, env.Metadata, req)
		},
		"settlement.promotion.view": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.village.PromotionView(ctx, env.Metadata)
		},
		"settlement.promote": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillagePromoteRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.Promote(ctx, env.Metadata, req)
		},
		"settlement.donate": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageDonateRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.Donate(ctx, env.Metadata, req)
		},
		"settlement.leave": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageJoinRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.Leave(ctx, env.Metadata, req)
		},
		"settlement.knowledge": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.village.KnowledgeList(ctx, env.Metadata)
		},
		"settlement.knowledge.research": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageKnowledgeRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.Research(ctx, env.Metadata, req)
		},
		"settlement.knowledge.buy": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageKnowledgeRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.Buy(ctx, env.Metadata, req)
		},
		"settlement.build": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.village.BuildMenu(ctx, env.Metadata)
		},
		"settlement.build.lots": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageLotsRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.Lots(ctx, env.Metadata, req)
		},
		"settlement.build.place": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageBuildRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.Place(ctx, env.Metadata, req)
		},
		"settlement.materials": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.village.Materials(ctx, env.Metadata)
		},
		"settlement.materials.buy": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageMaterialRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.MaterialsBuy(ctx, env.Metadata, req)
		},
		"settlement.work": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageWorkRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.Work(ctx, env.Metadata, req)
		},
		"settlement.labor.board": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.village.LaborBoard(ctx, env.Metadata)
		},
		"settlement.labor.mine": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.village.LaborMine(ctx, env.Metadata)
		},
		"settlement.labor.site": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageLaborRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.LaborSite(ctx, env.Metadata, req)
		},
		"settlement.labor.take": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageLaborRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.LaborTake(ctx, env.Metadata, req)
		},
		"settlement.labor.hire": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageLaborRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.LaborHire(ctx, env.Metadata, req)
		},
		"settlement.labor.wage": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageLaborRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.LaborWage(ctx, env.Metadata, req)
		},
		"settlement.labor.close": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageLaborRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.LaborClose(ctx, env.Metadata, req)
		},
		"settlement.labor.post": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageLaborRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.LaborPost(ctx, env.Metadata, req)
		},
		"settlement.worked": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.CrimeScheduledRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.Worked(ctx, env.Metadata, req)
		},
		"settlement.build.progress": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			return h.village.Progress(ctx, env.Metadata)
		},
		"settlement.build.cancel": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageBuildingRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.Cancel(ctx, env.Metadata, req)
		},
		"settlement.build.demolish": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.VillageBuildingRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.Demolish(ctx, env.Metadata, req)
		},
		// The scheduler's own dispatch payload, decoded into the request the
		// handler declares for it, exactly travel.arrive and
		// company.researched already do.
		"settlement.researched": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.CrimeScheduledRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.Researched(ctx, env.Metadata, req)
		},
		"settlement.taught": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.CrimeScheduledRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.Taught(ctx, env.Metadata, req)
		},
		"settlement.built": func(ctx context.Context, env *envelope.Envelope) (*presenter.Response, error) {
			var req handlers.CrimeScheduledRequest
			if err := decode(env, &req); err != nil {
				return nil, err
			}
			return h.village.Built(ctx, env.Metadata, req)
		},
	}
	// Work and study are bound in commands_work.go.
	for command, fn := range h.bindWork() {
		bound[command] = fn
	}
	// Crime is bound in commands_crime.go.
	for command, fn := range h.bindCrime() {
		bound[command] = fn
	}
	// Goods are bound in commands_goods.go.
	for command, fn := range h.bindGoods() {
		bound[command] = fn
	}
	for command, fn := range h.bindCompanies() {
		bound[command] = fn
	}
	for command, fn := range h.bindProduction() {
		bound[command] = fn
	}
	for command, fn := range h.bindRecruit() {
		bound[command] = fn
	}
	for command, fn := range h.bindMilitary() {
		bound[command] = fn
	}
	for command, fn := range h.bindStageE() {
		bound[command] = fn
	}
	for command, fn := range h.bindStageF() {
		bound[command] = fn
	}
	for command, fn := range h.bindLife() {
		bound[command] = fn
	}
	for command, fn := range h.bindFinance() {
		bound[command] = fn
	}
	for command, fn := range h.bindDevices() {
		bound[command] = fn
	}
	for command, fn := range h.bindInbox() {
		bound[command] = fn
	}
	return bound
}

// bindAll pairs every subscription with its handler, or explains which side
// is missing. It runs before anything subscribes, so a mismatch stops the
// process at startup instead of acking a stream of commands nothing ran.
func bindAll(subs []commands.Subscription, bound map[string]commandFunc) (map[string]commandFunc, error) {
	out := make(map[string]commandFunc, len(subs))
	for _, sub := range subs {
		fn, ok := bound[sub.Command()]
		if !ok {
			return nil, fmt.Errorf("game: %s is subscribed but no handler serves it", sub.Command())
		}
		out[sub.Command()] = fn
	}
	for command := range bound {
		if _, ok := out[command]; !ok {
			return nil, fmt.Errorf("game: %s has a handler but no subscription delivers it", command)
		}
	}
	return out, nil
}

// runnerFor runs one command from the bound table for a handler that has to
// run another command on the player's behalf (a walk's follow-up,
// internal/application/handlers/places_then.go). A command not in the table
// is refused as bad input.
func runnerFor(bound map[string]commandFunc) handlers.CommandRunner {
	return func(ctx context.Context, meta envelope.Metadata, command string, payload map[string]string) (*presenter.Response, error) {
		fn, ok := bound[command]
		if !ok {
			return nil, apperrors.InvalidInput("no such command to run")
		}
		if payload == nil {
			payload = map[string]string{}
		}
		meta.Command = command
		env, err := envelope.New(meta, payload)
		if err != nil {
			return nil, apperrors.InvalidInput("cannot run the command").WithCause(err)
		}
		return fn(ctx, env)
	}
}

// decode reads a command's payload into its request type.
//
// A payload that does not decode is the sender's mistake and will be the same
// mistake on every redelivery, so it is classified as bad input rather than
// returned raw, which the service would read as a fault worth retrying.
func decode(env *envelope.Envelope, dst any) error {
	if len(env.Payload) == 0 {
		return nil
	}
	if err := env.Decode(dst); err != nil {
		return apperrors.InvalidInput("command payload is not readable").WithCause(err)
	}
	return nil
}

// liveTransport answers which transport modes connect two cities from
// whatever content snapshot is current at the moment of the request.
//
// Each call reads the registry ONCE, so the options it returns and the content
// version it reports come from one snapshot; a content reload is a pointer
// swap in the registry with nothing to change here.
type liveTransport struct {
	registry *content.Registry
}

func (t liveTransport) Options(from, to string) ([]handlers.TransportOption, int) {
	snap := t.registry.Current()
	opts := snap.TransportOptions(from, to)
	out := make([]handlers.TransportOption, 0, len(opts))
	for _, o := range opts {
		out = append(out, handlers.TransportOption{Mode: o.Mode, Name: o.Name, DistanceKM: o.DistanceKM,
			Accepts: snap.ModeAccepts(o.Mode.Code)})
	}
	return out, snap.Version()
}

// liveWorldTransport prices the journeys the world derives (a founded village
// has no route in routes.yml) from the current content snapshot: the modes
// config travel.world_reach lets serve them, and the emblem of a village.
type liveWorldTransport struct {
	registry *content.Registry
	reach    map[string]int
}

func (t liveWorldTransport) Derived(distanceKM int) ([]handlers.TransportOption, int) {
	snap := t.registry.Current()
	opts := snap.DerivedTransport(distanceKM, t.reach)
	out := make([]handlers.TransportOption, 0, len(opts))
	for _, o := range opts {
		out = append(out, handlers.TransportOption{Mode: o.Mode, Name: o.Name, DistanceKM: o.DistanceKM,
			Accepts: snap.ModeAccepts(o.Mode.Code)})
	}
	return out, snap.Version()
}

func (t liveWorldTransport) EmblemText(e application.EmblemCodes) string {
	return handlers.EmblemTextOf(t.registry.Current(), e)
}

// worldRoutes builds the world-derived travel from configuration.
func worldRoutes(cfg *config.Config, cache *application.WorldCache, registry *content.Registry) (*handlers.WorldRoutes, error) {
	spots, err := cfg.Travel.CityLocationMap()
	if err != nil {
		return nil, err
	}
	reach, err := cfg.Travel.WorldReachMap()
	if err != nil {
		return nil, err
	}
	locations := make(map[string]handlers.GeoPoint, len(spots))
	for code, s := range spots {
		locations[code] = handlers.GeoPoint{LatDeg: s.LatDeg, LonDeg: s.LonDeg}
	}
	return handlers.NewWorldRoutes(cache, locations, cfg.WorldGen.PlanetRadiusKm, cfg.Game.TimeScale,
		liveWorldTransport{registry: registry, reach: reach}), nil
}

// liveRoutes is the route network the map screen reads, for the same reason.
// A destination is listed when some transport mode reaches it, at the
// shortest distance any mode offers: a city only a road nobody travels
// reaches is not somewhere the player can go.
type liveRoutes struct {
	registry *content.Registry
}

func (r liveRoutes) DistanceBetween(from, to string) (int, error) {
	snap := r.registry.Current()
	if d, ok := snap.NearestByAnyMode(from, to); ok {
		return d, nil
	}
	return 0, fmt.Errorf("%w: no transport mode connects %q and %q", world.ErrNoRoute, from, to)
}

func (r liveRoutes) Has(code string) bool { return r.registry.Routes().Has(code) }

// storeLanguages offers the languages of whatever catalogue the store holds
// at the moment of the request, for the same reason livePlanner reads the
// registry per request: a reloaded catalogue with a new locale in it is then
// offered on the settings screen without anything here changing.
type storeLanguages struct {
	store *i18n.Store
}

func (s storeLanguages) Languages() []string { return s.store.Catalog().Languages() }

// playerReader is the one read the refusal path needs.
type playerReader interface {
	GetByTelegramUserID(ctx context.Context, telegramUserID int64) (*application.Player, error)
}

// refusalLanguage is the language a refusal is written in: the player's
// stored choice, by the same rule every handler follows
// (handlers.RenderLanguage).
//
// A refused command never reaches the handler's render, so the choice is made
// again here. The read happens only on this path, never for a command that
// succeeded, and a read that fails costs nothing but the language: the
// refusal still goes out, in the Telegram client's.
func (s *service) refusalLanguage(ctx context.Context, meta envelope.Metadata) string {
	if s.players == nil || meta.TelegramUserID == 0 {
		return meta.Language
	}
	p, err := s.players.GetByTelegramUserID(ctx, meta.TelegramUserID)
	if err != nil {
		return handlers.RenderLanguage(meta, nil)
	}
	return handlers.RenderLanguage(meta, p)
}
