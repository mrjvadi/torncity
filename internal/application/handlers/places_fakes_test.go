package handlers

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
)

// fakePlaces is where players stand and their walks, in memory.
type fakePlaces struct {
	at     map[string]string
	moving map[string]application.PlaceMove
	moves  map[string]application.PlaceMove
}

var _ application.PlaceRepository = (*fakePlaces)(nil)

func newFakePlaces() *fakePlaces {
	return &fakePlaces{at: map[string]string{}, moving: map[string]application.PlaceMove{}, moves: map[string]application.PlaceMove{}}
}

func (t *fakeTx) Places() application.PlaceRepository { return t.places }

func (f *fakePlaces) snapshot() func() {
	at := make(map[string]string, len(f.at))
	for k, v := range f.at {
		at[k] = v
	}
	moving := make(map[string]application.PlaceMove, len(f.moving))
	for k, v := range f.moving {
		moving[k] = v
	}
	moves := make(map[string]application.PlaceMove, len(f.moves))
	for k, v := range f.moves {
		moves[k] = v
	}
	return func() { f.at, f.moving, f.moves = at, moving, moves }
}

func (f *fakePlaces) Where(_ context.Context, playerID string) (string, error) {
	return f.at[playerID], nil
}

func (f *fakePlaces) Put(_ context.Context, playerID, code string, _ time.Time) error {
	f.at[playerID] = code
	return nil
}

func (f *fakePlaces) ActiveMove(_ context.Context, playerID string) (*application.PlaceMove, error) {
	m, ok := f.moving[playerID]
	if !ok {
		return nil, application.ErrNotMoving
	}
	return &m, nil
}

func (f *fakePlaces) StartMove(_ context.Context, m application.PlaceMove) error {
	if _, busy := f.moving[m.PlayerID]; busy {
		return application.ErrAlreadyMoving
	}
	m.Status = application.MoveMoving
	f.moving[m.PlayerID] = m
	f.moves[m.ID] = m
	return nil
}

func (f *fakePlaces) FinishMove(_ context.Context, id string, at time.Time) (*application.PlaceMove, error) {
	m, ok := f.moves[id]
	if !ok || m.Status != application.MoveMoving {
		return nil, application.ErrNotMoving
	}
	m.Status, m.ArrivedAt = application.MoveArrived, &at
	f.moves[id] = m
	delete(f.moving, m.PlayerID)
	f.at[m.PlayerID] = m.To
	return &m, nil
}

func (f *fakePlaces) CancelMove(_ context.Context, id string, _ time.Time) error {
	m, ok := f.moves[id]
	if !ok || m.Status != application.MoveMoving {
		return application.ErrNotMoving
	}
	m.Status = application.MoveCancelled
	f.moves[id] = m
	delete(f.moving, m.PlayerID)
	return nil
}

func (f *fakePlaces) Headcount(context.Context, string) (map[string]int, error) {
	out := map[string]int{}
	for id, code := range f.at {
		if _, walking := f.moving[id]; !walking {
			out[code]++
		}
	}
	return out, nil
}

// fakeItems is what players carry, in memory: enough for the crime engine's
// gear, wear, loot and theft, and for the inventory handler.
type fakeItems struct {
	stacks map[string]int64 // player|item|holding
	pieces map[string]application.Piece
	moves  []application.ItemMove
	used   map[string]time.Time
}

var _ application.ItemRepository = (*fakeItems)(nil)

func newFakeItems() *fakeItems {
	return &fakeItems{stacks: map[string]int64{}, pieces: map[string]application.Piece{}, used: map[string]time.Time{}}
}

func stackKey(player, item, holding string) string { return player + "|" + item + "|" + holding }

func (f *fakeItems) give(player, item string, qty int64) {
	f.stacks[stackKey(player, item, application.HoldCarried)] += qty
}

func (f *fakeItems) LockOwner(context.Context, string) error { return nil }

func (f *fakeItems) Holdings(_ context.Context, playerID, holding string) ([]application.Stack, []application.Piece, error) {
	var stacks []application.Stack
	for k, q := range f.stacks {
		var p, it, h string
		for i, part := range splitKey(k) {
			switch i {
			case 0:
				p = part
			case 1:
				it = part
			case 2:
				h = part
			}
		}
		if p == playerID && h == holding && q > 0 {
			stacks = append(stacks, application.Stack{PlayerID: p, Item: it, Holding: h, Qty: q})
		}
	}
	sort.Slice(stacks, func(i, j int) bool { return stacks[i].Item < stacks[j].Item })
	var pieces []application.Piece
	for _, pc := range f.pieces {
		if pc.OwnerID == playerID && pc.Holding == holding {
			pieces = append(pieces, pc)
		}
	}
	sort.Slice(pieces, func(i, j int) bool { return pieces[i].Serial < pieces[j].Serial })
	return stacks, pieces, nil
}

func splitKey(k string) []string { return strings.Split(k, "|") }

func (f *fakeItems) Piece(_ context.Context, id string) (*application.Piece, error) {
	p, ok := f.pieces[id]
	if !ok {
		return nil, application.ErrPieceNotFound
	}
	return &p, nil
}

func (f *fakeItems) PieceBySerial(_ context.Context, serial string) (*application.Piece, error) {
	for _, p := range f.pieces {
		if p.Serial == serial {
			return &p, nil
		}
	}
	return nil, application.ErrPieceNotFound
}

func (f *fakeItems) CreatePiece(_ context.Context, p application.Piece, m application.ItemMove) error {
	f.pieces[p.ID] = p
	m.PieceID, m.Qty = p.ID, 1
	f.moves = append(f.moves, m)
	return nil
}

func (f *fakeItems) Move(_ context.Context, m application.ItemMove) error {
	if m.PieceID != "" {
		p, ok := f.pieces[m.PieceID]
		if !ok || p.OwnerID != m.From || p.Holding != m.FromHolding {
			return application.ErrPieceNotFound
		}
		p.OwnerID, p.Holding = m.To, m.ToHolding
		if m.To == "" {
			p.Holding = "gone"
		}
		f.pieces[m.PieceID] = p
		f.moves = append(f.moves, m)
		return nil
	}
	if m.From != "" {
		k := stackKey(m.From, m.Item, m.FromHolding)
		if f.stacks[k] < m.Qty {
			return application.ErrNotEnoughItems
		}
		f.stacks[k] -= m.Qty
	}
	if m.To != "" {
		f.stacks[stackKey(m.To, m.Item, m.ToHolding)] += m.Qty
	}
	f.moves = append(f.moves, m)
	return nil
}

func (f *fakeItems) SetUses(_ context.Context, id string, uses int) error {
	p := f.pieces[id]
	p.UsesLeft = uses
	f.pieces[id] = p
	return nil
}

func (f *fakeItems) LastUsed(_ context.Context, playerID, group string) (time.Time, error) {
	return f.used[playerID+"|"+group], nil
}

func (f *fakeItems) MarkUsed(_ context.Context, playerID, group string, at time.Time) error {
	f.used[playerID+"|"+group] = at
	return nil
}

func (f *fakeItems) snapshot() func() {
	stacks := make(map[string]int64, len(f.stacks))
	for k, v := range f.stacks {
		stacks[k] = v
	}
	pieces := make(map[string]application.Piece, len(f.pieces))
	for k, v := range f.pieces {
		pieces[k] = v
	}
	used := make(map[string]time.Time, len(f.used))
	for k, v := range f.used {
		used[k] = v
	}
	moves := len(f.moves)
	return func() { f.stacks, f.pieces, f.used, f.moves = stacks, pieces, used, f.moves[:moves] }
}

func (t *fakeTx) Items() application.ItemRepository { return t.items }

// The shops, the market and the auction house have no fakes: their handlers
// are tested against PostgreSQL (tests/trade_integration_test.go).
func (t *fakeTx) Shops() application.ShopRepository         { return nil }
func (t *fakeTx) Market() application.MarketRepository      { return nil }
func (t *fakeTx) Auctions() application.AuctionRepository   { return nil }
func (t *fakeTx) Elections() application.ElectionRepository { return nil }
func (t *fakeTx) Companies() application.CompanyRepository  { return noCompanies{} }
