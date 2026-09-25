package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/crime"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// CrimeRules is the crime engine's tuning (config crime.*), gathered into
// the domain's values. None of it is policy: what a city's police charge and
// hand down is read through the policy resolver per request.
type CrimeRules struct {
	Nerve   crime.NerveRules
	Heat    crime.HeatRules
	Victims crime.VictimRules
	// ArrivalLinger is how long a player who arrived by bus, train or plane
	// stays at that terminal's venue. Real time.
	ArrivalLinger time.Duration
	// ReportWindow is how long after a theft its victim may report it. Real
	// time.
	ReportWindow time.Duration
	// Investigation is the solve-chance model; InvestigationDuration how
	// long an investigation takes, in GAME time.
	Investigation         crime.InvestigationModel
	InvestigationDuration time.Duration
	// NPCDailyCap is the most NPC crime pays into the economy per UTC day.
	NPCDailyCap money.Amount
	// GearCaps bound what every carried tool together adds to one attempt
	// (config crime.gear_*).
	GearCaps crime.GearCaps
}

// Validate reports whether the rules are usable.
func (r CrimeRules) Validate() error {
	var errs []error
	errs = append(errs, r.Nerve.Validate(), r.Heat.Validate(), r.Victims.Validate(), r.Investigation.Validate(),
		r.GearCaps.Validate())
	if r.ArrivalLinger <= 0 || r.ReportWindow <= 0 || r.InvestigationDuration <= 0 {
		errs = append(errs, fmt.Errorf("crime rules: arrival linger %s, report window %s and investigation %s must be positive",
			r.ArrivalLinger, r.ReportWindow, r.InvestigationDuration))
	}
	if r.NPCDailyCap.IsNegative() {
		errs = append(errs, fmt.Errorf("crime rules: npc daily cap %s is negative", r.NPCDailyCap))
	}
	return stderrors.Join(errs...)
}

// CrimeHandler serves the crime engine (docs/adr/0019-crime-engine.md): the
// hub and the crime screens, committing a crime, a timed crime's end, the
// criminal record, jail and bail, and the victim's report and its
// investigation.
//
// # Who a crime lands on
//
// Nobody chooses. A crime is committed where the thief is — their venue,
// derived by crime.Locate from what they are doing — and when it can hit a
// player, one of the eligible players at the same venue becomes the victim
// by chance (crime.ChooseVictim); otherwise an NPC passer-by does. There is
// no target argument anywhere in this handler, by design: a thief able to
// pick a victim would farm their friends.
//
// # Money
//
// Every movement is a ledger transaction under its own reason: an NPC take
// from system_source (crime_proceeds, under the global daily cap), a theft
// from the victim's cash (theft — never their bank), a fine, a report fee
// and a bail into the city's treasury, and restitution from a convicted
// thief to their victim.
type CrimeHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	policy  application.PolicyReader
	// scale is the game clock: crime durations, sentences and the
	// investigation are game time.
	scale gametime.Scale
	dice  crime.Dice
	rules CrimeRules

	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewCrimeHandler wires the handler. A missing dependency, invalid rules or
// a game clock outside 1..gametime.MaxScale are wiring mistakes and panic.
func NewCrimeHandler(
	uow application.UnitOfWork,
	ids IDGenerator,
	msgs Translator,
	source ContentSource,
	cities application.CityRepository,
	policy application.PolicyReader,
	scale gametime.Scale,
	dice crime.Dice,
	rules CrimeRules,
	idempotencyTTL time.Duration,
	now func() time.Time,
) *CrimeHandler {
	if source == nil || cities == nil || policy == nil || dice == nil || ids == nil {
		panic("handlers: NewCrimeHandler requires content, cities, a policy reader, dice and ids")
	}
	if err := rules.Validate(); err != nil {
		panic("handlers: NewCrimeHandler: " + err.Error())
	}
	if idempotencyTTL <= 0 {
		panic("handlers: NewCrimeHandler requires a positive idempotency ttl")
	}
	if scale.Validate() != nil {
		panic("handlers: NewCrimeHandler requires a game clock within 1..gametime.MaxScale")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &CrimeHandler{
		uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, policy: policy,
		scale: scale, dice: dice, rules: rules, idempotencyTTL: idempotencyTTL, now: now,
	}
}

// Requests.

// CrimeCategoryRequest is crime.list's payload.
type CrimeCategoryRequest struct {
	Category string `json:"category"`
	Page     string `json:"page,omitempty"`
}

// CrimeRequest names a crime by its content code, for crime.view.
type CrimeRequest struct {
	Crime string `json:"crime"`
}

// CrimeCommitRequest is crime.commit's payload: the crime and, from a
// button, its one-time token. There is no victim: see CrimeHandler.
type CrimeCommitRequest struct {
	Crime string `json:"crime"`
	Nonce string `json:"nonce,omitempty"`
}

// BailRequest is crime.bail's payload: the jail screen's one-time token and
// how the bail is paid, cash or card. Without a method the jail screen is
// shown, with a button per way to pay.
type BailRequest struct {
	Nonce  string `json:"nonce,omitempty"`
	Method string `json:"method,omitempty"`
}

// CrimeReportRequest is crime.report's payload: the theft (an attempt id,
// from the victim's notice) and, to file it, the confirmation — the way the
// fee is paid (cash or card), or "yes" when the city charges no fee.
type CrimeReportRequest struct {
	Crime   string `json:"crime"`
	Confirm string `json:"confirm,omitempty"`
}

// CrimeScheduledRequest is the scheduler's dispatch payload for crime.resolve,
// crime.conclude and crime.release, like FinishShiftRequest.
type CrimeScheduledRequest struct {
	ActionID      string          `json:"action_id"`
	ActorID       string          `json:"actor_id"`
	ReferenceType string          `json:"reference_type"`
	ReferenceID   string          `json:"reference_id"`
	Payload       json.RawMessage `json:"payload"`
}

// CrimeActionPayload is the jsonb a crime writes onto its game_actions rows
// and reads back, repeating the row's columns as TravelActionPayload does.
type CrimeActionPayload struct {
	ReferenceID string `json:"reference_id"`
	PlayerID    string `json:"player_id"`
}

// ids reads the player and the referenced row from a scheduled request,
// preferring the columns and falling back to the payload.
func (r CrimeScheduledRequest) ids() (playerID, refID string, err error) {
	playerID, refID = r.ActorID, r.ReferenceID
	if playerID == "" || refID == "" {
		var inner CrimeActionPayload
		if len(r.Payload) > 0 {
			if err := json.Unmarshal(r.Payload, &inner); err != nil {
				return "", "", errors.InvalidInput("crime action payload is unreadable").WithCause(err)
			}
		}
		if playerID == "" {
			playerID = inner.PlayerID
		}
		if refID == "" {
			refID = inner.ReferenceID
		}
	}
	if playerID == "" || refID == "" {
		return "", "", errors.InvalidInput("crime action names nothing")
	}
	return playerID, refID, nil
}

// Refusals.

// crimeRefusal carries a refused crime request out of a unit of work, so the
// transaction rolls back with its idempotency key, and the handler answers
// with the refusal screen instead of an error.
type crimeRefusal struct{ view screens.CrimeRefusalView }

func (r *crimeRefusal) Error() string { return "handlers: crime refused: " + r.view.Kind }

func refuseCrime(kind string) *crimeRefusal {
	return &crimeRefusal{view: screens.CrimeRefusalView{Kind: kind}}
}

func (h *CrimeHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// finish turns a command's outcome into what the player sees.
func (h *CrimeHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	var r *crimeRefusal
	if stderrors.As(err, &r) {
		return screens.CrimeRefusal(h.screen(meta, lang), r.view), nil
	}
	if v, ok := asDeclined(err, screens.PaymentDeclinedView{}); ok {
		return screens.PaymentDeclined(h.screen(meta, lang), v), nil
	}
	return nil, err
}

// reserve takes the command's idempotency key: a button's one-time token when
// it carries one, so two presses of one button are one attempt, and the
// update otherwise.
func (h *CrimeHandler) reserve(ctx context.Context, tx application.Tx, playerID string, meta envelope.Metadata, nonce string) (bool, error) {
	key := idempotency.Derive(playerID, meta.RequestID, meta.IdempotencyKey)
	if isNonce(nonce) {
		key = idempotency.Derive(playerID, meta.Command, nonce)
	}
	return tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

// isNonce reports whether s is a token this handler minted (h.nonce): hex,
// of the nonce length. A typed word in the nonce's place ("/crime
// pickpocketing @someone" — a victim can never be named) is ignored rather
// than turned into a key a second typing would collide with.
func isNonce(s string) bool {
	if len(s) != nonceLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// nonce is a fresh one-time button token.
func (h *CrimeHandler) nonce() string {
	id := strings.ReplaceAll(h.ids.NewID(), "-", "")
	if len(id) > nonceLength {
		id = id[:nonceLength]
	}
	return id
}

// Justice policy.

// justicePolicy is a city's police chief's levers, as the rules take them.
type justicePolicy struct {
	crime.JusticePolicy
	ReportFee   money.Amount
	BailPerHour money.Amount
	EffortBPS   int
}

// readJusticePolicy reads a city's justice levers through the resolver — the
// only place they are read.
func (h *CrimeHandler) readJusticePolicy(ctx context.Context, city application.City) (justicePolicy, error) {
	get := func(lever string) (int64, error) {
		v, err := h.policy.Get(ctx, city.JurisdictionID, lever)
		if err != nil {
			return 0, err
		}
		return v.Value, nil
	}
	var (
		p   justicePolicy
		err error
		v   int64
	)
	if v, err = get(application.LeverCrimeReportFee); err != nil {
		return p, err
	}
	p.ReportFee = money.FromMinor(v)
	if v, err = get(application.LeverBailPerHour); err != nil {
		return p, err
	}
	p.BailPerHour = money.FromMinor(v)
	if v, err = get(application.LeverJailTermMultiplier); err != nil {
		return p, err
	}
	p.JailTermPct = int(v)
	if v, err = get(application.LeverFineMultiplier); err != nil {
		return p, err
	}
	p.FinePct = int(v)
	if v, err = get(application.LeverInvestigationEffort); err != nil {
		return p, err
	}
	p.EffortBPS = int(v)
	if err := p.JusticePolicy.Validate(); err != nil {
		return p, errors.Internal(err)
	}
	return p, nil
}

// Profile, nerve and heat.

// freshProfile is the profile a player starts with: a full bar of nerve.
func (h *CrimeHandler) freshProfile(playerID string, now time.Time) application.CriminalProfile {
	return application.CriminalProfile{
		PlayerID: playerID, Nerve: h.rules.Nerve.Max,
		NerveUpdatedAt: now, HeatUpdatedAt: now, CreatedAt: now, UpdatedAt: now,
	}
}

// profileAt locks the player's profile (creating it) and brings its nerve
// and heat up to now. Nothing is written until SaveProfile.
func (h *CrimeHandler) profileAt(ctx context.Context, tx application.Tx, playerID string, now time.Time) (*application.CriminalProfile, error) {
	p, err := tx.Crime().Profile(ctx, playerID, h.freshProfile(playerID, now))
	if err != nil {
		return nil, err
	}
	n := h.rules.Nerve.Regenerate(crime.Nerve{Current: p.Nerve, UpdatedAt: p.NerveUpdatedAt}, now)
	heat := h.rules.Heat.Cool(crime.Heat{Level: p.Heat, UpdatedAt: p.HeatUpdatedAt}, now)
	p.Nerve, p.NerveUpdatedAt = n.Current, n.UpdatedAt
	p.Heat, p.HeatUpdatedAt = heat.Level, heat.UpdatedAt
	return p, nil
}

func (h *CrimeHandler) nerveView(p *application.CriminalProfile, now time.Time) screens.NerveView {
	n := crime.Nerve{Current: p.Nerve, UpdatedAt: p.NerveUpdatedAt}
	return screens.NerveView{Nerve: p.Nerve, Max: h.rules.Nerve.Max, FullIn: h.rules.Nerve.FullIn(n, now)}
}

func (h *CrimeHandler) heatView(heat int) screens.HeatView {
	return screens.HeatView{Heat: heat, Max: h.rules.Heat.Max, Wanted: h.rules.Heat.WantedLevel(heat), Stars: crime.WantedStars}
}

// tierView names a player's criminal tier and the next one.
func tierView(snap *content.Snapshot, xp int64) screens.TierView {
	defs := snap.CrimeTiers()
	if len(defs) == 0 {
		return screens.TierView{XP: xp}
	}
	i := crime.TierOf(snap.CrimeTierLadder(), xp)
	v := screens.TierView{Tier: screens.Named{Code: defs[i].Code, Name: defs[i].Name}, XP: xp}
	if i+1 < len(defs) {
		v.Next = screens.Named{Code: defs[i+1].Code, Name: defs[i+1].Name}
		v.NextXP = defs[i+1].MinXP
	}
	return v
}

// Where a player is.

// venueOf derives the venue a player stands at in cityID (crime.Locate):
// at work when a shift is running, at the city place they walked to or an
// arrival put them at, at a terminal just after arriving for a player with
// no place recorded, and at the default venue otherwise. ok is false when
// the content has no venues.
func (h *CrimeHandler) venueOf(ctx context.Context, tx application.Tx, snap *content.Snapshot, playerID, cityID string, now time.Time) (content.VenueDef, int, bool, error) {
	defs := snap.Venues()
	if len(defs) == 0 {
		return content.VenueDef{}, 0, false, nil
	}
	var w crime.Whereabouts
	career, arrivedBy, err := tx.Crime().Whereabouts(ctx, playerID, cityID, now.Add(-h.rules.ArrivalLinger))
	if err != nil {
		return content.VenueDef{}, 0, false, err
	}
	if def, ok := snap.CareerDef(career); ok && career != "" {
		w.ShiftCategory = def.Category
	}
	w.ArrivedBy = arrivedBy
	if w.Place, err = tx.Places().Where(ctx, playerID); err != nil {
		return content.VenueDef{}, 0, false, err
	}
	i := crime.Locate(snap.VenueList(), w)
	return defs[i], i, true, nil
}

func named(code, name string) screens.Named { return screens.Named{Code: code, Name: name} }

func venueNamed(v content.VenueDef) screens.Named { return named(v.Code, v.Name) }

func crimeNamed(def content.CrimeDef) screens.Named { return named(def.Code, def.Name) }

// crimeOf returns a crime's definition and domain value. A stored attempt or
// case naming a crime the content lacks is a load that should have been
// refused (ADR 0004 rule 7), so it is a fault.
func crimeOf(snap *content.Snapshot, code string) (content.CrimeDef, crime.Crime, error) {
	def, ok := snap.CrimeDef(code)
	cr, ok2 := snap.Crime(code)
	if !ok || !ok2 {
		return content.CrimeDef{}, crime.Crime{}, errors.Internal(stderrors.New("handlers: stored crime names content that does not exist: " + code))
	}
	return def, cr, nil
}

// Detention: jail and a timed crime keep a player from other things.

// detention is what holds a player: a sentence being served, a timed crime
// under way, a hospital stay (docs/adr/0023). Any may be nil.
type detention struct {
	sentence *application.JailSentence
	attempt  *application.CrimeAttempt
	stay     *application.HospitalStay
}

// detained reads what holds the player at now. A sentence whose time is up
// but whose release has not been processed yet does not hold them.
func detained(ctx context.Context, tx application.Tx, playerID string, now time.Time) (detention, error) {
	var d detention
	s, err := tx.Crime().ActiveSentence(ctx, playerID)
	switch {
	case err == nil && s.Serving(now):
		d.sentence = s
	case err != nil && !isSentinel(err, application.ErrNotJailed):
		return d, err
	}
	a, err := tx.Crime().ActiveAttempt(ctx, playerID)
	switch {
	case err == nil:
		d.attempt = a
	case !isSentinel(err, application.ErrNoCrimeInProgress):
		return d, err
	}
	if d.stay, err = hospitalised(ctx, tx, playerID, now); err != nil {
		return d, err
	}
	return d, nil
}

// RefuseDetained refuses what a player in jail, in the middle of a timed
// crime, or in hospital cannot do: travel and work a shift
// (docs/adr/0019-crime-engine.md, docs/adr/0023). Travel and the jobs handler
// call it beside their own "at work" check.
func RefuseDetained(ctx context.Context, tx application.Tx, playerID string, now time.Time) error {
	d, err := detained(ctx, tx, playerID, now)
	if err != nil {
		return err
	}
	if d.sentence != nil {
		return application.ErrInJail.WithDetail("remaining_seconds", int64(d.sentence.EndsAt.Sub(now)/time.Second))
	}
	if d.stay != nil {
		return application.ErrHospitalised.WithDetail("remaining_seconds", int64(d.stay.EndsAt.Sub(now)/time.Second))
	}
	if d.attempt != nil {
		return application.ErrCrimeInProgress
	}
	return nil
}

// RefuseJailed refuses what only jail rules out: a bank withdrawal. A player
// in the middle of a timed crime may still use their bank.
func RefuseJailed(ctx context.Context, tx application.Tx, playerID string, now time.Time) error {
	d, err := detained(ctx, tx, playerID, now)
	if err != nil {
		return err
	}
	if d.sentence != nil {
		return application.ErrInJail.WithDetail("remaining_seconds", int64(d.sentence.EndsAt.Sub(now)/time.Second))
	}
	return nil
}

// Scheduling and events.

// schedule writes one scheduled action of the crime engine.
func (h *CrimeHandler) schedule(ctx context.Context, tx application.Tx, actionType, playerID, refType, refID string, start, finish time.Time) (string, error) {
	payload, err := json.Marshal(CrimeActionPayload{ReferenceID: refID, PlayerID: playerID})
	if err != nil {
		return "", err
	}
	id := h.ids.NewID()
	return id, tx.GameActions().Schedule(ctx, application.GameAction{
		ID:            id,
		ActionType:    actionType,
		ActorType:     "player",
		ActorID:       playerID,
		ReferenceType: refType,
		ReferenceID:   refID,
		Payload:       payload,
		StartedAt:     start,
		FinishAt:      finish,
	})
}

// appendCrimeEvent writes a crime event to the outbox in the command's
// transaction; the notifier turns some of them into private notices.
func appendCrimeEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata, name, aggregateID string, payload map[string]any) error {
	ev, err := events.New("crime."+name, "crime", aggregateID, payload)
	if err != nil {
		return err
	}
	return tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID:  ev.ID,
		Subject:  subjects.Event("crime", name),
		Metadata: meta,
		Payload:  ev.Payload,
	})
}

// Money.

// legs builds the entries of a movement out of a player's cash and bank
// into one account, leaving out a leg that moves nothing.
func legs(cash, bank application.Account, fromCash, fromBank money.Amount, toID string) []application.LedgerEntry {
	var out []application.LedgerEntry
	if !fromCash.IsZero() {
		out = append(out, application.LedgerEntry{AccountID: cash.ID, Amount: money.FromMinor(-fromCash.Minor())})
	}
	if !fromBank.IsZero() {
		out = append(out, application.LedgerEntry{AccountID: bank.ID, Amount: money.FromMinor(-fromBank.Minor())})
	}
	total := fromCash.Minor() + fromBank.Minor()
	if total > 0 {
		out = append(out, application.LedgerEntry{AccountID: toID, Amount: money.FromMinor(total)})
	}
	return out
}

// post writes one movement, or nothing when it moves nothing.
func (h *CrimeHandler) post(ctx context.Context, tx application.Tx, reason application.Reason, refType, refID string, entries []application.LedgerEntry) (string, error) {
	if len(entries) < 2 {
		return "", nil
	}
	return tx.Ledger().Post(ctx, application.LedgerTransaction{
		Reason:        reason,
		ReferenceType: refType,
		ReferenceID:   refID,
		Entries:       entries,
		CreatedAt:     h.now(),
	})
}
