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
	if len(AllUnionPermissions()) != len(seen) {
		t.Error("AllUnionPermissions differs from the catalogue")
	}
	for _, g := range AllPermissions() {
		if UnionOnly(g.Permission) {
			t.Errorf("a settlement's founder office must not hold the union-only %s", g.Permission)
		}
	}
	if _, err := NormaliseGrantsForSettlement([]Grant{{Permission: WarDeclare}}); err == nil {
		t.Error("a settlement may not be given the power to declare war")
	}
	if _, err := NormaliseGrants([]Grant{{Permission: WarDeclare}}); err != nil {
		t.Errorf("a union may: %v", err)
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

// In the adversarial model no office may hold both the power to charge and the power to judge, and the office that audits for integrity never also
// arrests (docs/adr/0078 3.2). The founder's office holds every power and is exempt: the case itself keeps the roles apart. The inquisitorial model lifts
// the first rule only.
func TestNoOfficeHoldsTheChargeAndTheJudgmentOrTheAuditAndTheArrest(t *testing.T) {
	office := func(acq Acquisition, perms ...Permission) Office {
		o := Office{ID: "o", Title: "x", Seats: 1, Acquisition: acq}
		for _, p := range perms {
			o.Grants = append(o.Grants, Grant{Permission: p})
		}
		return o
	}
	l := Limits{MaxOffices: 5, MaxSeats: 5, MaxPermissions: 90, TitleMin: 2, TitleMax: 32}
	if err := ValidateOffice(office(AcquireAppointment, ProsecuteCharge, CourtJudge), 1, l); !errors.Is(err, ErrSeparation) {
		t.Errorf("charge and judge in one office: %v", err)
	}
	if err := ValidateOffice(office(AcquireElection, CourtJudge, ProsecuteCharge, PolicePatrol), 1, l); !errors.Is(err, ErrSeparation) {
		t.Errorf("an elected office too: %v", err)
	}
	if err := ValidateOffice(office(AcquireAppointment, AuditIntegrity, PoliceArrest), 1, l); !errors.Is(err, ErrSeparation) {
		t.Errorf("audit and arrest in one office: %v", err)
	}
	for _, ok := range [][]Permission{{ProsecuteCharge, ProsecuteDrop}, {CourtJudge, CourtWarrant, CourtAppealHear}, {AuditIntegrity, AuditBooks}, {PoliceArrest, PoliceInvestigate}} {
		if err := ValidateOffice(office(AcquireAppointment, ok...), 1, l); err != nil {
			t.Errorf("%v is a fine office: %v", ok, err)
		}
	}
	l.Inquisitorial = true
	if err := ValidateOffice(office(AcquireAppointment, ProsecuteCharge, CourtJudge), 1, l); err != nil {
		t.Errorf("a magistrate may do both in the inquisitorial model: %v", err)
	}
	if err := ValidateOffice(office(AcquireAppointment, AuditIntegrity, PoliceArrest), 1, l); !errors.Is(err, ErrSeparation) {
		t.Errorf("the audit and the arrest stay apart in every model: %v", err)
	}
	l.Inquisitorial = false
	head := FoundersOffice("h", "head")
	if err := ValidateOffice(head, 1, Limits{MaxOffices: 5, MaxSeats: 5, MaxPermissions: 200, TitleMin: 2, TitleMax: 32}); err != nil {
		t.Errorf("the founder's office holds every power: %v", err)
	}
}
