package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// DisplayResolver finds the display currency of a player: the money of the settlement they live in
// when it is chartered (docs/adr/0033 section 6.9, rule 2). It is read once per command reply, so a
// player's answer is cached for a few seconds; the rate moves with a book (a 7-period average), never
// faster than that.
type DisplayResolver struct {
	pool *Pool
	ttl  time.Duration
	now  func() time.Time

	mu    sync.Mutex
	cache map[string]displayEntry
}

type displayEntry struct {
	money *presentation.Money
	until time.Time
}

// NewDisplayResolver returns a resolver that keeps an answer for ttl.
func NewDisplayResolver(p *Pool, ttl time.Duration) *DisplayResolver {
	return &DisplayResolver{pool: p, ttl: ttl, now: time.Now, cache: map[string]displayEntry{}}
}

// Display returns the player's display currency, nil when their home has none.
func (r *DisplayResolver) Display(ctx context.Context, playerID string) (*presentation.Money, error) {
	if playerID == "" {
		return nil, nil
	}
	now := r.now()
	r.mu.Lock()
	if e, ok := r.cache[playerID]; ok && now.Before(e.until) {
		r.mu.Unlock()
		return e.money, nil
	}
	r.mu.Unlock()

	var m presentation.Money
	err := r.pool.shared().QueryRow(ctx, `
		SELECT s.currency_code, v.name, v.symbol, s.r0, s.x_ref_ppm
		  FROM players p
		  JOIN village_currency_state s ON s.settlement_id = p.residence_city_id AND s.status = 'chartered'
		  JOIN village_currency_reservations v ON v.settlement_id = s.settlement_id
		 WHERE p.id = $1::uuid`, playerID).Scan(&m.Code, &m.Name, &m.Symbol, &m.R0, &m.XRefPPM)
	var out *presentation.Money
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, fmt.Errorf("postgres: reading a display currency: %w", err)
	default:
		m.RateNum, m.RateDen = m.R0*1_000_000, m.XRefPPM
		out = &m
	}
	r.mu.Lock()
	if len(r.cache) > 20_000 {
		r.cache = map[string]displayEntry{} // bounded: a flush costs one read per active player
	}
	r.cache[playerID] = displayEntry{money: out, until: now.Add(r.ttl)}
	r.mu.Unlock()
	return out, nil
}
