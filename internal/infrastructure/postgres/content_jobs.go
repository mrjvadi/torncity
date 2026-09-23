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

// This file writes and reads the work-and-study content of a version: the
// careers of jobs.yml and the courses of education.yml
// (migrations/0011_jobs_and_education.up.sql). Like routes and skills they are
// written fresh for every version, so re-activating an old version needs no
// file, and each definition is stored whole as a document of the content type
// it was parsed into.

// insertJobs writes this version's careers and courses, after refusing a
// load that would pull one out from under a player (refuseRemovalOfJobsInUse).
func insertJobs(ctx context.Context, tx pgx.Tx, p *content.Pack, versionID string) error {
	if err := refuseRemovalOfJobsInUse(ctx, tx, p); err != nil {
		return err
	}
	for _, c := range p.Careers {
		doc, err := json.Marshal(c)
		if err != nil {
			return fmt.Errorf("postgres: content apply: career %q: %w", c.Code, err)
		}
		id, err := newUUID()
		if err != nil {
			return fmt.Errorf("postgres: content apply: career %q: %w", c.Code, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO career_definitions (id, code, category, definition, content_version_id)
			 VALUES ($1::uuid, $2, $3, $4::jsonb, $5::uuid)`,
			id, c.Code, c.Category, string(doc), versionID); err != nil {
			return fmt.Errorf("postgres: content apply: career %q: %w", c.Code, err)
		}
	}
	for _, c := range p.Courses {
		doc, err := json.Marshal(c)
		if err != nil {
			return fmt.Errorf("postgres: content apply: course %q: %w", c.Code, err)
		}
		id, err := newUUID()
		if err != nil {
			return fmt.Errorf("postgres: content apply: course %q: %w", c.Code, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO course_definitions (id, code, definition, content_version_id)
			 VALUES ($1::uuid, $2, $3::jsonb, $4::uuid)`,
			id, c.Code, string(doc), versionID); err != nil {
			return fmt.Errorf("postgres: content apply: course %q: %w", c.Code, err)
		}
	}
	return nil
}

// loadJobs reads one version's careers and courses into pack, ordered by code
// so a pack read back twice is the same pack. It runs in LoadActive's
// read-only snapshot, with everything else.
func loadJobs(ctx context.Context, tx pgx.Tx, versionID string, pack *content.Pack) error {
	rows, err := tx.Query(ctx,
		`SELECT definition FROM career_definitions WHERE content_version_id = $1::uuid ORDER BY code`, versionID)
	if err != nil {
		return fmt.Errorf("postgres: content load: careers: %w", err)
	}
	for rows.Next() {
		var (
			raw []byte
			c   content.CareerDef
		)
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: content load: scanning career: %w", err)
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: content load: decoding career: %w", err)
		}
		pack.Careers = append(pack.Careers, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: content load: reading careers: %w", err)
	}

	rows, err = tx.Query(ctx,
		`SELECT definition FROM course_definitions WHERE content_version_id = $1::uuid ORDER BY code`, versionID)
	if err != nil {
		return fmt.Errorf("postgres: content load: courses: %w", err)
	}
	for rows.Next() {
		var (
			raw []byte
			c   content.CourseDef
		)
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: content load: scanning course: %w", err)
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: content load: decoding course: %w", err)
		}
		pack.Courses = append(pack.Courses, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: content load: reading courses: %w", err)
	}
	return nil
}

// checksumJobs adds the careers and courses to a value checksum (see
// Checksum). The documents are the stored form, so a pack from files and the
// same pack read back fingerprint alike.
func checksumJobs(h hash.Hash, p *content.Pack) {
	for _, c := range p.Careers {
		doc, _ := json.Marshal(c)
		fmt.Fprintf(h, "career|%s\n", doc)
	}
	for _, c := range p.Courses {
		doc, _ := json.Marshal(c)
		fmt.Fprintf(h, "course|%s\n", doc)
	}
}

// ErrJobContentInUse reports that a load would remove a career someone works
// in, cut a career below the position someone holds, or remove a course
// someone is studying. ADR 0004 rule 7, like ErrCityInUse: a stored job or
// enrolment naming content that no longer exists is a broken save.
var ErrJobContentInUse = errors.New("postgres: content: a career or course in use would be removed")

// refuseRemovalOfJobsInUse implements ADR 0004 rule 7 for work and study,
// inside the applying transaction.
//
// The rows are locked FOR SHARE, so a job or an enrolment changing while the
// load decides is serialised against it: a hire or a promotion waits until
// the load commits, and then runs against the content it installed.
func refuseRemovalOfJobsInUse(ctx context.Context, tx pgx.Tx, p *content.Pack) error {
	tiers := make(map[string]int, len(p.Careers))
	for _, c := range p.Careers {
		tiers[c.Code] = len(c.Tiers)
	}
	courses := make(map[string]bool, len(p.Courses))
	for _, c := range p.Courses {
		courses[c.Code] = true
	}

	var blocked []string
	rows, err := tx.Query(ctx,
		`SELECT career_code, tier FROM employments WHERE ended_at IS NULL ORDER BY career_code, tier FOR SHARE`)
	if err != nil {
		return fmt.Errorf("postgres: content apply: checking jobs in use: %w", err)
	}
	worst := map[string]int{}
	for rows.Next() {
		var (
			code string
			tier int
		)
		if err := rows.Scan(&code, &tier); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: content apply: scanning job in use: %w", err)
		}
		if tier+1 > worst[code] {
			worst[code] = tier + 1
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: content apply: checking jobs in use: %w", err)
	}
	for code, need := range worst {
		if have, ok := tiers[code]; !ok || have < need {
			blocked = append(blocked, fmt.Sprintf("career %s (a player holds position %d; the new content has %d)", code, need, have))
		}
	}

	rows, err = tx.Query(ctx,
		`SELECT DISTINCT course_code FROM enrollments WHERE status = 'in_progress' ORDER BY course_code`)
	if err != nil {
		return fmt.Errorf("postgres: content apply: checking courses in use: %w", err)
	}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: content apply: scanning course in use: %w", err)
		}
		if !courses[code] {
			blocked = append(blocked, fmt.Sprintf("course %s (players are enrolled)", code))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: content apply: checking courses in use: %w", err)
	}

	if len(blocked) > 0 {
		sort.Strings(blocked)
		return fmt.Errorf("%w:%s", ErrJobContentInUse, joinLines(blocked))
	}
	return nil
}
