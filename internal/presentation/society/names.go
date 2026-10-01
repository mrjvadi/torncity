package society

// The screens of the politics and society area, by their names on the wire.
// The names are part of the client contract; renaming one breaks every client
// in the field.
const (
	// Appointments by office holders.
	ScreenAppointConfirm = "appoint_confirm"
	ScreenDismissConfirm = "dismiss_confirm"
	ScreenAppointDone    = "appoint_done"
	ScreenAppointRefusal = "appoint_refusal"

	// Elections.
	ScreenElections       = "elections"
	ScreenElection        = "election"
	ScreenStood           = "stood"
	ScreenVoted           = "voted"
	ScreenElectionRefusal = "election_refusal"

	// Factions.
	ScreenFactionList     = "faction_list"
	ScreenFactionPage     = "faction_page"
	ScreenFactionFound    = "faction_found"
	ScreenFactionFounded  = "faction_founded"
	ScreenFactionHome     = "faction_home"
	ScreenFactionMembers  = "faction_members"
	ScreenFactionAnswered = "faction_answered"
	ScreenFactionConfirm  = "faction_confirm"
	ScreenFactionLeft     = "faction_left"
	ScreenFactionLinked   = "faction_linked"
	ScreenFactionBank     = "faction_bank"
	ScreenFactionCrime    = "faction_crime"
	ScreenFactionRefusal  = "faction_refusal"
	ScreenFactionInvited  = "faction_invited"
	ScreenFactionApplied  = "faction_applied"

	// Diplomacy.
	ScreenSanctions        = "sanctions"
	ScreenImpose           = "impose"
	ScreenLift             = "lift"
	ScreenTreaties         = "treaties"
	ScreenPropose          = "propose"
	ScreenEndTreaty        = "end_treaty"
	ScreenDiplomacyHistory = "diplomacy_history"
	ScreenDiplomacyRefusal = "diplomacy_refusal"
	ScreenSanctionBlocked  = "sanction_blocked"

	// Governance.
	ScreenCityGovernance    = "city_governance"
	ScreenMyOffice          = "my_office"
	ScreenLeverEdit         = "lever_edit"
	ScreenPolicyConfirm     = "policy_confirm"
	ScreenPolicyAnnounced   = "policy_announced"
	ScreenAllocationEdit    = "allocation_edit"
	ScreenAllocationConfirm = "allocation_confirm"
	ScreenGovHistory        = "gov_history"
	ScreenPolicyRefused     = "policy_refused"

	// Legislature.
	ScreenBills       = "bills"
	ScreenBill        = "bill"
	ScreenBillRefusal = "bill_refusal"

	// Boards and people.
	ScreenLeaderboard     = "leaderboard"
	ScreenSearch          = "search"
	ScreenFriends         = "friends"
	ScreenFriendRequested = "friend_requested"
	ScreenFriendAccepted  = "friend_accepted"
)

// Refusal codes: the screen's refusal kind under the area it belongs to. The
// web words each code with its own table.
func AppointRefusalCode(v AppointRefusalView) string {
	if v.Gov != nil {
		return "gov_" + v.Gov.Kind
	}
	return "appoint_" + v.Kind
}

// PolicyRefusalCode is the refusal code of a refused policy command.
func PolicyRefusalCode(v PolicyRefusalView) string { return "gov_" + v.Refusal.Kind }

// ElectionRefusalCode is the refusal code of a refused election request.
func ElectionRefusalCode(kind string) string { return "election_" + kind }

// FactionRefusalCode is the refusal code of a refused faction request.
func FactionRefusalCode(kind string) string { return "faction_" + kind }

// DiplomacyRefusalCode is the refusal code of a refused diplomacy command.
func DiplomacyRefusalCode(kind string) string { return "diplomacy_" + kind }

// BillRefusalCode is the refusal code of a refused legislature request.
func BillRefusalCode(kind string) string { return "bill_" + kind }

// Faction request kinds and organised-crime states the views carry as codes
// (the game's own spelling).
const (
	FactionRequestInvite = "invite"
	FactionRequestApply  = "apply"
	OperationGathering   = "gathering"
	OperationRunning     = "running"
)

// Answers to a bill's vote and to a treaty proposal, as the commands take them.
const (
	VoteYes = "yes"
	VoteNo  = "no"
)

// FactionInvitedView is an invitation sent.
type FactionInvitedView struct{ Player GovPlayer }

// FactionAppliedView is an application sent.
type FactionAppliedView struct{ Ref FactionRef }

// FriendRequestedView is a friend request sent. Name is empty when the
// player's name is not known.
type FriendRequestedView struct{ Name string }

// FriendAcceptedView is a friend request accepted.
type FriendAcceptedView struct{ Name string }
