package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
}

// Apply writes a pack as a new active version, in one transaction.
//
// Everything happens together: the version row, the cities, the routes, the
// skills, the supersede of the previous version, the outbox record that tells
// running services to reload, and the audit row that records who did it. A
// failure anywhere leaves the world exactly as it was.
//
// The audit row is written HERE rather than by the calling command. ADR 0004
// rule 5 requires that every load leave one, and a row written after the
// commit is a row that is missing whenever the loader dies in between —
// exactly the load an investigator would most want to find.
//
// Apply does not validate the pack. That is the caller's job and it must have
// happened before anything reached this point (ADR 0004 rule 1); re-running it
// here would only hide a caller that forgot.
func (s *ContentStore) Apply(ctx context.Context, p *content.Pack, req ApplyRequest) (Applied, error) {
	if p == nil {
		return Applied{}, fmt.Errorf("postgres: content apply: no pack")
	}
	if req.Reason == "" {
		return Applied{}, ErrNoReason
	}
	actor := req.Actor
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

	if err := refuseRemovalOfCitiesInUse(ctx, tx, p); err != nil {
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
		versionID, version, time.Now().UTC(), actor, checksum, req.Reason); err != nil {
		return Applied{}, fmt.Errorf("postgres: content apply: version row: %w", err)
	}

	cityIDs, err := upsertCities(ctx, tx, p, versionID)
	if err != nil {
		return Applied{}, err
	}
	if err := insertRoutes(ctx, tx, p, versionID, cityIDs); err != nil {
		return Applied{}, err
	}
	if err := insertSkills(ctx, tx, p, versionID); err != nil {
		return Applied{}, err
	}
	if err := appendContentEvent(ctx, tx, version, versionID, checksum); err != nil {
		return Applied{}, err
	}
	if err := appendContentAudit(ctx, tx, version, versionID, checksum, p, actor, req.Reason); err != nil {
		return Applied{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Applied{}, fmt.Errorf("postgres: content apply: commit: %w", err)
	}
	return Applied{Version: version, VersionID: versionID, Checksum: checksum}, nil
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
//     ActiveVersion to read it.
//
// CityIDs is filled, so the snapshot built from this pack can answer lookups
// by storage id — which a pack loaded from yaml cannot.
func (s *ContentStore) LoadActive(ctx context.Context) (*content.Pack, error) {
	var (
		versionID string
		version   int
	)
	err := s.pool.QueryRow(ctx,
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
	cityRows, err := s.pool.Query(ctx,
		`SELECT id::text, code, name, tax_rate_bps, cost_of_living
		   FROM cities
		  WHERE content_version_id = $1::uuid
		  ORDER BY code`, versionID)
	if err != nil {
		return nil, fmt.Errorf("postgres: content load: cities: %w", err)
	}
	defer cityRows.Close()
	for cityRows.Next() {
		var (
			id string
			c  content.CityDef
		)
		if err := cityRows.Scan(&id, &c.Code, &c.Name, &c.TaxRateBPS, &c.CostOfLiving); err != nil {
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
	routeRows, err := s.pool.Query(ctx,
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

	skillRows, err := s.pool.Query(ctx,
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

// refuseRemovalOfCitiesInUse implements ADR 0004 rule 7.
//
// A city that no longer appears in the files would be orphaned by this load.
// If a player is standing in it or travelling to it, the load is refused: a
// dangling city_id is a broken save, and discovering it when the player next
// opens a screen is far worse than refusing here.
//
// It runs inside the applying transaction and after the advisory lock, so the
// answer cannot change between the check and the write.
func refuseRemovalOfCitiesInUse(ctx context.Context, tx pgx.Tx, p *content.Pack) error {
	codes := make([]string, 0, len(p.Cities))
	for _, c := range p.Cities {
		codes = append(codes, c.Code)
	}

	// Residents are counted regardless of player status: a banned or deleted
	// player still holds a city_id, and the foreign key does not care why.
	rows, err := tx.Query(ctx,
		`SELECT c.code,
		        (SELECT count(*) FROM players pl WHERE pl.city_id = c.id) AS residents,
		        (SELECT count(*) FROM travels t  WHERE (t.to_city_id = c.id OR t.from_city_id = c.id)
		                                           AND t.status = 'in_transit') AS journeys
		   FROM cities c
		  WHERE NOT (c.code = ANY($1::text[]))`, codes)
	if err != nil {
		return fmt.Errorf("postgres: content apply: checking cities in use: %w", err)
	}
	defer rows.Close()

	var blocked []string
	for rows.Next() {
		var code string
		var residents, journeys int64
		if err := rows.Scan(&code, &residents, &journeys); err != nil {
			return fmt.Errorf("postgres: content apply: scanning cities in use: %w", err)
		}
		if residents > 0 || journeys > 0 {
			blocked = append(blocked,
				fmt.Sprintf("%s (%d resident(s), %d journey(s) in transit)", code, residents, journeys))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: content apply: reading cities in use: %w", err)
	}
	if len(blocked) > 0 {
		sort.Strings(blocked)
		return fmt.Errorf("%w: %s", ErrCityInUse, joinLines(blocked))
	}
	return nil
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
func upsertCities(ctx context.Context, tx pgx.Tx, p *content.Pack, versionID string) (map[string]string, error) {
	ids := make(map[string]string, len(p.Cities))
	for _, c := range p.Cities {
		id, err := newUUID()
		if err != nil {
			return nil, fmt.Errorf("postgres: content apply: city %q: %w", c.Code, err)
		}
		var stored string
		err = tx.QueryRow(ctx,
			`INSERT INTO cities (id, code, name, tax_rate_bps, cost_of_living, population, content_version_id)
			      VALUES ($1::uuid, $2, $3, $4, $5, 0, $6::uuid)
			 ON CONFLICT (code) DO UPDATE
			    SET name               = EXCLUDED.name,
			        tax_rate_bps       = EXCLUDED.tax_rate_bps,
			        cost_of_living     = EXCLUDED.cost_of_living,
			        content_version_id = EXCLUDED.content_version_id
			  RETURNING id::text`,
			id, c.Code, c.Name, c.TaxRateBPS, c.CostOfLiving, versionID).Scan(&stored)
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

// appendContentAudit records the load in audit_logs (ADR 0004 rule 5).
//
// old_value is left NULL: there is no previous value for "a new version
// appeared", and writing the superseded version there would suggest a diff
// this row does not contain. new_value holds what the version is, which is
// enough to answer "what did this load change" when set beside the row before
// it.
func appendContentAudit(ctx context.Context, tx pgx.Tx, version int, versionID, checksum string, p *content.Pack, actor, reason string) error {
	newValue, err := json.Marshal(map[string]any{
		"version":         version,
		"version_id":      versionID,
		"source_checksum": checksum,
		"cities":          len(p.Cities),
		"routes":          len(p.Routes),
		"skills":          len(p.Skills),
	})
	if err != nil {
		return fmt.Errorf("postgres: content apply: encoding audit value: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_logs (actor, action, target_type, target_id, old_value, new_value, reason, created_at)
		 VALUES ($1, $2, $3, $4::uuid, NULL, $5::jsonb, $6, $7)`,
		actor, "content.load", "content_version", versionID,
		string(newValue), reason, time.Now().UTC()); err != nil {
		return fmt.Errorf("postgres: content apply: audit row: %w", err)
	}
	return nil
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
		fmt.Fprintf(h, "city|%s|%s|%d|%d\n", c.Code, c.Name, c.TaxRateBPS, c.CostOfLiving)
	}
	for _, r := range p.Routes {
		fmt.Fprintf(h, "route|%s|%s|%d|%t\n", r.From, r.To, r.Distance, r.IsBidirectional())
	}
	for _, s := range p.Skills {
		fmt.Fprintf(h, "skill|%s|%s|%s\n", s.Code, s.Name, s.Category)
	}
	return hex.EncodeToString(h.Sum(nil))
}
