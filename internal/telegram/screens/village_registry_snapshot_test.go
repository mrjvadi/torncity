package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The land registry joins the snapshot harness as an area of its own (docs/adr/0072).
func init() { snapshotAreas["land"] = landSnapshots }

func landSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	me := presentation.Named{Code: myCode, Name: who.me}
	friend := presentation.Named{Code: "F4R5T6Y", Name: who.friend}
	zone := &presentation.Named{Code: "z1", Name: map[string]string{"fa": "کشتزار", "en": "Fields"}[c.Lang]}
	later := snapshotNow.Add(40 * 24 * time.Hour)
	full := village.LandRegistryView{
		Village: villageNameFor(c), Foundations: 2, ZoningOn: true, SlopeOn: true, CanZone: true, CanCloseZone: true, CanClaim: true,
		ClaimUpkeepPerDay: 5, ParcelMaxCells: 60, ParcelChangeFee: 50, ZoneOpenFee: 20, ClaimCost: 2000,
		Parcels: []village.ParcelLine{
			{ID: "p1", Owner: me, Mine: true, Cells: []village.RegCell{{X: 2, Y: 2}, {X: 3, Y: 2}}, CanJoin: []string{"p2"}, CanSplit: true, Zone: zone},
			{ID: "p2", Owner: me, Mine: true, Cells: []village.RegCell{{X: 4, Y: 2}}, CanJoin: []string{"p1"}},
			{ID: "p3", Owner: friend, Cells: []village.RegCell{{X: 0, Y: 5}}},
			{ID: "p4", Treasury: true, Cells: []village.RegCell{{X: 6, Y: 6}, {X: 6, Y: 7}}},
		},
		Zones: []village.ZoneLine{
			{ID: "z1", Name: zone.Name, Kind: "farm", Cells: 12, Effective: true, OpenedBy: friend, CanClose: true},
			{ID: "z2", Name: map[string]string{"fa": "بازار", "en": "Market row"}[c.Lang], Kind: "core", Cells: 4, EffectiveAt: later, OpenedBy: me},
		},
		Claims: []village.ClaimLine{{ID: "c1", Face: 1, GX: 3, GY: 4, ClaimedAt: snapshotNow, PaidThrough: later}},
		Notice: village.LandNoticeJoined,
	}
	add("Land registry · parcels, zones and claims", LandRegistry(c, full))
	soon := village.LandRegistryView{Village: villageNameFor(c), ZoningStartsAt: &later, SlopeStartsAt: &later}
	add("Land registry · nothing yet, the rules not started", LandRegistry(c, soon))
	quote := village.LandRegistryView{Village: villageNameFor(c), ZoningOn: true,
		Quote: &village.LandQuote{Act: village.AddrFoundationLay, Args: []string{"3-4"}, Cells: 1, Money: 220, StoneNeed: 8, StoneHave: 5, StoneBuy: 3, Cash: 500, Lot: &village.RegCell{X: 3, Y: 4}}}
	add("Land registry · a foundation quote", LandRegistry(c, quote))
	for _, k := range []string{village.LandNotYours, village.LandNeedsFoundation, village.LandZoneForbids, village.LandCommons} {
		add("Land refusal · "+k, VillageRefusal(c, VillageRefusalView{Kind: k}))
	}
}
