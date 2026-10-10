//go:build integration

package tests

import (
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/presentation"
	plife "github.com/mrjvadi/torncity/internal/presentation/life"
)

// Care and medicine (ADR 0069): the health house gives first aid on a day it is held, the clinic treats for a fee, the apothecary
// makes the medicine from the herb garden's herbs, and a founded settlement is no longer offered a hospital it never built.

type careEnv struct {
	*craftEnv
	rules application.CareRules
}

func newCareEnv(t *testing.T) *careEnv {
	t.Helper()
	e := &careEnv{craftEnv: newCraftEnv(t)}
	e.rules = application.CareRules{RuleAt: e.clock.Now().Add(-30 * 24 * time.Hour), GraceDays: 14}
	t.Cleanup(func() {
		c := testCtx(t)
		for _, stmt := range []string{
			`ALTER TABLE village_treatments DISABLE TRIGGER village_treatments_append_only`,
			`ALTER TABLE hospital_treatments DISABLE TRIGGER hospital_treatments_append_only`,
			`DELETE FROM village_treatments WHERE settlement_id = $1::uuid`,
			`DELETE FROM hospital_treatments WHERE city_id = $1::uuid`,
			`ALTER TABLE village_treatments ENABLE TRIGGER village_treatments_append_only`,
			`ALTER TABLE hospital_treatments ENABLE TRIGGER hospital_treatments_append_only`,
			`DELETE FROM hospital_stays WHERE city_id = $1::uuid`,
		} {
			args := []any{}
			if containsArg(stmt) {
				args = append(args, e.cityID)
			}
			if _, err := e.pool.Raw().Exec(c, stmt, args...); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	return e
}

func containsArg(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '$' && s[i+1] == '1' {
			return true
		}
	}
	return false
}

// healthHandler is the health handler wired to the settlement, with the care rules of the env.
func (e *careEnv) healthHandler() *handlers.HealthHandler {
	e.t.Helper()
	limits, err := bank.NewLimits(1, 1_000_000_000)
	if err != nil {
		e.t.Fatal(err)
	}
	return handlers.NewHealthHandler(postgres.NewUnitOfWork(e.pool, testDefaultLanguage), workIDs{e.t}, nil,
		staticContentSource{snap: loadTestContent(e.t)}, postgres.NewCityRepository(e.pool), gameScale, limits, time.Hour, e.clock.Now).
		WithVillageCare(e.village, e.rules)
}

// hurt admits a resident to a hospital stay of the given city, admitted at the time given and ending after `left`.
func (e *careEnv) hurt(cityID string, admitted time.Time, left time.Duration) *application.Player {
	e.t.Helper()
	p := e.resident()
	ctx := testCtx(e.t)
	action := newUUID(e.t)
	e.t.Cleanup(func() {
		c := testCtx(e.t)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM hospital_stays WHERE player_id = $1::uuid`, p.ID)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM game_actions WHERE actor_id = $1::uuid`, p.ID)
		_, _ = e.pool.Raw().Exec(c, `DELETE FROM player_stats WHERE player_id = $1::uuid`, p.ID)
		purgeLedgerFor(e.t, e.pool, p.ID)
	})
	end := e.clock.Now().Add(left)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE players SET city_id = $2::uuid WHERE id = $1::uuid`, []any{p.ID, cityID}},
		{`INSERT INTO game_actions (id, action_type, actor_type, actor_id, status, started_at, finish_at)
		  VALUES ($1::uuid, 'health.discharge', 'player', $2::uuid, 'scheduled', $3, $4)`, []any{action, p.ID, admitted, end}},
		{`INSERT INTO hospital_stays (id, player_id, city_id, cause, status, health_in, health_out, admitted_at, ends_at, game_action_id)
		  VALUES (gen_random_uuid(), $1::uuid, $2::uuid, 'crime', 'admitted', 10, 60, $3, $4, $5::uuid)`, []any{p.ID, cityID, admitted, end, action}},
	} {
		if _, err := e.pool.Raw().Exec(ctx, q.sql, q.args...); err != nil {
			e.t.Fatal(err)
		}
	}
	return p
}

func (e *careEnv) hospital(h *handlers.HealthHandler, p *application.Player) plife.HospitalView {
	e.t.Helper()
	resp, err := h.Hospital(testCtx(e.t), e.as(p, "health.hospital", "hospital"))
	if err != nil {
		e.t.Fatal(err)
	}
	var v plife.HospitalView
	if err := presentation.DecodeView(resp.View, &v); err != nil {
		e.t.Fatalf("the hospital view: %v", err)
	}
	return v
}

// treat presses a provider with a method ("" first draws the confirmation) and returns the screen it answers.
func (e *careEnv) treat(h *handlers.HealthHandler, p *application.Player, provider, method string) *presentation.Response {
	e.t.Helper()
	resp, err := h.Treat(testCtx(e.t), e.as(p, "health.treat", "treat"), handlers.HealthRequest{Provider: provider, Method: method})
	if err != nil {
		e.t.Fatal(err)
	}
	return resp
}

func (e *careEnv) stayEnd(p *application.Player) time.Time {
	e.t.Helper()
	var end time.Time
	if err := e.pool.Raw().QueryRow(testCtx(e.t), `SELECT ends_at FROM hospital_stays WHERE player_id = $1::uuid AND status = 'admitted'`, p.ID).Scan(&end); err != nil {
		e.t.Fatal(err)
	}
	return end
}

func (e *careEnv) verifyCare() {
	e.t.Helper()
	v, err := postgres.NewEconomyAdmin(e.pool).VerifyLedger(testCtx(e.t), 10)
	if err != nil {
		e.t.Fatal(err)
	}
	s := v.VillageInvariants
	if !s.Care || s.CareFeeLedger != s.CareFeeRows || s.CareMedicineJournal != s.CareMedicineRows || s.CareBroken != 0 {
		e.t.Errorf("the care invariants do not hold: %+v", s)
	}
	e.verify()
}

// The health house gives first aid on a day it is held: one bandage of the settlement's stock, 15 percent off the stay, free
// for the patient; once per stay; and with the house closed or the shelf bare it says what is missing.
func TestTheHealthHouseGivesFirstAidOnItsHeldDay(t *testing.T) {
	e := newCareEnv(t)
	h := e.healthHandler()
	house := e.building("health_house")
	e.stock("cloth", 5)
	e.stock("spring_water", 10)
	patient := e.hurt(e.cityID, e.clock.Now(), 10*time.Minute)

	// no bandage in the stock: the house is open but cannot treat, and the view says what to do
	v := e.hospital(h, patient)
	if !v.Founded || v.CityHospital != nil || len(v.Village) != 1 || v.Village[0].Provider != application.ProviderHouse {
		t.Fatalf("a settlement offers its own care and no city hospital: %+v", v)
	}
	if !v.Village[0].Open || v.Village[0].CanTreat || v.Care == nil || !v.Care.NoMedicine || v.Care.Apothecary.Code != "apothecary" || v.Care.Clinic.Present {
		t.Fatalf("an open house with an empty shelf: %+v / %+v", v.Village[0], v.Care)
	}
	if r := e.treat(h, patient, application.ProviderHouse, ""); r.Refusal == nil {
		t.Fatalf("no bandage, no first aid: %+v", r)
	}

	e.stock("bandage", 2)
	end0 := e.stayEnd(patient)
	if r := e.treat(h, patient, application.ProviderHouse, ""); r.Refusal != nil {
		t.Fatalf("the confirmation was refused: %+v", r.Refusal)
	}
	if got := e.held("bandage"); got != 2 {
		t.Fatalf("the confirmation uses nothing: %d bandages", got)
	}
	e.treat(h, patient, application.ProviderHouse, "free")
	if e.held("bandage") != 1 {
		t.Errorf("first aid takes one bandage of the stock: %d left of 2", e.held("bandage"))
	}
	end1 := e.stayEnd(patient)
	left0, left1 := end0.Sub(e.clock.Now()), end1.Sub(e.clock.Now())
	if want := left0 * 85 / 100; left1 < want-2*time.Second || left1 > want+2*time.Second {
		t.Errorf("15 percent off the stay: %s left of %s, want about %s", left1, left0, want)
	}
	if e.scalar(`SELECT count(*) FROM village_treatments WHERE settlement_id = $1::uuid AND kind = 'health_house' AND fee = 0 AND medicine_item = 'bandage' AND building_id = $2::uuid`, e.cityID, house) != 1 {
		t.Error("the treatment is a row of its own with the building and the medicine")
	}
	if e.scalar(`SELECT count(*) FROM item_movements WHERE reason = 'medicine_used' AND from_org = $1::uuid`, e.cityID) != 1 {
		t.Error("the bandage left through the item journal as medicine_used")
	}
	// a second press is the same treatment, not a second one
	e.treat(h, patient, application.ProviderHouse, "free")
	if e.held("bandage") != 1 || e.scalar(`SELECT count(*) FROM village_treatments WHERE settlement_id = $1::uuid`, e.cityID) != 1 {
		t.Errorf("one treatment per stay: %d bandages", e.held("bandage"))
	}
	e.verifyCare()
}

// The clinic treats with a painkiller or a first aid kit from the stock for a fee into the treasury (hospital_fee): a kit takes
// 35 percent off, painkillers 25; a closed clinic or an empty shelf is refused.
func TestTheClinicTreatsForAFeeIntoTheTreasury(t *testing.T) {
	e := newCareEnv(t)
	h := e.healthHandler()
	e.building("health_house")
	clinic := e.building("clinic")
	e.stock("cloth", 6)
	e.stock("spring_water", 12)
	e.stock("painkillers", 1)
	e.stock("first_aid_kit", 1)
	patient := e.hurt(e.cityID, e.clock.Now(), 10*time.Minute)
	grant(t, e.pool, application.AccountPlayerBank, patient.ID, 50_000)

	v := e.hospital(h, patient)
	var vc *plife.TreatOption
	for i := range v.Village {
		if v.Village[i].Provider == application.ProviderVillageClinic {
			vc = &v.Village[i]
		}
	}
	if vc == nil || !vc.Open || !vc.CanTreat || vc.Medicine != "first_aid_kit" || vc.Price <= 0 {
		t.Fatalf("an open clinic with a kit on the shelf: %+v", v.Village)
	}
	treasury0 := e.treasury()
	end0 := e.stayEnd(patient)
	if r := e.treat(h, patient, application.ProviderVillageClinic, ""); r.Refusal != nil {
		t.Fatalf("the confirmation was refused: %+v", r.Refusal)
	}
	e.treat(h, patient, application.ProviderVillageClinic, "card")
	if e.held("first_aid_kit") != 0 || e.held("painkillers") != 1 {
		t.Errorf("the better medicine goes first: kit %d, painkillers %d", e.held("first_aid_kit"), e.held("painkillers"))
	}
	// the day was judged (and its wage paid) when the view was read; the treasury gained exactly the fee
	if got := e.treasury() - treasury0; got != vc.Price {
		t.Errorf("the fee reached the treasury: +%d (fee %d)", got, vc.Price)
	}
	if e.scalar(`SELECT fee FROM village_treatments WHERE settlement_id = $1::uuid AND kind = 'village_clinic' AND building_id = $2::uuid`, e.cityID, clinic) != vc.Price {
		t.Error("the village treatment records the fee")
	}
	left0, left1 := end0.Sub(e.clock.Now()), e.stayEnd(patient).Sub(e.clock.Now())
	if want := left0 * 65 / 100; left1 < want-2*time.Second || left1 > want+2*time.Second {
		t.Errorf("a kit takes 35 percent off: %s left of %s, want about %s", left1, left0, want)
	}

	// the next patient meets painkillers (25 percent) and, once they are gone, an empty shelf
	second := e.hurt(e.cityID, e.clock.Now(), 10*time.Minute)
	grant(t, e.pool, application.AccountPlayerBank, second.ID, 50_000)
	e.treat(h, second, application.ProviderVillageClinic, "card")
	if e.held("painkillers") != 0 {
		t.Errorf("the painkillers were used: %d", e.held("painkillers"))
	}
	third := e.hurt(e.cityID, e.clock.Now(), 10*time.Minute)
	grant(t, e.pool, application.AccountPlayerBank, third.ID, 50_000)
	if r := e.treat(h, third, application.ProviderVillageClinic, "card"); r.Refusal == nil {
		t.Fatalf("an empty shelf treats nobody: %+v", r)
	}
	if e.scalar(`SELECT count(*) FROM village_treatments WHERE settlement_id = $1::uuid`, e.cityID) != 2 {
		t.Error("two treatments, two rows")
	}
	e.verifyCare()
}

// A clinic with nobody to staff it (the treasury cannot pay the day's wage) is closed, and says so.
func TestAClosedClinicTreatsNobody(t *testing.T) {
	e := newCareEnv(t)
	h := e.healthHandler()
	e.building("clinic")
	e.stock("cloth", 6)
	e.stock("spring_water", 12)
	e.stock("painkillers", 2)
	e.drainTreasury()
	patient := e.hurt(e.cityID, e.clock.Now(), 10*time.Minute)
	grant(t, e.pool, application.AccountPlayerBank, patient.ID, 50_000)
	v := e.hospital(h, patient)
	if len(v.Village) != 1 || v.Village[0].Open || v.Village[0].Idle != "no_wage" || v.Care == nil {
		t.Fatalf("a clinic nobody can pay is closed and says why: %+v / %+v", v.Village, v.Care)
	}
	if r := e.treat(h, patient, application.ProviderVillageClinic, "card"); r.Refusal == nil {
		t.Fatalf("a closed clinic treats nobody: %+v", r)
	}
	if e.held("painkillers") != 2 {
		t.Errorf("nothing was used: %d painkillers", e.held("painkillers"))
	}
	e.verifyCare()
}

// The herb garden grows herbs, the apothecary makes extract from them and then painkillers, bandages and a first aid kit; each
// link missing says which one; the shifts train herbalism.
func TestTheApothecaryMakesPainkillersFromTheGardensHerbs(t *testing.T) {
	e := newCareEnv(t)
	hand := e.resident()
	garden := e.placeAt("herb_garden", 40, 0)
	apothecary := e.placeAt("apothecary", 40, 10)
	e.placeAt("storehouse", 70, 25) // room for the goods the chain makes
	e.stock("spring_water", 20)
	e.stock("firewood", 6)
	e.stock("cloth", 6)
	e.stock("pots", 2)
	e.stock("tools", 4)

	// the settlement has not researched herbalism: the bench offers nothing
	if got := e.shiftWith(hand, apothecary, "painkillers"); got == "" {
		t.Fatal("painkillers need herbalism")
	}
	e.grant("basic_medicine")
	e.grant("herbalism")
	// no herbs yet: the standard shift stands idle (the crew cannot start)
	if got := e.shiftWith(hand, apothecary, ""); got == "" {
		t.Fatal("no herbs, no extract")
	}
	if got := e.shiftWith(hand, garden, ""); got != "" {
		t.Fatalf("a shift in the herb garden: refused as %q", got)
	}
	// a new hand works at the pace of his level (about 70 percent): a shift makes a few of the 6 herbs
	if e.held("herbs") < 3 || e.held("spring_water") != 19 {
		t.Fatalf("the beds were watered and made herbs: herbs %d, water %d", e.held("herbs"), e.held("spring_water"))
	}
	if got := e.shiftWith(hand, garden, ""); got != "" {
		t.Fatalf("a second shift in the garden: %q", got)
	}
	herbs0 := e.held("herbs")
	if herbs0 < 6 {
		t.Fatalf("two shifts of the garden: %d herbs", herbs0)
	}
	if got := e.shiftWith(hand, apothecary, ""); got != "" {
		t.Fatalf("the apothecary's standard shift: refused as %q", got)
	}
	if e.held("herb_extract") < 1 || e.held("herbs") != herbs0-4 || e.held("firewood") != 5 {
		t.Fatalf("herbs 4, water and a firewood make extract: extract %d, herbs %d (was %d), firewood %d", e.held("herb_extract"), e.held("herbs"), herbs0, e.held("firewood"))
	}
	e.stock("herb_extract", 6)
	if got := e.shiftWith(hand, apothecary, "painkillers"); got != "" {
		t.Fatalf("painkillers from extract and cloth: refused as %q", got)
	}
	if e.held("painkillers") <= 0 || e.held("herb_extract") >= 4 {
		t.Errorf("the extract became painkillers: extract %d, painkillers %d", e.held("herb_extract"), e.held("painkillers"))
	}
	e.stock("bandage", 6)
	if got := e.shiftWith(hand, apothecary, "first_aid_kit"); got != "" {
		t.Fatalf("a first aid kit from bandages, painkillers and a pot: refused as %q", got)
	}
	if e.held("first_aid_kit") <= 0 {
		t.Error("the kit is in the store")
	}
	if e.scalar(`SELECT COALESCE(SUM(xp), 0)::bigint FROM player_skills WHERE player_id = $1::uuid AND skill_code = 'herbalism'`, hand.ID) <= 0 {
		t.Error("the shifts teach herbalism")
	}
	e.verify()
}

// After the rule date plus the grace a founded settlement is no longer offered the city hospital; a stay admitted before keeps it;
// a content city keeps its hospital.
func TestTheConjuredHospitalIsGoneButContentCitiesKeepTheirs(t *testing.T) {
	e := newCareEnv(t)
	h := e.healthHandler()
	now := e.clock.Now()

	fresh := e.hurt(e.cityID, now, 10*time.Minute)
	v := e.hospital(h, fresh)
	if v.CityHospital != nil || v.Care == nil || !v.Care.CityHospitalGone || v.Care.House.Present {
		t.Fatalf("a settlement with no care built: the city hospital is gone and the head is told what to build: %+v / %+v", v.CityHospital, v.Care)
	}
	if v.Care.House.Building.Code != "health_house" || v.Care.Refer.Code != "support" {
		t.Errorf("it names the house to build and the central city: %+v", v.Care)
	}
	if r := e.treat(h, fresh, application.ProviderCity, "cash"); r.Refusal == nil {
		t.Fatalf("the conjured hospital treats nobody: %+v", r)
	}

	// a stay that began before the grace is over keeps what it was offered
	old := e.hurt(e.cityID, e.rules.GraceUntil().Add(-time.Hour), 10*time.Minute)
	if v := e.hospital(h, old); v.CityHospital == nil {
		t.Fatalf("a running stay keeps the city hospital: %+v", v)
	}

	// the neutral city has a hospital of its own
	var support string
	if err := e.pool.Raw().QueryRow(testCtx(t), `SELECT id::text FROM cities WHERE code = 'support'`).Scan(&support); err != nil {
		t.Fatal(err)
	}
	inSupport := e.hurt(support, now, 10*time.Minute)
	if v := e.hospital(h, inSupport); v.CityHospital == nil || v.Care != nil || v.Founded {
		t.Fatalf("a content city keeps its hospital: %+v", v)
	}
	e.verifyCare()
}
