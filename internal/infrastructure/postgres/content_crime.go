package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/content"
)

// This file writes and reads the crime content of a version — the tiers,
// venues, categories and crimes of crimes.yml — into crime_content
// (migrations/0013_crime.up.sql). Like careers they are written fresh for
// every version, each entry stored whole as the document it was parsed into,
// with its position so an ordered list reads back in its order.

// Kinds of crime_content row.
const (
	crimeKindTier     = "tier"
	crimeKindVenue    = "venue"
	crimeKindCategory = "category"
	crimeKindCrime    = "crime"
)

// insertCrimes writes this version's crime content, after refusing a load
// that would pull a crime out from under an attempt or a case.
func insertCrimes(ctx context.Context, tx pgx.Tx, p *content.Pack, versionID string) error {
	if err := refuseRemovalOfCrimesInUse(ctx, tx, p); err != nil {
		return err
	}
	write := func(kind, code string, position int, v any) error {
		doc, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("postgres: content apply: crime %s %q: %w", kind, code, err)
		}
		id, err := newUUID()
		if err != nil {
			return fmt.Errorf("postgres: content apply: crime %s %q: %w", kind, code, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO crime_content (id, content_version_id, kind, code, position, definition)
			 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::jsonb)`,
			id, versionID, kind, code, position, string(doc)); err != nil {
			return fmt.Errorf("postgres: content apply: crime %s %q: %w", kind, code, err)
		}
		return nil
	}
	for i, t := range p.CrimeTiers {
		if err := write(crimeKindTier, t.Code, i, t); err != nil {
			return err
		}
	}
	// Venues are the city's places, stored with the other documents
	// (content_documents, kind place) since places became their own
	// content; a version stored before that still has them here, and
	// loadCrimes still reads them.
	for i, c := range p.CrimeCategories {
		if err := write(crimeKindCategory, c.Code, i, c); err != nil {
			return err
		}
	}
	for i, c := range p.Crimes {
		if err := write(crimeKindCrime, c.Code, i, c); err != nil {
			return err
		}
	}
	return nil
}

// loadCrimes reads one version's crime content into pack, each list in its
// authored order. It runs in LoadActive's read-only snapshot.
func loadCrimes(ctx context.Context, tx pgx.Tx, versionID string, pack *content.Pack) error {
	rows, err := tx.Query(ctx,
		`SELECT kind, definition FROM crime_content
		  WHERE content_version_id = $1::uuid
		  ORDER BY kind, position, code`, versionID)
	if err != nil {
		return fmt.Errorf("postgres: content load: crimes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			kind string
			raw  []byte
		)
		if err := rows.Scan(&kind, &raw); err != nil {
			return fmt.Errorf("postgres: content load: scanning crime content: %w", err)
		}
		switch kind {
		case crimeKindTier:
			var t content.CrimeTierDef
			err = json.Unmarshal(raw, &t)
			pack.CrimeTiers = append(pack.CrimeTiers, t)
		case crimeKindVenue:
			var v content.VenueDef
			err = json.Unmarshal(raw, &v)
			pack.Venues = append(pack.Venues, v)
		case crimeKindCategory:
			var c content.CrimeCategoryDef
			err = json.Unmarshal(raw, &c)
			pack.CrimeCategories = append(pack.CrimeCategories, c)
		case crimeKindCrime:
			var c content.CrimeDef
			err = json.Unmarshal(raw, &c)
			pack.Crimes = append(pack.Crimes, c)
		default:
			err = fmt.Errorf("unknown kind %q", kind)
		}
		if err != nil {
			return fmt.Errorf("postgres: content load: decoding crime content: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: content load: reading crime content: %w", err)
	}
	return nil
}

// checksumCrimes adds the crime content to a value checksum, in the stored
// form, so a pack from files and the same pack read back fingerprint alike.
func checksumCrimes(h hash.Hash, p *content.Pack) {
	for _, t := range p.CrimeTiers {
		doc, _ := json.Marshal(t)
		fmt.Fprintf(h, "crime_tier|%s\n", doc)
	}
	for _, c := range p.CrimeCategories {
		doc, _ := json.Marshal(c)
		fmt.Fprintf(h, "crime_category|%s\n", doc)
	}
	for _, c := range p.Crimes {
		doc, _ := json.Marshal(c)
		fmt.Fprintf(h, "crime|%s\n", doc)
	}
}

// ErrCrimeContentInUse reports that a load would remove a crime an attempt
// in progress or an open investigation still needs. ADR 0004 rule 7: a timed
// attempt resolves, and a solved case is sentenced, from its crime's
// content.
var ErrCrimeContentInUse = errors.New("postgres: content: a crime in use would be removed")

// refuseRemovalOfCrimesInUse implements ADR 0004 rule 7 for crime, inside
// the applying transaction. An attempt started while the load decides uses
// the content it read before the load, and resolves against the snapshot in
// force when it ends; a crime a running attempt names can therefore only
// disappear through a load, which this refuses.
func refuseRemovalOfCrimesInUse(ctx context.Context, tx pgx.Tx, p *content.Pack) error {
	known := make(map[string]bool, len(p.Crimes))
	for _, c := range p.Crimes {
		known[c.Code] = true
	}
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT c.crime_code
		   FROM crimes c
		  WHERE c.status = 'in_progress'
		     OR EXISTS (SELECT 1 FROM crime_reports r WHERE r.crime_id = c.id AND r.status = 'investigating')
		  ORDER BY c.crime_code`)
	if err != nil {
		return fmt.Errorf("postgres: content apply: checking crimes in use: %w", err)
	}
	var blocked []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: content apply: scanning crime in use: %w", err)
		}
		if !known[code] {
			blocked = append(blocked, fmt.Sprintf("crime %s (an attempt or an investigation needs it)", code))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: content apply: checking crimes in use: %w", err)
	}
	if len(blocked) > 0 {
		sort.Strings(blocked)
		return fmt.Errorf("%w:%s", ErrCrimeContentInUse, joinLines(blocked))
	}
	return nil
}
