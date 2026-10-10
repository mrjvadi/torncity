package handlers

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// The stall keeper (ADR 0062 and its addendum, plan A3c). A stall sells for its owner while he is in the settlement. A
// market trader who has to leave hires a keeper: in the real market a stallholder takes on an assistant who minds the
// stall, paid either by the day or by a share of the takings. Both are possible here and the owner chooses when he hires.
// The keeper is a person of the settlement's labour pool (like the shopkeeper and the market clerk) who holds one seat
// while he is hired.
//
//   - WHO WORKS THERE. The keeper, an NPC of the pool; no pool seat free: no hire.
//   - WHAT IT CONSUMES. One seat of the pool while hired. Paid by a share: that share of every sale made in the owner's
//     absence (config trade.stall_keeper_share_*, the owner picks a number in the range). Paid by the day: a fixed wage
//     from the owner's cash each local day the keeper holds the seat (trade.stall_keeper_wage*), ledger reason
//     stall_keeper_day_wage to the sink; when the owner cannot pay, the keeper leaves (the hire ends with the reason
//     wage_unpaid, an event goes out and the lot panel says so).
//   - WHAT IT PROVIDES. The owner's asks stay on the book while he is away.
//   - WHAT BREAKS. No keeper: the stall is shut while the owner is away (as before). Dismissing him ends it at once.

// StallKeeperTerms are the numbers of the keeper's pay (config trade.stall_keeper_*): the share of sales and the day wage,
// each with its default and the range the owner chooses within.
type StallKeeperTerms struct {
	ShareBPS, ShareMinBPS, ShareMaxBPS int64
	Wage, WageMin, WageMax             int64
}

func (t StallKeeperTerms) enabled() bool { return t.ShareBPS > 0 || t.Wage > 0 }

// WithStallKeeper gives the village handler the keeper's terms.
func (h *VillageHandler) WithStallKeeper(t StallKeeperTerms) *VillageHandler {
	h.keeperTerms = t
	return h
}

// keeperEndedNoticeWindow is how long the lot panel keeps saying that a keeper left because the wage was not paid.
const keeperEndedNoticeWindow = 7 * 24 * time.Hour

// stallKeeperLine is the keeper block of a stall the viewer owns; nil on any other building.
func (h *VillageHandler) stallKeeperLine(ctx context.Context, tx application.Tx, lc *lotCtx) (*village.LotKeeperLine, error) {
	if lc.owner.private == nil || lc.f == nil || !hasStaffRole(lc.k, lc.f.Function, "stall_keeper") {
		return nil, nil
	}
	t := h.keeperTerms
	line := &village.LotKeeperLine{ShareBPS: t.ShareBPS, ShareMinBPS: t.ShareMinBPS, ShareMaxBPS: t.ShareMaxBPS,
		Wage: t.Wage, WageMin: t.WageMin, WageMax: t.WageMax}
	hire, err := tx.StallKeepers().OfOwner(ctx, lc.s.CityID, lc.p.ID)
	if err != nil {
		return nil, err
	}
	if hire != nil {
		line.Hired, line.Pay, line.ShareBPS, line.Wage = true, hire.Pay, hire.ShareBPS, hire.DailyWage
		var terr error
		if line.SoldAway, line.CutTotal, terr = tx.StallKeepers().Takings(ctx, lc.s.CityID, lc.p.ID, hire.HiredAt); terr != nil {
			return nil, terr
		}
		dayStart := localDayStart(h.now(), lc.s.Zone())
		if line.SoldAwayToday, line.CutToday, terr = tx.StallKeepers().Takings(ctx, lc.s.CityID, lc.p.ID, maxTime(dayStart, hire.HiredAt)); terr != nil {
			return nil, terr
		}
		return line, nil
	}
	if last, err := tx.StallKeepers().LastEnded(ctx, lc.s.CityID, lc.p.ID); err != nil {
		return nil, err
	} else if last != nil && last.EndedReason == application.KeeperEndedWageUnpaid && last.EndedAt != nil && h.now().Sub(*last.EndedAt) < keeperEndedNoticeWindow {
		line.Left = application.KeeperEndedWageUnpaid
	}
	free, err := h.keeperSeatsFree(ctx, tx, lc.k.snap, lc.s, lc.buildings)
	if err != nil {
		return nil, err
	}
	line.SeatsFree = free
	switch {
	case !t.enabled():
		line.Reason = "off"
	case free < 1:
		line.Reason = "no_seat"
	default:
		line.Can = true
	}
	return line, nil
}

func hasStaffRole(k lotKit, function, role string) bool {
	for _, st := range k.defs[function].Staff {
		if st.Role == role {
			return true
		}
	}
	return false
}

// keeperSeatsFree is the people of the labour pool still free to be hired.
func (h *VillageHandler) keeperSeatsFree(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement,
	buildings []application.SettlementBuildingInstance,
) (int64, error) {
	market, err := h.laborMarket(ctx, tx, snap, s, buildings)
	if err != nil {
		return 0, err
	}
	return market.claims.Available(), nil
}

// keeperTermsOf reads the pay the owner asked for (code share or wage, name the number; empty: the default) and holds it to
// the configured range. ok is false for anything outside it.
func (h *VillageHandler) keeperTermsOf(code, number string) (pay string, share, wage int64, ok bool) {
	t := h.keeperTerms
	n := int64(-1)
	if strings.TrimSpace(number) != "" {
		v, err := strconv.ParseInt(strings.TrimSpace(number), 10, 64)
		if err != nil {
			return "", 0, 0, false
		}
		n = v
	}
	switch strings.TrimSpace(code) {
	case "", application.KeeperPayShare:
		if t.ShareBPS <= 0 {
			return "", 0, 0, false
		}
		if n < 0 {
			n = t.ShareBPS
		}
		return application.KeeperPayShare, n, 0, n >= t.ShareMinBPS && n <= t.ShareMaxBPS
	case application.KeeperPayWage:
		if t.Wage <= 0 {
			return "", 0, 0, false
		}
		if n < 0 {
			n = t.Wage
		}
		return application.KeeperPayWage, 0, n, n >= t.WageMin && n <= t.WageMax
	}
	return "", 0, 0, false
}

// keeperAct hires or dismisses the owner's stall keeper.
func (h *VillageHandler) keeperAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, lc *lotCtx, view *village.LotManageView, action string, confirmed bool) error {
	if lc.owner.private == nil || !hasStaffRole(lc.k, lc.f.Function, "stall_keeper") {
		return refuseVillage(village.LotNoKeeper, village.AddrLotManage)
	}
	now := h.now()
	switch action {
	case village.LotActionKeeperHire:
		line, err := h.stallKeeperLine(ctx, tx, lc)
		if err != nil {
			return err
		}
		view.Stage = village.LotAsk
		view.Keeper = line
		if line.Hired {
			return refuseVillage(village.LotKeeperNone, village.AddrLotManage)
		}
		pay, share, wage, ok := h.keeperTermsOf(view.Code, view.Name)
		if !ok {
			return refuseVillage(village.LotKeeperTerms, village.AddrLotManage)
		}
		view.Code = pay
		if pay == application.KeeperPayShare {
			view.Name = strconv.FormatInt(share, 10)
		} else {
			view.Name = strconv.FormatInt(wage, 10)
		}
		if !confirmed {
			return nil
		}
		if !line.Can {
			return refuseVillage(village.LotKeeperNone, village.AddrLotManage)
		}
		fresh, err := h.reserve(ctx, tx, lc.p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			view.Stage = village.LotDone
			return nil
		}
		if _, err := tx.StallKeepers().Hire(ctx, application.StallKeeper{ID: h.ids.NewID(), SettlementID: lc.s.CityID, OwnerID: lc.p.ID,
			Pay: pay, ShareBPS: share, DailyWage: wage, HiredAt: now}); err != nil {
			return err
		}
	case village.LotActionKeeperEnd:
		if !confirmed {
			view.Stage = village.LotAsk
			return nil
		}
		fresh, err := h.reserve(ctx, tx, lc.p.ID, meta)
		if err != nil {
			return err
		}
		if fresh {
			if _, err := tx.StallKeepers().End(ctx, lc.s.CityID, lc.p.ID, now, application.KeeperEndedDismissed); err != nil {
				return err
			}
		}
	}
	view.Stage = village.LotDone
	return h.refreshDetail(ctx, tx, lc, view)
}

// SettleKeeperWages charges the day wage of every keeper hired by the day for today's local day. Idempotent: the row
// stall_keeper_wages (keeper, day) is the fence, written with the ledger transaction it names. An owner who cannot pay
// loses the keeper: the hire ends with the reason wage_unpaid and an event goes out.
func (h *VillageHandler) SettleKeeperWages(ctx context.Context, tx application.Tx, s application.FoundedSettlement, now time.Time, meta envelope.Metadata) error {
	if !h.service.enabled() {
		return nil
	}
	keepers, err := tx.StallKeepers().OpenWages(ctx, s.CityID)
	if err != nil || len(keepers) == 0 {
		return err
	}
	today := h.service.Clock.DayAtIn(now, s.Zone())
	for _, k := range keepers {
		acct, cash, err := playerCash(ctx, tx, k.OwnerID)
		if err != nil {
			return err
		}
		if cash < k.DailyWage {
			if ended, err := tx.StallKeepers().End(ctx, s.CityID, k.OwnerID, now, application.KeeperEndedWageUnpaid); err != nil {
				return err
			} else if ended {
				if err := appendVillageEvent(ctx, tx, meta, "stall_keeper_left", k.ID, map[string]any{
					"settlement_id": s.CityID, "owner_id": k.OwnerID, "reason": application.KeeperEndedWageUnpaid,
				}); err != nil {
					return err
				}
			}
			continue
		}
		txID := h.ids.NewID()
		fresh, err := tx.StallKeepers().ClaimWage(ctx, k, today, k.DailyWage, txID, now)
		if err != nil {
			return err
		}
		if !fresh {
			continue
		}
		neg, _ := money.FromMinor(k.DailyWage).Neg()
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
			ID: txID, Reason: application.ReasonStallKeeperDayWage, ReferenceType: application.StallKeeperWageReference, ReferenceID: k.ID,
			Entries:   []application.LedgerEntry{{AccountID: acct.ID, Amount: neg}, {AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(k.DailyWage)}},
			CreatedAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
