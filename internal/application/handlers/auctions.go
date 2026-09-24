package handlers

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/auction"
	"github.com/mrjvadi/torncity/internal/domain/diplomacy"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/payment"
	"github.com/mrjvadi/torncity/internal/domain/place"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// AuctionRules are the auction house's tuning (config trade.auction_*).
type AuctionRules struct {
	// Durations are the lengths a seller may choose, GAME time.
	Durations  []time.Duration
	MaxReserve int64
	StepBPS    int
	MinStep    int64
	MaxOpen    int
	// ReservesBPS are the reserves offered, as shares of the good's base
	// price.
	ReservesBPS []int
}

// AuctionsHandler serves the auction house (internal/domain/auction): a
// city's open auctions, one auction, putting a piece up, bidding, a player's
// own auctions and bids, and an auction's close from the scheduler.
//
// # Escrow and the close, exactly once
//
// A piece put up moves into the seller's escrow holding; a bid is paid, from
// the purse the bidder chooses, into their escrow account, and an outbid bid
// is paid back in full, to that same purse, the moment it is beaten. The
// close is a scheduled action at the auction's end on the game clock: one
// idempotency key per auction and a status that moves only from open, so a
// second delivery finds nothing to do. Sold, the winner's escrow pays the
// seller's bank less the city's market fee and the piece goes to the winner;
// unsold, it goes back to the seller.
type AuctionsHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	policy  application.PolicyReader
	scale   gametime.Scale
	rules   AuctionRules

	pageSize       int
	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewAuctionsHandler wires the handler.
func NewAuctionsHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, policy application.PolicyReader, scale gametime.Scale, rules AuctionRules,
	pageSize int, idempotencyTTL time.Duration, now func() time.Time,
) *AuctionsHandler {
	if source == nil || cities == nil || policy == nil || ids == nil {
		panic("handlers: NewAuctionsHandler requires content, cities, policy and ids")
	}
	if len(rules.Durations) == 0 || rules.MaxReserve < 1 || rules.MinStep < 1 || rules.MaxOpen < 1 || len(rules.ReservesBPS) == 0 {
		panic("handlers: NewAuctionsHandler requires usable rules")
	}
	if pageSize < 1 {
		pageSize = DefaultPageSize
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &AuctionsHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, policy: policy, scale: scale,
		rules: rules, pageSize: pageSize, idempotencyTTL: idempotencyTTL, now: now}
}

// AuctionRequest is the house's payload: an auction's number, a piece, a
// reserve, a length (its index among the offered ones), an amount, the way to
// pay, a one-time token.
type AuctionRequest struct {
	No       string `json:"no,omitempty"`
	Item     string `json:"item,omitempty"`
	Reserve  string `json:"reserve,omitempty"`
	Duration string `json:"duration,omitempty"`
	Amount   string `json:"amount,omitempty"`
	Nonce    string `json:"nonce,omitempty"`
	Method   string `json:"method,omitempty"`
}

func (h *AuctionsHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

type auctionRefusal struct{ view screens.AuctionRefusalView }

func (r *auctionRefusal) Error() string { return "handlers: auction refused: " + r.view.Kind }

func refuseAuction(kind string, no int64) *auctionRefusal {
	return &auctionRefusal{view: screens.AuctionRefusalView{Kind: kind, No: no}}
}

func (h *AuctionsHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	var r *auctionRefusal
	if stderrors.As(err, &r) {
		return screens.AuctionRefusal(h.screen(meta, lang), r.view), nil
	}
	if v, ok := asBlocked(err); ok {
		return screens.SanctionBlocked(h.screen(meta, lang), v), nil
	}
	if v, ok := asNotHere(err); ok {
		return screens.NotHere(h.screen(meta, lang), v), nil
	}
	if v, ok := asDeclined(err, screens.PaymentDeclinedView{}); ok {
		return screens.PaymentDeclined(h.screen(meta, lang), v), nil
	}
	return nil, err
}

func (h *AuctionsHandler) nonce() string {
	id := strings.ReplaceAll(h.ids.NewID(), "-", "")
	return id[:min(len(id), nonceLength)]
}

func (h *AuctionsHandler) reserve(ctx context.Context, tx application.Tx, playerID string, meta envelope.Metadata, nonce string) (bool, error) {
	key := idempotency.Derive(playerID, meta.RequestID, meta.IdempotencyKey)
	if isNonce(nonce) {
		key = idempotency.Derive(playerID, meta.Command, nonce)
	}
	return tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

func (h *AuctionsHandler) city(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player) (whereabouts, error) {
	if _, err := tx.Travels().Active(ctx, p.ID); err == nil {
		return whereabouts{}, application.ErrAlreadyTravelling
	} else if !isSentinel(err, application.ErrNoActiveTravel) {
		return whereabouts{}, err
	}
	w, err := locate(ctx, tx, h.cities, snap, p)
	if err != nil {
		return w, err
	}
	if w.city == nil {
		return w, application.ErrCityNotFound
	}
	return w, nil
}

func (h *AuctionsHandler) atHouse(w whereabouts) bool {
	if !w.placed() {
		return true
	}
	pl, ok := w.cmap.ForService(place.ServiceAuctionHouse)
	return !ok || (w.walk == nil && pl.Code == w.here.Code)
}

func (h *AuctionsHandler) terms(a application.Auction) auction.Terms {
	return auction.Terms{Reserve: money.FromMinor(a.Reserve), StepBPS: a.StepBPS, MinStep: money.FromMinor(a.MinStep),
		OpensAt: a.OpensAt, EndsAt: a.EndsAt}
}

func highBid(a application.Auction) auction.Bid {
	if a.HighBidder == "" {
		return auction.Bid{}
	}
	return auction.Bid{Bidder: a.HighBidder, Amount: money.FromMinor(a.HighBid)}
}

// line is an auction as a list shows it.
func (h *AuctionsHandler) line(ctx context.Context, tx application.Tx, snap *content.Snapshot, a application.Auction, viewer string, now time.Time) (screens.AuctionLine, error) {
	pc, err := tx.Items().Piece(ctx, a.PieceID)
	if err != nil {
		return screens.AuctionLine{}, err
	}
	return screens.AuctionLine{
		No: a.No, Item: itemNamed(snap, a.Item), Quality: pc.Quality, HighBid: a.HighBid, Reserve: a.Reserve,
		Remaining: max(a.EndsAt.Sub(now), 0), EndsAt: a.EndsAt, Status: a.Status,
		Mine: a.SellerID == viewer, Leading: a.HighBidder != "" && a.HighBidder == viewer,
	}, nil
}

// List handles auction.list: the city's open auctions, soonest to close.
func (h *AuctionsHandler) List(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.AuctionsView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		w, err := h.city(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		view.CityCode, view.City, view.AtHouse = w.city.Code, w.city.Name, h.atHouse(w)
		view.Way = wayTo(w, snap, place.ServiceAuctionHouse, h.scale)
		list, err := tx.Auctions().List(ctx, w.city.ID, 15)
		if err != nil {
			return err
		}
		now := h.now()
		for _, a := range list {
			l, err := h.line(ctx, tx, snap, a, p.ID, now)
			if err != nil {
				return err
			}
			view.Auctions = append(view.Auctions, l)
		}
		return nil
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return screens.Auctions(h.screen(meta, lang), view), nil
}

func parseNo(raw string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// View handles auction.view: one auction, and the bid the viewer may make.
func (h *AuctionsHandler) View(ctx context.Context, meta envelope.Metadata, req AuctionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.AuctionView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		a, err := tx.Auctions().AuctionByNo(ctx, parseNo(req.No))
		if isSentinel(err, application.ErrAuctionNotFound) {
			return refuseAuction(screens.AuctionRefusedNone, 0)
		}
		if err != nil {
			return err
		}
		now := h.now()
		l, err := h.line(ctx, tx, snap, *a, p.ID, now)
		if err != nil {
			return err
		}
		view = screens.AuctionView{Line: l, MinNext: h.terms(*a).MinNext(highBid(*a)).Minor(), Nonce: h.nonce()}
		if seller, err := tx.Players().GetByID(ctx, a.SellerID); err == nil {
			view.Seller = seller.DisplayName
		} else if !isSentinel(err, application.ErrPlayerNotFound) {
			return err
		}
		if a.HighBid > 0 {
			view.Bids = 1
		}
		if a.Status == application.AuctionOpen && !l.Mine && !l.Leading && now.Before(a.EndsAt) {
			w, err := h.city(ctx, tx, snap, p)
			if err == nil && w.city.ID == a.CityID && h.atHouse(w) {
				wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
				if err != nil {
					return err
				}
				choice := paymentChoice(wallet.Plan(money.FromMinor(view.MinNext), snap.Accepts(content.ServiceAuction)), wallet)
				view.Payment = &choice
			}
		}
		return nil
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return screens.AuctionDetail(h.screen(meta, lang), view), nil
}

// New handles auction.new: without terms, the choice of a reserve and a
// length for a piece the player carries; with them, the auction opened.
func (h *AuctionsHandler) New(ctx context.Context, meta envelope.Metadata, req AuctionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	reserve := parseNo(req.Reserve)
	durationAt, durErr := strconv.Atoi(strings.TrimSpace(req.Duration))
	chosen := reserve > 0 && durErr == nil
	var (
		choose   *screens.AuctionNewView
		opened   screens.AuctionOpenedView
		replayed bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		if chosen {
			fresh, err := h.reserve(ctx, tx, p.ID, meta, req.Nonce)
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		now := h.now()
		w, err := h.city(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
			return err
		}
		code, qty, piece, err := held(ctx, tx, p.ID, req.Item)
		if err != nil {
			return err
		}
		if qty == 0 || piece == nil {
			return refuseAuction(screens.AuctionRefusedNotHeld, 0)
		}
		def, _ := snap.ItemDef(code)
		if !def.Item().Tradeable {
			return refuseAuction(screens.AuctionRefusedNotSellable, 0)
		}
		if !chosen {
			v := screens.AuctionNewView{Item: named(def.Code, def.Name), Ref: piece.Serial, Quality: piece.Quality, Nonce: h.nonce()}
			for _, d := range h.rules.Durations {
				v.Durations = append(v.Durations, h.scale.RealWait(d))
			}
			for _, bps := range h.rules.ReservesBPS {
				r := max(def.BasePrice*int64(bps)/10000, 1)
				v.Reserves = append(v.Reserves, min(r, h.rules.MaxReserve))
			}
			choose = &v
			return nil
		}
		if err := needService(w, snap, place.ServiceAuctionHouse, h.scale, now); err != nil {
			return thenFor(err, "auction.list")
		}
		if durationAt < 0 || durationAt >= len(h.rules.Durations) {
			return errors.InvalidInput("auction length is not one on offer")
		}
		open, err := tx.Auctions().CountOpen(ctx, p.ID)
		if err != nil {
			return err
		}
		if open >= h.rules.MaxOpen {
			r := refuseAuction(screens.AuctionRefusedTooMany, 0)
			r.view.Count = h.rules.MaxOpen
			return r
		}
		wait := h.scale.RealWait(h.rules.Durations[durationAt])
		t := auction.Terms{Reserve: money.FromMinor(reserve), StepBPS: h.rules.StepBPS, MinStep: money.FromMinor(h.rules.MinStep),
			OpensAt: now, EndsAt: now.Add(wait)}
		if err := t.Validate(auction.Limits{MinDuration: time.Second, MaxDuration: 365 * 24 * time.Hour,
			MaxReserve: money.FromMinor(h.rules.MaxReserve)}); err != nil {
			return errors.InvalidInput("auction terms are out of bounds").WithCause(err)
		}
		a := application.Auction{ID: h.ids.NewID(), CityID: w.city.ID, SellerID: p.ID, PieceID: piece.ID, Item: code,
			Reserve: reserve, StepBPS: h.rules.StepBPS, MinStep: h.rules.MinStep, OpensAt: now, EndsAt: t.EndsAt,
			GameActionID: h.ids.NewID()}
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: code, PieceID: piece.ID, Qty: 1,
			From: p.ID, FromHolding: application.HoldCarried, To: p.ID, ToHolding: application.HoldEscrow,
			Reason: application.ItemAuctionEscrow, ReferenceType: "auctions", ReferenceID: a.ID, At: now}); err != nil {
			return err
		}
		payload, err := json.Marshal(CrimeActionPayload{ReferenceID: a.ID, PlayerID: p.ID})
		if err != nil {
			return err
		}
		if err := tx.GameActions().Schedule(ctx, application.GameAction{
			ID: a.GameActionID, ActionType: application.AuctionCloseActionType, ActorType: "player", ActorID: p.ID,
			ReferenceType: "auctions", ReferenceID: a.ID, Payload: payload, StartedAt: now, FinishAt: a.EndsAt,
		}); err != nil {
			return err
		}
		stored, err := tx.Auctions().Open(ctx, a)
		if err != nil {
			return err
		}
		opened = screens.AuctionOpenedView{No: stored.No, Item: named(def.Code, def.Name), Reserve: reserve, Duration: wait, EndsAt: a.EndsAt}
		return appendAuctionEvent(ctx, tx, meta, "opened", a.ID, map[string]any{
			"auction_id": a.ID, "no": stored.No, "seller_id": p.ID, "item": code, "reserve": reserve, "ends_at": a.EndsAt,
		})
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	switch {
	case replayed:
		return h.Mine(ctx, meta)
	case choose != nil:
		return screens.AuctionNew(h.screen(meta, lang), *choose), nil
	}
	return screens.AuctionOpened(h.screen(meta, lang), opened), nil
}

// Bid handles auction.bid: a bid paid into escrow, the standing bid beaten
// and paid back.
func (h *AuctionsHandler) Bid(ctx context.Context, meta envelope.Metadata, req AuctionRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	method, chosen, err := chosenMethod(req.Method)
	if err != nil {
		return nil, err
	}
	if !chosen {
		return h.View(ctx, meta, req)
	}
	snap := h.content.Current()
	lang := meta.Language
	amount := parseNo(req.Amount)
	var (
		view     screens.BidPlacedView
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		fresh, err := h.reserve(ctx, tx, p.ID, meta, req.Nonce)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}
		now := h.now()
		a, err := tx.Auctions().AuctionByNo(ctx, parseNo(req.No))
		if isSentinel(err, application.ErrAuctionNotFound) {
			return refuseAuction(screens.AuctionRefusedNone, 0)
		}
		if err != nil {
			return err
		}
		w, err := h.city(ctx, tx, snap, p)
		if err != nil {
			return err
		}
		if w.city.ID != a.CityID {
			return refuseAuction(screens.AuctionRefusedNone, a.No)
		}
		if err := needService(w, snap, place.ServiceAuctionHouse, h.scale, now); err != nil {
			return thenFor(err, "auction.view", strconv.FormatInt(a.No, 10))
		}
		if a.Status != application.AuctionOpen {
			return refuseAuction(screens.AuctionRefusedClosed, a.No)
		}
		// A trade embargo between the bidder's country and the seller's
		// (docs/adr/0022): the one sanctions check.
		if err := playersSanctioned(ctx, tx, diplomacy.Trade, p.ID, a.SellerID, now, screens.AddrAuction,
			strconv.FormatInt(a.No, 10)); err != nil {
			return err
		}
		t := h.terms(*a)
		high, err := auction.PlaceBid(t, a.SellerID, highBid(*a), p.ID, money.FromMinor(amount), now)
		switch {
		case stderrors.Is(err, auction.ErrClosed):
			return refuseAuction(screens.AuctionRefusedClosed, a.No)
		case stderrors.Is(err, auction.ErrOwnAuction):
			return refuseAuction(screens.AuctionRefusedOwn, a.No)
		case stderrors.Is(err, auction.ErrAlreadyWinning):
			return refuseAuction(screens.AuctionRefusedLeading, a.No)
		case stderrors.Is(err, auction.ErrBidTooLow):
			r := refuseAuction(screens.AuctionRefusedTooLow, a.No)
			r.view.MinNext = t.MinNext(highBid(*a)).Minor()
			return r
		case err != nil:
			return errors.Internal(err)
		}
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		plan := wallet.Plan(high.Amount, snap.Accepts(content.ServiceAuction))
		back := []string{screens.AddrAuction, strconv.FormatInt(a.No, 10)}
		if err := checkMethod(plan, method, wallet, "auction.button.back", back...); err != nil {
			return err
		}
		escrow, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerEscrow, p.ID)
		if err != nil {
			return err
		}
		bidID := h.ids.NewID()
		txID, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{
			Method: method, Accepted: plan.Accepted, Reason: application.ReasonAuctionBid,
			ReferenceType: "auction_bids", ReferenceID: bidID,
			To: []application.LedgerEntry{{AccountID: escrow.ID, Amount: high.Amount}}, CreatedAt: now,
		})
		if err != nil {
			if stderrors.Is(err, application.ErrPaymentDeclined) {
				return declined(plan, wallet, "auction.button.back", back...)
			}
			return err
		}
		if a.HighBidID != "" {
			prev, err := tx.Auctions().Bid(ctx, a.HighBidID)
			if err != nil {
				return err
			}
			if err := h.refundBid(ctx, tx, *prev, now); err != nil {
				return err
			}
			def, _ := snap.ItemDef(a.Item)
			if err := appendAuctionEvent(ctx, tx, meta, "outbid", prev.BidderID, map[string]any{
				"player_id": prev.BidderID, "no": a.No, "item": a.Item, "item_name": def.Name, "amount": prev.Amount,
			}); err != nil {
				return err
			}
		}
		if err := tx.Auctions().PlaceBid(ctx, application.AuctionBid{ID: bidID, AuctionID: a.ID, BidderID: p.ID,
			Amount: high.Amount.Minor(), Method: string(method), LedgerTransactionID: txID, CreatedAt: now}, a.HighBidID); err != nil {
			return err
		}
		view = screens.BidPlacedView{No: a.No, Item: itemNamed(snap, a.Item), Amount: high.Amount.Minor(), Method: string(method), EndsAt: a.EndsAt}
		return nil
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	if replayed {
		return h.View(ctx, meta, AuctionRequest{No: req.No})
	}
	return screens.BidPlaced(h.screen(meta, lang), view), nil
}

// refundBid pays a beaten bid back in full, from the bidder's escrow to the
// purse it came from.
func (h *AuctionsHandler) refundBid(ctx context.Context, tx application.Tx, b application.AuctionBid, now time.Time) error {
	escrow, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerEscrow, b.BidderID)
	if err != nil {
		return err
	}
	kind := application.AccountPlayerCash
	if b.Method == string(payment.Card) {
		kind = application.AccountPlayerBank
	}
	purse, err := tx.Ledger().AccountFor(ctx, kind, b.BidderID)
	if err != nil {
		return err
	}
	_, err = tx.Ledger().Post(ctx, application.LedgerTransaction{
		Reason: application.ReasonAuctionRefund, ReferenceType: "auction_bids", ReferenceID: b.ID,
		Entries: []application.LedgerEntry{
			{AccountID: escrow.ID, Amount: money.FromMinor(-b.Amount)}, {AccountID: purse.ID, Amount: money.FromMinor(b.Amount)},
		},
		CreatedAt: now,
	})
	return err
}

// Mine handles auction.mine: what the player sells and bids on.
func (h *AuctionsHandler) Mine(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.MyAuctionsView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		list, err := tx.Auctions().Mine(ctx, p.ID, 15)
		if err != nil {
			return err
		}
		now := h.now()
		for _, a := range list {
			l, err := h.line(ctx, tx, snap, a, p.ID, now)
			if err != nil {
				return err
			}
			view.Auctions = append(view.Auctions, l)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return screens.MyAuctions(h.screen(meta, lang), view), nil
}

// Close ends an auction at its end. It arrives from the SCHEDULER and runs
// exactly once.
func (h *AuctionsHandler) Close(ctx context.Context, meta envelope.Metadata, req CrimeScheduledRequest) (*presenter.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	sellerID, auctionID, err := req.ids()
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		key := idempotency.Derive(sellerID, meta.Command, auctionID)
		fresh, err := tx.Idempotency().Reserve(ctx, string(key), sellerID, meta.RequestID, meta.Command, h.idempotencyTTL)
		if err != nil || !fresh {
			return err
		}
		a, err := tx.Auctions().Auction(ctx, auctionID)
		if isSentinel(err, application.ErrAuctionNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if a.Status != application.AuctionOpen {
			return nil
		}
		now := h.now()
		result, err := auction.Close(h.terms(*a), highBid(*a), now)
		if err != nil {
			return errors.Internal(err)
		}
		def, _ := snap.ItemDef(a.Item)
		// Both holders' goods, in id order.
		for _, id := range orderedIDs(a.SellerID, a.HighBidder) {
			if err := tx.Items().LockOwner(ctx, id); err != nil {
				return err
			}
		}
		if result == auction.Unsold {
			if err := tx.Auctions().Close(ctx, a.ID, application.AuctionUnsold, 0, now); err != nil {
				return err
			}
			if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: a.Item, PieceID: a.PieceID, Qty: 1,
				From: a.SellerID, FromHolding: application.HoldEscrow, To: a.SellerID, ToHolding: application.HoldCarried,
				Reason: application.ItemAuctionReturn, ReferenceType: "auctions", ReferenceID: a.ID, At: now}); err != nil {
				return err
			}
			return appendAuctionEvent(ctx, tx, meta, "unsold", a.SellerID, map[string]any{
				"player_id": a.SellerID, "no": a.No, "item": a.Item, "item_name": def.Name,
			})
		}
		city, err := h.cities.ByID(ctx, a.CityID)
		if err != nil {
			return err
		}
		feeLever, err := h.policy.Get(ctx, city.JurisdictionID, LeverMarketFee)
		if err != nil {
			return err
		}
		price := money.FromMinor(a.HighBid)
		fee := auction.Fee(price, int(feeLever.Value))
		proceeds, err := price.Sub(fee)
		if err != nil {
			return errors.Internal(err)
		}
		if err := tx.Auctions().Close(ctx, a.ID, application.AuctionSold, fee.Minor(), now); err != nil {
			return err
		}
		escrow, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerEscrow, a.HighBidder)
		if err != nil {
			return err
		}
		bank, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, a.SellerID)
		if err != nil {
			return err
		}
		if !proceeds.IsZero() {
			neg, _ := proceeds.Neg()
			if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
				Reason: application.ReasonAuctionSale, ReferenceType: "auctions", ReferenceID: a.ID,
				Entries:   []application.LedgerEntry{{AccountID: escrow.ID, Amount: neg}, {AccountID: bank.ID, Amount: proceeds}},
				CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		if !fee.IsZero() {
			neg, _ := fee.Neg()
			if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
				Reason: application.ReasonMarketFee, ReferenceType: "auctions", ReferenceID: a.ID,
				Entries:   []application.LedgerEntry{{AccountID: escrow.ID, Amount: neg}, {AccountID: application.SystemSinkAccountID, Amount: fee}},
				CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		if err := tx.Items().Move(ctx, application.ItemMove{ID: h.ids.NewID(), Item: a.Item, PieceID: a.PieceID, Qty: 1,
			From: a.SellerID, FromHolding: application.HoldEscrow, To: a.HighBidder, ToHolding: application.HoldCarried,
			Reason: application.ItemAuctionSold, ReferenceType: "auctions", ReferenceID: a.ID, At: now}); err != nil {
			return err
		}
		if err := tx.Auctions().SetBidStatus(ctx, a.HighBidID, application.BidWon); err != nil {
			return err
		}
		if err := appendAuctionEvent(ctx, tx, meta, "sold", a.SellerID, map[string]any{
			"player_id": a.SellerID, "no": a.No, "item": a.Item, "item_name": def.Name,
			"amount": proceeds.Minor(), "fee": fee.Minor(),
		}); err != nil {
			return err
		}
		return appendAuctionEvent(ctx, tx, meta, "won", a.HighBidder, map[string]any{
			"player_id": a.HighBidder, "no": a.No, "item": a.Item, "item_name": def.Name, "amount": a.HighBid,
		})
	})
}

// orderedIDs lists the non-empty ids, sorted, so locks are always taken in
// one order.
func orderedIDs(ids ...string) []string {
	var out []string
	for _, id := range ids {
		if id != "" {
			out = append(out, id)
		}
	}
	if len(out) == 2 && out[1] < out[0] {
		out[0], out[1] = out[1], out[0]
	}
	return out
}

// appendAuctionEvent writes an auction event to the outbox.
func appendAuctionEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata, name, aggregateID string, payload map[string]any) error {
	ev, err := events.New("auction."+name, "auction", aggregateID, payload)
	if err != nil {
		return err
	}
	return tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID: ev.ID, Subject: subjects.Event("auction", name), Metadata: meta, Payload: ev.Payload,
	})
}
