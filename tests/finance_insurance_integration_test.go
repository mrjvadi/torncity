//go:build integration

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// admit puts a player in the city hospital, as an injury does.
func (w *financeWorld) admit(p *application.Player, stay time.Duration) string {
	w.t.Helper()
	ctx := testCtx(w.t)
	s := application.HospitalStay{ID: newUUID(w.t), PlayerID: p.ID, CityID: w.city.ID, Cause: application.CauseCrime,
		Status: application.StayAdmitted, HealthIn: 20, HealthOut: 60, AdmittedAt: w.now(), EndsAt: w.now().Add(stay),
		GameActionID: newUUID(w.t)}
	if err := w.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		if err := tx.GameActions().Schedule(ctx, application.GameAction{ID: s.GameActionID,
			ActionType: application.HospitalDischargeActionType, ActorType: "player", ActorID: p.ID,
			ReferenceType: application.HospitalReference, ReferenceID: s.ID, Payload: []byte(`{}`),
			StartedAt: s.AdmittedAt, FinishAt: s.EndsAt}); err != nil {
			return err
		}
		return tx.Health().Admit(ctx, s)
	}); err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.pool.Raw().Exec(ctx, `UPDATE players SET place_code = 'hospital', place_since = now() WHERE id = $1::uuid`,
		p.ID); err != nil {
		w.t.Fatal(err)
	}
	return s.ID
}

func TestHealthInsurancePaysATreatmentOnce(t *testing.T) {
	w := newFinanceWorld(t, nil)
	ctx := testCtx(t)
	requireStageE(t, w.pool)
	product, ok := w.def.InsuranceProduct("health")
	if !ok {
		t.Skip("the active content sells no health insurance")
	}
	// The fund holds only what premiums paid it: a claim beyond that is
	// paid as far as the fund goes (solvency).
	p := w.resident(100_000)
	t.Cleanup(func() { purgeStageE(t, w.pool, p.ID) })

	// Bought: the first premium, once, into the fund.
	fund := func() int64 { return w.balance(application.AccountInsuranceFund, w.country) }
	before := fund()
	resp, err := w.fin.Buy(ctx, w.meta(p, "insure.buy"), handlers.FinanceRequest{Product: "health", Property: "0"})
	w.ok("the policy's price", resp, err, "Health insurance")
	for i := 0; i < 2; i++ {
		resp, err = w.fin.Buy(ctx, w.meta(p, "insure.buy"), handlers.FinanceRequest{Product: "health", Property: "0",
			Method: "card"})
		w.ok("buying the policy", resp, err)
	}
	if n := w.count(`SELECT count(*) FROM insurance_policies WHERE player_id = $1::uuid AND status = 'active'`, p.ID); n != 1 {
		t.Fatalf("two presses bought %d policies", n)
	}
	if got := fund() - before; got != product.Premium {
		t.Fatalf("the fund took %d, want one premium %d", got, product.Premium)
	}

	// Past the waiting time, hurt and treated at the city hospital, twice
	// pressed: one treatment, one claim.
	w.advance(gametime.Scale(gameScale).RealWait(product.WaitingDuration()) + time.Second)
	stay := w.admit(p, 2*time.Hour)
	limits, _ := bank.NewLimits(1, 1_000_000_000)
	hosp := handlers.NewHealthHandler(w.uow, workIDs{t}, nil, w.registry, postgres.NewCityRepository(w.pool), gameScale,
		limits, time.Hour, w.now)
	press := w.meta(p, "health.treat")
	bankBefore := w.balance(application.AccountPlayerBank, p.ID)
	fundBefore := fund()
	for i := 0; i < 2; i++ {
		resp, err := hosp.Treat(ctx, press, handlers.HealthRequest{Provider: "city", Method: "card"})
		w.ok("treated at the hospital", resp, err)
	}
	var price int64
	if err := w.pool.Raw().QueryRow(ctx, `SELECT price FROM hospital_treatments WHERE stay_id = $1::uuid`, stay).Scan(&price); err != nil {
		t.Fatalf("no treatment: %v", err)
	}
	due, paid := finance.Claim(price, product.CoverBPS, product.MaxClaim, fundBefore)
	if n := w.count(`SELECT count(*) FROM insurance_claims WHERE player_id = $1::uuid AND source = $2 AND due = $3 AND paid = $4`,
		p.ID, "stay:"+stay, due, paid); n != 1 || paid <= 0 {
		t.Fatalf("%d claims for the stay paying %d of %d; want one, paid", n, paid, due)
	}
	// Paid by card, the claim back into the bank: the treatment cost the
	// patient only what the policy did not cover.
	if got := bankBefore - w.balance(application.AccountPlayerBank, p.ID); got != price-paid {
		t.Fatalf("the treatment cost the patient %d, want %d less the claim %d", got, price, paid)
	}
	if got := fundBefore - fund(); got != paid {
		t.Fatalf("the fund paid %d, want %d", got, paid)
	}

	// The next period charges the premium once more; the period it was
	// bought in was paid at purchase.
	w.settle()
	w.settle()
	if n := w.count(`SELECT count(*) FROM insurance_premiums ip JOIN insurance_policies i ON i.id = ip.policy_id
		WHERE i.player_id = $1::uuid`, p.ID); n != 2 {
		t.Fatalf("%d premiums after two periods, want 2 (one at purchase, one after)", n)
	}
	w.verify()
}
