package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/application"
)

// The founding form's drafts and uniqueness checks (migration 0051).

const draftColumns = `d.id::text, d.chat_id, d.bot_id::text, d.founder_player_id::text, p.display_name, d.language,
	d.suggested_name, d.suggested_name_latin, d.status, d.created_at, d.expires_at,
	COALESCE(d.submitted_at, 'epoch'::timestamptz), COALESCE(d.settlement_id::text, '')`

func scanDraft(row pgx.Row) (application.FoundingDraft, error) {
	var d application.FoundingDraft
	err := row.Scan(&d.ID, &d.ChatID, &d.BotID, &d.FounderPlayerID, &d.FounderName, &d.Language,
		&d.SuggestedName, &d.SuggestedNameLatin, &d.Status, &d.CreatedAt, &d.ExpiresAt, &d.SubmittedAt, &d.SettlementID)
	if d.SubmittedAt.Unix() == 0 {
		d.SubmittedAt = time.Time{}
	}
	return d, err
}

const draftFrom = ` FROM settlement_founding_drafts d JOIN players p ON p.id = d.founder_player_id `

// CreateDraft opens a founding draft, or returns the chat's open one.
func (r *SettlementRepository) CreateDraft(ctx context.Context, d application.FoundingDraft, now time.Time) (application.FoundingDraft, bool, error) {
	if _, err := r.q.Exec(ctx,
		`UPDATE settlement_founding_drafts SET status = 'expired'
		  WHERE chat_id = $1 AND status = 'open' AND expires_at <= $2`, d.ChatID, now); err != nil {
		return d, false, fmt.Errorf("postgres: expiring founding drafts: %w", err)
	}
	tag, err := r.q.Exec(ctx,
		`INSERT INTO settlement_founding_drafts (id, chat_id, bot_id, founder_player_id, language, suggested_name,
		        suggested_name_latin, status, created_at, expires_at)
		 VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5, $6, $7, 'open', $8, $9)
		 ON CONFLICT (chat_id) WHERE status = 'open' DO NOTHING`,
		d.ID, d.ChatID, d.BotID, d.FounderPlayerID, d.Language, d.SuggestedName, d.SuggestedNameLatin, now, d.ExpiresAt)
	if err != nil {
		return d, false, fmt.Errorf("postgres: creating a founding draft: %w", err)
	}
	created := tag.RowsAffected() == 1
	out, err := scanDraft(r.q.QueryRow(ctx,
		`SELECT `+draftColumns+draftFrom+`WHERE d.chat_id = $1 AND d.status = 'open'`, d.ChatID))
	if err != nil {
		return d, false, fmt.Errorf("postgres: reading the open founding draft: %w", err)
	}
	return out, created, nil
}

// DraftByID reads a draft under a row lock.
func (r *SettlementRepository) DraftByID(ctx context.Context, id string, now time.Time) (application.FoundingDraft, error) {
	if !isUUID(id) {
		return application.FoundingDraft{}, application.ErrFoundingDraftNotFound
	}
	d, err := scanDraft(r.q.QueryRow(ctx,
		`SELECT `+draftColumns+draftFrom+`WHERE d.id = $1::uuid FOR UPDATE OF d`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return d, application.ErrFoundingDraftNotFound
	}
	if err != nil {
		return d, fmt.Errorf("postgres: reading founding draft %s: %w", id, err)
	}
	if d.Status == application.DraftOpen && !now.Before(d.ExpiresAt) {
		if _, err := r.q.Exec(ctx, `UPDATE settlement_founding_drafts SET status = 'expired' WHERE id = $1::uuid`, id); err != nil {
			return d, fmt.Errorf("postgres: expiring founding draft %s: %w", id, err)
		}
		d.Status = application.DraftExpired
	}
	return d, nil
}

// OpenDraftOfChat is the chat's open, unexpired draft.
func (r *SettlementRepository) OpenDraftOfChat(ctx context.Context, chatID int64, now time.Time) (application.FoundingDraft, error) {
	d, err := scanDraft(r.q.QueryRow(ctx,
		`SELECT `+draftColumns+draftFrom+`WHERE d.chat_id = $1 AND d.status = 'open' AND d.expires_at > $2`, chatID, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return d, application.ErrFoundingDraftNotFound
	}
	if err != nil {
		return d, fmt.Errorf("postgres: reading the open founding draft of chat %d: %w", chatID, err)
	}
	return d, nil
}

// OpenDraftOfPlayer is the player's newest open, unexpired draft.
func (r *SettlementRepository) OpenDraftOfPlayer(ctx context.Context, playerID string, now time.Time) (application.FoundingDraft, error) {
	if !isUUID(playerID) {
		return application.FoundingDraft{}, application.ErrFoundingDraftNotFound
	}
	d, err := scanDraft(r.q.QueryRow(ctx,
		`SELECT `+draftColumns+draftFrom+`
		  WHERE d.founder_player_id = $1::uuid AND d.status = 'open' AND d.expires_at > $2
		  ORDER BY d.created_at DESC LIMIT 1`, playerID, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return d, application.ErrFoundingDraftNotFound
	}
	if err != nil {
		return d, fmt.Errorf("postgres: reading the open founding draft of %s: %w", playerID, err)
	}
	return d, nil
}

// MarkDraftSubmitted records the settlement a draft became.
func (r *SettlementRepository) MarkDraftSubmitted(ctx context.Context, draftID, settlementID string, at time.Time) error {
	tag, err := r.q.Exec(ctx,
		`UPDATE settlement_founding_drafts SET status = 'submitted', settlement_id = $2::uuid, submitted_at = $3
		  WHERE id = $1::uuid AND status = 'open'`, draftID, settlementID, at)
	if err != nil {
		return fmt.Errorf("postgres: submitting founding draft %s: %w", draftID, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("postgres: founding draft %s is not open", draftID)
	}
	return nil
}

// FoundingNameTaken reports a name (by key) some city already has. A content
// city has no name_key, so its name is compared in the same normalised shape.
func (r *SettlementRepository) FoundingNameTaken(ctx context.Context, nameKey string) (bool, error) {
	var taken bool
	err := r.q.QueryRow(ctx,
		`SELECT EXISTS (
		    SELECT 1 FROM cities
		     WHERE name_key = $1
		        OR (origin = 'content' AND lower(regexp_replace(name, E'[\\s\\-\u200c]', '', 'g')) = $1))`, nameKey).Scan(&taken)
	if err != nil {
		return false, fmt.Errorf("postgres: checking whether a village name is taken: %w", err)
	}
	return taken, nil
}

// CurrencyTaken reports a currency code that exists or is reserved, and a
// reserved currency name.
func (r *SettlementRepository) CurrencyTaken(ctx context.Context, code, nameKey string) (bool, bool, error) {
	var codeTaken, nameTaken bool
	err := r.q.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM currencies WHERE code = $1)
		     OR EXISTS (SELECT 1 FROM village_currency_reservations WHERE code = $1),
		        EXISTS (SELECT 1 FROM village_currency_reservations WHERE name_key = $2)`, code, nameKey).Scan(&codeTaken, &nameTaken)
	if err != nil {
		return false, false, fmt.Errorf("postgres: checking whether a currency is taken: %w", err)
	}
	return codeTaken, nameTaken, nil
}
