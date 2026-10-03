package render

import (
	"github.com/mrjvadi/torncity/internal/presentation/society"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The politics and society area (docs/adr/0039): offices and governance,
// elections, factions, diplomacy, the legislature, friends, search and the
// boards. Each screen is drawn for Telegram by the renderer internal/telegram/screens
// has always had, from the view the core now sends as data.
func init() {
	Register(society.ScreenAppointConfirm, screens.AppointConfirm)
	Register(society.ScreenDismissConfirm, screens.DismissConfirm)
	Register(society.ScreenAppointDone, screens.AppointDone)
	Register(society.ScreenAppointRefusal, screens.AppointRefusal)

	Register(society.ScreenElections, screens.Elections)
	Register(society.ScreenElection, screens.Election)
	Register(society.ScreenStood, screens.Stood)
	Register(society.ScreenVoted, screens.Voted)
	Register(society.ScreenElectionRefusal, screens.ElectionRefusal)

	Register(society.ScreenFactionList, screens.FactionList)
	Register(society.ScreenFactionPage, screens.FactionPage)
	Register(society.ScreenFactionFound, screens.FactionFound)
	Register(society.ScreenFactionFounded, screens.FactionFounded)
	Register(society.ScreenFactionHome, screens.FactionHome)
	Register(society.ScreenFactionMembers, screens.FactionMembers)
	Register(society.ScreenFactionAnswered, screens.FactionAnswered)
	Register(society.ScreenFactionConfirm, screens.FactionConfirm)
	Register(society.ScreenFactionLeft, screens.FactionLeft)
	Register(society.ScreenFactionLinked, screens.FactionLinked)
	Register(society.ScreenFactionBank, screens.FactionBank)
	Register(society.ScreenFactionCrime, screens.FactionCrime)
	Register(society.ScreenFactionRefusal, screens.FactionRefusal)
	Register(society.ScreenFactionInvited, func(c screens.Context, v society.FactionInvitedView) *presenter.Response {
		return screens.FactionInvited(c, v.Player)
	})
	Register(society.ScreenFactionApplied, func(c screens.Context, v society.FactionAppliedView) *presenter.Response {
		return screens.FactionApplied(c, v.Ref)
	})

	Register(society.ScreenSanctions, screens.Sanctions)
	Register(society.ScreenImpose, screens.Impose)
	Register(society.ScreenLift, screens.Lift)
	Register(society.ScreenTreaties, screens.Treaties)
	Register(society.ScreenPropose, screens.Propose)
	Register(society.ScreenEndTreaty, screens.EndTreaty)
	Register(society.ScreenDiplomacyHistory, screens.DiplomacyHistory)
	Register(society.ScreenDiplomacyRefusal, screens.DiplomacyRefusal)
	Register(society.ScreenSanctionBlocked, screens.SanctionBlocked)

	Register(society.ScreenCityGovernance, screens.CityGovernance)
	Register(society.ScreenMyOffice, screens.MyOffice)
	Register(society.ScreenLeverEdit, screens.LeverEdit)
	Register(society.ScreenPolicyConfirm, screens.PolicyConfirm)
	Register(society.ScreenPolicyAnnounced, screens.PolicyAnnounced)
	Register(society.ScreenAllocationEdit, screens.AllocationEdit)
	Register(society.ScreenAllocationConfirm, screens.AllocationConfirm)
	Register(society.ScreenGovHistory, screens.GovHistory)
	Register(society.ScreenPolicyRefused, screens.PolicyRefused)

	Register(society.ScreenBills, screens.Bills)
	Register(society.ScreenBill, screens.Bill)
	Register(society.ScreenBillRefusal, screens.BillRefusal)

	Register(society.ScreenLeaderboard, screens.Leaderboard)
	Register(society.ScreenSearch, screens.Search)
	Register(society.ScreenFriends, screens.Friends)
	Register(society.ScreenFriendDetail, screens.FriendDetail)
	Register(society.ScreenFriendRemoveAsk, screens.FriendRemoveAsk)
	Register(society.ScreenFriendRemoved, screens.FriendRemoved)
	Register(society.ScreenFriendRequested, func(c screens.Context, v society.FriendRequestedView) *presenter.Response {
		return screens.FriendRequested(c, v.Name)
	})
	Register(society.ScreenFriendAccepted, func(c screens.Context, v society.FriendAcceptedView) *presenter.Response {
		return screens.FriendAccepted(c, v.Name)
	})
}
