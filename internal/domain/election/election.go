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

// Rules are one office's election terms — its ELECTION LAW, decided by the
// legislature (city council for city offices, parliament for national ones)
// like any other policy lever, with content's defaults and safety bounds
// (docs/adr/0015-player-held-offices.md). Never read from content directly:
// see application.ResolveElectionLaw.
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
	// Endorsements is how many residents must endorse a candidacy during the
	// candidacy window, one endorsement per resident per office per
	// election. Zero means none are needed.
	Endorsements int
	// TermLimitConsecutive is the most consecutive terms one player may
	// serve in the office before standing again is refused; zero means no
	// limit. TermLimitTotal is the most terms ever, consecutive or not.
	TermLimitConsecutive, TermLimitTotal int
	// EducationRank is the least rank a candidate's best certificate, among
	// the office's education_options, must reach; zero means none is
	// required. Rank 0 is always "none" (nobody is refused for holding no
	// certificate when this is zero).
	EducationRank int
	// MinAge is the least character age (G1, docs/adr/0025-life-and-legacy.md)
	// a candidate must have reached; zero means no requirement.
	MinAge int
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
	if r.Endorsements < 0 || r.TermLimitConsecutive < 0 || r.TermLimitTotal < 0 || r.EducationRank < 0 || r.MinAge < 0 {
		bad("election law fields cannot be negative")
	}
	if r.TermLimitConsecutive > 0 && r.TermLimitTotal > 0 && r.TermLimitConsecutive > r.TermLimitTotal {
		bad("a consecutive term limit above the total term limit could never bind")
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
	// WhyEndorsements means the candidacy has not gathered enough
	// endorsements yet.
	WhyEndorsements = "endorsements"
	// WhyTermLimit means the player has already served as many consecutive
	// or total terms as the law allows.
	WhyTermLimit = "term_limit"
	// WhyEducation means the candidate holds no certificate of the rank the
	// office requires.
	WhyEducation = "education"
	// WhyAge means the candidate has not reached the office's minimum age.
	WhyAge = "age"
	// WhyNotApproved means the election commission has not (yet) approved
	// the candidacy.
	WhyNotApproved = "not_approved"
	// WhyDisqualified means the election commission disqualified the
	// candidacy.
	WhyDisqualified = "disqualified"
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
	// Endorsements is how many residents have endorsed this candidacy so
	// far, gathered during the candidacy window.
	Endorsements int
	// ConsecutiveTerms is how many terms, immediately before this election,
	// the player has held the office back to back. TotalTerms is how many
	// terms they have ever held it.
	ConsecutiveTerms, TotalTerms int
	// EducationRank is the highest rank, among the office's education
	// options, of a certificate the player holds. Zero means none.
	EducationRank int
	// Age is the player's character age (0 when the pack has no life/age
	// content, or the character's life is not yet known).
	Age int
}

// CanStand checks a candidate against the rules. It does not check
// incompatible offices or the election commission's vetting, which need a
// database read; the handler checks those apart (WhyIncompatible,
// WhyNotApproved, WhyDisqualified).
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
	case r.MinAge > 0 && p.Age < r.MinAge:
		return Refusal{Why: WhyAge}
	case r.EducationRank > 0 && p.EducationRank < r.EducationRank:
		return Refusal{Why: WhyEducation}
	case r.TermLimitConsecutive > 0 && p.ConsecutiveTerms >= r.TermLimitConsecutive:
		return Refusal{Why: WhyTermLimit}
	case r.TermLimitTotal > 0 && p.TotalTerms >= r.TermLimitTotal:
		return Refusal{Why: WhyTermLimit}
	case r.Endorsements > 0 && p.Endorsements < r.Endorsements:
		return Refusal{Why: WhyEndorsements}
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

// Election law fields: the keys of the document an election_law policy
// lever holds (application.LeverDefinition.DefaultElectionLaw,
// application.PolicySetting.ElectionLaw). One office's whole election law is
// one such document; the legislature amends it one field at a time, in this
// order. Every value is a whole number: hours for a duration, minor units
// for the deposit, 0/1 for a yes/no, an index into the office's
// education_options for the certificate required, 0 for "none needed".
const (
	FieldCandidacyHours         = "candidacy_hours"
	FieldVotingHours            = "voting_hours"
	FieldMinLevel               = "min_level"
	FieldMinResidencyHours      = "min_residency_hours"
	FieldCleanRecord            = "clean_record"
	FieldVoterMinResidencyHours = "voter_min_residency_hours"
	FieldDeposit                = "deposit"
	FieldRefundShareBPS         = "refund_share_bps"
	FieldReopenAfterHours       = "reopen_after_hours"
	FieldEndorsementsRequired   = "endorsements_required"
	FieldTermLimitConsecutive   = "term_limit_consecutive"
	FieldTermLimitTotal         = "term_limit_total"
	FieldEducationRank          = "education_rank"
	FieldMinAge                 = "min_age"
)

// Fields lists every field of an election law document, in the order the
// legislature amends and the screens show them. A document is valid only
// with exactly these keys (ValidateFields).
var Fields = []string{
	FieldCandidacyHours, FieldVotingHours, FieldMinLevel, FieldMinResidencyHours, FieldCleanRecord,
	FieldVoterMinResidencyHours, FieldDeposit, FieldRefundShareBPS, FieldReopenAfterHours,
	FieldEndorsementsRequired, FieldTermLimitConsecutive, FieldTermLimitTotal, FieldEducationRank, FieldMinAge,
}

// ErrInvalidFields means an election law document is missing a field, has
// one it should not, or cannot be turned into usable rules.
var ErrInvalidFields = errors.New("election: invalid election law fields")

// ValidateFields checks that doc has exactly the keys Fields lists, and that
// the rules they make are usable (RulesFromFields(doc).Validate()).
func ValidateFields(doc map[string]int64) error {
	if len(doc) != len(Fields) {
		return fmt.Errorf("%w: has %d fields, want %d", ErrInvalidFields, len(doc), len(Fields))
	}
	for _, f := range Fields {
		if _, ok := doc[f]; !ok {
			return fmt.Errorf("%w: missing %q", ErrInvalidFields, f)
		}
	}
	return RulesFromFields(doc).Validate()
}

// RulesFromFields turns an election law document into Rules: hours become
// durations, and the deposit minor units become money.
func RulesFromFields(doc map[string]int64) Rules {
	return Rules{
		Candidacy:            time.Duration(doc[FieldCandidacyHours]) * time.Hour,
		Voting:               time.Duration(doc[FieldVotingHours]) * time.Hour,
		MinLevel:             int(doc[FieldMinLevel]),
		MinResidency:         time.Duration(doc[FieldMinResidencyHours]) * time.Hour,
		CleanRecord:          doc[FieldCleanRecord] != 0,
		VoterMinResidency:    time.Duration(doc[FieldVoterMinResidencyHours]) * time.Hour,
		Deposit:              money.FromMinor(doc[FieldDeposit]),
		RefundShareBPS:       int(doc[FieldRefundShareBPS]),
		ReopenAfter:          time.Duration(doc[FieldReopenAfterHours]) * time.Hour,
		Endorsements:         int(doc[FieldEndorsementsRequired]),
		TermLimitConsecutive: int(doc[FieldTermLimitConsecutive]),
		TermLimitTotal:       int(doc[FieldTermLimitTotal]),
		EducationRank:        int(doc[FieldEducationRank]),
		MinAge:               int(doc[FieldMinAge]),
	}
}

// FieldsFromRules is the inverse of RulesFromFields: a full election law
// document from Rules, for building a lever's default from content.
func FieldsFromRules(r Rules) map[string]int64 {
	return map[string]int64{
		FieldCandidacyHours:         int64(r.Candidacy / time.Hour),
		FieldVotingHours:            int64(r.Voting / time.Hour),
		FieldMinLevel:               int64(r.MinLevel),
		FieldMinResidencyHours:      int64(r.MinResidency / time.Hour),
		FieldCleanRecord:            boolInt(r.CleanRecord),
		FieldVoterMinResidencyHours: int64(r.VoterMinResidency / time.Hour),
		FieldDeposit:                r.Deposit.Minor(),
		FieldRefundShareBPS:         int64(r.RefundShareBPS),
		FieldReopenAfterHours:       int64(r.ReopenAfter / time.Hour),
		FieldEndorsementsRequired:   int64(r.Endorsements),
		FieldTermLimitConsecutive:   int64(r.TermLimitConsecutive),
		FieldTermLimitTotal:         int64(r.TermLimitTotal),
		FieldEducationRank:          int64(r.EducationRank),
		FieldMinAge:                 int64(r.MinAge),
	}
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
