//go:build integration

// Integration test of the village treasury's faucets (migration 0052): the
// founding grant, the operator's backfill and top-up, and residents'
// donations, all provable by `admin economy verify`.
package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/operator"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

func treasuryOf(t *testing.T, pool *postgres.Pool, cityID string) int64 {
	t.Helper()
	var bal int64
	err := postgres.NewUnitOfWork(pool, testDefaultLanguage).Do(testCtx(t), func(ctx context.Context, tx application.Tx) error {
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, cityID)
		if err != nil {
			return err
		}
		b, err := tx.Ledger().Balance(ctx, acct.ID)
		bal = b.Minor()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return bal
}

func TestVillageTreasury(t *testing.T) {
	e := newFoundingEnv(t)
	ctx := testCtx(t)
	pool := e.pool
	const grant = 10_000

	// The ledger is append-only, so earlier runs' rows may be gone while
	// their ledger entries remain: the invariants are checked as changes.
	v0, err := postgres.NewEconomyAdmin(pool).VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !v0.Village {
		t.Skip("migration 0052 is not applied")
	}
	drift := func(v postgres.LedgerVerification) [3]int64 {
		i := v.VillageInvariants
		return [3]int64{i.GrantLedger - i.GrantRows, i.DonationLedger - i.DonationRows, i.TopupLedger - i.TopupRows}
	}

	// Village B is founded before the grant exists (a live village of an
	// older release); village A after it.
	metaB, founderB := e.group(t)
	foundVillage(t, pool, e.h, metaB)
	var cityB string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, metaB.TelegramChatID).Scan(&cityB); err != nil {
		t.Fatal(err)
	}
	if got := treasuryOf(t, pool, cityB); got != 0 {
		t.Fatalf("a village founded without a grant starts with %d, want 0", got)
	}

	e.h.WithFoundingGrant(grant)
	metaA, founderA := e.group(t)
	foundVillage(t, pool, e.h, metaA)
	var cityA string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, metaA.TelegramChatID).Scan(&cityA); err != nil {
		t.Fatal(err)
	}
	if got := treasuryOf(t, pool, cityA); got != grant {
		t.Fatalf("the founding grant: treasury = %d, want %d", got, grant)
	}

	// The verifier flags B as ungranted until the backfill.
	admin := postgres.NewEconomyAdmin(pool)
	v, err := admin.VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if v.Ungranted != v0.Ungranted+1 {
		t.Fatalf("verifier: ungranted %d, want %d (the one village founded before the grant)", v.Ungranted, v0.Ungranted+1)
	}

	// Backfill: grants B once, idempotent; a concurrent second run grants nothing.
	ops := operator.Ops{Pool: pool, Language: testDefaultLanguage}
	actor := operator.Actor{Name: "tester", Reason: "integration", At: time.Now()}
	done := make(chan []string, 2)
	for i := 0; i < 2; i++ {
		go func() {
			ids, _ := ops.BackfillSettlementGrants(context.Background(), grant, actor)
			done <- ids
		}()
	}
	total := len(<-done) + len(<-done)
	if total != 1 {
		t.Fatalf("two racing backfills granted %d villages, want exactly 1", total)
	}
	if got := treasuryOf(t, pool, cityB); got != grant {
		t.Fatalf("after backfill B has %d, want %d", got, grant)
	}
	again, err := ops.BackfillSettlementGrants(ctx, grant, actor)
	if err != nil || len(again) != 0 {
		t.Fatalf("a repeated backfill granted %v (%v)", again, err)
	}
	if got := treasuryOf(t, pool, cityB); got != grant {
		t.Fatalf("a repeated backfill changed the treasury to %d", got)
	}

	// Operator top-up.
	top, err := ops.GrantSettlement(ctx, cityA, 2_500, actor)
	if err != nil || top.Amount != 2_500 {
		t.Fatalf("GrantSettlement: %+v %v", top, err)
	}
	if _, err := ops.GrantSettlement(ctx, cityA, 0, actor); err == nil {
		t.Error("a zero top-up was accepted")
	}
	if _, err := ops.GrantSettlement(ctx, "00000000-0000-0000-0000-000000000000", 5, actor); err == nil {
		t.Error("a top-up of a settlement that does not exist was accepted")
	}
	if got := treasuryOf(t, pool, cityA); got != grant+2_500 {
		t.Fatalf("after the top-up A has %d", got)
	}

	// Donation by a resident of A (the founder lives there).
	village := handlers.NewVillageHandler(postgres.NewUnitOfWork(pool, testDefaultLanguage), workIDs{t}, nil,
		staticContentSource{snap: loadTestContent(t)}, e.cache, postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500, HomeCityCode: "support"},
		time.Hour, e.clock.Now).WithDonationRules(100, 100_000, []int64{250, 1000})
	grantCash(t, pool, founderA.ID, 5_000)
	dm := func(amount, confirm string) (*presenter.Response, error) {
		m := asPlayer(metaA, founderA)
		m.Command, m.Action = "settlement.donate", "donate"
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return rrm(m)(village.Donate(ctx, m, handlers.VillageDonateRequest{Amount: amount, Confirm: confirm}))
	}
	before := treasuryOf(t, pool, cityA)
	if r, err := dm("", ""); err != nil || !strings.Contains(r.Text, "village.donate.body") {
		t.Fatalf("donate menu: %+v %v", r, err)
	}
	if r, err := dm("1000", ""); err != nil || !strings.Contains(r.Text, "village.donate.ask_title") {
		t.Fatalf("donate confirm: %+v %v", r, err)
	}
	if got := treasuryOf(t, pool, cityA); got != before {
		t.Fatalf("asking to donate moved money: %d", got)
	}
	r, err := dm("1000", screens.ResidenceConfirm)
	if err != nil || !strings.Contains(r.Text, "village.donate.done_title") {
		t.Fatalf("donate: %+v %v", r, err)
	}
	if got := treasuryOf(t, pool, cityA); got != before+1000 {
		t.Fatalf("after the donation A has %d, want %d", got, before+1000)
	}
	// Out of range, and more than the donor has.
	for _, amount := range []string{"50", "100001", "abc"} {
		r, err := dm(amount, screens.ResidenceConfirm)
		if err != nil || strings.Contains(r.Text, "village.donate.done_title") {
			t.Errorf("donating %q was not refused: %+v %v", amount, r, err)
		}
	}
	if r, err := dm("100000", screens.ResidenceConfirm); err != nil || strings.Contains(r.Text, "village.donate.done_title") {
		t.Errorf("donating more than the cash was not refused: %+v %v", r, err)
	}
	// A player of another village cannot donate here.
	other := asPlayer(metaA, founderB)
	other.Command, other.Action = "settlement.donate", "donate"
	other.IdempotencyKey = "it-" + randomToken(t, 16)
	if r, err := rrm(other)(village.Donate(ctx, other, handlers.VillageDonateRequest{Amount: "500", Confirm: screens.ResidenceConfirm})); err != nil || strings.Contains(r.Text, "village.donate.done_title") {
		t.Errorf("a non-resident donated: %+v %v", r, err)
	}
	if got := treasuryOf(t, pool, cityA); got != before+1000 {
		t.Fatalf("refusals moved money: %d", got)
	}

	// The ledger and the rows agree, and nothing is ungranted.
	v, err = admin.VerifyLedger(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if drift(v) != drift(v0) || v.GrantMismatched != v0.GrantMismatched || v.DonationMismatched != v0.DonationMismatched ||
		v.TopupMismatched != v0.TopupMismatched {
		t.Errorf("the ledger and the rows drifted apart: before %+v, after %+v", v0.VillageInvariants, v.VillageInvariants)
	}
	if v.Ungranted != v0.Ungranted {
		t.Errorf("ungranted after the backfill = %d", v.Ungranted)
	}

	// The village is the home. A group with a village gets its overview with
	// Support's services a journey away; a group without one, the call to
	// found; a resident in a private chat, the village, run in its group.
	cities := postgres.NewCityRepository(pool)
	if c, err := cities.ByID(ctx, cityA); err != nil || c.Tier != "village" || c.IsCityTier() {
		t.Errorf("a founded village is not village tier: %+v %v", c, err)
	}
	if c, err := cities.ByCode(ctx, "support"); err != nil || !c.IsCityTier() {
		t.Errorf("Support is not city tier: %+v %v", c, err)
	}
	hm := asPlayer(metaA, founderA)
	hm.Command, hm.Action = "settlement.home", "home"
	home, err := rrm(hm)(village.Home(ctx, hm))
	if err != nil || !strings.Contains(home.Text, "village.support.title") || !strings.Contains(home.Text, "village.treasury") {
		t.Errorf("the group's home is not the village: %+v %v", home, err)
	}
	if resp, ok, err := village.HomeIfVillage(ctx, hm); err != nil || !ok || resp == nil {
		t.Errorf("HomeIfVillage: %v %v", ok, err)
	}
	empty, _ := e.group(t)
	empty.Command, empty.Action = "settlement.home", "home"
	if resp, ok, err := village.HomeIfVillage(ctx, empty); err != nil || ok || resp != nil {
		t.Errorf("a group with no village was offered a village: %v %v", ok, err)
	}
	if call, err := rrm(empty)(village.Home(ctx, empty)); err != nil || !strings.Contains(call.Text, "village.home.call_title") {
		t.Errorf("a group with no village is not offered founding: %+v %v", call, err)
	}
	pm := asPlayer(metaA, founderA)
	pm.ChatType, pm.TelegramChatID = "private", founderA.TelegramUserID
	pm.Command, pm.Action = "settlement.home", "home"
	if r, err := rrm(pm)(village.Home(ctx, pm)); err != nil || !strings.Contains(r.Text, "village.private_hint") {
		t.Errorf("a resident's private home is not the village: %+v %v", r, err)
	}
}
