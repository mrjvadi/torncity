//go:build integration

// Integration test of the player's governance screens against PostgreSQL:
// a demo player is appointed mayor by the operator's path, then drives the
// city hall, office, change and history screens through the real handler,
// resolver, SetPolicy, triggers and outbox. The seat is vacated and every row
// the test wrote is removed when it ends.
package tests

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

func TestGovernanceScreensAgainstPostgres(t *testing.T) {
	pool := requirePostgres(t)
	requireGovernance(t, pool)
	ctx := testCtx(t)
	raw := pool.Raw()

	recordContentBaseline(t, pool)
	pack := shippedPack(t)
	applyContent(t, pool, pack, "governance screens integration test")

	const cityCode = "calderis"
	admin := postgres.NewGovernanceAdmin(pool)
	city, err := admin.JurisdictionByCode(ctx, "city", cityCode)
	if err != nil {
		t.Fatalf("the %s jurisdiction: %v", cityCode, err)
	}
	if n := countRows(t, pool,
		`SELECT count(*) FROM offices WHERE jurisdiction_id = $1::uuid AND holder_player_id IS NOT NULL`, city.ID); n > 0 {
		t.Skipf("%s already has %d office holder(s); this test needs its seats vacant", cityCode, n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM policy_values WHERE jurisdiction_id = $1::uuid`, city.ID); n > 0 {
		t.Skipf("%s already has %d policy value(s); this test needs a clean city", cityCode, n)
	}

	players := postgres.NewPlayerRepository(pool, testDefaultLanguage)
	mayor, outsider := insertPlayer(t, pool), insertPlayer(t, pool)
	for _, p := range []*application.Player{mayor, outsider} {
		if err := players.SetLanguage(ctx, p.ID, "en"); err != nil {
			t.Fatal(err)
		}
	}
	var touched []string
	seatCleanup(t, pool, []string{city.ID}, &touched)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if _, err := raw.Exec(ctx, `DELETE FROM outbox WHERE subject = $1 AND payload->>'jurisdiction_id' = $2`,
			handlers.PolicyChangedSubject, city.ID); err != nil {
			t.Errorf("cleanup outbox: %v", err)
		}
	})

	now := time.Now().UTC().Truncate(time.Second)
	clock := now
	tick := func() time.Time { return clock }

	change, err := admin.Appoint(ctx, postgres.SeatChangeRequest{
		SeatRef:    postgres.SeatRef{OfficeCode: "mayor", JurisdictionKind: "city", JurisdictionCode: cityCode, Seat: 1},
		PlayerCode: mayor.PublicCode, Actor: "integration-test", Reason: "integration test", At: now,
	})
	if err != nil {
		t.Fatalf("appointing the demo mayor: %v", err)
	}
	touched = append(touched, change.After.ID)

	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	h := handlers.NewGovernanceHandler(
		postgres.NewUnitOfWork(pool, testDefaultLanguage),
		catalog,
		postgres.NewCityRepository(pool),
		postgres.NewGovernanceDirectory(pool),
		postgres.NewPolicyReader(pool, tick),
		handlers.GovernanceSteps{FineDivisor: 100, CoarseDivisor: 10},
		handlers.DefaultPageSize, time.Hour, tick,
	)

	metaFor := func(p *application.Player, command string) envelope.Metadata {
		m := validMeta(t)
		m.TelegramUserID = p.TelegramUserID
		m.Command = command
		m.Language = "en"
		return m
	}
	screen := func(resp *presenter.Response, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var b strings.Builder
		b.WriteString(resp.Text)
		if resp.Keyboard != nil {
			for _, row := range resp.Keyboard.Rows {
				for _, btn := range row {
					b.WriteString("\n[" + btn.Text + "|" + btn.CallbackData + "]")
				}
			}
		}
		return b.String()
	}
	mayorLabel := mayor.DisplayName + " (" + mayor.PublicCode + ")"
	counts := func() (values, changes, events int) {
		return countRows(t, pool, `SELECT count(*) FROM policy_values WHERE jurisdiction_id = $1::uuid`, city.ID),
			countRows(t, pool, `SELECT count(*) FROM policy_changes WHERE jurisdiction_id = $1::uuid`, city.ID),
			countRows(t, pool, `SELECT count(*) FROM outbox WHERE subject = $1 AND payload->>'jurisdiction_id' = $2`,
				handlers.PolicyChangedSubject, city.ID)
	}

	// --- the city hall names the mayor and the default tax ---
	got := screen(h.City(ctx, metaFor(outsider, "gov.city"), handlers.GovCityRequest{City: cityCode}))
	for _, want := range []string{"Mayor: " + mayorLabel, "Tax rate:", "· default", "gov:history:" + cityCode} {
		if !strings.Contains(got, want) {
			t.Errorf("city hall lacks %q:\n%s", want, got)
		}
	}

	// --- the office screen offers the mayor's policies ---
	got = screen(h.Office(ctx, metaFor(mayor, "gov.office")))
	if !strings.Contains(got, "gov:lever:city.tax_rate:"+cityCode) {
		t.Errorf("the office screen offers no tax change:\n%s", got)
	}

	tax := func(v string) handlers.GovLeverRequest {
		return handlers.GovLeverRequest{Lever: "city.tax_rate", Place: cityCode, Value: v}
	}

	// --- refusals: an outsider, a value out of range — nothing written ---
	got = screen(h.Set(ctx, metaFor(outsider, "gov.set"), tax("800")))
	if !strings.Contains(got, "Only the Mayor can change this policy") {
		t.Errorf("an outsider's change:\n%s", got)
	}
	got = screen(h.Set(ctx, metaFor(mayor, "gov.set"), tax("99999")))
	if !strings.Contains(got, "outside the allowed range") {
		t.Errorf("an out-of-range change:\n%s", got)
	}
	if v, c, e := counts(); v+c+e != 0 {
		t.Fatalf("refusals wrote %d values, %d changes, %d events", v, c, e)
	}

	// --- confirm, then set: one value, one public record, one event ---
	got = screen(h.Confirm(ctx, metaFor(mayor, "gov.confirm"), tax("800")))
	if !strings.Contains(got, "to 8%") || !strings.Contains(got, "gov:set:city.tax_rate:"+cityCode+":800") {
		t.Errorf("the confirmation:\n%s", got)
	}
	set := metaFor(mayor, "gov.set")
	got = screen(h.Set(ctx, set, tax("800")))
	if !strings.Contains(got, "to 8% in 24h") {
		t.Errorf("the announcement:\n%s", got)
	}
	if v, c, e := counts(); v != 1 || c != 1 || e != 1 {
		t.Fatalf("after the change: %d values, %d changes, %d events, want 1 each", v, c, e)
	}

	// --- a redelivery of the same press writes nothing ---
	screen(h.Set(ctx, set, tax("800")))
	if v, c, e := counts(); v != 1 || c != 1 || e != 1 {
		t.Errorf("after a redelivery: %d values, %d changes, %d events, want 1 each", v, c, e)
	}

	// --- inside the cooldown: refused with the wait, nothing written ---
	clock = now.Add(time.Hour)
	got = screen(h.Set(ctx, metaFor(mayor, "gov.set"), tax("900")))
	if !strings.Contains(got, "You can change it again in 2d 23h") {
		t.Errorf("a change inside the cooldown:\n%s", got)
	}
	if v, _, _ := counts(); v != 1 {
		t.Errorf("the cooldown refusal wrote: %d values", v)
	}

	// --- the city hall shows the change pending, the history records it ---
	got = screen(h.City(ctx, metaFor(outsider, "gov.city"), handlers.GovCityRequest{City: cityCode}))
	if !strings.Contains(got, "Changes to 8% in 23h") {
		t.Errorf("the pending change is not on the city hall:\n%s", got)
	}
	got = screen(h.History(ctx, metaFor(outsider, "gov.history"), handlers.GovHistoryRequest{City: cityCode}))
	if !strings.Contains(got, "→ 8%") || !strings.Contains(got, mayorLabel) {
		t.Errorf("the history:\n%s", got)
	}

	// --- after the notice the value is in force, set by the mayor ---
	clock = now.Add(25 * time.Hour)
	got = screen(h.City(ctx, metaFor(outsider, "gov.city"), handlers.GovCityRequest{City: cityCode}))
	if !strings.Contains(got, "Tax rate: 8% · set by "+mayorLabel) {
		t.Errorf("the value in force:\n%s", got)
	}
}
