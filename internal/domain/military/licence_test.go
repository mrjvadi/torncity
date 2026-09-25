package military

import (
	"errors"
	"testing"
	"time"
)

func TestLicenceLifecycle(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	pending := Licence{Status: LicencePending}
	if pending.InForce(now) || !pending.Open(now) {
		t.Fatal("a pending application is open and not in force")
	}
	active, err := Decide(pending, true)
	if err != nil || active.Status != LicenceActive || !active.InForce(now) {
		t.Fatalf("approved: %+v %v", active, err)
	}
	if _, err := Decide(active, true); !errors.Is(err, ErrLicenceState) {
		t.Fatalf("approved twice: %v", err)
	}
	rejected, err := Decide(pending, false)
	if err != nil || rejected.Status != LicenceRejected || rejected.Open(now) {
		t.Fatalf("rejected: %+v %v", rejected, err)
	}

	revoking, err := Revoke(active, now, 24*time.Hour)
	if err != nil || revoking.Status != LicenceRevoking {
		t.Fatalf("revoked: %+v %v", revoking, err)
	}
	if !revoking.InForce(now.Add(23*time.Hour)) || revoking.Settled(now.Add(23*time.Hour)) != LicenceRevoking {
		t.Fatal("a revocation is in force until its notice runs out")
	}
	if revoking.InForce(now.Add(24*time.Hour)) || revoking.Settled(now.Add(24*time.Hour)) != LicenceRevoked {
		t.Fatal("a revocation lapses when its notice runs out")
	}
	if revoking.Open(now.Add(25 * time.Hour)) {
		t.Fatal("a lapsed licence does not stand in the way of a new application")
	}
	if _, err := Revoke(revoking, now, time.Hour); !errors.Is(err, ErrLicenceState) {
		t.Fatalf("revoked twice: %v", err)
	}
	if _, err := Revoke(pending, now, time.Hour); !errors.Is(err, ErrLicenceState) {
		t.Fatalf("an application revoked: %v", err)
	}
	if _, err := Revoke(active, now, -time.Hour); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative notice: %v", err)
	}
	immediate, _ := Revoke(active, now, 0)
	if immediate.InForce(now) {
		t.Fatal("a revocation with no notice is in force")
	}
}

func TestMayFound(t *testing.T) {
	cases := []struct {
		name string
		f    Founder
		want Basis
		err  error
	}{
		{"a civilian", Founder{}, "", ErrNoDefenceLicence},
		{"a private", Founder{Serving: true, RankTier: 0}, "", ErrNoDefenceLicence},
		{"a captain", Founder{Serving: true, RankTier: 3}, BasisRank, nil},
		{"a general", Founder{Serving: true, RankTier: 6}, BasisRank, nil},
		{"a retired captain", Founder{Serving: false, RankTier: 3}, "", ErrNoDefenceLicence},
		{"a contractor's owner", Founder{Contractor: true}, BasisContractor, nil},
		{"a private who owns a contractor", Founder{Serving: true, Contractor: true}, BasisContractor, nil},
	}
	for _, c := range cases {
		got, err := MayFound(c.f, 3)
		if got != c.want || !errors.Is(err, c.err) {
			t.Errorf("%s: %q %v, want %q %v", c.name, got, err, c.want, c.err)
		}
	}
	if _, err := MayFound(Founder{}, -1); !errors.Is(err, ErrInvalid) {
		t.Errorf("a negative rank: %v", err)
	}
}

func TestContractorRule(t *testing.T) {
	r := ContractorRule{MinTechnologies: 3, MinTier: 2}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := r.Eligible(3, 2); err != nil {
		t.Errorf("at the bar: %v", err)
	}
	if err := r.Eligible(2, 3); !errors.Is(err, ErrNotEligible) {
		t.Errorf("too few technologies: %v", err)
	}
	if err := r.Eligible(5, 1); !errors.Is(err, ErrNotEligible) {
		t.Errorf("too shallow: %v", err)
	}
	if err := (ContractorRule{MinTechnologies: -1}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("a negative bound: %v", err)
	}
	if err := (ContractorRule{}).Eligible(0, 0); err != nil {
		t.Errorf("a rule that asks nothing: %v", err)
	}
}
