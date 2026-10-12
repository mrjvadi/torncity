package handlers

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Timed works (docs/adr/0075): a module of a mine, a road between settlements, sections of track and a carrier are ordered, paid in
// materials and builders' wages at once, and done at an instant a scheduled action brings (works.done). The order is the record; the
// action applies it exactly once (the order's status is the fence).

// WorksActionType is the game action that finishes a works order.
const WorksActionType = "works_done"

// worksPay is what an order costs: goods from the stock and a wage from the treasury into the sink under a reason.
type worksPay struct {
	Materials map[string]int64
	Wage      int64
	Reason    application.Reason
	RefType   string
	RefID     string
	// Back is where a refusal sends the player.
	Back string
}

// payWorks takes the materials out of the settlement's stock and the wage out of its treasury. It refuses, before writing anything,
// with what is missing. The stock is locked by the caller (LockOrg).
func (h *VillageHandler) payWorks(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement, pay worksPay, now time.Time) (string, error) {
	buildings, err := tx.SettlementBuildings().List(ctx, s.CityID)
	if err != nil {
		return "", err
	}
	stock, err := h.stockOf(ctx, tx, snap, s.CityID, buildings)
	if err != nil {
		return "", err
	}
	pc, err := h.pathFor(ctx, tx, snap, s, buildings, stock)
	if err != nil {
		return "", err
	}
	if needs := pc.materialNeeds(pay.Materials); len(needs) > 0 {
		return "", needsRefusal(village.VillageMaterials, village.NeedsForWork, named("works", "works"), needs, pay.Back)
	}
	if pay.Wage > 0 {
		bal, err := treasuryBalance(ctx, tx, s.CityID)
		if err != nil {
			return "", err
		}
		if bal < pay.Wage {
			return "", refuseVillage(village.MineNoMoney, pay.Back)
		}
	}
	for _, code := range materialCodes(pay.Materials) {
		if pay.Materials[code] <= 0 {
			continue
		}
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: code, Qty: pay.Materials[code], FromOrg: application.SettlementOrg(s.CityID),
			FromHolding: application.HoldWarehouse, Reason: application.ItemWorksMaterials, ReferenceType: pay.RefType, ReferenceID: pay.RefID, At: now}); err != nil {
			if stderrors.Is(err, application.ErrNotEnoughItems) {
				return "", refuseVillage(village.VillageMaterials, pay.Back)
			}
			return "", err
		}
	}
	if pay.Wage <= 0 {
		return "", nil
	}
	acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, s.CityID)
	if err != nil {
		return "", err
	}
	txID := h.ids.NewID()
	if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: txID, Reason: pay.Reason, CreatedAt: now, ReferenceType: pay.RefType, ReferenceID: pay.RefID,
		Entries: []application.LedgerEntry{
			{AccountID: acct.ID, Amount: money.FromMinor(-pay.Wage)},
			{AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(pay.Wage)},
		}}); err != nil {
		return "", err
	}
	return txID, nil
}

// WorksDone handles works.done from the SCHEDULER: a works order reaching its end. Exactly once (FinishWork is the fence).
func (h *VillageHandler) WorksDone(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presentation.Response, error) {
	in, err := villagePayload(meta, req)
	if err != nil {
		return nil, err
	}
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		w, err := tx.Mines().Work(ctx, in.ID)
		if stderrors.Is(err, application.ErrWorkNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		now := h.now()
		if w.Status != "working" || now.Before(w.DueAt) {
			return nil
		}
		fresh, err := tx.Mines().FinishWork(ctx, w.ID, now)
		if err != nil || !fresh {
			return err
		}
		switch w.Kind {
		case application.WorkMineModule:
			err = h.finishMineModule(ctx, tx, meta, w, now)
		case application.WorkRoadLink:
			if _, err = tx.Haul().OpenRoadLink(ctx, w.RefID, now); err == nil {
				err = appendVillageEvent(ctx, tx, meta, "road_link_opened", w.SettlementID, map[string]any{"settlement_id": w.SettlementID, "link_id": w.RefID})
			}
		case application.WorkRailSections:
			n := int(paramInt(w.Param, "sections"))
			var laid int
			if laid, err = tx.Rail().LayNext(ctx, w.RefID, n, now); err == nil {
				err = appendVillageEvent(ctx, tx, meta, "rail_laid", w.SettlementID, map[string]any{"settlement_id": w.SettlementID, "line_id": w.RefID, "sections": laid})
			}
		case application.WorkCarrier:
			if _, err = tx.Haul().SetCarrierStatus(ctx, w.RefID, application.CarrierBuilding, application.CarrierIdle, "", now); err == nil {
				err = appendVillageEvent(ctx, tx, meta, "carrier_built", w.SettlementID, map[string]any{"settlement_id": w.SettlementID, "carrier_id": w.RefID})
			}
		}
		return err
	})
}

// paramInt reads a number of an order's detail (json numbers come back as float64).
func paramInt(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

func paramString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// postToSink moves money from an account to the sink under a reason.
func postToSink(ctx context.Context, tx application.Tx, txID string, reason application.Reason, from string, amount int64, refType, refID string, now time.Time) error {
	_, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: txID, Reason: reason, CreatedAt: now, ReferenceType: refType, ReferenceID: refID,
		Entries: []application.LedgerEntry{
			{AccountID: from, Amount: money.FromMinor(-amount)},
			{AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(amount)},
		}})
	return err
}
