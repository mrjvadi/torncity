package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/crime"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// Commit handles crime.commit: committing a crime where the player stands.
//
// Everything is decided and paid in one unit of work, with the player's
// criminal profile locked first, so a double press, a redelivery or two
// devices make one attempt: the second waits on the profile, then finds the
// idempotency key taken (a button carries a one-time token) or the nerve
// spent. In order:
//
//  1. what holds the player — jail, a crime or a shift under way, the road
//     — and the crime's requirements, at the venue they are at;
//  2. the nerve it costs;
//  3. the victim, by chance, from the eligible players at the same venue
//     (crime.ChooseVictim), locked where they stand so "together" is still
//     true when money moves; an NPC passer-by otherwise;
//  4. the odds, fixed from the thief's skills and heat and the victim's
//     awareness plus the venue's security;
//  5. for a timed crime, the attempt goes on the schedule and resolves when
//     it ends (Resolve); an instant one is rolled and settled here.
func (h *CrimeHandler) Commit(ctx context.Context, meta envelope.Metadata, req CrimeCommitRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		replayed bool
		result   *screens.CrimeResultView
		started  *screens.CrimeStartedView
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
		def, ok := snap.CrimeDef(req.Crime)
		cr, ok2 := snap.Crime(req.Crime)
		if !ok || !ok2 {
			return refuseCrime(screens.CrimeRefusedNotFound)
		}

		now := h.now()
		s, err := h.situate(ctx, tx, snap, p, now)
		if err != nil {
			return err
		}
		if kind, need, have, wait := h.blocked(s, cr.NerveCost, now); kind != "" {
			r := refuseCrime(blockedRefusal[kind])
			r.view.Crime, r.view.Need, r.view.Have, r.view.Wait = crimeNamed(def), need, have, wait
			if s.hold.sentence != nil {
				r.view.Remaining = s.hold.sentence.EndsAt.Sub(now)
			}
			return r
		}
		if missing := h.missing(snap, cr, s); len(missing) > 0 {
			r := refuseCrime(screens.CrimeRefusedRequirements)
			r.view.Crime, r.view.Missing = crimeNamed(def), missing
			return r
		}
		nerve, err := h.rules.Nerve.Spend(crime.Nerve{Current: s.profile.Nerve, UpdatedAt: s.profile.NerveUpdatedAt}, cr.NerveCost)
		if err != nil {
			var short crime.NerveShortfall
			if stderrors.As(err, &short) {
				r := refuseCrime(screens.CrimeRefusedNerve)
				r.view.Crime, r.view.Need, r.view.Have = crimeNamed(def), short.Need, short.Have
				return r
			}
			return errors.Internal(err)
		}
		s.profile.Nerve, s.profile.NerveUpdatedAt = nerve.Current, nerve.UpdatedAt

		victim, err := h.chooseVictim(ctx, tx, snap, cr, p, s, now)
		if err != nil {
			return err
		}
		chance := cr.SuccessChance(crime.Situation{
			Skills: domainSkills(s.stand.skills), Heat: s.profile.Heat, Victim: victim.kind,
			Awareness: victim.awareness, VenueSecurity: s.venue.Security,
		})
		attempt := application.CrimeAttempt{
			ID:             h.ids.NewID(),
			PlayerID:       p.ID,
			CrimeCode:      def.Code,
			Category:       def.Category,
			CityID:         s.city.ID,
			VenueCode:      s.venue.Code,
			VictimKind:     string(victim.kind),
			ChanceBPS:      chance,
			NerveCost:      cr.NerveCost,
			ContentVersion: snap.Version(),
			StartedAt:      now,
			ResolvesAt:     now,
		}
		if victim.player != nil {
			attempt.VictimPlayerID = victim.player.ID
		}

		if cr.Timed() {
			// Only NPC crimes take time (crime.Validate), so no player
			// is held while it runs.
			attempt.Status = application.CrimeInProgress
			attempt.ResolvesAt = now.Add(h.scale.RealWait(cr.Duration))
			if attempt.GameActionID, err = h.schedule(ctx, tx, application.CrimeActionType, p.ID,
				application.CrimeReferenceAttempt, attempt.ID, now, attempt.ResolvesAt); err != nil {
				return err
			}
			if err := tx.Crime().RecordAttempt(ctx, attempt); err != nil {
				if isSentinel(err, application.ErrCrimeInProgress) {
					return refuseCrime(screens.CrimeRefusedBusy)
				}
				return err
			}
			s.profile.UpdatedAt = now
			if err := tx.Crime().SaveProfile(ctx, *s.profile); err != nil {
				return err
			}
			started = &screens.CrimeStartedView{
				Player: shownName(p), Crime: crimeNamed(def), Venue: venueNamed(s.venue),
				Duration: attempt.ResolvesAt.Sub(now), EndsAt: attempt.ResolvesAt,
				Nerve: h.nerveView(s.profile, now),
			}
			return appendCrimeEvent(ctx, tx, meta, "started", attempt.ID, map[string]any{
				"attempt_id": attempt.ID, "player_id": p.ID, "crime": def.Code, "venue": s.venue.Code,
				"city_id": s.city.ID, "ends_at": attempt.ResolvesAt, "content_version": snap.Version(),
			})
		}

		// The row goes in first, in progress, so the sentence of an arrest
		// can point at it; settle then moves it to its outcome. The partial
		// unique index is the last word on "one crime at a time".
		attempt.Status = application.CrimeInProgress
		if err := tx.Crime().RecordAttempt(ctx, attempt); err != nil {
			if isSentinel(err, application.ErrCrimeInProgress) {
				return refuseCrime(screens.CrimeRefusedBusy)
			}
			return err
		}
		view, err := h.settle(ctx, tx, meta, settlement{
			snap: snap, def: def, cr: cr, thief: p, profile: s.profile, stand: s.stand,
			city: s.city, venue: s.venue, attempt: attempt, victim: victim.player, now: now,
		})
		if err != nil {
			return err
		}
		result = &view
		if meta.InGroup() && view.Result == screens.CrimeOutcomeSucceeded {
			// The group read that it happened; the take is told privately.
			return appendCrimeEvent(ctx, tx, meta, "take", attempt.ID, resultPayload(p.ID, view))
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	switch {
	case replayed:
		return h.Hub(ctx, meta)
	case started != nil:
		return screens.CrimeStarted(h.screen(meta, lang), *started), nil
	case result != nil:
		return screens.CrimeResult(h.screen(meta, lang), *result), nil
	}
	return nil, errors.Internal(stderrors.New("handlers: crime commit produced nothing"))
}

// blockedRefusal maps why a crime is blocked to its refusal.
var blockedRefusal = map[string]string{
	screens.CrimeBlockedJail:       screens.CrimeRefusedJail,
	screens.CrimeBlockedBusy:       screens.CrimeRefusedBusy,
	screens.CrimeBlockedWork:       screens.CrimeRefusedWork,
	screens.CrimeBlockedTravelling: screens.CrimeRefusedTravelling,
	screens.CrimeBlockedNowhere:    screens.CrimeRefusedNowhere,
	screens.CrimeBlockedNerve:      screens.CrimeRefusedNerve,
}

// victimChoice is who an attempt landed on.
type victimChoice struct {
	kind crime.TargetKind
	// player is the victim when kind is player, locked where they stand.
	player    *application.Player
	awareness int
}

// chooseVictim draws the victim of an attempt. For a crime that can hit a
// player it lists the bystanders in the city, derives each one's venue by
// the same rule the thief's was derived by, keeps those the victim rules
// allow at the thief's venue, and lets crime.ChooseVictim decide. A chosen
// player is then locked where they stand — with the thief, in id order, as a
// cash payment locks both — and must still be in the city and not on the
// road, or the attempt falls back to an NPC passer-by.
func (h *CrimeHandler) chooseVictim(ctx context.Context, tx application.Tx, snap *content.Snapshot, cr crime.Crime,
	thief *application.Player, s situation, now time.Time,
) (victimChoice, error) {
	venues := snap.VenueList()
	var eligible []application.Bystander
	if cr.Hits(crime.TargetPlayer) && s.hasVenue {
		all, err := tx.Crime().Bystanders(ctx, s.city.ID, thief.ID,
			now.Add(-h.rules.Victims.ActiveWindow), now.Add(-h.rules.ArrivalLinger), now)
		if err != nil {
			return victimChoice{}, err
		}
		for _, b := range all {
			w := crime.Whereabouts{ArrivedBy: b.ArrivedBy}
			if b.ShiftCareer != "" {
				if def, ok := snap.CareerDef(b.ShiftCareer); ok {
					w.ShiftCategory = def.Category
				}
			}
			domainB := crime.Bystander{
				PlayerID: b.PlayerID, Level: b.Level, CreatedAt: b.CreatedAt, LastActiveAt: b.LastActiveAt,
				Venue: crime.Locate(venues, w), LastVictimisedAt: b.LastVictimisedAt, LastHitByThief: b.LastHitByThief,
			}
			if h.rules.Victims.Eligible(domainB, s.venueIndex, now) {
				eligible = append(eligible, b)
			}
		}
		sort.Slice(eligible, func(i, j int) bool { return eligible[i].PlayerID < eligible[j].PlayerID })
	}
	kind, i, err := crime.ChooseVictim(cr, len(eligible), s.venue.OpportunityBPS, h.dice)
	switch {
	case stderrors.Is(err, crime.ErrNoVictim):
		return victimChoice{}, refuseCrime(screens.CrimeRefusedNoVictim)
	case err != nil:
		return victimChoice{}, errors.Internal(err)
	case kind != crime.TargetPlayer:
		return victimChoice{kind: kind}, nil
	}

	b := eligible[i]
	ids := []string{thief.ID, b.PlayerID}
	sort.Strings(ids)
	for _, id := range ids {
		if _, err := tx.Stats().EnsureDefaults(ctx, id, defaultStats(id, now)); err != nil {
			return victimChoice{}, err
		}
	}
	here, err := tx.Bank().LockPresence(ctx, thief.ID, b.PlayerID)
	if err != nil {
		return victimChoice{}, err
	}
	if here[1].Travelling || here[1].CityID != s.city.ID {
		// They left between the listing and the lock: the opportunity is
		// gone, and the hand in the crowd finds a stranger's pocket.
		if cr.Hits(crime.TargetNPC) {
			return victimChoice{kind: crime.TargetNPC}, nil
		}
		return victimChoice{}, refuseCrime(screens.CrimeRefusedNoVictim)
	}
	victim, err := tx.Players().GetByID(ctx, b.PlayerID)
	if err != nil {
		return victimChoice{}, err
	}
	skills, err := tx.Skills().List(ctx, b.PlayerID)
	if err != nil {
		return victimChoice{}, err
	}
	return victimChoice{
		kind: crime.TargetPlayer, player: victim,
		awareness: crime.VictimAwareness(b.Level, domainSkills(skills)),
	}, nil
}

// settlement is everything settle needs about one attempt.
type settlement struct {
	snap    *content.Snapshot
	def     content.CrimeDef
	cr      crime.Crime
	thief   *application.Player
	profile *application.CriminalProfile
	stand   standing
	city    *application.City
	venue   content.VenueDef
	// attempt is the attempt as recorded (timed) or about to be (instant).
	attempt application.CrimeAttempt
	// victim is the player victim, nil for an NPC.
	victim *application.Player
	now    time.Time
	// timed marks the end of a timed attempt: its outcome is a notice.
	timed bool
}

// settle rolls an attempt, recorded in progress, and applies everything it
// decides, in the unit of work of the caller: the take (crime_proceeds from system_source under the
// daily cap, or theft from the victim's cash), an arrest's sentence and fine,
// the XP, criminal XP and skill XP, heat, the profile's counts, the attempt
// row and the victim's notice. It returns the outcome as the thief's screen.
func (h *CrimeHandler) settle(ctx context.Context, tx application.Tx, meta envelope.Metadata, in settlement) (screens.CrimeResultView, error) {
	pol, err := h.readJusticePolicy(ctx, *in.city)
	if err != nil {
		return screens.CrimeResultView{}, err
	}
	victimKind := crime.TargetKind(in.attempt.VictimKind)
	a := crime.Attempt{Crime: in.cr, Victim: victimKind, Chance: in.attempt.ChanceBPS, Policy: pol.JusticePolicy}

	ledger := tx.Ledger()
	thiefCash, thiefBank, err := playerAccounts(ctx, ledger, in.thief.ID)
	if err != nil {
		return screens.CrimeResultView{}, err
	}
	var victimCash application.Account
	switch victimKind {
	case crime.TargetNPC:
		paid, err := tx.Crime().LockNPCProceeds(ctx, in.now, in.now)
		if err != nil {
			return screens.CrimeResultView{}, err
		}
		a.NPCAllowance = money.FromMinor(max(h.rules.NPCDailyCap.Minor()-paid, 0))
	case crime.TargetPlayer:
		if victimCash, err = ledger.AccountFor(ctx, application.AccountPlayerCash, in.victim.ID); err != nil {
			return screens.CrimeResultView{}, err
		}
		a.VictimCash = victimCash.Balance
	}

	out, err := crime.Resolve(a, h.dice)
	if err != nil {
		return screens.CrimeResultView{}, errors.Internal(err)
	}

	row := in.attempt
	row.Status = string(out.Result)
	row.Witnessed = out.Witnessed
	resolved := in.now
	row.ResolvedAt = &resolved
	prof := in.profile
	prof.Attempts++
	heat := h.rules.Heat.Add(crime.Heat{Level: prof.Heat, UpdatedAt: prof.HeatUpdatedAt}, out.Heat)
	prof.Heat, prof.HeatUpdatedAt = heat.Level, heat.UpdatedAt
	prof.CriminalXP += out.CriminalXP

	view := screens.CrimeResultView{
		Player: shownName(in.thief), Crime: crimeNamed(in.def), Venue: venueNamed(in.venue),
		CityCode: in.city.Code, City: in.city.Name, Result: string(out.Result),
		VictimPlayer: victimKind == crime.TargetPlayer, CriminalXP: out.CriminalXP, XP: out.XP,
		Notice: in.timed,
	}

	switch out.Result {
	case crime.Succeeded:
		prof.Successes++
		row.Reward = out.Take.Minor()
		switch {
		case out.Take.IsZero():
			view.DrySpell = victimKind == crime.TargetNPC
		case victimKind == crime.TargetNPC:
			if row.LedgerTransactionID, err = h.post(ctx, tx, application.ReasonCrimeProceeds,
				application.CrimeReferenceAttempt, row.ID, []application.LedgerEntry{
					{AccountID: application.SystemSourceAccountID, Amount: money.FromMinor(-out.Take.Minor())},
					{AccountID: thiefCash.ID, Amount: out.Take},
				}); err != nil {
				return view, err
			}
			if err := tx.Crime().AddNPCProceeds(ctx, in.now, out.Take.Minor(), in.now); err != nil {
				return view, err
			}
		default:
			if row.LedgerTransactionID, err = h.post(ctx, tx, application.ReasonTheft,
				application.CrimeReferenceAttempt, row.ID, []application.LedgerEntry{
					{AccountID: victimCash.ID, Amount: money.FromMinor(-out.Take.Minor())},
					{AccountID: thiefCash.ID, Amount: out.Take},
				}); err != nil {
				return view, err
			}
		}
		view.Take = out.Take.Minor()
	case crime.Caught:
		prof.Arrests++
		sentence, err := h.jail(ctx, tx, in.thief.ID, in.city.ID, row.ID, application.SentenceForArrest, out.JailTerm, in.now)
		if err != nil {
			return view, err
		}
		row.JailSentenceID = sentence.ID
		view.Jail = &screens.CrimeProgress{Remaining: sentence.EndsAt.Sub(in.now), EndsAt: sentence.EndsAt}
		// A fine is paid on the spot from cash, then the bank; what cannot
		// be paid stays on the record (docs/adr/0019).
		if out.Fine.Minor() > 0 {
			split := crime.Settle(money.Amount{}, out.Fine, thiefCash.Balance, thiefBank.Balance)
			treasury, err := ledger.AccountFor(ctx, application.AccountCityTreasury, in.city.ID)
			if err != nil {
				return view, err
			}
			if _, err := h.post(ctx, tx, application.ReasonCrimeFine, application.CrimeReferenceAttempt, row.ID,
				legs(thiefCash, thiefBank, split.FineFromCash, split.FineFromBank, treasury.ID)); err != nil {
				return view, err
			}
			row.FineAmount, row.FinePaid = out.Fine.Minor(), split.FinePaid().Minor()
			prof.UnpaidFines += split.FineShortfall.Minor()
			view.Fine, view.FinePaid = row.FineAmount, row.FinePaid
		}
	}

	// XP and skill XP.
	if out.XP > 0 {
		next, ups := domainStats(in.stand.stats).AddXP(out.XP)
		stats := storedStats(in.stand.stats, next)
		stats.UpdatedAt = in.stand.stats.UpdatedAt
		if err := tx.Stats().Save(ctx, stats); err != nil {
			return view, err
		}
		for _, up := range ups {
			view.Level = max(view.Level, up.Level)
		}
	}
	awards := make([]skillAward, 0, len(out.SkillXP))
	for _, s := range out.SkillXP {
		awards = append(awards, skillAward{Skill: player.SkillCode(s.Skill), XP: s.XP})
	}
	if view.Skills, err = awardSkillXP(ctx, tx, in.thief.ID, in.stand.skills, awards, in.now); err != nil {
		return view, err
	}

	prof.UpdatedAt = in.now
	if err := tx.Crime().SaveProfile(ctx, *prof); err != nil {
		return view, err
	}
	if err := tx.Crime().ResolveAttempt(ctx, row); err != nil {
		return view, err
	}
	view.Heat = h.heatView(prof.Heat)
	view.Nerve = h.nerveView(prof, in.now)

	if out.Result == crime.Succeeded && victimKind == crime.TargetPlayer && out.Take.Minor() > 0 {
		payload := map[string]any{
			"victim_id": in.victim.ID, "attempt_id": row.ID, "crime": in.def.Code, "crime_name": in.def.Name,
			"venue": in.venue.Code, "venue_name": in.venue.Name, "city_code": in.city.Code, "city_name": in.city.Name,
			"amount": out.Take.Minor(), "report_fee": pol.ReportFee.Minor(),
			"report_window_seconds": int64(h.rules.ReportWindow / time.Second),
		}
		if out.Witnessed {
			payload["thief_name"], payload["thief_code"] = shownName(in.thief), in.thief.PublicCode
		}
		if err := appendCrimeEvent(ctx, tx, meta, "victimised", row.ID, payload); err != nil {
			return view, err
		}
	}
	return view, nil
}

// resultPayload is an attempt's outcome as an event carries it to the
// notifier, which renders it as the thief's private notice.
func resultPayload(playerID string, v screens.CrimeResultView) map[string]any {
	skills := make([]map[string]any, 0, len(v.Skills))
	for _, s := range v.Skills {
		skills = append(skills, map[string]any{"skill": s.Skill, "xp": s.XP, "level": s.Level})
	}
	p := map[string]any{
		"player_id": playerID, "player": v.Player,
		"crime": v.Crime.Code, "crime_name": v.Crime.Name, "venue": v.Venue.Code, "venue_name": v.Venue.Name,
		"city_code": v.CityCode, "city_name": v.City, "result": v.Result, "victim_player": v.VictimPlayer,
		"take": v.Take, "dry": v.DrySpell, "xp": v.XP, "criminal_xp": v.CriminalXP, "skills": skills,
		"level": v.Level, "heat": v.Heat.Heat, "heat_max": v.Heat.Max, "wanted": v.Heat.Wanted,
		"stars": v.Heat.Stars, "nerve": v.Nerve.Nerve, "nerve_max": v.Nerve.Max,
		"nerve_full_in_seconds": int64(v.Nerve.FullIn / time.Second),
		"fine":                  v.Fine, "fine_paid": v.FinePaid,
	}
	if v.Jail != nil {
		p["jail_seconds"] = int64(v.Jail.Remaining / time.Second)
		p["jail_ends_at"] = v.Jail.EndsAt
	}
	return p
}

// Resolve settles a timed crime whose time is up. It arrives from the
// SCHEDULER, like job.finish_shift, and settles exactly once the same three
// ways: the idempotency key is derived from the attempt, the player's
// profile is locked first so two deliveries run in turn, and only an
// attempt still in progress moves.
func (h *CrimeHandler) Resolve(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	playerID, attemptID, err := req.ids()
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		key := idempotency.Derive(playerID, meta.Command, attemptID)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil || !fresh {
			return err
		}
		now := h.now()
		prof, err := h.profileAt(ctx, tx, playerID, now)
		if err != nil {
			return err
		}
		a, err := tx.Crime().ActiveAttempt(ctx, playerID)
		if isSentinel(err, application.ErrNoCrimeInProgress) || (err == nil && a.ID != attemptID) {
			return nil
		}
		if err != nil {
			return err
		}
		if now.Before(a.ResolvesAt) {
			// Early by a clock step: a fault worth a retry, which the
			// broker's backoff turns into "later".
			return errors.Internal(stderrors.New("handlers: crime resolved before its end"))
		}
		def, cr, err := crimeOf(snap, a.CrimeCode)
		if err != nil {
			return err
		}
		p, err := tx.Players().GetByID(ctx, playerID)
		if err != nil {
			return err
		}
		stand, err := loadStanding(ctx, tx, p, now)
		if err != nil {
			return err
		}
		city, err := h.cities.ByID(ctx, a.CityID)
		if err != nil {
			return err
		}
		venue := content.VenueDef{Code: a.VenueCode, Name: a.VenueCode}
		for _, v := range snap.Venues() {
			if v.Code == a.VenueCode {
				venue = v
			}
		}
		view, err := h.settle(ctx, tx, meta, settlement{
			snap: snap, def: def, cr: cr, thief: p, profile: prof, stand: stand,
			city: city, venue: venue, attempt: *a, now: now, timed: true,
		})
		if err != nil {
			return err
		}
		return appendCrimeEvent(ctx, tx, meta, "resolved", a.ID, resultPayload(playerID, view))
	})
	var r *crimeRefusal
	if stderrors.As(err, &r) {
		// The attempt was already settled some other way.
		return nil, nil
	}
	return nil, err
}
