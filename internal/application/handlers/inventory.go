package handlers

import (
	"context"
	stderrors "errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/crime"
	"github.com/mrjvadi/torncity/internal/domain/gametime"
	"github.com/mrjvadi/torncity/internal/domain/inventory"
	"github.com/mrjvadi/torncity/internal/domain/life"
	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/events"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// InventoryHandler serves what a player carries (internal/domain/inventory,
// items.yml): the bag, one good or piece in detail, using one, giving one to
// a friend standing at the same place, and dropping one.
//
// # Using a good
//
// Using a unit applies its effects to the player's condition — energy,
// health, happiness, nerve — each held inside its bar, and rests its
// cooldown group on the game clock. A second use inside the rest, a use that
// would change nothing, and a second press of the same button are refused or
// replayed; nothing is ever eaten twice by one press.
type InventoryHandler struct {
	uow     application.UnitOfWork
	ids     IDGenerator
	msgs    Translator
	content ContentSource
	cities  application.CityRepository
	scale   gametime.Scale
	// nerve is how a player's nerve bar refills, for a good that restores
	// nerve (crime.nerve_*).
	nerve crime.NerveRules

	pageSize       int
	idempotencyTTL time.Duration
	now            func() time.Time
	// hungerAlertCooldown, set by WithHungerAlert, is
	// notifications.hunger_alert_cooldown; zero still notices a hunger
	// crossing, just with no cooldown between repeats.
	hungerAlertCooldown time.Duration
}

// WithHungerAlert sets the real-time cooldown between two "you are hungry"
// instant notices to the same player (life_common.go's alertHunger).
func (h *InventoryHandler) WithHungerAlert(cooldown time.Duration) *InventoryHandler {
	h.hungerAlertCooldown = cooldown
	return h
}

// NewInventoryHandler wires the handler. A missing dependency is a wiring
// mistake and panics.
func NewInventoryHandler(uow application.UnitOfWork, ids IDGenerator, msgs Translator, source ContentSource,
	cities application.CityRepository, scale gametime.Scale, nerve crime.NerveRules, pageSize int,
	idempotencyTTL time.Duration, now func() time.Time,
) *InventoryHandler {
	if source == nil || cities == nil || ids == nil {
		panic("handlers: NewInventoryHandler requires content, cities and ids")
	}
	if scale.Validate() != nil || idempotencyTTL <= 0 {
		panic("handlers: NewInventoryHandler requires a game clock and an idempotency ttl")
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
	return &InventoryHandler{uow: uow, ids: ids, msgs: msgs, content: source, cities: cities, scale: scale,
		nerve: nerve, pageSize: pageSize, idempotencyTTL: idempotencyTTL, now: now}
}

// Requests.

// ItemRequest names a good (its code) or a piece (its serial); Nonce is a
// button's one-time token; Confirm the answer to "are you sure"; To a
// friend's player code.
type ItemRequest struct {
	Item    string `json:"item"`
	Nonce   string `json:"nonce,omitempty"`
	Confirm string `json:"confirm,omitempty"`
	To      string `json:"to,omitempty"`
}

func (h *InventoryHandler) screen(meta envelope.Metadata, lang string) screens.Context {
	return screens.Context{Msgs: h.msgs, Lang: lang, MessageID: editableMessageID(meta), Shared: meta.InGroup()}
}

// itemRefusal carries a refused request out of a unit of work.
type itemRefusal struct{ view screens.ItemRefusalView }

func (r *itemRefusal) Error() string { return "handlers: item refused: " + r.view.Kind }

func refuseItem(kind string, it screens.Named) *itemRefusal {
	return &itemRefusal{view: screens.ItemRefusalView{Kind: kind, Item: it}}
}

func (h *InventoryHandler) finish(meta envelope.Metadata, lang string, err error) (*presenter.Response, error) {
	var r *itemRefusal
	if stderrors.As(err, &r) {
		return screens.ItemRefusal(h.screen(meta, lang), r.view), nil
	}
	if v, ok := asNotHere(err); ok {
		return screens.NotHere(h.screen(meta, lang), v), nil
	}
	return nil, err
}

// nonce is a fresh one-time button token.
func (h *InventoryHandler) nonce() string {
	id := strings.ReplaceAll(h.ids.NewID(), "-", "")
	return id[:min(len(id), nonceLength)]
}

// Show handles inventory.show: what the player carries, a page at a time.
func (h *InventoryHandler) Show(ctx context.Context, meta envelope.Metadata, req PageRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	page := parsePage(req.Page)
	var view screens.InventoryView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		stacks, pieces, _, err := carried(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		names, err := designNames(ctx, tx, pieces)
		if err != nil {
			return err
		}
		lines := inventoryLines(snap, stacks, pieces, names)
		start, end, pages := pageWindow(len(lines), page, h.pageSize)
		view = screens.InventoryView{Lines: lines[start:end], Page: min(page, pages), Pages: pages, Total: len(lines)}
		esc, escPieces, err := tx.Items().Holdings(ctx, p.ID, application.HoldEscrow)
		if err != nil {
			return err
		}
		view.InEscrow = len(esc) + len(escPieces)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return screens.Inventory(h.screen(meta, lang), view), nil
}

// inventoryLines lists stacks and pieces in the content's order of goods.
func inventoryLines(snap *content.Snapshot, stacks []application.Stack, pieces []application.Piece,
	designs map[string]string,
) []screens.InventoryLine {
	order := map[string]int{}
	for i, d := range snap.Items() {
		order[d.Code] = i
	}
	var lines []screens.InventoryLine
	for _, s := range stacks {
		def, _ := snap.ItemDef(s.Item)
		lines = append(lines, screens.InventoryLine{Item: itemNamed(snap, s.Item), Category: def.Category, Qty: s.Qty})
	}
	for _, pc := range pieces {
		def, _ := snap.ItemDef(pc.Item)
		lines = append(lines, screens.InventoryLine{Item: itemNamed(snap, pc.Item), Category: def.Category, Qty: 1,
			Serial: pc.Serial, Quality: pc.Quality, UsesLeft: pc.UsesLeft, Durability: def.Durability,
			Design: designs[pc.DesignID]})
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if a, b := order[lines[i].Item.Code], order[lines[j].Item.Code]; a != b {
			return a < b
		}
		return lines[i].Serial < lines[j].Serial
	})
	return lines
}

// held finds what the player holds of a good (its code) or a piece (its
// serial), carried.
func held(ctx context.Context, tx application.Tx, playerID, ref string) (code string, qty int64, piece *application.Piece, err error) {
	stacks, pieces, _, err := carried(ctx, tx, playerID)
	if err != nil {
		return "", 0, nil, err
	}
	if isPieceRef(ref) {
		for i := range pieces {
			if pieces[i].Serial == ref {
				return pieces[i].Item, 1, &pieces[i], nil
			}
		}
		return "", 0, nil, nil
	}
	for _, s := range stacks {
		if s.Item == ref {
			return s.Item, s.Qty, nil, nil
		}
	}
	// A good held only as pieces: the first of them.
	for i := range pieces {
		if pieces[i].Item == ref {
			return pieces[i].Item, 1, &pieces[i], nil
		}
	}
	return "", 0, nil, nil
}

// Item handles inventory.item: one good or piece in detail, with what the
// player can do with it.
func (h *InventoryHandler) Item(ctx context.Context, meta envelope.Metadata, req ItemRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var view screens.ItemDetailView
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		code, qty, piece, err := held(ctx, tx, p.ID, req.Item)
		if err != nil {
			return err
		}
		if qty == 0 {
			return refuseItem(screens.ItemRefusedNotHeld, itemNamed(snap, req.Item))
		}
		view, err = h.detail(ctx, tx, snap, p, code, qty, piece)
		return err
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	return screens.ItemDetail(h.screen(meta, lang), view), nil
}

// detail builds a good's detail view.
func (h *InventoryHandler) detail(ctx context.Context, tx application.Tx, snap *content.Snapshot, p *application.Player,
	code string, qty int64, piece *application.Piece,
) (screens.ItemDetailView, error) {
	def, _ := snap.ItemDef(code)
	rules := def.Item()
	v := screens.ItemDetailView{
		Item: itemNamed(snap, code), Category: def.Category, Qty: qty, Worth: def.BasePrice,
		Usable: rules.Usable(), Tradeable: rules.Tradeable, Nonce: h.nonce(), Ref: code,
	}
	if piece != nil {
		v.Ref, v.Piece = piece.Serial, true
		v.Quality, v.UsesLeft, v.Durability = piece.Quality, piece.UsesLeft, def.Durability
	}
	for _, e := range def.Effects {
		v.Effects = append(v.Effects, screens.EffectLine{Target: e.Target, Op: e.Op, Value: e.Value})
	}
	if g := def.Gear; g != nil {
		v.Gear = &screens.GearLine{SuccessBPS: g.SuccessBPS, CatchBPS: g.CatchBPS, WitnessBPS: g.WitnessBPS,
			SolveBPS: g.SolveBPS, RewardBPS: g.RewardBPS, Nerve: g.Nerve, Confiscated: g.Confiscated}
		for _, c := range g.Categories {
			for _, cat := range snap.CrimeCategories() {
				if cat.Code == c {
					v.Gear.Categories = append(v.Gear.Categories, named(cat.Code, cat.Name))
				}
			}
		}
		for _, c := range g.Crimes {
			if cd, ok := snap.CrimeDef(c); ok {
				v.Gear.Crimes = append(v.Gear.Crimes, crimeNamed(cd))
			}
		}
	}
	if rules.Usable() && rules.Cooldown > 0 {
		last, err := tx.Items().LastUsed(ctx, p.ID, rules.Group())
		if err != nil {
			return v, err
		}
		v.CoolingFor = inventory.Cooling(rules, last, h.now(), h.scale)
		if v.CoolingFor > 0 {
			v.ReadyAt = h.now().Add(v.CoolingFor)
		}
		v.Cooldown = h.scale.RealWait(rules.Cooldown)
	}
	if rules.Tradeable {
		friends, err := h.friendsHere(ctx, tx, p)
		if err != nil {
			return v, err
		}
		v.GiveTo = friends
	}
	return v, nil
}

// friendsHere lists the player's friends standing where they stand: in the
// same city, not travelling, at the same place. A gift changes hands in
// person.
func (h *InventoryHandler) friendsHere(ctx context.Context, tx application.Tx, p *application.Player) ([]screens.Named, error) {
	if p.CityID == nil {
		return nil, nil
	}
	edges, err := tx.Friendships().List(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	here, err := tx.Places().Where(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	var out []screens.Named
	for _, e := range edges {
		if e.Status != friendAccepted {
			continue
		}
		f, err := tx.Players().GetByID(ctx, e.FriendPlayerID)
		if err != nil {
			if isSentinel(err, application.ErrPlayerNotFound) {
				continue
			}
			return nil, err
		}
		if f.CityID == nil || *f.CityID != *p.CityID {
			continue
		}
		if _, err := tx.Travels().Active(ctx, f.ID); err == nil {
			continue
		}
		there, err := tx.Places().Where(ctx, f.ID)
		if err != nil {
			return nil, err
		}
		if there != here {
			continue
		}
		out = append(out, screens.Named{Code: f.PublicCode, Name: f.DisplayName})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// reserve takes a command's idempotency key: the button's one-time token when
// it carries one, the update otherwise.
func (h *InventoryHandler) reserve(ctx context.Context, tx application.Tx, playerID string, meta envelope.Metadata, nonce string) (bool, error) {
	key := idempotency.Derive(playerID, meta.RequestID, meta.IdempotencyKey)
	if isNonce(nonce) {
		key = idempotency.Derive(playerID, meta.Command, nonce)
	}
	return tx.Idempotency().Reserve(ctx, string(key), playerID, meta.RequestID, meta.Command, h.idempotencyTTL)
}

// Use handles inventory.use: one unit of a good (or one use of a piece)
// applied to the player's condition.
func (h *InventoryHandler) Use(ctx context.Context, meta envelope.Metadata, req ItemRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	var (
		view     screens.ItemUsedView
		replayed bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
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
		// The stats row is the lock every change to the player's condition
		// takes; goods next.
		row, err := tx.Stats().EnsureDefaults(ctx, p.ID, defaultStats(p.ID, now))
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
		it := itemNamed(snap, req.Item)
		if qty == 0 {
			return refuseItem(screens.ItemRefusedNotHeld, it)
		}
		it = itemNamed(snap, code)
		def, _ := snap.ItemDef(code)
		rules := def.Item()
		if !rules.Usable() {
			return refuseItem(screens.ItemRefusedNotUsable, it)
		}
		last, err := tx.Items().LastUsed(ctx, p.ID, rules.Group())
		if err != nil {
			return err
		}
		regenerated, _ := regenerateEnergy(*row, now)
		// Food and drink touch the needs (docs/adr/0025): the life is caught
		// up first, so a meal lowers the hunger there is now.
		var lived *lifeNow
		for _, e := range rules.Effects {
			if inventory.NeedTarget(e.Target) && lived == nil {
				if lived, err = touchLife(ctx, tx, snap, h.scale, p, now, meta, h.hungerAlertCooldown, nil); err != nil {
					return err
				}
				if lived != nil {
					regenerated = *lived.stats
				}
			}
		}
		v := inventory.Vitals{
			Energy: regenerated.Energy, MaxEnergy: regenerated.MaxEnergy,
			Health: regenerated.Health, MaxHealth: regenerated.MaxHealth,
			Happiness: regenerated.Happiness, MaxHappiness: max(regenerated.Happiness, player.DefaultHappiness),
		}
		if lived != nil {
			v.Hunger, v.Sleep, v.Stress = life.Points(lived.needs.Hunger), life.Points(lived.needs.Sleep),
				life.Points(lived.needs.Stress)
			v.MaxNeed = life.MaxPoints
		}
		touchesNerve := false
		for _, e := range rules.Effects {
			touchesNerve = touchesNerve || e.Target == inventory.TargetNerve
		}
		var prof *application.CriminalProfile
		if touchesNerve {
			fresh := application.CriminalProfile{PlayerID: p.ID, Nerve: h.nerve.Max, NerveUpdatedAt: now,
				HeatUpdatedAt: now, CreatedAt: now, UpdatedAt: now}
			if prof, err = tx.Crime().Profile(ctx, p.ID, fresh); err != nil {
				return err
			}
			n := h.nerve.Regenerate(crime.Nerve{Current: prof.Nerve, UpdatedAt: prof.NerveUpdatedAt}, now)
			prof.Nerve, prof.NerveUpdatedAt = n.Current, n.UpdatedAt
			v.Nerve, v.MaxNerve = prof.Nerve, h.nerve.Max
		}
		after, ready, err := inventory.Use(rules, v, last, now, h.scale)
		switch {
		case stderrors.Is(err, inventory.ErrCoolingDown):
			r := refuseItem(screens.ItemRefusedCooling, it)
			r.view.Wait = inventory.Cooling(rules, last, now, h.scale)
			r.view.ReadyAt = now.Add(r.view.Wait)
			return r
		case stderrors.Is(err, inventory.ErrNoEffect):
			return refuseItem(screens.ItemRefusedNoEffect, it)
		case err != nil:
			return errors.Internal(err)
		}

		// One unit consumed, or one use of a piece.
		move := application.ItemMove{ID: h.ids.NewID(), Item: code, Qty: 1, From: p.ID, FromHolding: application.HoldCarried,
			Reason: application.ItemUsed, At: now}
		left := qty - 1
		if piece != nil {
			move.PieceID = piece.ID
			if def.Durability > 0 && piece.UsesLeft > 1 {
				if err := tx.Items().SetUses(ctx, piece.ID, piece.UsesLeft-1); err != nil {
					return err
				}
				left, move = 1, application.ItemMove{}
			}
		}
		if move.Item != "" {
			if err := tx.Items().Move(ctx, move); err != nil {
				return err
			}
		}
		if err := tx.Items().MarkUsed(ctx, p.ID, rules.Group(), now); err != nil {
			return err
		}
		next := regenerated
		next.Energy, next.Health, next.Happiness = after.Energy, after.Health, after.Happiness
		if err := tx.Stats().Save(ctx, next); err != nil {
			return err
		}
		if prof != nil {
			prof.Nerve, prof.UpdatedAt = after.Nerve, now
			if err := tx.Crime().SaveProfile(ctx, *prof); err != nil {
				return err
			}
		}
		if lived != nil {
			lived.row.SetNeeds(lived.needs.Change(after.Hunger-v.Hunger, after.Sleep-v.Sleep, after.Stress-v.Stress))
			if err := tx.Life().Save(ctx, *lived.row); err != nil {
				return err
			}
		}
		view = screens.ItemUsedView{Item: it, Left: left, ReadyAt: ready, Cooldown: h.scale.RealWait(rules.Cooldown)}
		for _, c := range []struct {
			target        string
			before, after int
			max           int
		}{
			{inventory.TargetEnergy, v.Energy, after.Energy, v.MaxEnergy},
			{inventory.TargetHealth, v.Health, after.Health, v.MaxHealth},
			{inventory.TargetHappiness, v.Happiness, after.Happiness, v.MaxHappiness},
			{inventory.TargetNerve, v.Nerve, after.Nerve, v.MaxNerve},
			{inventory.TargetHunger, v.Hunger, after.Hunger, v.MaxNeed},
			{inventory.TargetSleep, v.Sleep, after.Sleep, v.MaxNeed},
			{inventory.TargetStress, v.Stress, after.Stress, v.MaxNeed},
		} {
			if c.before != c.after {
				view.Changes = append(view.Changes, screens.VitalChange{Target: c.target, Before: c.before, After: c.after, Max: c.max})
			}
		}
		return appendItemEvent(ctx, tx, meta, "used", p.ID, map[string]any{
			"player_id": p.ID, "item": code, "content_version": snap.Version(),
		})
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	if replayed {
		return h.Show(ctx, meta, PageRequest{})
	}
	return screens.ItemUsed(h.screen(meta, lang), view), nil
}

// Give handles inventory.give: one unit or piece to a friend standing at the
// same place. Without a friend named it shows who can receive it.
func (h *InventoryHandler) Give(ctx context.Context, meta envelope.Metadata, req ItemRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	if strings.TrimSpace(req.To) == "" {
		return h.Item(ctx, meta, req)
	}
	var (
		view     screens.ItemGivenView
		replayed bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
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
		friends, err := h.friendsHere(ctx, tx, p)
		if err != nil {
			return err
		}
		var to *screens.Named
		for i := range friends {
			if strings.EqualFold(friends[i].Code, req.To) {
				to = &friends[i]
			}
		}
		if to == nil {
			return refuseItem(screens.ItemRefusedNotTogether, itemNamed(snap, req.Item))
		}
		friendID := ""
		edges, err := tx.Friendships().List(ctx, p.ID)
		if err != nil {
			return err
		}
		for _, e := range edges {
			if f, err := tx.Players().GetByID(ctx, e.FriendPlayerID); err == nil && f.PublicCode == to.Code {
				friendID = f.ID
			}
		}
		if friendID == "" {
			return refuseItem(screens.ItemRefusedNotTogether, itemNamed(snap, req.Item))
		}
		// Both holders' goods, in id order, so two gifts crossing cannot
		// deadlock.
		first, second := p.ID, friendID
		if second < first {
			first, second = second, first
		}
		if err := tx.Items().LockOwner(ctx, first); err != nil {
			return err
		}
		if err := tx.Items().LockOwner(ctx, second); err != nil {
			return err
		}
		code, qty, piece, err := held(ctx, tx, p.ID, req.Item)
		if err != nil {
			return err
		}
		if qty == 0 {
			return refuseItem(screens.ItemRefusedNotHeld, itemNamed(snap, req.Item))
		}
		def, _ := snap.ItemDef(code)
		if !def.Item().Tradeable {
			return refuseItem(screens.ItemRefusedNotTradeable, itemNamed(snap, code))
		}
		move := application.ItemMove{ID: h.ids.NewID(), Item: code, Qty: 1, From: p.ID, FromHolding: application.HoldCarried,
			To: friendID, ToHolding: application.HoldCarried, Reason: application.ItemGift,
			ReferenceType: "players", ReferenceID: friendID, At: now}
		if piece != nil {
			move.PieceID = piece.ID
		}
		if err := tx.Items().Move(ctx, move); err != nil {
			if isSentinel(err, application.ErrNotEnoughItems) || isSentinel(err, application.ErrPieceNotFound) {
				return refuseItem(screens.ItemRefusedNotHeld, itemNamed(snap, code))
			}
			return err
		}
		view = screens.ItemGivenView{Item: itemNamed(snap, code), To: *to}
		return appendItemEvent(ctx, tx, meta, "given", friendID, map[string]any{
			"player_id": friendID, "from_name": shownName(p), "from_code": p.PublicCode, "item": code,
			"item_name": def.Name, "content_version": snap.Version(),
		})
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	if replayed {
		return h.Show(ctx, meta, PageRequest{})
	}
	return screens.ItemGiven(h.screen(meta, lang), view), nil
}

// Drop handles inventory.drop: throwing away one unit or piece. Without the
// confirmation it asks first.
func (h *InventoryHandler) Drop(ctx context.Context, meta envelope.Metadata, req ItemRequest) (*presenter.Response, error) {
	if err := validatePlayerMeta(meta); err != nil {
		return nil, err
	}
	snap := h.content.Current()
	lang := meta.Language
	confirmed := req.Confirm == screens.DropConfirmation
	var (
		view     screens.ItemDroppedView
		ask      bool
		replayed bool
	)
	err := h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		p, err := tx.Players().GetByTelegramUserID(ctx, meta.TelegramUserID)
		if err != nil {
			return err
		}
		lang = RenderLanguage(meta, p)
		if confirmed {
			fresh, err := h.reserve(ctx, tx, p.ID, meta, req.Nonce)
			if err != nil {
				return err
			}
			if !fresh {
				replayed = true
				return nil
			}
		}
		if err := tx.Items().LockOwner(ctx, p.ID); err != nil {
			return err
		}
		code, qty, piece, err := held(ctx, tx, p.ID, req.Item)
		if err != nil {
			return err
		}
		if qty == 0 {
			return refuseItem(screens.ItemRefusedNotHeld, itemNamed(snap, req.Item))
		}
		view = screens.ItemDroppedView{Item: itemNamed(snap, code), Ref: req.Item, Nonce: h.nonce()}
		if !confirmed {
			ask = true
			return nil
		}
		move := application.ItemMove{ID: h.ids.NewID(), Item: code, Qty: 1, From: p.ID, FromHolding: application.HoldCarried,
			Reason: application.ItemDropped, At: h.now()}
		if piece != nil {
			move.PieceID = piece.ID
		}
		return tx.Items().Move(ctx, move)
	})
	if resp, ferr := h.finish(meta, lang, err); resp != nil || ferr != nil {
		return resp, ferr
	}
	switch {
	case replayed:
		return h.Show(ctx, meta, PageRequest{})
	case ask:
		return screens.DropConfirm(h.screen(meta, lang), view), nil
	}
	return screens.ItemDropped(h.screen(meta, lang), view), nil
}

// appendItemEvent writes an inventory event to the outbox in the command's
// transaction.
func appendItemEvent(ctx context.Context, tx application.Tx, meta envelope.Metadata, name, aggregateID string, payload map[string]any) error {
	ev, err := events.New("inventory."+name, "player", aggregateID, payload)
	if err != nil {
		return err
	}
	return tx.Outbox().Append(ctx, application.OutboxRecord{
		EventID: ev.ID, Subject: subjects.Event("inventory", name), Metadata: meta, Payload: ev.Payload,
	})
}

// parseQty reads a quantity from a button, at least one.
func parseQty(raw string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// designNames reads the names of the designs the pieces were made from: a
// phone a company made reads by its make in the bag.
func designNames(ctx context.Context, tx application.Tx, pieces []application.Piece) (map[string]string, error) {
	out := map[string]string{}
	for _, pc := range pieces {
		if pc.DesignID == "" {
			continue
		}
		if _, seen := out[pc.DesignID]; seen {
			continue
		}
		d, err := tx.Production().DesignByID(ctx, pc.DesignID)
		switch {
		case err == nil:
			out[pc.DesignID] = d.Name
		case isSentinel(err, application.ErrDesignNotFound):
			out[pc.DesignID] = ""
		default:
			return nil, err
		}
	}
	return out, nil
}
