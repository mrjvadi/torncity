package handlers

import (
	"context"
	stderrors "errors"
	"strconv"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/finance"
	"github.com/mrjvadi/torncity/internal/domain/market"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// owned reads a company its owner acts for, locked when lock is set.
func (h *FinanceHandler) owned(ctx context.Context, tx application.Tx, p *application.Player, code string, lock bool) (*application.Company, error) {
	c, err := h.company(ctx, tx, code, lock)
	if err != nil {
		return nil, err
	}
	if c.OwnerID != p.ID {
		return nil, refuseFinance(screens.FinanceRefusedNotOwner, screens.AddrStock, c.Code)
	}
	return c, nil
}

// listingView is what a company's owner may list it at.
func (h *FinanceHandler) listingView(ctx context.Context, tx application.Tx, def content.FinanceDef, c application.Company,
	p *application.Player,
) (screens.ListingView, error) {
	rules := def.ListingRules()
	v := screens.ListingView{Company: named(c.Code, c.Name), MinAge: h.scale.RealWait(rules.MinAge),
		MinRevenue: rules.MinRevenue, Total: c.TotalShares, Fee: def.Stocks.ListingFee}
	book, err := tx.Stocks().Book(ctx, c.ID)
	if err != nil {
		return v, err
	}
	v.Book = finance.BookPerShare(book, c.TotalShares)
	if v.Revenue, err = tx.Stocks().Revenue(ctx, c.ID); err != nil {
		return v, err
	}
	age := h.gameSince(c.FoundedAt, h.now())
	v.Age = h.scale.RealWait(age)
	hold, err := tx.Stocks().Holding(ctx, c.ID, p.ID)
	if err != nil {
		return v, err
	}
	for _, bps := range def.Stocks.FloatOptions {
		if n := finance.FloatShares(c.TotalShares, bps); n <= hold.Free() {
			v.Floats = append(v.Floats, screens.PriceOption{Qty: n, Price: bps})
		}
	}
	for _, bps := range def.Stocks.PriceOptions {
		v.Prices = append(v.Prices, screens.PriceOption{Qty: bps, Price: max(finance.OfBPS(v.Book, bps), 1)})
	}
	v.Refused = rules.Eligible(age, v.Revenue, c.Debt, def.Stocks.FloatOptions[0])
	if v.Refused == "" && len(v.Floats) == 0 {
		v.Refused = finance.ListBadFloat
	}
	return v, nil
}

// IPO handles stock.ipo: the owner lists their company. Without a choice,
// whether it may list and the choices; with one, its confirmation; with a
// nonce, the listing — its fee paid from the company to its city, and the
// shares offered on its new book.
func (h *FinanceHandler) IPO(ctx context.Context, meta envelope.Metadata, req FinanceRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	qty, _ := strconv.ParseInt(req.Qty, 10, 64)
	price, _ := strconv.ParseInt(req.Price, 10, 64)
	confirmed := isNonce(req.Nonce)
	lang := meta.Language
	var (
		view     screens.ListingView
		listed   bool
		code     = req.Code
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		if confirmed {
			fresh, err := h.reserve(ctx, tx, p, meta, req.Nonce)
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		c, err := h.owned(ctx, tx, p, req.Code, confirmed)
		if err != nil {
			return err
		}
		code = c.Code
		if listing, err := tx.Stocks().Listing(ctx, c.ID); err != nil || listing != nil {
			if err != nil {
				return err
			}
			return refuseFinance(screens.FinanceRefusedListed, screens.AddrStock, c.Code)
		}
		if view, err = h.listingView(ctx, tx, def, *c, p); err != nil || view.Refused != "" || qty == 0 {
			return err
		}
		var float, at *screens.PriceOption
		for i := range view.Floats {
			if view.Floats[i].Qty == qty {
				float = &view.Floats[i]
			}
		}
		for i := range view.Prices {
			if view.Prices[i].Price == price {
				at = &view.Prices[i]
			}
		}
		if float == nil || at == nil {
			return refuseFinance(screens.FinanceRefusedFloat, screens.AddrStockIPO, c.Code)
		}
		if !confirmed {
			view.Chosen, view.Nonce = &screens.PriceOption{Qty: qty, Price: price}, h.nonce()
			return nil
		}
		now := h.now()
		pu, err := companyPurse(ctx, tx, *c)
		if err != nil {
			return err
		}
		city, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, c.CityID)
		if err != nil {
			return err
		}
		if pu.total() < def.Stocks.ListingFee {
			r := refuseFinance(screens.FinanceRefusedNoMoney, screens.AddrStock, c.Code)
			r.view.Amount = def.Stocks.ListingFee
			return r
		}
		feeTx, err := pu.pay(ctx, tx, def.Stocks.ListingFee, application.ReasonListingFee, application.StockListingReference,
			c.ID, city.ID, "", now)
		if err != nil {
			return err
		}
		if err := tx.Stocks().RecordListing(ctx, application.StockListing{CompanyID: c.ID, ListedBy: p.ID, FloatShares: qty,
			Price: price, BookPerShare: view.Book, Fee: def.Stocks.ListingFee, LedgerTx: feeTx, At: now}); err != nil {
			return err
		}
		if _, err := h.placeShares(ctx, tx, meta, *c, p, market.Sell, qty, price, now); err != nil {
			return err
		}
		listed = true
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if listed || replayed {
		return h.stock(ctx, meta, code, "listed", nil)
	}
	return screens.Listing(h.screen(meta, lang), view), nil
}

// dividendOptions are the dividends a company may pay: shares of its free
// money, with what each share receives after the corporate tax.
func dividendOptions(def content.FinanceDef, free, taxBPS, total int64) []screens.PriceOption {
	var out []screens.PriceOption
	for _, bps := range def.Stocks.DividendOptions {
		amount := finance.OfBPS(free, bps)
		per := (amount - finance.OfBPS(amount, taxBPS)) / max(total, 1)
		if per >= 1 {
			out = append(out, screens.PriceOption{Qty: per, Price: amount})
		}
	}
	return out
}

// Dividend handles stock.dividend: the owner pays a share of the company's
// free money to every holder — the corporate tax to the city first, the rest
// the same to every share, exactly once.
func (h *FinanceHandler) Dividend(ctx context.Context, meta envelope.Metadata, req FinanceRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	def, err := h.def(snap)
	if err != nil {
		return nil, err
	}
	amount, _ := strconv.ParseInt(req.Amount, 10, 64)
	confirmed := isNonce(req.Nonce)
	lang := meta.Language
	var (
		view     screens.DividendView
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := h.player(ctx, tx, meta, &lang)
		if err != nil {
			return err
		}
		if confirmed {
			fresh, err := h.reserve(ctx, tx, p, meta, req.Nonce)
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		c, err := h.owned(ctx, tx, p, req.Code, true)
		if err != nil {
			return err
		}
		books, _, err := companyBooks(ctx, tx, *c)
		if err != nil {
			return err
		}
		tax, err := h.cityLever(ctx, *c, LeverCorporateTax)
		if err != nil {
			return err
		}
		view = screens.DividendView{Company: named(c.Code, c.Name), Free: books.Available().Minor(), TaxBPS: tax,
			Total: c.TotalShares}
		if c.Debt > 0 {
			view.Refused = "in_debt"
			return nil
		}
		view.Options = dividendOptions(def, view.Free, tax, c.TotalShares)
		if amount == 0 {
			return nil
		}
		var chosen *screens.PriceOption
		for i := range view.Options {
			if view.Options[i].Price == amount {
				chosen = &view.Options[i]
			}
		}
		if chosen == nil {
			return refuseFinance(screens.FinanceRefusedAmount, screens.AddrStockDividend, c.Code)
		}
		if !confirmed {
			view.Chosen, view.Nonce = chosen, h.nonce()
			return nil
		}
		return h.payDividend(ctx, tx, meta, *c, p, amount, tax, &view)
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replayed {
		return h.stock(ctx, meta, req.Code, "", nil)
	}
	return screens.Dividend(h.screen(meta, lang), view), nil
}

// cityLever reads a lever of a company's city.
func (h *FinanceHandler) cityLever(ctx context.Context, c application.Company, code string) (int64, error) {
	city, err := h.cities.ByID(ctx, c.CityID)
	if err != nil {
		return 0, err
	}
	v, err := h.policy.Get(ctx, city.JurisdictionID, code)
	if err != nil {
		return 0, err
	}
	return v.Value, nil
}

// payDividend pays a dividend, the company locked.
func (h *FinanceHandler) payDividend(ctx context.Context, tx application.Tx, meta envelope.Metadata, c application.Company,
	p *application.Player, amount, taxBPS int64, view *screens.DividendView,
) error {
	now := h.now()
	holdings, err := tx.Stocks().Holdings(ctx, c.ID)
	if err != nil {
		return err
	}
	byHolder, _ := sumShares(holdings)
	tax := finance.OfBPS(amount, taxBPS)
	split, err := finance.SplitDividend(amount-tax, c.TotalShares, byHolder)
	if err != nil {
		return err
	}
	treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCompanyTreasury, c.ID)
	if err != nil {
		return err
	}
	city, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, c.CityID)
	if err != nil {
		return err
	}
	d := application.Dividend{ID: h.ids.NewID(), CompanyID: c.ID, DeclaredBy: p.ID, Amount: amount, Tax: tax,
		PerShare: split.PerShare, TotalShares: c.TotalShares, Paid: split.Paid, At: now}
	if tax > 0 {
		d.TaxTx = h.ids.NewID()
	}
	if d, err = tx.Stocks().RecordDividend(ctx, d); err != nil {
		return err
	}
	if _, err := move(ctx, tx, treasury.ID, city.ID, tax, application.ReasonCorporateTax, application.DividendReference,
		d.ID, d.TaxTx, now); err != nil {
		if stderrors.Is(err, application.ErrInsufficientFunds) {
			return refuseFinance(screens.FinanceRefusedNoMoney, screens.AddrStock, c.Code)
		}
		return err
	}
	for _, holder := range sortedKeys(split.Payments) {
		pay := split.Payments[holder]
		bank, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, holder)
		if err != nil {
			return err
		}
		txID, err := move(ctx, tx, treasury.ID, bank.ID, pay, application.ReasonDividend, application.DividendReference,
			d.ID, "", now)
		if err != nil {
			return err
		}
		if err := tx.Stocks().RecordDividendPayment(ctx, application.DividendPayment{DividendID: d.ID, PlayerID: holder,
			Shares: byHolder[holder], Amount: pay, LedgerTx: txID}); err != nil {
			return err
		}
		if err := appendDomainEvent(ctx, tx, meta, "stock", "dividend", d.ID, map[string]any{"kind": "dividend",
			"player_id": holder, "company_code": c.Code, "company_name": c.Name, "qty": byHolder[holder],
			"price": split.PerShare, "amount": pay}); err != nil {
			return err
		}
	}
	view.Paid, view.PerShare, view.Holders = split.Paid, split.PerShare, int64(len(split.Payments))
	view.Free -= amount
	return nil
}
