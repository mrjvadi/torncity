package screens

import (
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Village news: the short line the group reads when something in its village
// finishes (internal/workers/notification/village_news.go). Public, never
// carrying an amount or one player's own business, and quiet: a single event
// is one sentence, a burst of them is one short list.

// The kinds of village news.
const (
	NewsBuilt        = "built"
	NewsBuildStarted = "build_started"
	NewsResearched   = "researched"
	NewsBought       = "bought"
	NewsTaught       = "taught"
)

// MaxNewsLines bounds a merged list; the rest is counted.
const MaxNewsLines = 6

// VillageNewsItem is one thing that happened.
type VillageNewsItem struct {
	Kind string
	// Building or Knowledge names what the item is about (by Kind).
	Building  Named
	Knowledge Named
	// Percent is the literacy reached, for NewsTaught.
	Percent int
}

// VillageNewsView is one post: one or more items of one village.
type VillageNewsView struct {
	Village string
	Items   []VillageNewsItem
}

// VillageNews renders the post with the button that opens the screen the
// news is about: the construction progress for construction, the knowledge
// list for research and purchases, the village overview for anything else or
// a mix.
func VillageNews(c Context, v VillageNewsView) *presenter.Response {
	kb := keyboards.New()
	addr, label := AddrVillageOverview, "village.button.overview"
	if kind := newsKindOf(v.Items); kind != "" {
		switch kind {
		case NewsBuilt, NewsBuildStarted:
			addr, label = AddrConstructionProgress, "village.button.progress"
		case NewsResearched, NewsBought:
			addr, label = AddrKnowledgeList, "village.button.knowledge"
		}
	}
	kb.Add(c.T(label, nil), addr)
	return c.respond(newsText(c, v), kb.Build())
}

// newsKindOf is the one kind every item shares, or "" for a mix (or none).
func newsKindOf(items []VillageNewsItem) string {
	if len(items) == 0 {
		return ""
	}
	kind := items[0].Kind
	for _, it := range items[1:] {
		if it.Kind != kind {
			return ""
		}
	}
	return kind
}

func newsArgs(c Context, village string, it VillageNewsItem) map[string]any {
	return map[string]any{
		"village":   village,
		"building":  c.SettlementBuildingName(it.Building),
		"knowledge": c.SettlementKnowledgeName(it.Knowledge),
		"percent":   FormatNumber(c, int64(it.Percent)),
	}
}

func newsText(c Context, v VillageNewsView) string {
	if len(v.Items) == 0 {
		return ""
	}
	if len(v.Items) == 1 {
		return c.T("village_news."+v.Items[0].Kind, newsArgs(c, v.Village, v.Items[0]))
	}
	lines := []string{c.T("village_news.merged.title", map[string]any{"village": v.Village})}
	shown := v.Items
	if len(shown) > MaxNewsLines {
		shown = shown[:MaxNewsLines]
	}
	for _, it := range shown {
		lines = append(lines, c.T("village_news.merged."+it.Kind, newsArgs(c, v.Village, it)))
	}
	if more := len(v.Items) - len(shown); more > 0 {
		lines = append(lines, c.T("village_news.merged.more", map[string]any{"count": FormatNumber(c, int64(more))}))
	}
	return body(lines...)
}
