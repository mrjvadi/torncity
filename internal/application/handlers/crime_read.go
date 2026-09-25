package handlers

import (
	"context"
	"slices"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/crime"
	"github.com/mrjvadi/torncity/internal/domain/inventory"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// This file holds the crime screens that change nothing a player did: the
// hub, a category's crimes, one crime, the record, the jail and the cases.
// They may create a player's criminal profile on first sight, as the
// condition row is created on first sight elsewhere.

// recordSize is how many past attempts the record screen lists: layout, not
// tuning.
const recordSize = 5

// casesSize is how many reports the cases screen lists.
const casesSize = 10

// situation is where a player stands, as the crime screens and commit read
// it.
type situation struct {
	profile *application.CriminalProfile
	stand   standing
	city    *application.City
	venue   content.VenueDef
	// venueIndex indexes snap.VenueList(); hasVenue is false when the
	// content has no venues.
	venueIndex int
	hasVenue   bool
	hold       detention
	atWork     bool
	// walking: on the way between two places of the city.
	walking bool
	// carried is what the player carries: the tools a crime may need and
	// the gear that helps it.
	carried []inventory.Holding
}

// situate reads everything a crime screen needs about the player, with
// their profile locked.
func (h *CrimeHandler) situate(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player, now time.Time) (situation, error) {
	var s situation
	var err error
	if s.profile, err = h.profileAt(ctx, tx, p.ID, now); err != nil {
		return s, err
	}
	if s.stand, err = loadStanding(ctx, tx, p, now); err != nil {
		return s, err
	}
	if s.hold, err = detained(ctx, tx, p.ID, now); err != nil {
		return s, err
	}
	if _, _, s.carried, err = carried(ctx, tx, p.ID); err != nil {
		return s, err
	}
	shift, err := activeShift(ctx, tx, p.ID)
	if err != nil {
		return s, err
	}
	s.atWork = shift != nil
	if s.stand.travelling || s.stand.here() == "" {
		return s, nil
	}
	if _, err := tx.Places().ActiveMove(ctx, p.ID); err == nil {
		s.walking = true
	} else if !isSentinel(err, application.ErrNotMoving) {
		return s, err
	}
	if s.city, err = h.cities.ByID(ctx, s.stand.here()); err != nil {
		return s, err
	}
	s.venue, s.venueIndex, s.hasVenue, err = h.venueOf(ctx, tx, snap, p.ID, s.city.ID, now)
	return s, err
}

// candidate is the player as crime.Eligibility reads them.
func (s situation) candidate(snap *content.Snapshot) crime.Candidate {
	c := crime.Candidate{
		Level:          s.stand.stats.Level,
		Skills:         domainSkills(s.stand.skills),
		Tier:           crime.TierOf(snap.CrimeTierLadder(), s.profile.CriminalXP),
		Certifications: certificateCodes(s.stand.certs),
	}
	for _, h := range s.carried {
		c.Tools = append(c.Tools, h.Item)
	}
	if s.city != nil {
		c.Facilities = snap.CityFacilities(s.city.Code)
	}
	return c
}

// blocked says why the player cannot commit a crime costing nerve now,
// beyond its requirements: jail, a crime under way, a shift, the road,
// nowhere, or too little nerve.
func (h *CrimeHandler) blocked(s situation, nerve int, now time.Time) (kind string, need, have int, wait time.Duration) {
	switch {
	case s.hold.sentence != nil:
		return screens.CrimeBlockedJail, 0, 0, 0
	case s.hold.stay != nil:
		return screens.CrimeBlockedHospital, 0, 0, 0
	case s.hold.attempt != nil:
		return screens.CrimeBlockedBusy, 0, 0, 0
	case s.atWork:
		return screens.CrimeBlockedWork, 0, 0, 0
	case s.stand.travelling:
		return screens.CrimeBlockedTravelling, 0, 0, 0
	case s.walking:
		return screens.CrimeBlockedWalking, 0, 0, 0
	case s.city == nil:
		return screens.CrimeBlockedNowhere, 0, 0, 0
	case s.profile.Nerve < nerve:
		n := crime.Nerve{Current: s.profile.Nerve, UpdatedAt: s.profile.NerveUpdatedAt}
		missing := crime.NerveRules{Max: nerve, RegenAmount: h.rules.Nerve.RegenAmount, RegenInterval: h.rules.Nerve.RegenInterval}
		return screens.CrimeBlockedNerve, nerve, s.profile.Nerve, missing.FullIn(n, now)
	}
	return "", 0, 0, 0
}

// requirements lists every requirement of a crime, met or not.
func (h *CrimeHandler) requirements(snap *content.Snapshot, cr crime.Crime, s situation) []screens.CrimeRequirement {
	cand := s.candidate(snap)
	tiers := snap.CrimeTiers()
	var out []screens.CrimeRequirement
	r := cr.Requirements
	if r.MinLevel > 1 {
		out = append(out, screens.CrimeRequirement{Requirement: screens.Requirement{
			Kind: screens.ReqLevel, Met: cand.Level >= r.MinLevel, Need: int64(r.MinLevel), Have: int64(cand.Level)}})
	}
	if r.MinTier > 0 && r.MinTier < len(tiers) {
		have := tiers[min(max(cand.Tier, 0), len(tiers)-1)]
		out = append(out, screens.CrimeRequirement{
			Requirement: screens.Requirement{Kind: screens.ReqCrimeTier, Met: cand.Tier >= r.MinTier},
			Tier:        named(tiers[r.MinTier].Code, tiers[r.MinTier].Name),
			HaveTier:    named(have.Code, have.Name),
		})
	}
	for _, sk := range r.Skills {
		have := cand.SkillLevel(sk.Skill)
		out = append(out, screens.CrimeRequirement{Requirement: screens.Requirement{
			Kind: screens.ReqSkill, Met: have >= sk.Level, Skill: string(sk.Skill), Need: int64(sk.Level), Have: int64(have)}})
	}
	for _, code := range r.Certifications {
		ref := courseRef(snap, code)
		out = append(out, screens.CrimeRequirement{Requirement: screens.Requirement{
			Kind: screens.ReqCertificate, Met: s.stand.holds(code), CourseCode: ref.Code, CourseName: ref.Name}})
	}
	for _, code := range r.Tools {
		out = append(out, screens.CrimeRequirement{
			Requirement: screens.Requirement{Kind: screens.ReqTool, Met: slices.Contains(cand.Tools, code)},
			Tool:        itemNamed(snap, code),
		})
	}
	for _, f := range r.Facilities {
		met := false
		for _, have := range cand.Facilities {
			met = met || have == f
		}
		out = append(out, screens.CrimeRequirement{Requirement: screens.Requirement{Kind: screens.ReqFacility, Met: met}, Facility: f})
	}
	if len(r.Venues) > 0 {
		req := screens.CrimeRequirement{
			Requirement: screens.Requirement{Kind: screens.ReqVenue, Met: s.hasVenue && cr.CommittableAt(s.venue.Code)},
			Here:        venueNamed(s.venue),
		}
		for _, code := range r.Venues {
			for _, v := range snap.Venues() {
				if v.Code == code {
					req.Venues = append(req.Venues, venueNamed(v))
				}
			}
		}
		out = append(out, req)
	}
	return out
}

// gear is what the player's carried tools add to one attempt of a crime, and
// what the attempt wears of them.
func (h *CrimeHandler) gear(snap *content.Snapshot, cr crime.Crime, s situation) (crime.Gear, []inventory.Wear) {
	return inventory.GearFor(snap.ItemRules(), s.carried, cr.Code, cr.Category, h.rules.GearCaps)
}

// cooldown is how long the player must still wait before trying a crime
// again, zero when they may.
func (h *CrimeHandler) cooldown(ctx context.Context, tx application.Tx, snap *content.Snapshot, cr crime.Crime,
	playerID string, now time.Time,
) (time.Duration, error) {
	catCD := snap.CategoryCooldown(cr.Category)
	if cr.Cooldown <= 0 && catCD <= 0 {
		return 0, nil
	}
	last, err := tx.Crime().LastAttempt(ctx, playerID, cr.Code)
	if err != nil {
		return 0, err
	}
	lastCat, err := tx.Crime().LastAttemptInCategory(ctx, playerID, cr.Category)
	if err != nil {
		return 0, err
	}
	return crime.CooldownLeft(last, cr.Cooldown, lastCat, catCD, h.scale, now), nil
}

// eligible reports whether every requirement of a crime is met here.
func (h *CrimeHandler) eligible(snap *content.Snapshot, cr crime.Crime, s situation) bool {
	return crime.Eligibility(cr, s.candidate(snap)) == nil && s.hasVenue && cr.CommittableAt(s.venue.Code)
}

// missing lists the unmet requirements only.
func (h *CrimeHandler) missing(snap *content.Snapshot, cr crime.Crime, s situation) []screens.CrimeRequirement {
	var out []screens.CrimeRequirement
	for _, r := range h.requirements(snap, cr, s) {
		if !r.Met {
			out = append(out, r)
		}
	}
	return out
}

func progressOf(snap *content.Snapshot, a *application.CrimeAttempt, now time.Time) *screens.CrimeProgress {
	p := &screens.CrimeProgress{Crime: named(a.CrimeCode, a.CrimeCode), Remaining: max(a.ResolvesAt.Sub(now), 0), EndsAt: a.ResolvesAt}
	if def, ok := snap.CrimeDef(a.CrimeCode); ok {
		p.Crime = crimeNamed(def)
	}
	return p
}

// Hub handles crime.hub: where the player is, their nerve, heat and rank,
// and the categories of crime.
func (h *CrimeHandler) Hub(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CrimeHubView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		now := h.now()
		s, err := h.situate(ctx, tx, snap, p, now)
		if err != nil {
			return err
		}
		view = screens.CrimeHubView{
			Venue:      venueNamed(s.venue),
			Nerve:      h.nerveView(s.profile, now),
			Heat:       h.heatView(s.profile.Heat),
			Tier:       tierView(snap, s.profile.CriminalXP),
			Travelling: s.stand.travelling,
		}
		if s.city != nil {
			view.CityCode, view.City = s.city.Code, s.city.Name
		}
		if s.hold.sentence != nil {
			view.Jail = &screens.CrimeProgress{Remaining: s.hold.sentence.EndsAt.Sub(now), EndsAt: s.hold.sentence.EndsAt}
		}
		if s.hold.attempt != nil {
			view.Busy = progressOf(snap, s.hold.attempt, now)
		}
		for _, c := range snap.CrimeCategories() {
			view.Categories = append(view.Categories, named(c.Code, c.Name))
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.CrimeHub(h.screen(meta, lang), view), nil
}

// List handles crime.list: one category's crimes, each marked eligible or
// not.
func (h *CrimeHandler) List(ctx context.Context, meta envelope.Metadata, req CrimeCategoryRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CrimeListView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		var cat *content.CrimeCategoryDef
		for _, c := range snap.CrimeCategories() {
			if c.Code == req.Category {
				c := c
				cat = &c
			}
		}
		if cat == nil {
			return refuseCrime(screens.CrimeRefusedNotFound)
		}
		now := h.now()
		s, err := h.situate(ctx, tx, snap, p, now)
		if err != nil {
			return err
		}
		view.Category = named(cat.Code, cat.Name)
		var all []screens.CrimeLine
		for _, def := range snap.Crimes() {
			if def.Category != cat.Code {
				continue
			}
			cr, ok := snap.Crime(def.Code)
			if !ok {
				continue
			}
			all = append(all, screens.CrimeLine{
				Crime:    crimeNamed(def),
				Nerve:    cr.NerveCost,
				Duration: h.scale.RealWait(cr.Duration),
				Eligible: h.eligible(snap, cr, s),
			})
		}
		page := parsePage(req.Page)
		start, end, pages := pageWindow(len(all), page, DefaultPageSize)
		view.Crimes = all[start:end]
		view.Page, view.Pages = min(page, pages), pages
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.CrimeList(h.screen(meta, lang), view), nil
}

// View handles crime.view: one crime, its odds and risks here, every
// requirement, and the commit button when nothing stands in the way.
func (h *CrimeHandler) View(ctx context.Context, meta envelope.Metadata, req CrimeRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CrimeDetailView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
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
		victim := crime.TargetNPC
		if !cr.Hits(crime.TargetNPC) {
			victim = crime.TargetPlayer
		}
		g, _ := h.gear(snap, cr, s)
		odds := cr.OddsOf(crime.Situation{
			Skills: domainSkills(s.stand.skills), Heat: s.profile.Heat, Victim: victim, VenueSecurity: s.venue.Security, Gear: g,
		})
		view = screens.CrimeDetailView{
			Crime:        crimeNamed(def),
			Nerve:        g.NerveCost(cr.NerveCost),
			Duration:     h.scale.RealWait(cr.Duration),
			HitsPlayers:  cr.Hits(crime.TargetPlayer),
			HitsNPCs:     cr.Hits(crime.TargetNPC),
			MinTake:      cr.Reward.MinCash.Minor(),
			MaxTake:      cr.Reward.MaxCash.Minor(),
			ChanceBPS:    odds.Chance,
			Odds:         screens.OddsView{Base: odds.Base, Skill: odds.Skill, Awareness: odds.Awareness, Heat: odds.Heat, Gear: odds.Gear},
			Requirements: h.requirements(snap, cr, s),
			GearCatchBPS: g.CatchBPS, GearWitnessBPS: g.WitnessBPS, GearSolveBPS: g.SolveBPS, GearRewardBPS: g.RewardBPS,
		}
		left, err := h.cooldown(ctx, tx, snap, cr, p.ID, now)
		if err != nil {
			return err
		}
		view.Cooldown, view.CooldownLeft = h.scale.RealWait(max(cr.Cooldown, snap.CategoryCooldown(cr.Category))), left
		for _, c := range snap.CrimeCategories() {
			if c.Code == def.Category {
				view.Category = named(c.Code, c.Name)
			}
		}
		pct := crime.JusticePolicy{JailTermPct: 100, FinePct: 100}
		if s.city != nil {
			pol, err := h.readJusticePolicy(ctx, *s.city)
			if err != nil {
				return err
			}
			pct = pol.JusticePolicy
		}
		view.JailMin = h.scale.RealWait(cr.Failure.JailMin * time.Duration(pct.JailTermPct) / 100)
		view.JailMax = h.scale.RealWait(cr.Failure.JailMax * time.Duration(pct.JailTermPct) / 100)
		view.FineMin = cr.Failure.FineMin.Minor() * int64(pct.FinePct) / 100
		view.FineMax = cr.Failure.FineMax.Minor() * int64(pct.FinePct) / 100
		view.Blocked, view.Need, view.Have, view.Wait = h.blocked(s, g.NerveCost(cr.NerveCost), now)
		if view.Blocked == "" && left > 0 {
			view.Blocked, view.Wait = screens.CrimeBlockedCooldown, left
		}
		view.CanCommit = view.Blocked == "" && h.eligible(snap, cr, s)
		if view.CanCommit {
			view.Nonce = h.nonce()
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.CrimeDetail(h.screen(meta, lang), view), nil
}

// Record handles crime.record: rank, heat, nerve, the counts of the record
// and the last few attempts.
func (h *CrimeHandler) Record(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CrimeRecordView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		now := h.now()
		prof, err := h.profileAt(ctx, tx, p.ID, now)
		if err != nil {
			return err
		}
		view = screens.CrimeRecordView{
			Nerve: h.nerveView(prof, now), Heat: h.heatView(prof.Heat), Tier: tierView(snap, prof.CriminalXP),
			Attempts: prof.Attempts, Successes: prof.Successes, Arrests: prof.Arrests, Convictions: prof.Convictions,
			UnpaidRestitution: prof.UnpaidRestitution, UnpaidFines: prof.UnpaidFines,
		}
		recent, err := tx.Crime().RecentAttempts(ctx, p.ID, recordSize)
		if err != nil {
			return err
		}
		for _, a := range recent {
			line := screens.CrimeRecordLine{Crime: named(a.CrimeCode, a.CrimeCode), Result: a.Status, At: a.StartedAt}
			if def, ok := snap.CrimeDef(a.CrimeCode); ok {
				line.Crime = crimeNamed(def)
			}
			view.Recent = append(view.Recent, line)
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.CrimeRecord(h.screen(meta, lang), view), nil
}

// Jail handles crime.jail: the sentence being served and what bail costs.
func (h *CrimeHandler) Jail(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.JailView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		now := h.now()
		s, err := tx.Crime().ActiveSentence(ctx, p.ID)
		if isSentinel(err, application.ErrNotJailed) || (err == nil && !s.Serving(now)) {
			return nil
		}
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
		view = screens.JailView{
			InJail: true, CityCode: city.Code, City: city.Name, Reason: s.Reason,
			Remaining: s.EndsAt.Sub(now), EndsAt: s.EndsAt, Bail: bail.Minor(), Nonce: h.nonce(),
		}
		if bail.Minor() > 0 {
			w, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
			if err != nil {
				return err
			}
			choice := paymentChoice(w.Plan(bail, snap.Accepts(content.ServiceBail)), w)
			view.Payment = &choice
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Jail(h.screen(meta, lang), view), nil
}

// Cases handles crime.cases: the reports the player filed and where each
// stands. Private.
func (h *CrimeHandler) Cases(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.CasesView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		reports, err := tx.Crime().ReportsBy(ctx, p.ID, casesSize)
		if err != nil {
			return err
		}
		now := h.now()
		for _, r := range reports {
			line, err := h.caseLine(ctx, tx, snap, r, now)
			if err != nil {
				return err
			}
			view.Cases = append(view.Cases, line)
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.Cases(h.screen(meta, lang), view), nil
}

// caseLine builds one report's line. The thief is named only once the case
// is solved.
func (h *CrimeHandler) caseLine(ctx context.Context, tx application.Tx, snap *content.Snapshot, r application.CrimeReport, now time.Time) (screens.CaseLine, error) {
	line := screens.CaseLine{
		Crime: named(r.CrimeCode, r.CrimeCode), Amount: r.Stolen, Status: r.Status,
		Remaining: max(r.ConcludesAt.Sub(now), 0), Restored: r.RestitutionPaid,
	}
	if def, ok := snap.CrimeDef(r.CrimeCode); ok {
		line.Crime = crimeNamed(def)
	}
	if city, err := h.cities.ByID(ctx, r.CityID); err == nil {
		line.CityCode, line.City = city.Code, city.Name
	} else if !isSentinel(err, application.ErrCityNotFound) {
		return line, err
	}
	if r.Status == application.ReportSolved {
		thief, err := tx.Players().GetByID(ctx, r.SuspectPlayerID)
		if err != nil && !isSentinel(err, application.ErrPlayerNotFound) {
			return line, err
		}
		if thief != nil {
			line.Thief = shownName(thief)
		}
	}
	return line, nil
}
