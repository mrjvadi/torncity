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
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// RecruitRules is the tuning of specialist recruitment (config
// company.recruit_*, and the bank's bound on one amount).
type RecruitRules struct {
	// CheckEvery is the GAME time between two checks of a campaign;
	// Checks how many checks one campaign runs.
	CheckEvery time.Duration
	Checks     int
	// MaxCampaigns bounds a company's running campaigns, MaxPositions the
	// hires one campaign seeks, MaxCandidates the candidates one check
	// brings, MaxStaff a company's specialists.
	MaxCampaigns, MaxPositions, MaxCandidates, MaxStaff int
	// Patience is how long, GAME time, a candidate waits for an answer.
	Patience time.Duration
	// Period is one company period, GAME time: what a contract counts in.
	Period time.Duration
	Limits bank.Limits
}

// RecruitHandler serves specialist recruitment
// (docs/adr/0027-specialist-recruitment.md): a company's recruitment hub,
// the campaign builder, posting a campaign (its advertising fee to each
// city's treasury), a campaign's candidates and hiring them (signing bonus
// and move paid once), the company's specialists — renewing, matching the
// market, parting ways — and, from the scheduler, a campaign's check.
//
// A specialist's pay each period is the company settlement's
// (recruit_period.go, CompaniesHandler).
type RecruitHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	scale   gametime.Scale
	rules   RecruitRules

	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewRecruitHandler wires the handler.
func NewRecruitHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, scale gametime.Scale, rules RecruitRules, idempotencyTTL time.Duration,
	now func() time.Time,
) *RecruitHandler {
	if source == nil || cities == nil || ids == nil {
		panic("handlers: NewRecruitHandler requires content, cities and ids")
	}
	if scale.Validate() != nil || idempotencyTTL <= 0 || rules.CheckEvery <= 0 || rules.Checks < 1 ||
		rules.MaxCampaigns < 1 || rules.MaxPositions < 1 || rules.MaxCandidates < 1 || rules.MaxStaff < 1 ||
		rules.Patience <= 0 || rules.Period <= 0 || rules.Limits.Max.Minor() <= 0 {
		panic("handlers: NewRecruitHandler requires a game clock, an idempotency ttl and valid rules")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &RecruitHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, scale: scale,
		rules: rules, idempotencyTTL: idempotencyTTL, now: now}
}

// RecruitRequest is the payload of the recruitment commands; which fields a
// command reads is its own. The names are those internal/gateway/routing
// gives the arguments.
type RecruitRequest struct {
	Company string `json:"company,omitempty"`
	Skill   string `json:"skill,omitempty"`
	Level   string `json:"level,omitempty"`
	No      string `json:"no,omitempty"`
	Section string `json:"section,omitempty"`
	Field   string `json:"field,omitempty"`
	Value   string `json:"value,omitempty"`
	Extra   string `json:"extra,omitempty"`
	Amount  string `json:"amount,omitempty"`
	Verdict string `json:"verdict,omitempty"`
	Act     string `json:"act,omitempty"`
	Confirm string `json:"confirm,omitempty"`
}

func (r RecruitRequest) code() string { return playercode.Normalize(r.Company) }

func (r RecruitRequest) confirmed() bool {
	return strings.TrimSpace(r.Confirm) == screens.RecruitConfirm
}

// number reads the request's public number.
func (r RecruitRequest) number() (int64, bool) { return number(r.No) }

func (h *RecruitHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// recruitRefusal carries a refused recruitment command out of a unit of
// work.
type recruitRefusal struct{ view screens.RecruitRefusalView }

func (r *recruitRefusal) Error() string { return "handlers: recruitment refused: " + r.view.Kind }

// refuseRecruit is a refusal about c (nil when none is known).
func refuseRecruit(kind string, c *application.Company, snap *content.Snapshot) *recruitRefusal {
	r := &recruitRefusal{view: screens.RecruitRefusalView{Kind: kind}}
	if c != nil {
		r.view.Ref = companyRef(snap, *c)
	}
	return r
}

// finish turns a refusal into its screen.
func (h *RecruitHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	if err == nil {
		return nil, nil
	}
	c := h.screen(meta, lang)
	var r *recruitRefusal
	if stderrors.As(err, &r) {
		return screens.RecruitRefusal(c, r.view), nil
	}
	if v, ok := asCompanyRefusal(err); ok {
		return screens.CompanyRefusal(c, v), nil
	}
	return nil, err
}

// reserve takes the idempotency key of a command that writes.
func (h *RecruitHandler) reserve(ctx context.Context, tx application.Tx, playerID string, meta envelope.Metadata) (bool, error) {
	key := idempotency.Derive(playerID, meta.RequestID, meta.IdempotencyKey)
	return tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

// player reads the player behind a command and the language to answer in.
func (h *RecruitHandler) player(ctx context.Context, tx application.Tx, meta envelope.Metadata, lang *string) (*application.Player, error) {
	p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
	if err != nil {
		return nil, err
	}
	*lang = RenderLanguage(meta, p)
	return p, nil
}

// managed reads and locks an active company the player recruits for: its
// owner or its manager (company.RightManageStaff).
func (h *RecruitHandler) managed(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
	companyID, code string,
) (*application.Company, error) {
	var (
		c   *application.Company
		err error
	)
	switch {
	case companyID != "":
		c, err = tx.Companies().Lock(ctx, companyID)
	case playercode.Valid(code):
		if c, err = tx.Companies().ByCode(ctx, code); err == nil {
			c, err = tx.Companies().Lock(ctx, c.ID)
		}
	default:
		return nil, refuseCompany(screens.CompanyRefusedNotFound, nil, snap)
	}
	if isSentinel(err, application.ErrCompanyNotFound) {
		return nil, refuseCompany(screens.CompanyRefusedNotFound, nil, snap)
	}
	if err != nil {
		return nil, err
	}
	role := company.RoleOf(p.ID, c.OwnerID, c.ManagerID)
	switch {
	case role == company.RoleNone:
		return nil, refuseCompany(screens.CompanyRefusedNotAllowed, c, snap)
	case !c.Active():
		return nil, refuseCompany(screens.CompanyRefusedDissolved, c, snap)
	case role.Check(company.RightManageStaff) != nil:
		return nil, refuseCompany(screens.CompanyRefusedNotAllowed, c, snap)
	}
	return c, nil
}

// recruitment reads the recruitment content, or a fault: the commands are
// not served without it.
func recruitment(snap *content.Snapshot) (content.RecruitmentDef, error) {
	def, ok := snap.Recruitment()
	if !ok {
		return def, errors.Internal(stderrors.New("handlers: the content has no recruitment section"))
	}
	return def, nil
}

// spendFree moves amount out of a company's free money under reason,
// referencing refType/refID, or refuses when the company cannot spare it.
func spendFree(ctx context.Context, tx application.Tx, c application.Company, snap *content.Snapshot,
	reason application.Reason, refType, refID, to string, amount int64, now time.Time,
) (string, error) {
	if amount <= 0 {
		return "", nil
	}
	b, acct, err := companyBooks(ctx, tx, c)
	if err != nil {
		return "", err
	}
	if b.Available().Minor() < amount {
		r := refuseRecruit(screens.RecruitRefusedFunds, &c, snap)
		r.view.Need, r.view.Have = amount, b.Available().Minor()
		return "", r
	}
	return postRef(ctx, tx.Ledger(), reason, refType, refID, acct.ID, to, money.FromMinor(amount), now)
}

// itoa64 is a number as a button carries it.
func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

// jsonOf is v as a scheduled action's payload.
func jsonOf(v any) ([]byte, error) { return json.Marshal(v) }
