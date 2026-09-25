package handlers

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/bank"
	"github.com/mrjvadi/torncity/internal/domain/company"
	"github.com/mrjvadi/torncity/internal/domain/faction"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/place"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/shared/money"
	"github.com/mrjvadi/torncity/internal/shared/playercode"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// FactionRules is the tuning of factions (config factions.*, and the bank's
// amount bounds for money put in or taken out).
type FactionRules struct {
	NameMin, NameMax int
	MaxMembers       int
	MaxPending       int
	ListSize         int
	Limits           bank.Limits
}

// FactionsHandler serves factions (docs/adr/0023-health-missions-factions.md):
// the factions of a city and a faction's page; founding one at city hall;
// its members, ranks, invitations and applications; its bank; the Telegram
// group it is played in; and its organised crimes, which a crew of its
// members commits together through the crime engine, settled once from the
// scheduler.
//
// # Money
//
// A faction's bank is a faction_treasury account. The founder pays the
// founding fee to the city (faction_registration); members put money in
// (faction_deposit) and those whose rank allows take it out to their own
// bank (faction_withdrawal); an organised crime's take enters from the NPC
// economy (crime_proceeds, under the crime's cap and the economy's daily
// cap), into the crew's cash and the bank's cut into its treasury.
type FactionsHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	search  application.PlayerSearch
	scale   gametime.Scale
	rules   FactionRules
	// crime is the crime engine: an organised crime uses its nerve, heat,
	// justice and jail.
	crime *CrimeHandler

	idempotencyTTL time.Duration
	now            func() time.Time
}

// NewFactionsHandler wires the handler. A missing dependency or unusable
// rules are a wiring mistake and panic.
func NewFactionsHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, search application.PlayerSearch, scale gametime.Scale, rules FactionRules,
	crime *CrimeHandler, idempotencyTTL time.Duration, now func() time.Time,
) *FactionsHandler {
	if uow == nil || ids == nil || source == nil || cities == nil || search == nil || crime == nil {
		panic("handlers: NewFactionsHandler requires a unit of work, ids, content, cities, a player search and the crime engine")
	}
	if scale.Validate() != nil || idempotencyTTL <= 0 || rules.NameMin < 1 || rules.NameMax < rules.NameMin ||
		rules.MaxMembers < 2 || rules.MaxPending < 1 || rules.ListSize < 1 || rules.Limits.Max.Minor() <= 0 {
		panic("handlers: NewFactionsHandler requires a game clock, an idempotency ttl and valid faction rules")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if msgs == nil {
		msgs = keyTranslator{}
	}
	return &FactionsHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, search: search,
		scale: scale, rules: rules, crime: crime, idempotencyTTL: idempotencyTTL, now: now}
}

// FactionCmd is the payload of the faction commands; which fields a command
// reads is its own. The names are those internal/gateway/routing gives the
// arguments.
type FactionCmd struct {
	Code    string `json:"code,omitempty"`
	Name    string `json:"name,omitempty"`
	Method  string `json:"method,omitempty"`
	Amount  string `json:"amount,omitempty"`
	To      string `json:"to,omitempty"`
	Player  string `json:"player,omitempty"`
	Rank    string `json:"rank,omitempty"`
	No      string `json:"no,omitempty"`
	Verdict string `json:"verdict,omitempty"`
	Confirm string `json:"confirm,omitempty"`
	Crime   string `json:"crime,omitempty"`
	Page    string `json:"page,omitempty"`
}

// FactionScheduledRequest is the scheduler's payload for faction.resolve.
type FactionScheduledRequest = CrimeScheduledRequest

// confirmed reports whether a press confirms.
func (r FactionCmd) confirmed() bool { return strings.TrimSpace(r.Confirm) == screens.FactionYes }

func (h *FactionsHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// factionRefusal carries a refused faction command out of a unit of work.
type factionRefusal struct{ view screens.FactionRefusalView }

func (r *factionRefusal) Error() string { return "handlers: faction refused: " + r.view.Kind }

func refuseFaction(kind string) *factionRefusal {
	return &factionRefusal{view: screens.FactionRefusalView{Kind: kind}}
}

// finish turns a refusal into its screen.
func (h *FactionsHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	if err == nil {
		return nil, nil
	}
	c := h.screen(meta, lang)
	var r *factionRefusal
	if stderrors.As(err, &r) {
		return screens.FactionRefusal(c, r.view), nil
	}
	if v, ok := asNotHere(err); ok {
		return screens.NotHere(c, v), nil
	}
	if v, ok := asDeclined(err, screens.PaymentDeclinedView{}); ok {
		return screens.PaymentDeclined(c, v), nil
	}
	return nil, err
}

// reserve takes the idempotency key of a command that writes.
func (h *FactionsHandler) reserve(ctx context.Context, tx application.Tx, playerID string, meta envelope.Metadata) (bool, error) {
	key := idempotency.Derive(playerID, meta.RequestID, meta.IdempotencyKey)
	return tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

// factionDef is the content's faction section, or a refusal when it has none.
func factionDef(snap *content.Snapshot) (content.FactionDef, error) {
	def, ok := snap.Faction()
	if !ok {
		return def, refuseFaction(screens.FactionRefusedNone)
	}
	return def, nil
}

// factionRef names a faction for a screen.
func factionRef(f application.Faction) screens.FactionRef {
	return screens.FactionRef{Code: f.Code, Name: f.Name}
}

// member is a player's membership with its faction, read together.
type member struct {
	faction *application.Faction
	m       application.FactionMember
	rank    faction.Rank
}

// membership reads the player's membership and locks their faction; a player
// in none is refused. lock takes the faction's row lock.
func (h *FactionsHandler) membership(ctx context.Context, tx application.Tx, playerID string, lock bool) (*member, error) {
	m, err := tx.Factions().Membership(ctx, playerID)
	if isSentinel(err, application.ErrNotInFaction) {
		return nil, refuseFaction(screens.FactionRefusedNotMember)
	}
	if err != nil {
		return nil, err
	}
	var f *application.Faction
	if lock {
		f, err = tx.Factions().Lock(ctx, m.FactionID)
	} else {
		f, err = tx.Factions().ByID(ctx, m.FactionID)
	}
	if err != nil {
		return nil, err
	}
	if lock {
		// Re-read under the faction's lock: a kick may have landed.
		if m, err = tx.Factions().Membership(ctx, playerID); err != nil {
			if isSentinel(err, application.ErrNotInFaction) {
				return nil, refuseFaction(screens.FactionRefusedNotMember)
			}
			return nil, err
		}
		if m.FactionID != f.ID {
			return nil, refuseFaction(screens.FactionRefusedNotMember)
		}
	}
	return &member{faction: f, m: *m, rank: faction.Rank(m.Rank)}, nil
}

// may refuses what the member's rank does not allow.
func (mb *member) may(def content.FactionDef, right faction.Right) error {
	if !def.Charter().Can(mb.rank, right) {
		return refuseFaction(screens.FactionRefusedRank)
	}
	return nil
}

// appendFactionEvent writes a faction event to the outbox.
func appendFactionEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata, name, aggregateID string,
	payload map[string]any,
) error {
	return appendDomainEvent(ctx, tx, meta, "faction", name, aggregateID, payload)
}

// groupFields are the event fields that address a faction's group line.
func groupFields(f application.Faction, payload map[string]any) map[string]any {
	payload["faction_code"], payload["faction_name"] = f.Code, f.Name
	if f.ChatID != 0 {
		payload["chat_id"], payload["bot_id"], payload["chat_language"] = f.ChatID, f.ChatBotID, f.ChatLanguage
	}
	return payload
}

// List handles faction.list: the factions of the player's city.
func (h *FactionsHandler) List(ctx context.Context, meta envelope.Metadata, req FactionCmd) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.FactionListView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		view.Fee = def.FoundingFee
		cityID := ""
		if p.CityID != nil {
			cityID = *p.CityID
			city, err := h.cities.ByID(ctx, cityID)
			if err != nil {
				return err
			}
			view.CityCode, view.City = city.Code, city.Name
		}
		lines, err := tx.Factions().List(ctx, cityID, h.rules.ListSize)
		if err != nil {
			return err
		}
		for _, l := range lines {
			view.Factions = append(view.Factions, screens.FactionLine{Ref: factionRef(l.Faction), Members: l.Members})
		}
		if m, err := tx.Factions().Membership(ctx, p.ID); err == nil {
			f, err := tx.Factions().ByID(ctx, m.FactionID)
			if err != nil {
				return err
			}
			mine := factionRef(*f)
			view.Mine = &mine
		} else if !isSentinel(err, application.ErrNotInFaction) {
			return err
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.FactionList(h.screen(meta, lang), view), nil
}

// View handles faction.view: a faction's public page — its city, leader,
// members and ranks.
func (h *FactionsHandler) View(ctx context.Context, meta envelope.Metadata, req FactionCmd) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.FactionPageView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		if _, err := factionDef(snap); err != nil {
			return err
		}
		f, err := h.byCode(ctx, tx, req.Code)
		if err != nil {
			return err
		}
		if view, err = h.page(ctx, tx, *f, p.ID); err != nil {
			return err
		}
		// Read in a group, its own member whose rank links may tie the
		// faction to that group from here.
		if meta.InGroup() && view.Mine && f.ChatID != meta.TelegramChatID {
			if def, ok := snap.Faction(); ok {
				if m, err := tx.Factions().Membership(ctx, p.ID); err == nil && m.FactionID == f.ID {
					view.CanLink = def.Charter().Can(faction.Rank(m.Rank), faction.Link)
				}
			}
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.FactionPage(h.screen(meta, lang), view), nil
}

// byCode reads an active faction by its public code.
func (h *FactionsHandler) byCode(ctx context.Context, tx application.Tx, code string) (*application.Faction, error) {
	code = playercode.Normalize(code)
	if !playercode.Valid(code) {
		return nil, refuseFaction(screens.FactionRefusedNotFound)
	}
	f, err := tx.Factions().ByCode(ctx, code)
	if isSentinel(err, application.ErrFactionNotFound) {
		return nil, refuseFaction(screens.FactionRefusedNotFound)
	}
	if err != nil {
		return nil, err
	}
	if !f.Active() {
		return nil, refuseFaction(screens.FactionRefusedNotFound)
	}
	return f, nil
}

// page reads a faction's public page for viewer.
func (h *FactionsHandler) page(ctx context.Context, tx application.Tx, f application.Faction, viewer string) (screens.FactionPageView, error) {
	v := screens.FactionPageView{Ref: factionRef(f), Linked: f.ChatID != 0}
	city, err := h.cities.ByID(ctx, f.CityID)
	if err != nil {
		return v, err
	}
	v.CityCode, v.City = city.Code, city.Name
	members, err := tx.Factions().Members(ctx, f.ID)
	if err != nil {
		return v, err
	}
	for _, m := range members {
		who, err := playerNamed(ctx, tx, m.PlayerID)
		if err != nil {
			return v, err
		}
		v.Members = append(v.Members, screens.FactionMemberLine{Player: who, Rank: m.Rank})
		if m.PlayerID == viewer {
			v.Mine = true
		}
	}
	if !v.Mine {
		if _, err := tx.Factions().Membership(ctx, viewer); isSentinel(err, application.ErrNotInFaction) {
			v.CanApply = len(members) < h.rules.MaxMembers
		} else if err != nil {
			return v, err
		}
	}
	return v, nil
}

// Found handles faction.found: founding a faction at city hall. Without a
// method it shows the fee and the ways to pay, each asking for the name;
// with one and a name it founds it, paying the fee to the city.
func (h *FactionsHandler) Found(ctx context.Context, meta envelope.Metadata, req FactionCmd) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	method, chosen, err := chosenMethod(req.Method)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	var (
		intro    *screens.FactionFoundView
		founded  *screens.FactionFoundedView
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		if chosen && name != "" {
			fresh, err := h.reserve(ctx, tx, p.ID, meta)
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		now := h.now()
		if _, err := tx.Factions().Membership(ctx, p.ID); err == nil {
			return refuseFaction(screens.FactionRefusedAlreadyMember)
		} else if !isSentinel(err, application.ErrNotInFaction) {
			return err
		}
		w, err := locate(ctx, tx, h.cities, snap, p)
		if err != nil {
			return err
		}
		if w.city == nil {
			return application.ErrCityNotFound
		}
		fee := money.FromMinor(def.FoundingFee)
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		plan := wallet.Plan(fee, snap.Accepts(content.ServiceFaction))
		if !chosen || name == "" {
			choice := paymentChoice(plan, wallet)
			intro = &screens.FactionFoundView{CityCode: w.city.Code, City: w.city.Name, Fee: def.FoundingFee, Payment: choice,
				NameMin: h.rules.NameMin, NameMax: h.rules.NameMax}
			return nil
		}
		if err := RefuseDetained(ctx, tx, p.ID, now); err != nil {
			return err
		}
		if err := needService(w, snap, place.ServiceCityHall, h.scale, now); err != nil {
			return err
		}
		clean, err := company.CheckName(name, company.NameRules{MinRunes: h.rules.NameMin, MaxRunes: h.rules.NameMax,
			Reserved: snap.CompanyReservedNames()})
		if err != nil {
			r := refuseFaction(screens.FactionRefusedName)
			r.view.Min, r.view.Max = h.rules.NameMin, h.rules.NameMax
			return r
		}
		code, err := h.freeCode(ctx, tx)
		if err != nil {
			return err
		}
		f := application.Faction{ID: h.ids.NewID(), Code: code, Name: clean, NameKey: company.NameKey(clean),
			CityID: w.city.ID, LeaderID: p.ID, Status: application.FactionActive, FoundingFee: fee.Minor(),
			ContentVersion: snap.Version(), FoundedAt: now, UpdatedAt: now}
		if err := tx.Factions().Create(ctx, f, application.FactionMember{PlayerID: p.ID, FactionID: f.ID,
			Rank: string(faction.Leader), JoinedAt: now}); err != nil {
			switch {
			case isSentinel(err, application.ErrFactionNameTaken):
				return refuseFaction(screens.FactionRefusedNameTaken)
			case isSentinel(err, application.ErrAlreadyInFaction):
				return refuseFaction(screens.FactionRefusedAlreadyMember)
			}
			return err
		}
		if !fee.IsZero() {
			if err := checkMethod(plan, method, wallet, "faction.button.found", screens.AddrFactionFound); err != nil {
				return err
			}
			treasury, err := tx.Ledger().AccountFor(ctx, application.AccountCityTreasury, w.city.ID)
			if err != nil {
				return err
			}
			txID, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{
				Method: method, Accepted: plan.Accepted, Reason: application.ReasonFactionRegistration,
				ReferenceType: application.FactionReference, ReferenceID: f.ID,
				To: []application.LedgerEntry{{AccountID: treasury.ID, Amount: fee}}, CreatedAt: now,
			})
			if err != nil {
				if stderrors.Is(err, application.ErrPaymentDeclined) {
					return declined(plan, wallet, "faction.button.found", screens.AddrFactionFound)
				}
				return err
			}
			f.RegistrationTxID = txID
			if err := tx.Factions().Save(ctx, f); err != nil {
				return err
			}
		}
		// The bank exists from the first moment, at zero.
		if _, err := tx.Ledger().AccountFor(ctx, application.AccountFactionTreasury, f.ID); err != nil {
			return err
		}
		if err := tx.Factions().WithdrawPendingOf(ctx, p.ID, now); err != nil {
			return err
		}
		founded = &screens.FactionFoundedView{Ref: factionRef(f), CityCode: w.city.Code, City: w.city.Name,
			Fee: fee.Minor(), Method: string(method)}
		return appendFactionEvent(ctx, tx, meta, "founded", f.ID, map[string]any{
			"faction_id": f.ID, "faction_code": f.Code, "faction_name": f.Name, "city_id": f.CityID,
			"player_id": p.ID, "player_name": shownName(p), "fee": fee.Minor(),
		})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	switch {
	case replayed:
		return h.Mine(ctx, meta)
	case intro != nil:
		return screens.FactionFound(h.screen(meta, lang), *intro), nil
	case founded != nil:
		return screens.FactionFounded(h.screen(meta, lang), *founded), nil
	}
	return nil, errors.Internal(stderrors.New("handlers: founding a faction produced nothing"))
}

// freeCode draws a public code no faction has.
func (h *FactionsHandler) freeCode(ctx context.Context, tx application.Tx) (string, error) {
	for range 16 {
		code, err := playercode.New()
		if err != nil {
			return "", errors.Internal(err)
		}
		taken, err := tx.Factions().CodeTaken(ctx, code)
		if err != nil {
			return "", err
		}
		if !taken {
			return code, nil
		}
	}
	return "", errors.Internal(stderrors.New("handlers: no free faction code after 16 draws"))
}

// Mine handles faction.mine: the player's faction — their rank, the bank,
// what is waiting, the organised crime under way, and what their rank may
// do. A player in none is sent to the list.
func (h *FactionsHandler) Mine(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view screens.FactionHomeView
		none bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		mb, err := h.membership(ctx, tx, p.ID, false)
		var r *factionRefusal
		if stderrors.As(err, &r) && r.view.Kind == screens.FactionRefusedNotMember {
			none = true
			return nil
		}
		if err != nil {
			return err
		}
		view, err = h.home(ctx, tx, snap, def, mb)
		return err
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if none {
		return h.List(ctx, meta, FactionCmd{})
	}
	return screens.FactionHome(h.screen(meta, lang), view), nil
}

// home reads a member's faction screen.
func (h *FactionsHandler) home(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.FactionDef,
	mb *member,
) (screens.FactionHomeView, error) {
	f := *mb.faction
	v := screens.FactionHomeView{Ref: factionRef(f), Rank: string(mb.rank), Linked: f.ChatID != 0}
	charter := def.Charter()
	for _, r := range faction.AllRights() {
		if charter.Can(mb.rank, r) {
			v.Rights = append(v.Rights, string(r))
		}
	}
	city, err := h.cities.ByID(ctx, f.CityID)
	if err != nil {
		return v, err
	}
	v.CityCode, v.City = city.Code, city.Name
	members, err := tx.Factions().Members(ctx, f.ID)
	if err != nil {
		return v, err
	}
	v.Members, v.MaxMembers = len(members), h.rules.MaxMembers
	acct, err := tx.Ledger().AccountFor(ctx, application.AccountFactionTreasury, f.ID)
	if err != nil {
		return v, err
	}
	v.Bank = acct.Balance.Minor()
	pending, err := tx.Factions().Pending(ctx, f.ID)
	if err != nil {
		return v, err
	}
	for _, q := range pending {
		if q.Kind == application.RequestApply {
			v.Applications++
		}
	}
	op, err := tx.Factions().OpenOperation(ctx, f.ID)
	switch {
	case err == nil:
		line, err := h.operationLine(ctx, tx, snap, def, *op, h.now())
		if err != nil {
			return v, err
		}
		v.Operation = &line
	case !isSentinel(err, application.ErrNoOperation):
		return v, err
	}
	return v, nil
}

// Link handles faction.link, sent in a group: the leader, or a member whose
// rank allows, ties the faction to this group; its news is posted here.
func (h *FactionsHandler) Link(ctx context.Context, meta envelope.Metadata, req FactionCmd) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.FactionLinkedView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		if !meta.InGroup() || meta.TelegramChatID >= 0 {
			return refuseFaction(screens.FactionRefusedNotGroup)
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		mb, err := h.membership(ctx, tx, p.ID, true)
		if err != nil {
			return err
		}
		if err := mb.may(def, faction.Link); err != nil {
			return err
		}
		f := mb.faction
		view = screens.FactionLinkedView{Ref: factionRef(*f)}
		if !fresh || f.ChatID == meta.TelegramChatID {
			return nil
		}
		if other, err := tx.Factions().ByChat(ctx, meta.TelegramChatID); err == nil && other.ID != f.ID {
			return refuseFaction(screens.FactionRefusedGroupTaken)
		} else if err != nil && !isSentinel(err, application.ErrFactionNotFound) {
			return err
		}
		f.ChatID, f.ChatBotID, f.ChatLanguage, f.UpdatedAt = meta.TelegramChatID, meta.BotID, lang, h.now()
		if err := tx.Factions().Save(ctx, *f); err != nil {
			if isSentinel(err, application.ErrFactionNameTaken) {
				return refuseFaction(screens.FactionRefusedGroupTaken)
			}
			return err
		}
		return appendFactionEvent(ctx, tx, meta, "linked", f.ID, groupFields(*f, map[string]any{
			"player_id": p.ID, "player_name": shownName(p)}))
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.FactionLinked(h.screen(meta, lang), view), nil
}

// parseAmount reads a typed amount inside the bank's bounds.
func (h *FactionsHandler) parseAmount(raw string) (money.Amount, error) {
	amount, err := bank.ParseAmount(raw)
	if err != nil {
		return money.Amount{}, application.ErrInvalidMoneyAmount
	}
	switch err := h.rules.Limits.Check(amount); {
	case stderrors.Is(err, bank.ErrBelowMinimum):
		return money.Amount{}, application.ErrAmountBelowMinimum.WithDetail("min", h.rules.Limits.Min.Minor())
	case stderrors.Is(err, bank.ErrAboveMaximum):
		return money.Amount{}, application.ErrAmountAboveMaximum.WithDetail("max", h.rules.Limits.Max.Minor())
	case err != nil:
		return money.Amount{}, application.ErrInvalidMoneyAmount
	}
	return amount, nil
}

// Bank handles faction.bank: the faction's bank, and the ways in and out
// the member's rank allows.
func (h *FactionsHandler) Bank(ctx context.Context, meta envelope.Metadata) (*presenter.Response, error) {
	return h.bankWith(ctx, meta, nil)
}

func (h *FactionsHandler) bankWith(ctx context.Context, meta envelope.Metadata, done *screens.FactionMoneyDone) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.FactionBankView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		mb, err := h.membership(ctx, tx, p.ID, false)
		if err != nil {
			return err
		}
		acct, err := tx.Ledger().AccountFor(ctx, application.AccountFactionTreasury, mb.faction.ID)
		if err != nil {
			return err
		}
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		charter := def.Charter()
		view = screens.FactionBankView{Ref: factionRef(*mb.faction), Balance: acct.Balance.Minor(),
			CanDeposit: charter.Can(mb.rank, faction.Deposit), CanWithdraw: charter.Can(mb.rank, faction.Withdraw),
			Cash: wallet.Cash.Balance.Minor(), BankBalance: wallet.Bank.Balance.Minor(), Done: done,
			Min: h.rules.Limits.Min.Minor(), Max: h.rules.Limits.Max.Minor()}
		for _, m := range snap.Accepts(content.ServiceFaction) {
			view.Methods = append(view.Methods, string(m))
		}
		return nil
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	return screens.FactionBank(h.screen(meta, lang), view), nil
}

// Deposit handles faction.deposit: money from the member's cash or card
// into the faction's bank.
func (h *FactionsHandler) Deposit(ctx context.Context, meta envelope.Metadata, req FactionCmd) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	method, chosen, err := chosenMethod(req.Method)
	if err != nil {
		return nil, err
	}
	if !chosen || strings.TrimSpace(req.Amount) == "" {
		return h.Bank(ctx, meta)
	}
	amount, err := h.parseAmount(req.Amount)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		done     screens.FactionMoneyDone
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}
		mb, err := h.membership(ctx, tx, p.ID, true)
		if err != nil {
			return err
		}
		if err := mb.may(def, faction.Deposit); err != nil {
			return err
		}
		treasury, err := tx.Ledger().AccountFor(ctx, application.AccountFactionTreasury, mb.faction.ID)
		if err != nil {
			return err
		}
		wallet, err := application.OpenWallet(ctx, tx.Ledger(), p.ID)
		if err != nil {
			return err
		}
		plan := wallet.Plan(amount, snap.Accepts(content.ServiceFaction))
		if err := checkMethod(plan, method, wallet, "faction.button.bank", screens.AddrFactionBank); err != nil {
			return err
		}
		if _, err := wallet.Pay(ctx, tx.Ledger(), application.Charge{
			Method: method, Accepted: plan.Accepted, Reason: application.ReasonFactionDeposit,
			ReferenceType: application.FactionReference, ReferenceID: mb.faction.ID,
			To: []application.LedgerEntry{{AccountID: treasury.ID, Amount: amount}}, CreatedAt: h.now(),
		}); err != nil {
			if stderrors.Is(err, application.ErrPaymentDeclined) {
				return declined(plan, wallet, "faction.button.bank", screens.AddrFactionBank)
			}
			return err
		}
		done = screens.FactionMoneyDone{Deposit: true, Amount: amount.Minor(), Method: string(method)}
		return appendFactionEvent(ctx, tx, meta, "deposited", mb.faction.ID, map[string]any{
			"faction_id": mb.faction.ID, "player_id": p.ID, "amount": amount.Minor(), "method": string(method)})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replayed {
		return h.Bank(ctx, meta)
	}
	return h.bankWith(ctx, meta, &done)
}

// Withdraw handles faction.withdraw: money from the faction's bank to the
// member's own bank, for a rank that may.
func (h *FactionsHandler) Withdraw(ctx context.Context, meta envelope.Metadata, req FactionCmd) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Amount) == "" {
		return h.Bank(ctx, meta)
	}
	amount, err := h.parseAmount(req.Amount)
	if err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		done     screens.FactionMoneyDone
		replayed bool
	)
	err = h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		def, err := factionDef(snap)
		if err != nil {
			return err
		}
		fresh, err := h.reserve(ctx, tx, p.ID, meta)
		if err != nil {
			return err
		}
		if !fresh {
			replayed = true
			return nil
		}
		mb, err := h.membership(ctx, tx, p.ID, true)
		if err != nil {
			return err
		}
		if err := mb.may(def, faction.Withdraw); err != nil {
			return err
		}
		treasury, err := tx.Ledger().AccountFor(ctx, application.AccountFactionTreasury, mb.faction.ID)
		if err != nil {
			return err
		}
		if treasury.Balance.Minor() < amount.Minor() {
			r := refuseFaction(screens.FactionRefusedBankShort)
			r.view.Amount, r.view.Balance = amount.Minor(), treasury.Balance.Minor()
			return r
		}
		to, err := tx.Ledger().AccountFor(ctx, application.AccountPlayerBank, p.ID)
		if err != nil {
			return err
		}
		if _, err := tx.Ledger().Post(ctx, application.LedgerTransaction{
			Reason: application.ReasonFactionWithdrawal, ReferenceType: application.FactionReference,
			ReferenceID: mb.faction.ID, CreatedAt: h.now(),
			Entries: []application.LedgerEntry{{AccountID: treasury.ID, Amount: money.FromMinor(-amount.Minor())},
				{AccountID: to.ID, Amount: amount}},
		}); err != nil {
			if isSentinel(err, application.ErrInsufficientFunds) {
				return refuseFaction(screens.FactionRefusedBankShort)
			}
			return err
		}
		done = screens.FactionMoneyDone{Amount: amount.Minor()}
		return appendFactionEvent(ctx, tx, meta, "withdrawn", mb.faction.ID, map[string]any{
			"faction_id": mb.faction.ID, "player_id": p.ID, "amount": amount.Minor()})
	})
	if resp, err := h.finish(meta, lang, err); resp != nil || err != nil {
		return resp, err
	}
	if replayed {
		return h.Bank(ctx, meta)
	}
	return h.bankWith(ctx, meta, &done)
}
