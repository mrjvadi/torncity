package diplomacy

import (
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func TestMasks(t *testing.T) {
	m, err := Mask([]Measure{Trade, Travel})
	if err != nil || m != 1|8 {
		t.Fatalf("Mask = %d, %v", m, err)
	}
	back, err := FromMask(m)
	if err != nil || len(back) != 2 || back[0] != Trade || back[1] != Travel {
		t.Fatalf("FromMask(%d) = %v, %v", m, back, err)
	}
	all, _ := FromMask(FullMask())
	if len(all) != 5 {
		t.Errorf("the full mask names %d measures", len(all))
	}
	if _, err := FromMask(FullMask() + 1); !errors.Is(err, ErrUnknownMeasure) {
		t.Errorf("a mask beyond the five = %v", err)
	}
	if _, err := Mask([]Measure{"blockade"}); !errors.Is(err, ErrUnknownMeasure) {
		t.Errorf("an unknown measure = %v", err)
	}
	if on, _ := Toggle(0, Arms); on != 2 {
		t.Errorf("Toggle on = %d", on)
	}
	if off, _ := Toggle(3, Arms); off != 1 {
		t.Errorf("Toggle off = %d", off)
	}
}

func TestBlocksBothWaysOnceInForce(t *testing.T) {
	s := Sanction{ID: "s1", Imposer: "a", Target: "b", Measures: []Measure{Trade, Financial},
		ImposedAt: t0, EffectiveAt: t0.Add(time.Hour)}
	list := []Sanction{s}
	if _, blocked := Blocks(list, "a", "b", Trade, t0); blocked {
		t.Error("a sanction binds before its notice passed")
	}
	for _, pair := range [][2]string{{"a", "b"}, {"b", "a"}} {
		if got, blocked := Blocks(list, pair[0], pair[1], Trade, t0.Add(time.Hour)); !blocked || got.ID != "s1" {
			t.Errorf("trade %s→%s not blocked", pair[0], pair[1])
		}
	}
	if _, blocked := Blocks(list, "a", "b", Travel, t0.Add(2*time.Hour)); blocked {
		t.Error("a measure the sanction lacks is blocked")
	}
	for _, pair := range [][2]string{{"a", "a"}, {"a", "c"}, {"", "b"}} {
		if _, blocked := Blocks(list, pair[0], pair[1], Trade, t0.Add(2*time.Hour)); blocked {
			t.Errorf("%v blocked", pair)
		}
	}
	lifted := t0.Add(3 * time.Hour)
	s.LiftedAt = &lifted
	if _, blocked := Blocks([]Sanction{s}, "a", "b", Trade, lifted); blocked {
		t.Error("a lifted sanction still blocks")
	}
}

func TestImposeAndLift(t *testing.T) {
	if err := CheckImpose("a", "a", []Measure{Trade}, nil); !errors.Is(err, ErrSelf) {
		t.Errorf("self = %v", err)
	}
	if err := CheckImpose("a", "b", nil, nil); !errors.Is(err, ErrNoMeasures) {
		t.Errorf("empty = %v", err)
	}
	standing := Sanction{Imposer: "a", Target: "b", ImposedAt: t0}
	if err := CheckImpose("a", "b", []Measure{Arms}, []Sanction{standing}); !errors.Is(err, ErrAlreadySanctioned) {
		t.Errorf("twice = %v", err)
	}
	if err := CheckImpose("b", "a", []Measure{Arms}, []Sanction{standing}); err != nil {
		t.Errorf("the target answering in kind = %v", err)
	}
	if err := CheckLift(standing, "a", t0.Add(time.Hour), 24*time.Hour); !errors.Is(err, ErrTooSoon) {
		t.Errorf("too soon = %v", err)
	}
	if err := CheckLift(standing, "b", t0.Add(48*time.Hour), 24*time.Hour); !errors.Is(err, ErrNotParty) {
		t.Errorf("the target lifting = %v", err)
	}
	if err := CheckLift(standing, "a", t0.Add(24*time.Hour), 24*time.Hour); err != nil {
		t.Errorf("on time = %v", err)
	}
	gone := t0
	standing.LiftedAt = &gone
	if err := CheckLift(standing, "a", t0.Add(48*time.Hour), 24*time.Hour); !errors.Is(err, ErrNotInForce) {
		t.Errorf("twice lifted = %v", err)
	}
}

func TestTreatyLifecycle(t *testing.T) {
	tr := Treaty{ID: "t", Kind: "alliance", Proposer: "a", Partner: "b", Status: Proposed, ExpiresAt: t0.Add(72 * time.Hour)}
	if err := CheckPropose("a", "b", "alliance", []Treaty{tr}, t0); !errors.Is(err, ErrTreatyOpen) {
		t.Errorf("a second open alliance = %v", err)
	}
	if err := CheckPropose("b", "a", "trade_agreement", []Treaty{tr}, t0); err != nil {
		t.Errorf("another kind = %v", err)
	}
	if err := CheckPropose("a", "a", "alliance", nil, t0); !errors.Is(err, ErrSelf) {
		t.Errorf("self = %v", err)
	}
	if _, err := Answer(tr, "a", true, t0); !errors.Is(err, ErrNotParty) {
		t.Errorf("the proposer accepting = %v", err)
	}
	if s, err := Answer(tr, "b", true, t0); err != nil || s != Active {
		t.Errorf("accept = %s, %v", s, err)
	}
	if _, err := Answer(tr, "b", true, t0.Add(72*time.Hour)); !errors.Is(err, ErrTreatyState) {
		t.Errorf("accepting an expired offer = %v", err)
	}
	if err := CheckPropose("a", "b", "alliance", []Treaty{tr}, t0.Add(72*time.Hour)); err != nil {
		t.Errorf("proposing again after expiry = %v", err)
	}
	if s, err := End(tr, "a", t0); err != nil || s != Withdrawn {
		t.Errorf("withdraw = %s, %v", s, err)
	}
	if _, err := End(tr, "b", t0); !errors.Is(err, ErrTreatyState) {
		t.Errorf("the partner withdrawing the offer = %v", err)
	}
	tr.Status = Active
	if s, err := End(tr, "b", t0.Add(100*time.Hour)); err != nil || s != Terminated {
		t.Errorf("terminate = %s, %v", s, err)
	}
	if _, err := End(tr, "c", t0); !errors.Is(err, ErrNotParty) {
		t.Errorf("a stranger ending it = %v", err)
	}
}

func TestPartnersAndTariff(t *testing.T) {
	types := map[string]TreatyType{
		"alliance":        {Code: "alliance", ArmsPartner: true, MutualDefence: true},
		"trade_agreement": {Code: "trade_agreement", TariffDiscountBPS: 5000},
	}
	list := []Treaty{
		{Kind: "alliance", Proposer: "a", Partner: "b", Status: Active},
		{Kind: "trade_agreement", Proposer: "b", Partner: "c", Status: Active},
		{Kind: "alliance", Proposer: "a", Partner: "c", Status: Declined},
	}
	arms := func(t TreatyType) bool { return t.ArmsPartner }
	if !Partners(list, types, "b", "a", t0, arms) {
		t.Error("allies are not arms partners")
	}
	if Partners(list, types, "a", "c", t0, arms) {
		t.Error("a declined alliance makes partners")
	}
	if got := Tariff(2000, list, types, "c", "b", t0); got != 1000 {
		t.Errorf("tariff under a trade agreement = %d, want 1000", got)
	}
	if got := Tariff(2000, list, types, "a", "c", t0); got != 2000 {
		t.Errorf("tariff without a treaty = %d, want 2000", got)
	}
}
