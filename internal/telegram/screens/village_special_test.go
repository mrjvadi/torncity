package screens

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// TestSpecialScreensRender renders the screens of great works, intelligence and licences in every shipped language, shut and open, and holds each
// to the rules every screen follows: no catalogue key or placeholder left over, every button a routable address within Telegram's 64 bytes. It also
// holds the intelligence report to its rule: a band is a word the catalogue words, never a figure.
func TestSpecialScreensRender(t *testing.T) {
	now := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	for _, lang := range []string{"fa", "en"} {
		c := ctx(t, lang, 0)
		project := village.ProjectLine{ID: "p1", Kind: presentation.Named{Code: "aqueduct", Name: "Aqueduct"}, Status: "active", Stage: 2, Stages: 3, Hours: 8, NeedHour: 32, Fund: 1200,
			Waiting: village.ProjectWaitMaterials, Materials: []village.ProjectMaterialLine{{Item: "stone", Need: 40, Have: 12}}, MayWork: true, MayCancel: true}
		done := village.ProjectLine{ID: "p2", Kind: presentation.Named{Code: "great_beacon", Name: "Great beacon"}, Status: "finished", Stage: 4, Stages: 4, Held: false, Idle: "no_wage"}
		report := village.IntelReportLine{ID: "r1", Op: "survey_stock", Target: presentation.Named{Code: "b", Name: "Bee"}, Confidence: "medium", TakenAt: now, Old: true,
			Fields: []village.IntelFieldLine{{Metric: "treasury", Band: "ample"}, {Metric: "food", Band: "scarce"}, {Metric: "materials", Band: "enough"}}}
		for name, resp := range map[string]*presenter.Response{
			"project shut":  Project(c, village.ProjectView{Village: "Alpha"}),
			"project open":  Project(c, village.ProjectView{Village: "Alpha", Open: true, MayStart: true, Kinds: []village.ProjectKindLine{{Code: "aqueduct", Name: "Aqueduct", Available: true}}, Projects: []village.ProjectLine{project, done}, Notice: village.ProjectNoticeStarted}),
			"project empty": Project(c, village.ProjectView{Village: "Alpha", Open: true}),
			"intel shut":    Intel(c, village.IntelView{Village: "Alpha"}),
			"intel open": Intel(c, village.IntelView{Village: "Alpha", Open: true, BureauBuilt: true, BureauOpen: true, Counter: "light", SabotageOpen: true,
				Offers: []village.IntelAgentLine{{ID: "o1", Sponsor: presentation.Named{Name: "Alpha"}, Target: presentation.Named{Name: "Bee"}, MayAccept: true}},
				Agents: []village.IntelAgentLine{{ID: "a1", Player: presentation.Named{Name: "Ada"}, Target: presentation.Named{Name: "Bee"}, Status: "active", CoverDays: 3, MayOperate: true, MayRecall: true}},
				Ops:    []village.IntelOpLine{{Code: "survey_stock", Name: "Stock", Kind: "survey", Available: true}}, Reports: []village.IntelReportLine{report}}),
			"intel no bureau": Intel(c, village.IntelView{Village: "Alpha", Open: true, Counter: "none"}),
			"licence shut":    Licence(c, village.LicenceView{Village: "Alpha"}),
			"licence open": Licence(c, village.LicenceView{Village: "Alpha", Open: true,
				Kinds:        []village.LicenceKindLine{{Code: "stall_right", Name: "Stall", Required: true, Mode: "auction", Reserve: 500, TermDays: 30}},
				Mine:         []village.LicenceLine{{ID: "l1", No: 7, Kind: "stall_right", TermEnd: now, MayRenew: true}},
				Applications: []village.LicenceApplicationLine{{ID: "x1", Kind: "craft_privilege", Holder: presentation.Named{Name: "Ada"}, Fee: 200, MayDecide: true}},
				Auctions:     []village.LicenceAuctionLine{{ID: "u1", Kind: "stall_right", Reserve: 500, Status: "open", ClosesAt: now, MayBid: true}},
				Board:        []village.LicenceListingLine{{ID: "g1", Licence: village.LicenceLine{No: 8, Kind: "stall_right"}, Price: 900, MayBuy: true}}, Notice: village.LicenceNoticeGranted}),
		} {
			t.Run(lang+"/"+name, func(t *testing.T) {
				assertRendered(t, resp)
				for _, a := range addresses(resp) {
					if len(a) > 64 {
						t.Errorf("address over 64 bytes: %q", a)
					}
				}
			})
		}
	}
}

// Every refusal and notice kind of the three screens has its wording (the Telegram layer shows nothing but a key otherwise).
func TestSpecialRefusalsAreAllWorded(t *testing.T) {
	kinds := []string{village.ProjectNotYet, village.ProjectBadArgs, village.ProjectNoKind, village.ProjectNoProject, village.ProjectResearch, village.ProjectTooMany, village.ProjectNoMoney,
		village.ProjectBelowMin, village.ProjectNotActive, village.ProjectNoPledge, village.ProjectSpent, village.ProjectNotNeeded, village.ProjectNoStock, village.ProjectNotHere,
		village.ProjectBusy, village.ProjectCrewFull, village.ProjectFundShort, village.ProjectTooLate, village.ProjectHasContract, village.ProjectNoCrew, village.ProjectNoHours,
		village.IntelNotYet, village.IntelBadArgs, village.IntelNoBureau, village.IntelBureauClosed, village.IntelNoCover, village.IntelExpelled, village.IntelDayLimit, village.IntelDone,
		village.IntelSabRefused + "_weekly_limit", village.IntelSabRefused + "_too_soon", village.IntelSabRefused + "_protected", village.IntelSabRefused + "_forbidden",
		village.LicenceNotYet, village.LicenceNoRule, village.LicenceInGrace, village.LicenceGraceOver, village.LicenceTaken, village.LicenceOverShare, village.LicenceCooling,
		village.LicenceNeedSecond, village.LicenceBadCause, village.LicenceMissing, village.LicenceConflict}
	for _, lang := range []string{"fa", "en"} {
		c := ctx(t, lang, 0)
		for _, kind := range kinds {
			resp := VillageRefusal(c, village.VillageRefusalView{Kind: kind})
			assertRendered(t, resp)
		}
	}
}
