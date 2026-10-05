package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The charter with its phase 2 parts (elections, a recall petition and vote, an
// amendment, the acting head), as its own snapshot area.
func init() { snapshotAreas["charter"] = charterSnapshots }

func charterSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	g := group(c)
	name := villageNameFor(c)
	at := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	// names that belong to the locale: the snapshot checks that no Arabic-script text
	// sits in English and no Latin word in Persian
	tr := func(fa, en string) string {
		if c.Lang == "fa" {
			return fa
		}
		return en
	}
	head, kad, ali, reza := tr("شهردار", "Mayor"), tr("کدخدا", "Elder"), tr("علی", "Ali"), tr("رضا", "Reza")
	p := func(n, code string) village.CharterPersonView { return village.CharterPersonView{Name: n, Code: code} }
	yes, no := int64(3), int64(1)
	v := village.CharterView{
		Village: name,
		Offices: []village.CharterOfficeView{
			{ID: "a", Title: head, Seats: 1, Acquisition: "head", Founder: true, Grants: []village.CharterGrantView{{Permission: "road.draw"}}},
			{ID: "b", Title: kad, Seats: 1, Open: 1, Acquisition: "election", Deputy: true, Grants: []village.CharterGrantView{{Permission: "storage.take"}, {Permission: "treasury.spend", Limit: 500}}},
		},
		HeadVacant: true,
		Acting:     &village.CharterActingView{Player: p(ali, "AAAA"), Office: kad, Ends: at.Add(7 * 24 * time.Hour), SpendCap: 2000},
		Ballots: []village.CharterBallotView{
			{ID: "1", Kind: "election", Office: head, Phase: "candidacy", Status: "open", OpensAt: at, ClosesAt: at.Add(120 * time.Hour), Eligible: 6},
			{ID: "2", Kind: "recall", Office: kad, Target: &village.CharterPersonView{Name: reza, Code: "BBBB"}, Phase: "voting", Status: "open", OpensAt: at, ClosesAt: at.Add(72 * time.Hour), Eligible: 6},
			{ID: "3", Kind: "amendment", Office: kad, Proposal: &village.CharterProposalView{Op: "save", Title: kad}, Phase: "closed", Status: "passed", Yes: &yes, No: &no},
			{ID: "4", Kind: "recall", Office: kad, Target: &village.CharterPersonView{Name: reza, Code: "BBBB"}, Phase: "closed", Status: "failed"},
		},
		Petitions: []village.CharterPetitionView{{ID: "p", Office: kad, Target: p(reza, "BBBB"), Signatures: 2, Needed: 3}},
		Audit:     []village.CharterAuditView{{Action: "election_opened", Title: head, Actor: ali, At: at}, {Action: "vote_cast", At: at}, {Action: "amendment_passed", Title: kad, At: at}},
	}
	add("Charter · elections, a petition, an amendment, an acting head", VillageCharter(g, v))
	add("Charter · an election opened", VillageCharterChanged(g, village.CharterChangedView{Action: "election_opened", Title: kad}))
	add("Charter · a vote cast", VillageCharterChanged(g, village.CharterChangedView{Action: "vote_cast"}))
	add("Charter · an amendment proposed", VillageCharterChanged(g, village.CharterChangedView{Action: "amendment_proposed", Title: kad}))
}
