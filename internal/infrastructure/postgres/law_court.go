package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// ---------------------------------------------------------------------------
// verdicts
// ---------------------------------------------------------------------------

// InsertVerdict records the decision of an instance, once: the unique (case, instance) is the fence.
func (r *LawRepository) InsertVerdict(ctx context.Context, v application.LawVerdict) (bool, error) {
	return r.one(ctx, "recording a verdict", `INSERT INTO law_verdicts (id, case_id, instance, judge_id, npc, guilty, total, quality, defence, effective, at)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5, $6, $7, $8, $9, $10, $11) ON CONFLICT DO NOTHING`,
		v.ID, v.CaseID, v.Instance, nullIfEmpty(v.JudgeID), v.NPC, v.Guilty, v.Total, v.Quality, v.Defence, v.Effective, utcT(v.At))
}

// VerdictOf reads the decision of an instance, nil for none.
func (r *LawRepository) VerdictOf(ctx context.Context, caseID string, instance int) (*application.LawVerdict, error) {
	var v application.LawVerdict
	err := r.q.QueryRow(ctx, `SELECT id::text, case_id::text, instance, COALESCE(judge_id::text, ''), npc, guilty, total, quality, defence, effective, at
		FROM law_verdicts WHERE case_id = $1::uuid AND instance = $2`, caseID, instance).
		Scan(&v.ID, &v.CaseID, &v.Instance, &v.JudgeID, &v.NPC, &v.Guilty, &v.Total, &v.Quality, &v.Defence, &v.Effective, &v.At)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a verdict: %w", err)
	}
	v.At = v.At.UTC()
	return &v, nil
}

// ---------------------------------------------------------------------------
// sentences
// ---------------------------------------------------------------------------

const lawSentenceColumns = `id::text, case_id::text, verdict_id::text, player_id::text, kind, fine, jail_hours, ban_days, ban, COALESCE(jail_building::text, ''),
	COALESCE(cell, 0), status, starts_at, ends_at, bail_paid, fine_paid`

func scanLawSentence(row pgx.Row) (*application.LawSentence, error) {
	var s application.LawSentence
	if err := row.Scan(&s.ID, &s.CaseID, &s.VerdictID, &s.PlayerID, &s.Kind, &s.Fine, &s.JailHours, &s.BanDays, &s.Ban, &s.JailBuilding, &s.Cell, &s.Status,
		&s.StartsAt, &s.EndsAt, &s.BailPaid, &s.FinePaid); err != nil {
		return nil, err
	}
	s.StartsAt, s.EndsAt = s.StartsAt.UTC(), utcPtr(s.EndsAt)
	return &s, nil
}

func (r *LawRepository) sentences(ctx context.Context, sql string, args ...any) ([]application.LawSentence, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing sentences: %w", err)
	}
	defer rows.Close()
	var out []application.LawSentence
	for rows.Next() {
		s, err := scanLawSentence(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a sentence: %w", err)
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// InsertSentence records a sentence handed down.
func (r *LawRepository) InsertSentence(ctx context.Context, s application.LawSentence) error {
	var cell any
	var jb any
	if s.Kind == "jail" {
		cell, jb = s.Cell, s.JailBuilding
	}
	return r.exec(ctx, "recording a sentence", `INSERT INTO law_sentences (id, case_id, verdict_id, player_id, kind, fine, jail_hours, ban_days, ban, jail_building, cell, status, starts_at, ends_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10::uuid, $11, 'active', $12, $13)`,
		s.ID, s.CaseID, s.VerdictID, s.PlayerID, s.Kind, s.Fine, s.JailHours, s.BanDays, s.Ban, jb, cell, utcT(s.StartsAt), tp(s.EndsAt))
}

// SentencesOf lists the sentences of a case.
func (r *LawRepository) SentencesOf(ctx context.Context, caseID string) ([]application.LawSentence, error) {
	return r.sentences(ctx, `SELECT `+lawSentenceColumns+` FROM law_sentences WHERE case_id = $1::uuid ORDER BY starts_at, id`, caseID)
}

// SentenceByID reads a sentence, nil for none.
func (r *LawRepository) SentenceByID(ctx context.Context, id string, lock bool) (*application.LawSentence, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + lawSentenceColumns + ` FROM law_sentences WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	s, err := scanLawSentence(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a sentence: %w", err)
	}
	return s, nil
}

// SetSentence moves an active sentence to a new status and records what was paid; false when it was not active.
func (r *LawRepository) SetSentence(ctx context.Context, id, status string, finePaid, bailPaid int64) (bool, error) {
	return r.one(ctx, "closing a sentence", `UPDATE law_sentences SET status = $2, fine_paid = GREATEST(fine_paid, $3), bail_paid = GREATEST(bail_paid, $4) WHERE id = $1::uuid AND status = 'active'`,
		id, status, finePaid, bailPaid)
}

// ForceSentence sets a sentence's status whatever it was, once.
func (r *LawRepository) ForceSentence(ctx context.Context, id, status string) (bool, error) {
	return r.one(ctx, "changing a sentence", `UPDATE law_sentences SET status = $2 WHERE id = $1::uuid AND status <> $2`, id, status)
}

// ActiveJail is the jail sentence a person is serving, nil for none.
func (r *LawRepository) ActiveJail(ctx context.Context, playerID string) (*application.LawSentence, error) {
	s, err := scanLawSentence(r.q.QueryRow(ctx, `SELECT `+lawSentenceColumns+` FROM law_sentences WHERE player_id = $1::uuid AND kind = 'jail' AND status = 'active'`, playerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a jail sentence: %w", err)
	}
	return s, nil
}

// JailsHeld is how many are serving in a jail.
func (r *LawRepository) JailsHeld(ctx context.Context, buildingID string) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM law_sentences WHERE jail_building = $1::uuid AND kind = 'jail' AND status = 'active'`, buildingID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting prisoners: %w", err)
	}
	return n, nil
}

// FreeCell is the lowest free cell of a jail with cells cells, false when it is full.
func (r *LawRepository) FreeCell(ctx context.Context, buildingID string, cells int64) (int, bool, error) {
	var cell int
	err := r.q.QueryRow(ctx, `SELECT g FROM generate_series(1, $2::int) g WHERE NOT EXISTS (SELECT 1 FROM law_sentences WHERE jail_building = $1::uuid
		AND kind = 'jail' AND status = 'active' AND cell = g) ORDER BY g LIMIT 1`, buildingID, cells).Scan(&cell)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("postgres: finding a free cell: %w", err)
	}
	return cell, true, nil
}

// DueJails are the jail sentences whose term has run out, oldest first.
func (r *LawRepository) DueJails(ctx context.Context, now time.Time, limit int) ([]application.LawSentence, error) {
	return r.sentences(ctx, `SELECT `+lawSentenceColumns+` FROM law_sentences WHERE kind = 'jail' AND status = 'active' AND ends_at <= $1 ORDER BY ends_at, id LIMIT $2`, utcT(now), limit)
}

// Banned says a person is under an active ban from a scope.
func (r *LawRepository) Banned(ctx context.Context, playerID, ban string, now time.Time) (bool, error) {
	var ok bool
	if err := r.q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM law_sentences WHERE player_id = $1::uuid AND kind = 'ban' AND ban = $2 AND status = 'active'
		AND (ends_at IS NULL OR ends_at > $3))`, playerID, ban, utcT(now)).Scan(&ok); err != nil {
		return false, fmt.Errorf("postgres: reading a ban: %w", err)
	}
	return ok, nil
}

// InsertAppeal records an appeal; false when the party has appealed already (the primary key is the fence).
func (r *LawRepository) InsertAppeal(ctx context.Context, a application.LawAppeal) (bool, error) {
	return r.one(ctx, "appealing", `INSERT INTO law_appeals (case_id, party, by_id, fee, status, at) VALUES ($1::uuid, $2, $3::uuid, $4, 'open', $5) ON CONFLICT DO NOTHING`,
		a.CaseID, a.Party, a.ByID, a.Fee, utcT(a.At))
}

// AppealsOf lists a case's appeals.
func (r *LawRepository) AppealsOf(ctx context.Context, caseID string) ([]application.LawAppeal, error) {
	rows, err := r.q.Query(ctx, `SELECT case_id::text, party, by_id::text, fee, status, at FROM law_appeals WHERE case_id = $1::uuid ORDER BY at`, caseID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing appeals: %w", err)
	}
	defer rows.Close()
	var out []application.LawAppeal
	for rows.Next() {
		var a application.LawAppeal
		if err := rows.Scan(&a.CaseID, &a.Party, &a.ByID, &a.Fee, &a.Status, &a.At); err != nil {
			return nil, fmt.Errorf("postgres: reading an appeal: %w", err)
		}
		a.At = a.At.UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetAppeal marks an open appeal heard.
func (r *LawRepository) SetAppeal(ctx context.Context, caseID, party, status string) (bool, error) {
	return r.one(ctx, "closing an appeal", `UPDATE law_appeals SET status = $3 WHERE case_id = $1::uuid AND party = $2 AND status = 'open'`, caseID, party, status)
}

// Convictions counts the guilty verdicts of the first instance against any of the players in a settlement since an instant, the appeals that quashed
// them taken off.
func (r *LawRepository) Convictions(ctx context.Context, settlementID string, players []string, since time.Time) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM law_cases c JOIN law_verdicts v ON v.case_id = c.id AND v.instance = 1 AND v.guilty
		WHERE c.settlement_id = $1::uuid AND c.accused_id = ANY($2::uuid[]) AND v.at >= $3
		AND NOT EXISTS (SELECT 1 FROM law_verdicts a WHERE a.case_id = c.id AND a.instance = 2 AND NOT a.guilty)`, settlementID, players, utcT(since)).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting convictions: %w", err)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// informants
// ---------------------------------------------------------------------------

const lawInformantColumns = `id::text, settlement_id::text, handler_id::text, COALESCE(source_id::text, ''), reliability, pay, state, handler_since, recruited_at, ended_at`

func scanLawInformant(row pgx.Row) (*application.LawInformant, error) {
	var i application.LawInformant
	if err := row.Scan(&i.ID, &i.SettlementID, &i.HandlerID, &i.SourceID, &i.Reliability, &i.Pay, &i.State, &i.HandlerSince, &i.RecruitedAt, &i.EndedAt); err != nil {
		return nil, err
	}
	i.HandlerSince, i.RecruitedAt, i.EndedAt = i.HandlerSince.UTC(), i.RecruitedAt.UTC(), utcPtr(i.EndedAt)
	return &i, nil
}

// InsertInformant recruits a source; false when the source is already one in the settlement (the partial unique index is the fence).
func (r *LawRepository) InsertInformant(ctx context.Context, i application.LawInformant) (bool, error) {
	return r.one(ctx, "recruiting an informant", `INSERT INTO law_informants (id, settlement_id, handler_id, source_id, reliability, pay, state, handler_since, recruited_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, 'active', $7, $7) ON CONFLICT DO NOTHING`,
		i.ID, i.SettlementID, i.HandlerID, nullIfEmpty(i.SourceID), i.Reliability, i.Pay, utcT(i.RecruitedAt))
}

// InformantByID reads an informant, nil for none.
func (r *LawRepository) InformantByID(ctx context.Context, id string, lock bool) (*application.LawInformant, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + lawInformantColumns + ` FROM law_informants WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	i, err := scanLawInformant(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading an informant: %w", err)
	}
	return i, nil
}

// InformantsOf lists the informants of a settlement; handlerID narrows to one handler's.
func (r *LawRepository) InformantsOf(ctx context.Context, settlementID, handlerID string) ([]application.LawInformant, error) {
	rows, err := r.q.Query(ctx, `SELECT `+lawInformantColumns+` FROM law_informants WHERE settlement_id = $1::uuid AND ($2 = '' OR handler_id = NULLIF($2, '')::uuid)
		ORDER BY recruited_at DESC, id`, settlementID, handlerID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing informants: %w", err)
	}
	defer rows.Close()
	var out []application.LawInformant
	for rows.Next() {
		i, err := scanLawInformant(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading an informant: %w", err)
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}

// LiveInformants is how many live informants a handler runs.
func (r *LawRepository) LiveInformants(ctx context.Context, handlerID string) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM law_informants WHERE handler_id = $1::uuid AND state IN ('recruited', 'active')`, handlerID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting informants: %w", err)
	}
	return n, nil
}

// SetReliability writes a source's reliability.
func (r *LawRepository) SetReliability(ctx context.Context, id string, reliability int) error {
	return r.exec(ctx, "setting reliability", `UPDATE law_informants SET reliability = $2 WHERE id = $1::uuid`, id, reliability)
}

// SetInformantState ends an informant (burned or retired), once.
func (r *LawRepository) SetInformantState(ctx context.Context, id, state string, at time.Time) (bool, error) {
	return r.one(ctx, "ending an informant", `UPDATE law_informants SET state = $2, ended_at = $3 WHERE id = $1::uuid AND state IN ('recruited', 'active')`, id, state, utcT(at))
}

// PassInformant hands a live informant to another handler.
func (r *LawRepository) PassInformant(ctx context.Context, id, handlerID string, at time.Time) (bool, error) {
	return r.one(ctx, "passing an informant", `UPDATE law_informants SET handler_id = $2::uuid, handler_since = $3 WHERE id = $1::uuid AND state IN ('recruited', 'active') AND handler_id <> $2::uuid`,
		id, handlerID, utcT(at))
}

// AddContact appends to the record of contacts.
func (r *LawRepository) AddContact(ctx context.Context, c application.LawContact) error {
	return r.exec(ctx, "recording a contact", `INSERT INTO law_informant_contacts (id, informant_id, handler_id, kind, pay, case_id, at) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::uuid, $7)`,
		c.ID, c.InformantID, c.HandlerID, c.Kind, c.Pay, nullIfEmpty(c.CaseID), utcT(c.At))
}

// ContactsOf lists an informant's contacts, newest first.
func (r *LawRepository) ContactsOf(ctx context.Context, informantID string, limit int) ([]application.LawContact, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, informant_id::text, handler_id::text, kind, pay, COALESCE(case_id::text, ''), at FROM law_informant_contacts
		WHERE informant_id = $1::uuid ORDER BY at DESC, id LIMIT $2`, informantID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing contacts: %w", err)
	}
	defer rows.Close()
	var out []application.LawContact
	for rows.Next() {
		var c application.LawContact
		if err := rows.Scan(&c.ID, &c.InformantID, &c.HandlerID, &c.Kind, &c.Pay, &c.CaseID, &c.At); err != nil {
			return nil, fmt.Errorf("postgres: reading a contact: %w", err)
		}
		c.At = c.At.UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// bribes and audits
// ---------------------------------------------------------------------------

const lawBribeColumns = `id::text, settlement_id::text, briber_id::text, holder_id::text, rung, effect_bps, amount, dirty, status, term_end, offered_at, answered_at, found_at`

func scanLawBribe(row pgx.Row) (*application.LawBribe, error) {
	var b application.LawBribe
	if err := row.Scan(&b.ID, &b.SettlementID, &b.BriberID, &b.HolderID, &b.Rung, &b.EffectBPS, &b.Amount, &b.Dirty, &b.Status, &b.TermEnd, &b.OfferedAt, &b.AnsweredAt, &b.FoundAt); err != nil {
		return nil, err
	}
	b.TermEnd, b.AnsweredAt, b.FoundAt, b.OfferedAt = utcPtr(b.TermEnd), utcPtr(b.AnsweredAt), utcPtr(b.FoundAt), b.OfferedAt.UTC()
	return &b, nil
}

func (r *LawRepository) bribes(ctx context.Context, sql string, args ...any) ([]application.LawBribe, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing bribes: %w", err)
	}
	defer rows.Close()
	var out []application.LawBribe
	for rows.Next() {
		b, err := scanLawBribe(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a bribe: %w", err)
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// InsertBribe records an offer.
func (r *LawRepository) InsertBribe(ctx context.Context, b application.LawBribe) error {
	return r.exec(ctx, "offering a bribe", `INSERT INTO law_bribes (id, settlement_id, briber_id, holder_id, rung, effect_bps, amount, dirty, status, offered_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, 'offered', $9)`,
		b.ID, b.SettlementID, b.BriberID, b.HolderID, b.Rung, b.EffectBPS, b.Amount, b.Dirty, utcT(b.OfferedAt))
}

// BribeByID reads a bribe, nil for none.
func (r *LawRepository) BribeByID(ctx context.Context, id string, lock bool) (*application.LawBribe, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + lawBribeColumns + ` FROM law_bribes WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	b, err := scanLawBribe(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a bribe: %w", err)
	}
	return b, nil
}

// BribesOf lists the settlement's bribes, newest first.
func (r *LawRepository) BribesOf(ctx context.Context, settlementID string, limit int) ([]application.LawBribe, error) {
	return r.bribes(ctx, `SELECT `+lawBribeColumns+` FROM law_bribes WHERE settlement_id = $1::uuid ORDER BY offered_at DESC, id LIMIT $2`, settlementID, limit)
}

// AnswerBribe answers an offered bribe, once.
func (r *LawRepository) AnswerBribe(ctx context.Context, id, status string, termEnd *time.Time, at time.Time) (bool, error) {
	return r.one(ctx, "answering a bribe", `UPDATE law_bribes SET status = $2, term_end = $3, answered_at = $4 WHERE id = $1::uuid AND status = 'offered'`, id, status, tp(termEnd), utcT(at))
}

// FindBribe marks an accepted bribe as found by an audit, once.
func (r *LawRepository) FindBribe(ctx context.Context, id string, at time.Time) (bool, error) {
	return r.one(ctx, "finding a bribe", `UPDATE law_bribes SET status = 'found', found_at = $2 WHERE id = $1::uuid AND status = 'accepted'`, id, utcT(at))
}

// OpenBribes are the accepted bribes to a holder that have not run out.
func (r *LawRepository) OpenBribes(ctx context.Context, settlementID, holderID string, now time.Time) ([]application.LawBribe, error) {
	return r.bribes(ctx, `SELECT `+lawBribeColumns+` FROM law_bribes WHERE settlement_id = $1::uuid AND holder_id = $2::uuid AND status = 'accepted' AND term_end > $3
		ORDER BY offered_at, id`, settlementID, holderID, utcT(now))
}

// BribeFrom is the accepted, unexpired bribe of a briber to a holder on a rung, nil for none.
func (r *LawRepository) BribeFrom(ctx context.Context, settlementID, briberID, holderID, rung string, now time.Time) (*application.LawBribe, error) {
	b, err := scanLawBribe(r.q.QueryRow(ctx, `SELECT `+lawBribeColumns+` FROM law_bribes WHERE settlement_id = $1::uuid AND briber_id = $2::uuid
		AND ($3 = '' OR holder_id = NULLIF($3, '')::uuid) AND rung = $4 AND status = 'accepted' AND term_end > $5 ORDER BY effect_bps DESC, offered_at LIMIT 1`,
		settlementID, briberID, holderID, rung, utcT(now)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a bribe: %w", err)
	}
	return b, nil
}

// BribedHolders is how many different office-holders of a settlement hold an unexpired accepted bribe.
func (r *LawRepository) BribedHolders(ctx context.Context, settlementID string, now time.Time) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT count(DISTINCT holder_id) FROM law_bribes WHERE settlement_id = $1::uuid AND status = 'accepted' AND term_end > $2`, settlementID, utcT(now)).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting bribed holders: %w", err)
	}
	return n, nil
}

// InsertAudit records an audit; false when that target was audited by that kind that day (the unique row is the fence).
func (r *LawRepository) InsertAudit(ctx context.Context, a application.LawAudit) (bool, error) {
	return r.one(ctx, "recording an audit", `INSERT INTO law_audits (id, settlement_id, kind, auditor_id, target_id, day, outcome, bribe_id, case_id, fee, at)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5::uuid, $6, $7, $8::uuid, $9::uuid, $10, $11) ON CONFLICT DO NOTHING`,
		a.ID, a.SettlementID, a.Kind, a.AuditorID, a.TargetID, a.Day, a.Outcome, nullIfEmpty(a.BribeID), nullIfEmpty(a.CaseID), a.Fee, utcT(a.At))
}

// AuditsOf lists a settlement's audits, newest first.
func (r *LawRepository) AuditsOf(ctx context.Context, settlementID string, limit int) ([]application.LawAudit, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, settlement_id::text, kind, auditor_id::text, target_id::text, day, outcome, COALESCE(bribe_id::text, ''), COALESCE(case_id::text, ''), fee, at
		FROM law_audits WHERE settlement_id = $1::uuid ORDER BY at DESC, id LIMIT $2`, settlementID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing audits: %w", err)
	}
	defer rows.Close()
	var out []application.LawAudit
	for rows.Next() {
		var a application.LawAudit
		if err := rows.Scan(&a.ID, &a.SettlementID, &a.Kind, &a.AuditorID, &a.TargetID, &a.Day, &a.Outcome, &a.BribeID, &a.CaseID, &a.Fee, &a.At); err != nil {
			return nil, fmt.Errorf("postgres: reading an audit: %w", err)
		}
		a.At = a.At.UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}
