//go:build integration

// Integration tests for player-held offices (migration 0008,
// docs/adr/0015-player-held-offices.md).
//
// What is proved here is what lives in PostgreSQL: the migration and its
// backfill, a content load creating one vacant seat per office, jurisdiction
// and seat — and not one more on a reload — the audited appointment, the
// resolver's precedence over real rows, the refusals writing nothing, and the
// triggers that guard the public record and the operator's bounds.
//
// The tests load the shipped content into the shared database. The content
// baseline (recordContentBaseline, extended by recordGovernanceBaseline below)
// puts back every row a load wrote; the tests remove the seats they filled
// and the policy rows they wrote, the append-only ones by disabling their
// trigger inside one transaction, as the ledger tests do.
package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// requireGovernance skips unless migration 0008 is applied.
func requireGovernance(t *testing.T, pool *postgres.Pool) {
	t.Helper()
	var exists bool
	if err := pool.Raw().QueryRow(testCtx(t),
		`SELECT to_regclass('public.policy_values') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("checking for governance tables: %v", err)
	}
	if !exists {
		t.Skip("policy_values does not exist; apply migration 0008_governance first")
	}
}

// ---------------------------------------------------------------------------
// The content baseline, governance part.
// ---------------------------------------------------------------------------

type baselineLevel struct {
	code             string
	parents          []string
	overlay          bool
	contentVersionID *string
}

type baselineJurisdiction struct {
	id, name         string
	parentID         *string
	contentVersionID *string
}

// governanceBaseline is what a content load can change in the governance
// tables, as it was before the test.
type governanceBaseline struct {
	present       bool
	levels        []baselineLevel
	jurisdictions []baselineJurisdiction
	offices       []string // seat ids
}

// recordGovernanceBaseline snapshots the rows a load upserts or creates. It is
// called by recordContentBaseline, whose cleanup calls restore.
func recordGovernanceBaseline(t *testing.T, pool *postgres.Pool) governanceBaseline {
	t.Helper()
	ctx := testCtx(t)
	raw := pool.Raw()

	var b governanceBaseline
	if err := raw.QueryRow(ctx, `SELECT to_regclass('public.jurisdiction_levels') IS NOT NULL`).Scan(&b.present); err != nil {
		t.Fatalf("checking for governance tables: %v", err)
	}
	if !b.present {
		return b
	}

	rows, err := raw.Query(ctx, `SELECT code, parents, overlay, content_version_id::text FROM jurisdiction_levels`)
	if err != nil {
		t.Fatalf("reading levels: %v", err)
	}
	for rows.Next() {
		var l baselineLevel
		if err := rows.Scan(&l.code, &l.parents, &l.overlay, &l.contentVersionID); err != nil {
			rows.Close()
			t.Fatalf("scanning level: %v", err)
		}
		b.levels = append(b.levels, l)
	}
	rows.Close()

	rows, err = raw.Query(ctx, `SELECT id::text, name, parent_id::text, content_version_id::text FROM jurisdictions`)
	if err != nil {
		t.Fatalf("reading jurisdictions: %v", err)
	}
	for rows.Next() {
		var j baselineJurisdiction
		if err := rows.Scan(&j.id, &j.name, &j.parentID, &j.contentVersionID); err != nil {
			rows.Close()
			t.Fatalf("scanning jurisdiction: %v", err)
		}
		b.jurisdictions = append(b.jurisdictions, j)
	}
	rows.Close()

	if err := raw.QueryRow(ctx, `SELECT COALESCE(array_agg(id::text), '{}') FROM offices`).Scan(&b.offices); err != nil {
		t.Fatalf("reading offices: %v", err)
	}
	return b
}

// restore puts the governance tables back. It runs inside the content
// baseline's cleanup, after the cities are restored and before the created
// cities and versions are deleted, so it first detaches those cities from the
// jurisdictions it is about to remove.
func (b governanceBaseline) restore(t *testing.T, exec func(what, sql string, args ...any), created []string) {
	t.Helper()
	if !b.present {
		return
	}
	jurisdictionIDs := make([]string, 0, len(b.jurisdictions))
	for _, j := range b.jurisdictions {
		jurisdictionIDs = append(jurisdictionIDs, j.id)
	}
	levelCodes := make([]string, 0, len(b.levels))
	for _, l := range b.levels {
		levelCodes = append(levelCodes, l.code)
	}

	exec("deleting seats the loads created",
		`DELETE FROM offices WHERE NOT (id = ANY($1::uuid[]))`, b.offices)
	exec("deleting lever definitions", `DELETE FROM lever_definitions WHERE content_version_id = ANY($1::uuid[])`, created)
	exec("deleting office definitions", `DELETE FROM office_definitions WHERE content_version_id = ANY($1::uuid[])`, created)
	exec("detaching created cities",
		`UPDATE cities SET jurisdiction_id = NULL WHERE content_version_id = ANY($1::uuid[])`, created)

	for _, j := range b.jurisdictions {
		exec("restoring jurisdiction "+j.id,
			`UPDATE jurisdictions SET name = $2, parent_id = $3::uuid, content_version_id = $4::uuid WHERE id = $1::uuid`,
			j.id, j.name, j.parentID, j.contentVersionID)
	}
	// Children before parents: delete leaves until none of the new rows is
	// left. The depth of the tree bounds the passes.
	for pass := 0; pass < 16; pass++ {
		exec("deleting jurisdictions the loads created",
			`DELETE FROM jurisdictions j
			  WHERE NOT (j.id = ANY($1::uuid[]))
			    AND NOT EXISTS (SELECT 1 FROM jurisdictions c WHERE c.parent_id = j.id)`, jurisdictionIDs)
	}

	for _, l := range b.levels {
		exec("restoring level "+l.code,
			`UPDATE jurisdiction_levels SET parents = $2, overlay = $3, content_version_id = $4::uuid WHERE code = $1`,
			l.code, l.parents, l.overlay, l.contentVersionID)
	}
	exec("deleting levels the loads created",
		`DELETE FROM jurisdiction_levels WHERE NOT (code = ANY($1::text[]))`, levelCodes)
}

// ---------------------------------------------------------------------------
// The migration.
// ---------------------------------------------------------------------------

// TestGovernanceMigrationBackfillsAndReverses applies 0001–0008 to a scratch
// schema holding a world loaded BEFORE governance existed, and checks the
// backfill gives every city a country — without which no service could boot
// on that world — then rolls 0008 back and forward again.
func TestGovernanceMigrationBackfillsAndReverses(t *testing.T) {
	conn, _ := scratchSchema(t)
	ctx := testCtx(t)

	applyMigrations(t, conn, append(append([]string{}, migrationsBeforeCodes...), "0007_player_public_code.up.sql")...)

	versionID := newUUID(t)
	if _, err := conn.Exec(ctx,
		`INSERT INTO content_versions (id, version, loaded_at, loaded_by, source_checksum, status, notes)
		 VALUES ($1::uuid, 1, now(), 'it', 'x', 'active', 'pre-governance world')`, versionID); err != nil {
		t.Fatalf("seeding a version: %v", err)
	}
	for _, code := range []string{"alpha", "bravo"} {
		if _, err := conn.Exec(ctx,
			`INSERT INTO cities (id, code, name, tax_rate_bps, cost_of_living, content_version_id)
			 VALUES ($1::uuid, $2, $2, 300, 1000, $3::uuid)`, newUUID(t), code, versionID); err != nil {
			t.Fatalf("seeding %s: %v", code, err)
		}
	}

	up, down := migrationFile(t, "0008_governance.up.sql"), migrationFile(t, "0008_governance.down.sql")
	for round := 1; round <= 2; round++ {
		if _, err := conn.Exec(ctx, up); err != nil {
			t.Fatalf("round %d: applying 0008: %v", round, err)
		}

		var linked, underCountry int
		if err := conn.QueryRow(ctx,
			`SELECT count(c.id), count(p.id) FILTER (WHERE p.kind = 'country' AND p.code = 'default_country')
			   FROM cities c
			   JOIN jurisdictions j ON j.id = c.jurisdiction_id AND j.kind = 'city' AND j.code = c.code
			   JOIN jurisdictions p ON p.id = j.parent_id`).Scan(&linked, &underCountry); err != nil {
			t.Fatalf("round %d: reading the backfill: %v", round, err)
		}
		if linked != 2 || underCountry != 2 {
			t.Errorf("round %d: %d cities linked, %d under default_country; want 2 and 2", round, linked, underCountry)
		}
		var levels int
		if err := conn.QueryRow(ctx,
			`SELECT count(*) FROM jurisdiction_levels WHERE content_version_id = $1::uuid`, versionID).Scan(&levels); err != nil {
			t.Fatal(err)
		}
		if levels != 2 {
			t.Errorf("round %d: the active version has %d levels, want city and country", round, levels)
		}

		// A level is a foreign key, not a CHECK list: an undeclared one is
		// refused, a newly declared one needs no migration.
		_, err := conn.Exec(ctx, `INSERT INTO jurisdictions (id, kind, code, name, parent_id)
			VALUES ($1::uuid, 'province', 'p', 'p', '00000000-0000-4000-8000-000000000100')`, newUUID(t))
		if err == nil {
			t.Errorf("round %d: a jurisdiction of an undeclared level was accepted", round)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO jurisdiction_levels (code, parents, overlay) VALUES ('province', '{world}', false)`); err != nil {
			t.Fatalf("round %d: declaring a level: %v", round, err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO jurisdictions (id, kind, code, name, parent_id)
			VALUES ($1::uuid, 'province', 'p', 'p', '00000000-0000-4000-8000-000000000100')`, newUUID(t)); err != nil {
			t.Errorf("round %d: a jurisdiction of a declared level was refused: %v", round, err)
		}

		if _, err := conn.Exec(ctx, down); err != nil {
			t.Fatalf("round %d: rolling 0008 back: %v", round, err)
		}
	}
}

// ---------------------------------------------------------------------------
// The whole path: load, appoint, set, resolve.
// ---------------------------------------------------------------------------

// seatCleanup vacates the named seats and removes every policy row of the
// jurisdictions when the test ends. Register it AFTER the players it seats,
// so it runs before they are deleted.
func seatCleanup(t *testing.T, pool *postgres.Pool, jurisdictionIDs []string, seatIDs *[]string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		tx, err := pool.Raw().Begin(ctx)
		if err != nil {
			t.Errorf("cleanup: begin: %v", err)
			return
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
		places, filled := jurisdictionIDs, *seatIDs
		for _, step := range []struct {
			sql  string
			args []any
		}{
			{`ALTER TABLE policy_changes DISABLE TRIGGER policy_changes_append_only`, nil},
			{`DELETE FROM policy_changes WHERE jurisdiction_id = ANY($1::uuid[])`, []any{places}},
			{`DELETE FROM policy_values WHERE jurisdiction_id = ANY($1::uuid[])`, []any{places}},
			{`ALTER TABLE policy_changes ENABLE TRIGGER policy_changes_append_only`, nil},
			{`UPDATE offices SET holder_player_id = NULL, acquired_by = NULL, term_ends_at = NULL
			   WHERE id = ANY($1::uuid[])`, []any{filled}},
			{`DELETE FROM audit_logs WHERE target_type = 'office' AND target_id = ANY($1::uuid[])
			     AND actor = 'integration-test'`, []any{filled}},
		} {
			if _, err := tx.Exec(ctx, step.sql, step.args...); err != nil {
				t.Errorf("cleanup %q: %v", firstLine(step.sql), err)
				return
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("cleanup: commit: %v", err)
		}
	})
}

func TestGovernanceLoadAppointSetResolve(t *testing.T) {
	pool := requirePostgres(t)
	requireGovernance(t, pool)
	ctx := testCtx(t)
	raw := pool.Raw()

	recordContentBaseline(t, pool)
	pack := shippedPack(t)

	// --- content load: one vacant seat per office, jurisdiction and seat ---
	applyContent(t, pool, pack, "governance integration test, first load")
	second := applyContent(t, pool, pack, "governance integration test, reload")
	if second.OfficesCreated != 0 {
		t.Errorf("a reload of unchanged content created %d seats, want 0", second.OfficesCreated)
	}
	wantSeats := 0
	for _, o := range pack.Offices {
		per := 0
		switch o.Jurisdiction {
		case "city":
			per = len(pack.Cities)
		default:
			for _, j := range pack.Jurisdictions {
				if j.Level == o.Jurisdiction {
					per++
				}
			}
		}
		wantSeats += per * o.Seats
	}
	var seats int
	if err := raw.QueryRow(ctx,
		`SELECT count(*) FROM offices o JOIN jurisdictions j ON j.id = o.jurisdiction_id
		  JOIN content_versions cv ON cv.id = j.content_version_id AND cv.status = 'active'`).Scan(&seats); err != nil {
		t.Fatal(err)
	}
	if seats != wantSeats {
		t.Errorf("the active world has %d seats, want %d (every office × jurisdiction × seat, once)", seats, wantSeats)
	}

	const cityCode = "kessmoor"
	admin := postgres.NewGovernanceAdmin(pool)
	city, err := admin.JurisdictionByCode(ctx, "city", cityCode)
	if err != nil {
		t.Fatalf("the %s jurisdiction: %v", cityCode, err)
	}
	var held int
	if err := raw.QueryRow(ctx,
		`SELECT count(*) FROM offices WHERE jurisdiction_id = $1::uuid AND holder_player_id IS NOT NULL`, city.ID).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if held > 0 {
		t.Skipf("%s already has %d office holder(s) in this database; this test needs its seats vacant", cityCode, held)
	}

	mayor, deputy, outsider := insertPlayer(t, pool), insertPlayer(t, pool), insertPlayer(t, pool)
	var touchedSeats []string
	seatCleanup(t, pool, []string{city.ID}, &touchedSeats)

	// --- the resolver on a vacant office: the city's own default, no error ---
	now := time.Now().UTC().Truncate(time.Second)
	clock := now
	reader := postgres.NewPolicyReader(pool, func() time.Time { return clock })
	cityDef, _ := pack.Lever("city.tax_rate")
	var kessmoor int64
	for _, c := range pack.Cities {
		if c.Code == cityCode {
			kessmoor = cityDef.DefaultFor(c)
		}
	}
	v, err := reader.Get(ctx, city.ID, "city.tax_rate")
	if err != nil {
		t.Fatalf("reading tax with the office vacant: %v", err)
	}
	if v.Value != kessmoor || v.Source != application.PolicyFromDefault || v.Acting != nil {
		t.Errorf("vacant: %d from %s acting %+v, want %d from the default and nobody acting", v.Value, v.Source, v.Acting, kessmoor)
	}

	// --- appoint, audited ---
	appoint := func(office string, p *application.Player) postgres.SeatChange {
		t.Helper()
		change, err := admin.Appoint(ctx, postgres.SeatChangeRequest{
			SeatRef:    postgres.SeatRef{OfficeCode: office, JurisdictionKind: "city", JurisdictionCode: cityCode, Seat: 1},
			PlayerCode: p.PublicCode, Actor: "integration-test", Reason: "integration test", At: now,
		})
		if err != nil {
			t.Fatalf("appointing %s: %v", office, err)
		}
		touchedSeats = append(touchedSeats, change.After.ID)
		return change
	}
	seat := appoint("mayor", mayor)
	if seat.After.HolderPlayerID != mayor.ID || seat.After.AcquiredBy != "appointment" {
		t.Errorf("appointed seat %+v", seat.After)
	}
	if n := countRows(t, pool,
		`SELECT count(*) FROM audit_logs WHERE action = 'office.appoint' AND target_id = $1::uuid`, seat.After.ID); n != 1 {
		t.Errorf("the appointment left %d audit rows, want 1", n)
	}

	// Incompatible offices are refused (the mayor may not sit on the council).
	_, err = admin.Appoint(ctx, postgres.SeatChangeRequest{
		SeatRef:    postgres.SeatRef{OfficeCode: "city_council", JurisdictionKind: "city", JurisdictionCode: cityCode, Seat: 1},
		PlayerCode: mayor.PublicCode, Actor: "integration-test", Reason: "integration test", At: now,
	})
	if !errors.Is(err, application.ErrIncompatibleOffices) {
		t.Errorf("mayor onto the council: %v, want ErrIncompatibleOffices", err)
	}

	uow := postgres.NewUnitOfWork(pool, testDefaultLanguage)
	set := func(p *application.Player, value int64, at time.Time) (application.PolicyChange, error) {
		var c application.PolicyChange
		err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
			var err error
			c, err = application.SetPolicy(ctx, tx, p.ID, city.ID, "city.tax_rate", value, at)
			return err
		})
		return c, err
	}
	values := func() int {
		return countRows(t, pool, `SELECT count(*) FROM policy_values WHERE jurisdiction_id = $1::uuid`, city.ID)
	}

	// --- refusals write nothing ---
	for _, tc := range []struct {
		name  string
		who   *application.Player
		value int64
		want  error
	}{
		{"not the holder", outsider, 800, application.ErrNotOfficeHolder},
		{"above the bound", mayor, cityDef.MaxValue() + 1, application.ErrPolicyOutOfBounds},
	} {
		if _, err := set(tc.who, tc.value, now); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
		if n := values(); n != 0 {
			t.Errorf("%s: a refusal wrote %d value(s)", tc.name, n)
		}
	}

	// --- set within bounds: announced now, in force after the notice ---
	notice, _ := cityDef.NoticeDuration()
	cooldown, _ := cityDef.CooldownDuration()
	first, err := set(mayor, 800, now)
	if err != nil {
		t.Fatalf("setting tax: %v", err)
	}
	if first.OldValue != kessmoor || !first.Setting.EffectiveAt.Equal(now.Add(notice)) {
		t.Errorf("recorded %+v", first)
	}
	var publicPlayer string
	if err := raw.QueryRow(ctx,
		`SELECT set_by_player_id::text FROM policy_changes WHERE policy_value_id = $1::uuid`, first.Setting.ID).Scan(&publicPlayer); err != nil {
		t.Fatalf("the public record: %v", err)
	}
	if publicPlayer != mayor.ID {
		t.Errorf("the public record names %s, want the mayor", publicPlayer)
	}

	clock = now.Add(notice - time.Second)
	if v, _ := reader.Get(ctx, city.ID, "city.tax_rate"); v.Value != kessmoor || v.Pending == nil {
		t.Errorf("inside the notice: %d pending %+v, want the default with the change pending", v.Value, v.Pending)
	}
	clock = now.Add(notice)
	if v, _ := reader.Get(ctx, city.ID, "city.tax_rate"); v.Value != 800 || v.Source != application.PolicyFromOffice {
		t.Errorf("after the notice: %d from %s, want 800 from the office", v.Value, v.Source)
	}

	// --- within the cooldown: refused, nothing written ---
	if _, err := set(mayor, 900, now.Add(cooldown-time.Second)); !errors.Is(err, application.ErrPolicyCooldown) {
		t.Errorf("within the cooldown: %v, want ErrPolicyCooldown", err)
	}
	if n := values(); n != 1 {
		t.Errorf("after a cooldown refusal there are %d values, want 1", n)
	}

	// --- the latest of several wins ---
	if _, err := set(mayor, 950, now.Add(cooldown)); err != nil {
		t.Fatalf("setting after the cooldown: %v", err)
	}
	clock = now.Add(cooldown + notice)
	if v, _ := reader.Get(ctx, city.ID, "city.tax_rate"); v.Value != 950 {
		t.Errorf("with two changes in effect: %d, want the later 950", v.Value)
	}

	// --- vacancy: the law stays, the deputy acts ---
	if _, err := admin.Vacate(ctx, postgres.SeatChangeRequest{
		SeatRef: postgres.SeatRef{OfficeCode: "mayor", JurisdictionKind: "city", JurisdictionCode: cityCode, Seat: 1},
		Actor:   "integration-test", Reason: "integration test", At: now,
	}); err != nil {
		t.Fatalf("vacating: %v", err)
	}
	v, err = reader.Get(ctx, city.ID, "city.tax_rate")
	if err != nil || v.Value != 950 || v.Acting != nil {
		t.Errorf("mayor and deputy vacant: %d acting %+v err %v, want 950, nobody acting, no error", v.Value, v.Acting, err)
	}
	appoint("deputy_mayor", deputy)
	if v, _ := reader.Get(ctx, city.ID, "city.tax_rate"); v.Acting == nil || !v.Acting.Deputy || v.Acting.OfficeCode != "deputy_mayor" {
		t.Errorf("with a deputy and no mayor, acting = %+v, want the deputy mayor", v.Acting)
	}
	if _, err := set(deputy, 500, now.Add(2*cooldown)); err != nil {
		t.Errorf("the acting deputy was refused: %v", err)
	}
}

// TestPolicyTablesGuardThemselves checks the triggers behind the application:
// the public record is append-only, a value outside the operator's bounds is
// refused by the database, and a value with no public record cannot commit.
func TestPolicyTablesGuardThemselves(t *testing.T) {
	pool := requirePostgres(t)
	requireGovernance(t, pool)
	ctx := testCtx(t)
	raw := pool.Raw()

	recordContentBaseline(t, pool)
	applyContent(t, pool, shippedPack(t), "governance integration test, triggers")

	admin := postgres.NewGovernanceAdmin(pool)
	city, err := admin.JurisdictionByCode(ctx, "city", "calderis")
	if err != nil {
		t.Fatal(err)
	}
	var officeID string
	if err := raw.QueryRow(ctx,
		`SELECT id::text FROM offices WHERE jurisdiction_id = $1::uuid AND office_code = 'mayor' AND seat = 1`, city.ID).Scan(&officeID); err != nil {
		t.Fatal(err)
	}
	p := insertPlayer(t, pool)
	seats := []string{}
	seatCleanup(t, pool, []string{city.ID}, &seats)

	far := time.Now().UTC().Add(10000 * time.Hour)
	insertValue := func(tx pgx.Tx, id string, value int64) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO policy_values (id, jurisdiction_id, lever_code, value_kind, value, set_by_player_id, office_id, set_at, effective_at)
			 VALUES ($1::uuid, $2::uuid, 'city.tax_rate', 'scalar', $3, $4::uuid, $5::uuid, $6::timestamptz, $6::timestamptz + interval '1 day')`,
			id, city.ID, value, p.ID, officeID, far)
		return err
	}
	constraintOf := func(err error) string {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			return pgErr.ConstraintName
		}
		return ""
	}

	// Out of bounds, refused by the trigger.
	tx, err := raw.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = insertValue(tx, newUUID(t), 999999)
	_ = tx.Rollback(ctx)
	if constraintOf(err) != "policy_values_within_bounds" {
		t.Errorf("an out-of-bounds value: %v, want policy_values_within_bounds", err)
	}

	// No public record: refused at commit.
	tx, err = raw.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertValue(tx, newUUID(t), 500); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("inserting a value: %v", err)
	}
	err = tx.Commit(ctx)
	if constraintOf(err) != "policy_values_public_record" {
		t.Errorf("a value with no public record committed: %v", err)
	}

	// With its public record it commits; the record then refuses to change.
	valueID, changeID := newUUID(t), newUUID(t)
	tx, err = raw.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertValue(tx, valueID, 500); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO policy_changes (id, policy_value_id, jurisdiction_id, lever_code, office_id, office_code,
		        set_by_player_id, value_kind, old_value, new_value, set_at, effective_at)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, 'city.tax_rate', $4::uuid, 'mayor', $5::uuid, 'scalar', 450, 500, $6::timestamptz, $6::timestamptz + interval '1 day')`,
		changeID, valueID, city.ID, officeID, p.ID, far); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("a value with its public record did not commit: %v", err)
	}

	for _, stmt := range []string{
		`UPDATE policy_changes SET new_value = 1 WHERE id = $1::uuid`,
		`DELETE FROM policy_changes WHERE id = $1::uuid`,
	} {
		_, err := raw.Exec(ctx, stmt, changeID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23001" {
			t.Errorf("%s: %v, want restrict_violation", firstLine(stmt), err)
		}
	}
	if _, err := raw.Exec(ctx, `TRUNCATE policy_changes`); err == nil {
		t.Error("TRUNCATE policy_changes succeeded")
	}
}
