package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// What a player carries: the bag, one good or piece in detail, using it,
// giving it to a friend nearby, dropping it.
//
// A good is content keyed on a code; its name is item_name.<code> in the
// catalogue, falling back to the authored name, and its group
// item_category.<category>. A piece's serial travels in a button's address
// and is never shown: a player tells two phones apart by their quality and
// what is left of them.

// Callback addresses of the bag.
const (
	AddrInventory = "inventory:show"
	AddrItem      = "inventory:item"
	AddrItemUse   = "inventory:use"
	AddrItemGive  = "inventory:give"
	AddrItemDrop  = "inventory:drop"
)

// DropConfirmation is the argument that turns inventory.drop from "are you
// sure" into the drop.
const DropConfirmation = "yes"

// ItemName is a good's display name in this context's language.
func (c Context) ItemName(n Named) string { return c.named("item_name."+n.Code, n.Name) }

// itemCategory is a good's group.
func (c Context) itemCategory(code string) string { return c.named("item_category."+code, code) }

// InventoryLine is one line of the bag: a stack, or one piece.
type InventoryLine struct {
	Item     Named
	Category string
	Qty      int64
	// Serial is set for a piece: its address. Quality, UsesLeft and
	// Durability describe it.
	Serial     string
	Quality    int
	UsesLeft   int
	Durability int
	// Design is the name of the design a piece was made from, when a
	// company made it (docs/adr/0021-production-economy.md).
	Design string
}

// InventoryView is one page of the bag.
type InventoryView struct {
	Lines       []InventoryLine
	Page, Pages int
	Total       int
	// InEscrow counts the goods set aside for the market and the auction
	// house: still the player's, not in the bag.
	InEscrow int
}

// pieceLine describes a piece: its quality, and what is left of it.
func (c Context) pieceLine(quality, uses, durability int) string {
	if durability > 0 {
		return c.T("item.piece_worn", map[string]any{
			"quality": FormatNumber(c, int64(quality)), "uses": FormatNumber(c, int64(uses)),
			"durability": FormatNumber(c, int64(durability)),
		})
	}
	return c.T("item.piece", map[string]any{"quality": FormatNumber(c, int64(quality))})
}

// Inventory renders the bag.
func Inventory(c Context, v InventoryView) *presenter.Response {
	kb := keyboards.New()
	var content string
	if len(v.Lines) == 0 {
		content = c.T("item.bag_empty", nil)
	} else {
		lines := make([]string, 0, len(v.Lines))
		var buttons []presenter.Button
		for _, l := range v.Lines {
			name := c.ItemName(l.Item)
			if l.Design != "" {
				name = c.GoodName(Good{Item: l.Item, Design: l.Design})
			}
			var line string
			if l.Serial != "" {
				line = c.T("item.bag_piece", map[string]any{"item": name, "detail": c.pieceLine(l.Quality, l.UsesLeft, l.Durability)})
			} else {
				line = c.T("item.bag_stack", map[string]any{"item": name, "qty": FormatNumber(c, l.Qty)})
			}
			lines = append(lines, line)
			ref := l.Item.Code
			if l.Serial != "" {
				ref = l.Serial
			}
			if btn, ok := keyboards.Button(c.T("item.button.open", map[string]any{"item": name}), AddrItem, ref); ok {
				buttons = append(buttons, btn)
			}
		}
		content = paragraphs(body(lines...), pageIndicator(c, v.Page, v.Pages))
		kb.Grid(2, buttons...)
	}
	var escrow string
	if v.InEscrow > 0 {
		escrow = c.T("item.in_escrow", map[string]any{"count": FormatNumber(c, int64(v.InEscrow))})
	}
	shops, _ := keyboards.Button(c.T("shop.button.shops", nil), AddrShops)
	market, _ := keyboards.Button(c.T("market.button.market", nil), AddrMarket)
	kb.Row(shops, market)
	kb.Nav(c.nav(keyboards.Nav{
		Prefix: AddrInventory, Page: v.Page, HasPrev: pageOrOne(v.Page) > 1, HasNext: pageOrOne(v.Page) < v.Pages,
		BackData: AddrHome,
	}))
	return c.respond(paragraphs(c.T("item.bag_title", nil), content, escrow), kb.Build())
}

// EffectLine is one effect of using a good.
type EffectLine struct {
	Target string
	Op     string
	Value  int64
}

// GearLine is what a good does to crimes while carried.
type GearLine struct {
	Categories                                            []Named
	Crimes                                                []Named
	SuccessBPS, CatchBPS, WitnessBPS, SolveBPS, RewardBPS int
	Nerve                                                 int
	Confiscated                                           bool
}

// ItemDetailView is one good or piece in detail.
type ItemDetailView struct {
	Item     Named
	Category string
	Qty      int64
	// Ref is the good's code or the piece's serial: its address.
	Ref                           string
	Piece                         bool
	Quality, UsesLeft, Durability int
	Worth                         int64
	Effects                       []EffectLine
	Gear                          *GearLine
	Usable, Tradeable             bool
	// Cooldown is the rest after a use; CoolingFor what is left of it now.
	Cooldown   time.Duration
	CoolingFor time.Duration
	ReadyAt    time.Time
	// Nonce is the one-time token of the use and give buttons.
	Nonce string
	// GiveTo are the friends standing here who may receive it.
	GiveTo []Named
}

// effectLine renders one effect.
func (c Context) effectLine(e EffectLine) string {
	target := c.T("vital."+e.Target, nil)
	switch e.Op {
	case "add":
		if e.Value < 0 {
			return c.T("item.effect_minus", map[string]any{"vital": target, "value": FormatNumber(c, -e.Value)})
		}
		return c.T("item.effect_plus", map[string]any{"vital": target, "value": FormatNumber(c, e.Value)})
	case "cap":
		return c.T("item.effect_cap", map[string]any{"vital": target, "value": FormatNumber(c, e.Value)})
	}
	return c.T("item.effect_times", map[string]any{"vital": target, "pct": PercentFromBPS(c, int(e.Value))})
}

// gearLines renders what a good does to crimes.
func (c Context) gearLines(g GearLine) []string {
	var names []string
	for _, n := range g.Categories {
		names = append(names, c.CrimeCategoryName(n))
	}
	for _, n := range g.Crimes {
		names = append(names, c.CrimeName(n))
	}
	out := []string{c.T("item.gear_for", map[string]any{"crimes": joinWith(c, names)})}
	signed := func(field string, bps int) {
		switch {
		case bps > 0:
			out = append(out, c.T("gear."+field+"_up", map[string]any{"pct": PercentFromBPS(c, bps)}))
		case bps < 0:
			out = append(out, c.T("gear."+field+"_down", map[string]any{"pct": PercentFromBPS(c, -bps)}))
		}
	}
	signed("success", g.SuccessBPS)
	signed("catch", g.CatchBPS)
	signed("witness", g.WitnessBPS)
	signed("solve", g.SolveBPS)
	signed("reward", g.RewardBPS)
	switch {
	case g.Nerve > 0:
		out = append(out, c.T("gear.nerve_up", map[string]any{"value": FormatNumber(c, int64(g.Nerve))}))
	case g.Nerve < 0:
		out = append(out, c.T("gear.nerve_down", map[string]any{"value": FormatNumber(c, int64(-g.Nerve))}))
	}
	if g.Confiscated {
		out = append(out, c.T("item.gear_evidence", nil))
	}
	return out
}

// joinWith joins names with the catalogue's list separator.
func joinWith(c Context, names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += c.T("place.separator", nil)
		}
		out += n
	}
	return out
}

// ItemDetail renders one good or piece and what can be done with it.
func ItemDetail(c Context, v ItemDetailView) *presenter.Response {
	name := c.ItemName(v.Item)
	facts := []string{c.T("item.category", map[string]any{"category": c.itemCategory(v.Category)})}
	if v.Piece {
		facts = append(facts, c.pieceLine(v.Quality, v.UsesLeft, v.Durability))
	} else {
		facts = append(facts, c.T("item.you_have", map[string]any{"qty": FormatNumber(c, v.Qty)}))
	}
	facts = append(facts, c.T("item.worth", map[string]any{"price": FormatMoney(c, v.Worth)}))

	var uses []string
	if len(v.Effects) > 0 {
		uses = append(uses, c.T("item.effects", nil))
		for _, e := range v.Effects {
			uses = append(uses, c.effectLine(e))
		}
		if v.Cooldown > 0 {
			uses = append(uses, c.T("item.cooldown", map[string]any{"duration": FormatDuration(c, v.Cooldown)}))
		}
	}
	var gear []string
	if v.Gear != nil {
		gear = c.gearLines(*v.Gear)
	}
	var cooling string
	if v.CoolingFor > 0 {
		cooling = body(c.T("item.cooling", map[string]any{"duration": FormatDuration(c, v.CoolingFor)}),
			clockLine(c, "item.ready_at", v.ReadyAt))
	}

	kb := keyboards.New()
	if v.Usable && v.CoolingFor <= 0 {
		if btn, ok := keyboards.Button(c.T("item.button.use", map[string]any{"item": name}), AddrItemUse, v.Ref, v.Nonce); ok {
			kb.Row(btn)
		}
	}
	if v.Tradeable {
		var row []presenter.Button
		if v.Piece {
			if btn, ok := keyboards.Button(c.T("auction.button.sell", nil), AddrAuctionNew, v.Ref); ok {
				row = append(row, btn)
			}
		} else if btn, ok := keyboards.Button(c.T("market.button.sell_here", nil), AddrMarketBook, v.Item.Code); ok {
			row = append(row, btn)
		}
		if btn, ok := keyboards.Button(c.T("shop.button.sell_back", nil), AddrShopSellOffers, v.Ref); ok {
			row = append(row, btn)
		}
		kb.Row(row...)
		var gifts []presenter.Button
		for _, f := range v.GiveTo {
			if btn, ok := keyboards.Button(c.T("item.button.give", map[string]any{"player": f.Name}), AddrItemGive, v.Ref, v.Nonce, f.Code); ok {
				gifts = append(gifts, btn)
			}
		}
		kb.Grid(2, gifts...)
	}
	if btn, ok := keyboards.Button(c.T("item.button.drop", nil), AddrItemDrop, v.Ref); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrInventory, RefreshData: keyboards.Data(AddrItem, v.Ref)}))
	var give string
	if v.Tradeable && len(v.GiveTo) == 0 {
		give = c.T("item.give_nobody", nil)
	}
	return c.respond(paragraphs(
		c.T("item.detail_title", map[string]any{"item": name}),
		body(facts...),
		body(uses...),
		body(gear...),
		cooling,
		give,
	), kb.Build())
}

// VitalChange is one value a use changed.
type VitalChange struct {
	Target        string
	Before, After int
	Max           int
}

// ItemUsedView is a good used.
type ItemUsedView struct {
	Item    Named
	Changes []VitalChange
	// Left is how many remain; ReadyAt when the group may be used again.
	Left     int64
	Cooldown time.Duration
	ReadyAt  time.Time
}

// ItemUsed renders a use.
func ItemUsed(c Context, v ItemUsedView) *presenter.Response {
	lines := []string{c.T("item.used", map[string]any{"item": c.ItemName(v.Item)})}
	for _, ch := range v.Changes {
		lines = append(lines, c.T("item.changed", map[string]any{
			"vital": c.T("vital."+ch.Target, nil), "before": FormatNumber(c, int64(ch.Before)),
			"after": FormatNumber(c, int64(ch.After)), "max": FormatNumber(c, int64(ch.Max)),
		}))
	}
	lines = append(lines, c.T("item.left", map[string]any{"qty": FormatNumber(c, v.Left)}))
	if !v.ReadyAt.IsZero() {
		lines = append(lines, c.T("item.next_use", map[string]any{"duration": FormatDuration(c, v.Cooldown)}))
	}
	kb := keyboards.New()
	bag, _ := keyboards.Button(c.T("item.button.bag", nil), AddrInventory)
	kb.Row(bag)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(body(lines...), kb.Build())
}

// ItemGivenView is a gift handed over.
type ItemGivenView struct {
	Item Named
	To   Named
}

// ItemGiven renders a gift.
func ItemGiven(c Context, v ItemGivenView) *presenter.Response {
	kb := keyboards.New()
	bag, _ := keyboards.Button(c.T("item.button.bag", nil), AddrInventory)
	kb.Row(bag)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(c.T("item.given", map[string]any{"item": c.ItemName(v.Item), "player": v.To.Name}), kb.Build())
}

// ItemReceivedNotice tells a player a friend gave them something.
func ItemReceivedNotice(c Context, item Named, from string) *presenter.Response {
	kb := keyboards.New()
	bag, _ := keyboards.Button(c.T("item.button.bag", nil), AddrInventory)
	kb.Row(bag)
	return c.respond(c.T("item.received", map[string]any{"item": c.ItemName(item), "player": from}), kb.Build()).MarkPrivate()
}

// ItemDroppedView is a drop asked about or done.
type ItemDroppedView struct {
	Item  Named
	Ref   string
	Nonce string
}

// DropConfirm asks before a good is thrown away.
func DropConfirm(c Context, v ItemDroppedView) *presenter.Response {
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("item.button.drop_confirm", nil), AddrItemDrop, v.Ref, DropConfirmation, v.Nonce); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrItem, v.Ref)}))
	return c.respond(c.T("item.drop_confirm", map[string]any{"item": c.ItemName(v.Item)}), kb.Build())
}

// ItemDropped renders a drop.
func ItemDropped(c Context, v ItemDroppedView) *presenter.Response {
	kb := keyboards.New()
	bag, _ := keyboards.Button(c.T("item.button.bag", nil), AddrInventory)
	kb.Row(bag)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(c.T("item.dropped", map[string]any{"item": c.ItemName(v.Item)}), kb.Build())
}

// Item refusal kinds.
const (
	ItemRefusedNotHeld      = "not_held"
	ItemRefusedNotUsable    = "not_usable"
	ItemRefusedCooling      = "cooling"
	ItemRefusedNoEffect     = "no_effect"
	ItemRefusedNotTradeable = "not_tradeable"
	ItemRefusedNotTogether  = "not_together"
)

// ItemRefusalView is a refused request about a good.
type ItemRefusalView struct {
	Kind string
	Item Named
	// Wait and ReadyAt are the rest left, for cooling.
	Wait    time.Duration
	ReadyAt time.Time
}

// ItemRefusal renders a refused request about a good.
func ItemRefusal(c Context, v ItemRefusalView) *presenter.Response {
	lines := []string{c.T("item.refused."+v.Kind, map[string]any{
		"item": c.ItemName(v.Item), "duration": FormatDuration(c, v.Wait),
	})}
	if v.Kind == ItemRefusedCooling {
		lines = append(lines, clockLine(c, "item.ready_at", v.ReadyAt))
	}
	kb := keyboards.New()
	bag, _ := keyboards.Button(c.T("item.button.bag", nil), AddrInventory)
	kb.Row(bag)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(body(lines...), kb.Build())
}
