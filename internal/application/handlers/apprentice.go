package handlers

import (
	"context"
	stderrors "errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
)

// Apprenticeship and the directory of teachers (docs/adr/0071, plan B7).
//
//   - WHO WORKS THERE. A master: a player at his own workshop (a citizen's twin of a craft's workplace) with the craft skill at the
//     content's least level, or the pool's craftsman of a public workshop (an NPC master of the content's NPC level). The apprentice
//     works shifts at that workshop beside him.
//   - WHAT IT CONSUMES. The apprentice's fee at the start (ledger apprentice_fee, less the settlement's income tax; a refund of the
//     unearned part if the master dismisses him), the master's attention (a master takes at most so many apprentices), the apprentice's
//     slow shifts (his output stays at his rung of the ladder).
//   - WHAT IT PROVIDES. While the term runs each finished shift of the apprentice at the master's workshop trains the trade faster
//     (a share of the base xp, by the master's own level, never past the master's level), and gives the master teaching xp once a day;
//     at the end the journeyman exam gives the craft's certificate with the master's name on it.
//   - NEVER A TICK. A term ends when it is read to have ended; the hooks are called from the shift that finishes.

// ApprenticeHook is what the village handler asks of the apprenticeships: the wage share of an apprentice's shift and the extra
// learning of a finished one.
type ApprenticeHook interface {
	// WageShareBPS is the share of a shift's wage a player keeps at a workshop (10000 unless he is an apprentice there).
	WageShareBPS(ctx context.Context, tx application.Tx, playerID, buildingID string, now time.Time) (int64, error)
	// OnShift gives an apprentice the extra learning of a finished shift of baseXP in skill at the workshop, and counts it.
	OnShift(ctx context.Context, tx application.Tx, snap *content.Snapshot, playerID, buildingID, skill string, baseXP int64, now time.Time) error
}

// ApprenticeHandler serves apprenticeships and the directory of teachers.
type ApprenticeHandler struct {
	uow       application.UnitOfWork
	ids       IDGenerator
	content   ContentSource
	cities    application.CityRepository
	search    application.PlayerSearch
	worlds    *application.WorldCache
	policy    application.PolicyReader
	teacherXP int64
	homeCode  string
	now       func() time.Time
	// teachingRules are the rule date of the teacher's skill (ADR 0071); the directory names the day it starts counting.
	teachingRules application.TeachingRules
}

// WithTeachingRules gives the handler the teacher rule's date, so the directory can name the day it starts.
func (h *ApprenticeHandler) WithTeachingRules(r application.TeachingRules) *ApprenticeHandler {
	h.teachingRules = r
	return h
}

// NewApprenticeHandler wires the handler.
func NewApprenticeHandler(uow application.UnitOfWork, ids IDGenerator, source ContentSource, cities application.CityRepository,
	search application.PlayerSearch, worlds *application.WorldCache, now func() time.Time,
) *ApprenticeHandler {
	if uow == nil || ids == nil || source == nil || cities == nil || search == nil || worlds == nil {
		panic("handlers: NewApprenticeHandler requires a unit of work, ids, content, cities, a player search and a world")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &ApprenticeHandler{uow: uow, ids: ids, content: source, cities: cities, search: search, worlds: worlds, now: now}
}

// WithRules gives the handler the teaching xp of a day with an apprentice, the income tax reader and the neutral city's code.
func (h *ApprenticeHandler) WithRules(teacherXP int64, policy application.PolicyReader, homeCode string) *ApprenticeHandler {
	h.teacherXP, h.policy, h.homeCode = teacherXP, policy, homeCode
	return h
}

// ApprenticeRequest is the payload of the apprenticeship commands; the names are those internal/gateway/routing gives the arguments.
type ApprenticeRequest struct {
	ID       string `json:"id,omitempty"`
	Building string `json:"building,omitempty"`
	Player   string `json:"player,omitempty"`
	Days     string `json:"days,omitempty"`
	Fee      string `json:"fee,omitempty"`
	Share    string `json:"share,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Pass     string `json:"pass,omitempty"`
	Craft    string `json:"craft,omitempty"`
}

// The refusal kinds.
const (
	ApprRefusedNotCraft    = "not_craft"
	ApprRefusedNotMaster   = "not_master"
	ApprRefusedMasterLevel = "master_level"
	ApprRefusedSlots       = "slots_full"
	ApprRefusedUnknown     = "apprentice_unknown"
	ApprRefusedSelf        = "self"
	ApprRefusedBusy        = "busy"
	ApprRefusedRange       = "range"
	ApprRefusedNotThere    = "not_there"
	ApprRefusedNotYours    = "not_yours"
	ApprRefusedNotOffered  = "not_offered"
	ApprRefusedNotActive   = "not_active"
	ApprRefusedAfford      = "cannot_afford"
	ApprRefusedRefund      = "cannot_refund"
	ApprRefusedTooSkilled  = "too_skilled"
	ApprRefusedReason      = "reason"
	ApprRefusedNotReady    = "exam_not_ready"
	ApprRefusedExamDone    = "exam_done"
	ApprRefusedExamNeeds   = "exam_needs"
)

type apprRefusal struct {
	kind   string
	bounds *plife.ApprenticeBounds
}

func (r *apprRefusal) Error() string { return "handlers: apprenticeship refused: " + r.kind }

func refuseAppr(kind string) error { return &apprRefusal{kind: kind} }

// apprenticeBounds are the term, fee and share bounds of an offer, from the content.
func apprenticeBounds(ad content.ApprenticeshipDef) plife.ApprenticeBounds {
	return plife.ApprenticeBounds{MinDays: ad.TermMin, MaxDays: ad.TermMax, DefaultDays: ad.TermDefault, MaxFee: ad.FeeMax,
		MinShareBPS: ad.WageShareMinBPS, DefaultShareBPS: ad.WageShareDefaultBPS, MaxShareBPS: 10_000}
}

func (h *ApprenticeHandler) finish(lang string, err error) (*presentation.Response, error) {
	var r *apprRefusal
	if stderrors.As(err, &r) {
		return plife.ApprenticeRefusal(presentation.Ctx{Lang: lang}, plife.ApprenticeRefusalView{Kind: r.kind, Bounds: r.bounds}), nil
	}
	return nil, err
}

func (h *ApprenticeHandler) def(snap *content.Snapshot) (content.CraftingDef, error) {
	cd, ok := snap.Crafting()
	if !ok || len(cd.Apprenticeship.Crafts) == 0 {
		return cd, refuseAppr(ApprRefusedNotCraft)
	}
	return cd, nil
}

// skillLevel is a player's level in a skill, 0 when he has none.
func skillLevel(ctx context.Context, tx application.Tx, playerID, skill string) (int, int64, error) {
	s, err := tx.Skills().Get(ctx, playerID, skill)
	if stderrors.Is(err, application.ErrSkillNotFound) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	return s.Level, s.XP, nil
}

// workshop is a standing building of a craft in a settlement and who owns it.
type workshop struct {
	b     application.SettlementBuildingInstance
	craft content.ApprenticeCraftDef
	owner string // the citizen who owns it; empty for a public workshop
}

func (h *ApprenticeHandler) workshopOf(ctx context.Context, tx application.Tx, cd content.CraftingDef, settlementID, buildingID string) (*workshop, error) {
	rows, err := tx.SettlementBuildings().List(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	var w *workshop
	for _, b := range rows {
		if b.ID != buildingID || b.Status != "complete" {
			continue
		}
		craft, ok := cd.Apprenticeship.CraftOfWorkplace(b.TypeCode)
		if !ok {
			return nil, refuseAppr(ApprRefusedNotCraft)
		}
		w = &workshop{b: b, craft: craft}
	}
	if w == nil {
		return nil, refuseAppr(ApprRefusedNotThere)
	}
	priv, err := tx.Citizens().PrivateBuildings(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	for _, p := range priv {
		if p.BuildingID == buildingID {
			w.owner = p.OwnerID
		}
	}
	return w, nil
}

func parseInt(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	var n int64
	neg := false
	for i, r := range s {
		switch {
		case i == 0 && r == '-':
			neg = true
		case r >= '0' && r <= '9':
			n = n*10 + int64(r-'0')
			if n > 1<<50 {
				return 0, false
			}
		default:
			return 0, false
		}
	}
	if neg {
		n = -n
	}
	return n, true
}

// masterSlots counts the offers and running contracts a master has.
func (h *ApprenticeHandler) masterSlots(ctx context.Context, tx application.Tx, masterID string) (int, error) {
	list, err := tx.Apprentices().OfMaster(ctx, masterID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range list {
		if a.Status == application.ApprenticeOffered || a.Status == application.ApprenticeActive {
			n++
		}
	}
	return n, nil
}

// Offer handles settlement.apprentice.offer: a master offers an apprenticeship at his workshop to a player, on terms of his own within
// the content's bounds.
func (h *ApprenticeHandler) Offer(ctx context.Context, meta envelope.Metadata, req ApprenticeRequest) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	code := playercode.Normalize(req.Player)
	var target *application.Player
	if playercode.Valid(code) {
		if found, err := h.search.Find(ctx, application.PlayerQuery{Kind: application.PlayerQueryPublicCode, PublicCode: code}); err == nil {
			target = found
		} else if !stderrors.Is(err, application.ErrPlayerNotFound) {
			return nil, err
		}
	}
	snap := h.content.Current()
	lang := meta.Language
	var notice string
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		cd, err := h.def(snap)
		if err != nil {
			return err
		}
		ad := cd.Apprenticeship
		if p.CityID == nil {
			return refuseAppr(ApprRefusedNotThere)
		}
		w, err := h.workshopOf(ctx, tx, cd, *p.CityID, req.Building)
		if err != nil {
			return err
		}
		if w.owner != p.ID {
			return refuseAppr(ApprRefusedNotMaster) // a player master teaches in his own workshop
		}
		if level, _, err := skillLevel(ctx, tx, p.ID, w.craft.Craft); err != nil {
			return err
		} else if level < ad.MasterMinLevel {
			return refuseAppr(ApprRefusedMasterLevel)
		}
		if n, err := h.masterSlots(ctx, tx, p.ID); err != nil {
			return err
		} else if n >= ad.MaxPerMaster {
			return refuseAppr(ApprRefusedSlots)
		}
		if target == nil {
			return refuseAppr(ApprRefusedUnknown)
		}
		if target.ID == p.ID {
			return refuseAppr(ApprRefusedSelf)
		}
		days, ok1 := parseInt(req.Days)
		fee, ok2 := parseInt(req.Fee)
		share, ok3 := parseInt(req.Share)
		if !ok1 {
			days = int64(ad.TermDefault)
		}
		if !ok2 {
			fee = 0
		}
		if !ok3 {
			share = int64(ad.WageShareDefaultBPS)
		}
		if days < int64(ad.TermMin) || days > int64(ad.TermMax) || fee < 0 || fee > ad.FeeMax || share < int64(ad.WageShareMinBPS) || share > 10_000 {
			b := apprenticeBounds(ad)
			return &apprRefusal{kind: ApprRefusedRange, bounds: &b}
		}
		if cur, err := tx.Apprentices().ActiveOf(ctx, target.ID); err != nil {
			return err
		} else if cur != nil {
			return refuseAppr(ApprRefusedBusy)
		}
		a := application.Apprenticeship{ID: h.ids.NewID(), SettlementID: *p.CityID, WorkplaceID: w.b.ID, Craft: w.craft.Craft,
			MasterKind: application.MasterPlayer, MasterID: p.ID, ApprenticeID: target.ID, TermDays: int(days), Fee: fee, WageShareBPS: int(share),
			Status: application.ApprenticeOffered, OfferedAt: h.now(), MasterXPDay: -1}
		if err := tx.Apprentices().Create(ctx, a); err != nil {
			return err
		}
		notice = plife.NoticeApprOffered
		return appendDomainEvent(ctx, tx, meta, "apprentice", "offered", a.ID, map[string]any{
			"apprenticeship_id": a.ID, "master_id": p.ID, "master_name": shownName(p), "apprentice_id": target.ID, "craft": a.Craft, "fee": fee, "term_days": days,
		})
	})
	if resp, err := h.finish(lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.mine(ctx, meta, notice)
}

// Join handles settlement.apprentice.join: a player takes up the trade at a public workshop, where the pool's craftsman is the master,
// for the default term, no fee and the default wage share. Such a master teaches up to the content's NPC level.
func (h *ApprenticeHandler) Join(ctx context.Context, meta envelope.Metadata, req ApprenticeRequest) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		cd, err := h.def(snap)
		if err != nil {
			return err
		}
		ad := cd.Apprenticeship
		if p.CityID == nil {
			return refuseAppr(ApprRefusedNotThere)
		}
		w, err := h.workshopOf(ctx, tx, cd, *p.CityID, req.Building)
		if err != nil {
			return err
		}
		if w.owner != "" {
			return refuseAppr(ApprRefusedNotThere) // a citizen's workshop has a player master, not the pool's
		}
		if level, _, err := skillLevel(ctx, tx, p.ID, w.craft.Craft); err != nil {
			return err
		} else if level >= ad.NPCMasterLevel {
			return refuseAppr(ApprRefusedTooSkilled)
		}
		if cur, err := tx.Apprentices().ActiveOf(ctx, p.ID); err != nil {
			return err
		} else if cur != nil {
			return refuseAppr(ApprRefusedBusy)
		}
		now := h.now()
		a := application.Apprenticeship{ID: h.ids.NewID(), SettlementID: *p.CityID, WorkplaceID: w.b.ID, Craft: w.craft.Craft,
			MasterKind: application.MasterNPC, ApprenticeID: p.ID, TermDays: ad.TermDefault, WageShareBPS: ad.WageShareDefaultBPS,
			Status: application.ApprenticeOffered, OfferedAt: now, MasterXPDay: -1}
		if err := tx.Apprentices().Create(ctx, a); err != nil {
			return err
		}
		return tx.Apprentices().Start(ctx, a.ID, now, now.Add(time.Duration(a.TermDays)*24*time.Hour), 0)
	})
	if resp, err := h.finish(lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.mine(ctx, meta, plife.NoticeApprStarted)
}

// taxBPS is the income tax the settlement's policy takes of a fee between players.
func (h *ApprenticeHandler) taxBPS(ctx context.Context, settlementID string) int64 {
	if h.policy == nil {
		return 0
	}
	city, err := h.cities.ByID(ctx, settlementID)
	if err != nil {
		return 0
	}
	pol, err := readLabourPolicy(ctx, h.policy, *city)
	if err != nil {
		return 0
	}
	return int64(pol.IncomeTaxBPS)
}

// Accept handles settlement.apprentice.accept: the apprentice takes an offer; the fee goes to the master (less the tax) and the term runs.
func (h *ApprenticeHandler) Accept(ctx context.Context, meta envelope.Metadata, req ApprenticeRequest) (*presentation.Response, error) {
	return h.change(ctx, meta, req.ID, plife.NoticeApprStarted, func(ctx context.Context, tx application.Tx, cd content.CraftingDef, p *application.Player, a *application.Apprenticeship, now time.Time) error {
		if a.ApprenticeID != p.ID {
			return refuseAppr(ApprRefusedNotYours)
		}
		if a.Status != application.ApprenticeOffered {
			return refuseAppr(ApprRefusedNotOffered)
		}
		if p.CityID == nil || *p.CityID != a.SettlementID {
			return refuseAppr(ApprRefusedNotThere)
		}
		ad := cd.Apprenticeship
		w, err := h.workshopOf(ctx, tx, cd, a.SettlementID, a.WorkplaceID)
		if err != nil {
			return err
		}
		if w.owner != a.MasterID {
			return refuseAppr(ApprRefusedNotThere)
		}
		if level, _, err := skillLevel(ctx, tx, a.MasterID, a.Craft); err != nil {
			return err
		} else if level < ad.MasterMinLevel {
			return refuseAppr(ApprRefusedMasterLevel)
		}
		if cur, err := tx.Apprentices().ActiveOf(ctx, p.ID); err != nil {
			return err
		} else if cur != nil {
			return refuseAppr(ApprRefusedBusy)
		}
		active := 0
		if list, err := tx.Apprentices().OfMaster(ctx, a.MasterID); err != nil {
			return err
		} else {
			for _, o := range list {
				if o.Status == application.ApprenticeActive {
					active++
				}
			}
		}
		if active >= ad.MaxPerMaster {
			return refuseAppr(ApprRefusedSlots)
		}
		if a.Fee > 0 {
			if err := h.payFee(ctx, tx, a, now); err != nil {
				return err
			}
		}
		return tx.Apprentices().Start(ctx, a.ID, now, now.Add(time.Duration(a.TermDays)*24*time.Hour), a.Fee)
	})
}

// payFee moves the fee from the apprentice to the master and the settlement's tax to its treasury, in one transaction.
func (h *ApprenticeHandler) payFee(ctx context.Context, tx application.Tx, a *application.Apprenticeship, now time.Time) error {
	payer, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, a.ApprenticeID)
	if err != nil {
		return err
	}
	if payer.Balance.Minor() < a.Fee {
		return refuseAppr(ApprRefusedAfford)
	}
	master, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, a.MasterID)
	if err != nil {
		return err
	}
	tax := a.Fee * h.taxBPS(ctx, a.SettlementID) / 10_000
	entries := []application.LedgerEntry{
		{AccountID: payer.ID, Amount: money.FromMinor(-a.Fee)},
		{AccountID: master.ID, Amount: money.FromMinor(a.Fee - tax)},
	}
	if tax > 0 {
		treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, a.SettlementID)
		if err != nil {
			return err
		}
		entries = append(entries, application.LedgerEntry{AccountID: treasury.ID, Amount: money.FromMinor(tax)})
	}
	_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{ID: h.ids.NewID(), Reason: application.ReasonApprenticeFee, CreatedAt: now,
		ReferenceType: application.ApprenticeReference, ReferenceID: a.ID, Entries: entries})
	return err
}

// Leave handles settlement.apprentice.leave: the apprentice turns an offer down or leaves a running contract, at once and with no debt.
func (h *ApprenticeHandler) Leave(ctx context.Context, meta envelope.Metadata, req ApprenticeRequest) (*presentation.Response, error) {
	return h.change(ctx, meta, req.ID, plife.NoticeApprLeft, func(ctx context.Context, tx application.Tx, cd content.CraftingDef, p *application.Player, a *application.Apprenticeship, now time.Time) error {
		if a.ApprenticeID != p.ID {
			return refuseAppr(ApprRefusedNotYours)
		}
		switch a.Status {
		case application.ApprenticeOffered:
			return tx.Apprentices().Finish(ctx, a.ID, application.ApprenticeDeclined, "", now, 0)
		case application.ApprenticeActive:
			return tx.Apprentices().Finish(ctx, a.ID, application.ApprenticeLeft, "", now, 0)
		}
		return refuseAppr(ApprRefusedNotActive)
	})
}

// Dismiss handles settlement.apprentice.dismiss: the master sends an apprentice away for a reason from the content's list, and gives
// back the part of the fee the term left unearned.
func (h *ApprenticeHandler) Dismiss(ctx context.Context, meta envelope.Metadata, req ApprenticeRequest) (*presentation.Response, error) {
	return h.change(ctx, meta, req.ID, plife.NoticeApprDismissed, func(ctx context.Context, tx application.Tx, cd content.CraftingDef, p *application.Player, a *application.Apprenticeship, now time.Time) error {
		if a.MasterKind != application.MasterPlayer || a.MasterID != p.ID {
			return refuseAppr(ApprRefusedNotYours)
		}
		if a.Status != application.ApprenticeActive {
			return refuseAppr(ApprRefusedNotActive)
		}
		ok := false
		for _, r := range cd.Apprenticeship.DismissReasons {
			ok = ok || r == req.Reason
		}
		if !ok {
			return refuseAppr(ApprRefusedReason)
		}
		var refund int64
		if a.FeePaid > 0 && now.Before(a.EndsAt) {
			left := int64(math.Ceil(float64(a.EndsAt.Sub(now)) / float64(24*time.Hour)))
			refund = a.FeePaid * min(left, int64(a.TermDays)) / int64(a.TermDays)
		}
		if refund > 0 {
			from, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, a.MasterID)
			if err != nil {
				return err
			}
			if from.Balance.Minor() < refund {
				return refuseAppr(ApprRefusedRefund)
			}
			to, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, a.ApprenticeID)
			if err != nil {
				return err
			}
			if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: h.ids.NewID(), Reason: application.ReasonApprenticeRefund, CreatedAt: now,
				ReferenceType: application.ApprenticeReference, ReferenceID: a.ID, Entries: []application.LedgerEntry{
					{AccountID: from.ID, Amount: money.FromMinor(-refund)}, {AccountID: to.ID, Amount: money.FromMinor(refund)}}}); err != nil {
				return err
			}
		}
		return tx.Apprentices().Finish(ctx, a.ID, application.ApprenticeDismissed, req.Reason, now, refund)
	})
}

// Exam handles settlement.apprentice.exam: at the end of the term the master judges the piece (the finished shifts at his workshop) and,
// on a pass, the apprentice gets the craft's certificate with the master's name. The pool's craftsman passes an apprentice who has reached
// his level; the apprentice asks for it himself.
func (h *ApprenticeHandler) Exam(ctx context.Context, meta envelope.Metadata, req ApprenticeRequest) (*presentation.Response, error) {
	return h.change(ctx, meta, req.ID, "", func(ctx context.Context, tx application.Tx, cd content.CraftingDef, p *application.Player, a *application.Apprenticeship, now time.Time) error {
		ad := cd.Apprenticeship
		npc := a.MasterKind == application.MasterNPC
		if (npc && a.ApprenticeID != p.ID) || (!npc && a.MasterID != p.ID) {
			return refuseAppr(ApprRefusedNotYours)
		}
		if a.Status == application.ApprenticeActive && !now.Before(a.EndsAt) {
			if err := tx.Apprentices().Finish(ctx, a.ID, application.ApprenticeCompleted, "", a.EndsAt, 0); err != nil {
				return err
			}
			a.Status = application.ApprenticeCompleted
		}
		if a.Status != application.ApprenticeCompleted {
			return refuseAppr(ApprRefusedNotReady)
		}
		if !a.CertifiedAt.IsZero() {
			return refuseAppr(ApprRefusedExamDone)
		}
		if a.Shifts < ad.ExamShifts {
			return refuseAppr(ApprRefusedNotReady)
		}
		craft, ok := ad.Craft(a.Craft)
		if !ok {
			return refuseAppr(ApprRefusedNotCraft)
		}
		pass := req.Pass != "fail"
		if npc {
			level, _, err := skillLevel(ctx, tx, a.ApprenticeID, a.Craft)
			if err != nil {
				return err
			}
			pass = level >= ad.NPCMasterLevel
		}
		if pass {
			// the course's own prerequisites (reading, for the crafts) are still the apprentice's to hold
			course, ok := h.content.Current().Course(craft.Course)
			if ok {
				held, err := tx.Education().Certifications(ctx, a.ApprenticeID)
				if err != nil {
					return err
				}
				have := map[string]bool{}
				for _, c := range held {
					have[c.CourseCode] = true
				}
				for _, need := range course.Prerequisites {
					if !have[string(need)] {
						return refuseAppr(ApprRefusedExamNeeds)
					}
				}
			}
		}
		kind, examiner := application.MasterPlayer, a.MasterID
		if npc {
			kind, examiner = application.MasterNPC, ""
		}
		fresh, err := tx.Apprentices().AddExam(ctx, application.ApprenticeExam{ID: h.ids.NewID(), ApprenticeshipID: a.ID, CandidateID: a.ApprenticeID, Craft: a.Craft,
			ExaminerKind: kind, ExaminerID: examiner, Shifts: a.Shifts, Passed: pass, At: now})
		if err != nil || !fresh {
			if err == nil {
				err = refuseAppr(ApprRefusedExamDone)
			}
			return err
		}
		if pass {
			if _, err := tx.Education().CertifyVia(ctx, application.CertificateIssue{PlayerID: a.ApprenticeID, CourseCode: craft.Course, IssuedBy: examiner,
				Route: application.RouteApprenticeship, ApprenticeshipID: a.ID, At: now}); err != nil {
				return err
			}
		}
		return appendDomainEvent(ctx, tx, meta, "apprentice", "examined", a.ID, map[string]any{
			"apprenticeship_id": a.ID, "apprentice_id": a.ApprenticeID, "master_id": a.MasterID, "craft": a.Craft, "course": craft.Course, "passed": pass,
		})
	})
}

type apprAction func(ctx context.Context, tx application.Tx, cd content.CraftingDef, p *application.Player, a *application.Apprenticeship, now time.Time) error

// change is the frame of a command on one apprenticeship: the player, the lock, the row, the action, then the list.
func (h *ApprenticeHandler) change(ctx context.Context, meta envelope.Metadata, id, notice string, do apprAction) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		cd, err := h.def(snap)
		if err != nil {
			return err
		}
		if err := tx.Apprentices().Lock(ctx, id); err != nil {
			return err
		}
		a, err := tx.Apprentices().Get(ctx, id)
		if err != nil {
			return err
		}
		if a == nil {
			return refuseAppr(ApprRefusedNotOffered)
		}
		if a.ApprenticeID != p.ID && a.MasterID != p.ID {
			return refuseAppr(ApprRefusedNotYours)
		}
		return do(ctx, tx, cd, p, a, h.now())
	})
	if resp, err := h.finish(lang, err); resp != nil || err != nil {
		return resp, err
	}
	return h.mine(ctx, meta, notice)
}

// Mine handles settlement.apprentice.mine: one's apprenticeships, as apprentice and as master, and the public workshops one could join.
func (h *ApprenticeHandler) Mine(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	return h.mine(ctx, meta, "")
}

func (h *ApprenticeHandler) mine(ctx context.Context, meta envelope.Metadata, notice string) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view plife.ApprenticesView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		cd, err := h.def(snap)
		if err != nil {
			return err
		}
		view.Notice = notice
		now := h.now()
		ad := cd.Apprenticeship
		b := apprenticeBounds(ad)
		view.Bounds = &b
		mine, err := tx.Apprentices().OfApprentice(ctx, p.ID, 10)
		if err != nil {
			return err
		}
		asMaster, err := tx.Apprentices().OfMaster(ctx, p.ID)
		if err != nil {
			return err
		}
		names := map[string]string{}
		nameOf := func(id string) (string, error) {
			if n, ok := names[id]; ok {
				return n, nil
			}
			pl, err := tx.Players().GetByID(ctx, id)
			if err != nil {
				if stderrors.Is(err, application.ErrPlayerNotFound) {
					return "", nil
				}
				return "", err
			}
			names[id] = shownName(pl)
			return names[id], nil
		}
		shops := map[string]map[string]presentation.Named{}
		shopOf := func(a application.Apprenticeship) (presentation.Named, error) {
			m, ok := shops[a.SettlementID]
			if !ok {
				rows, err := tx.SettlementBuildings().List(ctx, a.SettlementID)
				if err != nil {
					return presentation.Named{}, err
				}
				m = map[string]presentation.Named{}
				for _, b := range rows {
					if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok {
						m[b.ID] = presentation.Named{Code: d.Code, Name: d.Name}
					}
				}
				shops[a.SettlementID] = m
			}
			return m[a.WorkplaceID], nil
		}
		line := func(a application.Apprenticeship, role string) (plife.ApprenticeLine, error) {
			l := plife.ApprenticeLine{ID: a.ID, Craft: a.Craft, Role: role, Status: a.Status, NPC: a.MasterKind == application.MasterNPC, Fee: a.Fee,
				WageShareBPS: a.WageShareBPS, TermDays: a.TermDays, EndsAt: a.EndsAt, Shifts: a.Shifts, ExamShifts: ad.ExamShifts, ExtraXP: a.XPExtra,
				Certified: !a.CertifiedAt.IsZero()}
			other := a.MasterID
			if role == plife.RoleMaster {
				other = a.ApprenticeID
			}
			if other != "" {
				n, err := nameOf(other)
				if err != nil {
					return l, err
				}
				l.With = presentation.Named{Code: other, Name: n}
			}
			w, err := shopOf(a)
			if err != nil {
				return l, err
			}
			l.Workshop = w
			done := a.Status == application.ApprenticeCompleted || (a.Status == application.ApprenticeActive && !now.Before(a.EndsAt))
			ready := done && a.CertifiedAt.IsZero() && a.Shifts >= ad.ExamShifts
			switch role {
			case plife.RoleApprentice:
				l.CanAccept = a.Status == application.ApprenticeOffered
				l.CanLeave = a.Status == application.ApprenticeOffered || a.Status == application.ApprenticeActive
				l.CanExam = ready && l.NPC
			case plife.RoleMaster:
				l.CanDismiss = a.Status == application.ApprenticeActive && now.Before(a.EndsAt)
				l.CanExam = ready && !l.NPC
			}
			return l, nil
		}
		for _, a := range mine {
			l, err := line(a, plife.RoleApprentice)
			if err != nil {
				return err
			}
			view.Lines = append(view.Lines, l)
		}
		for _, a := range asMaster {
			l, err := line(a, plife.RoleMaster)
			if err != nil {
				return err
			}
			view.Lines = append(view.Lines, l)
		}
		// public workshops of the settlement the viewer could join
		if p.CityID != nil {
			if cur, err := tx.Apprentices().ActiveOf(ctx, p.ID); err != nil {
				return err
			} else if cur == nil {
				rows, err := tx.SettlementBuildings().List(ctx, *p.CityID)
				if err != nil {
					return err
				}
				priv, err := tx.Citizens().PrivateBuildings(ctx, *p.CityID)
				if err != nil {
					return err
				}
				owned := map[string]bool{}
				for _, pb := range priv {
					owned[pb.BuildingID] = true
				}
				for _, b := range rows {
					craft, ok := ad.CraftOfWorkplace(b.TypeCode)
					if !ok || b.Status != "complete" || owned[b.ID] || strings.HasSuffix(b.TypeCode, content.PrivateSuffix) {
						continue
					}
					level, _, err := skillLevel(ctx, tx, p.ID, craft.Craft)
					if err != nil {
						return err
					}
					if level >= ad.NPCMasterLevel {
						continue
					}
					if d, ok := snap.SettlementBuildingDef(b.TypeCode); ok {
						view.CanJoin = append(view.CanJoin, plife.ApprenticeJoinLine{Workshop: presentation.Named{Code: b.ID, Name: d.Name}, Craft: craft.Craft, UpTo: ad.NPCMasterLevel})
					}
				}
			}
		}
		return nil
	})
	if resp, err := h.finish(lang, err); resp != nil || err != nil {
		return resp, err
	}
	return plife.Apprentices(presentation.Ctx{Lang: lang}, view), nil
}

// ---- the hooks the village handler calls ----

// WageShareBPS is the share of a shift's wage a player keeps at a workshop: the contract's share while he is its apprentice there.
func (h *ApprenticeHandler) WageShareBPS(ctx context.Context, tx application.Tx, playerID, buildingID string, now time.Time) (int64, error) {
	a, err := tx.Apprentices().ActiveOf(ctx, playerID)
	if err != nil || a == nil || a.WorkplaceID != buildingID || !a.Running(now) {
		return 10_000, err
	}
	return int64(a.WageShareBPS), nil
}

// OnShift gives an apprentice the extra learning of a finished shift at his master's workshop, and the master his teaching xp for the day.
func (h *ApprenticeHandler) OnShift(ctx context.Context, tx application.Tx, snap *content.Snapshot, playerID, buildingID, skill string, baseXP int64, now time.Time) error {
	a, err := tx.Apprentices().ActiveOf(ctx, playerID)
	if err != nil || a == nil || a.WorkplaceID != buildingID || a.Craft != skill {
		return err
	}
	cd, ok := snap.Crafting()
	if !ok {
		return nil
	}
	ad := cd.Apprenticeship
	if !a.Running(now) {
		return tx.Apprentices().Finish(ctx, a.ID, application.ApprenticeCompleted, "", a.EndsAt, 0)
	}
	masterLevel := ad.NPCMasterLevel
	if a.MasterKind == application.MasterPlayer {
		if masterLevel, _, err = skillLevel(ctx, tx, a.MasterID, a.Craft); err != nil {
			return err
		}
	}
	_, have, err := skillLevel(ctx, tx, playerID, skill)
	if err != nil {
		return err
	}
	// the learning beside a master is a share more of the base xp, and never takes the apprentice past his master
	extra := baseXP * int64(ad.LearningBPS(masterLevel)-10_000) / 10_000
	extra = max(min(extra, player.SkillXPForLevel(masterLevel+1)-1-have), 0)
	if extra > 0 {
		skills, err := tx.Skills().List(ctx, playerID)
		if err != nil {
			return err
		}
		if _, err := awardSkillXP(ctx, tx, snap, playerID, skills, []skillAward{{Skill: player.SkillCode(skill), XP: extra}}, now); err != nil {
			return err
		}
	}
	day := now.UTC().Unix() / 86400
	masterDay := int64(-1)
	if a.MasterKind == application.MasterPlayer && h.teacherXP > 0 && a.MasterXPDay != day {
		skills, err := tx.Skills().List(ctx, a.MasterID)
		if err != nil {
			return err
		}
		if _, err := awardSkillXP(ctx, tx, snap, a.MasterID, skills, []skillAward{{Skill: "teaching", XP: h.teacherXP}}, now); err != nil {
			return err
		}
		masterDay = day
	}
	return tx.Apprentices().RecordShift(ctx, a.ID, extra, masterDay)
}

// ---- the directory of teachers ----

func haversineKM(la1, lo1, la2, lo2 float64) float64 {
	const r = 6371.0
	p1, p2 := la1*math.Pi/180, la2*math.Pi/180
	dla, dlo := (la2-la1)*math.Pi/180, (lo2-lo1)*math.Pi/180
	a := math.Sin(dla/2)*math.Sin(dla/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dlo/2)*math.Sin(dlo/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}

// Teachers handles education.teachers: where a craft can be learned within reach, and from whom.
func (h *ApprenticeHandler) Teachers(ctx context.Context, meta envelope.Metadata, req ApprenticeRequest) (*presentation.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view plife.TeachersView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		cd, err := h.def(snap)
		if err != nil {
			return err
		}
		ad, td := cd.Apprenticeship, cd.Teaching
		for _, c := range ad.Crafts {
			view.Crafts = append(view.Crafts, c.Craft)
		}
		if r := h.teachingRules; !r.RuleAt.IsZero() && !r.On(h.now()) {
			at := r.RuleAt.AddDate(0, 0, int(max(r.GraceDays, 0)))
			view.StartsAt = &at // the teacher's skill counts from this day (the rule date plus its grace)
		}
		craftCode := req.Craft
		if craftCode == "" {
			// the viewer's best craft, else the first of the list
			craftCode = ad.Crafts[0].Craft
			best := 0
			for _, c := range ad.Crafts {
				lvl, _, serr := skillLevel(ctx, tx, p.ID, c.Craft)
				if serr != nil {
					return serr
				}
				if lvl > best {
					best, craftCode = lvl, c.Craft
				}
			}
		}
		craft, ok := ad.Craft(craftCode)
		if !ok {
			return refuseAppr(ApprRefusedNotCraft)
		}
		view.Craft = craft.Craft
		courseDef, _ := snap.CourseDef(craft.Course)
		view.Course = presentation.Named{Code: craft.Course, Name: courseDef.Name}
		_, w, err := h.worlds.Active(ctx)
		if err != nil {
			return err
		}
		latLon := func(s application.FoundedSettlement) (float64, float64, bool) {
			if s.WorldCellID < 0 || int(s.WorldCellID) >= len(w.Cells) {
				return 0, 0, false
			}
			pt := w.Cells[s.WorldCellID].Point
			return pt.LatDeg, pt.LonDeg, true
		}
		var here *application.FoundedSettlement
		var hlat, hlon float64
		haveHere := false
		if p.CityID != nil {
			if s, err := tx.Settlements().ByID(ctx, *p.CityID); err == nil {
				here = &s
				hlat, hlon, haveHere = latLon(s)
			} else if !stderrors.Is(err, application.ErrCityNotFound) {
				return err
			}
		}
		all, err := tx.Settlements().Founded(ctx)
		if err != nil {
			return err
		}
		type cand struct {
			s  application.FoundedSettlement
			km int
		}
		var cands []cand
		for _, s := range all {
			km := 0
			if haveHere {
				lat, lon, ok := latLon(s)
				if !ok {
					continue
				}
				km = int(haversineKM(hlat, hlon, lat, lon))
				if km > td.DirectoryKM && !(here != nil && here.CityID == s.CityID) {
					continue
				}
			}
			cands = append(cands, cand{s, km})
		}
		sort.Slice(cands, func(i, j int) bool {
			return cands[i].km < cands[j].km || (cands[i].km == cands[j].km && cands[i].s.CityID < cands[j].s.CityID)
		})
		if len(cands) > td.DirectoryMax {
			cands = cands[:td.DirectoryMax]
		}
		named := func(id string) (presentation.Named, error) {
			pl, err := tx.Players().GetByID(ctx, id)
			if err != nil {
				if stderrors.Is(err, application.ErrPlayerNotFound) {
					return presentation.Named{}, nil
				}
				return presentation.Named{}, err
			}
			return presentation.Named{Code: pl.PublicCode, Name: shownName(pl)}, nil
		}
		for _, c := range cands {
			set := presentation.Named{Code: c.s.Code, Name: c.s.Name}
			isHere := here != nil && here.CityID == c.s.CityID
			// class teachers of the craft's course
			teachers, err := tx.Education().Teachers(ctx, c.s.CityID, craft.Course)
			if err != nil {
				return err
			}
			for _, t := range teachers {
				lvl, tl := td.NPCLevel, 0
				var who presentation.Named
				if t.Kind != application.TeacherNPC && t.PlayerID != "" {
					if lvl, tl, err = skillLevels(ctx, tx, t.PlayerID, craft.Craft, "teaching"); err != nil {
						return err
					}
					if who, err = named(t.PlayerID); err != nil {
						return err
					}
				}
				n, err := tx.Education().Students(ctx, t.ID)
				if err != nil {
					return err
				}
				view.Entries = append(view.Entries, plife.TeacherEntry{Kind: plife.DirKindClass, Settlement: set, KM: c.km, Teacher: who,
					CraftLevel: lvl, TeachingLevel: tl, UpTo: max(lvl-1, 0), Fee: courseDef.Cost, FreeSeats: max(12-n, 0), Here: isHere})
			}
			// workshops: the pool's craftsman at a public one, a player master at his own
			rows, err := tx.SettlementBuildings().List(ctx, c.s.CityID)
			if err != nil {
				return err
			}
			priv, err := tx.Citizens().PrivateBuildings(ctx, c.s.CityID)
			if err != nil {
				return err
			}
			owner := map[string]string{}
			for _, pb := range priv {
				owner[pb.BuildingID] = pb.OwnerID
			}
			publicSeen := false
			for _, b := range rows {
				cr, ok := ad.CraftOfWorkplace(b.TypeCode)
				if !ok || cr.Craft != craft.Craft || b.Status != "complete" {
					continue
				}
				if o := owner[b.ID]; o != "" {
					lvl, _, err := skillLevel(ctx, tx, o, craft.Craft)
					if err != nil {
						return err
					}
					if lvl < ad.MasterMinLevel {
						continue
					}
					slots, err := h.masterSlots(ctx, tx, o)
					if err != nil {
						return err
					}
					who, err := named(o)
					if err != nil {
						return err
					}
					_, tl, _ := skillLevels(ctx, tx, o, craft.Craft, "teaching")
					view.Entries = append(view.Entries, plife.TeacherEntry{Kind: plife.DirKindMaster, Settlement: set, KM: c.km, Teacher: who,
						CraftLevel: lvl, TeachingLevel: tl, UpTo: lvl, FreeSeats: max(ad.MaxPerMaster-slots, 0), Here: isHere})
				} else if !publicSeen {
					publicSeen = true
					view.Entries = append(view.Entries, plife.TeacherEntry{Kind: plife.DirKindNPC, Settlement: set, KM: c.km, CraftLevel: ad.NPCMasterLevel,
						UpTo: ad.NPCMasterLevel, Here: isHere})
				}
			}
		}
		if h.homeCode != "" {
			if city, err := h.cities.ByCode(ctx, h.homeCode); err == nil && city != nil {
				view.Entries = append(view.Entries, plife.TeacherEntry{Kind: plife.DirKindCity, Settlement: presentation.Named{Code: city.Code, Name: city.Name}, Fee: courseDef.Cost, FreeSeats: 99})
			}
		}
		sort.SliceStable(view.Entries, func(i, j int) bool {
			a, b := view.Entries[i], view.Entries[j]
			if a.Here != b.Here {
				return a.Here
			}
			if a.KM != b.KM {
				return a.KM < b.KM
			}
			return a.UpTo > b.UpTo
		})
		if len(view.Entries) > 24 {
			view.Entries = view.Entries[:24]
		}
		return nil
	})
	if resp, err := h.finish(lang, err); resp != nil || err != nil {
		return resp, err
	}
	return plife.Teachers(presentation.Ctx{Lang: lang}, view), nil
}
