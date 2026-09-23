package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mrjvadi/torncity/internal/application"
)

// The database re-checks bounds and cooldown in a trigger. When it refuses a
// write SetPolicy's own checks let through (a racing writer), the refusal
// must reach the caller as the same sentinel SetPolicy would have returned,
// not as an internal error.
func TestRecordPolicyMapsTriggerRefusals(t *testing.T) {
	for constraint, want := range map[string]error{
		policyValuesWithinBounds: application.ErrPolicyOutOfBounds,
		policyValuesCooldown:     application.ErrPolicyCooldown,
	} {
		q := &fakeQuerier{execErr: &pgconn.PgError{Code: sqlstateCheckViolation, ConstraintName: constraint}}
		_, err := (&GovernanceRepository{q: q}).RecordPolicy(context.Background(), application.PolicyChange{
			Setting: application.PolicySetting{SetAt: time.Now(), EffectiveAt: time.Now()},
		})
		if !errors.Is(err, want) {
			t.Errorf("%s: got %v, want %v", constraint, err, want)
		}
		if len(q.calls) != 1 {
			t.Errorf("%s: %d statements ran after the refused value, want none", constraint, len(q.calls)-1)
		}
	}
}

// A value and its public record are two statements of one transaction, and
// both carry the scalar kind the CHECK constraints require.
func TestRecordPolicyWritesValueThenPublicRecord(t *testing.T) {
	q := &fakeQuerier{}
	c, err := (&GovernanceRepository{q: q}).RecordPolicy(context.Background(), application.PolicyChange{
		Setting:    application.PolicySetting{Value: 800, SetAt: time.Now(), EffectiveAt: time.Now()},
		OfficeCode: "mayor", OldValue: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "" || c.Setting.ID == "" {
		t.Errorf("ids not assigned: %+v", c)
	}
	if len(q.calls) != 2 ||
		!strings.Contains(q.calls[0].sql, "INSERT INTO policy_values") ||
		!strings.Contains(q.calls[1].sql, "INSERT INTO policy_changes") {
		t.Fatalf("statements: %+v", q.calls)
	}
	for _, call := range q.calls {
		if !strings.Contains(call.sql, "'scalar'") {
			t.Errorf("a policy row is written without its value kind:\n%s", call.sql)
		}
	}
}

// A malformed id names nothing, and is answered before it reaches the server,
// where inside a transaction it would abort every statement after it.
func TestJurisdictionIDsAreCheckedBeforeTheServer(t *testing.T) {
	q := &fakeQuerier{}
	repo := &GovernanceRepository{q: q}
	if _, err := repo.Jurisdiction(context.Background(), "not-a-uuid"); !errors.Is(err, application.ErrJurisdictionNotFound) {
		t.Errorf("Jurisdiction: %v", err)
	}
	if _, err := repo.Seat(context.Background(), "mayor", "x", 1); !errors.Is(err, application.ErrOfficeNotFound) {
		t.Errorf("Seat: %v", err)
	}
	if len(q.calls) != 0 {
		t.Errorf("%d statements reached the server", len(q.calls))
	}

	for s, want := range map[string]bool{
		"00000000-0000-4000-8000-000000000100": true,
		"C17A0005-0000-4000-8000-000000000005": true,
		"00000000-0000-4000-8000-00000000010":  false,
		"00000000_0000-4000-8000-000000000100": false,
		"0000000g-0000-4000-8000-000000000100": false,
		"":                                     false,
	} {
		if got := validUUID(s); got != want {
			t.Errorf("validUUID(%q) = %v, want %v", s, got, want)
		}
	}
}

// Nothing reads the tax rate but the resolver: the city statements must not
// select it, and the resolver's default must come from the one closed source.
func TestResolverReadsTheCityDefaultFromOneSource(t *testing.T) {
	if !strings.Contains(normalize(selectJurisdiction), "c.tax_rate_bps FROM jurisdictions j LEFT JOIN cities c") {
		t.Errorf("the resolver no longer reads the per-city default:\n%s", normalize(selectJurisdiction))
	}
	if !strings.Contains(normalize(selectPolicySettings), "effective_at <= $3") ||
		!strings.Contains(normalize(selectPolicySettings), "effective_at > $3") {
		t.Errorf("the settings query no longer splits in-effect from pending:\n%s", normalize(selectPolicySettings))
	}
}
