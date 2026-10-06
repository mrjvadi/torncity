package handlers

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Training (docs/research/2026-10-03-activities-audit.md section 7).
//
// A session spends energy and gives stamina (which raises the most energy a player
// holds, with diminishing returns) and strength (a skill the crimes that need force
// read). Where it happens decides how well it trains:
//
//   - open ground: bodyweight exercise (press-ups, squats, running) needs no building
//     and no instructor; it is free and trains at training.yard_bps;
//   - a settlement's training ground: a levelled yard with an experienced trainer (upkeep
//     pays the trainer and the tools); a session costs training.ground_fee to the
//     treasury (training_fee) and trains at training.ground_bps (ADR 0038 4.6: 60 %);
//   - the neutral city's gym: full efficiency, the fee goes to the sink (service_fee).
//
// Recovery between sessions is the energy that has to come back (energy regenerates
// slowly, so sessions cannot be stacked), as the training literature asks for rest
// between sessions.

// TrainingRules is the tuning of training (config training.*).
type TrainingRules struct {
	Session                    player.TrainingRules
	YardBPS, GroundBPS, GymBPS int64
	GroundFee, GymFee          int64
}

// TrainingHandler serves training.home and training.start.
type TrainingHandler struct {
	uow            application.UnitOfWork
	ids            IDGenerator
	msgs           Translator
	content        ContentSource
	cities         application.CityRepository
	rules          TrainingRules
	home           string
	idempotencyTTL time.Duration
	now            func() time.Time
	// seat reads the trainer's seat of a settlement (free labourers, a session's
	// wage); nil: the ground always has its trainer (older wiring, tests).
	seat func(ctx context.Context, tx application.Tx, settlementID string) (free, wage int64, err error)
}

// WithTrainer makes the training ground a working building: it trains at its own
// rate only while an NPC of the pool coaches and the treasury pays the session's
// wage; without one it trains like open ground (rule 1c).
func (h *TrainingHandler) WithTrainer(seat func(ctx context.Context, tx application.Tx, settlementID string) (int64, int64, error)) *TrainingHandler {
	h.seat = seat
	return h
}

// NewTrainingHandler wires the handler.
func NewTrainingHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, rules TrainingRules, homeCity string, ttl time.Duration, now func() time.Time,
) *TrainingHandler {
	if source == nil || cities == nil || ids == nil || ttl <= 0 {
		panic("handlers: NewTrainingHandler requires content, cities, ids and an idempotency ttl")
	}
	if err := rules.Session.Validate(); err != nil {
		panic("handlers: NewTrainingHandler requires valid training rules")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &TrainingHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, rules: rules,
		home: homeCity, idempotencyTTL: ttl, now: now}
}

// TrainRequest names the venue of a session.
type TrainRequest struct {
	Venue string `json:"venue,omitempty"`
}

// groundBuilding is the building that makes a settlement's training ground.
const groundBuilding = "training_ground"

// venues weighs what stands where the player is.
func (h *TrainingHandler) venues(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
) ([]plife.TrainingVenue, presentation.Named, *application.City, bool, error) {
	out := []plife.TrainingVenue{{Code: plife.VenueYard, EfficiencyBPS: h.rules.YardBPS, Available: true}}
	if p.CityID == nil || *p.CityID == "" {
		return out, presentation.Named{}, nil, false, nil
	}
	city, err := h.cities.ByID(ctx, *p.CityID)
	if err != nil {
		return nil, presentation.Named{}, nil, false, err
	}
	place := presentation.Named{Code: city.Code, Name: city.Name}
	if h.home != "" && city.Code == h.home {
		out = append(out, plife.TrainingVenue{Code: plife.VenueGym, EfficiencyBPS: h.rules.GymBPS, Fee: h.rules.GymFee, Available: true})
		return out, place, city, false, nil
	}
	if _, err := tx.Settlements().ByID(ctx, city.ID); err != nil {
		if stderrors.Is(err, application.ErrCityNotFound) {
			return out, place, city, false, nil // a content city with no gym of its own
		}
		return nil, place, city, false, err
	}
	rows, err := tx.SettlementBuildings().List(ctx, city.ID)
	if err != nil {
		return nil, place, city, false, err
	}
	stands := false
	for _, b := range rows {
		stands = stands || (b.TypeCode == groundBuilding && b.Complete())
	}
	g := plife.TrainingVenue{Code: plife.VenueGround, EfficiencyBPS: h.rules.GroundBPS, Fee: h.rules.GroundFee, Available: stands}
	if stands && h.seat != nil {
		free, wage, err := h.seat(ctx, tx, city.ID)
		if err != nil {
			return nil, place, city, false, err
		}
		treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, city.ID)
		if err != nil {
			return nil, place, city, false, err
		}
		if free < 1 || treasury.Balance.Minor() < wage {
			g.EfficiencyBPS, g.Fee, g.Unkept = h.rules.YardBPS, 0, true
		}
	}
	if !stands {
		name := groundBuilding
		if d, ok := snap.SettlementBuildingDef(groundBuilding); ok {
			name = d.Name
		}
		g.Missing = &presentation.Named{Code: groundBuilding, Name: name}
	}
	return append(out, g), place, city, true, nil
}

// Home handles training.home.
func (h *TrainingHandler) Home(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view plife.TrainingHomeView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		st, err := h.stats(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		venues, place, _, _, err := h.venues(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		skills, err := tx.Skills().List(ctx, p.ID)
		if err != nil {
			return err
		}
		strength := 0
		for _, s := range skills {
			if s.Code == "strength" {
				strength = s.Level
			}
		}
		view = plife.TrainingHomeView{Place: place, Energy: st.Energy, MaxEnergy: st.MaxEnergy, Stamina: st.Stamina,
			StrengthLevel: strength, EnergyCost: h.rules.Session.EnergyCost, MaxEnergyCap: int(h.rules.Session.MaxEnergyBonusCap),
			Venues: venues}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return plife.TrainingHome(presentation.Ctx{Lang: lang}, view), nil
}

func (h *TrainingHandler) stats(ctx context.Context, tx application.Tx, playerID string) (application.Stats, error) {
	row, err := tx.Stats().EnsureDefaults(ctx, playerID, defaultStats(playerID, h.now()))
	if err != nil {
		return application.Stats{}, err
	}
	st, _ := regenerateEnergy(*row, h.now())
	return st, nil
}

// Start handles training.start: one session at a venue that stands here.
func (h *TrainingHandler) Start(ctx context.Context, meta envelope.Metadata, req TrainRequest) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	replayed := false
	var view plife.TrainedView
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
		now := h.now()
		if err := RefuseJailed(ctx, tx, p.ID, now); err != nil {
			return err
		}
		venues, _, city, founded, err := h.venues(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		var venue *plife.TrainingVenue
		for i := range venues {
			if venues[i].Code == req.Venue && venues[i].Available {
				venue = &venues[i]
			}
		}
		if venue == nil {
			return errors.NotFound("no such training place here")
		}
		st, err := h.stats(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		next, stamina, xp, added, err := h.rules.Session.Session(domainStats(st), venue.EfficiencyBPS)
		if err != nil {
			return energyRefusal(err, h.rules.Session.EnergyCost, st.Energy)
		}
		if venue.Fee > 0 {
			if err := h.payFee(ctx, tx, p.ID, city, founded, venue.Fee, now); err != nil {
				return err
			}
		}
		if founded && venue.Code == plife.VenueGround && !venue.Unkept && h.seat != nil {
			if err := h.payTrainer(ctx, tx, city, now); err != nil {
				return err
			}
		}
		out := storedStats(st, next)
		if out.UpdatedAt.IsZero() {
			out.UpdatedAt = now
		}
		if err := tx.Stats().Save(ctx, out); err != nil {
			return err
		}
		skills, err := tx.Skills().List(ctx, p.ID)
		if err != nil {
			return err
		}
		gains, err := awardSkillXP(ctx, tx, snap, p.ID, skills,
			[]skillAward{{Skill: player.SkillCode("strength"), XP: xp}}, now)
		if err != nil {
			return err
		}
		view = plife.TrainedView{Venue: venue.Code, Stamina: stamina, MaxEnergyAdded: added, StrengthXP: xp, Fee: venue.Fee,
			Energy: next.Energy, MaxEnergy: next.MaxEnergy}
		for _, g := range gains {
			view.StrengthLevel = g.Level
			view.StrengthXP = g.XP
		}
		return nil
	})
	if err != nil {
		if r, ok := asRefusal(err); ok {
			return plife.Refusal(presentation.Ctx{Lang: lang}, r.view), nil
		}
		return nil, err
	}
	if replayed {
		return h.Home(ctx, meta)
	}
	return plife.Trained(presentation.Ctx{Lang: lang}, view), nil
}

// payFee takes the session fee from the player's cash: into the settlement's treasury
// at a training ground (training_fee), into the sink at the neutral city's gym.
func (h *TrainingHandler) payFee(ctx context.Context, tx application.Tx, playerID string, city *application.City,
	founded bool, fee int64, now time.Time,
) error {
	// At a settlement's training ground the fee is paid in the settlement's own money when it has one
	// and the player holds the units; otherwise in SUP, as before (docs/adr/0033 6.9).
	if founded && fee > 0 {
		r, err := application.PayLocal(ctx, tx, h.ids.NewID, application.LocalPayment{SettlementID: city.ID, PlayerID: playerID,
			Direction: application.LocalCollect, Flow: application.ReasonTrainingFee, SUP: fee,
			RefType: "training", RefID: h.ids.NewID(), At: now})
		if err != nil {
			return err
		}
		if r.Paid {
			return nil
		}
	}
	cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, playerID)
	if err != nil {
		return err
	}
	if cash.Balance.Minor() < fee {
		return &refusal{view: plife.RefusalView{Kind: plife.RefusalCannotAfford, Fee: fee, Cash: cash.Balance.Minor()}}
	}
	reason, to := application.ReasonServiceFee, application.SystemSinkAccountID
	if founded {
		treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, city.ID)
		if err != nil {
			return err
		}
		reason, to = application.ReasonTrainingFee, treasury.ID
	}
	_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{
		Reason: reason, ReferenceType: "training", ReferenceID: h.ids.NewID(), CreatedAt: now,
		Entries: []application.LedgerEntry{
			{AccountID: cash.ID, Amount: money.FromMinor(-fee)},
			{AccountID: to, Amount: money.FromMinor(fee)},
		},
	})
	return err
}

// payTrainer pays the NPC trainer one session's wage from the treasury into the
// sink (trainer_wage). The venue was judged kept, so the treasury can pay it.
func (h *TrainingHandler) payTrainer(ctx context.Context, tx application.Tx, city *application.City, now time.Time) error {
	_, wage, err := h.seat(ctx, tx, city.ID)
	if err != nil || wage <= 0 {
		return err
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, city.ID)
	if err != nil {
		return err
	}
	_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{
		Reason: application.ReasonTrainerWage, ReferenceType: "training", ReferenceID: h.ids.NewID(), CreatedAt: now,
		Entries: []application.LedgerEntry{
			{AccountID: treasury.ID, Amount: money.FromMinor(-wage)},
			{AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(wage)},
		},
	})
	return err
}
