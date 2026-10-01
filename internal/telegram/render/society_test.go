package render

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/gateway/routing"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/society"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// societyFixture pairs what the core answers for a screen with what the
// Telegram screen function draws from the same view.
type societyFixture struct {
	name     string
	neutral  *presentation.Response
	telegram func(screens.Context) *presenter.Response
}

// sfx builds a fixture from a fully populated view of V, changed by mutate.
func sfx[V any](name string, core func(presentation.Ctx, V) *presentation.Response,
	tg func(screens.Context, V) *presenter.Response, mutate ...func(*V),
) societyFixture {
	var v V
	v = fill(reflect.TypeOf(v), 0).Interface().(V)
	for _, m := range mutate {
		m(&v)
	}
	return societyFixture{name: name, neutral: core(presentation.Ctx{Lang: "fa"}, v),
		telegram: func(c screens.Context) *presenter.Response { return tg(c, v) }}
}

func societyFixtures() []societyFixture {
	place := func(code string) society.GovPlace { return society.GovPlace{Kind: "city", Code: code, Name: code} }
	country := society.GovPlace{Kind: "country", Code: "vantor", Name: "Vantor"}
	return []societyFixture{
		sfx("appoint confirm", society.AppointConfirm, screens.AppointConfirm),
		sfx("dismiss confirm", society.DismissConfirm, screens.DismissConfirm),
		sfx("appoint done", society.AppointDone, screens.AppointDone),
		sfx("appoint refusal", society.AppointRefusal, screens.AppointRefusal),
		sfx("appoint refusal gov", society.AppointRefusal, screens.AppointRefusal, func(v *society.AppointRefusalView) {
			v.Gov = &society.GovRefusal{Kind: society.GovRefusedNotHolder, Office: "mayor"}
		}),

		sfx("city governance", society.CityGovernance, screens.CityGovernance, func(v *society.CityGovView) {
			v.Tier = "city"
			v.Sections = []society.GovSection{{Place: place("ostmarch")}, {Place: country}}
		}),
		sfx("city governance village", society.CityGovernance, screens.CityGovernance, func(v *society.CityGovView) {
			v.Tier = "village"
			v.Sections = []society.GovSection{{Place: place("ostmarch")}}
		}),
		sfx("no city", society.CityGovernance, screens.CityGovernance, func(v *society.CityGovView) { *v = society.CityGovView{NoCity: true} }),
		sfx("my office", society.MyOffice, screens.MyOffice, func(v *society.MyOfficeView) {
			v.Seats = []society.GovSeat{{Office: "president", Place: country,
				Levers:     []society.GovLever{{Code: "city.tax_rate", Max: 2500}},
				VoteLevers: []society.GovLever{{Code: "country.war_levy"}},
				Appointees: []society.GovAppointee{{Office: "defence_minister", Place: country, Seat: 1, CanDismiss: true},
					{Office: "foreign_minister", Place: country, Seat: 1, CanAppoint: true}}}}
		}),
		sfx("lever edit steps", society.LeverEdit, screens.LeverEdit, func(v *society.LeverEditView) {
			v.NextChangeIn = 0
			v.Lever = society.GovLever{Code: "city.tax_rate", Type: "bps", Value: 450, Default: 450, Min: 0, Max: 2500}
			v.Draft, v.FineStep, v.CoarseStep = 800, 25, 250
		}),
		sfx("lever edit choices", society.LeverEdit, screens.LeverEdit, func(v *society.LeverEditView) {
			v.NextChangeIn = 0
			v.Lever = society.GovLever{Code: "country.arms_exports", Type: "int", Value: 0, Min: 0, Max: 3}
			v.Draft = 1
		}),
		sfx("lever edit cooldown", society.LeverEdit, screens.LeverEdit),
		sfx("policy confirm", society.PolicyConfirm, screens.PolicyConfirm),
		sfx("policy announced", society.PolicyAnnounced, screens.PolicyAnnounced),
		sfx("allocation edit", society.AllocationEdit, screens.AllocationEdit, func(v *society.AllocationEditView) {
			v.NextChangeIn = 0
			v.Lines = []society.AllocationLine{{Code: "police", Share: 2000, Down: "a", Up: "b"}, {Code: "health", Share: 3000, Up: "c"}}
		}),
		sfx("allocation edit cooldown", society.AllocationEdit, screens.AllocationEdit),
		sfx("allocation confirm", society.AllocationConfirm, screens.AllocationConfirm),
		sfx("gov history", society.GovHistory, screens.GovHistory, func(v *society.GovHistoryView) { v.Page, v.Pages = 2, 3 }),
		sfx("policy refused", society.PolicyRefused, screens.PolicyRefused),
		sfx("policy refused plain", society.PolicyRefused, screens.PolicyRefused, func(v *society.PolicyRefusalView) { v.Place, v.Lever = nil, nil }),

		sfx("elections", society.Elections, screens.Elections),
		sfx("election", society.Election, screens.Election, func(v *society.ElectionView) { v.Payment = nil }),
		sfx("election paid", society.Election, screens.Election, func(v *society.ElectionView) {
			v.Standing, v.Voted = false, false
			v.Payment = &presentation.PaymentChoice{Amount: 5, Accepted: []string{"cash", "card"}, Usable: []string{"cash", "card"}}
		}),
		sfx("stood", society.Stood, screens.Stood),
		sfx("voted", society.Voted, screens.Voted),
		sfx("election refusal", society.ElectionRefusal, screens.ElectionRefusal),

		sfx("faction list", society.FactionList, screens.FactionList),
		sfx("faction list none", society.FactionList, screens.FactionList, func(v *society.FactionListView) { v.Mine = nil }),
		sfx("faction page", society.FactionPage, screens.FactionPage),
		sfx("faction found", society.FactionFound, screens.FactionFound, func(v *society.FactionFoundView) {
			v.Payment = presentation.PaymentChoice{Amount: 5, Accepted: []string{"cash", "card"}, Usable: []string{"cash", "card"}}
		}),
		sfx("faction found broke", society.FactionFound, screens.FactionFound, func(v *society.FactionFoundView) { v.Payment.Usable = nil }),
		sfx("faction founded", society.FactionFounded, screens.FactionFounded),
		sfx("faction home", society.FactionHome, screens.FactionHome, func(v *society.FactionHomeView) { v.Rights = []string{"invite"} }),
		sfx("faction members", society.FactionMembers, screens.FactionMembers, func(v *society.FactionMembersView) {
			v.Requests[0].Kind = society.FactionRequestApply
		}),
		sfx("faction invited", society.FactionInvited, func(c screens.Context, v society.FactionInvitedView) *presenter.Response {
			return screens.FactionInvited(c, v.Player)
		}),
		sfx("faction applied", society.FactionApplied, func(c screens.Context, v society.FactionAppliedView) *presenter.Response {
			return screens.FactionApplied(c, v.Ref)
		}),
		sfx("faction answered invite", society.FactionAnswered, screens.FactionAnswered, func(v *society.FactionAnsweredView) { v.Kind = society.FactionRequestInvite }),
		sfx("faction answered apply", society.FactionAnswered, screens.FactionAnswered, func(v *society.FactionAnsweredView) { v.Kind = society.FactionRequestApply }),
		sfx("faction confirm kick", society.FactionConfirm, screens.FactionConfirm, func(v *society.FactionConfirmView) { v.Kind = society.FactionConfirmKick }),
		sfx("faction confirm lead", society.FactionConfirm, screens.FactionConfirm, func(v *society.FactionConfirmView) { v.Kind = society.FactionConfirmLead }),
		sfx("faction confirm leave", society.FactionConfirm, screens.FactionConfirm, func(v *society.FactionConfirmView) { v.Kind = society.FactionConfirmLeave }),
		sfx("faction left", society.FactionLeft, screens.FactionLeft),
		sfx("faction linked", society.FactionLinked, screens.FactionLinked),
		sfx("faction bank", society.FactionBank, screens.FactionBank, func(v *society.FactionBankView) { v.Methods = []string{"cash", "card"} }),
		sfx("faction crime gathering", society.FactionCrime, screens.FactionCrime, func(v *society.FactionCrimeBoardView) {
			v.Operation.Status = society.OperationGathering
			v.InCrew = false
			v.Operation.Min = 1
		}),
		sfx("faction crime plan", society.FactionCrime, screens.FactionCrime, func(v *society.FactionCrimeBoardView) { v.Operation = nil }),
		sfx("faction refusal", society.FactionRefusal, screens.FactionRefusal),
		sfx("faction refusal crime", society.FactionRefusal, screens.FactionRefusal, func(v *society.FactionRefusalView) { v.Kind = society.FactionRefusedLevel }),
		sfx("faction refusal list", society.FactionRefusal, screens.FactionRefusal, func(v *society.FactionRefusalView) { v.Kind = society.FactionRefusedNotMember }),

		sfx("sanctions", society.Sanctions, screens.Sanctions, func(v *society.SanctionsView) { v.Notice = nil }),
		sfx("impose targets", society.Impose, screens.Impose, func(v *society.ImposeView) { v.Target = nil }),
		sfx("impose measures", society.Impose, screens.Impose, func(v *society.ImposeView) { v.Grounds, v.Ground = nil, "" }),
		sfx("impose grounds", society.Impose, screens.Impose, func(v *society.ImposeView) { v.Ground = "" }),
		sfx("impose confirm", society.Impose, screens.Impose),
		sfx("lift", society.Lift, screens.Lift),
		sfx("treaties", society.Treaties, screens.Treaties, func(v *society.TreatiesView) {
			v.Treaties = []society.TreatyLine{{No: 1, Status: "proposed", Incoming: true}, {No: 2, Status: "proposed"},
				{No: 3, Status: "active"}, {No: 4, Status: "ended"}}
		}),
		sfx("propose partners", society.Propose, screens.Propose, func(v *society.ProposeView) { v.Partner, v.Kind = nil, nil }),
		sfx("propose kinds", society.Propose, screens.Propose, func(v *society.ProposeView) { v.Kind = nil }),
		sfx("propose confirm", society.Propose, screens.Propose),
		sfx("end treaty", society.EndTreaty, screens.EndTreaty),
		sfx("diplomacy history", society.DiplomacyHistory, screens.DiplomacyHistory, func(v *society.DiplomacyHistoryView) { v.Page, v.Pages = 2, 3 }),
		sfx("diplomacy refusal", society.DiplomacyRefusal, screens.DiplomacyRefusal, func(v *society.DiplomacyRefusalView) { v.Back = presentation.Ref{} }),
		sfx("diplomacy refusal back", society.DiplomacyRefusal, screens.DiplomacyRefusal, func(v *society.DiplomacyRefusalView) {
			v.Back = presentation.Ref{Command: "market.list"}
		}),
		sfx("sanction blocked", society.SanctionBlocked, screens.SanctionBlocked, func(v *society.SanctionBlockedView) { v.Back = presentation.Ref{} }),
		sfx("sanction blocked back", society.SanctionBlocked, screens.SanctionBlocked, func(v *society.SanctionBlockedView) {
			v.Back = presentation.Ref{Command: "market.list"}
		}),

		sfx("bills", society.Bills, screens.Bills),
		sfx("bill open", society.Bill, screens.Bill, func(v *society.BillView) { v.Status = "open" }),
		sfx("bill refusal", society.BillRefusal, screens.BillRefusal),

		sfx("search found", society.Search, screens.Search, func(v *society.SearchView) { v.Help = false; v.Found.Self = false }),
		sfx("search self", society.Search, screens.Search, func(v *society.SearchView) { v.Help = false; v.Found.Self = true }),
		sfx("search none", society.Search, screens.Search, func(v *society.SearchView) { v.Help = false; v.Found = nil }),
		sfx("friends", society.Friends, screens.Friends, func(v *society.FriendsView) { v.Page, v.Pages = 2, 3 }),
		sfx("friend requested", society.FriendRequested, func(c screens.Context, v society.FriendRequestedView) *presenter.Response {
			return screens.FriendRequested(c, v.Name)
		}),
		sfx("friend accepted", society.FriendAccepted, func(c screens.Context, v society.FriendAcceptedView) *presenter.Response {
			return screens.FriendAccepted(c, v.Name)
		}),
		sfx("leaderboard", society.Leaderboard, screens.Leaderboard, func(v *society.BoardView) { v.Board = "richest" }),
	}
}

// TestSocietyActionsAreServedCommands: every action the core lists names a
// command the game serves to players, and its address is one the routing
// parses back into the same command.
func TestSocietyActionsAreServedCommands(t *testing.T) {
	for _, f := range societyFixtures() {
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
		}
	}
}

// TestTelegramButtonsOfSocietyAreNeutralActions: what Telegram offers under a
// screen is a subset of what the core says the viewer may do. A button that asks
// the player to type a value (ask:command:args) is the action of that command.
func TestTelegramButtonsOfSocietyAreNeutralActions(t *testing.T) {
	for _, f := range societyFixtures() {
		neutral := map[string]bool{}
		for _, a := range f.neutral.Actions {
			neutral[a.Address()] = true
		}
		for _, shared := range []bool{false, true} {
			out := f.telegram(screens.Context{Msgs: keyTranslator{}, Lang: "fa", Shared: shared})
			if out.Keyboard == nil {
				continue
			}
			for _, row := range out.Keyboard.Rows {
				for _, b := range row {
					data := b.CallbackData
					if rest, ok := strings.CutPrefix(data, "ask:"); ok {
						dom, after, _ := strings.Cut(rest, ".")
						data = dom + ":" + after
					}
					if data != "" && !neutral[data] {
						t.Errorf("%s (shared=%v): Telegram offers %q (%s) but the core lists no such action", f.name, shared, b.CallbackData, strings.TrimSpace(b.Text))
					}
				}
			}
		}
	}
}
