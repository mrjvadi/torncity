package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens/screentest"
)

// Elections join the snapshot harness as their own area,
// testdata/snapshots/<language>/elections.txt.
func init() { snapshotAreas["elections"] = electionSnapshots }

func electionSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	city := GovPlace{Kind: "city", Code: "ostmarch", Name: "Ostmarch"}
	country := GovPlace{Kind: "country", Code: "default_country", Name: "The Commonwealth"}
	me := GovPlayer{Name: who.me, Code: "K7Q2M9A"}
	friend := GovPlayer{Name: who.friend, Code: friendCode}
	add("Elections", Elections(c, ElectionsView{Place: city, Elections: []ElectionLine{
		{No: 4, Office: "mayor", Place: city, Phase: ElectionCandidacy, Seats: 1, Remaining: 31 * time.Hour, Candidates: 2},
		{No: 5, Office: "city_council", Place: city, Phase: ElectionVoting, Seats: 5, Remaining: 12 * time.Hour, Candidates: 6},
		{No: 2, Office: "president", Place: country, Phase: ElectionCounting, Seats: 1},
		{No: 1, Office: "mayor", Place: city, Phase: ElectionCounted, Seats: 1, Elected: []GovPlayer{friend}},
		{No: 3, Office: "city_council", Place: city, Phase: ElectionCounted, Seats: 5},
	}}))
	add("Elections · none yet", Elections(c, ElectionsView{Place: city}))
	add("Elections · not in a city", Elections(c, ElectionsView{NoCity: true}))

	candidacy := ElectionView{
		No: 4, Office: "mayor", Place: city, Seats: 1, Phase: ElectionCandidacy, Remaining: 31 * time.Hour,
		CandidacyEndsAt: snapshotNow.Add(31 * time.Hour), VotingEndsAt: snapshotNow.Add(79 * time.Hour),
		Candidates: []CandidateLine{{Player: friend}}, Deposit: 5000, RefundShareBPS: 1000, MinLevel: 5, Nonce: "0a1b2c3d4e70",
		CanStand: true,
		Payment: &PaymentChoice{Amount: 5000, Accepted: []string{MethodCash, MethodCard}, Usable: []string{MethodCash, MethodCard},
			Cash: 8000, Bank: 20000},
	}
	add("Election · taking candidates, you may stand", Election(c, candidacy))
	add("Election · taking candidates, in a group", Election(group(c), candidacy))
	low := candidacy
	low.CanStand, low.Payment, low.StandBlocked = false, nil, "level"
	add("Election · taking candidates, level too low", Election(c, low))
	standing := candidacy
	standing.CanStand, standing.Payment, standing.Standing = false, nil, true
	standing.Candidates = []CandidateLine{{Player: friend}, {Player: me, Mine: true}}
	add("Election · you are standing", Election(c, standing))

	voting := ElectionView{
		No: 5, Office: "city_council", Place: city, Seats: 5, Phase: ElectionVoting, Remaining: 12 * time.Hour,
		CandidacyEndsAt: snapshotNow.Add(-36 * time.Hour), VotingEndsAt: snapshotNow.Add(12 * time.Hour),
		Candidates: []CandidateLine{{Player: friend}, {Player: me, Mine: true}, {Player: GovPlayer{Name: who.third, Code: thirdCode}}},
		CanVote:    true, Nonce: "0a1b2c3d4e71",
	}
	add("Election · voting", Election(c, voting))
	voted := voting
	voted.CanVote, voted.Voted = false, true
	add("Election · voted", Election(c, voted))
	newcomer := voting
	newcomer.CanVote, newcomer.VoteBlocked = false, "too_new"
	add("Election · voting, too new to vote", Election(c, newcomer))
	counted := ElectionView{
		No: 1, Office: "mayor", Place: city, Seats: 1, Phase: ElectionCounted, VotesCast: 23,
		Candidates: []CandidateLine{
			{Player: friend, Counted: true, Votes: 15, Elected: true},
			{Player: me, Mine: true, Counted: true, Votes: 8},
		},
	}
	add("Election · counted", Election(group(c), counted))
	add("Election · counting", Election(c, ElectionView{No: 2, Office: "president", Place: country, Seats: 1,
		Phase: ElectionCounting, Candidates: []CandidateLine{{Player: friend}}}))
	add("Stood", Stood(c, StoodView{No: 4, Office: "mayor", Place: city, Deposit: 5000, Method: MethodCard,
		VotingAt: snapshotNow.Add(31 * time.Hour), VotingIn: 31 * time.Hour}))
	add("Voted", Voted(c, VotedView{No: 5, Office: "city_council", Place: city, Candidate: friend,
		CountAt: snapshotNow.Add(12 * time.Hour), CountIn: 12 * time.Hour}))
	for _, kind := range []string{
		ElectionRefusedNone, ElectionRefusedNotStanding, ElectionRefusedNotVoting, ElectionRefusedAway, ElectionRefusedNoCandidate,
		"not_resident", "too_new", "level", "record", "jailed", "standing", "voted", "incompatible",
	} {
		v := ElectionRefusalView{Kind: kind}
		if kind != ElectionRefusedNone {
			v.No, v.Office, v.Place = 4, "mayor", city
		}
		add("Election refused · "+kind, ElectionRefusal(c, v))
	}
	add("Notice · elected", ElectionResultNotice(sent(c), ElectionResultView{No: 1, Office: "mayor", Place: city,
		Elected: true, Votes: 15, Cast: 23, Deposit: 5000, DepositReturned: true}))
	add("Notice · not elected, deposit kept", ElectionResultNotice(sent(c), ElectionResultView{No: 1, Office: "mayor", Place: city,
		Votes: 1, Cast: 23, Deposit: 5000}))
}

// electionAnnouncements are the public lines a city's groups read of an
// election; they join the group lines (group.txt).
func electionAnnouncements(c Context, who people, book *screentest.Book) {
	city := GovPlace{Kind: "city", Code: "ostmarch", Name: "Ostmarch"}
	friend := GovPlayer{Name: who.friend, Code: friendCode}
	third := GovPlayer{Name: who.third, Code: thirdCode}
	book.AddText("announcement · an election opened", ElectionOpenedAnnouncement(c, "mayor", city, 4, 48*time.Hour))
	book.AddText("announcement · a candidate stood", ElectionStoodAnnouncement(c, who.friend, "mayor", city))
	book.AddText("announcement · the vote opened", ElectionVotingAnnouncement(c, "mayor", city, 3, 48*time.Hour))
	book.AddText("announcement · the vote opened, nobody stood", ElectionVotingAnnouncement(c, "mayor", city, 0, 48*time.Hour))
	book.AddText("announcement · elected", ElectionCountedAnnouncement(c, "mayor", city, []GovPlayer{friend}, 23, 2))
	book.AddText("announcement · a council elected", ElectionCountedAnnouncement(c, "city_council", city,
		[]GovPlayer{friend, third}, 41, 4))
	book.AddText("announcement · nobody won a vote", ElectionCountedAnnouncement(c, "city_council", city, nil, 0, 2))
	book.AddText("announcement · nobody stood", ElectionCountedAnnouncement(c, "mayor", city, nil, 0, 0))
}
