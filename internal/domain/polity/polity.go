// Package polity is the pure rules of unions and countries (docs/adr/0076): what a member may delegate, the ceiling a union office
// meets, how a proposal is ratified, how the members vote, what dues cost, how a member leaves and what it takes with it. No clock, no
// storage, no randomness: every function is given what it needs.
package polity

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/charter"
)

// BPS is the scale of basis points.
const BPS = 10_000

// Sentinel errors.
var (
	// ErrCeiling means a union office was given a permission whose class the members never delegated (rail D1).
	ErrCeiling = errors.New("polity: the permission's class is not delegated to the union")
	// ErrUnknownClass means a delegation outside the closed set.
	ErrUnknownClass = errors.New("polity: unknown delegation class")
	// ErrKind means a union kind that is neither a league nor a country.
	ErrKind = errors.New("polity: unknown union kind")
	// ErrTooSmall means a country charter below the minimum of combined residents.
	ErrTooSmall = errors.New("polity: too few residents for a country")
	// ErrNotCountry means a country charter that lacks the delegations of a country.
	ErrNotCountry = errors.New("polity: a country needs the delegations of a country")
)

// Class is a class of authority a member hands to a union (docs/adr/0076 4.3).
type Class string

// The classes. The set is closed and lives in code: each is enforced where the permissions of the class are asked.
const (
	Dues     Class = "dues"
	Treaty   Class = "treaty"
	Defence  Class = "defence"
	Courts   Class = "courts"
	Currency Class = "currency"
	Customs  Class = "customs"
	War      Class = "war"
	Roads    Class = "roads"
	Claims   Class = "claims"
)

var classes = []Class{Dues, Treaty, Defence, Courts, Currency, Customs, War, Roads, Claims}

// Classes lists the closed set, in a fixed order.
func Classes() []Class { return append([]Class(nil), classes...) }

// Valid reports whether c is one of the closed set.
func (c Class) Valid() bool {
	for _, x := range classes {
		if x == c {
			return true
		}
	}
	return false
}

// Normalise checks a list of classes, drops repeats and sorts it.
func Normalise(in []Class) ([]Class, error) {
	seen := map[Class]bool{}
	var out []Class
	for _, c := range in {
		if !c.Valid() {
			return nil, fmt.Errorf("%w: %q", ErrUnknownClass, string(c))
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// classOf is the class a permission belongs to. A permission that is in no class (the office and charter powers, the treasury, notices,
// the union's own acts, tenders and loans) is the union's own and needs no delegation.
var classOf = map[charter.Permission]Class{
	charter.TreatyPropose: Treaty, charter.TreatySign: Treaty, charter.TreatyEnd: Treaty, charter.SanctionImpose: Treaty,
	charter.WarDeclare: War, charter.WarPeace: War, charter.RaidDeclare: War,
	charter.MilitaryCommand: Defence, charter.MilitaryProcure: Defence, charter.MilitaryLicence: Defence,
	charter.CourtJudge: Courts, charter.PolicePatrol: Courts, charter.PoliceFine: Courts,
	charter.PoliceInvestigate: Courts, charter.PoliceArrest: Courts, charter.PoliceWarrantAsk: Courts, charter.PoliceInformantRun: Courts,
	charter.ProsecuteCharge: Courts, charter.ProsecuteDrop: Courts, charter.DefenceAppear: Courts, charter.CourtWarrant: Courts, charter.CourtAppealHear: Courts,
	charter.JailRun: Courts, charter.Pardon: Courts, charter.AuditIntegrity: Courts, charter.AuditBooks: Courts, charter.LawSet: Courts,
	charter.CustomsInspect:  Customs,
	charter.CurrencyCharter: Currency, charter.CurrencyIssue: Currency, charter.BankPolicy: Currency,
	charter.BorderClose: Customs, charter.CustomsSet: Customs,
	charter.RoadDraw: Roads, charter.RailDraw: Roads, charter.HaulOrder: Roads,
	charter.LandClaim: Claims,
}

// ClassOf is the class of a permission; false for a permission of no class.
func ClassOf(p charter.Permission) (Class, bool) { c, ok := classOf[p]; return c, ok }

// Ceiling is rail D1: a union office may hold a permission only if its class is among the delegations. The first permission that breaks
// it is named in the error.
func Ceiling(delegations []Class, grants []charter.Grant) error {
	have := map[Class]bool{}
	for _, c := range delegations {
		have[c] = true
	}
	for _, g := range grants {
		if c, ok := classOf[g.Permission]; ok && !have[c] {
			return fmt.Errorf("%w: %s needs %s", ErrCeiling, g.Permission, c)
		}
	}
	return nil
}

// Union kinds.
const (
	KindLeague  = "league"
	KindCountry = "country"
)

// IsCountry says the delegations make the union a country: it has every class the config requires (ADR 0076 5.1).
func IsCountry(delegations, required []Class) bool {
	have := map[Class]bool{}
	for _, c := range delegations {
		have[c] = true
	}
	for _, r := range required {
		if !have[r] {
			return false
		}
	}
	return len(required) > 0
}

// CheckKind checks that the kind and the delegations agree and that a country has the residents it needs. A league of any delegations
// is allowed; a country must hold the required classes.
func CheckKind(kind string, delegations, required []Class, residents, minResidents int64) error {
	switch kind {
	case KindLeague:
		return nil
	case KindCountry:
		if !IsCountry(delegations, required) {
			return ErrNotCountry
		}
		if residents < minResidents {
			return ErrTooSmall
		}
		return nil
	}
	return ErrKind
}

// AssemblyClasses are the delegations that bind a member so deeply that the member's assembly must approve them too.
var assemblyClasses = map[Class]bool{War: true, Courts: true, Currency: true, Customs: true, Defence: true}

// NeedsAssembly reports whether a member's own assembly must approve a ratification: the proposal delegates a deep class, or its dues
// rise above the bound (ADR 0076 4.2).
func NeedsAssembly(delegations []Class, duesBPS, duesBound int64) bool {
	for _, c := range delegations {
		if assemblyClasses[c] {
			return true
		}
	}
	return duesBPS > duesBound
}

// Threshold is how many ratifications put a proposal into force: all the invited for a country, two for a league (or all of them if
// fewer were invited).
func Threshold(kind string, invited int) int {
	if kind == KindCountry || invited < 2 {
		return max(invited, 1)
	}
	return 2
}

// Phase is where a proposal stands.
type Phase string

// The phases of a proposal.
const (
	Pending  Phase = "pending"
	InForce  Phase = "in_force"
	Lapsed   Phase = "lapsed"
	Refused  Phase = "refused"
	Withdraw Phase = "withdrawn"
)

// Decide is the phase of a proposal: yes ratifications, no refusals, the threshold, the number invited, the deadline.
// A country needs every invited settlement, so one refusal ends it; a league lapses when the deadline passes below the threshold.
func Decide(kind string, invited, yes, no int, deadline, now time.Time) Phase {
	need := Threshold(kind, invited)
	switch {
	case yes >= need:
		return InForce
	case invited-no < need:
		return Refused
	case !now.Before(deadline):
		return Lapsed
	}
	return Pending
}

// Mode is how the members of a union decide between themselves (docs/adr/0044 8.4).
type Mode string

// The modes.
const (
	OneMember      Mode = "one_member"
	Weighted       Mode = "weighted"
	DoubleMajority Mode = "double_majority"
)

// Valid reports whether m is one of the three.
func (m Mode) Valid() bool { return m == OneMember || m == Weighted || m == DoubleMajority }

// Member is a member as a vote sees it: its residents at the time.
type Member struct {
	ID        string
	Residents int64
}

// Rules are the numbers of a vote.
type Rules struct {
	Mode Mode
	// WeightCapBPS is the most one member weighs, of the total residents (weighted and double majority).
	WeightCapBPS int64
	// MembersBPS and PopulationBPS are the two thresholds of the double majority.
	MembersBPS, PopulationBPS int64
}

// Tally is how a vote stands.
type Tally struct {
	YesMembers, Members int
	YesWeight, Weight   int64
	// MembersOK and PopulationOK are the two halves of the double majority (both true for the other modes when carried).
	MembersOK, PopulationOK bool
	Carried                 bool
}

// capped weighs the members: a member weighs its residents, but never more than capBPS of the total weight, so that a big member cannot
// carry a vote alone. The cap is solved on the capped total itself (the top k members are cut to L, with L = cap x the rest / (1 - k x cap));
// a cap too tight for the number of members (n x cap below the whole) leaves every member one equal weight.
func capped(members []Member, capBPS int64) (weights map[string]int64, total int64) {
	weights = map[string]int64{}
	sorted := append([]Member(nil), members...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Residents != sorted[j].Residents {
			return sorted[i].Residents > sorted[j].Residents
		}
		return sorted[i].ID < sorted[j].ID
	})
	var raw int64
	for _, m := range sorted {
		raw += max(m.Residents, 0)
	}
	limit := int64(-1) // no cap
	if capBPS > 0 && capBPS < BPS {
		if capBPS*int64(len(sorted)) < BPS {
			for _, m := range sorted {
				weights[m.ID] = 1
				total++
			}
			return weights, total
		}
		rest := raw
		for k := 0; k < len(sorted); k++ {
			denom := BPS - int64(k)*capBPS
			if denom <= 0 {
				break
			}
			l := capBPS * rest / denom
			if max(sorted[k].Residents, 0) <= l {
				if k > 0 {
					limit = l
				}
				break
			}
			rest -= max(sorted[k].Residents, 0)
		}
	}
	for _, m := range sorted {
		w := max(m.Residents, 0)
		if limit >= 0 {
			w = min(w, limit)
		}
		weights[m.ID] = w
		total += w
	}
	return weights, total
}

// Carries tallies the members who said yes (a member who did not vote has not said yes). A member with no residents still counts as a
// member and weighs nothing.
func (r Rules) Carries(members []Member, yes map[string]bool) Tally {
	t := Tally{Members: len(members)}
	weights, total := capped(members, r.WeightCapBPS)
	t.Weight = total
	for _, m := range members {
		if yes[m.ID] {
			t.YesMembers++
			t.YesWeight += weights[m.ID]
		}
	}
	if t.Members == 0 {
		return t
	}
	switch r.Mode {
	case Weighted:
		t.MembersOK, t.PopulationOK = true, t.YesWeight*2 > t.Weight
		t.Carried = t.PopulationOK
	case DoubleMajority:
		t.MembersOK = int64(t.YesMembers)*BPS >= r.MembersBPS*int64(t.Members)
		t.PopulationOK = t.Weight > 0 && t.YesWeight*BPS >= r.PopulationBPS*t.Weight
		t.Carried = t.MembersOK && t.PopulationOK
	default:
		t.MembersOK, t.PopulationOK = t.YesMembers*2 > t.Members, true
		t.Carried = t.MembersOK
	}
	return t
}

// DuesRule is what a union asks of a member each period: a fixed sum and a share of the member treasury's income.
type DuesRule struct {
	Fixed    int64
	ShareBPS int64
}

// Due is what a member owes for a period: the fixed sum plus the share of its income, never more than maxBPS of the income and never
// below nothing. A member with no income owes nothing.
func (d DuesRule) Due(income, maxBPS int64) int64 {
	if income <= 0 {
		return 0
	}
	v := max(d.Fixed, 0) + income*max(d.ShareBPS, 0)/BPS
	return min(v, income*max(maxBPS, 0)/BPS)
}

// Arrears are how many periods a member has missed in a row; ExpellableAfter is the config bound.
func Expellable(arrears, after int64) bool { return after > 0 && arrears >= after }

// ExitAt is when a member that gave notice at noticeAt is out.
func ExitAt(noticeAt time.Time, days int64) time.Time {
	return noticeAt.AddDate(0, 0, int(max(days, 0)))
}

// ExitReady reports whether the notice has run its course.
func ExitReady(noticeAt time.Time, days int64, now time.Time) bool {
	return !now.Before(ExitAt(noticeAt, days))
}

// Share is a member's part of the purse by the default formula, pro rata to the dues each member paid (the leaver included): floor, and
// nothing when nobody paid.
func Share(purse int64, paid map[string]int64, member string) int64 {
	var sum int64
	for _, v := range paid {
		sum += max(v, 0)
	}
	if sum <= 0 || purse <= 0 {
		return 0
	}
	return purse * max(paid[member], 0) / sum
}

// SharesAll splits a purse among all the members by the same formula, the rounding remainder to the member who paid most (ties by id), so
// that exactly the purse is paid out.
func SharesAll(purse int64, paid map[string]int64) map[string]int64 {
	out := map[string]int64{}
	var sum, given int64
	ids := make([]string, 0, len(paid))
	for id, v := range paid {
		ids = append(ids, id)
		sum += max(v, 0)
	}
	sort.Strings(ids)
	if sum <= 0 || purse <= 0 {
		return out
	}
	top := ids[0]
	for _, id := range ids {
		out[id] = purse * max(paid[id], 0) / sum
		given += out[id]
		if paid[id] > paid[top] {
			top = id
		}
	}
	out[top] += purse - given
	return out
}
