package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mrjvadi/torncity/internal/application"
	apperrors "github.com/mrjvadi/torncity/internal/shared/errors"
)

// Error mapping is the contract between the database's vocabulary and the
// application's. Everything below asserts IDENTITY — `err != sentinel` — and
// never errors.Is, because internal/shared/errors matches by Code: every
// Conflict is errors.Is every other Conflict, so errors.Is(err,
// application.ErrAlreadyTravelling) is also satisfied by
// application.ErrAlreadyFriends and would pass while the player was shown the
// wrong screen. TestSentinelsOfOneClassAreIndistinguishableByIs pins that
// property so this choice does not look like an oversight.

// pgErr builds the driver error the server would produce.
func pgErr(code, constraint string) *pgconn.PgError {
	return &pgconn.PgError{Code: code, ConstraintName: constraint, Message: "server message, possibly translated"}
}

func okTag() pgconn.CommandTag     { return pgconn.NewCommandTag("UPDATE 1") }
func noRowsTag() pgconn.CommandTag { return pgconn.NewCommandTag("UPDATE 0") }

const (
	testPlayerID = "11111111-1111-4111-8111-111111111111"
	testFriendID = "22222222-2222-4222-8222-222222222222"
	testCityID   = "33333333-3333-4333-8333-333333333333"
	testOtherID  = "44444444-4444-4444-8444-444444444444"
)

// ---------------------------------------------------------------------------
// the two predicates every mapping rests on
// ---------------------------------------------------------------------------

// Both halves are required. The SQLSTATE alone is far too broad — one INSERT
// can violate several unique indexes — and the constraint name alone is not
// enough either, because the same name is reported for different failure
// classes on the same object.
func TestViolatesRequiresBothTheCodeAndTheConstraint(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		sqlstate   string
		constraint string
		want       bool
	}{
		{"exact match", pgErr("23505", "travels_one_active_per_player_idx"), "23505", "travels_one_active_per_player_idx", true},
		{"wrapped exact match", fmt.Errorf("postgres: starting travel: %w", pgErr("23505", "travels_one_active_per_player_idx")), "23505", "travels_one_active_per_player_idx", true},
		{"same code, other constraint", pgErr("23505", "travels_pkey"), "23505", "travels_one_active_per_player_idx", false},
		{"same constraint, other code", pgErr("23503", "travels_one_active_per_player_idx"), "23505", "travels_one_active_per_player_idx", false},
		{"no constraint name at all", pgErr("23505", ""), "23505", "travels_one_active_per_player_idx", false},
		{"not a driver error", errors.New("connection reset"), "23505", "travels_one_active_per_player_idx", false},
		{"nil", nil, "23505", "travels_one_active_per_player_idx", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := violates(tc.err, tc.sqlstate, tc.constraint); got != tc.want {
				t.Errorf("violates(%v, %q, %q) = %v, want %v", tc.err, tc.sqlstate, tc.constraint, got, tc.want)
			}
		})
	}
}

func TestIsInvalidUUIDText(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"invalid text representation", pgErr("22P02", ""), true},
		{"wrapped", fmt.Errorf("loading: %w", pgErr("22P02", "")), true},
		{"a different server error", pgErr("42703", ""), false},
		{"no rows is not a syntax complaint", pgx.ErrNoRows, false},
		{"plain error", errors.New("boom"), false},
		{"nil", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isInvalidUUIDText(tc.err); got != tc.want {
				t.Errorf("isInvalidUUIDText(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// errors.Is on these sentinels compares classes, not identities. This is not a
// defect of the errors package — two Conflicts are the same kind of failure —
// but it does mean a test that used errors.Is would not be testing the mapping
// at all.
func TestSentinelsOfOneClassAreIndistinguishableByIs(t *testing.T) {
	if !errors.Is(application.ErrAlreadyTravelling, application.ErrAlreadyFriends) {
		t.Skip("the shared errors package no longer matches by code; identity assertions remain correct either way")
	}
	if application.ErrAlreadyTravelling == application.ErrAlreadyFriends {
		t.Fatal("two distinct sentinels compare equal by identity, so no assertion in this file can distinguish them")
	}
}

// ---------------------------------------------------------------------------
// travel
// ---------------------------------------------------------------------------

// The narrow translation is the point: a retried command reusing the same
// travel id violates the primary key with the same 23505, and reporting that
// as "you are already travelling" would send a player to a journey screen
// instead of telling an operator that a command is being replayed.
func TestStartTravelMapsOnlyTheOneActiveIndex(t *testing.T) {
	newTravel := func() application.Travel {
		return application.Travel{
			ID:           testOtherID,
			PlayerID:     testPlayerID,
			FromCityID:   testCityID,
			ToCityID:     testCityID,
			GameActionID: testOtherID,
		}
	}

	t.Run("the one-active-per-player index becomes ErrAlreadyTravelling", func(t *testing.T) {
		q := &fakeTransactor{}
		q.execErr = pgErr(sqlstateUniqueViolation, travelsOneActivePerPlayerIdx)
		repo := &TravelRepository{q: q}

		err := repo.Start(context.Background(), newTravel())
		if err != application.ErrAlreadyTravelling {
			t.Fatalf("Start = %v, want the unwrapped application.ErrAlreadyTravelling", err)
		}
	})

	t.Run("a different 23505 is not ErrAlreadyTravelling", func(t *testing.T) {
		driverErr := pgErr(sqlstateUniqueViolation, "travels_pkey")
		q := &fakeTransactor{}
		q.execErr = driverErr
		repo := &TravelRepository{q: q}

		err := repo.Start(context.Background(), newTravel())
		if err == application.ErrAlreadyTravelling {
			t.Fatal("a primary key violation was reported as an ordinary second tap")
		}
		// It must still be reported as itself, so whoever reads the log sees
		// the replay rather than a sentinel that hides it.
		var got *pgconn.PgError
		if !errors.As(err, &got) || got != driverErr {
			t.Fatalf("Start = %v, want the driver error to survive unwrapping", err)
		}
	})

	t.Run("the index name alone is not enough", func(t *testing.T) {
		q := &fakeTransactor{}
		q.execErr = pgErr("23503", travelsOneActivePerPlayerIdx)
		repo := &TravelRepository{q: q}

		if err := repo.Start(context.Background(), newTravel()); err == application.ErrAlreadyTravelling {
			t.Fatal("a foreign key violation was reported as already travelling")
		}
	})

	t.Run("a wrapped violation still maps", func(t *testing.T) {
		q := &fakeTransactor{}
		q.execErr = fmt.Errorf("pool: %w", pgErr(sqlstateUniqueViolation, travelsOneActivePerPlayerIdx))
		repo := &TravelRepository{q: q}

		if err := repo.Start(context.Background(), newTravel()); err != application.ErrAlreadyTravelling {
			t.Fatalf("Start = %v, want application.ErrAlreadyTravelling through a wrapped error", err)
		}
	})
}

// A journey with no action behind it would never arrive, so it is refused
// before the server is asked.
func TestStartTravelRequiresAGameAction(t *testing.T) {
	q := &fakeTransactor{}
	repo := &TravelRepository{q: q}

	err := repo.Start(context.Background(), application.Travel{PlayerID: testPlayerID, FromCityID: testCityID, ToCityID: testCityID})
	if err == nil {
		t.Fatal("Start accepted a journey with no scheduled action")
	}
	if len(q.calls) != 0 {
		t.Errorf("Start reached the database anyway: %d statement(s)", len(q.calls))
	}
}

// Start fills in the status and the departure time so that a caller cannot
// insert a journey the one-active index does not cover.
func TestStartTravelDefaultsStatusAndDeparture(t *testing.T) {
	q := &fakeTransactor{fakeQuerier: fakeQuerier{execTag: okTag()}}
	repo := &TravelRepository{q: q}

	if err := repo.Start(context.Background(), application.Travel{
		PlayerID: testPlayerID, FromCityID: testCityID, ToCityID: testCityID, GameActionID: testOtherID,
	}); err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}

	args := q.last().args
	if len(args) != 9 {
		t.Fatalf("the travel insert was sent %d arguments, want 9", len(args))
	}
	if args[6] != TravelInTransit {
		t.Errorf("status argument = %v, want %q", args[6], TravelInTransit)
	}
	if args[0] == "" {
		t.Error("the travel was inserted with an empty id")
	}
}

func TestActiveTravelMapsNoRows(t *testing.T) {
	q := &fakeTransactor{}
	q.rowErr = pgx.ErrNoRows
	repo := &TravelRepository{q: q}

	if _, err := repo.Active(context.Background(), testPlayerID); err != application.ErrNoActiveTravel {
		t.Fatalf("Active = %v, want application.ErrNoActiveTravel", err)
	}
}

func TestCancelTravelMapsAnUnmatchedRow(t *testing.T) {
	q := &fakeTransactor{fakeQuerier: fakeQuerier{execTag: noRowsTag()}}
	repo := &TravelRepository{q: q}

	if err := repo.Cancel(context.Background(), testOtherID); err != application.ErrNoActiveTravel {
		t.Fatalf("Cancel = %v, want application.ErrNoActiveTravel", err)
	}
}

// Complete's atomicity is proved against a real server in tests/; what is
// checked here is that both statements go to ONE transaction and that the
// second one is fed from the first one's RETURNING.
func TestCompleteTravelRunsBothStatementsInOneTransaction(t *testing.T) {
	tx := &fakeTx{rowVals: []any{testPlayerID, testCityID}, execTag: okTag()}
	q := &fakeTransactor{tx: tx}
	repo := &TravelRepository{q: q}

	if err := repo.Complete(context.Background(), testOtherID); err != nil {
		t.Fatalf("Complete returned unexpected error: %v", err)
	}

	if len(tx.calls) != 2 {
		t.Fatalf("Complete sent %d statements inside the transaction, want 2", len(tx.calls))
	}
	if normalize(tx.calls[0].sql) != normalize(arriveTravel) {
		t.Errorf("the first statement is not the arrival update:\n%s", tx.calls[0].sql)
	}
	if normalize(tx.calls[1].sql) != normalize(movePlayerToCity) {
		t.Errorf("the second statement is not the player move:\n%s", tx.calls[1].sql)
	}
	// The player and the destination must come from the row the update
	// returned, not from anything the caller supplied.
	if tx.calls[1].args[0] != testPlayerID || tx.calls[1].args[1] != testCityID {
		t.Errorf("the player move was sent %v, want the returned player and city", tx.calls[1].args[:2])
	}
	if !tx.committed {
		t.Error("Complete did not commit")
	}
	if tx.rolledBack {
		t.Error("Complete rolled back a successful arrival")
	}
}

func TestCompleteTravelMapsAnUnmatchedJourney(t *testing.T) {
	tx := &fakeTx{rowErr: pgx.ErrNoRows}
	q := &fakeTransactor{tx: tx}
	repo := &TravelRepository{q: q}

	if err := repo.Complete(context.Background(), testOtherID); err != application.ErrNoActiveTravel {
		t.Fatalf("Complete = %v, want application.ErrNoActiveTravel", err)
	}
	if tx.committed {
		t.Error("Complete committed a transaction whose first statement matched nothing")
	}
	if !tx.rolledBack {
		t.Error("Complete left the transaction open")
	}
}

// If the second half fails, the first half must not survive. The rollback is
// what makes "arrived but still in the old city" unreachable.
func TestCompleteTravelRollsBackWhenTheMoveFails(t *testing.T) {
	tx := &fakeTx{rowVals: []any{testPlayerID, testCityID}, execErr: errors.New("connection reset")}
	q := &fakeTransactor{tx: tx}
	repo := &TravelRepository{q: q}

	err := repo.Complete(context.Background(), testOtherID)
	if err == nil {
		t.Fatal("Complete reported success although the player never moved")
	}
	if tx.committed {
		t.Error("Complete committed a half-finished arrival")
	}
	if !tx.rolledBack {
		t.Error("Complete did not roll back a failed arrival")
	}
}

// ---------------------------------------------------------------------------
// stats
// ---------------------------------------------------------------------------

func TestStatsMapMissingRows(t *testing.T) {
	t.Run("Get", func(t *testing.T) {
		q := &fakeQuerier{rowErr: pgx.ErrNoRows}
		repo := &StatsRepository{q: q}

		if _, err := repo.Get(context.Background(), testPlayerID); err != ErrStatsNotFound {
			t.Fatalf("Get = %v, want ErrStatsNotFound", err)
		}
	})

	// Save is an UPDATE, so "no row" is the caller having skipped
	// EnsureDefaults rather than a condition to paper over with an insert.
	t.Run("Save", func(t *testing.T) {
		q := &fakeQuerier{execTag: noRowsTag()}
		repo := &StatsRepository{q: q}

		if err := repo.Save(context.Background(), application.Stats{PlayerID: testPlayerID}); err != ErrStatsNotFound {
			t.Fatalf("Save = %v, want ErrStatsNotFound", err)
		}
	})
}

// Claiming the player does not exist would be a false statement shown to the
// very person it is about: a player can exist with no stats row.
func TestStatsNotFoundIsNotPlayerNotFound(t *testing.T) {
	if ErrStatsNotFound == application.ErrPlayerNotFound {
		t.Fatal("missing stats are reported as a missing player")
	}
}

// An empty player id would fail on the uuid cast with a complaint about
// syntax, which sends whoever reads it looking at the wrong thing.
func TestStatsRefuseAnEmptyPlayerID(t *testing.T) {
	q := &fakeQuerier{}
	repo := &StatsRepository{q: q}

	if _, err := repo.EnsureDefaults(context.Background(), "", application.Stats{}); err == nil {
		t.Error("EnsureDefaults accepted an empty player id")
	}
	if err := repo.Save(context.Background(), application.Stats{}); err == nil {
		t.Error("Save accepted an empty player id")
	}
	if len(q.calls) != 0 {
		t.Errorf("an empty player id still reached the database: %d statement(s)", len(q.calls))
	}
}

// updated_at is NOT NULL and the schema has no DEFAULT now(), so a zero time
// would be written as year 1 and would sort first in every staleness sweep.
func TestStatsSupplyATimestampWhenTheCallerDidNot(t *testing.T) {
	q := &fakeQuerier{
		rowVals: []any{testPlayerID, 1, int64(0), 100, 100, 50, 50, 50, 50, 0, nowForTest()},
		execTag: okTag(),
	}
	repo := &StatsRepository{q: q}

	if _, err := repo.EnsureDefaults(context.Background(), testPlayerID, application.Stats{}); err != nil {
		t.Fatalf("EnsureDefaults returned unexpected error: %v", err)
	}
	assertTimestampArgument(t, q.last().args, 10, "EnsureDefaults")

	if err := repo.Save(context.Background(), application.Stats{PlayerID: testPlayerID}); err != nil {
		t.Fatalf("Save returned unexpected error: %v", err)
	}
	assertTimestampArgument(t, q.last().args, 10, "Save")
}

// ---------------------------------------------------------------------------
// skills
// ---------------------------------------------------------------------------

func TestGetSkillMapsNoRows(t *testing.T) {
	q := &fakeQuerier{rowErr: pgx.ErrNoRows}
	repo := &SkillRepository{q: q}

	if _, err := repo.Get(context.Background(), testPlayerID, "programming"); err != application.ErrSkillNotFound {
		t.Fatalf("Get = %v, want application.ErrSkillNotFound", err)
	}
}

// An empty code would occupy the slot in the unique key and merge two
// unrelated abilities into one row.
func TestUpsertSkillRefusesAnEmptyCode(t *testing.T) {
	q := &fakeQuerier{}
	repo := &SkillRepository{q: q}

	if err := repo.Upsert(context.Background(), application.Skill{PlayerID: testPlayerID}); err == nil {
		t.Fatal("Upsert accepted an empty skill code")
	}
	if len(q.calls) != 0 {
		t.Errorf("an empty skill code still reached the database: %d statement(s)", len(q.calls))
	}
}

// An empty list is a legitimate answer for a player who has trained nothing,
// not a missing row.
func TestListSkillsTreatsAnEmptyResultAsAnAnswer(t *testing.T) {
	q := &fakeQuerier{rows: &fakeRows{}}
	repo := &SkillRepository{q: q}

	got, err := repo.List(context.Background(), testPlayerID)
	if err != nil {
		t.Fatalf("List returned unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List returned %d skills for an empty result", len(got))
	}
}

// ---------------------------------------------------------------------------
// cities
// ---------------------------------------------------------------------------

// A value that is not uuid text names no city, exactly like a well-formed uuid
// no row carries. Letting the server's complaint through would classify a bad
// button payload as an internal failure and put the caller's own string into
// the log line that recorded it.
func TestCityByIDTreatsAMalformedIdentifierAsAMiss(t *testing.T) {
	for name, cause := range map[string]error{
		"no rows":                   pgx.ErrNoRows,
		"invalid uuid text":         pgErr(sqlstateInvalidTextRepresentation, ""),
		"wrapped invalid uuid text": fmt.Errorf("pool: %w", pgErr(sqlstateInvalidTextRepresentation, "")),
	} {
		t.Run(name, func(t *testing.T) {
			q := &fakeQuerier{rowErr: cause}
			repo := &CityRepository{q: q}

			if _, err := repo.ByID(context.Background(), "not-a-uuid"); err != application.ErrCityNotFound {
				t.Fatalf("ByID = %v, want application.ErrCityNotFound", err)
			}
		})
	}
}

// Only those two conditions are a miss. Anything else is a fault and must keep
// saying so.
func TestCityByIDDoesNotSwallowOtherFailures(t *testing.T) {
	driverErr := pgErr("42703", "")
	q := &fakeQuerier{rowErr: driverErr}
	repo := &CityRepository{q: q}

	_, err := repo.ByID(context.Background(), testCityID)
	if err == application.ErrCityNotFound {
		t.Fatal("an undefined-column error was reported as a missing city")
	}
	var got *pgconn.PgError
	if !errors.As(err, &got) || got != driverErr {
		t.Fatalf("ByID = %v, want the driver error to survive unwrapping", err)
	}
}

// ByCode has no uuid cast to fail on, so it maps only pgx.ErrNoRows. The
// difference from ByID is deliberate and is pinned so nobody "tidies" it away.
func TestCityByCodeMapsOnlyNoRows(t *testing.T) {
	q := &fakeQuerier{rowErr: pgx.ErrNoRows}
	if _, err := (&CityRepository{q: q}).ByCode(context.Background(), "TC"); err != application.ErrCityNotFound {
		t.Fatalf("ByCode = %v, want application.ErrCityNotFound", err)
	}

	q = &fakeQuerier{rowErr: pgErr(sqlstateInvalidTextRepresentation, "")}
	if _, err := (&CityRepository{q: q}).ByCode(context.Background(), "TC"); err == application.ErrCityNotFound {
		t.Fatal("ByCode hid a server syntax error as a missing city")
	}
}

// ---------------------------------------------------------------------------
// the durable schedule
// ---------------------------------------------------------------------------

// Both closers are guarded on the open statuses, so zero rows means the action
// is unknown or was already closed out — one answer, deliberately.
func TestActionClosersMapAnUnmatchedRow(t *testing.T) {
	t.Run("Complete", func(t *testing.T) {
		q := &fakeQuerier{execTag: noRowsTag()}
		if err := (&GameActionRepository{q: q}).Complete(context.Background(), testOtherID); err != ErrGameActionNotFound {
			t.Fatalf("Complete = %v, want ErrGameActionNotFound", err)
		}
	})
	t.Run("Fail", func(t *testing.T) {
		q := &fakeQuerier{execTag: noRowsTag()}
		if err := (&GameActionRepository{q: q}).Fail(context.Background(), testOtherID, "boom"); err != ErrGameActionNotFound {
			t.Fatalf("Fail = %v, want ErrGameActionNotFound", err)
		}
	})
}

// Schedule refuses the two values that would produce a row no worker can act
// on: an action type nothing can route, and a finish time in the year 1 that
// is due immediately and forever first in every claim batch.
func TestScheduleRefusesUnroutableRows(t *testing.T) {
	tests := []struct {
		name string
		in   application.GameAction
	}{
		{"no action type", application.GameAction{FinishAt: nowForTest()}},
		{"no finish time", application.GameAction{ActionType: "travel"}},
		{"payload that is not json", application.GameAction{ActionType: "travel", FinishAt: nowForTest(), Payload: []byte("not json")}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &fakeQuerier{execTag: okTag()}
			if err := (&GameActionRepository{q: q}).Schedule(context.Background(), tc.in); err == nil {
				t.Fatal("Schedule accepted a row no worker could act on")
			}
			if len(q.calls) != 0 {
				t.Errorf("the row reached the database anyway: %d statement(s)", len(q.calls))
			}
		})
	}
}

// An action arriving without an owner is a world-level one, and the check
// constraint admits only player, company or system.
func TestScheduleDefaultsTheActorAndFixesTheStatus(t *testing.T) {
	q := &fakeQuerier{execTag: okTag()}
	repo := &GameActionRepository{q: q}

	if err := repo.Schedule(context.Background(), application.GameAction{
		ActionType: "world_tick",
		FinishAt:   nowForTest(),
	}); err != nil {
		t.Fatalf("Schedule returned unexpected error: %v", err)
	}

	args := q.last().args
	if len(args) != 10 {
		t.Fatalf("the action insert was sent %d arguments, want 10", len(args))
	}
	if args[2] != "system" {
		t.Errorf("actor_type argument = %v, want %q", args[2], "system")
	}
	if args[7] != ActionScheduled {
		t.Errorf("status argument = %v, want %q; the status is not the caller's to choose", args[7], ActionScheduled)
	}
	// An empty string is not valid uuid text, so absent identifiers must go
	// as NULL rather than fail the cast.
	if args[3] != (*string)(nil) {
		t.Errorf("an absent actor id was sent as %#v, want a NULL parameter", args[3])
	}
	if args[5] != (*string)(nil) {
		t.Errorf("an absent reference id was sent as %#v, want a NULL parameter", args[5])
	}
}

// A non-positive limit would make LIMIT meaningless and is a caller bug, not a
// batch of size zero.
func TestDueRefusesANonPositiveLimit(t *testing.T) {
	for _, limit := range []int{0, -1} {
		q := &fakeQuerier{}
		if _, err := (&GameActionRepository{q: q}).Due(context.Background(), nowForTest(), limit); err == nil {
			t.Errorf("Due accepted a limit of %d", limit)
		}
		if len(q.calls) != 0 {
			t.Errorf("Due(limit=%d) reached the database: %d statement(s)", limit, len(q.calls))
		}
	}
}

// ---------------------------------------------------------------------------
// friendships
// ---------------------------------------------------------------------------

// The self-edge is rejected by friendships_no_self_check at the lowest
// possible level, and every one of the three writing methods must translate
// it: a raw check violation reaching a player says nothing they can act on.
func TestFriendshipSelfEdgeBecomesASentinel(t *testing.T) {
	selfErr := pgErr(sqlstateCheckViolation, friendshipsNoSelfCheck)

	t.Run("Request", func(t *testing.T) {
		q := &fakeTransactor{}
		q.rowErr = selfErr
		if err := (&FriendshipRepository{q: q}).Request(context.Background(), testPlayerID, testPlayerID); err != ErrSelfFriendship {
			t.Fatalf("Request = %v, want ErrSelfFriendship", err)
		}
	})

	t.Run("Block", func(t *testing.T) {
		q := &fakeTransactor{}
		q.execErr = selfErr
		if err := (&FriendshipRepository{q: q}).Block(context.Background(), testPlayerID, testPlayerID); err != ErrSelfFriendship {
			t.Fatalf("Block = %v, want ErrSelfFriendship", err)
		}
	})

	t.Run("Accept", func(t *testing.T) {
		tx := &fakeTx{rowVals: []any{testOtherID}, execErr: selfErr}
		q := &fakeTransactor{tx: tx}
		if err := (&FriendshipRepository{q: q}).Accept(context.Background(), testPlayerID, testPlayerID); err != ErrSelfFriendship {
			t.Fatalf("Accept = %v, want ErrSelfFriendship", err)
		}
		if tx.committed {
			t.Error("Accept committed a transaction whose reverse edge was rejected")
		}
	})

	// A check violation on some other constraint is not this condition.
	t.Run("another check constraint is not a self edge", func(t *testing.T) {
		q := &fakeTransactor{}
		q.rowErr = pgErr(sqlstateCheckViolation, "friendships_status_check")
		if err := (&FriendshipRepository{q: q}).Request(context.Background(), testPlayerID, testFriendID); err == ErrSelfFriendship {
			t.Fatal("a status check violation was reported as a self edge")
		}
	})
}

// What an existing edge means depends on its status, and the caller needs to
// tell those apart to draw anything sensible.
func TestRequestReportsWhatTheExistingEdgeMeans(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   error
	}{
		{"already accepted", FriendshipAccepted, application.ErrAlreadyFriends},
		{"blocked by the caller", FriendshipBlocked, ErrFriendshipBlocked},
		// Re-sending a request that is still pending is ordinary: the edge is
		// already exactly what the caller asked for.
		{"still pending", FriendshipPending, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &fakeTransactor{}
			// The surviving id is not the one this call proposed, which is how
			// the repository knows the row was already there.
			q.rowFunc = func(string, []any) fakeRow { return fakeRow{vals: []any{testOtherID, tc.status}} }

			if err := (&FriendshipRepository{q: q}).Request(context.Background(), testPlayerID, testFriendID); err != tc.want {
				t.Fatalf("Request = %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("a fresh edge is success", func(t *testing.T) {
		q := &fakeTransactor{}
		// Echo back the id the statement proposed: this call inserted the row.
		q.rowFunc = func(_ string, args []any) fakeRow {
			return fakeRow{vals: []any{args[0], FriendshipPending}}
		}

		if err := (&FriendshipRepository{q: q}).Request(context.Background(), testPlayerID, testFriendID); err != nil {
			t.Fatalf("Request = %v, want success for a freshly created edge", err)
		}
	})
}

// Without a request there is nothing to become friends over.
func TestAcceptWithoutAPendingRequest(t *testing.T) {
	tx := &fakeTx{rowErr: pgx.ErrNoRows}
	q := &fakeTransactor{tx: tx}

	if err := (&FriendshipRepository{q: q}).Accept(context.Background(), testPlayerID, testFriendID); err != application.ErrNotFriends {
		t.Fatalf("Accept = %v, want application.ErrNotFriends", err)
	}
	if tx.committed {
		t.Error("Accept committed although no request was accepted")
	}
	if !tx.rolledBack {
		t.Error("Accept left the transaction open")
	}
}

// Both directions, one transaction. Split apart, a crash between them leaves a
// friendship only one of the two people has.
func TestAcceptWritesBothDirectionsInOneTransaction(t *testing.T) {
	tx := &fakeTx{rowVals: []any{testOtherID}, execTag: okTag()}
	q := &fakeTransactor{tx: tx}

	if err := (&FriendshipRepository{q: q}).Accept(context.Background(), testPlayerID, testFriendID); err != nil {
		t.Fatalf("Accept returned unexpected error: %v", err)
	}

	if len(tx.calls) != 2 {
		t.Fatalf("Accept sent %d statements inside the transaction, want 2", len(tx.calls))
	}
	if normalize(tx.calls[0].sql) != normalize(acceptIncoming) {
		t.Errorf("the first statement is not the accept:\n%s", tx.calls[0].sql)
	}
	if normalize(tx.calls[1].sql) != normalize(insertReverseEdge) {
		t.Errorf("the second statement is not the reverse edge:\n%s", tx.calls[1].sql)
	}
	// The incoming row is (friendPlayerID -> playerID): the other player asked
	// first, so theirs is the edge that already exists.
	if tx.calls[0].args[0] != testFriendID || tx.calls[0].args[1] != testPlayerID {
		t.Errorf("the accept was applied to %v, want the incoming edge (friend -> player)", tx.calls[0].args[:2])
	}
	// The reverse edge is the accepter's own: (playerID -> friendPlayerID).
	if tx.calls[1].args[1] != testPlayerID || tx.calls[1].args[2] != testFriendID {
		t.Errorf("the reverse edge is %v, want (player -> friend)", tx.calls[1].args[1:3])
	}
	if !tx.committed {
		t.Error("Accept did not commit")
	}
}

// The caller asked to remove something that was not there, which for a screen
// that just offered an unfriend button is stale state worth knowing about.
func TestRemoveFriendshipMapsAnUnmatchedRow(t *testing.T) {
	q := &fakeTransactor{}
	q.execTag = noRowsTag()

	if err := (&FriendshipRepository{q: q}).Remove(context.Background(), testPlayerID, testFriendID); err != application.ErrNotFriends {
		t.Fatalf("Remove = %v, want application.ErrNotFriends", err)
	}
}

// The sentinels this package declares must stay classified, so a caller in the
// application layer — which cannot import this package — can still branch on
// them through errors.Is against the shared classes.
func TestPackageSentinelsAreClassified(t *testing.T) {
	for name, err := range map[string]error{
		"ErrStatsNotFound":      ErrStatsNotFound,
		"ErrGameActionNotFound": ErrGameActionNotFound,
		"ErrSelfFriendship":     ErrSelfFriendship,
		"ErrFriendshipBlocked":  ErrFriendshipBlocked,
	} {
		if err == nil {
			t.Errorf("%s is nil", name)
			continue
		}
		if err.Error() == "" {
			t.Errorf("%s carries no message", name)
		}
	}

	// Each must belong to the class its comment claims.
	//
	// The target is the CLASS sentinel, never another package's named one.
	// A named sentinel now matches only itself, which is the point: asking
	// "is this a not-found?" with application.ErrPlayerNotFound as the target
	// asks the much narrower "is this THAT not-found?", and the answer for an
	// unrelated error is correctly no.
	if !errors.Is(ErrStatsNotFound, apperrors.ErrNotFound) {
		t.Error("ErrStatsNotFound is not classified as a not-found condition")
	}
	if !errors.Is(ErrGameActionNotFound, apperrors.ErrNotFound) {
		t.Error("ErrGameActionNotFound is not classified as a not-found condition")
	}
	if !errors.Is(ErrFriendshipBlocked, apperrors.ErrConflict) {
		t.Error("ErrFriendshipBlocked is not classified as a conflict")
	}
	// A self edge is never valid in any state, so it is invalid input rather
	// than a conflict.
	if errors.Is(ErrSelfFriendship, apperrors.ErrConflict) {
		t.Error("ErrSelfFriendship is classified as a conflict")
	}
}
