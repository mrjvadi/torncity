package handlers

import (
	"context"
	stderrors "errors"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/landroad"
	"github.com/mrjvadi/torncity/internal/domain/landuse"
	"github.com/mrjvadi/torncity/internal/domain/settlementbuilding"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// The land registry (docs/adr/0072): parcels, zones, tile claims and foundations. Every act answers the registry screen. An act that
// costs money answers a quote first and writes nothing; the same act with confirm does it. The permission of the charter is what lets
// a player open a zone (zone.open), close one (zone.close), claim a tile (land.claim) or lay a foundation on the treasury's land
// (public.build); a parcel and a foundation on his own lot are his own business.

// RegistryRules gives the handler the numbers and dates of the registry (settlementcfg.Registry).
func (h *VillageHandler) WithRegistryRules(r application.RegistryRules) *VillageHandler {
	h.registry = r
	return h
}

// VillageRegistryRequest is the payload of the registry acts; the names are those internal/gateway/routing gives the arguments.
type VillageRegistryRequest struct {
	// Parcel and With name parcels (a join keeps the first and takes in the second).
	Parcel string `json:"parcel,omitempty"`
	With   string `json:"with,omitempty"`
	// From and To are the two corners of a rectangle of lots, lot tokens ("x-y", m before a negative number).
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Lot is one lot token (a claim names the tile of the lot, a foundation the lot).
	Lot  string `json:"lot,omitempty"`
	Zone string `json:"zone,omitempty"`
	Kind string `json:"kind,omitempty"`
	Name string `json:"name,omitempty"`

	Confirm string `json:"confirm,omitempty"`
}

func (r VillageRegistryRequest) confirmed() bool {
	return strings.EqualFold(strings.TrimSpace(r.Confirm), village.ResidenceConfirm)
}

// LandRegistry handles settlement.land.registry: the parcels, zones and claims of the settlement.
func (h *VillageHandler) LandRegistry(ctx context.Context, meta envelope.Metadata) (*presentation.Response, error) {
	return h.registryRun(ctx, meta, false, func(ctx context.Context, tx application.Tx, sc *citizenScope, now time.Time, view *village.LandRegistryView) error {
		return nil
	})
}

// registryAct is one act of the registry inside its transaction: it may fill the Notice or the Quote of the view, and returns a refusal.
type registryAct func(ctx context.Context, tx application.Tx, sc *citizenScope, now time.Time, view *village.LandRegistryView) error

// registryRun is the shell of every registry act: the scope (the settlement, the viewer, the lots), the land lock for a writer, the act,
// and the registry view as the answer.
func (h *VillageHandler) registryRun(ctx context.Context, meta envelope.Metadata, lock bool, act registryAct) (*presentation.Response, error) {
	lang := meta.Language
	var view village.LandRegistryView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		sc, err := h.citizenScopeWith(ctx, tx, meta, &lang, true, lock)
		if err != nil {
			return err
		}
		now := h.now()
		if err := h.settleClaims(ctx, tx, sc.s, now); err != nil {
			return err
		}
		view = village.LandRegistryView{Village: sc.s.Name}
		if err := act(ctx, tx, sc, now, &view); err != nil {
			return err
		}
		return h.fillRegistry(ctx, tx, sc, now, &view)
	})
	if resp, ferr := h.villageFinish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return village.LandRegistry(h.screen(meta, lang), view), nil
}

// fillRegistry reads the registry into the view.
func (h *VillageHandler) fillRegistry(ctx context.Context, tx application.Tx, sc *citizenScope, now time.Time, view *village.LandRegistryView) error {
	reg := tx.Registry()
	parcels, err := reg.Parcels(ctx, sc.s.CityID)
	if err != nil {
		return err
	}
	zones, err := reg.Zones(ctx, sc.s.CityID)
	if err != nil {
		return err
	}
	claims, err := reg.Claims(ctx, sc.s.CityID)
	if err != nil {
		return err
	}
	found, err := reg.Foundations(ctx, sc.s.CityID)
	if err != nil {
		return err
	}
	names := map[string]presentation.Named{}
	nameOf := func(id string) (presentation.Named, error) {
		if n, ok := names[id]; ok {
			return n, nil
		}
		pl, err := tx.Players().GetByID(ctx, id)
		if err != nil {
			if stderrors.Is(err, application.ErrPlayerNotFound) {
				return presentation.Named{}, nil
			}
			return presentation.Named{}, err
		}
		names[id] = presentation.Named{Code: pl.PublicCode, Name: shownName(pl)}
		return names[id], nil
	}
	zoneName := map[string]string{}
	for _, z := range zones {
		zoneName[z.ID] = z.Name
	}
	byCell := map[landuse.Cell]string{}
	for _, p := range parcels {
		for _, c := range p.Cells {
			byCell[c] = p.ID
		}
	}
	for _, p := range parcels {
		line := village.ParcelLine{ID: p.ID, Use: p.Use, Treasury: p.OwnerKind == application.ParcelOwnerTreasury, Mine: p.OwnerID == sc.p.ID && p.OwnerID != ""}
		if p.OwnerID != "" {
			if line.Owner, err = nameOf(p.OwnerID); err != nil {
				return err
			}
		}
		if n, ok := zoneName[p.ZoneID]; ok {
			line.Zone = &presentation.Named{Code: p.ZoneID, Name: n}
		}
		landuse.Sort(p.Cells)
		for _, c := range p.Cells {
			line.Cells = append(line.Cells, village.RegCell{X: c.X, Y: c.Y})
		}
		if line.Mine {
			line.CanSplit = len(p.Cells) > 1
			seen := map[string]bool{}
			for _, c := range p.Cells {
				for _, d := range [4]landuse.Cell{{X: c.X + 1, Y: c.Y}, {X: c.X - 1, Y: c.Y}, {X: c.X, Y: c.Y + 1}, {X: c.X, Y: c.Y - 1}} {
					if id, ok := byCell[d]; ok && id != p.ID && !seen[id] {
						for _, q := range parcels {
							if q.ID == id && q.OwnerID == p.OwnerID && len(p.Cells)+len(q.Cells) <= h.registry.ParcelMaxCells {
								seen[id] = true
								line.CanJoin = append(line.CanJoin, id)
							}
						}
					}
				}
			}
			sort.Strings(line.CanJoin)
		}
		view.Parcels = append(view.Parcels, line)
	}
	canClose := h.mayVillage2(ctx, tx, sc, charter.ZoneClose)
	for _, z := range zones {
		by, err := nameOf(z.OpenedBy)
		if err != nil {
			return err
		}
		view.Zones = append(view.Zones, village.ZoneLine{ID: z.ID, Name: z.Name, Kind: z.Kind, Cells: len(z.Cells),
			Effective: !now.Before(z.EffectiveAt), EffectiveAt: z.EffectiveAt, OpenedBy: by, CanClose: canClose && h.registry.ZoningOn(now)})
	}
	for _, c := range claims {
		view.Claims = append(view.Claims, village.ClaimLine{ID: c.ID, Face: c.Tile.Face, GX: c.Tile.GX, GY: c.Tile.GY, ClaimedAt: c.ClaimedAt, PaidThrough: c.PaidThrough})
	}
	view.Foundations = len(found)
	view.ZoningOn, view.SlopeOn = h.registry.ZoningOn(now), h.registry.SlopeOn(now)
	if !view.ZoningOn && !h.registry.ZoningRuleAt.IsZero() {
		at := h.registry.ZoningStartsAt()
		view.ZoningStartsAt = &at
	}
	if !view.SlopeOn && !h.registry.SlopeRuleAt.IsZero() {
		at := h.registry.SlopeStartsAt()
		view.SlopeStartsAt = &at
	}
	view.CanZone = h.mayVillage2(ctx, tx, sc, charter.ZoneOpen) && view.ZoningOn
	view.CanCloseZone = canClose && view.ZoningOn
	view.CanClaim = h.mayVillage2(ctx, tx, sc, charter.LandClaim)
	view.CanLayFoundation = true
	view.ZoneKinds = content.ZoneKinds()
	r := h.registry
	view.ParcelMaxCells, view.ZoneMaxCells, view.ClaimMaxTiles = r.ParcelMaxCells, r.ZoneMaxCells, r.ClaimMaxTiles
	view.ParcelChangeFee, view.ZoneOpenFee, view.ClaimCost, view.ClaimUpkeepPerDay = r.ParcelChangeFee, r.ZoneOpenFee, r.ClaimCost, r.ClaimUpkeepPerDay
	view.FoundationStone, view.FoundationWage = r.FoundationStone, r.FoundationWage
	view.SlopeFlatM, view.SlopeGentleM, view.SlopeUnusableM = r.Slope.Flat, r.Slope.Gentle, r.Slope.Unusable
	return nil
}

func (h *VillageHandler) mayVillage2(ctx context.Context, tx application.Tx, sc *citizenScope, perm charter.Permission) bool {
	return hasPermission(ctx, tx, sc.s, sc.p.ID, perm)
}

// ---- parcels --------------------------------------------------------------------------------------------------------------

// ParcelJoin handles settlement.parcel.join: an owner joins two parcels of his own that touch into one, for the small fee.
func (h *VillageHandler) ParcelJoin(ctx context.Context, meta envelope.Metadata, req VillageRegistryRequest) (*presentation.Response, error) {
	return h.registryRun(ctx, meta, true, func(ctx context.Context, tx application.Tx, sc *citizenScope, now time.Time, view *village.LandRegistryView) error {
		keep, err := tx.Registry().ParcelByID(ctx, sc.s.CityID, strings.TrimSpace(req.Parcel))
		if err != nil {
			return refuseVillage(village.LandNotYours, village.AddrLandRegistry)
		}
		take, err := tx.Registry().ParcelByID(ctx, sc.s.CityID, strings.TrimSpace(req.With))
		if err != nil || keep.ID == take.ID || keep.OwnerID != sc.p.ID || take.OwnerID != sc.p.ID || keep.OwnerKind != application.ParcelOwnerPlayer {
			return refuseVillage(village.LandNotYours, village.AddrLandRegistry)
		}
		if !landuse.Touches(keep.Cells, take.Cells) {
			return refuseVillage(village.LandNotAdjacent, village.AddrLandRegistry)
		}
		if len(keep.Cells)+len(take.Cells) > h.registry.ParcelMaxCells {
			return refuseVillage(village.LandTooBig, village.AddrLandRegistry)
		}
		fee := h.registry.ParcelChangeFee
		if !req.confirmed() {
			return h.quoteSUP(ctx, tx, sc, view, village.AddrParcelJoin, []string{keep.ID, take.ID}, len(keep.Cells)+len(take.Cells), fee)
		}
		if fresh, err := h.reserve(ctx, tx, sc.p.ID, meta); err != nil || !fresh {
			return err
		}
		if err := h.payTreasury(ctx, tx, sc, application.ReasonParcelChangeFee, fee, keep.ID, now); err != nil {
			return err
		}
		if err := tx.Registry().MoveCells(ctx, sc.s.CityID, take.ID, keep.ID, now); err != nil {
			return err
		}
		view.Notice = village.LandNoticeJoined
		return h.registryDone(ctx, tx, meta, sc, "parcel_joined", map[string]any{"kept": keep.ID, "taken": take.ID, "cells": len(keep.Cells) + len(take.Cells)}, now)
	})
}

// ParcelSplit handles settlement.parcel.split: an owner cuts a rectangle of lots out of a parcel of his own into a parcel of its own.
func (h *VillageHandler) ParcelSplit(ctx context.Context, meta envelope.Metadata, req VillageRegistryRequest) (*presentation.Response, error) {
	return h.registryRun(ctx, meta, true, func(ctx context.Context, tx application.Tx, sc *citizenScope, now time.Time, view *village.LandRegistryView) error {
		par, err := tx.Registry().ParcelByID(ctx, sc.s.CityID, strings.TrimSpace(req.Parcel))
		if err != nil || par.OwnerID != sc.p.ID || par.OwnerKind != application.ParcelOwnerPlayer {
			return refuseVillage(village.LandNotYours, village.AddrLandRegistry)
		}
		cut, ok := rectCells(req.From, req.To, len(par.Cells))
		if !ok {
			return refuseVillage(village.LandBadCells, village.AddrLandRegistry)
		}
		in := map[landuse.Cell]bool{}
		for _, c := range par.Cells {
			in[c] = true
		}
		out := map[landuse.Cell]bool{}
		for _, c := range cut {
			if !in[c] {
				return refuseVillage(village.LandNotOneParcel, village.AddrLandRegistry)
			}
			out[c] = true
		}
		if len(cut) == len(par.Cells) {
			return refuseVillage(village.LandWholeParcel, village.AddrLandRegistry)
		}
		var rest []landuse.Cell
		for _, c := range par.Cells {
			if !out[c] {
				rest = append(rest, c)
			}
		}
		if !landuse.Connected(rest) || !landuse.Connected(cut) {
			return refuseVillage(village.LandNotConnected, village.AddrLandRegistry)
		}
		fee := h.registry.ParcelChangeFee
		if !req.confirmed() {
			return h.quoteSUP(ctx, tx, sc, view, village.AddrParcelSplit, []string{par.ID, strings.TrimSpace(req.From), strings.TrimSpace(req.To)}, len(cut), fee)
		}
		if fresh, err := h.reserve(ctx, tx, sc.p.ID, meta); err != nil || !fresh {
			return err
		}
		if err := h.payTreasury(ctx, tx, sc, application.ReasonParcelChangeFee, fee, par.ID, now); err != nil {
			return err
		}
		fresh := application.Parcel{ID: h.ids.NewID(), SettlementID: sc.s.CityID, OwnerKind: par.OwnerKind, OwnerID: par.OwnerID, Use: par.Use, ZoneID: par.ZoneID, Cells: cut, CreatedAt: now}
		if err := tx.Registry().SplitOff(ctx, par.ID, fresh, now); err != nil {
			return err
		}
		view.Notice = village.LandNoticeSplit
		return h.registryDone(ctx, tx, meta, sc, "parcel_split", map[string]any{"parcel": par.ID, "new": fresh.ID, "cells": len(cut)}, now)
	})
}

// quoteSUP answers the price of an act paid in the viewer's own cash.
func (h *VillageHandler) quoteSUP(ctx context.Context, tx application.Tx, sc *citizenScope, view *village.LandRegistryView, act string, args []string, cells int, fee int64) error {
	_, cash, err := playerCash(ctx, tx, sc.p.ID)
	if err != nil {
		return err
	}
	view.Quote = &village.LandQuote{Act: act, Args: args, Cells: cells, Money: fee, Cash: cash}
	if cash < fee {
		return refuseVillage(village.LandNoMoney, village.AddrLandRegistry)
	}
	return nil
}

// payTreasury moves a fee from the viewer's cash into the treasury.
func (h *VillageHandler) payTreasury(ctx context.Context, tx application.Tx, sc *citizenScope, reason application.Reason, fee int64, ref string, now time.Time) error {
	if fee <= 0 {
		return nil
	}
	cashAcct, _, err := playerCash(ctx, tx, sc.p.ID)
	if err != nil {
		return err
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, sc.s.CityID)
	if err != nil {
		return err
	}
	_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{ID: h.ids.NewID(), Reason: reason, CreatedAt: now,
		ReferenceType: "land_registry", ReferenceID: ref,
		Entries: []application.LedgerEntry{{AccountID: cashAcct.ID, Amount: money.FromMinor(-fee)}, {AccountID: treasury.ID, Amount: money.FromMinor(fee)}}})
	if stderrors.Is(err, application.ErrInsufficientFunds) {
		return refuseVillage(village.LandNoMoney, village.AddrLandRegistry)
	}
	return err
}

// registryDone writes the audit row and the event of an act.
func (h *VillageHandler) registryDone(ctx context.Context, tx application.Tx, meta envelope.Metadata, sc *citizenScope, kind string, detail map[string]any, now time.Time) error {
	if err := tx.Registry().Audit(ctx, h.ids.NewID(), sc.s.CityID, kind, sc.p.ID, detail, now); err != nil {
		return err
	}
	payload := map[string]any{"settlement_id": sc.s.CityID, "kind": kind}
	return appendVillageEvent(ctx, tx, meta, "land_changed", sc.s.CityID, payload)
}

// rectCells is the lots of the rectangle between two lot tokens, corners included; false when a token is bad or the rectangle
// is larger than max cells.
func rectCells(from, to string, max int) ([]landuse.Cell, bool) {
	x0, y0, _, ok0 := village.ParseLotTokenAny(strings.TrimSpace(from))
	x1, y1, _, ok1 := village.ParseLotTokenAny(strings.TrimSpace(to))
	if !ok0 || !ok1 {
		return nil, false
	}
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	if (x1-x0+1)*(y1-y0+1) > max {
		return nil, false
	}
	var out []landuse.Cell
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			out = append(out, landuse.Cell{X: x, Y: y})
		}
	}
	return out, true
}

// ---- zones ----------------------------------------------------------------------------------------------------------------

// ZoneOpen handles settlement.zone.open: a holder of zone.open opens a zone on a rectangle of lots, with a name of the players' own and
// a kind. It costs a fee for each lot (into the sink) and its rules bite after the notice. Only the treasury's land and the holder's
// own lots can be zoned; a lot somebody else owns, and a lot already in a zone, cannot.
func (h *VillageHandler) ZoneOpen(ctx context.Context, meta envelope.Metadata, req VillageRegistryRequest) (*presentation.Response, error) {
	return h.registryRun(ctx, meta, true, func(ctx context.Context, tx application.Tx, sc *citizenScope, now time.Time, view *village.LandRegistryView) error {
		if _, err := h.requireVillage(ctx, tx, sc.s, sc.p.ID, charter.ZoneOpen); err != nil {
			return err
		}
		if !h.registry.ZoningOn(now) {
			return refuseVillage(village.LandZoningOff, village.AddrLandRegistry)
		}
		name := strings.TrimSpace(req.Name)
		if n := utf8.RuneCountInString(name); n < 1 || n > 40 {
			return refuseVillage(village.LandZoneName, village.AddrLandRegistry)
		}
		kind := strings.TrimSpace(req.Kind)
		if !containsStr(content.ZoneKinds(), kind) {
			return refuseVillage(village.LandZoneKind, village.AddrLandRegistry)
		}
		cells, ok := rectCells(req.From, req.To, h.registry.ZoneMaxCells)
		if !ok {
			return refuseVillage(village.LandZoneSize, village.AddrLandRegistry)
		}
		w, err := h.world(ctx)
		if err != nil {
			return err
		}
		pic, err := h.picture(ctx, tx, w, sc.s, sc.lots)
		if err != nil {
			return err
		}
		claimed, err := h.claimedTiles(ctx, tx, sc.s)
		if err != nil {
			return err
		}
		ground := landroad.NewWorldGround(w, pic.frame, nil)
		for _, c := range cells {
			l := landroad.Lot{X: c.X, Y: c.Y}
			_, open := pic.open[l]
			_, road := pic.cells[l]
			f, gx, gy := ground.Tile(l).Parts()
			if !pic.inGrid(c.X, c.Y) && !open && !road && !claimed[application.TileKey{Face: int(f), GX: gx, GY: gy}] {
				return refuseVillage(village.LandOffLand, village.AddrLandRegistry)
			}
			if lot, owned := sc.lotAt(c.X, c.Y); owned && lot.OwnerID != sc.p.ID {
				return refuseVillage(village.LandCellOwned, village.AddrLandRegistry)
			}
		}
		taken, err := tx.Registry().ZoneCells(ctx, sc.s.CityID, cells)
		if err != nil {
			return err
		}
		if len(taken) > 0 {
			return refuseVillage(village.LandCellZoned, village.AddrLandRegistry)
		}
		fee := h.registry.ZoneOpenFee * int64(len(cells))
		args := []string{strings.TrimSpace(req.From), strings.TrimSpace(req.To), kind, name}
		if !req.confirmed() {
			bal, err := treasuryBalance(ctx, tx, sc.s.CityID)
			if err != nil {
				return err
			}
			view.Quote = &village.LandQuote{Act: village.AddrZoneOpen, Args: args, Cells: len(cells), Money: fee, Cash: bal}
			if bal < fee {
				return refuseVillage(village.LandNoMoney, village.AddrLandRegistry)
			}
			return nil
		}
		if fresh, err := h.reserve(ctx, tx, sc.p.ID, meta); err != nil || !fresh {
			return err
		}
		if _, err := spendVillage(ctx, tx, sc.s.CityID, application.ReasonZoneOpenFee, fee, now); err != nil {
			return err
		}
		z := application.Zone{ID: h.ids.NewID(), SettlementID: sc.s.CityID, Name: name, Kind: kind, OpenedBy: sc.p.ID, OpenedAt: now,
			EffectiveAt: now.Add(h.registry.ZoneNotice), Cells: cells}
		if err := tx.Registry().OpenZone(ctx, z); err != nil {
			if stderrors.Is(err, application.ErrZoneCellTaken) {
				return refuseVillage(village.LandCellZoned, village.AddrLandRegistry)
			}
			return err
		}
		if kind == application.ZoneKindCommons {
			// the commons are the treasury's: one treasury parcel holds the cells that nobody owns
			var free []landuse.Cell
			for _, c := range cells {
				if _, owned := sc.lotAt(c.X, c.Y); !owned {
					free = append(free, c)
				}
			}
			if len(free) > 0 {
				if err := tx.Registry().InsertParcel(ctx, application.Parcel{ID: h.ids.NewID(), SettlementID: sc.s.CityID, OwnerKind: application.ParcelOwnerTreasury,
					Use: application.ZoneKindCommons, ZoneID: z.ID, Cells: free, CreatedAt: now}); err != nil {
					return err
				}
			}
		}
		view.Notice = village.LandNoticeZoneOpen
		return h.registryDone(ctx, tx, meta, sc, "zone_opened", map[string]any{"zone": z.ID, "name": name, "kind": kind, "cells": len(cells), "fee": fee}, now)
	})
}

// ZoneClose handles settlement.zone.close: a holder of zone.close closes a zone. A building that stands keeps standing: closing frees
// the lots, it never removes anything.
func (h *VillageHandler) ZoneClose(ctx context.Context, meta envelope.Metadata, req VillageRegistryRequest) (*presentation.Response, error) {
	return h.registryRun(ctx, meta, true, func(ctx context.Context, tx application.Tx, sc *citizenScope, now time.Time, view *village.LandRegistryView) error {
		if _, err := h.requireVillage(ctx, tx, sc.s, sc.p.ID, charter.ZoneClose); err != nil {
			return err
		}
		if !h.registry.ZoningOn(now) {
			return refuseVillage(village.LandZoningOff, village.AddrLandRegistry)
		}
		id := strings.TrimSpace(req.Zone)
		zones, err := tx.Registry().Zones(ctx, sc.s.CityID)
		if err != nil {
			return err
		}
		var z *application.Zone
		for i := range zones {
			if zones[i].ID == id {
				z = &zones[i]
			}
		}
		if z == nil {
			return refuseVillage(village.LandZoneNone, village.AddrLandRegistry)
		}
		if fresh, err := h.reserve(ctx, tx, sc.p.ID, meta); err != nil || !fresh {
			return err
		}
		closed, err := tx.Registry().CloseZone(ctx, sc.s.CityID, z.ID, sc.p.ID, now)
		if err != nil {
			return err
		}
		if !closed {
			return refuseVillage(village.LandZoneNone, village.AddrLandRegistry)
		}
		if z.Kind == application.ZoneKindCommons {
			if err := tx.Registry().DropParcelsOfZone(ctx, z.ID); err != nil {
				return err
			}
		}
		view.Notice = village.LandNoticeZoneShut
		return h.registryDone(ctx, tx, meta, sc, "zone_closed", map[string]any{"zone": z.ID, "name": z.Name, "cells": len(z.Cells)}, now)
	})
}

// zoneVerdict refuses a building whose footprint lies in a zone that does not take it. A zone binds from its notice's end, never
// reaches back to a building that stands, and an open or commons-free cell binds nothing. The commons take no building at all.
func (h *VillageHandler) zoneVerdict(ctx context.Context, tx application.Tx, snap *content.Snapshot, settlementID string, def settlementbuilding.Def, x, y int, now time.Time) error {
	if !h.registry.ZoningOn(now) || def.Code == "road" {
		return nil
	}
	cells := make([]landuse.Cell, 0, def.FootprintW*def.FootprintH)
	for _, q := range footprintOf(def, x, y) {
		cells = append(cells, landuse.Cell{X: q[0], Y: q[1]})
	}
	zoned, err := tx.Registry().ZoneCells(ctx, settlementID, cells)
	if err != nil || len(zoned) == 0 {
		return err
	}
	var fnZones []string
	if code, ok := snap.FunctionReplacing(def.Code); ok {
		if f, ok := snap.BuildingFunction(code); ok {
			fnZones = f.Zones
		}
	} else if f, ok := snap.BuildingFunction(def.Code); ok {
		fnZones = f.Zones
	}
	for _, z := range zoned {
		if now.Before(z.EffectiveAt) {
			continue
		}
		if z.Kind == application.ZoneKindCommons {
			return refuseVillage(village.LandZoneForbids, village.AddrLandRegistry)
		}
		if len(fnZones) > 0 && !landuse.ZoneAllows(z.Kind, fnZones) {
			return refuseVillage(village.LandZoneForbids, village.AddrLandRegistry)
		}
	}
	return nil
}

// ---- claims ---------------------------------------------------------------------------------------------------------------

// claimedTiles is the set of tiles this settlement claims.
func (h *VillageHandler) claimedTiles(ctx context.Context, tx application.Tx, s application.FoundedSettlement) (map[application.TileKey]bool, error) {
	claims, err := tx.Registry().Claims(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	out := make(map[application.TileKey]bool, len(claims))
	for _, c := range claims {
		out[c.Tile] = true
	}
	return out, nil
}

// settleClaims charges the upkeep of the claims that fell due, from the treasury, whole days at a time; a claim the treasury cannot pay
// lapses (its tile is open to others again; what stands on it stays). The paid-through instant is the fence between replicas.
func (h *VillageHandler) settleClaims(ctx context.Context, tx application.Tx, s application.FoundedSettlement, now time.Time) error {
	if h.registry.ClaimUpkeepPerDay <= 0 {
		return nil
	}
	due, err := tx.Registry().DueClaims(ctx, s.CityID, now)
	if err != nil || len(due) == 0 {
		return err
	}
	bal, err := treasuryBalance(ctx, tx, s.CityID)
	if err != nil {
		return err
	}
	for _, c := range due {
		days := int64(math.Ceil(float64(now.Sub(c.PaidThrough)) / float64(24*time.Hour)))
		cost := days * h.registry.ClaimUpkeepPerDay
		if bal < cost {
			if _, err := tx.Registry().DropClaim(ctx, c.ID); err != nil {
				return err
			}
			if err := tx.Registry().Audit(ctx, h.ids.NewID(), s.CityID, "claim_lapsed", c.ClaimedBy, map[string]any{"claim": c.ID, "face": c.Tile.Face, "gx": c.Tile.GX, "gy": c.Tile.GY}, now); err != nil {
				return err
			}
			continue
		}
		moved, err := tx.Registry().AdvanceClaim(ctx, c.ID, c.PaidThrough, c.PaidThrough.Add(time.Duration(days)*24*time.Hour))
		if err != nil {
			return err
		}
		if !moved {
			continue // another replica paid it
		}
		if _, err := spendVillage(ctx, tx, s.CityID, application.ReasonClaimUpkeep, cost, now); err != nil {
			return err
		}
		bal -= cost
	}
	return nil
}

// Claim handles settlement.claim: a holder of land.claim claims the tile of a lot next to land the settlement holds, for a price from the
// treasury into the sink and an upkeep a day. A tile has one owner in the world; another settlement's tile, road or ring is refused.
func (h *VillageHandler) Claim(ctx context.Context, meta envelope.Metadata, req VillageRegistryRequest) (*presentation.Response, error) {
	return h.registryRun(ctx, meta, true, func(ctx context.Context, tx application.Tx, sc *citizenScope, now time.Time, view *village.LandRegistryView) error {
		if _, err := h.requireVillage(ctx, tx, sc.s, sc.p.ID, charter.LandClaim); err != nil {
			return err
		}
		x, y, _, ok := village.ParseLotTokenAny(req.Lot)
		if !ok {
			return refuseVillage(village.LandBadCells, village.AddrLandRegistry)
		}
		w, err := h.world(ctx)
		if err != nil {
			return err
		}
		pic, err := h.picture(ctx, tx, w, sc.s, sc.lots)
		if err != nil {
			return err
		}
		ground := landroad.NewWorldGround(w, pic.frame, nil)
		lot := landroad.Lot{X: x, Y: y}
		target := ground.Tile(lot)
		face, gx, gy := target.Parts()
		key := application.TileKey{Face: int(face), GX: gx, GY: gy}
		mine, err := h.claimedTiles(ctx, tx, sc.s)
		if err != nil {
			return err
		}
		if mine[key] {
			return refuseVillage(village.LandTileClaimed, village.AddrLandRegistry)
		}
		if len(mine) >= h.registry.ClaimMaxTiles {
			return refuseVillage(village.LandClaimLimit, village.AddrLandRegistry)
		}
		// the tiles the settlement holds: its own centre, the tiles of its road cells and its claims; the new tile must touch one
		held := map[worldgen.TileID]bool{}
		if sc.s.WorldCellID >= 0 && int(sc.s.WorldCellID) < len(w.Cells) {
			pt := w.Cells[sc.s.WorldCellID].Point
			held[w.TileOfLatLon(pt.LatDeg, pt.LonDeg)] = true
		}
		for l := range pic.cells {
			held[ground.Tile(l)] = true
		}
		for k := range mine {
			held[worldgen.MakeTileID(int8(k.Face), k.GX, k.GY)] = true
		}
		touches := held[target]
		for _, n := range w.Neighbours8(target) {
			if held[n] {
				touches = true
			}
		}
		if !touches {
			return refuseVillage(village.LandTileApart, village.AddrLandRegistry)
		}
		foreign, err := h.foreignTiles(ctx, tx, w, sc.s, ground, lot, lot, 0)
		if err != nil {
			return err
		}
		if foreign[target] {
			return refuseVillage(village.LandTileForeign, village.AddrLandRegistry)
		}
		others, err := tx.Registry().ClaimedTilesOfOthers(ctx, sc.s.CityID, int(face), gx, gx, gy, gy)
		if err != nil {
			return err
		}
		if len(others) > 0 {
			return refuseVillage(village.LandTileClaimed, village.AddrLandRegistry)
		}
		cost := h.registry.ClaimCost
		bal, err := treasuryBalance(ctx, tx, sc.s.CityID)
		if err != nil {
			return err
		}
		if !req.confirmed() {
			view.Quote = &village.LandQuote{Act: village.AddrClaim, Args: []string{strings.TrimSpace(req.Lot)}, Cells: 1, Money: cost, Cash: bal, Lot: &village.RegCell{X: x, Y: y}}
			if bal < cost {
				return refuseVillage(village.LandNoMoney, village.AddrLandRegistry)
			}
			return nil
		}
		if fresh, err := h.reserve(ctx, tx, sc.p.ID, meta); err != nil || !fresh {
			return err
		}
		txID, err := spendVillage(ctx, tx, sc.s.CityID, application.ReasonSettlementClaim, cost, now)
		if err != nil {
			return err
		}
		if err := tx.Registry().InsertClaim(ctx, application.TileClaim{ID: h.ids.NewID(), SettlementID: sc.s.CityID, Tile: key, ClaimedBy: sc.p.ID,
			ClaimedAt: now, PaidThrough: now.Add(24 * time.Hour), LedgerTransactionID: txID}); err != nil {
			if stderrors.Is(err, application.ErrTileClaimed) {
				return refuseVillage(village.LandTileClaimed, village.AddrLandRegistry)
			}
			return err
		}
		view.Notice = village.LandNoticeClaimed
		return h.registryDone(ctx, tx, meta, sc, "tile_claimed", map[string]any{"face": key.Face, "gx": key.GX, "gy": key.GY, "cost": cost}, now)
	})
}

// ---- foundations ----------------------------------------------------------------------------------------------------------

// foundationQuantities is the stone and wage a foundation on a lot of this band takes: the band counts as steps from flat.
func (h *VillageHandler) foundationQuantities(band int) (stone, wage int64) {
	return h.registry.FoundationStone * int64(band), h.registry.FoundationWage * int64(band)
}

// FoundationLay handles settlement.foundation.lay: the owner of a lot, or the holder of public.build for a lot of the treasury, lays a
// foundation on a sloped lot, so a building that does not take the slope unaided can stand on it. It takes stone (the owner's own, or
// bought at the reference price) and the mason's wage; the lot must be steeper than flat and not too steep for anything.
func (h *VillageHandler) FoundationLay(ctx context.Context, meta envelope.Metadata, req VillageRegistryRequest) (*presentation.Response, error) {
	return h.registryRun(ctx, meta, true, func(ctx context.Context, tx application.Tx, sc *citizenScope, now time.Time, view *village.LandRegistryView) error {
		snap := h.content.Current()
		if !h.registry.SlopeOn(now) {
			return refuseVillage(village.LandSlopeOff, village.AddrLandRegistry)
		}
		x, y, _, ok := village.ParseLotTokenAny(req.Lot)
		if !ok {
			return refuseVillage(village.LandBadCells, village.AddrLandRegistry)
		}
		owner := ""
		if l, owned := sc.lotAt(x, y); owned {
			owner = l.OwnerID
		}
		if owner != "" {
			if owner != sc.p.ID {
				return refuseVillage(village.LandNotYours, village.AddrLandRegistry)
			}
		} else if _, err := h.requireVillage(ctx, tx, sc.s, sc.p.ID, charter.PublicBuild); err != nil {
			return err
		}
		if !h.knows(ctx, tx, sc.s, "masonry") {
			return refuseVillage(village.LandNoMasonry, village.AddrLandRegistry)
		}
		w, err := h.world(ctx)
		if err != nil {
			return err
		}
		pic, err := h.picture(ctx, tx, w, sc.s, sc.lots)
		if err != nil {
			return err
		}
		lotState := pic.lotAt(x, y)
		if !lotState.Buildable || lotState.Occupied {
			return refuseVillage(village.VillageUnbuildable, village.AddrLandRegistry)
		}
		band := h.registry.Slope.Of(lotState.SlopeM)
		if band >= landuse.BandUnusable {
			return refuseVillage(village.LandSteepest, village.AddrLandRegistry)
		}
		if band <= landuse.BandFlat {
			return refuseVillage(village.LandNoFoundation, village.AddrLandRegistry)
		}
		if lotState.Foundation {
			return refuseVillage(village.LandFoundationDone, village.AddrLandRegistry)
		}
		stone, wage := h.foundationQuantities(band)
		args := []string{strings.TrimSpace(req.Lot)}
		def := settlementbuilding.Def{CostMaterials: map[string]int64{"stone": stone}}
		if owner != "" {
			bill, err := h.planMaterials(ctx, tx, snap, sc.p.ID, def)
			if err != nil {
				return err
			}
			_, cash, err := playerCash(ctx, tx, sc.p.ID)
			if err != nil {
				return err
			}
			var have, buy int64
			if len(bill.lines) == 1 {
				have, buy = bill.lines[0].have, bill.lines[0].buy
			}
			total := wage + bill.boughtCost
			if !req.confirmed() {
				view.Quote = &village.LandQuote{Act: village.AddrFoundationLay, Args: args, Cells: 1, Lot: &village.RegCell{X: x, Y: y}, Money: total,
					StoneNeed: stone, StoneHave: have, StoneBuy: buy, Cash: cash}
				if cash < total {
					return refuseVillage(village.LandNoMoney, village.AddrLandRegistry)
				}
				return nil
			}
			if fresh, err := h.reserve(ctx, tx, sc.p.ID, meta); err != nil || !fresh {
				return err
			}
			if err := tx.Items().LockOwner(ctx, sc.p.ID); err != nil {
				return err
			}
			// the stone he carries is used; the rest is bought with the wage, all into the sink
			bill, err = h.planMaterials(ctx, tx, snap, sc.p.ID, def)
			if err != nil {
				return err
			}
			for _, m := range bill.lines {
				if m.have <= 0 {
					continue
				}
				if err := tx.Items().Move(ctx, application.ItemMove{Item: m.code, Qty: m.have, From: sc.p.ID, FromHolding: application.HoldCarried,
					Reason: application.ItemSettlementConstruction, ReferenceType: "foundation", ReferenceID: strings.TrimSpace(req.Lot), At: now}); err != nil {
					if stderrors.Is(err, application.ErrNotEnoughItems) {
						return refuseVillage(village.VillageMaterials, village.AddrLandRegistry)
					}
					return err
				}
			}
			total = wage + bill.boughtCost
			if total > 0 {
				cashAcct, _, err := playerCash(ctx, tx, sc.p.ID)
				if err != nil {
					return err
				}
				if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{ID: h.ids.NewID(), Reason: application.ReasonFoundationWorks, CreatedAt: now,
					ReferenceType: "foundation", ReferenceID: strings.TrimSpace(req.Lot),
					Entries: []application.LedgerEntry{{AccountID: cashAcct.ID, Amount: money.FromMinor(-total)}, {AccountID: application.SystemSinkAccountID, Amount: money.FromMinor(total)}}}); err != nil {
					if stderrors.Is(err, application.ErrInsufficientFunds) {
						return refuseVillage(village.LandNoMoney, village.AddrLandRegistry)
					}
					return err
				}
			}
		} else {
			// the treasury's lot: the treasury buys the stone at the reference price and pays the mason
			cd, _ := snap.ComponentDef("stone")
			unit := cd.BasePrice * int64(h.citizen.MaterialMarkupBPS) / 10_000
			if cd.BasePrice > 0 && unit < 1 {
				unit = 1
			}
			total := stone*unit + wage
			bal, err := treasuryBalance(ctx, tx, sc.s.CityID)
			if err != nil {
				return err
			}
			if !req.confirmed() {
				view.Quote = &village.LandQuote{Act: village.AddrFoundationLay, Args: args, Cells: 1, Lot: &village.RegCell{X: x, Y: y}, Money: total, StoneNeed: stone, StoneBuy: stone, Cash: bal}
				if bal < total {
					return refuseVillage(village.LandNoMoney, village.AddrLandRegistry)
				}
				return nil
			}
			if fresh, err := h.reserve(ctx, tx, sc.p.ID, meta); err != nil || !fresh {
				return err
			}
			if _, err := spendVillage(ctx, tx, sc.s.CityID, application.ReasonFoundationWorks, total, now); err != nil {
				return err
			}
		}
		laid, err := tx.Registry().InsertFoundation(ctx, application.Foundation{SettlementID: sc.s.CityID, X: x, Y: y, LaidBy: sc.p.ID, Steps: 1, LaidAt: now})
		if err != nil {
			return err
		}
		if !laid {
			return refuseVillage(village.LandFoundationDone, village.AddrLandRegistry)
		}
		view.Notice = village.LandNoticeFoundated
		return h.registryDone(ctx, tx, meta, sc, "foundation_laid", map[string]any{"x": x, "y": y, "band": band, "stone": stone, "wage": wage}, now)
	})
}

// knows reports a research the settlement holds.
func (h *VillageHandler) knows(ctx context.Context, tx application.Tx, s application.FoundedSettlement, code string) bool {
	owned, err := tx.SettlementKnowledge().Owned(ctx, s.CityID)
	if err != nil {
		return false
	}
	for _, o := range owned {
		if o.Code == code {
			return true
		}
	}
	return false
}
