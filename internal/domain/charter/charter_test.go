package charter

import (
	"errors"
	"testing"
)

var lim = Limits{MaxOffices: 24, MaxSeats: 15, MaxPermissions: 40, TitleMin: 2, TitleMax: 32}

func TestTitles(t *testing.T) {
	if got, err := ValidateTitle("  معاون   شهردار ", nil, lim); err != nil || got != "معاون شهردار" {
		t.Fatalf("a good title: %q %v", got, err)
	}
	withContent := lim
	withContent.Reserved = []string{"ادمین", "شهر مرکزی"}
	for _, bad := range []string{"", "ا", "ادمین", "Support", "شهر مرکزی", "x234567890123456789012345678901234"} {
		if _, err := ValidateTitle(bad, nil, withContent); !errors.Is(err, ErrTitle) {
			t.Errorf("%q: err = %v, want ErrTitle", bad, err)
		}
	}
	if _, err := ValidateTitle("کلانتر", []string{"کلانتر"}, lim); !errors.Is(err, ErrTitleTaken) {
		t.Errorf("a duplicate title: %v", err)
	}
}

func TestEveryPermissionIsInTheCatalogueOnce(t *testing.T) {
	seen := map[Permission]bool{}
	for _, d := range Catalogue() {
		if seen[d.Code] || d.Group == "" {
			t.Errorf("%s listed twice or without a group", d.Code)
		}
		seen[d.Code] = true
	}
	if len(AllPermissions()) != len(seen) {
		t.Error("AllPermissions differs from the catalogue")
	}
	for _, p := range []Permission{RoadDraw, StorageTake, PublicBuild, TreasurySpend, OfficeEdit, CharterAmend} {
		if d, ok := Lookup(p); !ok || !d.Active {
			t.Errorf("%s should be an active permission", p)
		}
	}
}

func TestNormaliseGrants(t *testing.T) {
	got, err := NormaliseGrants([]Grant{{RoadDraw, 0}, {TreasurySpend, 100}, {TreasurySpend, 500}, {RoadDraw, 0}})
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	for _, g := range got {
		if g.Permission == TreasurySpend && g.Limit != 500 {
			t.Errorf("the larger limit should win: %+v", g)
		}
	}
	// 0 is no ceiling, so it beats any number
	got, _ = NormaliseGrants([]Grant{{TreasurySpend, 500}, {TreasurySpend, 0}})
	if len(got) != 1 || got[0].Limit != 0 {
		t.Errorf("no ceiling should win: %+v", got)
	}
	if _, err := NormaliseGrants([]Grant{{"fly.away", 0}}); !errors.Is(err, ErrUnknownGrant) {
		t.Errorf("unknown: %v", err)
	}
	if _, err := NormaliseGrants([]Grant{{RoadDraw, 5}}); !errors.Is(err, ErrLimitless) {
		t.Errorf("a limit on a limitless permission: %v", err)
	}
}

// R1: nobody grants what they do not hold, nor more widely than they hold it.
func TestNobodyGrantsWhatTheyDoNotHold(t *testing.T) {
	h := HeldBy([]Grant{{RoadDraw, 0}, {TreasurySpend, 1000}})
	if err := h.CanGrant([]Grant{{RoadDraw, 0}, {TreasurySpend, 500}}); err != nil {
		t.Errorf("a narrower grant of held permissions: %v", err)
	}
	for _, want := range [][]Grant{{{StorageTake, 0}}, {{TreasurySpend, 2000}}, {{TreasurySpend, 0}}} {
		if err := h.CanGrant(want); !errors.Is(err, ErrNotHeld) {
			t.Errorf("%+v granted: %v", want, err)
		}
	}
	// two offices of one person together
	h = HeldBy([]Grant{{TreasurySpend, 1000}}, []Grant{{TreasurySpend, 0}})
	if err := h.CanGrant([]Grant{{TreasurySpend, 0}}); err != nil {
		t.Errorf("offices fold to the widest limit: %v", err)
	}
}

// R2: there is always an office manager who can act.
func TestThereIsAlwaysAnOfficeManager(t *testing.T) {
	head := OfficeState{Office: FoundersOffice("h", "شهردار"), Held: true}
	sheriff := OfficeState{Office: Office{ID: "s", Title: "کلانتر", Seats: 1, Grants: []Grant{{RoadDraw, 0}}, Acquisition: AcquireAppointment}, Held: true}
	if err := ManagerGuard([]OfficeState{head, sheriff}); err != nil {
		t.Fatal(err)
	}
	if err := ManagerGuard([]OfficeState{sheriff}); !errors.Is(err, ErrLastManager) {
		t.Errorf("no manager: %v", err)
	}
	deputy := OfficeState{Office: Office{ID: "d", Title: "معاون", Seats: 1, Grants: []Grant{{OfficeEdit, 0}, {CharterAmend, 0}}, Acquisition: AcquireAppointment}, Held: false}
	if err := ManagerGuard([]OfficeState{sheriff, deputy}); !errors.Is(err, ErrLastManager) {
		t.Errorf("a vacant manager office is no manager: %v", err)
	}
	deputy.Held = true
	deputy.Closed = true
	if err := ManagerGuard([]OfficeState{sheriff, deputy}); !errors.Is(err, ErrLastManager) {
		t.Errorf("a closed manager office is no manager: %v", err)
	}
}

func TestCaps(t *testing.T) {
	o := Office{Title: "x", Seats: 3, Acquisition: AcquireAppointment}
	if err := ValidateOffice(o, 3, lim); err != nil {
		t.Fatal(err)
	}
	o.Seats = 16
	if err := ValidateOffice(o, 3, lim); !errors.Is(err, ErrCaps) {
		t.Errorf("too many seats: %v", err)
	}
	o.Seats = 1
	if err := ValidateOffice(o, 25, lim); !errors.Is(err, ErrCaps) {
		t.Errorf("too many offices: %v", err)
	}
	o.Acquisition = "sorcery"
	if err := ValidateOffice(o, 1, lim); !errors.Is(err, ErrAcquisition) {
		t.Errorf("unknown acquisition: %v", err)
	}
}
