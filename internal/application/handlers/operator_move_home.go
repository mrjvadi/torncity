package handlers

import (
	"context"
	stderrors "errors"
	"fmt"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// OperatorHomeMove is what an operator's move of a player's home did.
type OperatorHomeMove struct {
	// NoOp is true when the player already lived there: nothing moved.
	NoOp bool
	// From and To are the settlements the player lived in and now lives in.
	From, To string
	// SinceBefore is the residence stamp before the move (nil: none).
	SinceBefore *time.Time
	// Placed is whether the player's physical place changed too (false while travelling,
	// or when they already stood there).
	Placed bool
}

// OperatorMoveHome moves a player's home to another settlement for an operator, skipping
// only the residence cool-down (the 72 h wait of settlement.join): it is the same
// moveHome(..., "join") a player's own join runs, so the residence, its since-stamp, the
// physical place and the residence.changed event (which the notices and the settlement
// channels react to) are exactly those of a normal join. The other gates stay: a player who
// is travelling, at work (a shift) or detained is refused, and the refusal says which.
// Running it again for a player already at home there changes nothing (NoOp).
func OperatorMoveHome(ctx context.Context, tx application.Tx, meta envelope.Metadata, playerID, toSettlementID string, now time.Time,
) (OperatorHomeMove, error) {
	s, err := tx.Settlements().ByID(ctx, toSettlementID)
	if err != nil {
		return OperatorHomeMove{}, fmt.Errorf("handlers: the target is not a founded settlement: %w", err)
	}
	home, err := tx.Employment().ResidenceCityID(ctx, playerID)
	if err != nil {
		return OperatorHomeMove{}, err
	}
	out := OperatorHomeMove{From: home, To: s.CityID}
	if home == s.CityID {
		out.NoOp = true
		return out, nil
	}
	if _, err := tx.Travels().Active(ctx, playerID); err == nil {
		return out, fmt.Errorf("the player is travelling: %w", application.ErrAlreadyTravelling)
	} else if !isSentinel(err, application.ErrNoActiveTravel) {
		return out, err
	}
	if err := refuseAtWork(ctx, tx, playerID); err != nil {
		return out, fmt.Errorf("the player is at work: %w", err)
	}
	if err := RefuseDetained(ctx, tx, playerID, now); err != nil {
		return out, fmt.Errorf("the player is detained: %w", err)
	}
	if out.SinceBefore, err = tx.Property().ResidenceSince(ctx, playerID); err != nil {
		return out, err
	}
	if _, err := moveResidence(ctx, tx, meta, playerID, s.CityID, now, "join"); err != nil {
		return out, err
	}
	out.Placed, err = tx.Property().MoveInto(ctx, playerID, s.CityID, now)
	return out, err
}

// IsHomeMoveRefusal reports whether err is one of the gates OperatorMoveHome keeps.
func IsHomeMoveRefusal(err error) bool {
	return stderrors.Is(err, application.ErrAlreadyTravelling) || stderrors.Is(err, application.ErrShiftInProgress) ||
		stderrors.Is(err, application.ErrInJail) || stderrors.Is(err, application.ErrHospitalised) ||
		stderrors.Is(err, application.ErrCrimeInProgress)
}
