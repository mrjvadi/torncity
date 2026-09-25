package handlers

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/watch"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The finance levers (configs/content/governance.yml), read only through
// application.PolicyReader: every country's central banker sets its policy
// rate and its bank's reserve, its president the treasury's funding of the
// bank.
const (
	LeverBaseInterestRate = "country.base_interest_rate"
	LeverBankReserveRatio = "country.bank_reserve_ratio"
	LeverBankFunding      = "country.bank_funding"
)

// FinanceLimits bound what the exchange accepts (config trade.market_*).
type FinanceLimits struct {
	OrderTTL time.Duration
	MaxOpen  int
}

// FinanceHandler serves finance (docs/adr/0026-finance.md): the national
// bank's loans and the credit score they are lent by, savings, insurance,
// the stock exchange and the gold dealer; and — from the scheduler — the
// finance period, which collects instalments and premiums, pays savings
// interest, funds the banks and moves the gold price, exactly once.
type FinanceHandler struct {
	uow            application.UnitOfWork
	ids            IDGenerator
	msgs           Translator
	content        ContentSource
	cities         application.CityRepository
	policy         application.PolicyReader
	scale          gametime.Scale
	limits         FinanceLimits
	idempotencyTTL time.Duration
	now            func() time.Time

	// watch is the watch's tuning (docs/adr/0023); nil checks nothing.
	watch *watch.Thresholds
}

// NewFinanceHandler builds the handler.
func NewFinanceHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, policy application.PolicyReader, scale gametime.Scale, limits FinanceLimits,
	idempotencyTTL time.Duration, now func() time.Time,
) *FinanceHandler {
	if source == nil || cities == nil || policy == nil || ids == nil {
		panic("handlers: NewFinanceHandler requires content, cities, policy and ids")
	}
	if scale.Validate() != nil || idempotencyTTL <= 0 || limits.OrderTTL <= 0 || limits.MaxOpen < 1 {
		panic("handlers: NewFinanceHandler requires a game clock, limits and an idempotency ttl")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &FinanceHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, policy: policy, scale: scale,
		limits: limits, idempotencyTTL: idempotencyTTL, now: now}
}

// WithWatch has every share trade checked for wash trading and off-market
// prices.
func (h *FinanceHandler) WithWatch(th watch.Thresholds) *FinanceHandler {
	h.watch = &th
	return h
}

// FinanceRequest is every finance command's payload.
type FinanceRequest struct {
	Product  string `json:"product,omitempty"`
	Amount   string `json:"amount,omitempty"`
	Term     string `json:"term,omitempty"`
	Pledge   string `json:"pledge,omitempty"`
	Property string `json:"property,omitempty"`
	Method   string `json:"method,omitempty"`
	Nonce    string `json:"nonce,omitempty"`
	No       string `json:"no,omitempty"`
	Code     string `json:"code,omitempty"`
	Qty      string `json:"qty,omitempty"`
	Price    string `json:"price,omitempty"`
	Grams    string `json:"grams,omitempty"`
	Confirm  string `json:"confirm,omitempty"`
}

// financeRefusal carries a refusal out of a unit of work.
type financeRefusal struct{ view screens.FinanceRefusalView }

func (r *financeRefusal) Error() string { return "handlers: finance refused: " + r.view.Kind }

func refuseFinance(kind string, back ...string) *financeRefusal {
	return &financeRefusal{view: screens.FinanceRefusalView{Kind: kind, Back: back}}
}

func (h *FinanceHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// finish turns what a unit of work ended with into a screen.
func (h *FinanceHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	var r *financeRefusal
	if stderrors.As(err, &r) {
		return screens.FinanceRefusal(h.screen(meta, lang), r.view), nil
	}
	if v, ok := asDeclined(err, screens.PaymentDeclinedView{}); ok {
		return screens.PaymentDeclined(h.screen(meta, lang), v), nil
	}
	return nil, err
}

func (h *FinanceHandler) player(ctx context.Context, tx application.Tx, meta envelope.Metadata, lang *string) (*application.Player, error) {
	p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
	if err != nil {
		return nil, err
	}
	*lang = RenderLanguage(meta, p)
	return p, nil
}

// reserve takes a press's one-time key: the nonce a confirmation carries,
// else the request.
func (h *FinanceHandler) reserve(ctx context.Context, tx application.Tx, p *application.Player, meta envelope.Metadata,
	nonce string,
) (bool, error) {
	key := idempotency.Derive(p.ID, meta.RequestID, meta.IdempotencyKey)
	if isNonce(nonce) {
		key = idempotency.Derive(p.ID, meta.Command, nonce)
	}
	return tx.Idempotency().Reserve(ctx, string(key), p.ID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

// nonce is a fresh one-time token for a confirmation button.
func (h *FinanceHandler) nonce() string {
	id := strings.ReplaceAll(h.ids.NewID(), "-", "")
	return id[:min(len(id), nonceLength)]
}

// def is the finance content; a world without it has no finance.
func (h *FinanceHandler) def(snap *content.Snapshot) (content.FinanceDef, error) {
	d, ok := snap.Finance()
	if !ok {
		return d, errors.NotFound("the content has no finance")
	}
	return d, nil
}

// periodWait is one finance period in real time.
func (h *FinanceHandler) periodWait(def content.FinanceDef) time.Duration {
	return h.scale.RealWait(def.PeriodDuration())
}

// gameSince is the game time since then.
func (h *FinanceHandler) gameSince(then, now time.Time) time.Duration {
	if !now.After(then) {
		return 0
	}
	return now.Sub(then) * time.Duration(h.scale)
}

// realBefore is the instant a GAME duration before now.
func (h *FinanceHandler) realBefore(now time.Time, game string) time.Time {
	d, _ := time.ParseDuration(game)
	return now.Add(-h.scale.RealWait(d))
}

// lever reads a country lever, 0 for a country without one.
func (h *FinanceHandler) lever(ctx context.Context, countryID, code string) (int64, error) {
	v, err := h.policy.Get(ctx, countryID, code)
	if err != nil {
		return 0, err
	}
	return v.Value, nil
}

// countryOf is the player's country, refusing one who belongs to none.
func (h *FinanceHandler) countryOf(ctx context.Context, tx application.Tx, p *application.Player) (application.Jurisdiction, error) {
	j, err := countryFor(ctx, tx, p, "")
	if err != nil {
		return application.Jurisdiction{}, err
	}
	if j == nil {
		return application.Jurisdiction{}, refuseFinance(screens.FinanceRefusedNoBank, screens.AddrHome)
	}
	return *j, nil
}

// nextAt is when the finance clock settles next, zero when not scheduled.
func nextAt(ctx context.Context, tx application.Tx) time.Time {
	_, next, err := tx.Finance().CurrentPeriod(ctx)
	if err != nil || next == nil {
		return time.Time{}
	}
	return *next
}
