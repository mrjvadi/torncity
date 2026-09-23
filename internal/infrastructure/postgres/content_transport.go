package postgres

import (
	"context"
	"fmt"
	"hash"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/content"
)

// This file stores and reads transport content (configs/content/transport.yml
// and the transport keys of cities.yml and routes.yml) as part of a content
// version. It runs inside ContentStore.Apply and ContentStore.LoadActive, on
// their transaction, so transport lands and is read with the rest of one
// version or not at all. Migration: 0009_transport_modes.

// applyTransport writes a pack's transport content under versionID. It runs
// after upsertCities and insertRoutes, whose rows it completes.
func applyTransport(ctx context.Context, tx pgx.Tx, p *content.Pack, versionID string, cityIDs map[string]string) error {
	for _, f := range p.Facilities {
		if _, err := tx.Exec(ctx,
			`INSERT INTO transport_facilities (content_version_id, code) VALUES ($1::uuid, $2)`,
			versionID, f); err != nil {
			return fmt.Errorf("postgres: content apply: facility %q: %w", f, err)
		}
	}

	for i, m := range p.TransportModes {
		mode, err := m.Mode()
		if err != nil {
			return fmt.Errorf("postgres: content apply: mode %q: %w", m.Code, err)
		}
		id, err := newUUID()
		if err != nil {
			return fmt.Errorf("postgres: content apply: mode %q: %w", m.Code, err)
		}
		requires := append([]string{}, m.Requires...)
		if _, err := tx.Exec(ctx,
			`INSERT INTO transport_modes
			        (id, content_version_id, position, code, name, public, requires, speed,
			         boarding_seconds, base_fare, fare_per_distance, energy_cost,
			         demand_window_seconds, demand_free_departures, demand_step_bps, demand_max_bps)
			 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
			id, versionID, i, m.Code, m.Name, m.Public, requires, m.Speed,
			int64(mode.Boarding/time.Second), m.BaseFare, m.FarePerDistance, m.EnergyCost,
			int64(mode.Demand.Window/time.Second), m.Demand.FreeDepartures, m.Demand.StepBPS, m.Demand.MaxBPS); err != nil {
			return fmt.Errorf("postgres: content apply: mode %q: %w", m.Code, err)
		}
	}

	// A city row is written in place by upsertCities; its facilities are
	// written here, every city, so a facility an author removed is removed.
	for _, c := range p.Cities {
		facilities := append([]string{}, c.Facilities...)
		if _, err := tx.Exec(ctx,
			`UPDATE cities SET facilities = $2 WHERE id = $1::uuid`,
			cityIDs[c.Code], facilities); err != nil {
			return fmt.Errorf("postgres: content apply: facilities of %q: %w", c.Code, err)
		}
	}

	// Only routes that list their modes are touched; the rest keep NULL,
	// which reads back as "derived".
	for _, r := range p.Routes {
		if r.Modes == nil {
			continue
		}
		tag, err := tx.Exec(ctx,
			`UPDATE city_routes SET modes = $4
			  WHERE content_version_id = $1::uuid AND from_city_id = $2::uuid AND to_city_id = $3::uuid`,
			versionID, cityIDs[r.From], cityIDs[r.To], append([]string{}, r.Modes...))
		if err != nil {
			return fmt.Errorf("postgres: content apply: modes of route %s->%s: %w", r.From, r.To, err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("postgres: content apply: route %s->%s was not written", r.From, r.To)
		}
	}
	return nil
}

// loadTransport reads the transport content of versionID into pack, whose
// cities and routes are already read.
func loadTransport(ctx context.Context, tx pgx.Tx, versionID string, pack *content.Pack) error {
	rows, err := tx.Query(ctx,
		`SELECT code FROM transport_facilities WHERE content_version_id = $1::uuid ORDER BY code`, versionID)
	if err != nil {
		return fmt.Errorf("postgres: content load: facilities: %w", err)
	}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: content load: scanning facility: %w", err)
		}
		pack.Facilities = append(pack.Facilities, code)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: content load: facilities: %w", err)
	}

	rows, err = tx.Query(ctx,
		`SELECT code, name, public, requires, speed, boarding_seconds, base_fare, fare_per_distance,
		        energy_cost, demand_window_seconds, demand_free_departures, demand_step_bps, demand_max_bps
		   FROM transport_modes
		  WHERE content_version_id = $1::uuid
		  ORDER BY position`, versionID)
	if err != nil {
		return fmt.Errorf("postgres: content load: transport modes: %w", err)
	}
	for rows.Next() {
		var (
			m                      content.TransportModeDef
			boarding, windowSecond int64
		)
		if err := rows.Scan(&m.Code, &m.Name, &m.Public, &m.Requires, &m.Speed, &boarding,
			&m.BaseFare, &m.FarePerDistance, &m.EnergyCost, &windowSecond,
			&m.Demand.FreeDepartures, &m.Demand.StepBPS, &m.Demand.MaxBPS); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: content load: scanning transport mode: %w", err)
		}
		m.Boarding = content.FormatTransportDuration(boarding)
		m.Demand.Window = content.FormatTransportDuration(windowSecond)
		pack.TransportModes = append(pack.TransportModes, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: content load: transport modes: %w", err)
	}

	facilities := map[string][]string{}
	rows, err = tx.Query(ctx,
		`SELECT code, facilities FROM cities WHERE content_version_id = $1::uuid`, versionID)
	if err != nil {
		return fmt.Errorf("postgres: content load: city facilities: %w", err)
	}
	for rows.Next() {
		var (
			code string
			fs   []string
		)
		if err := rows.Scan(&code, &fs); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: content load: scanning city facilities: %w", err)
		}
		if len(fs) > 0 {
			facilities[code] = fs
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: content load: city facilities: %w", err)
	}
	for i := range pack.Cities {
		pack.Cities[i].Facilities = facilities[pack.Cities[i].Code]
	}

	type pair struct{ from, to string }
	declared := map[pair][]string{}
	rows, err = tx.Query(ctx,
		`SELECT f.code, t.code, r.modes
		   FROM city_routes r
		   JOIN cities f ON f.id = r.from_city_id
		   JOIN cities t ON t.id = r.to_city_id
		  WHERE r.content_version_id = $1::uuid AND r.modes IS NOT NULL`, versionID)
	if err != nil {
		return fmt.Errorf("postgres: content load: route modes: %w", err)
	}
	for rows.Next() {
		var (
			k     pair
			modes []string
		)
		if err := rows.Scan(&k.from, &k.to, &modes); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: content load: scanning route modes: %w", err)
		}
		declared[k] = append([]string{}, modes...)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: content load: route modes: %w", err)
	}
	for i := range pack.Routes {
		if modes, ok := declared[pair{pack.Routes[i].From, pack.Routes[i].To}]; ok {
			pack.Routes[i].Modes = modes
		}
	}
	return nil
}

// checksumTransport adds transport content to a value checksum; see Checksum.
func checksumTransport(h hash.Hash, p *content.Pack) {
	for _, f := range p.Facilities {
		fmt.Fprintf(h, "facility|%s\n", f)
	}
	for _, c := range p.Cities {
		if len(c.Facilities) > 0 {
			fmt.Fprintf(h, "city_facilities|%s|%v\n", c.Code, c.Facilities)
		}
	}
	for _, r := range p.Routes {
		if r.Modes != nil {
			fmt.Fprintf(h, "route_modes|%s|%s|%v\n", r.From, r.To, r.Modes)
		}
	}
	for _, m := range p.TransportModes {
		// Durations through the domain value, so "20m" from a file and
		// "20m0s" read back from the database fingerprint alike.
		mode, _ := m.Mode()
		fmt.Fprintf(h, "transport_mode|%s|%s|%t|%v|%d|%d|%d|%d|%d|%d|%d|%d|%d\n",
			m.Code, m.Name, m.Public, m.Requires, m.Speed, int64(mode.Boarding/time.Second),
			m.BaseFare, m.FarePerDistance, m.EnergyCost, int64(mode.Demand.Window/time.Second),
			m.Demand.FreeDepartures, m.Demand.StepBPS, m.Demand.MaxBPS)
	}
}
