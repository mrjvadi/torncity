package company

import (
	"errors"
	"testing"

	"github.com/mrjvadi/torncity/internal/shared/money"
)

func grocery() Type {
	return Type{
		Code: "grocery", Category: "groceries", Careers: []string{"retail"}, Place: "bazaar",
		FoundingFee: money.FromMinor(20000), Upkeep: money.FromMinor(500), MaxStaff: 4,
		UnitPrice: money.FromMinor(12), BaseUnits: 20, UnitsPerShift: 40, StaffTarget: 4,
		MinQualityBPS: 3000, PriceMinBPS: 6000, PriceMaxBPS: 16000, ElasticityBPS: 14000,
	}
}

func TestTypeValidate(t *testing.T) {
	if err := grocery().Validate(); err != nil {
		t.Fatalf("valid type refused: %v", err)
	}
	for name, mutate := range map[string]func(*Type){
		"no code":          func(t *Type) { t.Code = "" },
		"no careers":       func(t *Type) { t.Careers = nil },
		"repeated career":  func(t *Type) { t.Careers = []string{"retail", "retail"} },
		"no place":         func(t *Type) { t.Place = "" },
		"negative upkeep":  func(t *Type) { t.Upkeep = money.FromMinor(-1) },
		"zero staff":       func(t *Type) { t.MaxStaff = 0 },
		"free units":       func(t *Type) { t.UnitPrice = money.FromMinor(0) },
		"price above ref":  func(t *Type) { t.PriceMinBPS = 11000 },
		"price below ref":  func(t *Type) { t.PriceMaxBPS = 9000 },
		"no staff target":  func(t *Type) { t.StaffTarget = 0 },
		"quality too high": func(t *Type) { t.MinQualityBPS = 10001 },
	} {
		ty := grocery()
		mutate(&ty)
		if err := ty.Validate(); !errors.Is(err, ErrInvalidType) {
			t.Errorf("%s: got %v, want ErrInvalidType", name, err)
		}
	}
}

func TestRoles(t *testing.T) {
	owner, manager := RoleOf("a", "a", "b"), RoleOf("b", "a", "b")
	if owner != RoleOwner || manager != RoleManager || RoleOf("c", "a", "b") != RoleNone || RoleOf("", "a", "") != RoleNone {
		t.Fatal("roles misread")
	}
	for _, r := range []Right{RightWithdraw, RightAppoint, RightClose} {
		if manager.Can(r) {
			t.Errorf("a manager may %s", r)
		}
		if !owner.Can(r) {
			t.Errorf("an owner may not %s", r)
		}
	}
	for _, r := range []Right{RightViewBooks, RightDeposit, RightManageStaff, RightSetPrice} {
		if !manager.Can(r) {
			t.Errorf("a manager may not %s", r)
		}
		if RoleNone.Can(r) {
			t.Errorf("a stranger may %s", r)
		}
	}
	if err := manager.Check(RightClose); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("Check: %v", err)
	}
}

func TestRegistrationFee(t *testing.T) {
	fee, err := RegistrationFee(grocery(), 15000)
	if err != nil || fee.Minor() != 30000 {
		t.Fatalf("fee %v %v, want 30000", fee, err)
	}
}

func TestCheckOpening(t *testing.T) {
	ty := grocery()
	min := money.FromMinor(100)
	ok := Opening{Career: "retail", Wage: money.FromMinor(150), Positions: 2}
	if err := CheckOpening(ty, ok, min, 2); err != nil {
		t.Fatalf("valid opening refused: %v", err)
	}
	if err := CheckOpening(ty, Opening{Career: "finance", Wage: money.FromMinor(150), Positions: 1}, min, 0); !errors.Is(err, ErrCareerNotHired) {
		t.Errorf("career: %v", err)
	}
	var below BelowMinimumWage
	if err := CheckOpening(ty, Opening{Career: "retail", Wage: money.FromMinor(99), Positions: 1}, min, 0); !errors.As(err, &below) || below.Minimum.Minor() != 100 {
		t.Errorf("wage: %v", err)
	}
	if err := CheckOpening(ty, ok, min, 3); !errors.Is(err, ErrStaffFull) {
		t.Errorf("full: %v", err)
	}
	if err := CheckOpening(ty, Opening{Career: "retail", Wage: money.FromMinor(150)}, min, 0); !errors.Is(err, ErrInvalidAmount) {
		t.Errorf("no positions: %v", err)
	}
}

func TestStars(t *testing.T) {
	for q, want := range map[int]int{0: 1, 2500: 2, 5000: 3, 7500: 4, 9999: 4, 10000: 5, 20000: 5, -5: 1} {
		if got := Stars(q); got != want {
			t.Errorf("Stars(%d) = %d, want %d", q, got, want)
		}
	}
}

func TestCheckName(t *testing.T) {
	rules := NameRules{MinRunes: 3, MaxRunes: 24, Reserved: []string{"شهرداری", "Police"}}
	for raw, want := range map[string]string{
		"  نان   و  شیرینی  کاوه ": "نان و شیرینی کاوه",
		"Kaveh & Sons":             "Kaveh & Sons",
		"کافه‌نیل":                 "کافه‌نیل",
		"۲۴ ساعته":                 "۲۴ ساعته",
	} {
		got, err := CheckName(raw, rules)
		if err != nil || got != want {
			t.Errorf("CheckName(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	var length NameLength
	if _, err := CheckName("ab", rules); !errors.As(err, &length) || length.Min != 3 {
		t.Errorf("short: %v", err)
	}
	if _, err := CheckName("abcdefghijklmnopqrstuvwxyz", rules); !errors.Is(err, ErrNameLength) {
		t.Errorf("long: %v", err)
	}
	for _, raw := range []string{"<script>", "a_b_c", "-abc", "123", "abć", "ab\u200bcd"} {
		if _, err := CheckName(raw, rules); err == nil {
			t.Errorf("CheckName(%q) accepted", raw)
		}
	}
	for _, raw := range []string{"شهرداری نیل", "the POLICE shop", "Pol-ice Co"} {
		if _, err := CheckName(raw, rules); !errors.Is(err, ErrNameReserved) {
			t.Errorf("CheckName(%q) = %v, want reserved", raw, err)
		}
	}
	if NameKey("كاوه  Foods.") != NameKey("کاوه foods") {
		t.Error("name keys differ for one name")
	}
}
