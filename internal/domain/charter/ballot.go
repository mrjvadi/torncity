package charter

import (
	"sort"
	"time"
)

// Phase 2 of the charter (docs/adr/0044 6.5, 6.6; research note
// charter-phase2-elections-recall-amendments-interim, 2026-10-05): elected offices,
// recall, amendments and the acting head. Everything here is pure: the handlers
// read the rows, ask these functions and write what they answer.
//
// Real basis (the research note): plurality is the simplest rule that suits a small
// electorate; recall needs a signature threshold WITH a floor for tiny towns, a
// minimum tenure and a cool-down against petition spam (Peru, British Columbia); an
// amendment of the structural rules takes a vote of the members, not of the office
// holders (Germany Article 79, Denmark, Australia); a vacancy is covered by a named
// acting officer on a clock with limited powers whose acts outside the rule are void
// (US Vacancies Reform Act, caretaker governments).

// BallotKind is what a ballot decides.
type BallotKind string

const (
	// BallotElection fills the seats of an elected office (or the head office).
	BallotElection BallotKind = "election"
	// BallotRecall asks whether one holder keeps their seat.
	BallotRecall BallotKind = "recall"
	// BallotAmendment asks the residents to approve a structural change.
	BallotAmendment BallotKind = "amendment"
)

// BallotStatus is where a ballot stands.
type BallotStatus string

const (
	BallotOpen      BallotStatus = "open"
	BallotPassed    BallotStatus = "passed"    // an election filled seats, a recall removed, an amendment applied
	BallotFailed    BallotStatus = "failed"    // a recall or amendment did not carry
	BallotNoResult  BallotStatus = "no_result" // an election drew no vote
	BallotCancelled BallotStatus = "cancelled" // the target went away before the end
	BallotVoid      BallotStatus = "void"      // carried, but no longer allowed when it came to apply it
)

// Settings are the numbers phase 2 runs on (config settlement.charter_*).
type Settings struct {
	// ElectionTermDays is the term of an elected seat (14).
	ElectionTermDays int
	// CandidacyHours and VotingHours are the two windows of an election.
	CandidacyHours, VotingHours int
	// RecallMinTenureDays is how long a holder must have served before a petition
	// may start (5); RecallSignatureBPS the share of eligible residents that must
	// sign (2000 = 20 percent); RecallMinSignatures the floor for a tiny town;
	// RecallVoteHours the length of the vote; RecallCooldownDays how long a holder
	// who was recalled, or survived a recall, is left alone and cannot be
	// re-appointed to the office.
	RecallMinTenureDays, RecallSignatureBPS, RecallMinSignatures, RecallVoteHours, RecallCooldownDays int
	// AmendVoteHours and AmendQuorumBPS: the length of an amendment vote and the
	// share of eligible residents that must vote (with the same floor as recall);
	// AmendVoteMinResidents the size from which a structural change needs the vote
	// at all (a settlement of two people has nobody to ask).
	AmendVoteHours, AmendQuorumBPS, AmendVoteMinResidents int
	// ActingDays is how long an acting head may act (7); ActingSpendCap the most
	// one spend of the acting head may be, minor units; MinResidencyDays how long a
	// resident must have lived there to vote or stand (3).
	ActingDays, MinResidencyDays int
	ActingSpendCap               int64
}

// Defaults are the shipped numbers; config overrides them.
func Defaults() Settings {
	return Settings{
		ElectionTermDays: 14, CandidacyHours: 48, VotingHours: 72,
		RecallMinTenureDays: 5, RecallSignatureBPS: 2000, RecallMinSignatures: 3, RecallVoteHours: 72, RecallCooldownDays: 14,
		AmendVoteHours: 72, AmendQuorumBPS: 3000, AmendVoteMinResidents: 6,
		ActingDays: 7, ActingSpendCap: 2000, MinResidencyDays: 3,
	}
}

// ElectionPhase is where an election is on its calendar.
type ElectionPhase string

const (
	PhaseCandidacy ElectionPhase = "candidacy"
	PhaseVoting    ElectionPhase = "voting"
	PhaseClosed    ElectionPhase = "closed"
)

// PhaseAt is the phase of an election opened at `opens` at the instant now.
func (s Settings) PhaseAt(opens, now time.Time) ElectionPhase {
	cand := opens.Add(time.Duration(s.CandidacyHours) * time.Hour)
	switch {
	case now.Before(cand):
		return PhaseCandidacy
	case now.Before(cand.Add(time.Duration(s.VotingHours) * time.Hour)):
		return PhaseVoting
	}
	return PhaseClosed
}

// ElectionCloses is when the voting of an election opened at `opens` ends.
func (s Settings) ElectionCloses(opens time.Time) time.Time {
	return opens.Add(time.Duration(s.CandidacyHours+s.VotingHours) * time.Hour)
}

// Floor is the least a threshold may be for an electorate of the given size: the
// floor, but never more than everyone (a recall of 15 percent of six people is one
// person; with a floor of three it is three).
func Floor(eligible, floor int) int {
	if floor > eligible {
		return eligible
	}
	return floor
}

// RecallSignatures is how many signatures put a holder to a recall vote: the share
// of the eligible residents, rounded up, at least the floor.
func (s Settings) RecallSignatures(eligible int) int {
	need := (eligible*s.RecallSignatureBPS + 9_999) / 10_000
	return max(need, Floor(eligible, s.RecallMinSignatures), 1)
}

// RecallTenureOK reports that a holder has served long enough to be petitioned.
func (s Settings) RecallTenureOK(since, now time.Time) bool {
	return !now.Before(since.Add(time.Duration(s.RecallMinTenureDays) * 24 * time.Hour))
}

// RecallCoolingUntil is when a holder may be petitioned again after a recall vote
// ended at `ended`.
func (s Settings) RecallCoolingUntil(ended time.Time) time.Time {
	return ended.Add(time.Duration(s.RecallCooldownDays) * 24 * time.Hour)
}

// Carries decides a yes-or-no ballot: more yes than no, with at least `need` votes
// cast (the quorum). Votes cast are yes plus no.
func Carries(yes, no int64, need int) bool {
	return yes > no && yes+no >= int64(need)
}

// QuorumVotes is how many votes an amendment needs to count: the share of the
// eligible residents, rounded up, at least the floor.
func (s Settings) QuorumVotes(eligible int) int {
	need := (eligible*s.AmendQuorumBPS + 9_999) / 10_000
	return max(need, Floor(eligible, s.RecallMinSignatures), 1)
}

// NeedsVote reports whether a structural change must go to the residents in a
// settlement of this size.
func (s Settings) NeedsVote(residents int) bool { return residents >= s.AmendVoteMinResidents }

// Key permissions are the ones that decide who rules: changing which office holds
// one of them, or a spending ceiling, is a structural change.
var keyPermissions = map[Permission]bool{
	OfficeCreate: true, OfficeEdit: true, OfficeAppoint: true, OfficeDismiss: true,
	CharterAmend: true, ElectionCall: true, TreasurySpend: true, SettingsTimezone: true,
}

// IsKey reports a key permission.
func IsKey(p Permission) bool { return keyPermissions[p] }

// Structural reports whether turning office `prev` into `next` is a change the
// residents must approve: it adds, removes or re-limits a key permission, switches
// how the office is filled (so an elected office cannot be turned into an appointed
// one by its own appointers) or sets who acts for the head. A rename, a seat count
// or a non-key permission is not.
func Structural(prev, next Office) bool {
	if prev.Acquisition != next.Acquisition || prev.Deputy != next.Deputy {
		return true
	}
	a, b := keyGrants(prev.Grants), keyGrants(next.Grants)
	if len(a) != len(b) {
		return true
	}
	for p, l := range a {
		if lb, ok := b[p]; !ok || lb != l {
			return true
		}
	}
	return false
}

func keyGrants(gs []Grant) map[Permission]int64 {
	out := map[Permission]int64{}
	for _, g := range gs {
		if keyPermissions[g.Permission] {
			out[g.Permission] = g.Limit
		}
	}
	return out
}

// ClosingNeedsVote reports whether closing an office must go to the residents: an
// elected office, because the residents chose its holders.
func ClosingNeedsVote(o Office) bool { return o.Acquisition == AcquireElection }

// ActingGrants are what an acting head may do: the founder's powers less the ones
// that change the charter or bind the settlement abroad, and with every spend
// capped. Anything not in this list is void while acting.
func ActingGrants(founder []Grant, spendCap int64) []Grant {
	forbidden := map[Permission]bool{
		CharterAmend: true, OfficeCreate: true, OfficeEdit: true, OfficeAppoint: true, OfficeDismiss: true,
		TreatyPropose: true, UnionPropose: true, RaidDeclare: true, SettingsTimezone: true, ElectionCall: true,
	}
	out := make([]Grant, 0, len(founder))
	for _, g := range founder {
		if forbidden[g.Permission] {
			continue
		}
		if g.Permission == TreasurySpend {
			g.Limit = capLimit(g.Limit, spendCap)
		}
		out = append(out, g)
	}
	return out
}

func capLimit(have, cap int64) int64 {
	if cap <= 0 {
		return have
	}
	if have == 0 || have > cap {
		return cap
	}
	return have
}

// SeatInfo is a holder of a seat, for choosing the acting head.
type SeatInfo struct {
	PlayerID string
	OfficeID string
	Since    time.Time
}

// ActingHead chooses who acts while the head seat is vacant: the longest-serving
// holder of the deputy office, else the longest-serving holder of any office. Ties
// go to the earlier player id so every replica agrees. The bool is false when
// nobody can act.
func ActingHead(seats []SeatInfo, deputyOffice string) (SeatInfo, bool) {
	pick := func(only string) (SeatInfo, bool) {
		var cand []SeatInfo
		for _, s := range seats {
			if only == "" || s.OfficeID == only {
				cand = append(cand, s)
			}
		}
		if len(cand) == 0 {
			return SeatInfo{}, false
		}
		sort.Slice(cand, func(i, j int) bool {
			if !cand[i].Since.Equal(cand[j].Since) {
				return cand[i].Since.Before(cand[j].Since)
			}
			return cand[i].PlayerID < cand[j].PlayerID
		})
		return cand[0], true
	}
	if deputyOffice != "" {
		if s, ok := pick(deputyOffice); ok {
			return s, true
		}
	}
	return pick("")
}

// ActingEnds is when an acting head's authority runs out: the vacancy plus the
// acting days.
func (s Settings) ActingEnds(vacantSince time.Time) time.Time {
	return vacantSince.Add(time.Duration(s.ActingDays) * 24 * time.Hour)
}
