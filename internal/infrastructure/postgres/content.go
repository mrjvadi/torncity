package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
)

// ContentStore persists authored game content and announces new versions.
//
// Content is written as a whole version, never patched in place. A load either
// lands completely or not at all, and the previous version stays active until
// the new one is committed. That is what lets an operator reload while players
// are mid-request: nobody ever observes half a world.
//
// It holds the pool rather than a querier, unlike the repositories in this
// package. A content load IS a transaction — the version row, the rows it
// owns, the supersede, the outbox announcement and the audit row have to
// commit together or not at all — so this type begins its own, and a querier
// cannot do that.
//
// See docs/adr/0004-content-system.md.
type ContentStore struct {
	pool *pgxpool.Pool
}

// NewContentStore returns a store backed by p.
func NewContentStore(p *Pool) *ContentStore { return &ContentStore{pool: p.Raw()} }

// ErrCityInUse reports that a load would remove a city that players are
// standing in or travelling to.
//
// ADR 0004 rule 7. This cannot be caught by pure validation because it depends
// on live game state, not on the files, so it is checked here inside the
// applying transaction where the answer cannot go stale between the check and
// the write.
var ErrCityInUse = errors.New("postgres: content: a city in use would be removed")

// ErrNoActiveVersion means no content has ever been loaded, so there is no
// world to read. It is returned rather than an empty pack because "nothing has
// been loaded" and "the world is empty" need different responses from a
// service that is booting.
var ErrNoActiveVersion = errors.New("postgres: content: no active content version")

// ErrNoReason means a load was attempted without an explanation.
//
// ADR 0009: a change whose reason was not recorded is a change nobody can
// evaluate six months later. content_versions.notes and audit_logs.reason are
// both NOT NULL for that reason, and an empty string would satisfy the column
// while defeating the point, so it is refused here.
var ErrNoReason = errors.New("postgres: content: a reason is required")

// contentLoadLockKey serialises concurrent loaders.
//
// The version number is derived as MAX(version) + 1 inside the transaction.
// Two loaders reading that concurrently would both compute the same number and
// the second would fail on content_versions_version_key — a confusing error
// for what is really a queueing problem. A transaction-scoped advisory lock
// makes the second loader wait and then read the number the first one wrote.
// It is released by commit or rollback, so a crashed loader does not hold it.
//
// The value is arbitrary but must be unique among this project's advisory
// locks: it reads as ADR 0004, lock 1.
const contentLoadLockKey int64 = 4_000_001

// ApplyRequest carries who is loading content and why.
//
// Both are required and both are stored twice — on the version row, which is
// what an operator reads when asking "what is the world running", and on the
// audit row, which is what an investigator reads when asking "who changed
// what". Those are different questions asked by different people, and a join
// between two tables is not what either of them wants to type.
type ApplyRequest struct {
	// Actor is the operator identity. Free text: at this stage that is an
	// operating-system account, not a row in players.
	Actor string
	// Reason is why this load is happening. Must not be empty.
	Reason string
}

// Applied describes a committed content load.
type Applied struct {
	// Version is the monotonic number an operator quotes and every domain
	// event produced under this content carries (ADR 0004 rule 3).
	Version int
	// VersionID is the content_versions.id foreign keys point at.
	VersionID string
	// Checksum is what was stored as source_checksum.
	Checksum string
	// PlayersPlaced is how many players had no city and were placed in their
	// spawn city by this load. It is zero on every load after the first one
	// that ran with spawn weights, unless players were created while no city
	// had a positive weight — which is exactly the case it is there to report.
	PlayersPlaced int64
	// ResidencesSet is how many players had no residence and were given one
	// by this load: players placed above, and players who already stood in a
	// city before residence existed (migration 0005), whose current city
	// becomes where they live.
	ResidencesSet int64
	// Jurisdictions is how many jurisdictions this load wrote, one per city
	// included (ADR 0015).
	Jurisdictions int
	// OfficesCreated is how many office seats were new, all vacant. Zero on
	// a reload: existing seats, and whoever holds them, are left alone.
	OfficesCreated int64
}

// Apply writes a pack as a new active version, in one transaction.
//
// Everything happens together: the version row, the cities, the routes, the
// skills (spawn weights included), the governance content — levels,
// jurisdictions, office and lever definitions, and one vacant seat per office,
// jurisdiction and seat that does not exist yet — the supersede of the
// previous version, the
// placement of players who have no city yet, the residence of players who have
// none yet, the outbox record that tells
// running services to reload, and the audit row that records who did it. A
// failure anywhere leaves the world exactly as it was.
//
// The audit row is written HERE rather than by the calling command. ADR 0004
// rule 5 requires that every load leave one, and a row written after the
// commit is a row that is missing whenever the loader dies in between —
// exactly the load an investigator would most want to find.
//
// Apply validates the pack again before it opens a transaction. The caller is
// expected to have done so already (ADR 0004 rule 1), and the admin command
// does; repeating it here costs microseconds and means no future caller of
// this method can write a world that the loader would have refused.
func (s *ContentStore) Apply(ctx context.Context, p *content.Pack, req ApplyRequest) (Applied, error) {
	if p == nil {
		return Applied{}, fmt.Errorf("postgres: content apply: no pack")
	}
	// A reason of only spaces satisfies NOT NULL and says nothing, which is
	// exactly what the column exists to prevent.
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return Applied{}, ErrNoReason
	}
	if err := p.Validate(); err != nil {
		return Applied{}, fmt.Errorf("postgres: content apply: refusing invalid content: %w", err)
	}
	actor := strings.TrimSpace(req.Actor)
	if actor == "" {
		actor = "unknown"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Applied{}, fmt.Errorf("postgres: content apply: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, contentLoadLockKey); err != nil {
		return Applied{}, fmt.Errorf("postgres: content apply: lock: %w", err)
	}

	previous, err := currentWorld(ctx, tx)
	if err != nil {
		return Applied{}, err
	}

	removed, err := refuseRemovalOfCitiesInUse(ctx, tx, p)
	if err != nil {
		return Applied{}, err
	}

	var version int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) + 1 FROM content_versions`).Scan(&version); err != nil {
		return Applied{}, fmt.Errorf("postgres: content apply: next version: %w", err)
	}

	// Supersede first: the partial unique index allows exactly one active row,
	// so the new one cannot be inserted while the old one still claims it.
	if _, err := tx.Exec(ctx,
		`UPDATE content_versions SET status = 'superseded' WHERE status = 'active'`); err != nil {
		return Applied{}, fmt.Errorf("postgres: content apply: supersede: %w", err)
	}

	versionID, err := newUUID()
	if err != nil {
		return Applied{}, fmt.Errorf("postgres: content apply: %w", err)
	}

	checksum := SourceChecksum(p)
	if _, err := tx.Exec(ctx,
		`INSERT INTO content_versions (id, version, loaded_at, loaded_by, source_checksum, status, notes)
		 VALUES ($1::uuid, $2, $3, $4, $5, 'active', $6)`,
		versionID, version, time.Now().UTC(), actor, checksum, reason); err != nil {
		return Applied{}, fmt.Errorf("postgres: content apply: version row: %w", err)
	}

	cityIDs, err := upsertCities(ctx, tx, p, versionID)
	if err != nil {
		return Applied{}, err
	}
	// After the cities, because the pick needs their ids; placing before
	// housing, because a newly placed player's residence is the city they
	// were just placed in; and before the commit, so a load that fails leaves
	// nobody half-placed.
	placed, err := placeUnplacedPlayers(ctx, tx, spawnCandidates(p, cityIDs))
	if err != nil {
		return Applied{}, err
	}
	housed, err := houseUnhousedPlayers(ctx, tx)
	if err != nil {
		return Applied{}, err
	}
	if err := insertRoutes(ctx, tx, p, versionID, cityIDs); err != nil {
		return Applied{}, err
	}
	if err := insertSkills(ctx, tx, p, versionID); err != nil {
		return Applied{}, err
	}
	if err := insertJobs(ctx, tx, p, versionID); err != nil {
		return Applied{}, err
	}
	if err := applyTransport(ctx, tx, p, versionID, cityIDs); err != nil {
		return Applied{}, err
	}
	if err := insertCrimes(ctx, tx, p, versionID); err != nil {
		return Applied{}, err
	}
	if err := insertDocuments(ctx, tx, p, versionID); err != nil {
		return Applied{}, err
	}
	governance, err := applyGovernance(ctx, tx, p, versionID, cityIDs, time.Now().UTC())
	if err != nil {
		return Applied{}, err
	}
	if err := appendContentEvent(ctx, tx, version, versionID, checksum); err != nil {
		return Applied{}, err
	}
	audit := contentAudit{
		version:   version,
		versionID: versionID,
		checksum:  checksum,
		previous:  previous,
		removed:   removed,
		pack:      p,
		actor:     actor,
		reason:    reason,
		placed:    placed,
		housed:    housed,
		governed:  governance,
	}
	if err := appendContentAudit(ctx, tx, audit); err != nil {
		return Applied{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Applied{}, fmt.Errorf("postgres: content apply: commit: %w", err)
	}
	return Applied{
		Version:        version,
		VersionID:      versionID,
		Checksum:       checksum,
		PlayersPlaced:  placed,
		ResidencesSet:  housed,
		Jurisdictions:  governance.jurisdictions,
		OfficesCreated: governance.officesCreated,
	}, nil
}

// spawnCandidates is the pack's spawn cities with the ids this load stored
// them under. The pack's own CityIDs cannot be used: a pack read from files
// has none, and for a city new in this load no id existed before upsertCities.
func spawnCandidates(p *content.Pack, cityIDs map[string]string) []content.SpawnCandidate {
	out := p.SpawnCandidates()
	for i := range out {
		out[i].ID = cityIDs[out[i].Code]
	}
	return out
}

// selectUnplacedPlayers finds every player with no city.
//
// This is the backfill for players created while no city had a positive
// spawn weight: every player that reached first contact before migration
// 0005, and any created between that migration and the first load after it.
//
// The rows are locked so that the update below writes exactly the players
// picked for here. A player row is only ever created with a NULL city while
// no spawn weight exists, so once this backlog is gone the select matches
// nothing, through the players.city_id index.
//
// Player status is not considered: a banned or deleted player with no city is
// as broken a row as an active one, and the foreign key does not care why.
const selectUnplacedPlayers = `
SELECT id::text, telegram_user_id
  FROM players
 WHERE city_id IS NULL
 ORDER BY id
   FOR UPDATE`

// placeUnplacedStatement places each listed player in the city picked for
// them. Their residence is set right after, by houseUnhousedStatement, so
// that one statement owns "give a player a residence" and one count reports
// it. It deliberately never moves a player who already has a city (the
// `city_id IS NULL` guard): changing the weights changes where NEW players
// land, and relocating existing players because an author edited a number
// would be a silent teleport.
const placeUnplacedStatement = `
UPDATE players AS p
   SET city_id    = v.city_id,
       updated_at = $3
  FROM unnest($1::uuid[], $2::uuid[]) AS v(player_id, city_id)
 WHERE p.id = v.player_id
   AND p.city_id IS NULL`

// placeUnplacedPlayers gives every player with no city their spawn city, by
// the same deterministic pick first contact uses (content.PickSpawnCity), and
// returns how many it placed. The pick runs here, in Go, rather than in SQL,
// so there is exactly one implementation of it.
func placeUnplacedPlayers(ctx context.Context, tx pgx.Tx, candidates []content.SpawnCandidate) (int64, error) {
	rows, err := tx.Query(ctx, selectUnplacedPlayers)
	if err != nil {
		return 0, fmt.Errorf("postgres: content apply: finding players with no city: %w", err)
	}
	var playerIDs, cityIDs []string
	for rows.Next() {
		var (
			id             string
			telegramUserID int64
		)
		if err := rows.Scan(&id, &telegramUserID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("postgres: content apply: scanning player with no city: %w", err)
		}
		city, ok := content.PickSpawnCity(telegramUserID, candidates)
		if !ok || city.ID == "" {
			// Unreachable for a validated pack: Validate requires a positive
			// weight somewhere, and upsertCities returns an id for every city.
			rows.Close()
			return 0, fmt.Errorf("postgres: content apply: no spawn city was written")
		}
		playerIDs = append(playerIDs, id)
		cityIDs = append(cityIDs, city.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("postgres: content apply: finding players with no city: %w", err)
	}
	if len(playerIDs) == 0 {
		return 0, nil
	}

	tag, err := tx.Exec(ctx, placeUnplacedStatement, playerIDs, cityIDs, time.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("postgres: content apply: placing players with no city: %w", err)
	}
	return tag.RowsAffected(), nil
}

// houseUnhousedStatement gives every player who has a city but no residence
// their current city as their residence. Two kinds of player match:
//
//   - players placeUnplacedStatement just placed, for whom the current city IS
//     the spawn city, so they end up living where they were born — the same
//     outcome first contact gives a new player;
//   - players who were already somewhere when migration 0005 introduced
//     residence. Where they stand is the best record there is of where they
//     live.
//
// Like the placement, it never changes a residence that is already set.
const houseUnhousedStatement = `
UPDATE players
   SET residence_city_id = city_id,
       updated_at        = $1
 WHERE residence_city_id IS NULL
   AND city_id IS NOT NULL`

// houseUnhousedPlayers runs houseUnhousedStatement and returns how many
// players it gave a residence. It must run after placeUnplacedPlayers.
func houseUnhousedPlayers(ctx context.Context, tx pgx.Tx) (int64, error) {
	tag, err := tx.Exec(ctx, houseUnhousedStatement, time.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("postgres: content apply: giving players a residence: %w", err)
	}
	return tag.RowsAffected(), nil
}

// activeWorld is the part of the active version the audit row compares
// against.
type activeWorld struct {
	version  int
	checksum string
	codes    map[string]struct{}
}

// currentWorld reads the active version's number, checksum and city codes. A
// database that has never been loaded answers version 0 and no cities.
func currentWorld(ctx context.Context, tx pgx.Tx) (activeWorld, error) {
	w := activeWorld{codes: map[string]struct{}{}}
	var id string
	err := tx.QueryRow(ctx,
		`SELECT id::text, version, source_checksum FROM content_versions WHERE status = 'active'`).
		Scan(&id, &w.version, &w.checksum)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, nil
	}
	if err != nil {
		return w, fmt.Errorf("postgres: content apply: reading the active version: %w", err)
	}

	rows, err := tx.Query(ctx, `SELECT code FROM cities WHERE content_version_id = $1::uuid`, id)
	if err != nil {
		return w, fmt.Errorf("postgres: content apply: reading active cities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return w, fmt.Errorf("postgres: content apply: scanning active city: %w", err)
		}
		w.codes[code] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return w, fmt.Errorf("postgres: content apply: reading active cities: %w", err)
	}
	return w, nil
}

// LoadActive reads the active version back out as a pack.
//
// This is how a service boots. ADR 0004 rule 6 is explicit that the initial
// load comes from the database and not from NATS and not from the files: a
// running container may not even have configs/content/ on disk, and if it does
// there is no guarantee it holds what was loaded.
//
// The returned pack has Version set to the stored load number, which is what
// BuildSnapshot wants. Two fields are deliberately NOT set:
//
//   - Schema, the `version:` key the files declare, because content_versions
//     does not record it. It describes the shape of the files and nothing read
//     back out of the database needs it.
//   - Checksum, because the pack's checksum is a digest of source FILES and
//     there are none here. The stored digest belongs to the version row; use
//     Active to read it.
//
// CityIDs is filled, so the snapshot built from this pack can answer lookups
// by storage id — which a pack loaded from yaml cannot.
//
// All the reads — governance included — run in ONE read-only REPEATABLE READ
// transaction. They were
// four independent statements in the first draft, and a load committing
// between them breaks the result: cities are rewritten in place (their
// content_version_id moves to the new version), so the city query would find
// none of the old version's cities while the route query still found its
// edges, and BuildSnapshot would refuse the pack as a route to nowhere. One
// snapshot means one version, whole.
func (s *ContentStore) LoadActive(ctx context.Context) (*content.Pack, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("postgres: content load: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var (
		versionID string
		version   int
	)
	err = tx.QueryRow(ctx,
		`SELECT id::text, version FROM content_versions WHERE status = 'active'`).Scan(&versionID, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoActiveVersion
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: content load: active version: %w", err)
	}

	pack := &content.Pack{Version: version, CityIDs: map[string]string{}}

	// Ordered by code, not by insertion: a pack read back twice must be the
	// same pack, and the checksum comparison an operator makes between a
	// stored version and a checkout depends on nothing here being arbitrary.
	// A city's country is the parent of its own jurisdiction — or, for a
	// city another country holds by conquest, the content's country that
	// city_control remembers: the pack is what was loaded, not the war. A
	// city row with no jurisdiction reads back with no country, which
	// Validate then refuses by name rather than letting a half-migrated
	// world boot.
	cityRows, err := tx.Query(ctx,
		`SELECT c.id::text, c.code, c.name, c.tax_rate_bps, c.cost_of_living, c.spawn_weight,
		        COALESCE(dj.code, p.code, '')
		   FROM cities c
		   LEFT JOIN jurisdictions j ON j.id = c.jurisdiction_id
		   LEFT JOIN jurisdictions p ON p.id = j.parent_id
		   LEFT JOIN city_control cc ON cc.city_id = c.id
		   LEFT JOIN jurisdictions dj ON dj.id = cc.de_jure_country_id
		  WHERE c.content_version_id = $1::uuid
		  ORDER BY c.code`, versionID)
	if err != nil {
		return nil, fmt.Errorf("postgres: content load: cities: %w", err)
	}
	defer cityRows.Close()
	for cityRows.Next() {
		var (
			id string
			c  content.CityDef
		)
		if err := cityRows.Scan(&id, &c.Code, &c.Name, &c.TaxRateBPS, &c.CostOfLiving, &c.SpawnWeight, &c.Country); err != nil {
			return nil, fmt.Errorf("postgres: content load: scanning city: %w", err)
		}
		pack.Cities = append(pack.Cities, c)
		pack.CityIDs[c.Code] = id
	}
	if err := cityRows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: content load: reading cities: %w", err)
	}
	cityRows.Close()

	// The join is on the city rows of THIS version. A route always points at
	// cities the same load wrote, so an inner join cannot drop an edge; if it
	// ever did, a missing edge would be far better than a route naming a city
	// the pack does not contain, which would fail Validate with a message
	// blaming the content instead of the query.
	routeRows, err := tx.Query(ctx,
		`SELECT f.code, t.code, r.distance, r.bidirectional
		   FROM city_routes r
		   JOIN cities f ON f.id = r.from_city_id
		   JOIN cities t ON t.id = r.to_city_id
		  WHERE r.content_version_id = $1::uuid
		  ORDER BY f.code, t.code`, versionID)
	if err != nil {
		return nil, fmt.Errorf("postgres: content load: routes: %w", err)
	}
	defer routeRows.Close()
	for routeRows.Next() {
		var (
			r    content.RouteDef
			both bool
		)
		if err := routeRows.Scan(&r.From, &r.To, &r.Distance, &both); err != nil {
			return nil, fmt.Errorf("postgres: content load: scanning route: %w", err)
		}
		// Always explicit on the way out. RouteDef.Bidirectional is a pointer
		// precisely because an absent key means true, and leaving it nil here
		// would make a one-way route read back as two-way.
		flag := both
		r.Bidirectional = &flag
		pack.Routes = append(pack.Routes, r)
	}
	if err := routeRows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: content load: reading routes: %w", err)
	}
	routeRows.Close()

	skillRows, err := tx.Query(ctx,
		`SELECT code, name, category
		   FROM skill_definitions
		  WHERE content_version_id = $1::uuid
		  ORDER BY code`, versionID)
	if err != nil {
		return nil, fmt.Errorf("postgres: content load: skills: %w", err)
	}
	defer skillRows.Close()
	for skillRows.Next() {
		var s content.SkillDef
		if err := skillRows.Scan(&s.Code, &s.Name, &s.Category); err != nil {
			return nil, fmt.Errorf("postgres: content load: scanning skill: %w", err)
		}
		pack.Skills = append(pack.Skills, s)
	}
	if err := skillRows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: content load: reading skills: %w", err)
	}
	skillRows.Close()

	if err := loadGovernance(ctx, tx, versionID, pack); err != nil {
		return nil, err
	}
	if err := loadJobs(ctx, tx, versionID, pack); err != nil {
		return nil, err
	}
	if err := loadTransport(ctx, tx, versionID, pack); err != nil {
		return nil, err
	}
	if err := loadCrimes(ctx, tx, versionID, pack); err != nil {
		return nil, err
	}
	if err := loadDocuments(ctx, tx, versionID, pack); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres: content load: commit: %w", err)
	}
	return pack, nil
}

// ActiveVersion describes the version currently in force, without reading the
// content itself. It is what a status command prints.
type ActiveVersion struct {
	ID       string
	Version  int
	LoadedAt time.Time
	LoadedBy string
	Checksum string
	Notes    string
}

// Active returns the active version row, or ErrNoActiveVersion.
func (s *ContentStore) Active(ctx context.Context) (ActiveVersion, error) {
	var v ActiveVersion
	err := s.pool.QueryRow(ctx,
		`SELECT id::text, version, loaded_at, loaded_by, source_checksum, notes
		   FROM content_versions
		  WHERE status = 'active'`).
		Scan(&v.ID, &v.Version, &v.LoadedAt, &v.LoadedBy, &v.Checksum, &v.Notes)
	if errors.Is(err, pgx.ErrNoRows) {
		return ActiveVersion{}, ErrNoActiveVersion
	}
	if err != nil {
		return ActiveVersion{}, fmt.Errorf("postgres: content: active version: %w", err)
	}
	return v, nil
}

// refuseRemovalOfCitiesInUse implements ADR 0004 rule 7 and returns the codes
// this load retires.
//
// A city of the current world that no longer appears in the files would be
// retired by this load. If a player is standing in it, lives in it (their
// residence), or is travelling to or from it, the load is refused: a player in a city that is not part of the world is
// a broken save, and discovering it when the player next opens a screen is far
// worse than refusing here.
//
// "The current world" is the active version's cities plus any row with no
// version at all (written before the content system existed). A city an
// earlier load already retired is not being removed by THIS load, and must not
// block every future load forever.
//
// # Why the rows are locked first, in a separate statement
//
// The advisory lock serialises loaders against each other; it does nothing
// about the game service, which never takes it. In the first draft a player
// could therefore move into a city after the count and before the commit.
// So the retiring city rows are locked FOR UPDATE first. Writing a
// players.city_id, a players.residence_city_id or a travels row that
// references a city takes a FOR KEY SHARE lock on that city, which conflicts with FOR UPDATE: from here to
// commit nobody can start referencing a retiring city, and anybody who was
// mid-way through doing so has finished before the lock is granted. The count
// is then a SECOND statement, because under READ COMMITTED each statement
// takes a fresh snapshot, so it sees every reference committed while this
// transaction was waiting for the lock.
func refuseRemovalOfCitiesInUse(ctx context.Context, tx pgx.Tx, p *content.Pack) ([]string, error) {
	codes := make([]string, 0, len(p.Cities))
	for _, c := range p.Cities {
		codes = append(codes, c.Code)
	}

	lockRows, err := tx.Query(ctx,
		`SELECT c.code
		   FROM cities c
		  WHERE NOT (c.code = ANY($1::text[]))
		    AND (c.content_version_id IS NULL
		         OR c.content_version_id IN (SELECT id FROM content_versions WHERE status = 'active'))
		  ORDER BY c.code
		    FOR UPDATE OF c`, codes)
	if err != nil {
		return nil, fmt.Errorf("postgres: content apply: locking retiring cities: %w", err)
	}
	var retiring []string
	for lockRows.Next() {
		var code string
		if err := lockRows.Scan(&code); err != nil {
			lockRows.Close()
			return nil, fmt.Errorf("postgres: content apply: scanning retiring city: %w", err)
		}
		retiring = append(retiring, code)
	}
	lockRows.Close()
	if err := lockRows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: content apply: locking retiring cities: %w", err)
	}
	if len(retiring) == 0 {
		return nil, nil
	}

	// Players are counted regardless of status: a banned or deleted player
	// still holds a city_id and a residence_city_id, and the foreign key does
	// not care why. Present and resident are counted apart because they are
	// different facts: a traveller standing in a city does not live there.
	rows, err := tx.Query(ctx,
		`SELECT c.code,
		        (SELECT count(*) FROM players pl WHERE pl.city_id = c.id) AS present,
		        (SELECT count(*) FROM players pl WHERE pl.residence_city_id = c.id) AS residents,
		        (SELECT count(*) FROM travels t  WHERE (t.to_city_id = c.id OR t.from_city_id = c.id)
		                                           AND t.status = 'in_transit') AS journeys
		   FROM cities c
		  WHERE c.code = ANY($1::text[])
		  ORDER BY c.code`, retiring)
	if err != nil {
		return nil, fmt.Errorf("postgres: content apply: checking cities in use: %w", err)
	}
	defer rows.Close()

	var blocked []string
	for rows.Next() {
		var code string
		var present, residents, journeys int64
		if err := rows.Scan(&code, &present, &residents, &journeys); err != nil {
			return nil, fmt.Errorf("postgres: content apply: scanning cities in use: %w", err)
		}
		if present > 0 || residents > 0 || journeys > 0 {
			blocked = append(blocked,
				fmt.Sprintf("%s (%d player(s) present, %d resident(s), %d journey(s) in transit)",
					code, present, residents, journeys))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: content apply: reading cities in use: %w", err)
	}
	if len(blocked) > 0 {
		return nil, fmt.Errorf("%w:%s", ErrCityInUse, joinLines(blocked))
	}
	return retiring, nil
}

// joinLines renders a list of offenders one per line, so a refusal naming six
// cities is readable in a terminal instead of one long line.
func joinLines(items []string) string {
	out := ""
	for _, it := range items {
		out += "\n  - " + it
	}
	return out
}

// upsertCity writes one city row; see upsertCities.
const upsertCity = `
INSERT INTO cities (id, code, name, tax_rate_bps, cost_of_living, population, content_version_id, spawn_weight)
     VALUES ($1::uuid, $2, $3, $4, $5, 0, $6::uuid, $7)
ON CONFLICT (code) DO UPDATE
   SET name               = EXCLUDED.name,
       tax_rate_bps       = EXCLUDED.tax_rate_bps,
       cost_of_living     = EXCLUDED.cost_of_living,
       content_version_id = EXCLUDED.content_version_id,
       spawn_weight       = EXCLUDED.spawn_weight
 RETURNING id::text`

// upsertCities writes each city and returns code to id.
//
// Cities are matched on code, never on id: the code is the stable identity an
// author writes and a route refers to. A city that already exists keeps its
// id, so every player standing in it keeps standing in it and every past
// travel row still points somewhere real.
//
// population is inserted as zero and NEVER updated: it is world state the
// simulation moves, not content, so a load must leave it exactly as it was.
// The same goes for treasury_account_id, which is why neither appears in the
// DO UPDATE list.
//
// spawn_weight IS written, for every city, zero included: it is content, and a
// city whose weight an author lowered to 0 must stop receiving newcomers. A
// city this load retires keeps its last weight, but it also keeps the old
// content_version_id, and new players are only ever picked from the active
// version's cities.
func upsertCities(ctx context.Context, tx pgx.Tx, p *content.Pack, versionID string) (map[string]string, error) {
	ids := make(map[string]string, len(p.Cities))
	for _, c := range p.Cities {
		id, err := newUUID()
		if err != nil {
			return nil, fmt.Errorf("postgres: content apply: city %q: %w", c.Code, err)
		}
		var stored string
		err = tx.QueryRow(ctx,
			upsertCity,
			id, c.Code, c.Name, c.TaxRateBPS, c.CostOfLiving, versionID, c.SpawnWeight).Scan(&stored)
		if err != nil {
			return nil, fmt.Errorf("postgres: content apply: city %q: %w", c.Code, err)
		}
		ids[c.Code] = stored
	}
	return ids, nil
}

// insertRoutes writes this version's edges.
//
// A bidirectional route is stored as the single row the author wrote; the
// reverse direction is implied and internal/domain/world applies it when the
// graph is built. Storing both directions would mean two rows that can drift.
//
// Rows are inserted fresh for every version rather than updated, which is what
// makes ADR 0004 rule 4 work: the previous version's edges are still there, so
// re-activating it needs no file and no deploy.
func insertRoutes(ctx context.Context, tx pgx.Tx, p *content.Pack, versionID string, cityIDs map[string]string) error {
	for _, r := range p.Routes {
		from, ok := cityIDs[r.From]
		if !ok {
			return fmt.Errorf("postgres: content apply: route from unknown city %q", r.From)
		}
		to, ok := cityIDs[r.To]
		if !ok {
			return fmt.Errorf("postgres: content apply: route to unknown city %q", r.To)
		}
		id, err := newUUID()
		if err != nil {
			return fmt.Errorf("postgres: content apply: route %s->%s: %w", r.From, r.To, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO city_routes (id, from_city_id, to_city_id, distance, bidirectional, content_version_id)
			 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::uuid)`,
			id, from, to, r.Distance, r.IsBidirectional(), versionID); err != nil {
			return fmt.Errorf("postgres: content apply: route %s->%s: %w", r.From, r.To, err)
		}
	}
	return nil
}

// insertSkills writes this version's skill definitions.
func insertSkills(ctx context.Context, tx pgx.Tx, p *content.Pack, versionID string) error {
	for _, s := range p.Skills {
		id, err := newUUID()
		if err != nil {
			return fmt.Errorf("postgres: content apply: skill %q: %w", s.Code, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO skill_definitions (id, code, name, category, content_version_id)
			 VALUES ($1::uuid, $2, $3, $4, $5::uuid)`,
			id, s.Code, s.Name, s.Category, versionID); err != nil {
			return fmt.Errorf("postgres: content apply: skill %q: %w", s.Code, err)
		}
	}
	return nil
}

// appendContentEvent queues the reload announcement in the same transaction as
// the content itself, so the event cannot describe a version that was rolled
// back, nor be lost once one was committed.
//
// It writes the outbox row directly rather than through OutboxRepository,
// because that repository takes a querier bound to a unit of work and this
// transaction is not one.
func appendContentEvent(ctx context.Context, tx pgx.Tx, version int, versionID, checksum string) error {
	payload, err := json.Marshal(map[string]any{
		"version":    version,
		"version_id": versionID,
		"checksum":   checksum,
	})
	if err != nil {
		return fmt.Errorf("postgres: content apply: encoding event: %w", err)
	}

	eventID, err := newUUID()
	if err != nil {
		return fmt.Errorf("postgres: content apply: %w", err)
	}

	// A content load has no Telegram origin, so the identity fields that carry
	// a chat and a user are left at zero. The four the envelope actually
	// requires are filled from the load itself, which makes every consumer
	// able to trace a reload back to the version that caused it.
	now := time.Now().UTC()
	meta := envelope.Metadata{
		RequestID:         "content-" + versionID,
		TraceID:           "content-" + versionID,
		BotID:             "system",
		GatewayInstanceID: "admin",
		ChatType:          "system",
		UpdateType:        "content",
		Command:           "content.publish",
		Language:          "en",
		ReceivedAt:        now,
		SchemaVersion:     envelope.SchemaVersion,
	}
	if err := meta.Validate(); err != nil {
		// Unreachable unless the envelope contract changes underneath this
		// function. Caught here rather than by a consumer at 3am.
		return fmt.Errorf("postgres: content apply: event metadata: %w", err)
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("postgres: content apply: encoding metadata: %w", err)
	}

	_, err = tx.Exec(ctx, insertOutbox,
		eventID, subjects.Event("content", "published"),
		string(metaJSON), string(payload), StatusPending, now)
	if err != nil {
		return fmt.Errorf("postgres: content apply: outbox: %w", err)
	}
	return nil
}

// contentAudit is everything the audit row for one load records.
type contentAudit struct {
	version   int
	versionID string
	checksum  string
	previous  activeWorld
	removed   []string
	pack      *content.Pack
	actor     string
	reason    string
	placed    int64
	housed    int64
	governed  governanceApplied
}

// appendContentAudit records the load in audit_logs (ADR 0004 rule 5: who,
// when, which version, and what changed).
//
// old_value names the version this load replaced and new_value the version it
// created, each with its checksum, so a row read on its own says what moved.
// The difference is recorded at the level an investigator asks about first —
// which cities entered or left the world — rather than as a full row diff;
// the complete content of both versions is still in the database under their
// version ids. old_value is NULL for the very first load, because there was
// no previous value.
func appendContentAudit(ctx context.Context, tx pgx.Tx, a contentAudit) error {
	var oldValue any
	if a.previous.version > 0 {
		b, err := json.Marshal(map[string]any{
			"version":         a.previous.version,
			"source_checksum": a.previous.checksum,
			"cities":          len(a.previous.codes),
		})
		if err != nil {
			return fmt.Errorf("postgres: content apply: encoding audit value: %w", err)
		}
		oldValue = string(b)
	}

	added := []string{}
	for _, c := range a.pack.Cities {
		if _, ok := a.previous.codes[c.Code]; !ok {
			added = append(added, c.Code)
		}
	}
	sort.Strings(added)
	removed := append([]string{}, a.removed...)

	newValue, err := json.Marshal(map[string]any{
		"version":         a.version,
		"version_id":      a.versionID,
		"source_checksum": a.checksum,
		"cities":          len(a.pack.Cities),
		"routes":          len(a.pack.Routes),
		"skills":          len(a.pack.Skills),
		"cities_added":    added,
		"cities_removed":  removed,
		"same_source":     a.previous.version > 0 && a.previous.checksum == a.checksum,
		"spawn_weights":   spawnWeights(a.pack),
		"players_placed":  a.placed,
		"residences_set":  a.housed,
		"levels":          len(a.pack.Levels),
		"jurisdictions":   a.governed.jurisdictions,
		"levers":          len(a.pack.Levers),
		"offices":         len(a.pack.Offices),
		"offices_created": a.governed.officesCreated,
		"careers":         len(a.pack.Careers),
		"courses":         len(a.pack.Courses),
		"transport_modes": len(a.pack.TransportModes),
		"crimes":          len(a.pack.Crimes),
		"venues":          len(a.pack.Venues),
	})
	if err != nil {
		return fmt.Errorf("postgres: content apply: encoding audit value: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_logs (actor, action, target_type, target_id, old_value, new_value, reason, created_at)
		 VALUES ($1, $2, $3, $4::uuid, $5::jsonb, $6::jsonb, $7, $8)`,
		a.actor, "content.load", "content_version", a.versionID,
		oldValue, string(newValue), a.reason, time.Now().UTC()); err != nil {
		return fmt.Errorf("postgres: content apply: audit row: %w", err)
	}
	return nil
}

// spawnWeights is the pack's positive spawn weights by city code, for the
// audit row: "where were newcomers being sent after this load" is a question
// an investigator asks, and the answer should not need a join.
func spawnWeights(p *content.Pack) map[string]int {
	out := map[string]int{}
	for _, c := range p.Cities {
		if c.SpawnWeight > 0 {
			out[c.Code] = c.SpawnWeight
		}
	}
	return out
}

// SourceChecksum is what goes into content_versions.source_checksum.
//
// The column's job is to answer "is the checkout in front of me the content
// production is running?", so the digest of the SOURCE FILES is preferred: it
// changes when a comment changes, when a file is renamed, when two files are
// merged — all of which make a checkout a different checkout even though the
// loaded world is identical.
//
// Checksum is the fallback for a pack that was never read from files (one
// assembled in a test, or read back out of the database). It digests the
// values instead, which is weaker but never wrong.
func SourceChecksum(p *content.Pack) string {
	if p.Checksum != "" {
		return p.Checksum
	}
	return Checksum(p)
}

// Checksum fingerprints a pack's values so two loads of identical content are
// visible as identical in content_versions, without having to diff the rows.
//
// The order is the pack's own order, which Load fixes by sorting the file
// names; two packs that differ only in the order their entries were listed are
// different packs as far as this is concerned, and that is deliberate — the
// file is the artefact being fingerprinted.
func Checksum(p *content.Pack) string {
	h := sha256.New()
	for _, c := range p.Cities {
		fmt.Fprintf(h, "city|%s|%s|%d|%d|%d|%s\n", c.Code, c.Name, c.TaxRateBPS, c.CostOfLiving, c.SpawnWeight, c.Country)
	}
	for _, r := range p.Routes {
		fmt.Fprintf(h, "route|%s|%s|%d|%t\n", r.From, r.To, r.Distance, r.IsBidirectional())
	}
	for _, s := range p.Skills {
		fmt.Fprintf(h, "skill|%s|%s|%s\n", s.Code, s.Name, s.Category)
	}
	for _, l := range p.Levels {
		fmt.Fprintf(h, "level|%s|%v|%t\n", l.Code, l.Parents, l.Overlay)
	}
	for _, j := range p.Jurisdictions {
		fmt.Fprintf(h, "jurisdiction|%s|%s|%s|%s\n", j.Code, j.Name, j.Level, j.Parent)
	}
	for _, o := range p.Offices {
		limit := 0
		if o.TermLimit != nil {
			limit = *o.TermLimit
		}
		term, _ := o.TermDuration()
		levers := append([]string(nil), o.Levers...)
		sort.Strings(levers)
		fmt.Fprintf(h, "office|%s|%s|%d|%s|%v|%s|%s|%s|%d|%d|%v|%v|%v\n",
			o.Code, o.Jurisdiction, o.Seats, o.AcquiredBy, levers, o.Deputy, o.AppointedBy,
			o.RequiresConfirmationBy, int64(term/time.Second), limit, o.CanBeRemovedBy, o.VetoOver, o.IncompatibleWith)
	}
	for _, l := range p.Levers {
		// Durations as seconds, so "72h" from a file and "72h0m0s" read back
		// from the database fingerprint alike.
		cooldown, _ := l.CooldownDuration()
		notice, _ := l.NoticeDuration()
		def := fmt.Sprint(l.DefaultValue())
		if l.ValueKind() != content.ValueKindScalar {
			raw, _ := l.DefaultJSON() // encoding/json sorts map keys
			def = string(raw)
		}
		fmt.Fprintf(h, "lever|%s|%s|%s|%s|%d|%d|%v|%s|%v|%s|%s|%s|%s|%s|%v|%s|%s|%d|%d\n",
			l.Code, l.Jurisdiction, l.Type, def, l.MinValue(), l.MaxValue(), l.Options, l.Key, l.Categories,
			l.CityDefault,
			l.HeldBy, l.Rule(), l.Threshold, l.Quorum, l.VetoBy, l.OverrideRule, l.OverrideThreshold,
			int64(cooldown/time.Second), int64(notice/time.Second))
	}
	checksumJobs(h, p)
	checksumTransport(h, p)
	checksumCrimes(h, p)
	checksumDocuments(h, p)
	return hex.EncodeToString(h.Sum(nil))
}
