package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// LicenceRepository is the requirements, licences, auctions, applications, listings and the audit of the licence market (migration 0155;
// docs/adr/0077 7), bound to one transaction.
type LicenceRepository struct{ q querier }

var _ application.LicenceRepository = (*LicenceRepository)(nil)

const licRuleColumns = `settlement_id::text, kind, mode, fee, reserve, term_days, transferable, set_by::text, set_at, effective_at, closed_at`

func scanLicRule(row pgx.Row) (*application.LicenceRule, error) {
	var r application.LicenceRule
	if err := row.Scan(&r.SettlementID, &r.Kind, &r.Mode, &r.Fee, &r.Reserve, &r.TermDays, &r.Transferable, &r.SetBy, &r.SetAt, &r.EffectiveAt, &r.ClosedAt); err != nil {
		return nil, err
	}
	r.SetAt, r.EffectiveAt, r.ClosedAt = r.SetAt.UTC(), r.EffectiveAt.UTC(), utcPtr(r.ClosedAt)
	return &r, nil
}

// Rule is the open requirement of a kind in a settlement, nil for none.
func (r *LicenceRepository) Rule(ctx context.Context, settlementID, kind string) (*application.LicenceRule, error) {
	if !validUUID(settlementID) {
		return nil, nil
	}
	x, err := scanLicRule(r.q.QueryRow(ctx, `SELECT `+licRuleColumns+` FROM licence_rules WHERE settlement_id = $1::uuid AND kind = $2 AND closed_at IS NULL`, settlementID, kind))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a requirement: %w", err)
	}
	return x, nil
}

// Rules lists a settlement's open requirements.
func (r *LicenceRepository) Rules(ctx context.Context, settlementID string) ([]application.LicenceRule, error) {
	rows, err := r.q.Query(ctx, `SELECT `+licRuleColumns+` FROM licence_rules WHERE settlement_id = $1::uuid AND closed_at IS NULL ORDER BY kind`, settlementID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing requirements: %w", err)
	}
	defer rows.Close()
	var out []application.LicenceRule
	for rows.Next() {
		x, err := scanLicRule(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a requirement: %w", err)
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

// UpsertRule sets or changes a requirement. The day it takes effect is kept while the rule stands: changing the price does not move the grace.
func (r *LicenceRepository) UpsertRule(ctx context.Context, x application.LicenceRule) error {
	_, err := r.q.Exec(ctx, `INSERT INTO licence_rules (settlement_id, kind, mode, fee, reserve, term_days, transferable, set_by, set_at, effective_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8::uuid, $9, $10)
		ON CONFLICT (settlement_id, kind) DO UPDATE SET mode = EXCLUDED.mode, fee = EXCLUDED.fee, reserve = EXCLUDED.reserve, term_days = EXCLUDED.term_days,
			transferable = EXCLUDED.transferable, set_by = EXCLUDED.set_by,
			set_at = CASE WHEN licence_rules.closed_at IS NULL THEN licence_rules.set_at ELSE EXCLUDED.set_at END,
			effective_at = CASE WHEN licence_rules.closed_at IS NULL THEN licence_rules.effective_at ELSE EXCLUDED.effective_at END,
			closed_at = NULL`,
		x.SettlementID, x.Kind, x.Mode, x.Fee, x.Reserve, x.TermDays, x.Transferable, x.SetBy, x.SetAt.UTC(), x.EffectiveAt.UTC())
	if err != nil {
		return fmt.Errorf("postgres: setting a requirement: %w", err)
	}
	return nil
}

// CloseRule ends a requirement; the licences granted stand to the end of their terms.
func (r *LicenceRepository) CloseRule(ctx context.Context, settlementID, kind string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE licence_rules SET closed_at = $3 WHERE settlement_id = $1::uuid AND kind = $2 AND closed_at IS NULL`, settlementID, kind, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: closing a requirement: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const licColumns = `id::text, no, kind, issuer_id::text, holder_kind, holder_id::text, target_ref, via, paid, transferable, status, term_start, term_end, created_at,
	last_used_at, notice_until, transferred_at, ended_at, COALESCE(cause, ''), COALESCE(revoke_proposed_by::text, ''), COALESCE(from_licence::text, '')`

func scanLic(row pgx.Row) (*application.Licence, error) {
	var l application.Licence
	if err := row.Scan(&l.ID, &l.No, &l.Kind, &l.IssuerID, &l.HolderKind, &l.HolderID, &l.TargetRef, &l.Via, &l.Paid, &l.Transferable, &l.Status, &l.TermStart, &l.TermEnd, &l.CreatedAt,
		&l.LastUsedAt, &l.NoticeUntil, &l.TransferredAt, &l.EndedAt, &l.Cause, &l.RevokeProposedBy, &l.FromLicence); err != nil {
		return nil, err
	}
	l.TermStart, l.TermEnd, l.CreatedAt = l.TermStart.UTC(), l.TermEnd.UTC(), l.CreatedAt.UTC()
	l.LastUsedAt, l.NoticeUntil, l.TransferredAt, l.EndedAt = utcPtr(l.LastUsedAt), utcPtr(l.NoticeUntil), utcPtr(l.TransferredAt), utcPtr(l.EndedAt)
	return &l, nil
}

func (r *LicenceRepository) lics(ctx context.Context, sql string, args ...any) ([]application.Licence, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing licences: %w", err)
	}
	defer rows.Close()
	var out []application.Licence
	for rows.Next() {
		l, err := scanLic(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a licence: %w", err)
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// Insert grants a licence; false when the right is held already (the unique index is the fence) or the id exists.
func (r *LicenceRepository) Insert(ctx context.Context, l application.Licence) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO licences (id, kind, issuer_id, holder_kind, holder_id, target_ref, via, paid, transferable, status, term_start, term_end, created_at, from_licence)
		VALUES ($1::uuid, $2, $3::uuid, $4, $5::uuid, $6, $7, $8, $9, 'active', $10, $11, $12, $13::uuid) ON CONFLICT DO NOTHING`,
		l.ID, l.Kind, l.IssuerID, l.HolderKind, l.HolderID, l.TargetRef, l.Via, l.Paid, l.Transferable, l.TermStart.UTC(), l.TermEnd.UTC(), l.CreatedAt.UTC(), nullText(l.FromLicence))
	if err != nil {
		return false, fmt.Errorf("postgres: granting a licence: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ByID reads a licence, nil for none.
func (r *LicenceRepository) ByID(ctx context.Context, id string, lock bool) (*application.Licence, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + licColumns + ` FROM licences WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	l, err := scanLic(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a licence: %w", err)
	}
	return l, nil
}

// Live lists the live licences of a kind in a settlement.
func (r *LicenceRepository) Live(ctx context.Context, issuerID, kind string) ([]application.Licence, error) {
	return r.lics(ctx, `SELECT `+licColumns+` FROM licences WHERE issuer_id = $1::uuid AND kind = $2 AND status IN ('active', 'notice') ORDER BY no`, issuerID, kind)
}

// LiveOfHolder lists the live licences a holder has.
func (r *LicenceRepository) LiveOfHolder(ctx context.Context, holderKind, holderID string) ([]application.Licence, error) {
	if !validUUID(holderID) {
		return nil, nil
	}
	return r.lics(ctx, `SELECT `+licColumns+` FROM licences WHERE holder_kind = $1 AND holder_id = $2::uuid AND status IN ('active', 'notice') ORDER BY no`, holderKind, holderID)
}

// Held is the live licence over a target, nil for none.
func (r *LicenceRepository) Held(ctx context.Context, issuerID, kind, targetRef string) (*application.Licence, error) {
	if !validUUID(issuerID) {
		return nil, nil
	}
	l, err := scanLic(r.q.QueryRow(ctx, `SELECT `+licColumns+` FROM licences WHERE issuer_id = $1::uuid AND kind = $2 AND target_ref = $3 AND status IN ('active', 'notice')`, issuerID, kind, targetRef))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a licence: %w", err)
	}
	return l, nil
}

// CountLive is how many live licences of the kind the settlement has.
func (r *LicenceRepository) CountLive(ctx context.Context, issuerID, kind string) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM licences WHERE issuer_id = $1::uuid AND kind = $2 AND status IN ('active', 'notice')`, issuerID, kind).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting licences: %w", err)
	}
	return n, nil
}

// CountHeld is how many live licences of the kind one holder has in the settlement.
func (r *LicenceRepository) CountHeld(ctx context.Context, issuerID, kind, holderKind, holderID string) (int64, error) {
	var n int64
	if err := r.q.QueryRow(ctx, `SELECT count(*) FROM licences WHERE issuer_id = $1::uuid AND kind = $2 AND holder_kind = $3 AND holder_id = $4::uuid AND status IN ('active', 'notice')`,
		issuerID, kind, holderKind, holderID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting licences: %w", err)
	}
	return n, nil
}

// OfIssuer lists the latest licences of a settlement, newest first.
func (r *LicenceRepository) OfIssuer(ctx context.Context, issuerID string, limit int) ([]application.Licence, error) {
	return r.lics(ctx, `SELECT `+licColumns+` FROM licences WHERE issuer_id = $1::uuid ORDER BY no DESC LIMIT $2`, issuerID, limit)
}

// Touch records a use of the licence.
func (r *LicenceRepository) Touch(ctx context.Context, id string, at time.Time) error {
	_, err := r.q.Exec(ctx, `UPDATE licences SET last_used_at = $2 WHERE id = $1::uuid AND status IN ('active', 'notice') AND (last_used_at IS NULL OR last_used_at < $2)`, id, at.UTC())
	if err != nil {
		return fmt.Errorf("postgres: touching a licence: %w", err)
	}
	return nil
}

// Notice puts an active licence under notice, once.
func (r *LicenceRepository) Notice(ctx context.Context, id, cause string, until time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE licences SET status = 'notice', cause = $2, notice_until = $3, revoke_proposed_by = NULL WHERE id = $1::uuid AND status = 'active'`, id, cause, until.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: serving notice: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Revoke ends a live licence, once.
func (r *LicenceRepository) Revoke(ctx context.Context, id, cause string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE licences SET status = 'revoked', cause = $2, ended_at = $3 WHERE id = $1::uuid AND status IN ('active', 'notice')`, id, cause, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: revoking a licence: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Expire ends a licence whose term is over, once.
func (r *LicenceRepository) Expire(ctx context.Context, id string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE licences SET status = 'expired', ended_at = $2 WHERE id = $1::uuid AND status IN ('active', 'notice') AND term_end <= $2`, id, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: expiring a licence: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Transferred ends a licence that was sold, once.
func (r *LicenceRepository) Transferred(ctx context.Context, id string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE licences SET status = 'transferred', transferred_at = $2, ended_at = $2 WHERE id = $1::uuid AND status = 'active'`, id, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: transferring a licence: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ProposeRevoke records the first holder's proposal of a revocation without cause; false when one stands already.
func (r *LicenceRepository) ProposeRevoke(ctx context.Context, id, by string) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE licences SET revoke_proposed_by = $2::uuid WHERE id = $1::uuid AND status = 'active' AND revoke_proposed_by IS NULL`, id, by)
	if err != nil {
		return false, fmt.Errorf("postgres: proposing a revocation: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Renew extends a live licence to a later end and counts what was paid.
func (r *LicenceRepository) Renew(ctx context.Context, id string, newEnd time.Time, paid int64) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE licences SET term_end = $2, paid = paid + $3 WHERE id = $1::uuid AND status IN ('active') AND term_end < $2`, id, newEnd.UTC(), paid)
	if err != nil {
		return false, fmt.Errorf("postgres: renewing a licence: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const licAuctionColumns = `id::text, issuer_id::text, kind, target_ref, posted_by::text, status, COALESCE(bid_id::text, ''), COALESCE(licence_id::text, ''), reserve, COALESCE(price, 0), term_days, transferable, opens_at, closes_at, closed_at`

func scanLicAuction(row pgx.Row) (*application.LicenceAuction, error) {
	var a application.LicenceAuction
	if err := row.Scan(&a.ID, &a.IssuerID, &a.Kind, &a.TargetRef, &a.PostedBy, &a.Status, &a.BidID, &a.LicenceID, &a.Reserve, &a.Price, &a.TermDays, &a.Transferable, &a.OpensAt, &a.ClosesAt, &a.ClosedAt); err != nil {
		return nil, err
	}
	a.OpensAt, a.ClosesAt, a.ClosedAt = a.OpensAt.UTC(), a.ClosesAt.UTC(), utcPtr(a.ClosedAt)
	return &a, nil
}

func (r *LicenceRepository) auctions(ctx context.Context, sql string, args ...any) ([]application.LicenceAuction, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing auctions: %w", err)
	}
	defer rows.Close()
	var out []application.LicenceAuction
	for rows.Next() {
		a, err := scanLicAuction(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading an auction: %w", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// InsertAuction opens an auction; false when the right has an open one.
func (r *LicenceRepository) InsertAuction(ctx context.Context, a application.LicenceAuction) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO licence_auctions (id, issuer_id, kind, target_ref, posted_by, reserve, term_days, transferable, opens_at, closes_at, status)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5::uuid, $6, $7, $8, $9, $10, 'open') ON CONFLICT DO NOTHING`,
		a.ID, a.IssuerID, a.Kind, a.TargetRef, a.PostedBy, a.Reserve, a.TermDays, a.Transferable, a.OpensAt.UTC(), a.ClosesAt.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: opening an auction: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// AuctionByID reads an auction, nil for none.
func (r *LicenceRepository) AuctionByID(ctx context.Context, id string, lock bool) (*application.LicenceAuction, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + licAuctionColumns + ` FROM licence_auctions WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	a, err := scanLicAuction(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading an auction: %w", err)
	}
	return a, nil
}

// AuctionsOf lists a settlement's latest auctions.
func (r *LicenceRepository) AuctionsOf(ctx context.Context, issuerID string, limit int) ([]application.LicenceAuction, error) {
	return r.auctions(ctx, `SELECT `+licAuctionColumns+` FROM licence_auctions WHERE issuer_id = $1::uuid ORDER BY opens_at DESC, id LIMIT $2`, issuerID, limit)
}

// AddBid records a sealed bid; false when the bidder has bid on the auction already.
func (r *LicenceRepository) AddBid(ctx context.Context, b application.LicenceBid) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO licence_bids (id, auction_id, bidder_kind, bidder_id, bid_by, amount, status, at)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid, $5::uuid, $6, 'standing', $7) ON CONFLICT DO NOTHING`, b.ID, b.AuctionID, b.BidderKind, b.BidderID, b.BidBy, b.Amount, b.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording a bid: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Bids lists an auction's bids, oldest first.
func (r *LicenceRepository) Bids(ctx context.Context, auctionID string) ([]application.LicenceBid, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, auction_id::text, bidder_kind, bidder_id::text, bid_by::text, status, amount, at FROM licence_bids WHERE auction_id = $1::uuid ORDER BY at, id`, auctionID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing bids: %w", err)
	}
	defer rows.Close()
	var out []application.LicenceBid
	for rows.Next() {
		var b application.LicenceBid
		if err := rows.Scan(&b.ID, &b.AuctionID, &b.BidderKind, &b.BidderID, &b.BidBy, &b.Status, &b.Amount, &b.At); err != nil {
			return nil, fmt.Errorf("postgres: reading a bid: %w", err)
		}
		b.At = b.At.UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}

// SetBid sets one bid's state.
func (r *LicenceRepository) SetBid(ctx context.Context, bidID, status string) error {
	_, err := r.q.Exec(ctx, `UPDATE licence_bids SET status = $2 WHERE id = $1::uuid`, bidID, status)
	if err != nil {
		return fmt.Errorf("postgres: setting a bid: %w", err)
	}
	return nil
}

// AwardAuction closes an open auction with its winner in one conditional update: true for exactly one caller.
func (r *LicenceRepository) AwardAuction(ctx context.Context, id, bidID, licenceID string, price int64, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE licence_auctions SET status = 'awarded', bid_id = $2::uuid, licence_id = $3::uuid, price = $4, closed_at = $5 WHERE id = $1::uuid AND status = 'open'`,
		id, bidID, licenceID, price, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: awarding an auction: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetAuctionStatus moves an auction from one state to another, once.
func (r *LicenceRepository) SetAuctionStatus(ctx context.Context, id, from, to string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE licence_auctions SET status = $3, closed_at = $4 WHERE id = $1::uuid AND status = $2`, id, from, to, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: moving an auction: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const licAppColumns = `id::text, issuer_id::text, kind, target_ref, holder_kind, holder_id::text, applied_by::text, status, COALESCE(decided_by::text, ''), COALESCE(licence_id::text, ''), fee, term_days, transferable, at, decided_at`

func scanLicApp(row pgx.Row) (*application.LicenceApplication, error) {
	var a application.LicenceApplication
	if err := row.Scan(&a.ID, &a.IssuerID, &a.Kind, &a.TargetRef, &a.HolderKind, &a.HolderID, &a.AppliedBy, &a.Status, &a.DecidedBy, &a.LicenceID, &a.Fee, &a.TermDays, &a.Transferable, &a.At, &a.DecidedAt); err != nil {
		return nil, err
	}
	a.At, a.DecidedAt = a.At.UTC(), utcPtr(a.DecidedAt)
	return &a, nil
}

// InsertApplication records an application; false when the holder has one pending for the right.
func (r *LicenceRepository) InsertApplication(ctx context.Context, a application.LicenceApplication) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO licence_applications (id, issuer_id, kind, target_ref, holder_kind, holder_id, applied_by, fee, term_days, transferable, status, at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, $7::uuid, $8, $9, $10, 'pending', $11) ON CONFLICT DO NOTHING`,
		a.ID, a.IssuerID, a.Kind, a.TargetRef, a.HolderKind, a.HolderID, a.AppliedBy, a.Fee, a.TermDays, a.Transferable, a.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: recording an application: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ApplicationByID reads an application, nil for none.
func (r *LicenceRepository) ApplicationByID(ctx context.Context, id string, lock bool) (*application.LicenceApplication, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + licAppColumns + ` FROM licence_applications WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	a, err := scanLicApp(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading an application: %w", err)
	}
	return a, nil
}

// PendingApplications lists a settlement's pending applications, oldest first.
func (r *LicenceRepository) PendingApplications(ctx context.Context, issuerID string, limit int) ([]application.LicenceApplication, error) {
	rows, err := r.q.Query(ctx, `SELECT `+licAppColumns+` FROM licence_applications WHERE issuer_id = $1::uuid AND status = 'pending' ORDER BY at, id LIMIT $2`, issuerID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing applications: %w", err)
	}
	defer rows.Close()
	var out []application.LicenceApplication
	for rows.Next() {
		a, err := scanLicApp(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading an application: %w", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// DecideApplication moves a pending application to granted, refused or withdrawn, once.
func (r *LicenceRepository) DecideApplication(ctx context.Context, id, from, to, by, licenceID string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE licence_applications SET status = $3, decided_by = $4::uuid, licence_id = $5::uuid, decided_at = $6 WHERE id = $1::uuid AND status = $2`,
		id, from, to, nullText(by), nullText(licenceID), at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: deciding an application: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// InsertListing puts a licence on the board; false when it is listed already.
func (r *LicenceRepository) InsertListing(ctx context.Context, l application.LicenceListing) (bool, error) {
	tag, err := r.q.Exec(ctx, `INSERT INTO licence_listings (id, licence_id, seller_id, price, status, at) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 'open', $5) ON CONFLICT DO NOTHING`,
		l.ID, l.LicenceID, l.SellerID, l.Price, l.At.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: listing a licence: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

const licListColumns = `id::text, licence_id::text, seller_id::text, status, price, at, closed_at`

func scanLicListing(row pgx.Row) (*application.LicenceListing, error) {
	var l application.LicenceListing
	if err := row.Scan(&l.ID, &l.LicenceID, &l.SellerID, &l.Status, &l.Price, &l.At, &l.ClosedAt); err != nil {
		return nil, err
	}
	l.At, l.ClosedAt = l.At.UTC(), utcPtr(l.ClosedAt)
	return &l, nil
}

// ListingByID reads a listing, nil for none.
func (r *LicenceRepository) ListingByID(ctx context.Context, id string, lock bool) (*application.LicenceListing, error) {
	if !validUUID(id) {
		return nil, nil
	}
	sql := `SELECT ` + licListColumns + ` FROM licence_listings WHERE id = $1::uuid`
	if lock {
		sql += ` FOR UPDATE`
	}
	l, err := scanLicListing(r.q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: reading a listing: %w", err)
	}
	return l, nil
}

// OpenListings lists the licences for sale in a settlement.
func (r *LicenceRepository) OpenListings(ctx context.Context, issuerID string, limit int) ([]application.LicenceListing, error) {
	rows, err := r.q.Query(ctx, `SELECT l.id::text, l.licence_id::text, l.seller_id::text, l.status, l.price, l.at, l.closed_at FROM licence_listings l
		JOIN licences x ON x.id = l.licence_id WHERE x.issuer_id = $1::uuid AND l.status = 'open' ORDER BY l.at, l.id LIMIT $2`, issuerID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing the board: %w", err)
	}
	defer rows.Close()
	var out []application.LicenceListing
	for rows.Next() {
		l, err := scanLicListing(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a listing: %w", err)
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// CloseListing ends an open listing (sold or cancelled), once.
func (r *LicenceRepository) CloseListing(ctx context.Context, id, to string, at time.Time) (bool, error) {
	tag, err := r.q.Exec(ctx, `UPDATE licence_listings SET status = $2, closed_at = $3 WHERE id = $1::uuid AND status = 'open'`, id, to, at.UTC())
	if err != nil {
		return false, fmt.Errorf("postgres: closing a listing: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Event writes a line of the public audit.
func (r *LicenceRepository) Event(ctx context.Context, e application.LicenceEvent) error {
	raw, err := json.Marshal(e.Detail)
	if err != nil {
		return err
	}
	_, err = r.q.Exec(ctx, `INSERT INTO licence_events (id, issuer_id, licence_id, actor_id, kind, detail, at) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::jsonb, $7)`,
		e.ID, e.IssuerID, nullText(e.LicenceID), nullText(e.ActorID), e.Kind, string(raw), e.At.UTC())
	if err != nil {
		return fmt.Errorf("postgres: writing a licence event: %w", err)
	}
	return nil
}

// Events lists a settlement's latest audit lines.
func (r *LicenceRepository) Events(ctx context.Context, issuerID string, limit int) ([]application.LicenceEvent, error) {
	rows, err := r.q.Query(ctx, `SELECT id::text, issuer_id::text, COALESCE(licence_id::text, ''), COALESCE(actor_id::text, ''), kind, detail, at FROM licence_events WHERE issuer_id = $1::uuid ORDER BY at DESC, id LIMIT $2`, issuerID, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing licence events: %w", err)
	}
	defer rows.Close()
	var out []application.LicenceEvent
	for rows.Next() {
		var e application.LicenceEvent
		var raw []byte
		if err := rows.Scan(&e.ID, &e.IssuerID, &e.LicenceID, &e.ActorID, &e.Kind, &raw, &e.At); err != nil {
			return nil, fmt.Errorf("postgres: reading a licence event: %w", err)
		}
		_ = json.Unmarshal(raw, &e.Detail)
		e.At = e.At.UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
