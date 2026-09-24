// Package election holds the rules of electing a player to an office: when
// an election takes candidates and when it takes votes, who may stand and who
// may vote, and how the count fills the seats.
//
// Which offices are elected, for how long and on what terms is CONTENT
// (configs/content/governance.yml); the seats and who holds them are the
// governance package's. The package reads no clock and no file.
//
// # Real time
//
// Every duration here is REAL time, never waited through the game clock
// (docs/adr/0018-game-clock.md). An election is a governance guarantee in
// the sense that ADR gives a term, a notice and a cooldown: the days a
// resident has to stand or to vote, and the years of residence a candidate
// needs, are promises made on the player's own calendar, and a change to
// game.time_scale must never shorten them. The term the next election is
// timed from is real time too, so one calendar runs through the whole cycle.
//
// # Ties
//
// The count ranks by votes; a tie goes to the EARLIER candidacy (who stood
// first), then to the lower player id, so the order never depends on how
// storage happened to return the rows. A candidate with no vote is never
// elected, so a seat nobody voted for stays vacant.
package election

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

// Failures.
var (
	// ErrInvalidRules means an office's election terms are unusable.
	ErrInvalidRules = errors.New("election: invalid rules")
	// ErrNotInPhase means the election is not taking what was asked of it:
	// a candidacy outside the candidacy period, a vote outside the vote.
	ErrNotInPhase = errors.New("election: not now")
	// ErrNotEligible means the player may not stand or vote; Why says why.
	ErrNotEligible = errors.New("election: not eligible")
)

// Rules are one office's election terms, as content states them.
type Rules struct {
	// Candidacy and Voting are how long candidates may stand and then
	// residents may vote.
	Candidacy, Voting time.Duration
	// MinLevel and MinResidency are what a candidate needs: a character
	// level, and how long they have lived in the jurisdiction.
	MinLevel     int
	MinResidency time.Duration
	// CleanRecord means a candidate may owe nothing to victims or the city
	// and may not be serving a sentence.
	CleanRecord bool
	// Deposit is what a candidate pays to stand; RefundShareBPS the share of
	// the votes cast that earns it back. Below it the deposit is forfeit to
	// the jurisdiction's treasury.
	Deposit        money.Amount
	RefundShareBPS int
	// VoterMinResidency is how long a resident must have lived there to
	// vote.
	VoterMinResidency time.Duration
	// ReopenAfter is how long after a count that filled no seat the next
	// election opens.
	ReopenAfter time.Duration
}

// Validate checks the rules.
func (r Rules) Validate() error {
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: %s", ErrInvalidRules, fmt.Sprintf(format, args...)))
	}
	if r.Candidacy <= 0 || r.Voting <= 0 {
		bad("candidacy and voting must both last")
	}
	if r.ReopenAfter <= 0 {
		bad("reopen_after must be set")
	}
	if r.MinLevel < 0 || r.MinResidency < 0 || r.VoterMinResidency < 0 {
		bad("requirements cannot be negative")
	}
	if r.Deposit.Minor() < 0 {
		bad("a deposit cannot be negative")
	}
	if r.RefundShareBPS < 0 || r.RefundShareBPS > 10_000 {
		bad("refund share %d is outside 0..10000", r.RefundShareBPS)
	}
	return errors.Join(errs...)
}

// Phase is where an election stands.
type Phase string

const (
	// Candidacy takes candidates.
	Candidacy Phase = "candidacy"
	// Voting takes votes.
	Voting Phase = "voting"
	// Counting is after the vote and before the count ran.
	Counting Phase = "counting"
)

// Schedule is an election's calendar in real time.
type Schedule struct {
	OpensAt, CandidacyEndsAt, VotingEndsAt time.Time
}

// Plan lays an election's calendar from its opening.
func Plan(r Rules, opensAt time.Time) Schedule {
	c := opensAt.Add(r.Candidacy)
	return Schedule{OpensAt: opensAt, CandidacyEndsAt: c, VotingEndsAt: c.Add(r.Voting)}
}

// NextOpening is when the office's next election opens after a count at
// countedAt. When the count filled a seat, the next one is timed so that ITS
// count falls when the new term ends — the holder serves the whole term and
// is vacated at its end by the next count; a term shorter than the calendar
// opens the next election at once. When it filled none, the next opens
// ReopenAfter later. An office held at pleasure (term 0) opens none.
func NextOpening(r Rules, term time.Duration, countedAt time.Time, filled bool) (time.Time, bool) {
	if term <= 0 {
		return time.Time{}, false
	}
	if !filled {
		return countedAt.Add(r.ReopenAfter), true
	}
	lead := term - r.Candidacy - r.Voting
	if lead < 0 {
		lead = 0
	}
	return countedAt.Add(lead), true
}

// At is the phase at now.
func (s Schedule) At(now time.Time) Phase {
	switch {
	case now.Before(s.CandidacyEndsAt):
		return Candidacy
	case now.Before(s.VotingEndsAt):
		return Voting
	}
	return Counting
}

// Why a player may not stand or vote.
const (
	WhyNotResident  = "not_resident"
	WhyTooNew       = "too_new"
	WhyLevel        = "level"
	WhyRecord       = "record"
	WhyJailed       = "jailed"
	WhyStanding     = "standing"
	WhyVoted        = "voted"
	WhyIncompatible = "incompatible"
)

// Refusal is a player not allowed to stand or vote.
type Refusal struct {
	Why string
}

func (r Refusal) Error() string { return "election: not eligible: " + r.Why }

// Unwrap lets errors.Is match ErrNotEligible.
func (r Refusal) Unwrap() error { return ErrNotEligible }

// Person is what eligibility looks at of one player.
type Person struct {
	// Resident means the player lives in the jurisdiction.
	Resident bool
	// ResidentFor is how long they have lived there.
	ResidentFor time.Duration
	Level       int
	// Owes means unpaid restitution or fines; Jailed a sentence being
	// served.
	Owes, Jailed bool
}

// CanStand checks a candidate against the rules.
func CanStand(r Rules, p Person) error {
	switch {
	case !p.Resident:
		return Refusal{Why: WhyNotResident}
	case p.ResidentFor < r.MinResidency:
		return Refusal{Why: WhyTooNew}
	case p.Level < r.MinLevel:
		return Refusal{Why: WhyLevel}
	case r.CleanRecord && p.Jailed:
		return Refusal{Why: WhyJailed}
	case r.CleanRecord && p.Owes:
		return Refusal{Why: WhyRecord}
	}
	return nil
}

// CanVote checks a voter against the rules: a resident of long enough
// standing.
func CanVote(r Rules, p Person) error {
	switch {
	case !p.Resident:
		return Refusal{Why: WhyNotResident}
	case p.ResidentFor < r.VoterMinResidency:
		return Refusal{Why: WhyTooNew}
	}
	return nil
}

// Candidate is one candidate at the count.
type Candidate struct {
	PlayerID string
	// StoodAt is when they stood: the earlier candidacy wins a tie.
	StoodAt time.Time
	Votes   int64
}

// Result is a count.
type Result struct {
	// Ranked is every candidate, most votes first; ties go to the earlier
	// candidacy, then the lower player id, so the order never depends on
	// storage.
	Ranked []Candidate
	// Elected are the first Seats of Ranked with at least one vote.
	Elected []Candidate
	// Cast is every vote counted.
	Cast int64
}

// Count ranks the candidates and fills the seats. A candidate with no vote
// is never elected: an unopposed candidate still needs one voter.
func Count(candidates []Candidate, seats int) Result {
	ranked := append([]Candidate(nil), candidates...)
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.Votes != b.Votes {
			return a.Votes > b.Votes
		}
		if !a.StoodAt.Equal(b.StoodAt) {
			return a.StoodAt.Before(b.StoodAt)
		}
		return a.PlayerID < b.PlayerID
	})
	res := Result{Ranked: ranked}
	for _, c := range ranked {
		res.Cast += c.Votes
		if len(res.Elected) < seats && c.Votes > 0 {
			res.Elected = append(res.Elected, c)
		}
	}
	return res
}

// Refunded reports whether a candidate's share of the votes earns the
// deposit back: votes × 10000 ≥ cast × RefundShareBPS. With no votes cast at
// all, every deposit comes back — nobody was rejected.
func Refunded(r Rules, votes, cast int64) bool {
	if cast <= 0 {
		return true
	}
	return votes*10_000 >= cast*int64(r.RefundShareBPS)
}
