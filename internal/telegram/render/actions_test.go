package render

import (
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/gateway/routing"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

type fixture struct {
	name     string
	neutral  *presentation.Response
	telegram func(screens.Context) *presenter.Response
}

func named(code string) presentation.Named { return presentation.Named{Code: code, Name: code} }

func fixtures() []fixture {
	c := presentation.Ctx{Lang: "fa"}
	promo := &village.PromotionView{Village: "v", From: "village", To: "town", Met: true, CanPromote: true}
	ov := village.VillageOverviewView{Name: "v", Tier: "village", Resident: true, IsHead: true, Promotion: promo,
		Support: &village.VillageSupport{Code: "support", Name: "Support", Services: []string{"bank", "market"}}}
	know := village.KnowledgeListView{Name: "v", Lines: []village.KnowledgeLine{
		{Knowledge: named("carpentry"), State: village.KnowledgeAvailable, BuyPrice: 50}, {Knowledge: named("x"), State: village.KnowledgeLocked}}}
	menu := village.BuildMenuView{Name: "v", Lines: []village.BuildLine{{Building: named("house"), State: village.BuildAvailable}, {Building: named("school"), State: village.BuildLocked}}}
	progress := village.ConstructionProgressView{Name: "v",
		Lines:    []village.ConstructionLine{{ID: "b1", Building: named("house"), State: village.ConstructionBuilding, FinishAt: time.Unix(1_700_000_000, 0).UTC(), Left: time.Minute}, {ID: "b2", Building: named("barn"), ByWork: true, ProgressBPS: 500, DoneMinutes: 30, RequiredMinutes: 600, LeftMinutes: 570}},
		Standing: []village.StandingLine{{ID: "b3", Building: named("school")}}}
	board := village.LaborBoardView{Village: "v", Resident: true, Jobs: []village.LaborJobLine{{ID: "j1", BuildingID: "b2", Building: named("barn"), Wage: 40}},
		Sites: []village.LaborSiteRef{{ID: "b4", Building: named("hall")}}}
	site := village.LaborSiteView{Village: "v", ID: "b2", Building: named("barn"), Status: "building", CanWork: true, CanEmploy: true, CanPost: true,
		Job: &village.LaborJobLine{ID: "j1"}, HirePresets: []int{1, 3}, WagePresets: []village.LaborPreset{{Percent: 100, Wage: 40}}}
	mats := village.MaterialsView{Village: "v", CanBuy: true, Presets: []int64{5, 10}, Market: []village.MaterialMarketLine{{Item: named("plank"), Price: 3}}}
	work := village.WorkView{Village: "v", Resident: true, Places: []village.WorkplaceLine{{ID: "p1", Building: named("mill")}}}
	priv := village.PrivateMenuView{Village: "v", OwnedLots: 2, FreeLots: 1, Lines: []village.PrivateLine{{Building: named("cottage"), Affordable: true}}}
	mine := village.MineView{Village: "v", Home: &presentation.Named{Code: "cottage"}, CanRest: true, Debt: 10}
	terms := village.TermsView{Village: "v", LotPresets: []int64{100}, PermitPresets: []int64{5}, TaxPresets: []int{100}}
	donate := village.DonateView{Village: "v", Presets: []int64{100, 500}}
	res := village.ResidenceView{Village: "v", Home: "h"}
	promoV := *promo
	village := []fixture{
		{"overview", village.VillageOverview(c, ov), func(x screens.Context) *presenter.Response { return screens.VillageOverview(x, ov) }},
		{"knowledge", village.KnowledgeList(c, know), func(x screens.Context) *presenter.Response { return screens.KnowledgeList(x, know) }},
		{"build menu", village.BuildMenu(c, menu), func(x screens.Context) *presenter.Response { return screens.BuildMenu(x, menu) }},
		{"progress", village.ConstructionProgress(c, progress), func(x screens.Context) *presenter.Response { return screens.ConstructionProgress(x, progress) }},
		{"labor board", village.LaborBoard(c, board), func(x screens.Context) *presenter.Response { return screens.LaborBoard(x, board) }},
		{"labor site", village.LaborSite(c, site), func(x screens.Context) *presenter.Response { return screens.LaborSite(x, site) }},
		{"materials", village.VillageStock(c, mats), func(x screens.Context) *presenter.Response { return screens.VillageStock(x, mats) }},
		{"work", village.VillageWork(c, work), func(x screens.Context) *presenter.Response { return screens.VillageWork(x, work) }},
		{"private menu", village.PrivateMenu(c, priv), func(x screens.Context) *presenter.Response { return screens.PrivateMenu(x, priv) }},
		{"mine", village.Mine(c, mine), func(x screens.Context) *presenter.Response { return screens.Mine(x, mine) }},
		{"terms", village.Terms(c, terms), func(x screens.Context) *presenter.Response { return screens.Terms(x, terms) }},
		{"donate", village.VillageDonateMenu(c, donate), func(x screens.Context) *presenter.Response { return screens.VillageDonateMenu(x, donate) }},
		{"residence", village.ResidenceAsk(c, res), func(x screens.Context) *presenter.Response { return screens.ResidenceAsk(x, res) }},
		{"promotion", village.VillagePromotion(c, promoV), func(x screens.Context) *presenter.Response { return screens.VillagePromotion(x, promoV) }},
	}
	return append(village, economyFixtures()...)
}

// TestNeutralActionsAreServedCommands: every action the core lists names a
// command the game serves to players, and its address is one the routing
// parses back into the same command: what the web sends and what Telegram's
// button sends are the same thing.
func TestNeutralActionsAreServedCommands(t *testing.T) {
	for _, f := range fixtures() {
		if len(f.neutral.Actions) == 0 {
			t.Errorf("%s: no actions", f.name)
		}
		for _, a := range f.neutral.Actions {
			if !commands.FromPlayerCommand(a.Command) {
				t.Errorf("%s: action %s names %q, which the game does not serve to players", f.name, a.Key(), a.Command)
				continue
			}
			cmd, _, err := routing.ParseCallbackData(a.Address())
			if err != nil || cmd != a.Command {
				t.Errorf("%s: action %s address %q parses to %q (%v)", f.name, a.Key(), a.Address(), cmd, err)
			}
			switch a.Role {
			case "", presentation.RolePrimary, presentation.RoleSecondary, presentation.RoleDanger,
				presentation.RoleNavigation, presentation.RoleBack, presentation.RoleConfirm:
			default:
				t.Errorf("%s: action %s has the unknown role %q", f.name, a.Key(), a.Role)
			}
		}
	}
}

// TestTelegramButtonsAreNeutralActions: what Telegram offers under a screen is
// a subset of what the core says the viewer may do (Telegram leaves some out
// for a group, lays the rest out its own way), so the two presentations cannot
// disagree about what exists. The cells of a grid are addressed by coordinates
// the view carries and are not actions.
func TestTelegramButtonsAreNeutralActions(t *testing.T) {
	for _, f := range fixtures() {
		neutral := map[string]bool{}
		for _, a := range f.neutral.Actions {
			neutral[a.Address()] = true
			if a.Ask {
				// A button that asks for a value carries "ask:<command>:<fixed args>".
				neutral[askAddress(a)] = true
			}
		}
		for _, shared := range []bool{false, true} {
			out := f.telegram(screens.Context{Msgs: keyTranslator{}, Lang: "fa", Shared: shared})
			if out.Keyboard == nil {
				continue
			}
			for _, row := range out.Keyboard.Rows {
				for _, b := range row {
					if b.CallbackData != "" && !neutral[b.CallbackData] {
						t.Errorf("%s (shared=%v): Telegram offers %q (%s) but the core lists no such action", f.name, shared, b.CallbackData, strings.TrimSpace(b.Text))
					}
				}
			}
		}
	}
}

// askAddress is the callback data of a button that asks the player for the
// last value of an action: "ask", the command, and the fixed arguments with
// the trailing empty ones dropped.
func askAddress(a presentation.Action) string {
	args := append([]string(nil), a.Args...)
	for len(args) > 0 && args[len(args)-1] == "" {
		args = args[:len(args)-1]
	}
	return strings.Join(append([]string{screens.AddrAsk, a.Command}, args...), ":")
}
