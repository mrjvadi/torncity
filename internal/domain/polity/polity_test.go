package polity

import (
	"errors"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/charter"
)

func TestCeilingRefusesAPermissionOfAClassNobodyDelegated(t *testing.T) {
	grants := []charter.Grant{{Permission: charter.TreatySign}, {Permission: charter.OfficeEdit}}
	if err := Ceiling([]Class{Treaty}, grants); err != nil {
		t.Fatalf("a treaty permission under a treaty delegation: %v", err)
	}
	if err := Ceiling([]Class{Dues}, grants); !errors.Is(err, ErrCeiling) {
		t.Fatalf("a treaty permission with no treaty delegation must break the ceiling, got %v", err)
	}
	if err := Ceiling(nil, []charter.Grant{{Permission: charter.OfficeEdit}, {Permission: charter.TreasurySpend}, {Permission: charter.DebtLend}}); err != nil {
		t.Fatalf("the union's own powers need no delegation: %v", err)
	}
	if err := Ceiling([]Class{Treaty}, []charter.Grant{{Permission: charter.WarDeclare}}); !errors.Is(err, ErrCeiling) {
		t.Fatal("war needs its own delegation, not a treaty one")
	}
}

func TestACountryNeedsItsDelegationsAndItsResidents(t *testing.T) {
	req := []Class{War, Treaty, Courts}
	if err := CheckKind(KindCountry, []Class{War, Treaty}, req, 100, 15); !errors.Is(err, ErrNotCountry) {
		t.Fatalf("a country without courts: %v", err)
	}
	if err := CheckKind(KindCountry, req, req, 14, 15); !errors.Is(err, ErrTooSmall) {
		t.Fatalf("a country below the minimum: %v", err)
	}
	if err := CheckKind(KindCountry, req, req, 15, 15); err != nil {
		t.Fatal(err)
	}
	if err := CheckKind(KindLeague, nil, req, 0, 15); err != nil {
		t.Fatalf("a league needs neither: %v", err)
	}
	if err := CheckKind("empire", nil, req, 99, 15); !errors.Is(err, ErrKind) {
		t.Fatal(err)
	}
}

func TestADeepDelegationNeedsTheMembersAssembly(t *testing.T) {
	if NeedsAssembly([]Class{Roads, Claims}, 500, 1000) {
		t.Fatal("roads and claims alone do not need the assembly")
	}
	for _, c := range []Class{War, Courts, Currency, Customs, Defence} {
		if !NeedsAssembly([]Class{c}, 0, 1000) {
			t.Fatalf("%s must need the assembly", c)
		}
	}
	if !NeedsAssembly(nil, 1001, 1000) {
		t.Fatal("dues above the bound need the assembly")
	}
}

func TestDecideEachMemberRatifiesOnItsOwn(t *testing.T) {
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	dl := now.Add(48 * time.Hour)
	if got := Decide(KindCountry, 3, 2, 0, dl, now); got != Pending {
		t.Fatalf("a country waits for all three, got %s", got)
	}
	if got := Decide(KindCountry, 3, 3, 0, dl, now); got != InForce {
		t.Fatal(got)
	}
	if got := Decide(KindCountry, 3, 2, 1, dl, now); got != Refused {
		t.Fatalf("one refusal ends a country, got %s", got)
	}
	if got := Decide(KindLeague, 4, 2, 1, dl, now); got != InForce {
		t.Fatalf("a league enters into force at two, got %s", got)
	}
	if got := Decide(KindLeague, 3, 1, 0, now, now); got != Lapsed {
		t.Fatalf("a league below two at the deadline lapses, got %s", got)
	}
	if got := Decide(KindCountry, 1, 1, 0, dl, now); got != InForce {
		t.Fatalf("a country of one needs its own yes: %s", got)
	}
}

func TestDoubleMajorityNeedsBothTheMembersAndThePopulationWithACap(t *testing.T) {
	members := []Member{{"a", 100}, {"b", 30}, {"c", 20}}
	r := Rules{Mode: DoubleMajority, WeightCapBPS: 4000, MembersBPS: 5500, PopulationBPS: 6500}
	cases := []struct {
		yes  []string
		want bool
		why  string
	}{
		{[]string{"a"}, false, "the big member alone is one member of three"},
		{[]string{"a", "b"}, true, "two members and 63 of 83 capped residents"},
		{[]string{"b", "c"}, false, "two members but only 50 of 83 capped residents"},
		{[]string{"a", "b", "c"}, true, "everyone"},
		{nil, false, "nobody"},
	}
	for _, c := range cases {
		yes := map[string]bool{}
		for _, id := range c.yes {
			yes[id] = true
		}
		if got := r.Carries(members, yes); got.Carried != c.want {
			t.Errorf("%v: carried=%v, want %v (%s) %+v", c.yes, got.Carried, c.want, c.why, got)
		}
	}
	// without the cap the big member alone has the population but still not the members
	r.WeightCapBPS = 0
	if got := r.Carries(members, map[string]bool{"a": true}); got.Carried || !got.PopulationOK {
		t.Fatalf("uncapped, a alone has 66%% of the people but one of three members: %+v", got)
	}
}

func TestWeightedVoteCapsTheBigMember(t *testing.T) {
	members := []Member{{"a", 90}, {"b", 5}, {"c", 5}}
	r := Rules{Mode: Weighted, WeightCapBPS: 4000}
	if r.Carries(members, map[string]bool{"a": true}).Carried {
		t.Fatal("capped at 40 percent the big member alone must not carry")
	}
	r.WeightCapBPS = 0
	if !r.Carries(members, map[string]bool{"a": true}).Carried {
		t.Fatal("uncapped it carries alone")
	}
	one := Rules{Mode: OneMember}
	if !one.Carries(members, map[string]bool{"b": true, "c": true}).Carried || one.Carries(members, map[string]bool{"a": true}).Carried {
		t.Fatal("one member one vote: two of three carry, one does not")
	}
}

func TestDuesAreCappedAndNeverNegative(t *testing.T) {
	d := DuesRule{Fixed: 100, ShareBPS: 500}
	if got := d.Due(10_000, 1000); got != 600 {
		t.Fatalf("100 + 5%% of 10000 = 600, got %d", got)
	}
	if got := d.Due(10_000, 300); got != 300 {
		t.Fatalf("capped at 3%% of income: %d", got)
	}
	if d.Due(0, 1000) != 0 || d.Due(-5, 1000) != 0 {
		t.Fatal("no income, no dues")
	}
}

func TestExitRunsItsNotice(t *testing.T) {
	at := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	if ExitReady(at, 14, at.AddDate(0, 0, 13)) {
		t.Fatal("thirteen days into a fourteen day notice the member is still in")
	}
	if !ExitReady(at, 14, at.AddDate(0, 0, 14)) {
		t.Fatal("on the fourteenth day the member is out")
	}
	if !Expellable(3, 3) || Expellable(2, 3) || Expellable(5, 0) {
		t.Fatal("expellable after the configured arrears only")
	}
}

func TestSharesPayOutExactlyThePurse(t *testing.T) {
	paid := map[string]int64{"a": 300, "b": 200, "c": 100}
	if got := Share(600, paid, "a"); got != 300 {
		t.Fatal(got)
	}
	all := SharesAll(1000, paid)
	var sum int64
	for _, v := range all {
		sum += v
	}
	if sum != 1000 {
		t.Fatalf("shares must add up to the purse, got %d (%v)", sum, all)
	}
	if Share(500, map[string]int64{}, "a") != 0 || len(SharesAll(500, nil)) != 0 {
		t.Fatal("nobody paid, nothing to share")
	}
}

func TestNormaliseRefusesAnUnknownClass(t *testing.T) {
	if _, err := Normalise([]Class{War, "tax"}); !errors.Is(err, ErrUnknownClass) {
		t.Fatal(err)
	}
	got, err := Normalise([]Class{War, Dues, War})
	if err != nil || len(got) != 2 || got[0] != Dues {
		t.Fatalf("%v %v", got, err)
	}
}
