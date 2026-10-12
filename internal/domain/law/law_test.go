package law

import (
	"errors"
	"testing"
)

func tune() Tuning {
	return Tuning{
		StrengthWitness: 20, StrengthRecord: 25, StrengthLedgerTrace: 30, StrengthSeizedItem: 35, StrengthInformant: 20, StrengthConfession: 40,
		StrengthInspection: 30, StrengthIntelTrace: 25, WeightDecayBPS: 8000, WarrantThreshold: 60, ImproperPenalty: 30, NPCThreshold: 55,
		DefenceStrength: 10, AppealDays: 7, ReliabilityFloor: 30, ReliabilityGain: 10, ReliabilityLoss: 15, RotateDays: 14,
		RetaliationFine: 300, RetaliationBanDays: 3, DealCutBPS: 5000, BribeMaxEffectBPS: 6000, AuditBaseBPS: 1500, AuditAgeBPSPerDay: 300,
		AuditCapBPS: 9000, BrokerNewCap: 5000, BrokerCapPerRep: 100, EscrowFeeBPS: 500, BustBaseBPS: 800, BustPatrolBPS: 100, TraceBPS: 4000,
		LaunderCapBPS: 3000, LaunderFeeBPS: 2500, AnomalyBPS: 2000, InspectBaseBPS: 1500, InspectPerInspectorBPS: 1000, InspectSizeBPS: 50,
		InspectCapBPS: 9000, CustomsPenaltyBPS: 5000, DutyBPS: 500, PoliceShareBPS: 3000, RacketConvictions: 3, BaseDetectBPS: 800,
		PatrolDetectBPS: 150, DetectCapBPS: 8000, JailCellsBase: 2, CellsPerWarder: 4, BailPerHour: 250,
	}
}

func TestEvidenceTotalDecaysAndExcludedAndLowTipsCountForNothing(t *testing.T) {
	tn := tune()
	items := []Evidence{{Kind: EvWitness, Strength: 20, Proper: true, Reliability: -1}, {Kind: EvWitness, Strength: 20, Proper: true, Reliability: -1}}
	if got := tn.Total(items, true); got != 20+16 {
		t.Errorf("two witnesses = %d, want 36 (the second counts 80 percent)", got)
	}
	items = append(items, Evidence{Kind: EvConfession, Strength: 40, Proper: false, Excluded: true, Reliability: -1})
	if got := tn.Total(items, false); got != 36 {
		t.Errorf("an excluded item counts for something: %d", got)
	}
	low := []Evidence{{Kind: EvInformant, Strength: 50, Proper: true, Reliability: 10}}
	if tn.Total(low, true) != 0 {
		t.Error("a tip from a source under the floor counted")
	}
}

func TestAWarrantNeedsStrengthAndNeverRestsOnTipsAlone(t *testing.T) {
	tn := tune()
	weak := []Evidence{{Kind: EvWitness, Strength: 20, Proper: true, Reliability: -1}}
	if err := tn.WarrantAllowed(weak); !errors.Is(err, ErrWeakEvidence) {
		t.Errorf("weak: %v", err)
	}
	tips := []Evidence{{Kind: EvInformant, Strength: 50, Proper: true, Reliability: 90}, {Kind: EvInformant, Strength: 50, Proper: true, Reliability: 90}}
	if err := tn.WarrantAllowed(tips); !errors.Is(err, ErrTipsAlone) {
		t.Errorf("tips alone: %v", err)
	}
	lowTip := append(append([]Evidence{}, tips...), Evidence{Kind: EvWitness, Strength: 20, Proper: true, Reliability: -1}, Evidence{Kind: EvRecord, Strength: 25, Proper: true, Reliability: -1})
	lowTip[0].Reliability, lowTip[1].Reliability = 5, 5 // under the floor: they count for nothing
	if err := tn.WarrantAllowed(lowTip); !errors.Is(err, ErrWeakEvidence) {
		t.Errorf("low-reliability tips made a warrant: %v", err)
	}
	ok := append(append([]Evidence{}, tips...), Evidence{Kind: EvRecord, Strength: 25, Proper: true, Reliability: -1})
	if err := tn.WarrantAllowed(ok); err != nil {
		t.Errorf("a tip with a record: %v", err)
	}
	improper := []Evidence{{Kind: EvSeized, Strength: 80, Proper: false, Reliability: -1}}
	if err := tn.WarrantAllowed(improper); !errors.Is(err, ErrWeakEvidence) {
		t.Errorf("improper evidence made a warrant: %v", err)
	}
}

func TestProcedureQualityFallsWithImproperEvidenceAndWeakensTheVerdict(t *testing.T) {
	tn := tune()
	items := []Evidence{{Proper: true}, {Proper: false}, {Proper: false}}
	if q := tn.Quality(items); q != 40 {
		t.Errorf("quality = %d, want 40", q)
	}
	strong := VerdictIn{Total: 80, Quality: 100}
	if !tn.NPCGuilty(strong) {
		t.Error("strong clean case not convicted")
	}
	if tn.NPCGuilty(VerdictIn{Total: 80, Quality: 40}) {
		t.Error("a spoiled procedure still convicted")
	}
	if tn.NPCGuilty(VerdictIn{Total: 60, Quality: 100, Defence: true}) != (60-10 >= 55) {
		t.Error("the defence did not take its strength off")
	}
}

func TestSentenceNeedsAJailAndStaysInsideTheBounds(t *testing.T) {
	tn := tune()
	_ = tn
	b := Bounds{Kinds: []string{"fine", "jail", "ban"}, MaxFine: 1000, MaxJailHours: 24, MaxBanDays: 10}
	if err := ValidateSentence(b, Sentence{Kind: "jail", JailHours: 10}, 0, false); !errors.Is(err, ErrNoJail) {
		t.Errorf("no jail: %v", err)
	}
	if err := ValidateSentence(b, Sentence{Kind: "jail", JailHours: 10}, 0, true); err != nil {
		t.Errorf("jail with a cell: %v", err)
	}
	if err := ValidateSentence(b, Sentence{Kind: "jail", JailHours: 99}, 0, true); !errors.Is(err, ErrOverBound) {
		t.Errorf("over the bound: %v", err)
	}
	if err := ValidateSentence(b, Sentence{Kind: "fine", Fine: 900}, 500, true); !errors.Is(err, ErrJudgeLimit) {
		t.Errorf("over the judge's limit: %v", err)
	}
	if err := ValidateSentence(b, Sentence{Kind: "forfeiture", Fine: 1}, 0, true); !errors.Is(err, ErrKindNotAllowed) {
		t.Errorf("kind not allowed: %v", err)
	}
	if err := ValidateSentence(b, Sentence{Kind: "ban", BanDays: 3}, 0, true); !errors.Is(err, ErrEmptySentence) {
		t.Errorf("a ban from nothing: %v", err)
	}
}

func TestEachPartyAppealsOnceWithinTheWindow(t *testing.T) {
	tn := tune()
	if err := tn.CanAppeal(false, 24); err != nil {
		t.Errorf("first appeal: %v", err)
	}
	if err := tn.CanAppeal(true, 24); !errors.Is(err, ErrAppealUsed) {
		t.Errorf("second appeal: %v", err)
	}
	if err := tn.CanAppeal(false, 24*8); !errors.Is(err, ErrAppealLate) {
		t.Errorf("late appeal: %v", err)
	}
}

func TestWhoChargedACaseNeverJudgesIt(t *testing.T) {
	if !Conflict("p1", "p1") || Conflict("p1", "p2") || Conflict("", "") {
		t.Error("the conflict rule is wrong")
	}
}

func TestReliabilityRotationAndRetaliationAreBounded(t *testing.T) {
	tn := tune()
	if tn.NextReliability(95, true) != 100 || tn.NextReliability(5, false) != 0 || tn.NextReliability(50, true) != 60 {
		t.Error("reliability left its range or moved wrongly")
	}
	if tn.NeedsRotation(24*13) || !tn.NeedsRotation(24*14) {
		t.Error("rotation at the wrong time")
	}
	fine, ban := tn.Retaliation()
	if fine != 300 || ban != 3 {
		t.Errorf("retaliation = %d, %d", fine, ban)
	}
	if tn.DealCut(1000) != 500 {
		t.Error("deal cut")
	}
}

func TestABribeLowersARungButNeverToZero(t *testing.T) {
	tn := tune()
	if tn.BribeEffect(9999) != 6000 {
		t.Errorf("the effect is not capped: %d", tn.BribeEffect(9999))
	}
	for _, v := range []int64{1, 3, 60, 1000} {
		if got := tn.AfterBribe(v, 10000); got < 1 || got >= v && v > 1 {
			t.Errorf("AfterBribe(%d) = %d: never zero, and lower than before", v, got)
		}
	}
	if tn.AfterBribe(60, 10000) != 24 {
		t.Errorf("60 lowered by 60 percent = %d, want 24", tn.AfterBribe(60, 10000))
	}
	if tn.AuditChance(0) != 1500 || tn.AuditChance(5) != 3000 || tn.AuditChance(1000) != 9000 {
		t.Error("audit chance")
	}
}

func TestSeededRollsAreTheSameOnAnyReplica(t *testing.T) {
	a, b := Roll("case-1|detect"), Roll("case-1|detect")
	if a != b || a < 0 || a >= BPS {
		t.Errorf("roll = %d, %d", a, b)
	}
	if Roll("case-1|detect") == Roll("case-2|detect") && Roll("x") == Roll("y") {
		t.Error("labels do not move the roll")
	}
}

func TestHiddenBoardPriceBrokerCapAndFees(t *testing.T) {
	tn := tune()
	if BlackPrice(1000, 2500) != 1250 {
		t.Error("price")
	}
	if tn.BrokerCap(0) != 5000 || tn.BrokerCap(10) != 6000 || tn.BrokerCap(-5) != 5000 {
		t.Error("broker cap")
	}
	if tn.EscrowFee(1000) != 50 {
		t.Error("escrow fee")
	}
	if !tn.Busted(0, 799) || tn.Busted(0, 800) || !tn.Busted(10, 1799) {
		t.Error("bust")
	}
}

func TestLaunderingCapFeeAndAnomaly(t *testing.T) {
	tn := tune()
	if tn.LaunderCap(10000) != 3000 || tn.LaunderFee(1000) != 250 {
		t.Error("cap or fee")
	}
	if tn.Anomaly(12000, 10000) || !tn.Anomaly(12001, 10000) {
		t.Error("anomaly threshold")
	}
	if tn.AnomalyStrength(12000, 10000) != 0 || tn.AnomalyStrength(15000, 10000) <= tn.StrengthLedgerTrace {
		t.Error("anomaly strength")
	}
}

func TestCustomsFindsWhatIsNotDeclared(t *testing.T) {
	tn := tune()
	l := Load{Declared: map[string]int64{"salt": 5}, Carried: map[string]int64{"salt": 8, "fake_documents": 2}, Price: map[string]int64{"salt": 10, "fake_documents": 100}}
	un := l.Undeclared()
	if un["salt"] != 3 || un["fake_documents"] != 2 || len(un) != 2 {
		t.Errorf("undeclared = %v", un)
	}
	if l.Units() != 10 {
		t.Errorf("units = %d", l.Units())
	}
	if l.Value(un) != 230 || tn.Penalty(230) != 115 || tn.Duty(50) != 2 {
		t.Error("values")
	}
	if tn.InspectChance(0, 0) != 1500 || tn.InspectChance(2, 10) != 1500+2000+500 || tn.InspectChance(99, 999) != 9000 {
		t.Error("inspect chance")
	}
	if tn.PoliceShare(1000) != 300 {
		t.Error("police share")
	}
}

func TestJailCellsAndBailAndRacketeering(t *testing.T) {
	tn := tune()
	if tn.JailCells(0) != 2 || tn.JailCells(2) != 10 || tn.Bail(4) != 1000 {
		t.Error("cells or bail")
	}
	if tn.Racketeering(2) || !tn.Racketeering(3) {
		t.Error("racketeering")
	}
	if tn.DetectChance(0, 0) != 800 || tn.DetectChance(4, 0) != 1400 || tn.DetectChance(1000, 0) != 8000 {
		t.Error("detect chance")
	}
}
