//go:build integration

// Integration test of the charter (docs/adr/0044 section 6): the founder's office
// holds everything; the players create offices with permission sets; an act of the
// village asks one permission, not "are you the head"; the rails hold (nobody grants
// what they do not hold, an office manager always exists, the audit is append-only).
package tests

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/labor"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	vpres "github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
)

func TestCharterOfficesAndRails(t *testing.T) {
	e := newFoundingEnv(t)
	pool := e.pool
	ctx := testCtx(t)
	catalog, err := i18n.Load(filepath.Join("..", "configs", "locales"))
	if err != nil {
		t.Fatal(err)
	}
	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	clock := gametime.Clock{Epoch: e.clock.Now().Add(-100 * 24 * time.Hour), Scale: 1}
	village := handlers.NewVillageHandler(uow, workIDs{t}, catalog, staticContentSource{snap: loadTestContent(t)}, e.cache,
		postgres.NewCityRepository(pool), gametime.Scale(1),
		handlers.VillageRules{
			VillageGridLots: 5, TeachPeriod: time.Second, TeachRateBPS: 10_000,
			BaseSchoolCapacityBPS: 10_000, ScarcityKBPS: 10_000, ScarcityFloorBPS: 3_000, ScarcityCapBPS: 80_000, SellerBandBPS: 500,
			HomeCityCode: "support", MaterialMarkupBPS: 12_000, StockBaseCapacity: 60, MaterialBuyMax: 200,
			MaterialBuyPresets: []int64{5, 20, 50},
		}, time.Hour, e.clock.Now).
		WithLabor(labor.Default(), []int64{1, 2, 4}, []int64{100, 125, 150, 200}).
		WithStorage(handlers.StorageRules{Clock: clock, SpoilKeptBPS: 100, SpoilUnkeptBPS: 3000})
	meta, founder := e.group(t)
	foundVillage(t, pool, e.h, meta)
	var cityID string
	if err := pool.Raw().QueryRow(ctx, `SELECT id::text FROM cities WHERE founded_by_group_id = $1`, meta.TelegramChatID).Scan(&cityID); err != nil {
		t.Fatal(err)
	}

	resident := func() *application.Player {
		p := insertPlayer(t, pool)
		if _, err := pool.Raw().Exec(ctx, `UPDATE players SET city_id = $1::uuid, residence_city_id = $1::uuid WHERE id = $2::uuid`, cityID, p.ID); err != nil {
			t.Fatal(err)
		}
		cid := cityID
		p.CityID = &cid
		return p
	}
	code := func(p *application.Player) string {
		var c string
		if err := pool.Raw().QueryRow(ctx, `SELECT public_code FROM players WHERE id = $1::uuid`, p.ID).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	as := func(p *application.Player, command string) envelope.Metadata {
		m := asPlayer(meta, p)
		m.Command = command
		m.RequestID = "req_" + randomToken(t, 16)
		m.IdempotencyKey = "it-" + randomToken(t, 16)
		return m
	}
	view := func(p *application.Player) vpres.CharterView {
		t.Helper()
		resp, err := village.CharterView(ctx, as(p, "settlement.charter.view"))
		if err != nil {
			t.Fatal(err)
		}
		var v vpres.CharterView
		if err := presentation.DecodeView(resp.View, &v); err != nil {
			t.Fatalf("%v: %s", err, resp.Text)
		}
		return v
	}
	refusal := func(resp *presentation.Response) string {
		var v vpres.VillageRefusalView
		if resp != nil && resp.Screen == vpres.ScreenVillageRefusal && presentation.DecodeView(resp.View, &v) == nil {
			return v.Kind
		}
		return ""
	}
	save := func(p *application.Player, req handlers.VillageCharterRequest) string {
		t.Helper()
		resp, err := village.CharterOfficeSave(ctx, as(p, "settlement.charter.office.save"), req)
		if err != nil {
			t.Fatal(err)
		}
		return refusal(resp)
	}
	grants := func(ps ...string) []handlers.VillageCharterGrant {
		var out []handlers.VillageCharterGrant
		for _, p := range ps {
			out = append(out, handlers.VillageCharterGrant{Permission: p})
		}
		return out
	}
	take := func(p *application.Player) string {
		t.Helper()
		resp, err := village.StockTake(ctx, as(p, "settlement.stock.take"), handlers.VillageStockMoveRequest{Item: "timber", Qty: "1"})
		if err != nil {
			t.Fatal(err)
		}
		return refusal(resp)
	}

	// 1. The default charter: one founder's office, everything, held by the head.
	v := view(founder)
	if len(v.Offices) != 1 || !v.Offices[0].Founder || !v.Offices[0].Mine || len(v.Offices[0].Grants) != len(v.Permissions) {
		t.Fatalf("the default charter: %+v", v)
	}
	if !v.CanCreate || !v.CanEdit || !v.CanAppoint {
		t.Errorf("the founder's office cannot edit its own charter: %+v", v)
	}
	if n := e.count(t, `SELECT count(*) FROM charter_offices WHERE settlement_id = $1::uuid`, cityID); n != 0 {
		t.Errorf("reading wrote %d offices", n)
	}

	sheriffP, deputyP, strangerP := resident(), resident(), resident()
	t.Cleanup(func() {
		c := testCtx(t)
		_, _ = pool.Raw().Exec(c, `ALTER TABLE charter_audit DISABLE TRIGGER charter_audit_no_change`)
		_, _ = pool.Raw().Exec(c, `DELETE FROM charter_audit WHERE settlement_id = $1::uuid`, cityID)
		_, _ = pool.Raw().Exec(c, `ALTER TABLE charter_audit ENABLE TRIGGER charter_audit_no_change`)
		_, _ = pool.Raw().Exec(c, `DELETE FROM charter_seats WHERE office_id IN (SELECT id FROM charter_offices WHERE settlement_id = $1::uuid)`, cityID)
		_, _ = pool.Raw().Exec(c, `DELETE FROM charter_offices WHERE settlement_id = $1::uuid`, cityID)
	})
	// 2. A resident with no office may do nothing; the founder creates a sheriff who may take from the stock.
	if k := take(sheriffP); k != vpres.VillageNotOfficeHolder {
		t.Errorf("a plain resident took from the stock: %q", k)
	}
	if k := save(sheriffP, handlers.VillageCharterRequest{Title: "کلانتر", Grants: grants("storage.take")}); k != "" {
		// not an office holder: the refusal is the generic one
		if k != vpres.VillageNotOfficeHolder {
			t.Errorf("a resident created an office: %q", k)
		}
	}
	if k := save(founder, handlers.VillageCharterRequest{Title: "کلانتر", Seats: 1, Grants: grants("storage.take"), Acquisition: "appointment"}); k != "" {
		t.Fatalf("the founder could not create an office: %q", k)
	}
	if n := e.count(t, `SELECT count(*) FROM charter_offices WHERE settlement_id = $1::uuid`, cityID); n != 2 {
		t.Fatalf("%d offices after the first edit, want the founder's and the sheriff", n)
	}
	v = view(founder)
	var sheriff vpres.CharterOfficeView
	for _, o := range v.Offices {
		if o.Title == "کلانتر" {
			sheriff = o
		}
	}
	if sheriff.ID == "" || sheriff.Open != 1 {
		t.Fatalf("the sheriff's office: %+v", sheriff)
	}
	// the same title again, and a reserved one, are refused
	if k := save(founder, handlers.VillageCharterRequest{Title: " کلانتر ", Grants: grants("road.draw")}); k != vpres.CharterTitleTaken {
		t.Errorf("a duplicate title: %q", k)
	}
	if k := save(founder, handlers.VillageCharterRequest{Title: "ادمین", Grants: grants("road.draw")}); k != vpres.CharterTitle {
		t.Errorf("a reserved title: %q", k)
	}
	if k := save(founder, handlers.VillageCharterRequest{Title: "خیالی", Grants: grants("fly.away")}); k != vpres.CharterGrant {
		t.Errorf("an unknown permission: %q", k)
	}

	// 3. Appointing: the sheriff may take from the stock (and nothing else).
	appoint := func(by *application.Player, office string, who *application.Player) string {
		t.Helper()
		resp, err := village.CharterAppoint(ctx, as(by, "settlement.charter.appoint"), handlers.VillageCharterRequest{Office: office, Player: code(who)})
		if err != nil {
			t.Fatal(err)
		}
		return refusal(resp)
	}
	if k := appoint(strangerP, sheriff.ID, sheriffP); k != vpres.VillageNotOfficeHolder {
		t.Errorf("a resident appointed: %q", k)
	}
	if k := appoint(founder, sheriff.ID, sheriffP); k != "" {
		t.Fatalf("appointing the sheriff: %q", k)
	}
	if k := appoint(founder, sheriff.ID, deputyP); k != vpres.CharterSeatsFull {
		t.Errorf("a second sheriff in a one-seat office: %q", k)
	}
	if k := take(sheriffP); k == vpres.VillageNotOfficeHolder {
		t.Errorf("the sheriff may take from the stock, got %q", k)
	}
	if k := take(strangerP); k != vpres.VillageNotOfficeHolder {
		t.Errorf("a stranger took from the stock: %q", k)
	}
	if hold := view(sheriffP); hold.CanCreate || hold.CanAppoint || len(hold.Mine) != 1 || hold.Mine[0].Permission != "storage.take" {
		t.Errorf("what the sheriff holds: %+v", hold)
	}

	// 4. R1: a deputy who may create offices cannot hand out what the deputy lacks.
	if k := save(founder, handlers.VillageCharterRequest{Title: "معاون", Seats: 1, Grants: grants("office.create", "office.appoint", "storage.take")}); k != "" {
		t.Fatalf("creating the deputy's office: %q", k)
	}
	var deputy vpres.CharterOfficeView
	for _, o := range view(founder).Offices {
		if o.Title == "معاون" {
			deputy = o
		}
	}
	if k := appoint(founder, deputy.ID, deputyP); k != "" {
		t.Fatalf("appointing the deputy: %q", k)
	}
	if k := save(deputyP, handlers.VillageCharterRequest{Title: "راهدار", Grants: grants("road.draw")}); k != vpres.CharterNotHeld {
		t.Errorf("a deputy granted road.draw without holding it: %q", k)
	}
	if k := save(deputyP, handlers.VillageCharterRequest{Title: "انباردار", Grants: grants("storage.take")}); k != "" {
		t.Errorf("a deputy could not create an office inside their own powers: %q", k)
	}
	if k := appoint(deputyP, sheriff.ID, strangerP); k != vpres.CharterSeatsFull && k != "" {
		t.Errorf("the deputy appointing to the sheriff's office: %q", k)
	}

	// 5. R2 and the founder's office: it cannot be closed; a manager is always left.
	var founderOffice string
	for _, o := range view(founder).Offices {
		if o.Founder {
			founderOffice = o.ID
		}
	}
	closeOffice := func(by *application.Player, id string) string {
		t.Helper()
		resp, err := village.CharterOfficeClose(ctx, as(by, "settlement.charter.office.close"), handlers.VillageCharterRequest{Office: id})
		if err != nil {
			t.Fatal(err)
		}
		return refusal(resp)
	}
	if k := closeOffice(founder, founderOffice); k != vpres.CharterHeldOffice {
		t.Errorf("closing the founder's office: %q", k)
	}
	if k := closeOffice(founder, sheriff.ID); k != "" {
		t.Errorf("closing the sheriff's office: %q", k)
	}
	if k := take(sheriffP); k != vpres.VillageNotOfficeHolder {
		t.Errorf("a closed office still grants: %q", k)
	}

	// 6. Resign and dismiss.
	resp, err := village.CharterResign(ctx, as(deputyP, "settlement.charter.resign"), handlers.VillageCharterRequest{Office: deputy.ID})
	if err != nil || refusal(resp) != "" {
		t.Errorf("resigning: %v %q", err, refusal(resp))
	}
	if k := save(deputyP, handlers.VillageCharterRequest{Title: "کدخدا", Grants: grants("storage.take")}); k != vpres.VillageNotOfficeHolder {
		t.Errorf("a resigned deputy still creates offices: %q", k)
	}

	// 7. R3: everything above is in the log and the log cannot be changed.
	v = view(founder)
	if len(v.Audit) < 6 {
		t.Errorf("the log has %d lines", len(v.Audit))
	}
	if _, err := pool.Raw().Exec(ctx, `UPDATE charter_audit SET action = 'x' WHERE settlement_id = $1::uuid`, cityID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("the charter log could be changed: %v", err)
	}
	if _, err := pool.Raw().Exec(ctx, `DELETE FROM charter_audit WHERE settlement_id = $1::uuid`, cityID); err == nil {
		t.Error("the charter log could be deleted")
	}
}
