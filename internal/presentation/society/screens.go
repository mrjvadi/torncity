package society

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The politics and society area's screens, as the core builds them
// (docs/adr/0039). Each constructor takes the view a handler worked out and
// returns a neutral response: the screen's name, the view, and the actions the
// viewer may take next. Nothing here is worded, laid out or marked up.
//
// The actions are what the viewer MAY do, whatever an edge decides to show:
// Telegram's keyboard for a group screen offers a subset (deciding is the
// office holder's, in their private chat), the web picks the ones its layout
// has a place for.

// Screens, each defined once with the type of its view.
var (
	screenAppointConfirm = presentation.Define[AppointView](ScreenAppointConfirm, "society")
	screenDismissConfirm = presentation.Define[DismissView](ScreenDismissConfirm, "society")
	screenAppointDone    = presentation.Define[AppointDoneView](ScreenAppointDone, "society")
	screenAppointRefusal = presentation.Define[AppointRefusalView](ScreenAppointRefusal, "society", presentation.Refusal())

	screenElections       = presentation.Define[ElectionsView](ScreenElections, "society")
	screenElection        = presentation.Define[ElectionView](ScreenElection, "society")
	screenStood           = presentation.Define[StoodView](ScreenStood, "society", presentation.Private())
	screenVoted           = presentation.Define[VotedView](ScreenVoted, "society", presentation.Private())
	screenElectionRefusal = presentation.Define[ElectionRefusalView](ScreenElectionRefusal, "society", presentation.Refusal())

	screenFactionList     = presentation.Define[FactionListView](ScreenFactionList, "society")
	screenFactionPage     = presentation.Define[FactionPageView](ScreenFactionPage, "society")
	screenFactionFound    = presentation.Define[FactionFoundView](ScreenFactionFound, "society", presentation.Private())
	screenFactionFounded  = presentation.Define[FactionFoundedView](ScreenFactionFounded, "society", presentation.Private())
	screenFactionHome     = presentation.Define[FactionHomeView](ScreenFactionHome, "society", presentation.Private())
	screenFactionMembers  = presentation.Define[FactionMembersView](ScreenFactionMembers, "society", presentation.Private())
	screenFactionAnswered = presentation.Define[FactionAnsweredView](ScreenFactionAnswered, "society", presentation.Private())
	screenFactionConfirm  = presentation.Define[FactionConfirmView](ScreenFactionConfirm, "society", presentation.Private())
	screenFactionLeft     = presentation.Define[FactionLeftView](ScreenFactionLeft, "society", presentation.Private())
	screenFactionLinked   = presentation.Define[FactionLinkedView](ScreenFactionLinked, "society")
	screenFactionBank     = presentation.Define[FactionBankView](ScreenFactionBank, "society", presentation.Private())
	screenFactionCrime    = presentation.Define[FactionCrimeView](ScreenFactionCrime, "society")
	screenFactionRefusal  = presentation.Define[FactionRefusalView](ScreenFactionRefusal, "society", presentation.Refusal())
	screenFactionInvited  = presentation.Define[FactionInvitedView](ScreenFactionInvited, "society", presentation.Private())
	screenFactionApplied  = presentation.Define[FactionAppliedView](ScreenFactionApplied, "society", presentation.Private())

	screenSanctions        = presentation.Define[SanctionsView](ScreenSanctions, "society")
	screenImpose           = presentation.Define[ImposeView](ScreenImpose, "society")
	screenLift             = presentation.Define[LiftView](ScreenLift, "society")
	screenTreaties         = presentation.Define[TreatiesView](ScreenTreaties, "society")
	screenPropose          = presentation.Define[ProposeView](ScreenPropose, "society")
	screenEndTreaty        = presentation.Define[EndTreatyView](ScreenEndTreaty, "society")
	screenDiplomacyHistory = presentation.Define[DiplomacyHistoryView](ScreenDiplomacyHistory, "society")
	screenDiplomacyRefusal = presentation.Define[DiplomacyRefusalView](ScreenDiplomacyRefusal, "society", presentation.Refusal())
	screenSanctionBlocked  = presentation.Define[SanctionBlockedView](ScreenSanctionBlocked, "society", presentation.Refusal())

	screenCityGovernance    = presentation.Define[CityGovView](ScreenCityGovernance, "society")
	screenMyOffice          = presentation.Define[MyOfficeView](ScreenMyOffice, "society")
	screenLeverEdit         = presentation.Define[LeverEditView](ScreenLeverEdit, "society")
	screenPolicyConfirm     = presentation.Define[PolicyConfirmView](ScreenPolicyConfirm, "society")
	screenPolicyAnnounced   = presentation.Define[PolicyAnnouncedView](ScreenPolicyAnnounced, "society")
	screenAllocationEdit    = presentation.Define[AllocationEditView](ScreenAllocationEdit, "society")
	screenAllocationConfirm = presentation.Define[AllocationConfirmView](ScreenAllocationConfirm, "society")
	screenGovHistory        = presentation.Define[GovHistoryView](ScreenGovHistory, "society")
	screenPolicyRefused     = presentation.Define[PolicyRefusalView](ScreenPolicyRefused, "society", presentation.Refusal())

	screenBills       = presentation.Define[BillsView](ScreenBills, "society")
	screenBill        = presentation.Define[BillView](ScreenBill, "society")
	screenBillRefusal = presentation.Define[BillRefusalView](ScreenBillRefusal, "society", presentation.Refusal())

	screenLeaderboard     = presentation.Define[BoardView](ScreenLeaderboard, "society")
	screenSearch          = presentation.Define[SearchView](ScreenSearch, "society")
	screenFriends         = presentation.Define[FriendsView](ScreenFriends, "society")
	screenFriendRequested = presentation.Define[FriendRequestedView](ScreenFriendRequested, "society")
	screenFriendAccepted  = presentation.Define[FriendAcceptedView](ScreenFriendAccepted, "society")
)

// Addresses of screens outside this area that its screens lead to.
const (
	addrHome     = "player:profile.get"
	addrMap      = "map:list"
	addrBank     = "bank:show"
	addrPay      = "bank:pay"
	addrMinistry = "military:ministry"
	addrBudget   = "city:budget"
	addrLifeTop  = "life:top"

	// Friends.
	addrFriendList   = "social:friend.list"
	addrFriendAdd    = "social:friend.add"
	addrFriendAccept = "social:friend.accept"
)

// Commands of the typed-in actions (the player types the last value).
const (
	commandAppoint     = "gov.appoint"
	commandFactionInv  = "faction.invite"
	commandFactionDep  = "faction.deposit"
	commandFactionWd   = "faction.withdraw"
	commandFactionFind = "faction.found"
)

func act(addr string, args ...string) presentation.Action {
	r := presentation.RefOfAddress(addr)
	return presentation.Do(r.Command, append(append([]string(nil), r.Args...), args...)...)
}

func back(addr string, args ...string) presentation.Action {
	r := presentation.RefOfAddress(addr)
	return presentation.Back(r.Command, append(append([]string(nil), r.Args...), args...)...)
}

func refresh(addr string, args ...string) presentation.Action {
	r := presentation.RefOfAddress(addr)
	return presentation.Refresh(r.Command, append(append([]string(nil), r.Args...), args...)...)
}

func confirm(addr string, args ...string) presentation.Action {
	r := presentation.RefOfAddress(addr)
	return presentation.Confirm(r.Command, append(append([]string(nil), r.Args...), args...)...)
}

func i64(n int64) string { return strconv.FormatInt(n, 10) }
func itoa(n int) string  { return strconv.Itoa(n) }

func pageOr1(p int) int {
	if p < 1 {
		return 1
	}
	return p
}

// pager adds the previous and next page actions of a list screen. addr is the
// list's address with its fixed arguments.
func pager(a []presentation.Action, addr string, fixed []string, page, pages int) []presentation.Action {
	p := pageOr1(page)
	if p > 1 {
		a = append(a, act(addr, append(append([]string(nil), fixed...), itoa(p-1))...).Named("page.prev"))
	}
	if p < pages {
		a = append(a, act(addr, append(append([]string(nil), fixed...), itoa(p+1))...).Named("page.next"))
	}
	return a
}

// ---- appointments ----

// AppointConfirm asks the appointer to confirm an appointment.
func AppointConfirm(c presentation.Ctx, v AppointView) *presentation.Response {
	return screenAppointConfirm.Response(c.Lang, v,
		confirm(AddrGovSeat, v.Office, v.Place.Code, v.Player.Code).Named("gov.appoint.confirm"),
		back(AddrGovOffice))
}

// DismissConfirm asks the holder to confirm removing another from office.
func DismissConfirm(c presentation.Ctx, v DismissView) *presentation.Response {
	return screenDismissConfirm.Response(c.Lang, v,
		confirm(AddrGovUnseat, v.Office, v.Place.Code, itoa(v.Seat)).Named("gov.dismiss.confirm"),
		back(AddrGovOffice))
}

// AppointDone reports an appointment or a removal made.
func AppointDone(c presentation.Ctx, v AppointDoneView) *presentation.Response {
	return screenAppointDone.Response(c.Lang, v,
		act(AddrGovOffice).Named("gov.my_office"),
		back(AddrGovCity))
}

// AppointRefusal is a refused appointment or removal.
func AppointRefusal(c presentation.Ctx, v AppointRefusalView) *presentation.Response {
	return screenAppointRefusal.Response(c.Lang, v, back(AddrGovOffice)).
		Refused(AppointRefusalCode(v), map[string]any{"office": v.Office})
}

// ---- governance ----

// CityGovernance is a city's offices and policies.
func CityGovernance(c presentation.Ctx, v CityGovView) *presentation.Response {
	if v.NoCity {
		return screenCityGovernance.Response(c.Lang, v, act(addrMap).Named("map"), back(addrMap))
	}
	a := []presentation.Action{
		act(AddrGovHistory, v.City.Code).Named("gov.history"),
		act(AddrElections).Named("gov.elections"),
	}
	if v.Tier == "" || v.Tier == "city" {
		a = append(a, act(addrBudget, v.City.Code).Named("gov.budget"), act(AddrBills).Named("gov.laws"))
	}
	if v.HoldsOffice {
		a = append(a, act(AddrGovOffice).Named("gov.my_office"))
	}
	for _, sec := range v.Sections {
		if sec.Place.Kind == "country" {
			a = append(a, act(addrMinistry, sec.Place.Code).Named("military.ministry").About(sec.Place.Code))
		}
	}
	a = append(a, back(addrMap), refresh(AddrGovCity, v.City.Code))
	return screenCityGovernance.Response(c.Lang, v, a...)
}

// MyOffice is the office holder's screen.
func MyOffice(c presentation.Ctx, v MyOfficeView) *presentation.Response {
	var a []presentation.Action
	for _, s := range v.Seats {
		for _, l := range s.Levers {
			a = append(a, act(AddrGovLever, l.Code, s.Place.Code).Named("gov.lever.change").About(l.Code))
		}
		for _, l := range s.VoteLevers {
			a = append(a, act(AddrGovLever, l.Code, s.Place.Code).Named("gov.lever.propose").About(l.Code))
		}
		for _, ap := range s.Appointees {
			switch {
			case ap.CanAppoint:
				a = append(a, presentation.Do(commandAppoint, ap.Office, ap.Place.Code).Asking().Named("gov.appoint").About(ap.Office))
			case ap.CanDismiss:
				a = append(a, act(AddrGovDismiss, ap.Office, ap.Place.Code, itoa(ap.Seat)).Named("gov.dismiss").About(ap.Office))
			}
		}
	}
	shown := map[string]bool{}
	for _, s := range v.Seats {
		if s.Place.Kind != "country" || shown[s.Place.Code] {
			continue
		}
		shown[s.Place.Code] = true
		a = append(a, act(addrMinistry, s.Place.Code).Named("military.ministry").About(s.Place.Code))
	}
	a = append(a, back(AddrGovCity), refresh(AddrGovOffice))
	return screenMyOffice.Response(c.Lang, v, a...)
}

// LeverEdit is one policy the viewer may change, with the value proposed.
func LeverEdit(c presentation.Ctx, v LeverEditView) *presentation.Response {
	l := v.Lever
	var a []presentation.Action
	set := func(id string, x int64) {
		a = append(a, act(AddrGovLever, l.Code, v.Place.Code, i64(x)).Named(id))
	}
	if v.NextChangeIn <= 0 {
		// Named choices: a lever whose values are words is set by choosing.
		if l.Max-l.Min <= 10 {
			for x := l.Min; x <= l.Max; x++ {
				if x != v.Draft {
					set("gov.lever.choose", x)
				}
			}
		}
		for _, s := range []struct {
			id    string
			delta int64
		}{{"gov.lever.down_coarse", -v.CoarseStep}, {"gov.lever.down_fine", -v.FineStep},
			{"gov.lever.up_fine", v.FineStep}, {"gov.lever.up_coarse", v.CoarseStep}} {
			if s.delta == 0 {
				continue
			}
			if next := clamp(v.Draft+s.delta, l.Min, l.Max); next != v.Draft {
				set(s.id, next)
			}
		}
		for _, p := range []struct {
			id    string
			value int64
		}{{"gov.lever.min", l.Min}, {"gov.lever.default", l.Default}, {"gov.lever.max", l.Max}} {
			if p.value != v.Draft {
				set(p.id, p.value)
			}
		}
		if v.Draft != l.Value {
			a = append(a, act(AddrGovConfirm, l.Code, v.Place.Code, i64(v.Draft)).Named("gov.review"))
		}
	}
	a = append(a, back(AddrGovOffice), refresh(AddrGovLever, l.Code, v.Place.Code))
	return screenLeverEdit.Response(c.Lang, v, a...)
}

func clamp(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// PolicyConfirm asks the office holder to confirm one change.
func PolicyConfirm(c presentation.Ctx, v PolicyConfirmView) *presentation.Response {
	l := v.Lever
	return screenPolicyConfirm.Response(c.Lang, v,
		confirm(AddrGovSet, l.Code, v.Place.Code, i64(v.NewValue)).Named("gov.confirm"),
		act(AddrGovLever, l.Code, v.Place.Code, i64(v.NewValue)).Named("cancel").As(presentation.RoleSecondary))
}

// PolicyAnnounced reports a change that was just announced.
func PolicyAnnounced(c presentation.Ctx, v PolicyAnnouncedView) *presentation.Response {
	return screenPolicyAnnounced.Response(c.Lang, v,
		act(AddrGovOffice).Named("gov.my_office"),
		back(AddrGovOffice), refresh(AddrGovLever, v.Lever.Code, v.Place.Code))
}

// AllocationEdit is the allocation editor.
func AllocationEdit(c presentation.Ctx, v AllocationEditView) *presentation.Response {
	l := v.Lever
	var a []presentation.Action
	if v.NextChangeIn > 0 {
		a = append(a, back(AddrGovOffice), refresh(AddrGovLever, l.Code, v.Place.Code))
		return screenAllocationEdit.Response(c.Lang, v, a...)
	}
	for _, line := range v.Lines {
		if line.Down != "" {
			a = append(a, act(AddrGovAlloc, l.Code, v.Place.Code, line.Down).Named("gov.alloc.down").About(line.Code))
		}
		if line.Up != "" {
			a = append(a, act(AddrGovAlloc, l.Code, v.Place.Code, line.Up).Named("gov.alloc.up").About(line.Code))
		}
	}
	if v.Changed {
		a = append(a, act(AddrGovAllocConfirm, l.Code, v.Place.Code, v.Draft).Named("gov.review"))
	}
	a = append(a, back(AddrGovOffice), refresh(AddrGovAlloc, l.Code, v.Place.Code, v.Draft))
	return screenAllocationEdit.Response(c.Lang, v, a...)
}

// AllocationConfirm asks the holder to confirm an allocation.
func AllocationConfirm(c presentation.Ctx, v AllocationConfirmView) *presentation.Response {
	l := v.Lever
	return screenAllocationConfirm.Response(c.Lang, v,
		confirm(AddrGovAllocSet, l.Code, v.Place.Code, v.Draft).Named("gov.confirm"),
		act(AddrGovAlloc, l.Code, v.Place.Code, v.Draft).Named("cancel").As(presentation.RoleSecondary))
}

// GovHistory is one page of a city's public record of changes.
func GovHistory(c presentation.Ctx, v GovHistoryView) *presentation.Response {
	a := pager(nil, AddrGovHistory, []string{v.City.Code}, v.Page, v.Pages)
	a = append(a, back(AddrGovCity, v.City.Code), refresh(AddrGovHistory, v.City.Code, itoa(pageOr1(v.Page))))
	return screenGovHistory.Response(c.Lang, v, a...)
}

// PolicyRefused is a governance refusal, with the way back.
func PolicyRefused(c presentation.Ctx, v PolicyRefusalView) *presentation.Response {
	var a []presentation.Action
	b := back(AddrGovOffice)
	if v.Lever != nil && v.Place != nil {
		b = back(AddrGovLever, v.Lever.Code, v.Place.Code)
		a = append(a, act(AddrGovOffice).Named("gov.my_office"))
	}
	a = append(a, b)
	return screenPolicyRefused.Response(c.Lang, v, a...).
		Refused(PolicyRefusalCode(v), map[string]any{"office": v.Refusal.Office})
}

// ---- elections ----

// Elections is the list of elections of the player's city and above it.
func Elections(c presentation.Ctx, v ElectionsView) *presentation.Response {
	if v.NoCity {
		return screenElections.Response(c.Lang, v, back(addrMap))
	}
	var a []presentation.Action
	for _, l := range v.Elections {
		a = append(a, act(AddrElection, i64(l.No)).Named("election.open"))
	}
	a = append(a, back(AddrGovCity, v.Place.Code), refresh(AddrElections))
	return screenElections.Response(c.Lang, v, a...)
}

// Election is one election for the viewer.
func Election(c presentation.Ctx, v ElectionView) *presentation.Response {
	no := i64(v.No)
	var a []presentation.Action
	if v.CanVote {
		for i := range v.Candidates {
			a = append(a, act(AddrElectionVote, no, itoa(i+1), v.Nonce).Named("election.vote"))
		}
	}
	if v.CanStand {
		if v.Payment != nil {
			for _, m := range v.Payment.Usable {
				a = append(a, act(AddrElectionStand, no, m, v.Nonce).Named("election.stand"))
			}
		} else {
			// A free candidacy has no method to name: the arguments go by name.
			a = append(a, presentation.Do("election.stand", no).With("no", no).With("nonce", v.Nonce).Named("election.stand"))
		}
	}
	a = append(a, back(AddrElections), refresh(AddrElection, no))
	return screenElection.Response(c.Lang, v, a...)
}

// Stood is a candidacy registered.
func Stood(c presentation.Ctx, v StoodView) *presentation.Response {
	return screenStood.Response(c.Lang, v, back(AddrElection, i64(v.No)))
}

// Voted is a vote cast. It is private: whom a player voted for is theirs alone.
func Voted(c presentation.Ctx, v VotedView) *presentation.Response {
	return screenVoted.Response(c.Lang, v, back(AddrElection, i64(v.No)))
}

// ElectionRefusal is a refused election request.
func ElectionRefusal(c presentation.Ctx, v ElectionRefusalView) *presentation.Response {
	var a []presentation.Action
	if v.No > 0 {
		a = append(a, act(AddrElection, i64(v.No)).Named("election.open"))
	}
	a = append(a, back(AddrElections))
	return screenElectionRefusal.Response(c.Lang, v, a...).
		Refused(ElectionRefusalCode(v.Kind), map[string]any{"no": v.No, "office": v.Office})
}

// ---- factions ----

// FactionList is the factions of the player's city.
func FactionList(c presentation.Ctx, v FactionListView) *presentation.Response {
	var a []presentation.Action
	for _, f := range v.Factions {
		a = append(a, act(AddrFaction, f.Ref.Code).Named("faction.view"))
	}
	if v.Mine != nil {
		a = append(a, act(AddrFactionMine).Named("faction.mine"))
	} else {
		a = append(a, act(AddrFactionFound).Named("faction.found"))
	}
	a = append(a, back(addrHome), refresh(AddrFactions))
	return screenFactionList.Response(c.Lang, v, a...)
}

// FactionPage is a faction's public page.
func FactionPage(c presentation.Ctx, v FactionPageView) *presentation.Response {
	var a []presentation.Action
	if v.CanLink {
		a = append(a, act(AddrFactionLink).Named("faction.link"))
	}
	switch {
	case v.Mine:
		a = append(a, act(AddrFactionMine).Named("faction.mine"))
	case v.CanApply:
		a = append(a, act(AddrFactionApply, v.Ref.Code, FactionYes).Named("faction.apply"))
	}
	a = append(a, back(AddrFactions), refresh(AddrFaction, v.Ref.Code))
	return screenFactionPage.Response(c.Lang, v, a...)
}

// FactionFound is founding a faction: the fee, and a way to pay each asking for
// the name.
func FactionFound(c presentation.Ctx, v FactionFoundView) *presentation.Response {
	var a []presentation.Action
	if len(v.Payment.Usable) > 0 {
		for _, m := range v.Payment.Usable {
			a = append(a, presentation.Do(commandFactionFind, m).Asking().Named("faction.found.pay"))
		}
	} else {
		a = append(a, act(addrBank).Named("bank"))
	}
	a = append(a, back(AddrFactions))
	return screenFactionFound.Response(c.Lang, v, a...)
}

// FactionFounded is a faction founded.
func FactionFounded(c presentation.Ctx, v FactionFoundedView) *presentation.Response {
	return screenFactionFounded.Response(c.Lang, v, act(AddrFactionMine).Named("faction.mine"), back(addrHome))
}

// FactionHome is a member's faction screen.
func FactionHome(c presentation.Ctx, v FactionHomeView) *presentation.Response {
	a := []presentation.Action{
		act(AddrFactionMembers).Named("faction.members"),
		act(AddrFactionBank).Named("faction.bank"),
		act(AddrFactionCrime).Named("faction.crime"),
	}
	for _, r := range v.Rights {
		if r == "invite" {
			a = append(a, presentation.Do(commandFactionInv).Asking().Named("faction.invite"))
			break
		}
	}
	a = append(a, act(AddrFactionLeave).Named("faction.leave").As(presentation.RoleDanger),
		back(addrHome), refresh(AddrFactionMine))
	return screenFactionHome.Response(c.Lang, v, a...)
}

// FactionMembers is a faction's members, and what is waiting.
func FactionMembers(c presentation.Ctx, v FactionMembersView) *presentation.Response {
	var a []presentation.Action
	for _, m := range v.Members {
		if m.CanPromote {
			a = append(a, act(AddrFactionRank, m.Player.Code, "officer").Named("faction.promote"))
		}
		if m.CanDemote {
			a = append(a, act(AddrFactionRank, m.Player.Code, "member").Named("faction.demote"))
		}
		if m.CanKick {
			a = append(a, act(AddrFactionKick, m.Player.Code).Named("faction.kick").As(presentation.RoleDanger))
		}
		if m.CanLead {
			a = append(a, act(AddrFactionRank, m.Player.Code, "leader").Named("faction.lead"))
		}
	}
	for _, q := range v.Requests {
		if q.CanDecide {
			no := i64(q.No)
			a = append(a, act(AddrFactionAnswer, no, FactionAccept).Named("faction.accept"),
				act(AddrFactionAnswer, no, FactionDecline).Named("faction.decline"))
		}
	}
	if v.CanInvite {
		a = append(a, presentation.Do(commandFactionInv).Asking().Named("faction.invite"))
	}
	a = append(a, back(AddrFactionMine), refresh(AddrFactionMembers))
	return screenFactionMembers.Response(c.Lang, v, a...)
}

// FactionInvited is an invitation sent.
func FactionInvited(c presentation.Ctx, v FactionInvitedView) *presentation.Response {
	return screenFactionInvited.Response(c.Lang, v, act(AddrFactionMembers).Named("faction.members"), back(AddrFactionMine))
}

// FactionApplied is an application sent.
func FactionApplied(c presentation.Ctx, v FactionAppliedView) *presentation.Response {
	return screenFactionApplied.Response(c.Lang, v, act(AddrFactions).Named("faction.list"), back(addrHome))
}

// FactionAnswered is an invitation or application answered.
func FactionAnswered(c presentation.Ctx, v FactionAnsweredView) *presentation.Response {
	var next presentation.Action
	if v.Accepted && v.Kind == FactionRequestInvite {
		next = act(AddrFactionMine).Named("faction.mine")
	} else {
		next = act(AddrFactionMembers).Named("faction.members")
	}
	return screenFactionAnswered.Response(c.Lang, v, next, back(addrHome))
}

// FactionConfirm asks to confirm an act that cannot be taken back.
func FactionConfirm(c presentation.Ctx, v FactionConfirmView) *presentation.Response {
	var yes presentation.Action
	switch v.Kind {
	case FactionConfirmKick:
		yes = confirm(AddrFactionKick, v.Player.Code, FactionYes)
	case FactionConfirmLead:
		yes = confirm(AddrFactionRank, v.Player.Code, "leader", FactionYes)
	default:
		yes = confirm(AddrFactionLeave, FactionYes)
	}
	return screenFactionConfirm.Response(c.Lang, v, yes.Named("faction.confirm."+v.Kind), back(AddrFactionMine))
}

// FactionLeft is a member gone, or a faction disbanded.
func FactionLeft(c presentation.Ctx, v FactionLeftView) *presentation.Response {
	return screenFactionLeft.Response(c.Lang, v, act(AddrFactions).Named("faction.list"), back(addrHome))
}

// FactionLinked is a faction tied to a group.
func FactionLinked(c presentation.Ctx, v FactionLinkedView) *presentation.Response {
	return screenFactionLinked.Response(c.Lang, v, act(AddrFactionCrime).Named("faction.crime"))
}

// FactionBank is a faction's bank.
func FactionBank(c presentation.Ctx, v FactionBankView) *presentation.Response {
	var a []presentation.Action
	if v.CanDeposit {
		for _, m := range v.Methods {
			a = append(a, presentation.Do(commandFactionDep, m).Asking().Named("faction.deposit."+m))
		}
	}
	if v.CanWithdraw {
		a = append(a, presentation.Do(commandFactionWd).Asking().Named("faction.withdraw"))
	}
	a = append(a, back(AddrFactionMine), refresh(AddrFactionBank))
	return screenFactionBank.Response(c.Lang, v, a...)
}

// FactionCrime is a faction's organised crime board.
func FactionCrime(c presentation.Ctx, v FactionCrimeView) *presentation.Response {
	var a []presentation.Action
	if o := v.Operation; o != nil {
		if o.Status == OperationGathering {
			if v.CanJoin && !v.InCrew {
				a = append(a, act(AddrFactionJoin).Named("faction.join"))
			}
			if v.CanLaunch && len(o.Crew) >= o.Min {
				a = append(a, act(AddrFactionLaunch).Named("faction.launch"))
			}
			if v.CanPlan {
				a = append(a, act(AddrFactionCallOff).Named("faction.calloff").As(presentation.RoleDanger))
			}
		}
	} else if v.CanPlan {
		for _, l := range v.Crimes {
			a = append(a, act(AddrFactionPlan, l.Crime.Code).Named("faction.plan").About(l.Crime.Code))
		}
	}
	a = append(a, back(AddrFactionMine), refresh(AddrFactionCrime))
	return screenFactionCrime.Response(c.Lang, v, a...)
}

// FactionRefusal is a refused faction request.
func FactionRefusal(c presentation.Ctx, v FactionRefusalView) *presentation.Response {
	var next presentation.Action
	switch v.Kind {
	case FactionRefusedNotMember, FactionRefusedNotFound, FactionRefusedNone:
		next = act(AddrFactions).Named("faction.list")
	case FactionRefusedNoSuchCrime, FactionRefusedOperationOpen, FactionRefusedLevel, FactionRefusedNoPlaceHere,
		FactionRefusedNoOperation, FactionRefusedCrewFull, FactionRefusedElsewhere, FactionRefusedCrewShort, FactionRefusedOnAJob:
		next = act(AddrFactionCrime).Named("faction.crime")
	default:
		next = act(AddrFactionMine).Named("faction.mine")
	}
	return screenFactionRefusal.Response(c.Lang, v, next, back(addrHome)).
		Refused(FactionRefusalCode(v.Kind), map[string]any{"min": v.Min, "max": v.Max, "amount": v.Amount,
			"balance": v.Balance, "need": v.Need, "have": v.Have, "level": v.Level})
}

// ---- diplomacy ----

// Sanctions is a country's sanctions board.
func Sanctions(c presentation.Ctx, v SanctionsView) *presentation.Response {
	var a []presentation.Action
	if v.CanImpose {
		for _, s := range v.Imposed {
			if s.Liftable {
				a = append(a, act(AddrLift, i64(s.No)).Named("diplomacy.lift"))
			}
		}
		a = append(a, act(AddrImpose).Named("diplomacy.impose"))
	}
	a = append(a, act(AddrTreaties, v.Country.Code).Named("diplomacy.treaties"),
		act(AddrDipHist, v.Country.Code).Named("diplomacy.history"),
		back(addrMinistry, v.Country.Code), refresh(AddrSanctions, v.Country.Code))
	return screenSanctions.Response(c.Lang, v, a...)
}

// Impose is the impose flow: choose the target, the measures, the ground, then
// confirm.
func Impose(c presentation.Ctx, v ImposeView) *presentation.Response {
	var a []presentation.Action
	b := back(AddrSanctions, v.Country.Code)
	mask := itoa(v.Mask)
	switch {
	case v.Target == nil:
		for _, t := range v.Targets {
			a = append(a, act(AddrImpose, t.Code).Named("diplomacy.target").About(t.Code))
		}
	case v.Ground == "" && len(v.Grounds) == 0:
		for _, m := range v.Measures {
			a = append(a, act(AddrImpose, v.Target.Code, itoa(m.Mask)).Named("diplomacy.measure").About(m.Code))
		}
		if v.Mask != 0 {
			a = append(a, act(AddrImpose, v.Target.Code, mask, ChooseGround).Named("diplomacy.next"))
		}
		b = back(AddrImpose)
	case v.Ground == "":
		for _, g := range v.Grounds {
			a = append(a, act(AddrImpose, v.Target.Code, mask, g).Named("diplomacy.ground").About(g))
		}
		b = back(AddrImpose, v.Target.Code, mask)
	default:
		a = append(a, confirm(AddrImpose, v.Target.Code, mask, v.Ground, DiplomacyConfirm).Named("diplomacy.impose.confirm"))
		b = back(AddrImpose, v.Target.Code, mask, ChooseGround)
	}
	a = append(a, b)
	return screenImpose.Response(c.Lang, v, a...)
}

// Lift asks the holder to confirm lifting a sanction.
func Lift(c presentation.Ctx, v LiftView) *presentation.Response {
	return screenLift.Response(c.Lang, v,
		confirm(AddrLift, i64(v.Sanction.No), DiplomacyConfirm).Named("diplomacy.lift.confirm"),
		back(AddrSanctions, v.Country.Code))
}

// Treaties is a country's treaties board.
func Treaties(c presentation.Ctx, v TreatiesView) *presentation.Response {
	var a []presentation.Action
	if v.CanAct {
		for _, t := range v.Treaties {
			no := i64(t.No)
			switch {
			case t.Status == "proposed" && t.Incoming:
				a = append(a, act(AddrAnswer, no, AnswerAccept).Named("diplomacy.accept"),
					act(AddrAnswer, no, AnswerDecline).Named("diplomacy.decline"))
			case t.Status == "proposed":
				a = append(a, act(AddrEndTreaty, no).Named("diplomacy.withdraw"))
			case t.Status == "active":
				a = append(a, act(AddrEndTreaty, no).Named("diplomacy.terminate").As(presentation.RoleDanger))
			}
		}
		a = append(a, act(AddrPropose).Named("diplomacy.propose"))
	}
	a = append(a, act(AddrSanctions, v.Country.Code).Named("diplomacy.sanctions"),
		act(AddrDipHist, v.Country.Code).Named("diplomacy.history"),
		back(addrMinistry, v.Country.Code), refresh(AddrTreaties, v.Country.Code))
	return screenTreaties.Response(c.Lang, v, a...)
}

// Propose is the propose flow: choose the partner, then the kind, then confirm.
func Propose(c presentation.Ctx, v ProposeView) *presentation.Response {
	var a []presentation.Action
	b := back(AddrTreaties, v.Country.Code)
	switch {
	case v.Partner == nil:
		for _, p := range v.Partners {
			a = append(a, act(AddrPropose, p.Code).Named("diplomacy.partner").About(p.Code))
		}
	case v.Kind == nil:
		for _, k := range v.Kinds {
			a = append(a, act(AddrPropose, v.Partner.Code, k.Code).Named("diplomacy.kind").About(k.Code))
		}
		b = back(AddrPropose)
	default:
		a = append(a, confirm(AddrPropose, v.Partner.Code, v.Kind.Code, DiplomacyConfirm).Named("diplomacy.propose.confirm"))
		b = back(AddrPropose, v.Partner.Code)
	}
	a = append(a, b)
	return screenPropose.Response(c.Lang, v, a...)
}

// EndTreaty asks the holder to confirm withdrawing a proposal or ending a treaty.
func EndTreaty(c presentation.Ctx, v EndTreatyView) *presentation.Response {
	id := "diplomacy.terminate.confirm"
	if v.Treaty.Status == "proposed" {
		id = "diplomacy.withdraw.confirm"
	}
	return screenEndTreaty.Response(c.Lang, v,
		confirm(AddrEndTreaty, i64(v.Treaty.No), DiplomacyConfirm).Named(id),
		back(AddrTreaties, v.Country.Code))
}

// DiplomacyHistory is one page of the public record.
func DiplomacyHistory(c presentation.Ctx, v DiplomacyHistoryView) *presentation.Response {
	a := pager(nil, AddrDipHist, []string{v.Country.Code}, v.Page, v.Pages)
	a = append(a, back(AddrSanctions, v.Country.Code), refresh(AddrDipHist, v.Country.Code, itoa(pageOr1(v.Page))))
	return screenDiplomacyHistory.Response(c.Lang, v, a...)
}

// DiplomacyRefusal is a refused diplomacy command.
func DiplomacyRefusal(c presentation.Ctx, v DiplomacyRefusalView) *presentation.Response {
	b := back(AddrGovCity)
	if v.Country.Code != "" {
		b = back(AddrSanctions, v.Country.Code)
	}
	if v.Back.Command != "" {
		b = presentation.Back(v.Back.Command, v.Back.Args...)
	}
	return screenDiplomacyRefusal.Response(c.Lang, v, b).
		Refused(DiplomacyRefusalCode(v.Kind), map[string]any{"office": v.Office})
}

// SanctionBlocked is a cross-border action a sanction blocked.
func SanctionBlocked(c presentation.Ctx, v SanctionBlockedView) *presentation.Response {
	b := back(addrHome)
	if v.Back.Command != "" {
		b = presentation.Back(v.Back.Command, v.Back.Args...)
	}
	return screenSanctionBlocked.Response(c.Lang, v, act(AddrSanctions, v.Imposer.Code).Named("diplomacy.sanctions"), b).
		Refused("sanction_blocked", map[string]any{"measure": v.Measure})
}

// ---- legislature ----

// Bills is the proposals of the player's places.
func Bills(c presentation.Ctx, v BillsView) *presentation.Response {
	var a []presentation.Action
	for _, b := range v.Bills {
		a = append(a, act(AddrBill, i64(b.No)).Named("law.view"))
	}
	a = append(a, back(AddrGovCity), refresh(AddrBills))
	return screenBills.Response(c.Lang, v, a...)
}

// Bill is one proposal.
func Bill(c presentation.Ctx, v BillView) *presentation.Response {
	no := i64(v.No)
	var a []presentation.Action
	if v.CanVote && v.Status == "open" {
		a = append(a, act(AddrBillVot, no, VoteYes).Named("law.vote.for"),
			act(AddrBillVot, no, VoteNo).Named("law.vote.against"))
	}
	a = append(a, act(AddrBills).Named("law.list"), back(AddrBills), refresh(AddrBill, no))
	return screenBill.Response(c.Lang, v, a...)
}

// BillRefusal is a refused legislature request.
func BillRefusal(c presentation.Ctx, v BillRefusalView) *presentation.Response {
	b := back(AddrBills)
	if v.No > 0 {
		b = back(AddrBill, i64(v.No))
	}
	return screenBillRefusal.Response(c.Lang, v, b).
		Refused(BillRefusalCode(v.Kind), map[string]any{"no": v.No, "body": v.Body})
}

// ---- people and boards ----

// Search is the answer to one search.
func Search(c presentation.Ctx, v SearchView) *presentation.Response {
	var a []presentation.Action
	if f := v.Found; !v.Help && f != nil && !f.Self {
		a = append(a, act(addrFriendAdd, f.ID).Named("social.add_friend"))
		if f.Code != "" {
			a = append(a, act(addrPay, f.Code).Named("social.pay"))
		}
	}
	a = append(a, back(addrHome), refresh(addrFriendList))
	return screenSearch.Response(c.Lang, v, a...)
}

// Friends is one page of the friend list.
func Friends(c presentation.Ctx, v FriendsView) *presentation.Response {
	var a []presentation.Action
	for _, f := range v.Friends {
		if f.Incoming {
			a = append(a, act(addrFriendAccept, f.ID).Named("social.accept"))
		}
	}
	if len(v.Friends) > 0 {
		a = pager(a, addrFriendList, nil, v.Page, v.Pages)
	}
	a = append(a, back(addrHome), refresh(addrFriendList, itoa(pageOr1(v.Page))))
	return screenFriends.Response(c.Lang, v, a...)
}

// FriendRequested confirms a sent request.
func FriendRequested(c presentation.Ctx, v FriendRequestedView) *presentation.Response {
	return screenFriendRequested.Response(c.Lang, v, back(addrHome), refresh(addrFriendList))
}

// FriendAccepted confirms an accepted request.
func FriendAccepted(c presentation.Ctx, v FriendAcceptedView) *presentation.Response {
	return screenFriendAccepted.Response(c.Lang, v, back(addrHome), refresh(addrFriendList))
}

// Boards the leaderboard offers, in tab order.
var Boards = []string{"richest", "companies", "cities", "workers", "investors"}

// Leaderboard is one board, and the way to the others.
func Leaderboard(c presentation.Ctx, v BoardView) *presentation.Response {
	var a []presentation.Action
	for _, b := range Boards {
		if b != v.Board {
			a = append(a, act(addrLifeTop, b).Named("board.tab").About(b))
		}
	}
	a = append(a, back(addrHome), refresh(addrLifeTop, v.Board))
	return screenLeaderboard.Response(c.Lang, v, a...)
}
