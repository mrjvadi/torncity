package screens

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// TestLawScreenRenders renders the law screen in every shipped language, shut and open, with a case, a sentence, an informant, a bribe, the board, a front and
// a load, and holds each to the rules every screen follows: no catalogue key or placeholder left over, every button a routable address within Telegram's 64 bytes.
func TestLawScreenRenders(t *testing.T) {
	now := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	end := now.Add(10 * time.Hour)
	for _, lang := range []string{"fa", "en"} {
		c := ctx(t, lang, 0)
		line := village.LawCaseLine{ID: "c1", No: 7, Offence: presentation.Named{Code: "bribery", Name: "Bribery"}, Accused: presentation.Named{Code: "AAAA", Name: "Ada"}, State: "hearing", Source: "audit", Strength: 62, At: now}
		detail := &village.LawCaseDetail{Line: line, Model: "adversarial", Total: 62, Quality: 70,
			Evidence:       []village.LawEvidenceLine{{ID: "e1", Kind: "record", Strength: 25, Proper: true}, {ID: "e2", Kind: "seized_item", Strength: 5}, {ID: "e3", Kind: "witness", Strength: 20, Proper: true, Excluded: true}},
			Verdicts:       []village.LawVerdictLine{{Instance: 1, Guilty: true, NPC: true}, {Instance: 2}},
			MayInvestigate: true, MayAskWarrant: true, MayArrest: true, MayCharge: true, MayDrop: true, MayDefend: true, MayAppeal: true, MayAppealAs: "accused", MayDecide: true}
		full := village.LawView{Village: "Alpha", Model: "inquisitorial", Open: village.LawOpen{Police: true, Courts: true, Black: true, Customs: true, Launder: true},
			Mays:   village.LawMays{Patrol: true, Pardon: true, Informants: true},
			Posts:  []village.LawPostLine{{Service: "constabulary", Building: presentation.Named{Name: "Police station"}, Stands: true, Open: true}, {Service: "custody", Building: presentation.Named{Name: "Jail"}, Stands: true, Open: true, Cells: 10, Held: 2}, {Service: "court", Building: presentation.Named{Name: "Courthouse"}}},
			Points: 40, Spent: 12, Patrol: 3, Captured: true,
			Offences: []village.LawOffenceLine{{Code: "bribery", Name: "Bribery", Class: "serious", Listed: true, MaxFine: 100}, {Code: "theft", Name: "Theft", Class: "petty"}},
			Cases:    []village.LawCaseLine{line}, Case: detail,
			Mine: []village.LawSentenceLine{{ID: "s1", Kind: "jail", Status: "active", JailHours: 10, EndsAt: &end, Bail: 2500, MayBail: true, MayPardon: true},
				{ID: "s2", Kind: "fine", Status: "paid", Fine: 600, FinePaid: 600}, {ID: "s3", Kind: "ban", Ban: "market", BanDays: 3, Status: "active"}},
			Informants: []village.LawInformantLine{{ID: "i1", Reliability: 55, State: "active", Mine: true}},
			Bribes:     []village.LawBribeLine{{ID: "b1", Rung: "warrant", Status: "offered", Amount: 600, EffectBPS: 6000, MayAnswer: true}},
			Board: village.LawBoard{Known: true, Standing: 15, Dirty: 300, Listings: []village.LawListingLine{{ID: "l1", Item: presentation.Named{Name: "Bread"}, Qty: 2, Price: 50}, {ID: "l2", Item: presentation.Named{Name: "Coffee"}, Qty: 1, Price: 90, Mine: true}},
				Escrows: []village.LawEscrowLine{{ID: "x1", Status: "held", Amount: 100, Item: presentation.Named{Name: "Bread"}, Buyer: true, Broker: true, Mine: true}}},
			Fronts: []village.LawFrontLine{{Company: presentation.Named{Name: "Mill"}, Batches: []village.LawBatchLine{{ID: "t1", State: "placed", Amount: 100}}}},
			Loads:  []village.LawLoadLine{{ID: "d1", Status: "declared", To: presentation.Named{Name: "Bee"}, MayCross: true}, {ID: "d2", Status: "seized"}},
			Notice: village.LawNoticeVerdict}
		unknown := village.LawView{Village: "Alpha", Board: village.LawBoard{}, Open: village.LawOpen{Black: true}}
		for name, resp := range map[string]*presenter.Response{
			"shut":    Law(c, village.LawView{Village: "Alpha"}),
			"open":    Law(c, full),
			"unknown": Law(c, unknown),
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

// Every refusal of the law screen has its wording (the Telegram layer shows nothing but a key otherwise).
func TestLawRefusalsAreAllWorded(t *testing.T) {
	kinds := []string{village.LawAlreadyFront, village.LawAlreadyInformant, village.LawAppealLate, village.LawAppealUsed, village.LawAuditedToday, village.LawBadArgs,
		village.LawBanned, village.LawBriberShort, village.LawBribeSmall, village.LawCaseClosed, village.LawCaseOpen, village.LawConflict, village.LawHasDefence,
		village.LawInCustody, village.LawJudgeLimit, village.LawKindNotAllowed, village.LawNobodyTalks, village.LawNoBribe, village.LawNoBroker, village.LawNoCase,
		village.LawNoCompany, village.LawNoEscrow, village.LawNoEvidence, village.LawNoFaction, village.LawNoFront, village.LawNoGoods, village.LawNoInformant, village.LawNoJail,
		village.LawNoListing, village.LawNoLoad, village.LawNoMoney, village.LawNoOffence, village.LawNoPlayer, village.LawNoPoints, village.LawNoSentence, village.LawNoSettlement,
		village.LawNoStanding, village.LawNotAccused, village.LawNotAppealed, village.LawNoTarget, village.LawNotHolder, village.LawNotImproper, village.LawNotInHearing,
		village.LawNotLeader, village.LawNotOutlawed, village.LawNotResident, village.LawNotYet, village.LawNotYourInformant, village.LawNoWarrant, village.LawOverBound,
		village.LawOverBrokerCap, village.LawPostClosed, village.LawResearch, village.LawRotateDue, village.LawRotateNotDue, village.LawSelf, village.LawSeparation,
		village.LawTipsAlone, village.LawTooManyInformants, village.LawTreasuryShort, village.LawWeakEvidence}
	notices := []string{village.LawNoticeAlreadyKnown, village.LawNoticeAppealDecided, village.LawNoticeAppealed, village.LawNoticeArrested, village.LawNoticeAuditAnomaly,
		village.LawNoticeAuditClean, village.LawNoticeAuditFound, village.LawNoticeBailed, village.LawNoticeBought, village.LawNoticeBribeOffered, village.LawNoticeBribeRefused,
		village.LawNoticeBribeTaken, village.LawNoticeBroker, village.LawNoticeBusted, village.LawNoticeCharged, village.LawNoticeComplaint, village.LawNoticeConfirmed,
		village.LawNoticeContact, village.LawNoticeCrossed, village.LawNoticeDeclared, village.LawNoticeDefended, village.LawNoticeDelisted, village.LawNoticeDropped,
		village.LawNoticeEscrowed, village.LawNoticeExcluded, village.LawNoticeFront, village.LawNoticeGangDeclared, village.LawNoticeGangMarked, village.LawNoticeInspectedClean,
		village.LawNoticeInvestigated, village.LawNoticeLaundered, village.LawNoticeListed, village.LawNoticeModel, village.LawNoticeNotExcluded, village.LawNoticeNothingNew,
		village.LawNoticeOffence, village.LawNoticeOffenceGone, village.LawNoticePardoned, village.LawNoticePatrolQuiet, village.LawNoticePatrolSaw, village.LawNoticeRecruited,
		village.LawNoticeRefunded, village.LawNoticeRetired, village.LawNoticeRotated, village.LawNoticeScam, village.LawNoticeScamTraced, village.LawNoticeSeized,
		village.LawNoticeTip, village.LawNoticeTipEmpty, village.LawNoticeVerdict, village.LawNoticeWarrantAsked, village.LawNoticeWarrantRefused, village.LawNoticeWarrantSigned}
	for _, lang := range []string{"fa", "en"} {
		c := ctx(t, lang, 0)
		for _, kind := range kinds {
			assertRendered(t, VillageRefusal(c, village.VillageRefusalView{Kind: kind}))
		}
		for _, n := range notices {
			assertRendered(t, Law(c, village.LawView{Village: "Alpha", Open: village.LawOpen{Police: true}, Notice: n}))
		}
	}
}
