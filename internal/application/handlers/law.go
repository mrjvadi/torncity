package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// lawMeta is the metadata a scheduled tick of the item carries.
type lawMeta = envelope.Metadata

// lawCtxFor builds the context of a tick: the settlement a case or a sentence belongs to. There is no player; the mature switch is off (a tick never
// handles the mature content, and a switch is never read inside a unit of work).
func (h *VillageHandler) lawCtxFor(ctx context.Context, tx application.Tx, meta lawMeta, settlementID string, now time.Time) (*lawCtx, error) {
	s, err := tx.Settlements().ByID(ctx, settlementID)
	if err != nil {
		return nil, err
	}
	return &lawCtx{meta: meta, snap: h.content.Current(), s: s, has: true, now: now, t: h.law.T}, nil
}

// Law handles settlement.law: the screen of the police, the courts, the jail, the informants, the hidden board, laundering and customs, and every act
// on it. One entry point, like the other village screens; the permission an act needs is asked inside the act.
func (h *VillageHandler) Law(ctx context.Context, meta envelope.Metadata, req VillageLawRequest) (*presentation.Response, error) {
	mature := h.matureOn(ctx)
	snap := h.content.Current()
	lang := meta.Language
	var view village.LawView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, l, err := h.viewer(ctx, tx, meta)
		if err != nil {
			return err
		}
		lang = l
		s, has, err := h.settlementOpt(ctx, tx, meta)
		if err != nil {
			return err
		}
		lc := &lawCtx{meta: meta, snap: snap, s: s, has: has, p: p, now: h.now(), t: h.law.T, req: req, mature: mature}
		notice := ""
		if act := strings.TrimSpace(req.Action); act != "" && act != village.LawActionOpen {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if fresh {
				if notice, err = h.lawAct(ctx, tx, lc, act); err != nil {
					return err
				}
			}
		}
		if has {
			// a closed jail holds nobody; the hidden board's fences put the day's goods up
			if err := h.lawJailCheck(ctx, tx, lc); err != nil {
				return err
			}
			if err := h.lawSupply(ctx, tx, lc); err != nil {
				return err
			}
		}
		view, err = h.lawView(ctx, tx, lc)
		view.Notice = notice
		return err
	})
	if resp, err := h.villageFinish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return village.Law(h.screen(meta, lang), view), nil
}

// lawAct runs one act of the screen and returns the notice to show.
func (h *VillageHandler) lawAct(ctx context.Context, tx application.Tx, lc *lawCtx, act string) (string, error) {
	// the short forms of a button are the same acts with their last argument set
	switch act {
	case village.LawActionDefendNPC:
		act, lc.req.Kind = village.LawActionDefend, "npc"
	case village.LawActionAppealAccused:
		act, lc.req.Kind = village.LawActionAppeal, "accused"
	case village.LawActionAppealProsecution:
		act, lc.req.Kind = village.LawActionAppeal, "prosecution"
	case village.LawActionAppealConfirm:
		act, lc.req.Code = village.LawActionAppealDecide, "confirm"
	case village.LawActionAppealQuash:
		act, lc.req.Code = village.LawActionAppealDecide, "quash"
	}
	switch act {
	case village.LawActionModel:
		return h.lawSetModel(ctx, tx, lc)
	case village.LawActionOffence:
		return h.lawSetOffence(ctx, tx, lc)
	case village.LawActionOffenceUnset:
		return h.lawUnsetOffence(ctx, tx, lc)
	case village.LawActionPatrol:
		return h.lawPatrol(ctx, tx, lc)
	case village.LawActionComplain:
		return h.lawComplain(ctx, tx, lc)
	case village.LawActionInvestigate:
		return h.lawInvestigate(ctx, tx, lc)
	case village.LawActionWarrantAsk:
		return h.lawWarrantAsk(ctx, tx, lc)
	case village.LawActionWarrantSign:
		return h.lawWarrantSign(ctx, tx, lc, true)
	case village.LawActionWarrantRefuse:
		return h.lawWarrantSign(ctx, tx, lc, false)
	case village.LawActionArrest:
		return h.lawArrest(ctx, tx, lc)
	case village.LawActionCharge:
		return h.lawCharge(ctx, tx, lc)
	case village.LawActionDrop:
		return h.lawDrop(ctx, tx, lc)
	case village.LawActionDefend:
		return h.lawDefend(ctx, tx, lc)
	case village.LawActionExclude:
		return h.lawExclude(ctx, tx, lc)
	case village.LawActionVerdict:
		return h.lawVerdict(ctx, tx, lc)
	case village.LawActionAppeal:
		return h.lawAppeal(ctx, tx, lc)
	case village.LawActionAppealDecide:
		return h.lawAppealDecide(ctx, tx, lc)
	case village.LawActionPardon:
		return h.lawPardon(ctx, tx, lc)
	case village.LawActionBail:
		return h.lawBail(ctx, tx, lc)
	case village.LawActionRecruit:
		return h.lawRecruit(ctx, tx, lc)
	case village.LawActionTip:
		return h.lawTip(ctx, tx, lc)
	case village.LawActionTake:
		return h.lawInformantTake(ctx, tx, lc)
	case village.LawActionRetire:
		return h.lawRetire(ctx, tx, lc)
	case village.LawActionBribe:
		return h.lawBribeOffer(ctx, tx, lc)
	case village.LawActionBribeAccept:
		return h.lawBribeAnswer(ctx, tx, lc, true)
	case village.LawActionBribeRefuse:
		return h.lawBribeAnswer(ctx, tx, lc, false)
	case village.LawActionAudit:
		return h.lawAuditIntegrity(ctx, tx, lc)
	case village.LawActionAuditBooks:
		return h.lawAuditBooks(ctx, tx, lc)
	case village.LawActionAsk:
		return h.lawAsk(ctx, tx, lc)
	case village.LawActionIntroduce:
		return h.lawIntroduce(ctx, tx, lc)
	case village.LawActionBroker:
		return h.lawBroker(ctx, tx, lc)
	case village.LawActionList:
		return h.lawList(ctx, tx, lc)
	case village.LawActionDelist:
		return h.lawDelist(ctx, tx, lc)
	case village.LawActionBuy:
		return h.lawBuy(ctx, tx, lc)
	case village.LawActionConfirm:
		return h.lawConfirm(ctx, tx, lc)
	case village.LawActionRefund:
		return h.lawRefund(ctx, tx, lc)
	case village.LawActionScam:
		return h.lawScam(ctx, tx, lc)
	case village.LawActionFront:
		return h.lawFrontRegister(ctx, tx, lc)
	case village.LawActionLaunder:
		return h.lawLaunder(ctx, tx, lc)
	case village.LawActionDeclare:
		return h.lawDeclare(ctx, tx, lc)
	case village.LawActionCross:
		return h.lawCross(ctx, tx, lc)
	case village.LawActionGangMark:
		return h.lawGangMark(ctx, tx, lc, false)
	case village.LawActionGangDeclare:
		return h.lawGangMark(ctx, tx, lc, true)
	}
	return "", lawRefuse(village.LawBadArgs)
}

// lawTick dispatches the ticks of the item; the polity tick calls it for the kinds it does not know.
func (h *VillageHandler) lawTick(ctx context.Context, tx application.Tx, meta envelope.Metadata, kind, id, aux string, now time.Time) (bool, error) {
	switch kind {
	case application.TickLawHearing:
		return true, h.tickLawHearing(ctx, tx, meta, id, aux, now)
	case application.TickLawFinal:
		return true, h.tickLawFinal(ctx, tx, meta, id, now)
	case application.TickLawAppeal:
		return true, h.tickLawAppeal(ctx, tx, meta, id, now)
	case application.TickLawJail:
		return true, h.tickLawJail(ctx, tx, meta, id, now)
	case application.TickLawEscrow:
		return true, h.tickLawEscrow(ctx, tx, meta, id, now)
	case application.TickLawBatch:
		return true, h.tickLawBatch(ctx, tx, meta, id, now)
	case application.TickLawCustody:
		return true, h.tickLawCustody(ctx, tx, meta, id, now)
	}
	return false, nil
}

// tickLawCustody lets a case lapse whose custody clock ran out with no charge.
func (h *VillageHandler) tickLawCustody(ctx context.Context, tx application.Tx, meta lawMeta, caseID string, now time.Time) error {
	c, err := tx.Law().CaseByID(ctx, caseID, true)
	if err != nil || c == nil {
		return err
	}
	lc, err := h.lawCtxFor(ctx, tx, meta, c.SettlementID, now)
	if err != nil || lc == nil {
		return err
	}
	return h.lawCustodyLapse(ctx, tx, lc, c)
}
