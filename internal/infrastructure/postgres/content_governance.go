package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
)

// This file is the governance part of a content load (ADR 0015): levels,
// jurisdictions, office and lever definitions, and the vacant seats. It runs
// inside ContentStore.Apply's transaction, after the cities are written, so a
// load either lands all of it or none of it.

// governanceApplied is what the governance part of a load reports.
type governanceApplied struct {
	// jurisdictions is how many jurisdictions (cities included) this load
	// wrote.
	jurisdictions int
	// officesCreated is how many seats were new. Zero on every reload of
	// unchanged content: seats are matched on (office, jurisdiction, seat).
	officesCreated int64
}

// applyGovernance writes the pack's governance content under versionID.
// cityIDs maps each city code to the id upsertCities stored it under.
func applyGovernance(ctx context.Context, tx pgx.Tx, p *content.Pack, versionID string,
	cityIDs map[string]string, now time.Time,
) (governanceApplied, error) {
	var out governanceApplied

	if err := insertLevels(ctx, tx, p, versionID); err != nil {
		return out, err
	}
	byLevel, err := upsertJurisdictions(ctx, tx, p, versionID, cityIDs)
	if err != nil {
		return out, err
	}
	for _, ids := range byLevel {
		out.jurisdictions += len(ids)
	}
	if err := insertOfficeDefinitions(ctx, tx, p, versionID); err != nil {
		return out, err
	}
	if err := insertLeverDefinitions(ctx, tx, p, versionID); err != nil {
		return out, err
	}
	out.officesCreated, err = ensureSeats(ctx, tx, p, byLevel, now)
	if err != nil {
		return out, err
	}
	return out, nil
}

// upsertLevel writes one level into the registry, matched on code like a
// city: jurisdictions and definitions hold foreign keys to it, so a level
// keeps its row, and a level a load no longer declares keeps its row and its
// old version.
const upsertLevel = `
INSERT INTO jurisdiction_levels (code, parents, overlay, content_version_id)
     VALUES ($1, $2, $3, $4::uuid)
ON CONFLICT (code) DO UPDATE
   SET parents            = EXCLUDED.parents,
       overlay            = EXCLUDED.overlay,
       content_version_id = EXCLUDED.content_version_id`

// insertLevels writes this version's levels. It runs first: every other
// governance row names a level.
func insertLevels(ctx context.Context, tx pgx.Tx, p *content.Pack, versionID string) error {
	for _, l := range p.Levels {
		if _, err := tx.Exec(ctx, upsertLevel, l.Code, nonNil(l.Parents), l.Overlay, versionID); err != nil {
			return fmt.Errorf("postgres: content apply: level %q: %w", l.Code, err)
		}
	}
	return nil
}

// upsertJurisdiction writes one jurisdiction, matched on (kind, code) so it
// keeps its id across loads: offices and policy values point at it.
const upsertJurisdiction = `
INSERT INTO jurisdictions (id, kind, code, name, parent_id, content_version_id)
     VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6::uuid)
ON CONFLICT (kind, code) DO UPDATE
   SET name               = EXCLUDED.name,
       parent_id          = EXCLUDED.parent_id,
       content_version_id = EXCLUDED.content_version_id
 RETURNING id::text`

// upsertCityJurisdiction is upsertJurisdiction for a city's own
// jurisdiction: a city another country holds by conquest (city_control,
// migration 0022) stays under the country that holds it — a content load
// never undoes a war. Its content country is kept in city_control.
const upsertCityJurisdiction = `
INSERT INTO jurisdictions (id, kind, code, name, parent_id, content_version_id)
     VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6::uuid)
ON CONFLICT (kind, code) DO UPDATE
   SET name               = EXCLUDED.name,
       parent_id          = CASE WHEN EXISTS (SELECT 1 FROM city_control cc JOIN cities c ON c.id = cc.city_id
                                               WHERE c.jurisdiction_id = jurisdictions.id)
                                 THEN jurisdictions.parent_id ELSE EXCLUDED.parent_id END,
       content_version_id = EXCLUDED.content_version_id
 RETURNING id::text`

// pendingJurisdiction is one jurisdiction waiting for its parent's id.
type pendingJurisdiction struct {
	kind, code, name, parent string // parent "" = the world
	cityID                   string
}

// upsertJurisdictions writes every declared jurisdiction and one jurisdiction
// per city, parents before children, links each city to its own, and returns
// the ids written by level.
//
// Parents come first because a row's parent_id must exist. The order is found
// by resolving whatever has a known parent until nothing is left: a country
// under the world, a city under its country, a port authority under a city.
// Validate has already refused a cycle, so each pass resolves at least one.
func upsertJurisdictions(ctx context.Context, tx pgx.Tx, p *content.Pack, versionID string,
	cityIDs map[string]string,
) (map[string][]string, error) {
	pending := make([]pendingJurisdiction, 0, len(p.Jurisdictions)+len(p.Cities))
	for _, j := range p.Jurisdictions {
		pending = append(pending, pendingJurisdiction{kind: j.Level, code: j.Code, name: j.Name, parent: j.Parent})
	}
	for _, c := range p.Cities {
		pending = append(pending, pendingJurisdiction{
			kind: content.CityLevel, code: c.Code, name: c.Name, parent: c.Country, cityID: cityIDs[c.Code],
		})
	}

	ids := map[string]string{} // code -> id; codes are unique across the pack
	byLevel := map[string][]string{}
	for len(pending) > 0 {
		var next []pendingJurisdiction
		for _, j := range pending {
			parentID := application.WorldJurisdictionID
			if j.parent != "" {
				id, ok := ids[j.parent]
				if !ok {
					next = append(next, j)
					continue
				}
				parentID = id
			}
			newID, err := newUUID()
			if err != nil {
				return nil, fmt.Errorf("postgres: content apply: jurisdiction %q: %w", j.code, err)
			}
			var stored string
			stmt := upsertJurisdiction
			if j.cityID != "" {
				stmt = upsertCityJurisdiction
			}
			if err := tx.QueryRow(ctx, stmt,
				newID, j.kind, j.code, j.name, parentID, versionID).Scan(&stored); err != nil {
				return nil, fmt.Errorf("postgres: content apply: jurisdiction %s %q: %w", j.kind, j.code, err)
			}
			ids[j.code] = stored
			byLevel[j.kind] = append(byLevel[j.kind], stored)

			if j.cityID != "" {
				if _, err := tx.Exec(ctx,
					`UPDATE cities SET jurisdiction_id = $2::uuid WHERE id = $1::uuid`, j.cityID, stored); err != nil {
					return nil, fmt.Errorf("postgres: content apply: linking city %q to its jurisdiction: %w", j.code, err)
				}
			}
		}
		if len(next) == len(pending) {
			// Unreachable for a validated pack.
			return nil, fmt.Errorf("postgres: content apply: jurisdiction %q names a parent %q that was never written",
				next[0].code, next[0].parent)
		}
		pending = next
	}
	return byLevel, nil
}

// insertOfficeDefinitions writes this version's offices.
func insertOfficeDefinitions(ctx context.Context, tx pgx.Tx, p *content.Pack, versionID string) error {
	for _, o := range p.Offices {
		id, err := newUUID()
		if err != nil {
			return fmt.Errorf("postgres: content apply: office %q: %w", o.Code, err)
		}
		var term *int64
		if d, err := o.TermDuration(); err == nil && d > 0 {
			s := int64(d / time.Second)
			term = &s
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO office_definitions (id, content_version_id, code, jurisdiction_kind, seats, acquired_by,
			        deputy, appointed_by, requires_confirmation_by, term_seconds, term_limit,
			        can_be_removed_by, veto_over, incompatible_with)
			 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''), $10, $11,
			         $12, $13, $14)`,
			id, versionID, o.Code, o.Jurisdiction, o.Seats, o.AcquiredBy,
			o.Deputy, o.AppointedBy, o.RequiresConfirmationBy, term, o.TermLimit,
			nonNil(o.CanBeRemovedBy), nonNil(o.VetoOver), nonNil(o.IncompatibleWith)); err != nil {
			return fmt.Errorf("postgres: content apply: office %q: %w", o.Code, err)
		}
	}
	return nil
}

// insertLeverDefinitions writes this version's levers. It runs after the
// offices, which held_by references.
func insertLeverDefinitions(ctx context.Context, tx pgx.Tx, p *content.Pack, versionID string) error {
	for _, l := range p.Levers {
		id, err := newUUID()
		if err != nil {
			return fmt.Errorf("postgres: content apply: lever %q: %w", l.Code, err)
		}
		cooldown, err := l.CooldownDuration()
		if err != nil {
			return fmt.Errorf("postgres: content apply: lever %q cooldown: %w", l.Code, err)
		}
		notice, err := l.NoticeDuration()
		if err != nil {
			return fmt.Errorf("postgres: content apply: lever %q notice: %w", l.Code, err)
		}
		// A scalar lever stores its default and bounds as integers, a
		// structured one its default as a document; the other side is NULL.
		var (
			def, lo, hi *int64
			defJSON     *string
		)
		if l.ValueKind() == content.ValueKindScalar {
			d, mn, mx := l.DefaultValue(), l.MinValue(), l.MaxValue()
			def, lo, hi = &d, &mn, &mx
		} else {
			raw, err := l.DefaultJSON()
			if err != nil {
				return fmt.Errorf("postgres: content apply: lever %q default: %w", l.Code, err)
			}
			doc := string(raw)
			defJSON = &doc
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO lever_definitions (id, content_version_id, code, jurisdiction_kind, value_type, value_kind,
			        default_value, min_value, max_value, default_json, options, map_key, categories,
			        held_by, decision_rule, threshold, quorum, veto_by, override_rule, override_threshold,
			        change_cooldown_seconds, notice_seconds, city_default)
			 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11, NULLIF($12, ''), $13,
			         $14, $15, NULLIF($16, ''), NULLIF($17, ''), $18, NULLIF($19, ''), NULLIF($20, ''),
			         $21, $22, NULLIF($23, ''))`,
			id, versionID, l.Code, l.Jurisdiction, l.Type, l.ValueKind(),
			def, lo, hi, defJSON, nonNil(l.Options), l.Key, nonNil(l.Categories),
			l.HeldBy, l.Rule(), l.Threshold, l.Quorum, nonNil(l.VetoBy), l.OverrideRule, l.OverrideThreshold,
			int64(cooldown/time.Second), int64(notice/time.Second), l.CityDefault); err != nil {
			return fmt.Errorf("postgres: content apply: lever %q: %w", l.Code, err)
		}
	}
	return nil
}

// insertSeat creates the listed seats vacant, skipping any that exist. A
// reload therefore never duplicates a seat and never unseats a holder.
//
// A seat beyond an office's current count (seats lowered from 5 to 3) is left
// in place, holder and all: removing a seat somebody holds is a political act,
// not a side effect of editing a number, and deserves its own tool.
const insertSeat = `
INSERT INTO offices (id, office_code, jurisdiction_id, seat, holder_player_id, term_ends_at, acquired_by, since)
SELECT v.id, v.office_code, v.jurisdiction_id, v.seat, NULL, NULL, NULL, $5
  FROM unnest($1::uuid[], $2::text[], $3::uuid[], $4::int[]) AS v(id, office_code, jurisdiction_id, seat)
ON CONFLICT (office_code, jurisdiction_id, seat) DO NOTHING`

// ensureSeats creates one vacant seat per office, jurisdiction of its level
// and seat number, and returns how many were new.
func ensureSeats(ctx context.Context, tx pgx.Tx, p *content.Pack, byLevel map[string][]string, now time.Time) (int64, error) {
	var ids, codes, jurisdictions []string
	var seats []int32
	for _, o := range p.Offices {
		for _, j := range byLevel[o.Jurisdiction] {
			for seat := 1; seat <= o.Seats; seat++ {
				id, err := newUUID()
				if err != nil {
					return 0, fmt.Errorf("postgres: content apply: seat: %w", err)
				}
				ids = append(ids, id)
				codes = append(codes, o.Code)
				jurisdictions = append(jurisdictions, j)
				seats = append(seats, int32(seat))
			}
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := tx.Exec(ctx, insertSeat, ids, codes, jurisdictions, seats, now.UTC())
	if err != nil {
		return 0, fmt.Errorf("postgres: content apply: creating seats: %w", err)
	}
	return tag.RowsAffected(), nil
}

// nonNil turns a nil list into an empty one, for a NOT NULL array column.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// loadGovernance reads the governance of one version back into a pack, in the
// same read-only transaction LoadActive reads the rest in. Everything is
// ordered by code, so reading a version twice yields the same pack.
func loadGovernance(ctx context.Context, tx pgx.Tx, versionID string, pack *content.Pack) error {
	levelRows, err := tx.Query(ctx,
		`SELECT code, parents, overlay FROM jurisdiction_levels
		  WHERE content_version_id = $1::uuid ORDER BY code`, versionID)
	if err != nil {
		return fmt.Errorf("postgres: content load: levels: %w", err)
	}
	for levelRows.Next() {
		var l content.LevelDef
		if err := levelRows.Scan(&l.Code, &l.Parents, &l.Overlay); err != nil {
			levelRows.Close()
			return fmt.Errorf("postgres: content load: scanning level: %w", err)
		}
		pack.Levels = append(pack.Levels, l)
	}
	levelRows.Close()
	if err := levelRows.Err(); err != nil {
		return fmt.Errorf("postgres: content load: reading levels: %w", err)
	}

	// Declared jurisdictions: everything of this version that is neither the
	// world nor a city. A parent that is the world reads back as "".
	jRows, err := tx.Query(ctx,
		`SELECT j.code, j.name, j.kind, CASE WHEN p.kind = 'world' THEN '' ELSE p.code END
		   FROM jurisdictions j
		   JOIN jurisdictions p ON p.id = j.parent_id
		  WHERE j.content_version_id = $1::uuid AND j.kind <> 'city'
		  ORDER BY j.code`, versionID)
	if err != nil {
		return fmt.Errorf("postgres: content load: jurisdictions: %w", err)
	}
	for jRows.Next() {
		var j content.JurisdictionDef
		if err := jRows.Scan(&j.Code, &j.Name, &j.Level, &j.Parent); err != nil {
			jRows.Close()
			return fmt.Errorf("postgres: content load: scanning jurisdiction: %w", err)
		}
		pack.Jurisdictions = append(pack.Jurisdictions, j)
	}
	jRows.Close()
	if err := jRows.Err(); err != nil {
		return fmt.Errorf("postgres: content load: reading jurisdictions: %w", err)
	}

	oRows, err := tx.Query(ctx,
		`SELECT code, jurisdiction_kind, seats, acquired_by, COALESCE(deputy, ''), COALESCE(appointed_by, ''),
		        COALESCE(requires_confirmation_by, ''), term_seconds, term_limit,
		        can_be_removed_by, veto_over, incompatible_with
		   FROM office_definitions WHERE content_version_id = $1::uuid ORDER BY code`, versionID)
	if err != nil {
		return fmt.Errorf("postgres: content load: offices: %w", err)
	}
	for oRows.Next() {
		var (
			o    content.OfficeDef
			term *int64
		)
		if err := oRows.Scan(&o.Code, &o.Jurisdiction, &o.Seats, &o.AcquiredBy, &o.Deputy, &o.AppointedBy,
			&o.RequiresConfirmationBy, &term, &o.TermLimit,
			&o.CanBeRemovedBy, &o.VetoOver, &o.IncompatibleWith); err != nil {
			oRows.Close()
			return fmt.Errorf("postgres: content load: scanning office: %w", err)
		}
		if term != nil {
			o.Term = content.FormatLeverDuration(*term)
		}
		pack.Offices = append(pack.Offices, o)
	}
	oRows.Close()
	if err := oRows.Err(); err != nil {
		return fmt.Errorf("postgres: content load: reading offices: %w", err)
	}

	lRows, err := tx.Query(ctx,
		`SELECT code, jurisdiction_kind, value_type, default_value, min_value, max_value, default_json,
		        options, COALESCE(map_key, ''), categories, held_by,
		        decision_rule, COALESCE(threshold, ''), COALESCE(quorum, ''), veto_by,
		        COALESCE(override_rule, ''), COALESCE(override_threshold, ''),
		        change_cooldown_seconds, notice_seconds, COALESCE(city_default, '')
		   FROM lever_definitions WHERE content_version_id = $1::uuid ORDER BY code`, versionID)
	if err != nil {
		return fmt.Errorf("postgres: content load: levers: %w", err)
	}
	for lRows.Next() {
		var (
			l                    content.LeverDef
			def                  *int64
			defJSON              []byte
			cooldown, noticeSecs int64
		)
		if err := lRows.Scan(&l.Code, &l.Jurisdiction, &l.Type, &def, &l.Min, &l.Max, &defJSON,
			&l.Options, &l.Key, &l.Categories, &l.HeldBy,
			&l.DecisionRule, &l.Threshold, &l.Quorum, &l.VetoBy, &l.OverrideRule, &l.OverrideThreshold,
			&cooldown, &noticeSecs, &l.CityDefault); err != nil {
			lRows.Close()
			return fmt.Errorf("postgres: content load: scanning lever: %w", err)
		}
		if def != nil {
			l.Default = *def
		} else if defJSON != nil {
			if err := json.Unmarshal(defJSON, &l.Default); err != nil {
				lRows.Close()
				return fmt.Errorf("postgres: content load: lever %q default: %w", l.Code, err)
			}
		}
		l.ChangeCooldown = content.FormatLeverDuration(cooldown)
		l.Notice = content.FormatLeverDuration(noticeSecs)
		pack.Levers = append(pack.Levers, l)
	}
	lRows.Close()
	if err := lRows.Err(); err != nil {
		return fmt.Errorf("postgres: content load: reading levers: %w", err)
	}

	// Each office's levers are the levers naming it: stored once, in
	// held_by, and derived here so the two can never disagree.
	for i := range pack.Offices {
		pack.Offices[i].Levers = []string{}
		for _, l := range pack.Levers {
			if l.HeldBy == pack.Offices[i].Code {
				pack.Offices[i].Levers = append(pack.Offices[i].Levers, l.Code)
			}
		}
	}
	return nil
}
