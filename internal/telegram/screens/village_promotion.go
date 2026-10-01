package screens

import (
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"strconv"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The way forward (docs/adr/0028-world-and-settlements.md section 4.1): the
// goals of the NEXT tier - and only the next one - with the settlement's
// progress on each, and, once every goal is met, the head's button to grow
// the settlement into it. A village reads about its town and nothing about
// the city beyond (progressive disclosure, ADR 0033 section 5). Group
// screens, like every civic act of the village.

// Structured screens of the promotion commands (clients).
const (
	ScreenVillagePromotion  = village.ScreenVillagePromotion
	ScreenVillagePromoteAsk = village.ScreenVillagePromoteAsk
	ScreenVillagePromoted   = village.ScreenVillagePromoted
)

// The kinds of goal, as the rules name them (internal/domain/settlement).
const (
	promoResidents = "residents"
	promoLiteracy  = "literacy"
	promoBuildings = "buildings"
	promoRole      = "role"
	promoKnowledge = "knowledge"
	promoTreasury  = "treasury"
)

// promotionLines is the goals as lines: a check or an empty box, the goal, and
// how far along the settlement is.
func promotionLines(c Context, v PromotionView) string {
	var lines []string
	for _, k := range v.Criteria {
		mark := "▫️"
		if k.Met {
			mark = "✅"
		}
		args := map[string]any{"mark": mark}
		key := "village.promotion.goal." + k.Kind
		switch k.Kind {
		case promoLiteracy:
			args["current"] = PercentFromBPS(c, int(k.Current))
			args["required"] = PercentFromBPS(c, int(k.Required))
		case promoTreasury:
			args["current"] = FormatMoney(c, k.Current)
			args["required"] = FormatMoney(c, k.Required)
		case promoRole:
			key = "village.promotion.goal.role"
			args["role"] = c.T("village.promotion.role."+k.Role+".t"+strconv.FormatInt(k.Required, 10), nil)
		default:
			args["current"] = FormatNumber(c, k.Current)
			args["required"] = FormatNumber(c, k.Required)
		}
		lines = append(lines, c.T(key, args))
	}
	return body(lines...)
}

func promotionArgs(c Context, v PromotionView) map[string]any {
	return map[string]any{
		"village": v.Village,
		"tier":    c.T("village.tier_name."+v.To, nil),
		"from":    c.T("village.tier_name."+v.From, nil),
		"office":  c.T("village.office."+v.To, nil),
	}
}

// promotionBlock is the compact way forward shown on the village overview.
func promotionBlock(c Context, v PromotionView) string {
	args := promotionArgs(c, v)
	head := c.T("village.promotion.block_title", args)
	return body(head, promotionLines(c, v))
}

// promotionButton is the button under the overview's block: the act itself
// for a head whose settlement is ready, the goals otherwise.
func promotionButton(c Context, kb *keyboards.Builder, v PromotionView) {
	args := promotionArgs(c, v)
	if v.Met && v.CanPromote {
		kb.Add(c.T("village.promotion.button_promote", args), AddrVillagePromote)
		return
	}
	kb.Add(c.T("village.promotion.button_view", args), AddrVillagePromotion)
}

// VillagePromotion renders the goals of the next tier.
func VillagePromotion(c Context, v PromotionView) *presenter.Response {
	return c.withView(renderVillagePromotion(c, v), ScreenVillagePromotion, v)
}

func renderVillagePromotion(c Context, v PromotionView) *presenter.Response {
	args := promotionArgs(c, v)
	blocks := []string{c.T("village.promotion.title", args), c.T("village.promotion.intro", args), promotionLines(c, v)}
	kb := keyboards.New()
	switch {
	case v.Met && v.CanPromote:
		blocks = append(blocks, c.T("village.promotion.ready_head", args))
		kb.Add(c.T("village.promotion.button_promote", args), AddrVillagePromote)
	case v.Met:
		blocks = append(blocks, c.T("village.promotion.ready_member", args))
	default:
		blocks = append(blocks, c.T("village.promotion.not_ready", args))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrVillagePromotion}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// VillagePromoteAsk renders the confirm step.
func VillagePromoteAsk(c Context, v PromotionView) *presenter.Response {
	return c.withView(renderVillagePromoteAsk(c, v), ScreenVillagePromoteAsk, v)
}

func renderVillagePromoteAsk(c Context, v PromotionView) *presenter.Response {
	args := promotionArgs(c, v)
	kb := keyboards.New()
	if b, ok := keyboards.Button(c.T("village.promotion.button_yes", args), AddrVillagePromote, VillagePromoteConfirm); ok {
		kb.Row(b)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview}))
	return c.respond(paragraphs(c.T("village.promotion.ask_title", args), c.T("village.promotion.ask_body."+v.To, args)), kb.Build())
}

// VillagePromoted renders the result: the settlement is one tier up.
func VillagePromoted(c Context, v PromotionView) *presenter.Response {
	return c.withView(renderVillagePromoted(c, v), ScreenVillagePromoted, v)
}

func renderVillagePromoted(c Context, v PromotionView) *presenter.Response {
	args := promotionArgs(c, v)
	kb := keyboards.New()
	kb.Row(villageButtons(c, "village.button.build", AddrBuildMenu, "village.button.knowledge", AddrKnowledgeList)...)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrVillageOverview}))
	return c.respond(paragraphs(c.T("village.promotion.done_title", args), c.T("village.promotion.done_body."+v.To, args)), kb.Build())
}
