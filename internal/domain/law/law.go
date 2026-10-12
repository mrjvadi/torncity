// Package law holds the pure rules of police, courts, informants, bribery, the hidden board and customs (docs/adr/0078). Nothing here
// touches a database or a clock: a rule takes its figures from a Tuning (config law.*) and a seeded label, so any replica reaches the same
// answer, and a retried step finds what the first one wrote.
//
// It is a model of institutions, not a manual: evidence has a strength, a warrant needs a judge, a tip needs a reliable source, a bribe
// lowers a rung but never to nothing, an audit may find it, a load may be inspected. No rule here describes how an act is done.
package law

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
)

// BPS is 100 percent in basis points.
const BPS = 10_000

// Roll is a seeded roll in 0..9999 from a label (the case's id and a purpose). The same label gives the same roll on any replica.
func Roll(label string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("law|" + label))
	return int64(h.Sum64() % BPS)
}

// Hit reports whether a roll is under a chance in basis points.
func Hit(chance, roll int64) bool { return roll < chance }

func clamp(v, lo, hi int64) int64 { return min(max(v, lo), hi) }

// ---------------------------------------------------------------------------
// evidence, warrants
// ---------------------------------------------------------------------------

// The kinds of evidence.
const (
	EvWitness    = "witness"
	EvRecord     = "record"
	EvLedger     = "ledger_trace"
	EvSeized     = "seized_item"
	EvInformant  = "informant_report"
	EvConfession = "confession"
	EvInspection = "inspection"
	EvIntelTrace = "intel_trace"
)

// Kinds are the kinds of evidence, in a stable order.
func Kinds() []string {
	return []string{EvWitness, EvRecord, EvLedger, EvSeized, EvInformant, EvConfession, EvInspection, EvIntelTrace}
}

// Evidence is one stored item. It is immutable once stored; an item the defence has excluded stays in the file and counts for nothing.
type Evidence struct {
	Kind     string
	Strength int64
	// Proper says the item was got by the book (with a warrant where one was needed); an improper item adds strength but costs procedure quality.
	Proper   bool
	Excluded bool
	// Reliability is the informant's reliability when the item is a tip; -1 for every other kind.
	Reliability int64
}

// KindStrength is the strength a new item of the kind starts with. A tip is scaled by the informant's reliability (0 to 100).
func (t Tuning) KindStrength(kind string, reliability int64) int64 {
	switch kind {
	case EvWitness:
		return t.StrengthWitness
	case EvRecord:
		return t.StrengthRecord
	case EvLedger:
		return t.StrengthLedgerTrace
	case EvSeized:
		return t.StrengthSeizedItem
	case EvInformant:
		return t.StrengthInformant * clamp(reliability, 0, 100) / 100
	case EvConfession:
		return t.StrengthConfession
	case EvInspection:
		return t.StrengthInspection
	case EvIntelTrace:
		return t.StrengthIntelTrace
	}
	return 0
}

// counted is what an item adds to a total: nothing when excluded, and nothing for a tip from a source under the reliability floor.
func (t Tuning) counted(e Evidence) int64 {
	if e.Excluded || e.Strength <= 0 {
		return 0
	}
	if e.Kind == EvInformant && e.Reliability < t.ReliabilityFloor {
		return 0
	}
	return e.Strength
}

// Total adds the strength of the counted items, strongest first, each next one counting weight_decay_bps of the one before it (a fifth
// witness says less than the first). Proper reports only the items got by the book.
func (t Tuning) Total(items []Evidence, properOnly bool) int64 {
	var s []int64
	for _, e := range items {
		if properOnly && !e.Proper {
			continue
		}
		if c := t.counted(e); c > 0 {
			s = append(s, c)
		}
	}
	sort.Slice(s, func(i, j int) bool { return s[i] > s[j] })
	var sum int64
	w := int64(BPS)
	for _, v := range s {
		sum += v * w / BPS
		w = w * t.WeightDecayBPS / BPS
	}
	return sum
}

// Quality is the procedure quality of a case, 0 to 100: it starts whole and loses improper_penalty for each improper item.
func (t Tuning) Quality(items []Evidence) int64 {
	q := int64(100)
	for _, e := range items {
		if !e.Proper {
			q -= t.ImproperPenalty
		}
	}
	return clamp(q, 0, 100)
}

// Refusals of a warrant.
var (
	ErrWeakEvidence = errors.New("law: the proper evidence is not strong enough for a warrant")
	ErrTipsAlone    = errors.New("law: a warrant cannot rest on informants' tips alone")
)

// WarrantAllowed says a judge may sign a warrant: the proper evidence reaches the threshold, and it does not rest on informants' tips alone
// (a tip under the reliability floor counts for nothing at all, and the tips together never make the whole of it).
func (t Tuning) WarrantAllowed(items []Evidence) error {
	if t.Total(items, true) < t.WarrantThreshold {
		return ErrWeakEvidence
	}
	var other []Evidence
	for _, e := range items {
		if e.Kind != EvInformant {
			other = append(other, e)
		}
	}
	if t.Total(other, true) <= 0 {
		return ErrTipsAlone
	}
	return nil
}

// DetectChance is the chance a beat notices an offence on its own: the base, plus what the patrol points spent on it add, to a cap.
func (t Tuning) DetectChance(patrolPoints, pressureBPS int64) int64 {
	c := t.BaseDetectBPS + patrolPoints*t.PatrolDetectBPS
	c = c * (BPS + max(pressureBPS, 0)) / BPS
	return clamp(c, 0, t.DetectCapBPS)
}

// ---------------------------------------------------------------------------
// the roles of a case
// ---------------------------------------------------------------------------

// Conflict says one person may not hold two roles of the same case: whoever charged a case never judges it, and whoever signed its warrant
// never sits on its appeal. The founder's office holds every power, so the case itself keeps the roles apart.
func Conflict(charger, judge string) bool { return charger != "" && charger == judge }

// ---------------------------------------------------------------------------
// the verdict
// ---------------------------------------------------------------------------

// Verdict inputs of the NPC judge: the strength of the proper-and-admitted evidence, the procedure quality and whether a defence appeared.
// The judge of a settlement without a player judge decides by a threshold, so there is never a legal vacuum; a player judge decides by hand.
type VerdictIn struct {
	Total   int64
	Quality int64
	Defence bool
}

// Effective is the strength that stands after the procedure and the defence.
func (t Tuning) Effective(in VerdictIn) int64 {
	e := in.Total * clamp(in.Quality, 0, 100) / 100
	if in.Defence {
		e -= t.DefenceStrength
	}
	return max(e, 0)
}

// NPCGuilty is the NPC judge's decision.
func (t Tuning) NPCGuilty(in VerdictIn) bool { return t.Effective(in) >= t.NPCThreshold }

// Sentence is one sentence handed down. Fine is in minor units, JailHours in hours, BanDays in days.
type Sentence struct {
	Kind      string
	Fine      int64
	JailHours int64
	BanDays   int64
	// Ban is what a ban keeps the person out of: a building code, a market, an office or the settlement.
	Ban string
}

// Bounds are the limits of an offence a sentence works inside (the code bounds lowered by the charter, never raised).
type Bounds struct {
	Kinds        []string
	MaxFine      int64
	MaxJailHours int64
	MaxBanDays   int64
}

// Refusals of a sentence.
var (
	ErrKindNotAllowed = errors.New("law: the offence does not carry this kind of sentence")
	ErrOverBound      = errors.New("law: the sentence is over the bound of the offence")
	ErrJudgeLimit     = errors.New("law: the sentence is over what this judge may hand down")
	ErrNoJail         = errors.New("law: there is no jail to serve a jail sentence in")
	ErrEmptySentence  = errors.New("law: a sentence needs a size")
)

// ValidateSentence checks a sentence against the offence's bounds, the judge's limit on a fine (0: none) and whether a jail with a free cell
// stands. Nobody is sentenced to jail where no jail stands.
func ValidateSentence(b Bounds, s Sentence, judgeFineLimit int64, jailFree bool) error {
	ok := false
	for _, k := range b.Kinds {
		ok = ok || k == s.Kind
	}
	if !ok {
		return fmt.Errorf("%w: %s", ErrKindNotAllowed, s.Kind)
	}
	switch s.Kind {
	case "fine", "restitution":
		if s.Fine <= 0 {
			return ErrEmptySentence
		}
		if s.Fine > b.MaxFine {
			return ErrOverBound
		}
		if judgeFineLimit > 0 && s.Fine > judgeFineLimit {
			return ErrJudgeLimit
		}
	case "jail":
		if s.JailHours <= 0 {
			return ErrEmptySentence
		}
		if s.JailHours > b.MaxJailHours {
			return ErrOverBound
		}
		if !jailFree {
			return ErrNoJail
		}
	case "ban":
		if s.BanDays <= 0 || s.Ban == "" {
			return ErrEmptySentence
		}
		if s.BanDays > b.MaxBanDays {
			return ErrOverBound
		}
	}
	return nil
}

// Appeals: each party may appeal once within the window.
var (
	ErrAppealUsed = errors.New("law: this party has already appealed")
	ErrAppealLate = errors.New("law: the appeal window is over")
)

// CanAppeal checks the one-appeal rule: used says the party already has an appeal, ageHours how long ago the verdict was.
func (t Tuning) CanAppeal(used bool, ageHours int64) error {
	if used {
		return ErrAppealUsed
	}
	if ageHours > t.AppealDays*24 {
		return ErrAppealLate
	}
	return nil
}

// JailCells is how many cells an open jail has: its base plus what its warders can keep.
func (t Tuning) JailCells(warders int64) int64 { return t.JailCellsBase + warders*t.CellsPerWarder }

// Bail is the price of an hour-for-hour release.
func (t Tuning) Bail(hours int64) int64 { return hours * t.BailPerHour }

// ---------------------------------------------------------------------------
// informants
// ---------------------------------------------------------------------------

// NextReliability moves a source's reliability: up when a tip is corroborated, down when it is contradicted; always inside 0 to 100.
func (t Tuning) NextReliability(cur int64, corroborated bool) int64 {
	if corroborated {
		return clamp(cur+t.ReliabilityGain, 0, 100)
	}
	return clamp(cur-t.ReliabilityLoss, 0, 100)
}

// NeedsRotation says a handler has kept an informant too long: the informant passes to another handler (the research: close, regular
// pairings are where coaching and invention begin).
func (t Tuning) NeedsRotation(sinceHours int64) bool { return sinceHours >= t.RotateDays*24 }

// Retaliation is the penalty on a burned informant: cash and a ban from a building, never harm to the person.
func (t Tuning) Retaliation() (fine int64, banDays int64) {
	return t.RetaliationFine, t.RetaliationBanDays
}

// DealCut is the sentence left after an accused turns informant.
func (t Tuning) DealCut(v int64) int64 { return v - v*t.DealCutBPS/BPS }

// ---------------------------------------------------------------------------
// bribery and audits
// ---------------------------------------------------------------------------

// BribeEffect is how much a bribe lowers a rung: what was asked, to the cap, and never the whole of it.
func (t Tuning) BribeEffect(askedBPS int64) int64 {
	c := min(t.BribeMaxEffectBPS, BPS-1)
	return clamp(askedBPS, 0, c)
}

// AfterBribe is a rung's value (a warrant threshold, a detection chance, a sentence) once a bribe has lowered it: it never reaches zero.
func (t Tuning) AfterBribe(value, effectBPS int64) int64 {
	if value <= 0 {
		return value
	}
	out := value - value*t.BribeEffect(effectBPS)/BPS
	return max(out, 1)
}

// AuditChance is the chance an audit finds an open bribe: the base, plus the days it has stood, to a cap.
func (t Tuning) AuditChance(ageDays int64) int64 {
	return clamp(t.AuditBaseBPS+ageDays*t.AuditAgeBPSPerDay, 0, t.AuditCapBPS)
}

// ---------------------------------------------------------------------------
// the hidden board
// ---------------------------------------------------------------------------

// BlackPrice is a good's price on the board: its base price plus the premium of having no legal source.
func BlackPrice(base, premiumBPS int64) int64 { return base + base*premiumBPS/BPS }

// BrokerCap is the most a broker may hold in escrow: nothing much for a new one, more with reputation.
func (t Tuning) BrokerCap(reputation int64) int64 {
	return t.BrokerNewCap + max(reputation, 0)*t.BrokerCapPerRep
}

// EscrowFee is the broker's fee.
func (t Tuning) EscrowFee(amount int64) int64 { return amount * t.EscrowFeeBPS / BPS }

// Busted reports whether a trade on the board is noticed: the base chance plus what the settlement's patrol adds.
func (t Tuning) Busted(patrolPoints, roll int64) bool {
	return Hit(clamp(t.BustBaseBPS+patrolPoints*t.BustPatrolBPS, 0, BPS), roll)
}

// ScamTraced reports whether an exit scam is traced to the broker.
func (t Tuning) ScamTraced(roll int64) bool { return Hit(t.TraceBPS, roll) }

// ---------------------------------------------------------------------------
// laundering
// ---------------------------------------------------------------------------

// LaunderCap is the most a front may turn clean in a period: a share of its legal turnover.
func (t Tuning) LaunderCap(turnover int64) int64 { return turnover * t.LaunderCapBPS / BPS }

// LaunderFee is the fee of turning an amount clean.
func (t Tuning) LaunderFee(amount int64) int64 { return amount * t.LaunderFeeBPS / BPS }

// Anomaly says a business's clean inflow over a window is out of proportion with its legal turnover: the ledger check of the audit.
func (t Tuning) Anomaly(cleanInflow, turnover int64) bool {
	allowed := turnover + turnover*t.AnomalyBPS/BPS
	return cleanInflow > allowed
}

// AnomalyStrength is the strength of the ledger-trace evidence an anomaly yields: it grows with how far the inflow is over the allowance, to
// twice the base strength.
func (t Tuning) AnomalyStrength(cleanInflow, turnover int64) int64 {
	if !t.Anomaly(cleanInflow, turnover) {
		return 0
	}
	allowed := turnover + turnover*t.AnomalyBPS/BPS
	over := cleanInflow - allowed
	extra := t.StrengthLedgerTrace
	if allowed > 0 {
		extra = min(t.StrengthLedgerTrace, t.StrengthLedgerTrace*over/allowed)
	}
	return t.StrengthLedgerTrace + extra
}

// The states of a laundering batch, in order.
var BatchStates = []string{"placed", "layered", "integrated"}

// ---------------------------------------------------------------------------
// customs
// ---------------------------------------------------------------------------

// InspectChance is the chance a load is inspected at an open customs house.
func (t Tuning) InspectChance(inspectors, units int64) int64 {
	return clamp(t.InspectBaseBPS+inspectors*t.InspectPerInspectorBPS+units*t.InspectSizeBPS, 0, t.InspectCapBPS)
}

// Load is a load that crosses a border: what was declared and what is carried, item to quantity, and what each unit is worth.
type Load struct {
	Declared map[string]int64
	Carried  map[string]int64
	Price    map[string]int64
}

// Undeclared is what is carried over what was declared, item to quantity.
func (l Load) Undeclared() map[string]int64 {
	out := map[string]int64{}
	for it, q := range l.Carried {
		if extra := q - l.Declared[it]; extra > 0 {
			out[it] = extra
		}
	}
	return out
}

// Units is the size of the load.
func (l Load) Units() int64 {
	var n int64
	for _, q := range l.Carried {
		n += q
	}
	return n
}

// Value is what a set of goods is worth.
func (l Load) Value(m map[string]int64) int64 {
	var v int64
	for it, q := range m {
		v += q * l.Price[it]
	}
	return v
}

// Duty is the duty on the declared value; Penalty the penalty on the undeclared value found by an inspection.
func (t Tuning) Duty(declaredValue int64) int64 { return declaredValue * t.DutyBPS / BPS }

// Penalty is the penalty on the value of what was not declared.
func (t Tuning) Penalty(undeclaredValue int64) int64 {
	return undeclaredValue * t.CustomsPenaltyBPS / BPS
}

// PoliceShare is the share of a seizure that goes to the police purse (capped by config); the rest goes to the settlement stock.
func (t Tuning) PoliceShare(v int64) int64 { return v * t.PoliceShareBPS / BPS }

// ---------------------------------------------------------------------------
// gangs
// ---------------------------------------------------------------------------

// Racketeering says a gang's linked convictions make a racketeering count.
func (t Tuning) Racketeering(linked int64) bool { return linked >= t.RacketConvictions }
