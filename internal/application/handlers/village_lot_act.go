package handlers

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/domain/lookdesc"
	"github.com/mrjvadi/torncity/internal/domain/lotbuild"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// manageOne serves the detail of one building and the acts on it (ask, then confirm).
func (h *VillageHandler) manageOne(ctx context.Context, tx application.Tx, meta envelope.Metadata, req VillageManageRequest, k lotKit,
	s application.FoundedSettlement, p *application.Player, b application.SettlementBuildingInstance,
	buildings []application.SettlementBuildingInstance, view *village.LotManageView, offer **application.LocalOffer,
) error {
	lc, err := h.loadLot(ctx, tx, k, s, p, b, buildings)
	if err != nil {
		return err
	}
	view.Stage = village.LotDetail
	action := strings.TrimSpace(req.Action)
	if err := h.fillDetail(ctx, tx, lc, view); err != nil {
		return err
	}
	if action == "" {
		return nil
	}
	if !lc.owner.can() {
		return refuseVillage(village.LotNotYours, village.AddrLotManage)
	}
	if lc.f == nil || lc.b.Status != "complete" {
		return refuseVillage(village.LotNotBuilt, village.AddrLotManage)
	}
	view.Action, view.Code, view.N, view.Name = action, strings.TrimSpace(req.Code), req.count(), strings.TrimSpace(req.Name)
	confirmed := req.confirmed()

	switch action {
	case village.LotActionTemplateSave, village.LotActionTemplateDelete:
		return h.templateAct(ctx, tx, meta, req, lc, view, confirmed)
	case village.LotActionRemove:
		return h.removeAct(ctx, tx, meta, req, lc, view, confirmed)
	case village.LotActionKeeperHire, village.LotActionKeeperEnd:
		return h.keeperAct(ctx, tx, meta, lc, view, action, confirmed)
	}

	var o lotbuild.Order
	switch action {
	case village.LotActionAdd:
		if _, ok := lc.spec.Slots[view.Code]; !ok {
			return refuseVillage(village.LotNoModule, village.AddrLotManage)
		}
		o = lotbuild.Order{Adds: map[string]int{view.Code: view.N}}
	case village.LotActionLevel:
		o = lotbuild.Order{LevelTo: lc.f.Level + 1}
	case village.LotActionStorey:
		o = lotbuild.Order{StoreysTo: lc.f.Storeys + 1}
	case village.LotActionFunction:
		if !lc.k.managed(view.Code) || view.Code == lc.f.Function {
			return refuseVillage(village.LotNoFunction, village.AddrLotManage)
		}
		o = lotbuild.Order{ConvertTo: view.Code}
	case village.LotActionTemplateApply:
		t, err := h.templateOf(ctx, tx, p.ID, view.Code)
		if err != nil {
			return err
		}
		var skipped []string
		o, skipped = lotbuild.OrderToReach(lc.k.specs, lc.k.mods, lc.comp, lotbuild.Template{Function: t.Function, Level: t.Level, Storeys: t.Storeys, Modules: t.Modules})
		defer func() {
			if view.Quote != nil {
				for _, code := range skipped {
					view.Quote.Skipped = append(view.Quote.Skipped, lc.k.moduleName(code))
				}
			}
		}()
	default:
		return refuseVillage(village.LotInvalid, village.AddrLotManage)
	}
	if o.Empty() {
		return refuseVillage(village.LotNothing, village.AddrLotManage)
	}
	cost, quote, reason, needs := h.quoteFor(lc, o)
	view.Quote, view.Reason, view.Needs = quote, reason, needs
	view.Stage = village.LotAsk
	if lc.owner.private != nil && o.ConvertTo != "" {
		if offer2, err := application.LocalOfferFor(ctx, tx, s.CityID, p.ID, quote.FeeSUP, 0); err != nil {
			return err
		} else {
			*offer = offer2
		}
	}
	if !confirmed {
		return nil
	}
	if reason != "" {
		return h.lotRefusal(reason, needs)
	}
	fresh, err := h.reserve(ctx, tx, p.ID, meta)
	if err != nil {
		return err
	}
	if !fresh {
		view.Stage = village.LotDone
		return nil
	}
	if err := h.placeOrder(ctx, tx, meta, req, lc, o, cost, view); err != nil {
		return err
	}
	view.Stage = village.LotDone
	return h.refreshDetail(ctx, tx, lc, view)
}

// refreshDetail re-reads the building after an act and refills the tabs, keeping the act's echo.
func (h *VillageHandler) refreshDetail(ctx context.Context, tx application.Tx, lc *lotCtx, view *village.LotManageView) error {
	keep := *view
	b, err := tx.SettlementBuildings().Get(ctx, lc.b.ID)
	if err != nil {
		return err
	}
	lc.b = *b
	if lc.f, err = tx.SettlementBuildings().FunctionOf(ctx, application.BuildingRefSettlement, lc.b.ID); err != nil {
		return err
	}
	lc.comp, lc.spec = compositionOf(lc.f), lc.k.specs[lc.f.Function]
	if lc.work, err = tx.SettlementBuildings().OpenWork(ctx, lc.b.ID); err != nil {
		return err
	}
	if lc.owner.public {
		lc.cash, err = treasuryBalance(ctx, tx, lc.s.CityID)
	} else {
		_, lc.cash, err = playerCash(ctx, tx, lc.p.ID)
		if err == nil {
			lc.stock, err = holdingsOf(ctx, tx, lc.p.ID)
		}
	}
	if err != nil {
		return err
	}
	lc.st.pc.stock = lc.stock
	*view = village.LotManageView{Village: keep.Village, Stage: keep.Stage, Action: keep.Action, Code: keep.Code, N: keep.N, Name: keep.Name,
		Quote: keep.Quote, ShareCode: keep.ShareCode}
	return h.fillDetail(ctx, tx, lc, view)
}

// lotRefusal turns a reason an order cannot be placed into the refusal a client reads.
func (h *VillageHandler) lotRefusal(reason string, needs []village.VillageNeed) error {
	switch reason {
	case village.LotReasonMaterials:
		return needsRefusal(village.VillageMaterials, "lot", named("lot", "lot"), needs, village.AddrLotManage)
	case village.LotReasonCash:
		return refuseVillage(village.CitizenNoCash, village.AddrLotManage)
	case village.LotReasonRequires:
		return needsRefusal(village.VillagePrerequisite, "lot", named("lot", "lot"), needs, village.AddrLotManage)
	case village.LotReasonArea:
		return refuseVillage(village.LotNoArea, village.AddrLotManage)
	case village.LotReasonSlot:
		return refuseVillage(village.LotSlotFull, village.AddrLotManage)
	case village.LotReasonStoreys:
		return refuseVillage(village.LotStoreys, village.AddrLotManage)
	case village.LotReasonBusy:
		return refuseVillage(village.LotBusy, village.AddrLotManage)
	case village.LotReasonBuilt:
		return refuseVillage(village.LotNotBuilt, village.AddrLotManage)
	}
	return refuseVillage(village.LotInvalid, village.AddrLotManage)
}

// placeOrder carries an order out: the materials leave the store, the money and the fee are paid, the order is
// written and its labour posted on the hiring board; with the labour rules off it is built at once.
func (h *VillageHandler) placeOrder(ctx context.Context, tx application.Tx, meta envelope.Metadata, req VillageManageRequest, lc *lotCtx,
	o lotbuild.Order, cost lotbuild.Cost, view *village.LotManageView,
) error {
	snap := lc.k.snap
	now := h.now()
	workID := h.ids.NewID()
	repo := tx.SettlementBuildings()
	if lc.owner.public {
		if err := tx.Items().LockOrg(ctx, application.SettlementOrg(lc.s.CityID)); err != nil {
			return err
		}
	} else if err := tx.Items().LockOwner(ctx, lc.p.ID); err != nil {
		return err
	}

	// the materials: the home store first, then the bags; a settlement's building takes them from its stock
	for _, code := range lotKeys(cost.Materials) {
		need := cost.Materials[code]
		if lc.owner.public {
			if err := tx.Items().Move(ctx, application.ItemMove{Item: code, Qty: need, FromOrg: application.SettlementOrg(lc.s.CityID),
				FromHolding: application.HoldWarehouse, Reason: application.ItemFitoutMaterials, ReferenceType: application.BuildingWorkReference,
				ReferenceID: workID, At: now}); err != nil {
				if stderrors.Is(err, application.ErrNotEnoughItems) {
					return refuseVillage(village.VillageMaterials, village.AddrLotManage)
				}
				return err
			}
			continue
		}
		for _, holding := range []string{application.HoldHome, application.HoldCarried} {
			stacks, _, err := tx.Items().Holdings(ctx, lc.p.ID, holding)
			if err != nil {
				return err
			}
			var have int64
			for _, st := range stacks {
				if st.Item == code {
					have = st.Qty
				}
			}
			take := min(have, need)
			if take <= 0 {
				continue
			}
			if err := tx.Items().Move(ctx, application.ItemMove{Item: code, Qty: take, From: lc.p.ID, FromHolding: holding,
				Reason: application.ItemFitoutMaterials, ReferenceType: application.BuildingWorkReference, ReferenceID: workID, At: now}); err != nil {
				if stderrors.Is(err, application.ErrNotEnoughItems) {
					return refuseVillage(village.VillageMaterials, village.AddrLotManage)
				}
				return err
			}
			need -= take
		}
		if need > 0 {
			return refuseVillage(village.VillageMaterials, village.AddrLotManage)
		}
	}

	// the money: to the builders' trade (the sink); the fee of a change of use to the treasury
	var fee int64
	feeTx := ""
	if o.ConvertTo != "" && lc.owner.private != nil {
		fee = h.useChangeFee(lc.owner.private.AssessedValue)
	}
	if fee > 0 {
		feeTx = h.ids.NewID()
		lr, lerr := application.PayLocal(ctx, tx, h.ids.NewID, application.LocalPayment{
			SettlementID: lc.s.CityID, PlayerID: lc.p.ID, Direction: application.LocalCollect, Flow: application.ReasonUseChangeFee,
			SUP: fee, TxID: feeTx, RefType: application.BuildingWorkReference, RefID: workID, At: now,
			Convert: req.wantsConvert(), MaxConvertSUP: req.maxSUP(),
		})
		if lerr != nil {
			if r, ok := deskRefusal(lerr); ok {
				return r
			}
			return lerr
		}
		if !lr.Paid {
			cash, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, lc.p.ID)
			if err != nil {
				return err
			}
			treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, lc.s.CityID)
			if err != nil {
				return err
			}
			if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: feeTx, Reason: application.ReasonUseChangeFee, CreatedAt: now,
				ReferenceType: application.BuildingWorkReference, ReferenceID: workID,
				Entries: []application.LedgerEntry{{AccountID: cash.ID, Amount: money.FromMinor(-fee)}, {AccountID: treasury.ID, Amount: money.FromMinor(fee)}}}); err != nil {
				if stderrors.Is(err, application.ErrInsufficientFunds) {
					return refuseVillage(village.CitizenNoCash, village.AddrLotManage)
				}
				return err
			}
		}
	}
	if cost.Money > 0 {
		var from application.Account
		var reason application.Reason
		var err error
		if lc.owner.public {
			from, err = tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, lc.s.CityID)
			reason = application.ReasonSettlementConstruction
		} else {
			from, err = tx.Ledger().AccountFor(ctx, application.AccountPlayerCash, lc.p.ID)
			reason = application.ReasonCitizenConstruction
		}
		if err != nil {
			return err
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: h.ids.NewID(), Reason: reason, CreatedAt: now,
			ReferenceType: application.BuildingWorkReference, ReferenceID: workID,
			Entries: []application.LedgerEntry{{AccountID: from.ID, Amount: money.FromMinor(-cost.Money)},
				{AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(cost.Money)}}}); err != nil {
			if stderrors.Is(err, application.ErrInsufficientFunds) {
				return refuseVillage(village.CitizenNoCash, village.AddrLotManage)
			}
			return err
		}
	}

	w := application.BuildingWork{ID: workID, SettlementID: lc.s.CityID, BuildingID: lc.b.ID, Status: application.WorkOpen, Adds: o.Adds,
		LevelTo: o.LevelTo, StoreysTo: o.StoreysTo, ConvertTo: o.ConvertTo, ShiftsTotal: cost.Shifts, WorkRequired: h.workRequired(cost.Shifts),
		CostMoney: cost.Money, CostMaterials: cost.Materials, FeePaid: fee, FeeTx: feeTx, OrderedBy: lc.p.ID, OrderedAt: now}
	if err := repo.InsertWork(ctx, w); err != nil {
		if stderrors.Is(err, application.ErrWorkOpen) {
			return refuseVillage(village.LotBusy, village.AddrLotManage)
		}
		return err
	}
	if err := appendVillageEvent(ctx, tx, meta, "lot_ordered", lc.s.CityID, map[string]any{
		"settlement_id": lc.s.CityID, "building_id": lc.b.ID, "work_id": workID, "function": lc.f.Function, "convert_to": o.ConvertTo,
		"level_to": o.LevelTo, "storeys_to": o.StoreysTo, "adds": o.Adds, "shifts": cost.Shifts, "player_id": lc.p.ID,
	}); err != nil {
		return err
	}
	if !h.labor.Enabled() {
		return h.applyWork(ctx, tx, meta, lc.k, lc.s, w, now)
	}
	// the labour: a job of the hiring board; the owner (or the settlement) is the employer
	employerKind, employerID := application.LaborEmployerPlayer, lc.p.ID
	if lc.owner.public {
		employerKind, employerID = application.LaborEmployerSettlement, lc.s.CityID
	}
	budget := int((int64(cost.Shifts)*(labor.BPS+h.labor.BudgetSlackBPS) + labor.BPS - 1) / labor.BPS)
	if err := tx.SettlementTreasury().PostJob(ctx, application.LaborJob{ID: h.ids.NewID(), SettlementID: lc.s.CityID, BuildingID: lc.b.ID,
		Kind: application.LaborKindFitout, EmployerKind: employerKind, EmployerID: employerID, Wage: lc.wage, ShiftsTotal: budget,
		CreatedBy: lc.p.ID, CreatedAt: now}); err != nil && !stderrors.Is(err, application.ErrJobExists) {
		return err
	}
	_ = snap
	return nil
}

// applyWork builds a finished order into the building, exactly once: the function, the level, the storeys, the
// modules; the catalogue building follows the level; the look follows the contents.
func (h *VillageHandler) applyWork(ctx context.Context, tx application.Tx, meta envelope.Metadata, k lotKit, s application.FoundedSettlement,
	w application.BuildingWork, now time.Time,
) error {
	repo := tx.SettlementBuildings()
	if ok, err := repo.CompleteWork(ctx, w.ID, now); err != nil || !ok {
		return err
	}
	f, err := repo.FunctionOf(ctx, application.BuildingRefSettlement, w.BuildingID)
	if err != nil || f == nil {
		if err == nil {
			err = application.ErrFunctionNotFound
		}
		return err
	}
	c := compositionOf(f)
	spec := k.specs[c.Function]
	typeCode := ""
	if w.ConvertTo != "" {
		// the modules of the old function that the new one cannot hold are taken out; what the build gave back
		// is salvage (the owner's holding slot)
		old := lotbuild.Extras(spec, c)
		if err := h.salvageModules(ctx, tx, k, w.BuildingID, ownerOfWork(w), old, now, w.ID); err != nil {
			return err
		}
		spec = k.specs[w.ConvertTo]
		if err := repo.RecordConversion(ctx, application.FunctionConversion{ID: h.ids.NewID(), SettlementID: s.CityID, BuildingID: w.BuildingID,
			From: c.Function, To: w.ConvertTo, Fee: w.FeePaid, LedgerTransactionID: w.FeeTx, WorkID: w.ID, At: now}); err != nil {
			return err
		}
		c.Function, c.Level, c.Modules = w.ConvertTo, 1, lotbuild.Included(spec, 1)
		if l1, ok := spec.LevelOf(1); ok {
			typeCode = l1.Building
		}
	}
	for n := c.Level + 1; n <= w.LevelTo; n++ {
		l, ok := spec.LevelOf(n)
		if !ok {
			break
		}
		for _, a := range l.Adds {
			c.Modules[a]++
		}
		c.Level = n
		if l.Building != "" {
			typeCode = l.Building
		}
	}
	if w.StoreysTo > c.Storeys {
		c.Storeys = w.StoreysTo
	}
	for code, n := range w.Adds {
		c.Modules[code] += n
	}
	nf := *f
	nf.Function, nf.Level, nf.Storeys, nf.Modules, nf.Status = c.Function, c.Level, c.Storeys, c.Modules, application.FunctionActive
	if err := repo.SetFunction(ctx, nf); err != nil {
		return err
	}
	if typeCode != "" {
		if err := repo.SetTypeCode(ctx, w.BuildingID, typeCode); err != nil {
			return err
		}
	}
	if _, err := h.lookOf(ctx, tx, s, &nf, true, now); err != nil {
		return err
	}
	if err := tx.SettlementTreasury().CloseJobOfBuilding(ctx, w.BuildingID, now); err != nil {
		return err
	}
	return appendVillageEvent(ctx, tx, meta, "lot_built", s.CityID, map[string]any{
		"settlement_id": s.CityID, "building_id": w.BuildingID, "work_id": w.ID, "function": c.Function, "level": c.Level, "storeys": c.Storeys,
		"modules": c.Modules,
	})
}

// ownerOfWork is the player an order's leftovers go to.
func ownerOfWork(w application.BuildingWork) string { return w.OrderedBy }

// salvageModules gives back a share of the materials of modules that are taken out (config
// settlement.building_salvage_bps) into the owner's holding slot: never lost, claimed when there is room.
func (h *VillageHandler) salvageModules(ctx context.Context, tx application.Tx, k lotKit, buildingID, ownerID string, taken map[string]int, now time.Time, ref string) error {
	if h.lot.SalvageBPS <= 0 || ownerID == "" {
		return nil
	}
	back := map[string]int64{}
	for code, n := range taken {
		for item, qty := range k.mods[code].Materials {
			back[item] += qty * int64(n) * h.lot.SalvageBPS / labor.BPS
		}
	}
	if len(back) == 0 {
		return nil
	}
	if err := tx.Items().LockOwner(ctx, ownerID); err != nil {
		return err
	}
	for _, code := range lotKeys(back) {
		if back[code] <= 0 {
			continue
		}
		if err := tx.Items().Move(ctx, application.ItemMove{Item: code, Qty: back[code], To: ownerID, ToHolding: application.HoldClaim,
			Reason: application.ItemSalvage, ReferenceType: application.BuildingWorkReference, ReferenceID: ref, At: now}); err != nil {
			return err
		}
	}
	return nil
}

// removeAct takes modules out (beyond the ones the level includes): at once, with a share of the materials back.
func (h *VillageHandler) removeAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, req VillageManageRequest, lc *lotCtx,
	view *village.LotManageView, confirmed bool,
) error {
	if lc.work != nil {
		return refuseVillage(village.LotBusy, village.AddrLotManage)
	}
	code, n := view.Code, view.N
	extras := lotbuild.Extras(lc.spec, lc.comp)
	if !lc.k.mods[code].Buildable() || extras[code] < 1 {
		return refuseVillage(village.LotNoModule, village.AddrLotManage)
	}
	n = min(n, extras[code])
	view.N = n
	taken := map[string]int{code: n}
	q := &village.LotQuote{Cash: lc.cash, Adds: []village.LotWorkAdd{{Module: lc.k.moduleName(code), Count: n}}}
	back := map[string]int64{}
	for item, qty := range lc.k.mods[code].Materials {
		back[item] = qty * int64(n) * h.lot.SalvageBPS / labor.BPS
	}
	q.Salvage = h.itemLines(lc.k.snap, back)
	view.Quote, view.Stage = q, village.LotAsk
	if !confirmed {
		return nil
	}
	fresh, err := h.reserve(ctx, tx, lc.p.ID, meta)
	if err != nil {
		return err
	}
	if !fresh {
		view.Stage = village.LotDone
		return nil
	}
	now := h.now()
	repo := tx.SettlementBuildings()
	if err := repo.SetModuleCount(ctx, application.BuildingRefSettlement, lc.b.ID, code, lc.f.Modules[code]-n); err != nil {
		return err
	}
	if err := h.salvageModules(ctx, tx, lc.k, lc.b.ID, lc.p.ID, taken, now, lc.b.ID); err != nil {
		return err
	}
	nf := *lc.f
	nf.Modules = map[string]int{}
	for m, c := range lc.f.Modules {
		nf.Modules[m] = c
	}
	if nf.Modules[code]-n > 0 {
		nf.Modules[code] -= n
	} else {
		delete(nf.Modules, code)
	}
	if _, err := h.lookOf(ctx, tx, lc.s, &nf, true, now); err != nil {
		return err
	}
	if err := appendVillageEvent(ctx, tx, meta, "lot_built", lc.s.CityID, map[string]any{
		"settlement_id": lc.s.CityID, "building_id": lc.b.ID, "function": nf.Function, "level": nf.Level, "storeys": nf.Storeys,
		"modules": nf.Modules, "removed": code,
	}); err != nil {
		return err
	}
	view.Stage = village.LotDone
	return h.refreshDetail(ctx, tx, lc, view)
}

// templateOf finds a template by my own id or by a share code.
func (h *VillageHandler) templateOf(ctx context.Context, tx application.Tx, playerID, ref string) (*application.PlanTemplate, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, refuseVillage(village.LotNoTemplate, village.AddrLotManage)
	}
	repo := tx.SettlementBuildings()
	t, err := repo.TemplateByCode(ctx, strings.ToUpper(ref))
	if err != nil && stderrors.Is(err, application.ErrTemplateNotFound) {
		t, err = repo.TemplateByID(ctx, ref)
	}
	if err != nil {
		if stderrors.Is(err, application.ErrTemplateNotFound) {
			return nil, refuseVillage(village.LotNoTemplate, village.AddrLotManage)
		}
		return nil, err
	}
	return t, nil
}

// shareCode is a short code a template is shared by, made from its id.
func shareCode(id string) string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	var out []byte
	for _, r := range strings.ReplaceAll(id, "-", "") {
		out = append(out, alphabet[int(r)%len(alphabet)])
		if len(out) == 8 {
			break
		}
	}
	return string(out)
}

// templateAct saves the composition of the building as a template, or deletes one of mine.
func (h *VillageHandler) templateAct(ctx context.Context, tx application.Tx, meta envelope.Metadata, req VillageManageRequest, lc *lotCtx,
	view *village.LotManageView, confirmed bool,
) error {
	repo := tx.SettlementBuildings()
	if view.Action == village.LotActionTemplateDelete {
		if !confirmed {
			view.Stage = village.LotAsk
			return nil
		}
		fresh, err := h.reserve(ctx, tx, lc.p.ID, meta)
		if err != nil {
			return err
		}
		if fresh {
			if ok, err := repo.DeleteTemplate(ctx, view.Code, lc.p.ID); err != nil {
				return err
			} else if !ok {
				return refuseVillage(village.LotNoTemplate, village.AddrLotManage)
			}
		}
		view.Stage = village.LotDone
		return h.refreshDetail(ctx, tx, lc, view)
	}
	mine, err := repo.Templates(ctx, lc.p.ID)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(view.Name)
	if name == "" && h.msgs != nil {
		// a button cannot carry a name: the layout is called "template N" in the player's language
		name = h.msgs.T(RenderLanguage(meta, lc.p), "lot.template_default", map[string]any{"n": len(mine) + 1})
		view.Name = name
	}
	if n := len([]rune(name)); n < 1 || n > 60 {
		return refuseVillage(village.LotInvalid, village.AddrLotManage)
	}
	if len(mine) >= h.lot.TemplatesMax {
		return refuseVillage(village.LotTemplates, village.AddrLotManage)
	}
	view.Stage = village.LotAsk
	view.Quote = &village.LotQuote{Cash: lc.cash}
	t := lotbuild.TemplateOf(lc.comp)
	for _, code := range lotKeys(t.Modules) {
		view.Quote.Adds = append(view.Quote.Adds, village.LotWorkAdd{Module: lc.k.moduleName(code), Count: t.Modules[code]})
	}
	if !confirmed {
		return nil
	}
	fresh, err := h.reserve(ctx, tx, lc.p.ID, meta)
	if err != nil {
		return err
	}
	if fresh {
		id := h.ids.NewID()
		if err := repo.InsertTemplate(ctx, application.PlanTemplate{ID: id, OwnerID: lc.p.ID, Name: name, Code: shareCode(id), Function: t.Function,
			Level: t.Level, Storeys: t.Storeys, Modules: t.Modules, CreatedAt: h.now()}); err != nil {
			return err
		}
		view.ShareCode = shareCode(id)
	}
	view.Stage = village.LotDone
	return h.refreshDetail(ctx, tx, lc, view)
}

var _ = lookdesc.Version
