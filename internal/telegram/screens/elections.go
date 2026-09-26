package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Elections: the elections of the player's city and country, one election
// with its candidates, standing, voting, and the public lines a city's groups
// read when one opens and when one is counted.
//
// A ballot is secret: no screen says who voted for whom, and the counts are
// shown only after the count.

// Callback addresses of elections.
const (
	AddrElections     = "election:list"
	AddrElection      = "election:view"
	AddrElectionStand = "election:stand"
	AddrElectionVote  = "election:vote"
)

// ElectionCounted is the phase of an election after its count; the others
// are the domain's candidacy, voting and counting.
const ElectionCounted = "counted"

// Election phases as the screens spell them.
const (
	ElectionCandidacy = "candidacy"
	ElectionVoting    = "voting"
	ElectionCounting  = "counting"
)

// ElectionLine is one election of the list.
type ElectionLine struct {
	No     int64
	Office string
	Place  GovPlace
	Phase  string
	Seats  int
	// EndsAt and Remaining are the end of the phase under way.
	EndsAt     time.Time
	Remaining  time.Duration
	Candidates int
	// Elected are the winners of a counted election.
	Elected []GovPlayer
}

// ElectionsView is the elections of the player's city and above it.
type ElectionsView struct {
	NoCity    bool
	Place     GovPlace
	Elections []ElectionLine
}

// electionTitle names an election: the usual name of an office's election
// (election.name.<office>), or the office and the place.
func (c Context) electionTitle(office string, place GovPlace) string {
	args := map[string]any{"office": c.OfficeName(office), "place": c.PlaceName(place)}
	if office != "" {
		key := "election.name." + office
		if text := c.T(key, args); text != key {
			return text
		}
	}
	return c.T("election.of", args)
}

// clockHorizon is how far ahead a clock time alone ("at 14:32") still names
// one moment: within a day. Past it, the election calendar says how long
// until instead. Layout, not tuning.
const clockHorizon = 24 * time.Hour

// electionWhen is a moment of the election calendar, in a remaining in: how
// long until it, and within a day its clock time too.
func electionWhen(c Context, clockKey, spanKey string, at time.Time, in time.Duration) string {
	if at.IsZero() {
		return ""
	}
	args := map[string]any{"duration": FormatSpan(c, in)}
	if in < clockHorizon {
		args["time"] = FormatClock(c, at)
		return c.T(clockKey, args)
	}
	return c.T(spanKey, args)
}

// electionLine renders one election in the list.
func (c Context) electionLine(l ElectionLine) string {
	args := map[string]any{
		"no": FormatNumber(c, l.No), "election": c.electionTitle(l.Office, l.Place),
		"remaining": FormatSpan(c, l.Remaining), "count": FormatNumber(c, int64(l.Candidates)),
		"elected": c.govPlayers(l.Elected),
	}
	switch l.Phase {
	case ElectionCounted:
		if len(l.Elected) == 0 {
			return c.T("election.line.unfilled", args)
		}
		return c.T("election.line.counted", args)
	case ElectionCounting:
		return c.T("election.line.counting", args)
	case ElectionVoting:
		return c.T("election.line.voting", args)
	}
	return c.T("election.line.candidacy", args)
}

// Elections renders the list.
func Elections(c Context, v ElectionsView) *presenter.Response {
	return c.withView(renderElections(c, v), ScreenElections, v)
}

func renderElections(c Context, v ElectionsView) *presenter.Response {
	kb := keyboards.New()
	if v.NoCity {
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrMap}))
		return c.respond(paragraphs(c.T("election.title_plain", nil), c.T("election.no_city", nil)), kb.Build())
	}
	lines := []string{}
	var buttons []presenter.Button
	for _, l := range v.Elections {
		lines = append(lines, c.electionLine(l))
		if btn, ok := keyboards.Button(c.T("election.button.open", map[string]any{"no": FormatNumber(c, l.No)}),
			AddrElection, strconv.FormatInt(l.No, 10)); ok {
			buttons = append(buttons, btn)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, c.T("election.none", nil))
	}
	kb.Grid(3, buttons...)
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrGovCity, v.Place.Code), RefreshData: AddrElections}))
	return c.respond(paragraphs(c.T("election.title", map[string]any{"place": c.PlaceName(v.Place)}), body(lines...),
		c.T("election.hint", nil)), kb.Build())
}

// CandidateLine is one candidate on the ballot.
type CandidateLine struct {
	Player GovPlayer
	// Mine marks the viewer.
	Mine bool
	// Votes is known once Counted.
	Votes   int64
	Counted bool
	Elected bool
}

// ElectionView is one election for the viewer.
type ElectionView struct {
	No     int64
	Office string
	Place  GovPlace
	Seats  int
	Phase  string
	// Remaining is what is left of the phase under way.
	Remaining                     time.Duration
	CandidacyEndsAt, VotingEndsAt time.Time
	VotesCast                     int64
	Candidates                    []CandidateLine
	Deposit                       int64
	RefundShareBPS                int
	MinLevel                      int
	// The viewer: standing, voted, and what they may do now. StandBlocked
	// and VoteBlocked name why not (the domain's Why).
	Standing, Voted           bool
	CanStand, CanVote         bool
	StandBlocked, VoteBlocked string
	// Payment is how the deposit may be paid, nil for none.
	Payment *PaymentChoice
	// Nonce binds the stand and vote buttons.
	Nonce string
}

// Election renders one election.
func Election(c Context, v ElectionView) *presenter.Response {
	return c.withView(renderElection(c, v), ScreenElection, v)
}

func renderElection(c Context, v ElectionView) *presenter.Response {
	facts := []string{c.T("election.seats", map[string]any{"seats": FormatNumber(c, int64(v.Seats))})}
	switch v.Phase {
	case ElectionCandidacy:
		facts = append(facts, c.T("election.phase.candidacy", map[string]any{"remaining": FormatSpan(c, v.Remaining)}),
			electionWhen(c, "election.voting_from", "election.voting_in", v.CandidacyEndsAt, v.Remaining))
		if v.Deposit > 0 {
			facts = append(facts, c.T("election.deposit", map[string]any{"deposit": FormatMoney(c, v.Deposit),
				"share": c.T("gov.percent", map[string]any{"value": PercentFromBPS(c, v.RefundShareBPS)})}))
		}
	case ElectionVoting:
		facts = append(facts, c.T("election.phase.voting", map[string]any{"remaining": FormatSpan(c, v.Remaining)}),
			electionWhen(c, "election.count_at", "election.count_in", v.VotingEndsAt, v.Remaining))
	case ElectionCounting:
		facts = append(facts, c.T("election.phase.counting", nil))
	default:
		facts = append(facts, c.T("election.phase.counted", map[string]any{"cast": FormatNumber(c, v.VotesCast)}))
	}

	cands := []string{c.T("election.candidates", nil)}
	if len(v.Candidates) == 0 {
		cands = append(cands, c.T("election.no_candidates", nil))
	}
	kb := keyboards.New()
	var votes []presenter.Button
	for i, cand := range v.Candidates {
		args := map[string]any{"n": FormatNumber(c, int64(i+1)), "player": c.govPlayer(&cand.Player),
			"votes": FormatNumber(c, cand.Votes)}
		key := "election.candidate"
		switch {
		case cand.Counted && cand.Elected:
			key = "election.candidate_elected"
		case cand.Counted:
			key = "election.candidate_counted"
		}
		line := c.T(key, args)
		// In a group everyone reads the screen: nobody is "you" there.
		if cand.Mine && !c.Shared {
			line = c.T("election.candidate_you", map[string]any{"line": line})
		}
		cands = append(cands, line)
		if v.CanVote && !c.Shared {
			if btn, ok := keyboards.Button(c.T("election.button.vote", map[string]any{"player": c.govPlayer(&cand.Player)}),
				AddrElectionVote, strconv.FormatInt(v.No, 10), strconv.Itoa(i+1), v.Nonce); ok {
				votes = append(votes, btn)
			}
		}
	}
	kb.Grid(1, votes...)

	// In a group the screen is everyone's: it says nothing of the viewer —
	// not whether they stand, may vote or have voted — only where standing
	// and voting are done.
	var you []string
	switch {
	case c.Shared:
		if v.Phase == ElectionCandidacy || v.Phase == ElectionVoting {
			you = append(you, c.T("election.private", nil))
		}
	case v.Standing && v.Phase != ElectionCounted:
		you = append(you, c.T("election.you_stand", nil))
	case v.CanStand && v.Payment != nil && len(v.Payment.Usable) > 0:
		you = append(you, c.T("election.stand_how", map[string]any{"deposit": FormatMoney(c, v.Deposit)}), c.paymentNote(*v.Payment))
		c.paymentButtons(kb, *v.Payment, func(m string) []string {
			return []string{AddrElectionStand, strconv.FormatInt(v.No, 10), m, v.Nonce}
		})
	case v.CanStand && v.Payment != nil:
		you = append(you, c.T("payment.cannot_afford", nil), c.paymentNote(*v.Payment))
	case v.CanStand:
		if btn, ok := keyboards.Button(c.T("election.button.stand", nil), AddrElectionStand, strconv.FormatInt(v.No, 10), "", v.Nonce); ok {
			kb.Row(btn)
		}
	case v.StandBlocked != "" && v.Phase == ElectionCandidacy:
		you = append(you, c.T("election.cannot_stand."+v.StandBlocked, map[string]any{"level": FormatNumber(c, int64(v.MinLevel))}))
	}
	switch {
	case c.Shared:
	case v.Voted:
		you = append(you, c.T("election.you_voted", nil))
	case v.CanVote:
		you = append(you, c.T("election.vote_how", nil))
	case v.VoteBlocked != "" && v.Phase == ElectionVoting:
		you = append(you, c.T("election.cannot_vote."+v.VoteBlocked, nil))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrElections, RefreshData: keyboards.Data(AddrElection, strconv.FormatInt(v.No, 10))}))
	return c.respond(paragraphs(
		c.T("election.detail_title", map[string]any{"no": FormatNumber(c, v.No), "election": c.electionTitle(v.Office, v.Place)}),
		body(facts...), body(cands...), body(you...),
	), kb.Build())
}

// StoodView is a candidacy registered.
type StoodView struct {
	No       int64
	Office   string
	Place    GovPlace
	Deposit  int64
	Method   string
	VotingAt time.Time
	// VotingIn is how long until the vote opens.
	VotingIn time.Duration
}

// Stood renders a candidacy registered.
func Stood(c Context, v StoodView) *presenter.Response {
	return c.withView(renderStood(c, v), ScreenStood, v)
}

func renderStood(c Context, v StoodView) *presenter.Response {
	lines := []string{c.T("election.stood", map[string]any{"election": c.electionTitle(v.Office, v.Place)})}
	if v.Deposit > 0 {
		lines = append(lines, c.T("election.stood_deposit", map[string]any{"deposit": FormatMoney(c, v.Deposit)}), c.paidLine(v.Method))
	}
	lines = append(lines, electionWhen(c, "election.voting_from", "election.voting_in", v.VotingAt, v.VotingIn))
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("election.button.back", nil), AddrElection, strconv.FormatInt(v.No, 10)); ok {
		kb.Row(btn)
	}
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// VotedView is a vote cast.
type VotedView struct {
	No        int64
	Office    string
	Place     GovPlace
	Candidate GovPlayer
	CountAt   time.Time
	// CountIn is how long until the count.
	CountIn time.Duration
}

// Voted renders a vote cast. It is private: whom a player voted for is
// theirs alone.
func Voted(c Context, v VotedView) *presenter.Response {
	return c.withView(renderVoted(c, v), ScreenVoted, v)
}

func renderVoted(c Context, v VotedView) *presenter.Response {
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("election.button.back", nil), AddrElection, strconv.FormatInt(v.No, 10)); ok {
		kb.Row(btn)
	}
	return c.respond(body(
		c.T("election.voted", map[string]any{"election": c.electionTitle(v.Office, v.Place), "player": c.govPlayer(&v.Candidate)}),
		c.T("election.secret", nil),
		electionWhen(c, "election.count_at", "election.count_in", v.CountAt, v.CountIn),
	), kb.Build()).MarkPrivate()
}

// Election refusal kinds, beside the domain's reasons (not_resident,
// too_new, level, record, jailed, standing, voted, incompatible).
const (
	ElectionRefusedNone        = "none"
	ElectionRefusedNotStanding = "not_candidacy"
	ElectionRefusedNotVoting   = "not_voting"
	ElectionRefusedAway        = "away"
	ElectionRefusedNoCandidate = "no_candidate"
)

// ElectionRefusalView is a refused election request.
type ElectionRefusalView struct {
	Kind   string
	No     int64
	Office string
	Place  GovPlace
}

// ElectionRefusal renders a refused election request.
func ElectionRefusal(c Context, v ElectionRefusalView) *presenter.Response {
	return c.withView(renderElectionRefusal(c, v), ScreenElectionRefusal, v)
}

func renderElectionRefusal(c Context, v ElectionRefusalView) *presenter.Response {
	kb := keyboards.New()
	if v.No > 0 {
		if btn, ok := keyboards.Button(c.T("election.button.back", nil), AddrElection, strconv.FormatInt(v.No, 10)); ok {
			kb.Row(btn)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrElections}))
	election := c.T("election.this", nil)
	if v.Office != "" {
		election = c.electionTitle(v.Office, v.Place)
	}
	return c.respond(c.T("election.refused."+v.Kind, map[string]any{"election": election}), kb.Build())
}

// ElectionResultView is a candidate's own result, as a notice.
type ElectionResultView struct {
	No              int64
	Office          string
	Place           GovPlace
	Elected         bool
	Votes, Cast     int64
	Deposit         int64
	DepositReturned bool
}

// ElectionResultNotice tells a candidate how their election went.
func ElectionResultNotice(c Context, v ElectionResultView) *presenter.Response {
	return c.withView(renderElectionResultNotice(c, v), ScreenElectionResultNotice, v)
}

func renderElectionResultNotice(c Context, v ElectionResultView) *presenter.Response {
	args := map[string]any{"election": c.electionTitle(v.Office, v.Place), "votes": FormatNumber(c, v.Votes),
		"cast": FormatNumber(c, v.Cast), "deposit": FormatMoney(c, v.Deposit)}
	key := "election.notice.lost"
	if v.Elected {
		key = "election.notice.won"
	}
	lines := []string{c.T(key, args)}
	if v.Deposit > 0 {
		if v.DepositReturned {
			lines = append(lines, c.T("election.notice.deposit_back", args))
		} else {
			lines = append(lines, c.T("election.notice.deposit_kept", args))
		}
	}
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("election.button.results", nil), AddrElection, strconv.FormatInt(v.No, 10)); ok {
		kb.Row(btn)
	}
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// ElectionOpenedAnnouncement is a city group's line when an election opens.
func ElectionOpenedAnnouncement(c Context, office string, place GovPlace, no int64, candidacy time.Duration) string {
	return c.T("announce.election_opened", map[string]any{"election": c.electionTitle(office, place),
		"no": FormatNumber(c, no), "duration": FormatSpan(c, candidacy)})
}

// ElectionVotingAnnouncement is a city group's line when the vote opens:
// how many stand and how long the vote lasts, or that nobody stood.
func ElectionVotingAnnouncement(c Context, office string, place GovPlace, candidates int, voting time.Duration) string {
	args := map[string]any{"election": c.electionTitle(office, place), "count": FormatNumber(c, int64(candidates)),
		"duration": FormatSpan(c, voting)}
	if candidates == 0 {
		return c.T("announce.election_voting_none", args)
	}
	return c.T("announce.election_voting", args)
}

// ElectionStoodAnnouncement is a city group's line when a player stands.
func ElectionStoodAnnouncement(c Context, player, office string, place GovPlace) string {
	return c.T("announce.election_stood", map[string]any{"player": c.playerName(player), "election": c.electionTitle(office, place)})
}

// ElectionCountedAnnouncement is a city group's line when an election is
// counted: who won, or that nobody did — because nobody stood, or because
// no candidate won a vote — and the office stays vacant.
func ElectionCountedAnnouncement(c Context, office string, place GovPlace, elected []GovPlayer, cast int64, candidates int) string {
	args := map[string]any{"election": c.electionTitle(office, place), "elected": c.govPlayers(elected), "cast": FormatNumber(c, cast)}
	switch {
	case candidates == 0:
		return c.T("announce.election_no_candidates", args)
	case len(elected) == 0:
		return c.T("announce.election_unfilled", args)
	case len(elected) > 1:
		return c.T("announce.election_counted_many", args)
	}
	return c.T("announce.election_counted", args)
}
