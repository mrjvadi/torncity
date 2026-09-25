package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/crime"
	"github.com/mrjvadi/torncity/internal/domain/election"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/place"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// ElectionsHandler serves elections (internal/domain/election): the
// elections of the player's city and country, one election, standing as a
// candidate at city hall, voting in secret, and — from the scheduler — the
// count that fills the seats and the next election opening when a term runs
// out.
//
// Who may stand and vote, and how long each period lasts, is content
// (governance.yml elections). Every election period is REAL time, like the
// term it renews: an election is a governance guarantee in the sense of
// docs/adr/0018-game-clock.md (see internal/domain/election). The count is
// the only path that seats an elected holder; it runs exactly once because
// it moves only an election still open, under that election's row lock.
type ElectionsHandler struct {
	uow            application.UnitOfWork
	ids            IDGenerator
	msgs           Translator
	content        ContentSource
	cities         application.CityRepository
	scale          gametime.Scale
	nerve          crime.NerveRules
	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewElectionsHandler builds the handler. nerve is the crime engine's, for
// the criminal profile a candidate's record is read from.
func NewElectionsHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, scale gametime.Scale, nerve crime.NerveRules,
	idempotencyTTL time.Duration, now func() time.Time,
) *ElectionsHandler {
	if source == nil || cities == nil || ids == nil {
		panic("handlers: NewElectionsHandler requires content, cities and ids")
	}
	if scale.Validate() != nil || idempotencyTTL <= 0 {
		panic("handlers: NewElectionsHandler requires a game clock and an idempotency ttl")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &ElectionsHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, scale: scale,
		nerve: nerve, idempotencyTTL: idempotencyTTL, now: now}
}

// ElectionRequest names an election by its public number; candidate is a
// candidate's place on the ballot, from 1.
type ElectionRequest struct {
	No        string `json:"no,omitempty"`
	Candidate string `json:"candidate,omitempty"`
	Method    string `json:"method,omitempty"`
	Nonce     string `json:"nonce,omitempty"`
}

// ElectionActionPayload is the jsonb of an election's scheduled actions: the
// count names the election; the next opening names the office and the
// jurisdiction, and the election it follows.
type ElectionActionPayload struct {
	ReferenceID    string `json:"reference_id"`
	Office         string `json:"office,omitempty"`
	JurisdictionID string `json:"jurisdiction_id,omitempty"`
}

// electionsListSize is how many elections the list shows: layout.
const electionsListSize = 10

func (h *ElectionsHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// electionRefusal carries a refused request out of a unit of work.
type electionRefusal struct{ view screens.ElectionRefusalView }

func (r *electionRefusal) Error() string { return "handlers: election refused: " + r.view.Kind }

func refuseElection(kind string, e *application.Election, j application.Jurisdiction) *electionRefusal {
	r := &electionRefusal{view: screens.ElectionRefusalView{Kind: kind}}
	if e != nil {
		r.view.No, r.view.Office, r.view.Place = e.No, e.OfficeCode, govPlace(j)
	}
	return r
}

func (h *ElectionsHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	var r *electionRefusal
	if stderrors.As(err, &r) {
		return screens.ElectionRefusal(h.screen(meta, lang), r.view), nil
	}
	if v, ok := asNotHere(err); ok {
		return screens.NotHere(h.screen(meta, lang), v), nil
	}
	if v, ok := asDeclined(err, screens.PaymentDeclinedView{}); ok {
		return screens.PaymentDeclined(h.screen(meta, lang), v), nil
	}
	return nil, err
}

func (h *ElectionsHandler) reserve(ctx context.Context, tx application.Tx, playerID string, meta envelope.Metadata, nonce string) (bool, error) {
	key := idempotency.Derive(playerID, meta.RequestID, meta.IdempotencyKey)
	if isNonce(nonce) {
		key = idempotency.Derive(playerID, meta.Command, nonce)
	}
	return tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

// nonce is a fresh one-time button token.
func (h *ElectionsHandler) nonce() string {
	id := strings.ReplaceAll(h.ids.NewID(), "-", "")
	return id[:min(len(id), nonceLength)]
}

// OpenElection opens an election of one office in one jurisdiction at now:
// its calendar is the content's periods (real time), the vote's opening goes
// on the schedule at the candidacy's end and its count at the vote's end. It refuses an office that is
// not elected, one of another level, and a second election under way
// (application.ErrElectionUnderWay).
func OpenElection(ctx context.Context, tx application.Tx, ids IDGenerator, snap *content.Snapshot,
	office, jurisdictionID string, now time.Time,
) (application.Election, error) {
	_, rules, ok := snap.Election(office)
	if !ok {
		return application.Election{}, errors.InvalidInput("the office " + office + " is not elected")
	}
	defs, err := tx.Governance().OfficeDefinitions(ctx)
	if err != nil {
		return application.Election{}, err
	}
	var def *application.OfficeDefinition
	for i := range defs {
		if defs[i].Code == office {
			def = &defs[i]
		}
	}
	if def == nil {
		return application.Election{}, application.ErrOfficeNotFound
	}
	j, err := tx.Governance().Jurisdiction(ctx, jurisdictionID)
	if err != nil {
		return application.Election{}, err
	}
	if j.Kind != def.Jurisdiction {
		return application.Election{}, application.ErrWrongJurisdiction
	}
	cal := election.Plan(rules, now)
	e := application.Election{
		ID: ids.NewID(), OfficeCode: office, JurisdictionID: jurisdictionID, Seats: def.Seats,
		OpensAt: cal.OpensAt, CandidacyEndsAt: cal.CandidacyEndsAt, VotingEndsAt: cal.VotingEndsAt,
		CountActionID: ids.NewID(), ContentVersion: snap.Version(),
	}
	payload, err := json.Marshal(ElectionActionPayload{ReferenceID: e.ID})
	if err != nil {
		return e, err
	}
	stored, err := tx.Elections().Open(ctx, e)
	if err != nil {
		return e, err
	}
	if err := tx.GameActions().Schedule(ctx, application.GameAction{
		ID: ids.NewID(), ActionType: application.ElectionVotingActionType, ActorType: "system",
		ReferenceType: application.ElectionReference, ReferenceID: e.ID, Payload: payload,
		StartedAt: now, FinishAt: e.CandidacyEndsAt,
	}); err != nil {
		return e, err
	}
	if err := tx.GameActions().Schedule(ctx, application.GameAction{
		ID: e.CountActionID, ActionType: application.ElectionCountActionType, ActorType: "system",
		ReferenceType: application.ElectionReference, ReferenceID: e.ID, Payload: payload,
		StartedAt: now, FinishAt: e.VotingEndsAt,
	}); err != nil {
		return e, err
	}
	return stored, nil
}

// electorate is where a player may take part: the jurisdictions of the
// city they stand in and above it, and those of the city they live in.
type electorate struct {
	here         *application.City
	residence    *application.City
	hereChain    []application.Jurisdiction
	homeChain    []application.Jurisdiction
	jurisdiction map[string]application.Jurisdiction
}

// chain is a jurisdiction and every one above it.
func chain(ctx context.Context, tx application.Tx, id string) ([]application.Jurisdiction, error) {
	var out []application.Jurisdiction
	for id != "" && len(out) < 8 {
		j, err := tx.Governance().Jurisdiction(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
		id = j.ParentID
	}
	return out, nil
}

func (h *ElectionsHandler) electorate(ctx context.Context, tx application.Tx, p *application.Player) (electorate, error) {
	el := electorate{jurisdiction: map[string]application.Jurisdiction{}}
	if p.CityID != nil && *p.CityID != "" {
		c, err := h.cities.ByID(ctx, *p.CityID)
		if err != nil {
			return el, err
		}
		el.here = c
		if el.hereChain, err = chain(ctx, tx, c.JurisdictionID); err != nil {
			return el, err
		}
	}
	home, err := tx.Employment().ResidenceCityID(ctx, p.ID)
	if err != nil {
		return el, err
	}
	if home != "" {
		c, err := h.cities.ByID(ctx, home)
		if err != nil {
			return el, err
		}
		el.residence = c
		if el.homeChain, err = chain(ctx, tx, c.JurisdictionID); err != nil {
			return el, err
		}
	}
	for _, j := range append(append([]application.Jurisdiction(nil), el.hereChain...), el.homeChain...) {
		el.jurisdiction[j.ID] = j
	}
	return el, nil
}

// residentOf reports whether the player lives within a jurisdiction.
func (el electorate) residentOf(jurisdictionID string) bool {
	for _, j := range el.homeChain {
		if j.ID == jurisdictionID {
			return true
		}
	}
	return false
}

// person is what eligibility reads of the player for one election.
func (h *ElectionsHandler) person(ctx context.Context, tx application.Tx, p *application.Player, el electorate,
	e *application.Election, rules election.Rules, now time.Time,
) (election.Person, error) {
	who := election.Person{Resident: el.residentOf(e.JurisdictionID)}
	// A resident has lived there since the character was made, or since a
	// home moved them there (docs/adr/0024). Real time, as every election
	// period is.
	since := p.CreatedAt
	if repo := tx.Property(); repo != nil {
		moved, err := repo.ResidenceSince(ctx, p.ID)
		if err != nil {
			return who, err
		}
		if moved != nil {
			since = *moved
		}
	}
	who.ResidentFor = max(now.Sub(since), 0)
	stats, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, now))
	if err != nil {
		return who, err
	}
	who.Level = stats.Level
	if rules.CleanRecord {
		prof, err := tx.Crime().Profile(ctx, p.ID, application.CriminalProfile{
			PlayerID: p.ID, Nerve: h.nerve.Max, NerveUpdatedAt: now, HeatUpdatedAt: now, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return who, err
		}
		who.Owes = prof.UnpaidRestitution > 0 || prof.UnpaidFines > 0
		d, err := detained(ctx, tx, p.ID, now)
		if err != nil {
			return who, err
		}
		who.Jailed = d.sentence != nil
	}
	return who, nil
}

// phaseOf is an election's phase now, counted ones included.
func phaseOf(e application.Election, now time.Time) string {
	if e.Status == application.ElectionCounted {
		return screens.ElectionCounted
	}
	return string(election.Schedule{OpensAt: e.OpensAt, CandidacyEndsAt: e.CandidacyEndsAt, VotingEndsAt: e.VotingEndsAt}.At(now))
}

// line is an election as the list shows it.
func (h *ElectionsHandler) line(ctx context.Context, tx application.Tx, e application.Election, j application.Jurisdiction,
	now time.Time,
) (screens.ElectionLine, error) {
	l := screens.ElectionLine{No: e.No, Office: e.OfficeCode, Place: govPlace(j), Phase: phaseOf(e, now), Seats: e.Seats}
	switch l.Phase {
	case string(election.Candidacy):
		l.EndsAt = e.CandidacyEndsAt
	case string(election.Voting):
		l.EndsAt = e.VotingEndsAt
	}
	if !l.EndsAt.IsZero() {
		l.Remaining = max(l.EndsAt.Sub(now), 0)
	}
	cs, err := tx.Elections().Candidates(ctx, e.ID)
	if err != nil {
		return l, err
	}
	l.Candidates = len(cs)
	for _, c := range cs {
		if c.Elected != nil && *c.Elected {
			who, err := tx.Players().GetByID(ctx, c.PlayerID)
			if err != nil {
				return l, err
			}
			l.Elected = append(l.Elected, screens.GovPlayer{Name: shownName(who), Code: who.PublicCode})
		}
	}
	return l, nil
}

// List handles election.list: the elections of the player's city and of
// the jurisdictions above it, under way first, then the latest of each
// office.
func (h *ElectionsHandler) List(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	lang := meta.Language
	var view screens.ElectionsView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		el, err := h.electorate(ctx, tx, p)
		if err != nil {
			return err
		}
		chainIDs := el.hereChain
		if el.here == nil {
			chainIDs = el.homeChain
		}
		if len(chainIDs) == 0 {
			view.NoCity = true
			return nil
		}
		view.Place = govPlace(chainIDs[0])
		ids := make([]string, 0, len(chainIDs))
		for _, j := range chainIDs {
			ids = append(ids, j.ID)
		}
		list, err := tx.Elections().ForJurisdictions(ctx, ids, electionsListSize)
		if err != nil {
			return err
		}
		now := h.now()
		for _, e := range list {
			l, err := h.line(ctx, tx, e, el.jurisdiction[e.JurisdictionID], now)
			if err != nil {
				return err
			}
			view.Elections = append(view.Elections, l)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return screens.Elections(h.screen(meta, lang), view), nil
}

// View handles election.view: one election, its candidates and — for the
// player — whether and how they may stand or vote.
func (h *ElectionsHandler) View(ctx context.Context, meta envelope.Metadata, req ElectionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.ElectionView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		v, err := h.view(ctx, tx, snap, p, req.No)
		view = v
		return err
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return screens.Election(h.screen(meta, lang), view), nil
}

// ballot is an election's candidates in ballot order, with the players.
func ballot(ctx context.Context, tx application.Tx, electionID string) ([]application.ElectionCandidate, []*application.Player, error) {
	cs, err := tx.Elections().Candidates(ctx, electionID)
	if err != nil {
		return nil, nil, err
	}
	who := make([]*application.Player, len(cs))
	for i, c := range cs {
		if who[i], err = tx.Players().GetByID(ctx, c.PlayerID); err != nil {
			return nil, nil, err
		}
	}
	return cs, who, nil
}

func (h *ElectionsHandler) election(ctx context.Context, tx application.Tx, no string) (*application.Election, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(no), 10, 64)
	if err != nil || n <= 0 {
		return nil, refuseElection(screens.ElectionRefusedNone, nil, application.Jurisdiction{})
	}
	e, err := tx.Elections().ElectionByNo(ctx, n)
	if isSentinel(err, application.ErrElectionNotFound) {
		return nil, refuseElection(screens.ElectionRefusedNone, nil, application.Jurisdiction{})
	}
	return e, err
}

func (h *ElectionsHandler) view(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, no string,
) (screens.ElectionView, error) {
	e, err := h.election(ctx, tx, no)
	if err != nil {
		return screens.ElectionView{}, err
	}
	j, err := tx.Governance().Jurisdiction(ctx, e.JurisdictionID)
	if err != nil {
		return screens.ElectionView{}, err
	}
	now := h.now()
	def, rules, _ := snap.Election(e.OfficeCode)
	v := screens.ElectionView{
		No: e.No, Office: e.OfficeCode, Place: govPlace(j), Seats: e.Seats, Phase: phaseOf(*e, now),
		CandidacyEndsAt: e.CandidacyEndsAt, VotingEndsAt: e.VotingEndsAt, VotesCast: e.VotesCast,
		Deposit: def.Deposit, RefundShareBPS: rules.RefundShareBPS, MinLevel: rules.MinLevel, Nonce: h.nonce(),
	}
	switch v.Phase {
	case string(election.Candidacy):
		v.Remaining = max(e.CandidacyEndsAt.Sub(now), 0)
	case string(election.Voting):
		v.Remaining = max(e.VotingEndsAt.Sub(now), 0)
	}
	cs, who, err := ballot(ctx, tx, e.ID)
	if err != nil {
		return v, err
	}
	for i, c := range cs {
		line := screens.CandidateLine{Player: screens.GovPlayer{Name: shownName(who[i]), Code: who[i].PublicCode},
			Mine: c.PlayerID == p.ID}
		if c.Votes != nil {
			line.Votes, line.Counted = *c.Votes, true
		}
		line.Elected = c.Elected != nil && *c.Elected
		v.Candidates = append(v.Candidates, line)
		v.Standing = v.Standing || line.Mine
	}
	if v.Voted, err = tx.Elections().Voted(ctx, e.ID, p.ID); err != nil {
		return v, err
	}
	el, err := h.electorate(ctx, tx, p)
	if err != nil {
		return v, err
	}
	who2, err := h.person(ctx, tx, p, el, e, rules, now)
	if err != nil {
		return v, err
	}
	switch v.Phase {
	case string(election.Candidacy):
		if v.Standing {
			break
		}
		if err := election.CanStand(rules, who2); err != nil {
			var r election.Refusal
			if stderrors.As(err, &r) {
				v.StandBlocked = r.Why
			}
			break
		}
		v.CanStand = true
		if def.Deposit > 0 {
			w, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
			if err != nil {
				return v, err
			}
			choice := paymentChoice(w.Plan(money.FromMinor(def.Deposit), snap.Accepts(content.ServiceElection)), w)
			v.Payment = &choice
		}
	case string(election.Voting):
		if v.Voted || len(cs) == 0 {
			break
		}
		if err := election.CanVote(rules, who2); err != nil {
			var r election.Refusal
			if stderrors.As(err, &r) {
				v.VoteBlocked = r.Why
			}
			break
		}
		v.CanVote = true
	}
	return v, nil
}

// Stand handles election.stand: a candidacy, registered at city hall, the
// deposit held in the candidate's escrow until the count.
func (h *ElectionsHandler) Stand(ctx context.Context, meta envelope.Metadata, req ElectionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	method, chosen, err := chosenMethod(req.Method)
	if err != nil {
		return nil, err
	}
	var (
		stood    screens.StoodView
		replayed bool
		asked    *screens.ElectionView
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		fresh, err := h.reserve(ctx, tx, p.ID, meta, req.Nonce)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}
		e, err := h.election(ctx, tx, req.No)
		if err != nil {
			return err
		}
		j, err := tx.Governance().Jurisdiction(ctx, e.JurisdictionID)
		if err != nil {
			return err
		}
		now := h.now()
		def, rules, ok := snap.Election(e.OfficeCode)
		if !ok || e.Status != application.ElectionOpen || phaseOf(*e, now) != string(election.Candidacy) {
			return refuseElection(screens.ElectionRefusedNotStanding, e, j)
		}
		el, err := h.electorate(ctx, tx, p)
		if err != nil {
			return err
		}
		who, err := h.person(ctx, tx, p, el, e, rules, now)
		if err != nil {
			return err
		}
		if err := election.CanStand(rules, who); err != nil {
			var r election.Refusal
			if stderrors.As(err, &r) {
				return refuseElection(r.Why, e, j)
			}
			return errors.Internal(err)
		}
		// A candidacy is registered at city hall, in a city of the
		// jurisdiction.
		w, err := locate(ctx, tx, h.cities, snap, p)
		if err != nil {
			return err
		}
		if w.city == nil || !el.residentOf(e.JurisdictionID) || !inChain(el.hereChain, e.JurisdictionID) {
			return refuseElection(screens.ElectionRefusedAway, e, j)
		}
		if err := needService(w, snap, place.ServiceCityHall, h.scale, now); err != nil {
			// Away from city hall: the walk there reopens this election.
			return thenFor(err, "election.view", strconv.FormatInt(e.No, 10))
		}
		if err := h.compatible(ctx, tx, p.ID, e.OfficeCode); err != nil {
			return refuseElection(election.WhyIncompatible, e, j)
		}
		c := application.ElectionCandidate{ElectionID: e.ID, PlayerID: p.ID, StoodAt: now, Deposit: def.Deposit}
		if def.Deposit > 0 {
			if !chosen {
				v, err := h.view(ctx, tx, snap, p, req.No)
				if err != nil {
					return err
				}
				asked = &v
				return errShowConfirm
			}
			wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
			if err != nil {
				return err
			}
			plan := wallet.Plan(money.FromMinor(def.Deposit), snap.Accepts(content.ServiceElection))
			back := []string{screens.AddrElection, strconv.FormatInt(e.No, 10)}
			if err := checkMethod(plan, method, wallet, "election.button.back", back...); err != nil {
				return err
			}
			escrow, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerEscrow, p.ID)
			if err != nil {
				return err
			}
			txID, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{
				Method: method, Accepted: plan.Accepted, Reason: application.ReasonElectionDeposit,
				ReferenceType: application.ElectionReference, ReferenceID: e.ID,
				To: []application.LedgerEntry{{AccountID: escrow.ID, Amount: money.FromMinor(def.Deposit)}}, CreatedAt: now,
			})
			if err != nil {
				if stderrors.Is(err, application.ErrPaymentDeclined) {
					return declined(plan, wallet, "election.button.back", back...)
				}
				return err
			}
			c.DepositMethod, c.DepositTransactionID = string(method), txID
		}
		if err := tx.Elections().Stand(ctx, c); err != nil {
			if isSentinel(err, application.ErrAlreadyStanding) {
				return refuseElection(election.WhyStanding, e, j)
			}
			return err
		}
		stood = screens.StoodView{No: e.No, Office: e.OfficeCode, Place: govPlace(j), Deposit: def.Deposit,
			Method: string(method), VotingAt: e.CandidacyEndsAt, VotingIn: max(e.CandidacyEndsAt.Sub(now), 0)}
		cityIDs, err := electionCities(ctx, tx, j)
		if err != nil {
			return err
		}
		return appendElectionEvent(ctx, tx, meta, "stood", e.ID, map[string]any{
			"election_id": e.ID, "no": e.No, "office": e.OfficeCode, "jurisdiction_id": e.JurisdictionID,
			"player_id": p.ID, "player_name": shownName(p), "city_id": cityOfJurisdiction(el, e.JurisdictionID),
			"city_ids":   cityIDs,
			"place_kind": j.Kind, "place_code": j.Code, "place_name": j.Name,
		})
	})
	if err == errShowConfirm {
		err = nil
	}
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	switch {
	case replayed:
		return h.View(ctx, meta, ElectionRequest{No: req.No})
	case asked != nil:
		return screens.Election(h.screen(meta, lang), *asked), nil
	}
	return screens.Stood(h.screen(meta, lang), stood), nil
}

func inChain(chain []application.Jurisdiction, id string) bool {
	for _, j := range chain {
		if j.ID == id {
			return true
		}
	}
	return false
}

// cityOfJurisdiction is the city whose groups hear of a city election:
// the player's own city when the election is of it, else none.
func cityOfJurisdiction(el electorate, jurisdictionID string) string {
	for _, c := range []*application.City{el.here, el.residence} {
		if c != nil && c.JurisdictionID == jurisdictionID {
			return c.ID
		}
	}
	return ""
}

// compatible refuses a candidacy for an office incompatible with one the
// player holds.
func (h *ElectionsHandler) compatible(ctx context.Context, tx application.Tx, playerID, office string) error {
	defs, err := tx.Governance().OfficeDefinitions(ctx)
	if err != nil {
		return err
	}
	byCode := map[string]application.OfficeDefinition{}
	for _, d := range defs {
		byCode[d.Code] = d
	}
	held, err := tx.Governance().SeatsHeldBy(ctx, playerID)
	if err != nil {
		return err
	}
	for _, s := range held {
		if s.OfficeCode != office && incompatibleOffices(byCode[office], byCode[s.OfficeCode]) {
			return application.ErrIncompatibleOffices
		}
	}
	return nil
}

func incompatibleOffices(a, b application.OfficeDefinition) bool {
	for _, x := range a.IncompatibleWith {
		if x == b.Code {
			return true
		}
	}
	for _, x := range b.IncompatibleWith {
		if x == a.Code {
			return true
		}
	}
	return false
}

// Vote handles election.vote: one secret vote per resident. Who voted and
// for whom are written apart, so nothing ties the two.
func (h *ElectionsHandler) Vote(ctx context.Context, meta envelope.Metadata, req ElectionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		voted    screens.VotedView
		replayed bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		fresh, err := h.reserve(ctx, tx, p.ID, meta, req.Nonce)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}
		e, err := h.election(ctx, tx, req.No)
		if err != nil {
			return err
		}
		j, err := tx.Governance().Jurisdiction(ctx, e.JurisdictionID)
		if err != nil {
			return err
		}
		now := h.now()
		_, rules, ok := snap.Election(e.OfficeCode)
		if !ok || e.Status != application.ElectionOpen || phaseOf(*e, now) != string(election.Voting) {
			return refuseElection(screens.ElectionRefusedNotVoting, e, j)
		}
		el, err := h.electorate(ctx, tx, p)
		if err != nil {
			return err
		}
		who, err := h.person(ctx, tx, p, el, e, rules, now)
		if err != nil {
			return err
		}
		if err := election.CanVote(rules, who); err != nil {
			var r election.Refusal
			if stderrors.As(err, &r) {
				return refuseElection(r.Why, e, j)
			}
			return errors.Internal(err)
		}
		cs, players, err := ballot(ctx, tx, e.ID)
		if err != nil {
			return err
		}
		n, err := strconv.Atoi(strings.TrimSpace(req.Candidate))
		if err != nil || n < 1 || n > len(cs) {
			return refuseElection(screens.ElectionRefusedNoCandidate, e, j)
		}
		chosen := cs[n-1]
		if err := tx.Elections().Vote(ctx, e.ID, p.ID, chosen.PlayerID, h.ids.NewID()); err != nil {
			if isSentinel(err, application.ErrAlreadyVoted) {
				return refuseElection(election.WhyVoted, e, j)
			}
			return err
		}
		voted = screens.VotedView{No: e.No, Office: e.OfficeCode, Place: govPlace(j),
			Candidate: screens.GovPlayer{Name: shownName(players[n-1]), Code: players[n-1].PublicCode}, CountAt: e.VotingEndsAt,
			CountIn: max(e.VotingEndsAt.Sub(now), 0)}
		return nil
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	if replayed {
		return h.View(ctx, meta, ElectionRequest{No: req.No})
	}
	return screens.Voted(h.screen(meta, lang), voted), nil
}

// electionActionID is the election a scheduled election action names.
func electionActionID(req CrimeScheduledRequest) (string, error) {
	id := req.ReferenceID
	if id == "" && len(req.Payload) > 0 {
		var inner ElectionActionPayload
		if err := json.Unmarshal(req.Payload, &inner); err != nil {
			return "", errors.InvalidInput("election action payload is unreadable").WithCause(err)
		}
		id = inner.ReferenceID
	}
	if id == "" {
		return "", errors.InvalidInput("election action names nothing")
	}
	return id, nil
}

// electionCity is the city whose groups hear of an election: the city a
// city's election is of. A country's election has none of its own; its
// lines go to the groups of every city of the country (electionCities).
func electionCity(ctx context.Context, cities application.CityRepository, j application.Jurisdiction) (string, error) {
	if j.Kind != "city" {
		return "", nil
	}
	c, err := cities.ByCode(ctx, j.Code)
	switch {
	case isSentinel(err, application.ErrCityNotFound):
		return "", nil
	case err != nil:
		return "", err
	}
	return c.ID, nil
}

// electionCities is, for an election of a place above a city — a
// presidency, a parliament — every city of that country, whose groups all
// hear of it (docs/adr/0024); nil for a city's own election.
func electionCities(ctx context.Context, tx application.Tx, j application.Jurisdiction) ([]string, error) {
	if j.Kind != "country" {
		return nil, nil
	}
	return cityIDsOf(ctx, tx, j.ID)
}

// Voting handles election.voting from the SCHEDULER: the candidacy is over
// and the vote opens, which the city's groups hear — with how many stood, or
// that nobody did. It runs once: voting_opened_at moves from NULL only once,
// under the election's row lock.
func (h *ElectionsHandler) Voting(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	id, err := electionActionID(req)
	if err != nil {
		return nil, err
	}
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		e, err := tx.Elections().Election(ctx, id)
		if isSentinel(err, application.ErrElectionNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if e.Status != application.ElectionOpen {
			return nil
		}
		now := h.now()
		if now.Before(e.CandidacyEndsAt) {
			return errors.Internal(stderrors.New("handlers: a vote opened before the candidacy ended"))
		}
		fresh, err := tx.Elections().MarkVotingOpened(ctx, e.ID, now)
		if err != nil || !fresh {
			return err
		}
		cs, err := tx.Elections().Candidates(ctx, e.ID)
		if err != nil {
			return err
		}
		j, err := tx.Governance().Jurisdiction(ctx, e.JurisdictionID)
		if err != nil {
			return err
		}
		cityID, err := electionCity(ctx, h.cities, j)
		if err != nil {
			return err
		}
		cityIDs, err := electionCities(ctx, tx, j)
		if err != nil {
			return err
		}
		return appendElectionEvent(ctx, tx, meta, "voting", e.ID, map[string]any{
			"election_id": e.ID, "no": e.No, "office": e.OfficeCode, "jurisdiction_id": e.JurisdictionID,
			"place_kind": j.Kind, "place_code": j.Code, "place_name": j.Name, "city_id": cityID, "city_ids": cityIDs,
			"candidate_count": len(cs), "voting_ends_at": e.VotingEndsAt,
			"voting_seconds": int64(max(e.VotingEndsAt.Sub(now), 0) / time.Second),
		})
	})
}

// Count handles election.count from the SCHEDULER: the ballots counted; the
// term over, so every seat of the office vacated; the seats filled in
// ranking order (an elected candidate who meanwhile took an incompatible
// office gives way to the next); each seat change written to the audit log
// as an appointment is; every deposit returned or forfeit; the result made
// public; and — for an office with a term — the next election put on the
// schedule. A seat nobody won stays vacant, which the result says. It runs
// once: only an open election moves, under its row lock.
func (h *ElectionsHandler) Count(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	id, err := electionActionID(req)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		e, err := tx.Elections().Election(ctx, id)
		if isSentinel(err, application.ErrElectionNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if e.Status != application.ElectionOpen {
			return nil
		}
		now := h.now()
		if now.Before(e.VotingEndsAt) {
			return errors.Internal(stderrors.New("handlers: election counted before the vote ended"))
		}
		// An office no longer elected (content changed mid-election) is
		// still counted; it only schedules no next election.
		_, rules, elected := snap.Election(e.OfficeCode)
		cs, players, err := ballot(ctx, tx, e.ID)
		if err != nil {
			return err
		}
		tally, err := tx.Elections().Tally(ctx, e.ID)
		if err != nil {
			return err
		}
		in := make([]election.Candidate, 0, len(cs))
		for _, c := range cs {
			in = append(in, election.Candidate{PlayerID: c.PlayerID, StoodAt: c.StoodAt, Votes: tally[c.PlayerID]})
		}
		res := election.Count(in, len(cs))
		// Seat in ranking order whoever still may hold the office.
		var winners []string
		for _, c := range res.Ranked {
			if len(winners) >= e.Seats || c.Votes == 0 {
				break
			}
			if err := h.compatible(ctx, tx, c.PlayerID, e.OfficeCode); err != nil {
				continue
			}
			winners = append(winners, c.PlayerID)
		}
		j, err := tx.Governance().Jurisdiction(ctx, e.JurisdictionID)
		if err != nil {
			return err
		}
		// The term is over: every seat is vacated, then the winners are
		// seated in order. Both go to the audit log.
		for seat := 1; seat <= e.Seats; seat++ {
			before, after, err := application.VacateOffice(ctx, tx, e.OfficeCode, e.JurisdictionID, seat, now)
			if isSentinel(err, application.ErrOfficeVacant) {
				continue
			}
			if err != nil {
				return err
			}
			if err := tx.Elections().AuditSeat(ctx, application.SeatAudit{Action: application.ElectionAuditVacate,
				Jurisdiction: j, Before: before, After: after, ElectionNo: e.No, At: now}); err != nil {
				return err
			}
		}
		seated := map[string]int{}
		for i, pid := range winners {
			before, after, err := application.ElectToOffice(ctx, tx, e.OfficeCode, e.JurisdictionID, i+1, pid, now)
			if err != nil {
				return err
			}
			if err := tx.Elections().AuditSeat(ctx, application.SeatAudit{Action: application.ElectionAuditElect,
				Jurisdiction: j, Before: before, After: after, ElectionNo: e.No, At: now}); err != nil {
				return err
			}
			seated[pid] = i + 1
		}
		// Deposits: back to the purse they came from, or to the treasury of
		// the city the candidate lives in.
		out := make([]application.ElectionCandidate, len(cs))
		var results []map[string]any
		for i, c := range cs {
			votes := tally[c.PlayerID]
			won := seated[c.PlayerID] > 0
			back := election.Refunded(rules, votes, res.Cast)
			c.Votes, c.Elected, c.DepositReturned = &votes, &won, &back
			if seat := seated[c.PlayerID]; seat > 0 {
				c.Seat = &seat
			}
			if err := h.settleDeposit(ctx, tx, c, back, now); err != nil {
				return err
			}
			out[i] = c
			results = append(results, map[string]any{
				"player_id": c.PlayerID, "player_name": shownName(players[i]), "player_code": players[i].PublicCode,
				"votes": votes, "elected": won, "deposit": c.Deposit, "deposit_returned": back,
			})
		}
		e.CountedAt, e.VotesCast = &now, res.Cast
		if err := tx.Elections().RecordCount(ctx, *e, out); err != nil {
			return err
		}
		if elected {
			if err := h.scheduleNext(ctx, tx, *e, rules, len(winners) > 0, now); err != nil {
				return err
			}
		}
		cityID, err := electionCity(ctx, h.cities, j)
		if err != nil {
			return err
		}
		cityIDs, err := electionCities(ctx, tx, j)
		if err != nil {
			return err
		}
		// Each candidate hears their own result privately.
		for _, r := range results {
			if err := appendElectionEvent(ctx, tx, meta, "result", e.ID, map[string]any{
				"election_id": e.ID, "player_id": r["player_id"], "no": e.No, "office": e.OfficeCode, "place_kind": j.Kind,
				"place_code": j.Code, "place_name": j.Name, "votes": r["votes"], "cast": res.Cast,
				"elected": r["elected"], "deposit": r["deposit"], "deposit_returned": r["deposit_returned"],
			}); err != nil {
				return err
			}
		}
		return appendElectionEvent(ctx, tx, meta, "counted", e.ID, map[string]any{
			"election_id": e.ID, "no": e.No, "office": e.OfficeCode, "jurisdiction_id": e.JurisdictionID,
			"place_kind": j.Kind, "place_code": j.Code, "place_name": j.Name, "city_id": cityID, "city_ids": cityIDs,
			"seats": e.Seats, "votes_cast": res.Cast, "candidate_count": len(cs), "candidates": results,
		})
	})
}

// settleDeposit returns a candidate's deposit or forfeits it.
func (h *ElectionsHandler) settleDeposit(ctx context.Context, tx application.Tx, c application.ElectionCandidate, back bool, now time.Time) error {
	if c.Deposit <= 0 {
		return nil
	}
	escrow, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerEscrow, c.PlayerID)
	if err != nil {
		return err
	}
	reason := application.ReasonElectionRefund
	var to application.Account
	switch {
	case back:
		kind := application.AccountPlayerCash
		if c.DepositMethod == "card" {
			kind = application.AccountPlayerBank
		}
		if to, err = tx.Ledger().AccountFor(ctx, kind, c.PlayerID); err != nil {
			return err
		}
	default:
		reason = application.ReasonElectionForfeit
		home, err := tx.Employment().ResidenceCityID(ctx, c.PlayerID)
		if err != nil {
			return err
		}
		if home == "" {
			// Nowhere to forfeit to: it goes back.
			reason = application.ReasonElectionRefund
			if to, err = tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, c.PlayerID); err != nil {
				return err
			}
			break
		}
		if to, err = tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, home); err != nil {
			return err
		}
	}
	_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{
		Reason: reason, ReferenceType: application.ElectionReference, ReferenceID: c.ElectionID,
		Entries: []application.LedgerEntry{
			{AccountID: escrow.ID, Amount: money.FromMinor(-c.Deposit)}, {AccountID: to.ID, Amount: money.FromMinor(c.Deposit)},
		},
		CreatedAt: now,
	})
	return err
}

// scheduleNext puts the office's next election on the schedule
// (election.NextOpening): after a count that filled a seat, so that the next
// count falls when the new term ends; after one that filled none,
// reopen_after later. The term is real time (docs/adr/0018-game-clock.md),
// as AppointToOffice records it. An office held at pleasure schedules
// nothing; an operator opens its next.
func (h *ElectionsHandler) scheduleNext(ctx context.Context, tx application.Tx, e application.Election, rules election.Rules,
	filled bool, now time.Time,
) error {
	defs, err := tx.Governance().OfficeDefinitions(ctx)
	if err != nil {
		return err
	}
	var term time.Duration
	for _, d := range defs {
		if d.Code == e.OfficeCode {
			term = d.Term
		}
	}
	at, ok := election.NextOpening(rules, term, now, filled)
	if !ok {
		return nil
	}
	payload, err := json.Marshal(ElectionActionPayload{ReferenceID: e.ID, Office: e.OfficeCode, JurisdictionID: e.JurisdictionID})
	if err != nil {
		return err
	}
	return tx.GameActions().Schedule(ctx, application.GameAction{
		ID: h.ids.NewID(), ActionType: application.ElectionOpenActionType, ActorType: "system",
		ReferenceType: application.ElectionReference, ReferenceID: e.ID, Payload: payload,
		StartedAt: now, FinishAt: at,
	})
}

// Open handles election.open from the SCHEDULER: the next election of an
// office whose term is running out. It opens only if the election it follows
// is still the latest of that office there, so a redelivery opens nothing.
func (h *ElectionsHandler) Open(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	var in ElectionActionPayload
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &in); err != nil {
			return nil, errors.InvalidInput("election action payload is unreadable").WithCause(err)
		}
	}
	if in.Office == "" || in.JurisdictionID == "" || in.ReferenceID == "" {
		return nil, errors.InvalidInput("election action names nothing")
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		latest, err := tx.Elections().Latest(ctx, in.Office, in.JurisdictionID)
		if err != nil && !isSentinel(err, application.ErrElectionNotFound) {
			return err
		}
		if latest == nil || latest.ID != in.ReferenceID {
			return nil
		}
		if _, _, ok := snap.Election(in.Office); !ok {
			// No longer elected: nothing to open.
			return nil
		}
		e, err := OpenElection(ctx, tx, h.ids, snap, in.Office, in.JurisdictionID, h.now())
		if isSentinel(err, application.ErrElectionUnderWay) {
			return nil
		}
		if err != nil {
			return err
		}
		return AnnounceElectionOpened(ctx, tx, h.cities, meta, e)
	})
}

// AnnounceElectionOpened writes election.opened for an election just opened,
// for the groups of its city (a country's election is announced nowhere
// yet).
func AnnounceElectionOpened(ctx context.Context, tx application.Tx, cities application.CityRepository,
	meta envelope.Metadata, e application.Election,
) error {
	j, err := tx.Governance().Jurisdiction(ctx, e.JurisdictionID)
	if err != nil {
		return err
	}
	cityID, err := electionCity(ctx, cities, j)
	if err != nil {
		return err
	}
	cityIDs, err := electionCities(ctx, tx, j)
	if err != nil {
		return err
	}
	return appendElectionEvent(ctx, tx, meta, "opened", e.ID, map[string]any{
		"election_id": e.ID, "no": e.No, "office": e.OfficeCode, "jurisdiction_id": e.JurisdictionID,
		"place_kind": j.Kind, "place_code": j.Code, "place_name": j.Name, "city_id": cityID, "city_ids": cityIDs,
		"candidacy_ends_at": e.CandidacyEndsAt, "voting_ends_at": e.VotingEndsAt,
		"candidacy_seconds": int64(e.CandidacyEndsAt.Sub(e.OpensAt) / time.Second),
	})
}

// appendElectionEvent writes an election event to the outbox.
func appendElectionEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata, name, aggregateID string, payload map[string]any) error {
	ev, err := events.New("election."+name, "election", aggregateID, payload)
	if err != nil {
		return err
	}
	return tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID: ev.ID, Subject: subjects.Event("election", name), Metadata: meta, Payload: ev.Payload,
	})
}
