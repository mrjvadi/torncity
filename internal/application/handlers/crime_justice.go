package handlers

import (
	"context"
	stderrors "errors"
	"github.com/mrjvadi/torncity/internal/domain/budget"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/crime"
	"github.com/mrjvadi/torncity/internal/domain/payment"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file holds justice: jail and bail, a victim's report, the
// investigation it opens, and the conviction or the dead end it comes to.

// jail puts a player in jail for term (game time) in cityID, or lengthens
// the sentence they are already serving by it: one sentence at a time, the
// second served after the first. A sentence whose time is up but whose
// release has not been processed yet is closed first. Its release is put on
// the schedule, the old release of an extended sentence becoming a no-op.
//
// A new sentence is announced (crime.jailed): the city's group reads that the
// player was jailed there, never why or for how long.
func (h *CrimeHandler) jail(ctx context.Context, tx application.Tx, meta envelope.Metadata, playerID, cityID, crimeID, reason string, term time.Duration, now time.Time) (application.JailSentence, error) {
	secs := int64(term / time.Second)
	if secs < 1 {
		secs = 1
	}
	wait := h.scale.RealWait(time.Duration(secs) * time.Second)
	current, err := tx.Crime().ActiveSentence(ctx, playerID)
	switch {
	case err == nil && current.Serving(now):
		ends := current.EndsAt.Add(wait)
		actionID, err := h.schedule(ctx, tx, application.JailReleaseActionType, playerID,
			application.CrimeReferenceSentence, current.ID, now, ends)
		if err != nil {
			return application.JailSentence{}, err
		}
		err = tx.Crime().ExtendSentence(ctx, current.ID, current.TermSeconds+secs, ends, actionID)
		if err == nil {
			current.TermSeconds, current.EndsAt, current.GameActionID = current.TermSeconds+secs, ends, actionID
			return *current, nil
		}
		if !isSentinel(err, application.ErrNotJailed) {
			return application.JailSentence{}, err
		}
		// Released or bailed between the read and the write: a new
		// sentence begins.
	case err == nil:
		if err := tx.Crime().EndSentence(ctx, current.ID, application.SentenceReleased, 0, "", now); err != nil &&
			!isSentinel(err, application.ErrNotJailed) {
			return application.JailSentence{}, err
		}
		// That sentence ended at its own end, not now: the course ran again
		// from then until this jailing stops it once more, below.
		if err := resumeStudies(ctx, tx, h.ids, playerID, current.EndsAt); err != nil {
			return application.JailSentence{}, err
		}
	case !isSentinel(err, application.ErrNotJailed):
		return application.JailSentence{}, err
	}

	s := application.JailSentence{
		ID: h.ids.NewID(), PlayerID: playerID, CityID: cityID, CrimeID: crimeID, Reason: reason,
		TermSeconds: secs, StartsAt: now, EndsAt: now.Add(wait),
	}
	if s.GameActionID, err = h.schedule(ctx, tx, application.JailReleaseActionType, playerID,
		application.CrimeReferenceSentence, s.ID, now, s.EndsAt); err != nil {
		return application.JailSentence{}, err
	}
	if err := tx.Crime().Jail(ctx, s); err != nil {
		return application.JailSentence{}, err
	}
	// A prisoner's course stands still until they are out.
	if err := pauseStudies(ctx, tx, playerID, now); err != nil {
		return application.JailSentence{}, err
	}
	var name string
	if p, err := tx.Players().GetByID(ctx, playerID); err == nil {
		name = shownName(p)
	} else if !isSentinel(err, application.ErrPlayerNotFound) {
		return application.JailSentence{}, err
	}
	if err := appendCrimeEvent(ctx, tx, meta, "jailed", s.ID, map[string]any{
		"sentence_id": s.ID, "player_id": playerID, "player_name": name, "city_id": cityID,
		"reason": reason, "ends_at": s.EndsAt,
	}); err != nil {
		return application.JailSentence{}, err
	}
	return s, nil
}

// bailFor is what leaving a sentence now costs, at its city's bail rate.
func (h *CrimeHandler) bailFor(ctx context.Context, s application.JailSentence, city application.City, now time.Time) (money.Amount, error) {
	pol, err := h.readJusticePolicy(ctx, city)
	if err != nil {
		return money.Amount{}, err
	}
	left := crime.RemainingTerm(time.Duration(s.TermSeconds)*time.Second, s.StartsAt, s.EndsAt, now)
	b, err := crime.Bail(pol.BailPerHour, left)
	if err != nil {
		return money.Amount{}, errors.Internal(err)
	}
	return b, nil
}

// Bail handles crime.bail: paying to leave jail early, from the purse the
// player chose — cash or card, whichever bail accepts (payments.yml) — into
// the treasury of the city that jailed the player. A card works from a cell:
// jail blocks a withdrawal, not a payment. The cash and the card button share
// one token, so a second press — of either — is a replay, never a second
// charge. A press without a method shows the jail screen.
func (h *CrimeHandler) Bail(ctx context.Context, meta envelope.Metadata, req BailRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	method, chosen, err := chosenMethod(req.Method)
	if err != nil {
		return nil, err
	}
	if !chosen {
		return h.Jail(ctx, meta)
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view     screens.BailedView
		replayed bool
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
		now := h.now()
		// Every change of a player's sentence runs under their profile.
		if _, err := h.profileAt(ctx, tx, p.ID, now); err != nil {
			return err
		}
		active, err := tx.Crime().ActiveSentence(ctx, p.ID)
		if isSentinel(err, application.ErrNotJailed) || (err == nil && !active.Serving(now)) {
			return refuseCrime(screens.CrimeRefusedNotJailed)
		}
		if err != nil {
			return err
		}
		s, err := tx.Crime().Sentence(ctx, active.ID)
		if err != nil {
			return err
		}
		city, err := h.cities.ByID(ctx, s.CityID)
		if err != nil {
			return err
		}
		bail, err := h.bailFor(ctx, *s, *city, now)
		if err != nil {
			return err
		}
		treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, city.ID)
		if err != nil {
			return err
		}
		txID, err := h.pay(ctx, tx, p.ID, snap.Accepts(content.ServiceBail), method, application.Charge{
			Reason: application.ReasonBail, ReferenceType: application.CrimeReferenceSentence, ReferenceID: s.ID,
			To: []application.LedgerEntry{{AccountID: treasury.ID, Amount: bail}},
		}, "crime.button.jail", screens.AddrCrimeJail)
		if err != nil {
			return err
		}
		if err := tx.Crime().EndSentence(ctx, s.ID, application.SentenceBailed, bail.Minor(), txID, now); err != nil {
			return err
		}
		if err := resumeStudies(ctx, tx, h.ids, p.ID, now); err != nil {
			return err
		}
		view = screens.BailedView{Player: shownName(p), Bail: bail.Minor(), Method: string(method)}
		return appendCrimeEvent(ctx, tx, meta, "bailed", s.ID, map[string]any{
			"sentence_id": s.ID, "player_id": p.ID, "city_id": city.ID, "bail": bail.Minor(),
		})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replayed {
		return h.Jail(ctx, meta)
	}
	return screens.Bailed(h.screen(meta, lang), view), nil
}

// errShowConfirm rolls back a report confirmed without a way to pay its fee,
// so the confirmation screen is shown instead and its idempotency key is not
// spent. It never leaves this file.
var errShowConfirm = stderrors.New("handlers: the report needs a way to pay its fee")

// pay takes a charge from the purse the player chose, refusing a method the
// service does not take or that does not cover it (screens.PaymentDeclined,
// with backLabel and back as the way back).
func (h *CrimeHandler) pay(ctx context.Context, tx application.Tx, playerID string, accepts payment.Accepts,
	method payment.Method, c application.Charge, backLabel string, back ...string,
) (string, error) {
	w, err := application.OpenWallet(ctx, tx.Ledger(), playerID)
	if err != nil {
		return "", err
	}
	amount, err := c.Amount()
	if err != nil {
		return "", errors.Internal(err)
	}
	plan := w.Plan(amount, accepts)
	if err := checkMethod(plan, method, w, backLabel, back...); err != nil {
		return "", err
	}
	c.Method, c.Accepted, c.CreatedAt = method, plan.Accepted, h.now()
	id, err := w.Pay(ctx, tx.Ledger(), c)
	if stderrors.Is(err, application.ErrPaymentDeclined) {
		return "", declined(plan, w, backLabel, back...)
	}
	return id, err
}

// Release ends a sentence whose time is up. It arrives from the SCHEDULER.
// A sentence already ended — bailed, or released by an earlier delivery —
// is left alone, and so is one this action no longer ends because a
// conviction lengthened it and scheduled a later release.
func (h *CrimeHandler) Release(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	playerID, sentenceID, err := req.ids()
	if err != nil {
		return nil, err
	}
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		key := idempotency.Derive(playerID, meta.Command, sentenceID+":"+req.ActionID)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil || !fresh {
			return err
		}
		now := h.now()
		s, err := tx.Crime().Sentence(ctx, sentenceID)
		if isSentinel(err, application.ErrSentenceNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if s.Status != application.SentenceServing || (req.ActionID != "" && s.GameActionID != req.ActionID) {
			return nil
		}
		if now.Before(s.EndsAt) {
			return errors.Internal(stderrors.New("handlers: release before the sentence ends"))
		}
		if err := tx.Crime().EndSentence(ctx, s.ID, application.SentenceReleased, 0, "", now); err != nil {
			if isSentinel(err, application.ErrNotJailed) {
				return nil
			}
			return err
		}
		// The course resumes from the sentence's end, not from whenever the
		// release was processed: a late scheduler costs the student nothing.
		if err := resumeStudies(ctx, tx, h.ids, playerID, s.EndsAt); err != nil {
			return err
		}
		city, err := h.cities.ByID(ctx, s.CityID)
		if err != nil {
			return err
		}
		return appendCrimeEvent(ctx, tx, meta, "released", s.ID, map[string]any{
			"sentence_id": s.ID, "player_id": playerID, "city_code": city.Code, "city_name": city.Name,
		})
	})
}

// Report handles crime.report: a victim reporting a theft to the police.
// Without the confirmation it only shows the fee; with it, the fee is paid
// into the treasury of the city where the theft happened, and an
// investigation is put on the schedule with its solve chance fixed now.
//
// Only a theft the player suffered can be reported, once, while the report
// window is open. That is why a report cannot be false (ADR 0012's "false
// report" case): there is nothing to report but a theft the ledger saw.
func (h *CrimeHandler) Report(ctx context.Context, meta envelope.Metadata, req CrimeReportRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	method, paid, err := chosenMethod(req.Confirm)
	if req.Confirm == screens.ReportConfirmation {
		method, paid, err = "", false, nil
	}
	if err != nil {
		return nil, err
	}
	confirmed := req.Confirm == screens.ReportConfirmation || paid
	var (
		confirm  *screens.ReportConfirmView
		filed    bool
		endsAt   time.Time
		replayed bool
		existing bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		if confirmed {
			fresh, err := h.reserve(ctx, tx, p.ID, meta, "")
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		a, err := tx.Crime().Attempt(ctx, req.Crime)
		if isSentinel(err, application.ErrCrimeNotFound) {
			return refuseCrime(screens.CrimeRefusedNotYours)
		}
		if err != nil {
			return err
		}
		if a.VictimPlayerID != p.ID {
			return refuseCrime(screens.CrimeRefusedNotYours)
		}
		if a.Status != application.CrimeSucceeded || (a.Reward <= 0 && a.StolenItem == "") || a.ResolvedAt == nil {
			return refuseCrime(screens.CrimeRefusedNothingStolen)
		}
		if _, err := tx.Crime().ReportForCrime(ctx, a.ID); err == nil {
			existing = true
			return nil
		} else if !isSentinel(err, application.ErrReportNotFound) {
			return err
		}
		now := h.now()
		deadline := a.ResolvedAt.Add(h.rules.ReportWindow)
		if now.After(deadline) {
			return refuseCrime(screens.CrimeRefusedExpired)
		}
		city, err := h.cities.ByID(ctx, a.CityID)
		if err != nil {
			return err
		}
		pol, err := h.readJusticePolicy(ctx, *city)
		if err != nil {
			return err
		}
		investigation := h.scale.RealWait(h.rules.InvestigationDuration)
		accepts := snap.Accepts(content.ServiceCrimeReport)
		// A fee to pay and no way to pay it chosen, or no fee and no
		// confirmation: the confirmation screen.
		if !confirmed || (pol.ReportFee.Minor() > 0 && !paid) {
			def, _ := snap.CrimeDef(a.CrimeCode)
			confirm = &screens.ReportConfirmView{
				CrimeID: a.ID, Crime: named(a.CrimeCode, def.Name), CityCode: city.Code, City: city.Name,
				Amount: a.Reward, Fee: pol.ReportFee.Minor(), Investigation: investigation, ReportWithin: deadline.Sub(now),
			}
			if pol.ReportFee.Minor() > 0 {
				w, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
				if err != nil {
					return err
				}
				choice := paymentChoice(w.Plan(pol.ReportFee, accepts), w)
				confirm.Payment = &choice
			}
			if confirmed {
				// The key reserved for a confirmation that turned out to
				// need a method is released with the rollback.
				return errShowConfirm
			}
			return nil
		}

		report := application.CrimeReport{
			ID: h.ids.NewID(), CrimeID: a.ID, VictimPlayerID: p.ID, SuspectPlayerID: a.PlayerID,
			CityID: city.ID, Stolen: a.Reward, ReportFee: pol.ReportFee.Minor(), FiledAt: now,
			ConcludesAt: now.Add(investigation),
		}
		if pol.ReportFee.Minor() > 0 {
			treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, city.ID)
			if err != nil {
				return err
			}
			if report.FeeTransactionID, err = h.pay(ctx, tx, p.ID, accepts, method, application.Charge{
				Reason: application.ReasonReportFee, ReferenceType: application.CrimeReferenceReport, ReferenceID: report.ID,
				To: []application.LedgerEntry{{AccountID: treasury.ID, Amount: pol.ReportFee}},
			}, "crime.button.cases", screens.AddrCrimeCases); err != nil {
				return err
			}
		}
		// The suspect's heat decides the odds: a known face is found. Read
		// under their profile lock, like every change to them.
		suspect, err := h.profileAt(ctx, tx, a.PlayerID, now)
		if err != nil {
			return err
		}
		// A thief who wore gloves leaves less behind: the gear's solve
		// term, fixed at the attempt, moves the odds.
		// The city's police budget adds to the chief's effort.
		police, err := budgetEffect(ctx, tx, city.ID, budget.EffectInvestigation)
		if err != nil {
			return err
		}
		effort := int(min(int64(pol.EffortBPS)+police, budget.BasisPoints))
		report.SolveChanceBPS = h.rules.Investigation.SolveChanceWithGear(suspect.Heat, a.Witnessed, effort, a.GearSolveBPS)
		if report.GameActionID, err = h.schedule(ctx, tx, application.InvestigationActionType, p.ID,
			application.CrimeReferenceReport, report.ID, now, report.ConcludesAt); err != nil {
			return err
		}
		if err := tx.Crime().FileReport(ctx, report); err != nil {
			if isSentinel(err, application.ErrAlreadyReported) {
				existing = true
				return nil
			}
			return err
		}
		filed, endsAt = true, report.ConcludesAt
		return appendCrimeEvent(ctx, tx, meta, "reported", report.ID, map[string]any{
			"report_id": report.ID, "attempt_id": a.ID, "victim_id": p.ID, "city_id": city.ID,
			"fee": report.ReportFee, "solve_chance_bps": report.SolveChanceBPS, "concludes_at": report.ConcludesAt,
		})
	})
	if err == errShowConfirm {
		err = nil
	}
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	switch {
	case replayed, existing:
		return h.Cases(ctx, meta)
	case confirm != nil:
		return screens.ReportConfirm(h.screen(meta, lang), *confirm), nil
	case filed:
		return screens.CaseFiled(h.screen(meta, lang), h.scale.RealWait(h.rules.InvestigationDuration), endsAt), nil
	}
	return nil, errors.Internal(stderrors.New("handlers: crime report produced nothing"))
}

// Conclude ends an investigation. It arrives from the SCHEDULER and runs
// exactly once: the idempotency key is derived from the report, and only a
// report still under investigation moves.
//
// Unsolved, the case closes and the fee is spent. Solved, the thief is
// identified to the victim and settled with under the conviction rules
// (crime.Settle): what they stole goes back to the victim first — from their
// cash, then their bank, never below zero — then the fine goes to the city,
// and whatever they cannot pay is recorded against them (unpaid restitution
// and fines on their profile, the shortfall on the case). They are
// sentenced, or their sentence lengthened, from the crime's failure ranges
// at the city's policy.
func (h *CrimeHandler) Conclude(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	victimID, reportID, err := req.ids()
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		key := idempotency.Derive(victimID, meta.Command, reportID)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), victimID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil || !fresh {
			return err
		}
		r, err := tx.Crime().Report(ctx, reportID)
		if isSentinel(err, application.ErrReportNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if r.Status != application.ReportInvestigating {
			return nil
		}
		now := h.now()
		if now.Before(r.ConcludesAt) {
			return errors.Internal(stderrors.New("handlers: investigation concluded before its end"))
		}
		def, cr, err := crimeOf(snap, r.CrimeCode)
		if err != nil {
			return err
		}
		city, err := h.cities.ByID(ctx, r.CityID)
		if err != nil {
			return err
		}
		solved, err := crime.Solved(r.SolveChanceBPS, h.dice)
		if err != nil {
			return errors.Internal(err)
		}
		concluded := now
		r.ConcludedAt = &concluded
		outcome := map[string]any{
			"report_id": r.ID, "victim_id": r.VictimPlayerID, "crime": def.Code, "crime_name": def.Name,
			"city_code": city.Code, "city_name": city.Name, "stolen": r.Stolen,
		}
		if !solved {
			r.Status = application.ReportUnsolved
			if err := tx.Crime().ConcludeReport(ctx, *r); err != nil {
				return err
			}
			return appendCrimeEvent(ctx, tx, meta, "case_closed", r.ID, outcome)
		}

		thief, err := tx.Players().GetByID(ctx, r.SuspectPlayerID)
		if err != nil {
			return err
		}
		prof, err := h.profileAt(ctx, tx, thief.ID, now)
		if err != nil {
			return err
		}
		pol, err := h.readJusticePolicy(ctx, *city)
		if err != nil {
			return err
		}
		term, fine, err := crime.Sentence(cr.Failure, pol.JusticePolicy, h.dice)
		if err != nil {
			return errors.Internal(err)
		}
		ledger := tx.Ledger()
		cash, bank, err := playerAccounts(ctx, ledger, thief.ID)
		if err != nil {
			return err
		}
		victimCash, err := ledger.AccountFor(ctx, application.AccountPlayerCash, r.VictimPlayerID)
		if err != nil {
			return err
		}
		treasury, err := ledger.AccountFor(ctx, application.AccountCityTreasury, city.ID)
		if err != nil {
			return err
		}
		// A good taken goes back if the thief still carries it; if not,
		// its reference price joins what they owe.
		owed := r.Stolen
		returned, value, err := h.returnGoods(ctx, tx, snap, r, thief.ID, now)
		if err != nil {
			return err
		}
		owed += value
		split := crime.Settle(money.FromMinor(owed), fine, cash.Balance, bank.Balance)
		if _, err := h.post(ctx, tx, application.ReasonRestitution, application.CrimeReferenceReport, r.ID,
			legs(cash, bank, split.RestitutionFromCash, split.RestitutionFromBank, victimCash.ID)); err != nil {
			return err
		}
		if _, err := h.post(ctx, tx, application.ReasonCrimeFine, application.CrimeReferenceReport, r.ID,
			legs(cash, bank, split.FineFromCash, split.FineFromBank, treasury.ID)); err != nil {
			return err
		}
		var sentence application.JailSentence
		if term > 0 {
			if sentence, err = h.jail(ctx, tx, meta, thief.ID, city.ID, r.CrimeID, application.SentenceForConviction, term, now); err != nil {
				return err
			}
		}
		prof.Convictions++
		prof.UnpaidRestitution += split.RestitutionShortfall.Minor()
		prof.UnpaidFines += split.FineShortfall.Minor()
		heat := h.rules.Heat.Add(crime.Heat{Level: prof.Heat, UpdatedAt: prof.HeatUpdatedAt}, cr.Failure.Heat)
		prof.Heat, prof.HeatUpdatedAt, prof.UpdatedAt = heat.Level, heat.UpdatedAt, now
		if err := tx.Crime().SaveProfile(ctx, *prof); err != nil {
			return err
		}

		r.Status = application.ReportSolved
		r.RestitutionPaid, r.RestitutionShortfall = split.Restitution().Minor(), split.RestitutionShortfall.Minor()
		r.FineAmount, r.FinePaid, r.JailSentenceID = fine.Minor(), split.FinePaid().Minor(), sentence.ID
		if err := tx.Crime().ConcludeReport(ctx, *r); err != nil {
			return err
		}
		outcome["thief_id"], outcome["thief_name"], outcome["thief_code"] = thief.ID, shownName(thief), thief.PublicCode
		outcome["restored"], outcome["shortfall"] = r.RestitutionPaid, r.RestitutionShortfall
		outcome["fine"], outcome["fine_paid"] = r.FineAmount, r.FinePaid
		if returned != "" {
			outcome["item_returned"] = returned
			def, _ := snap.ItemDef(returned)
			outcome["item_returned_name"] = def.Name
		}
		if sentence.ID != "" {
			outcome["term_seconds"] = int64(sentence.EndsAt.Sub(now) / time.Second)
		}
		if err := appendCrimeEvent(ctx, tx, meta, "case_solved", r.ID, outcome); err != nil {
			return err
		}
		return appendCrimeEvent(ctx, tx, meta, "convicted", r.ID, outcome)
	})
}

// returnGoods gives a victim back what a solved theft took beside money: the
// very piece, or the units of a stack, if the thief still carries them. It
// returns the good given back, or — when the thief no longer has it — the
// good's reference price times the units, which the caller adds to the
// restitution owed.
func (h *CrimeHandler) returnGoods(ctx context.Context, tx application.Tx, snap *content.Snapshot, r *application.CrimeReport,
	thiefID string, now time.Time,
) (string, int64, error) {
	a, err := tx.Crime().Attempt(ctx, r.CrimeID)
	if err != nil {
		return "", 0, err
	}
	if a.StolenItem == "" || a.StolenQty <= 0 {
		return "", 0, nil
	}
	if err := lockGoods(ctx, tx, thiefID, r.VictimPlayerID); err != nil {
		return "", 0, err
	}
	err = tx.Items().Move(ctx, application.ItemMove{
		ID: h.ids.NewID(), Item: a.StolenItem, PieceID: a.StolenPieceID, Qty: a.StolenQty,
		From: thiefID, FromHolding: application.HoldCarried, To: r.VictimPlayerID, ToHolding: application.HoldCarried,
		Reason: application.ItemRestitution, ReferenceType: application.CrimeReferenceReport, ReferenceID: r.ID, At: now,
	})
	switch {
	case err == nil:
		return a.StolenItem, 0, nil
	case isSentinel(err, application.ErrNotEnoughItems), isSentinel(err, application.ErrPieceNotFound):
		def, _ := snap.ItemDef(a.StolenItem)
		return "", def.BasePrice * a.StolenQty, nil
	}
	return "", 0, err
}
