package military

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/society"
)

// The military area's screens, as the core builds them (docs/adr/0039). Each
// constructor takes the view a handler worked out and returns a neutral
// response: the screen's name, the view and the actions the viewer may take
// next. Nothing here is worded, laid out or marked up; the Telegram edge
// (internal/telegram/render) and the web client each do that themselves.
//
// The actions are what the viewer MAY do, whatever an edge decides to show:
// Telegram leaves the commander's buttons out of a group screen, the web places
// the ones its layout has room for.

// Screens, each defined once with the type of its view.
var (
	screenMinistry   = presentation.Define[MinistryView](ScreenMinistry, "military")
	screenForces     = presentation.Define[ForcesView](ScreenForces, "military")
	screenBranch     = presentation.Define[BranchView](ScreenBranch, "military", presentation.Private())
	screenStation    = presentation.Define[StationView](ScreenStation, "military", presentation.Private())
	screenProcure    = presentation.Define[ProcureView](ScreenProcure, "military", presentation.Private())
	screenArmsBuy    = presentation.Define[ArmsBuyView](ScreenArmsBuy, "military", presentation.Private())
	screenMilitaryNo = presentation.Define[MilitaryRefusalView](ScreenMilitaryRefusal, "military", presentation.Refusal())
	screenWarBoard   = presentation.Define[WarBoardView](ScreenWarBoard, "military")
	screenDeclare    = presentation.Define[DeclareView](ScreenWarDeclare, "military", presentation.Private())
	screenDecision   = presentation.Define[WarDecisionView](ScreenWarDecision, "military", presentation.Private())
	screenWarRoom    = presentation.Define[WarRoomView](ScreenWarRoom, "military", presentation.Private())
	screenWarTarget  = presentation.Define[WarTargetView](ScreenWarTarget, "military", presentation.Private())
	screenWarLaunch  = presentation.Define[LaunchView](ScreenWarLaunch, "military", presentation.Private())
	screenStrike     = presentation.Define[StrikeReportView](ScreenStrikeReport, "military", presentation.Private())
	screenWarNo      = presentation.Define[WarRefusalView](ScreenWarRefusal, "military", presentation.Refusal())
	screenWarBlocked = presentation.Define[WarBlockedView](ScreenWarBlocked, "military", presentation.Refusal())
	screenCompanyDef = presentation.Define[CompanyDefenceView](ScreenCompanyDefence, "military", presentation.Private())
	screenLicences   = presentation.Define[LicencesView](ScreenLicences, "military")
	screenKitBuy     = presentation.Define[KitPurchaseView](ScreenKitPurchase, "military", presentation.Private())
	screenRetrofit   = presentation.Define[StateRetrofitView](ScreenStateRetrofit, "military", presentation.Private())
	screenArrived    = presentation.Define[MilitaryNoticeView](ScreenMoveArrivedNotice, "military", presentation.Private())
	screenLicenceNtc = presentation.Define[LicenceNoticeView](ScreenLicenceNotice, "military", presentation.Private())
	screenWarNotice  = presentation.Define[WarNoticeView](ScreenWarNotice, "military", presentation.Private())
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

// ask is the action whose last value the player types.
func ask(command string, args ...string) presentation.Action {
	return presentation.Do(command, args...).Asking()
}

func n(v int64) string { return strconv.FormatInt(v, 10) }

// qtyChoices are the quantities offered: the presets that fit within max, then
// max itself, once each. afford, when set, also drops a quantity it refuses.
func qtyChoices(presets []int64, max int64, afford func(int64) bool) []int64 {
	var out []int64
	seen := map[int64]bool{}
	for _, q := range append(append([]int64(nil), presets...), max) {
		if q < 1 || q > max || seen[q] || (afford != nil && !afford(q)) {
			continue
		}
		seen[q] = true
		out = append(out, q)
	}
	return out
}

// ---- the ministry, the forces and stationing ----

// Ministry is a country's ministry of defence.
func Ministry(c presentation.Ctx, v MinistryView) *presentation.Response {
	if v.Unavailable != nil {
		return screenMinistry.Response(c.Lang, v, v.Unavailable.Actions(AddrHome)...)
	}
	code := v.Country.Code
	a := []presentation.Action{act(AddrForces, code).Named("military.forces")}
	if v.CanProcure {
		a = append(a, act(AddrProcure, code).Named("military.procure"))
	}
	a = append(a,
		act(society.AddrSanctions, code).Named("military.sanctions"),
		act(society.AddrTreaties, code).Named("military.treaties"),
		act(AddrWarBoard, code).Named("military.war"),
		act(AddrLicences, code).Named("military.licences"),
		back(AddrGovCity), refresh(AddrMinistry, code))
	return screenMinistry.Response(c.Lang, v, a...)
}

// Forces is a country's forces by branch.
func Forces(c presentation.Ctx, v ForcesView) *presentation.Response {
	if v.Unavailable != nil {
		return screenForces.Response(c.Lang, v, v.Unavailable.Actions(AddrHome)...)
	}
	code := v.Country.Code
	var a []presentation.Action
	if v.Cleared {
		for _, b := range v.Branches {
			a = append(a, act(AddrBranch, code, b.Branch.Code).Named("military.branch").About(b.Branch.Code))
		}
	}
	a = append(a, back(AddrMinistry, code), refresh(AddrForces, code))
	return screenForces.Response(c.Lang, v, a...)
}

// Branch is one branch's equipment and where it stands, for a cleared viewer.
func Branch(c presentation.Ctx, v BranchView) *presentation.Response {
	code := v.Country.Code
	var a []presentation.Action
	if v.CanStation {
		for _, g := range v.Groups {
			if g.Count > g.Moving {
				a = append(a, act(AddrStation, code, g.Good.TargetArg()).Named("military.station").About(g.Good.Item.Code))
			}
		}
	}
	a = append(a, back(AddrForces, code), refresh(AddrBranch, code, v.Branch.Code))
	return screenBranch.Response(c.Lang, v, a...)
}

// Station is ordering equipment to a garrison: the city, how many, confirm.
func Station(c presentation.Ctx, v StationView) *presentation.Response {
	code, good := v.Country.Code, v.Good.TargetArg()
	var a []presentation.Action
	switch {
	case v.CityCode == "":
		for _, city := range v.Cities {
			a = append(a, act(AddrStation, code, good, city.Code).Named("military.station_city").About(city.Code))
		}
		a = append(a, back(AddrBranch, code, v.Branch.Code))
	case !v.Confirm:
		for _, q := range qtyChoices(StationQtyChoices, v.Available, nil) {
			a = append(a, act(AddrStation, code, good, v.CityCode, n(q)).Named("military.station_qty"))
		}
		if v.Available > 0 {
			a = append(a, ask("military.station", code, good, v.CityCode).Named("military.station_custom"))
		}
		a = append(a, back(AddrStation, code, good))
	default:
		a = append(a, confirm(AddrStation, code, good, v.CityCode, n(v.Qty), MilitaryConfirm).Named("military.station_confirm"),
			back(AddrStation, code, good, v.CityCode))
	}
	return screenStation.Response(c.Lang, v, a...)
}

// ---- procurement ----

// Procure is the military goods for sale that the state may buy.
func Procure(c presentation.Ctx, v ProcureView) *presentation.Response {
	if v.Unavailable != nil {
		return screenProcure.Response(c.Lang, v, v.Unavailable.Actions(AddrHome)...)
	}
	code := v.Country.Code
	var a []presentation.Action
	for _, o := range v.Offers {
		if o.Blocked == "" {
			a = append(a, act(AddrArmsBuy, code, n(o.No)).Named("military.buy_open").About(o.Good.Item.Code))
		}
	}
	a = append(a, back(AddrMinistry, code), refresh(AddrProcure, code))
	return screenProcure.Response(c.Lang, v, a...)
}

// ArmsBuy is one listing a state may buy from: how many, then confirm.
func ArmsBuy(c presentation.Ctx, v ArmsBuyView) *presentation.Response {
	code, no := v.Country.Code, n(v.Offer.No)
	var a []presentation.Action
	if v.Confirm {
		a = append(a, confirm(AddrArmsBuy, code, no, n(v.Qty), MilitaryConfirm).Named("military.buy_confirm"),
			back(AddrArmsBuy, code, no))
		return screenArmsBuy.Response(c.Lang, v, a...)
	}
	o := v.Offer
	for _, q := range qtyChoices(BuyQtyChoices, o.Left, func(q int64) bool { return q*o.Price <= v.Fund }) {
		a = append(a, act(AddrArmsBuy, code, no, n(q)).Named("military.buy_qty"))
	}
	if o.Left > 0 && o.Price <= v.Fund {
		a = append(a, ask("military.buy", code, no).Named("military.buy_custom"))
	}
	a = append(a, back(AddrProcure, code))
	return screenArmsBuy.Response(c.Lang, v, a...)
}

// MilitaryRefusal is a refused military command.
func MilitaryRefusal(c presentation.Ctx, v MilitaryRefusalView) *presentation.Response {
	fallback := presentation.RefOfAddress(AddrGovCity)
	if v.Country.Code != "" {
		fallback = presentation.Ref{Command: "military.ministry", Args: []string{v.Country.Code}}
	}
	args := map[string]any{}
	if v.Country.Code != "" {
		args["country"] = v.Country.Code
	}
	if v.Office != "" {
		args["office"] = v.Office
	}
	if v.Need != 0 {
		args["need"] = v.Need
	}
	if v.Have != 0 {
		args["have"] = v.Have
	}
	if v.Max != 0 {
		args["max"] = v.Max
	}
	if len(args) == 0 {
		args = nil
	}
	return screenMilitaryNo.Response(c.Lang, v, presentation.BackTo(v.Back, fallback)).
		Refused(RefusalCode("military", v.Kind), args)
}

// MoveArrivedNotice tells the commander equipment reached its garrison.
func MoveArrivedNotice(c presentation.Ctx, v MilitaryNoticeView) *presentation.Response {
	return screenArrived.Response(c.Lang, v,
		act(AddrBranch, v.Country.Code, v.Branch.Code).Named("military.branch").About(v.Branch.Code),
		back(AddrForces, v.Country.Code))
}

// ---- the war board and the flows of war ----

// WarBoard is the war board of a country.
func WarBoard(c presentation.Ctx, v WarBoardView) *presentation.Response {
	if v.Unavailable != nil {
		return screenWarBoard.Response(c.Lang, v, v.Unavailable.Actions(AddrHome)...)
	}
	code := v.Country.Code
	var a []presentation.Action
	for _, w := range v.Wars {
		no := n(w.No)
		for _, p := range w.Proposals {
			if p.Incoming && w.CanPropose {
				a = append(a,
					act(AddrWarAnswer, n(p.No), society.AnswerAccept).Named("war.accept").As(presentation.RolePrimary),
					act(AddrWarAnswer, n(p.No), society.AnswerDecline).Named("war.decline"))
			}
		}
		if w.CanPropose {
			if w.Status == "active" || w.Status == "declared" {
				a = append(a, act(AddrWarPropose, no, "ceasefire").Named("war.ceasefire"))
			}
			a = append(a, act(AddrWarPropose, no, "peace").Named("war.peace"))
		}
		if w.CanResume {
			a = append(a, act(AddrWarResume, no).Named("war.resume"))
		}
	}
	if v.CanDeclare {
		for _, j := range v.Joinable {
			a = append(a, act(AddrWarJoin, n(j.WarNo)).Named("war.join"))
		}
	}
	if v.CanCommand {
		a = append(a, act(AddrWarRoom).Named("war.room"))
	}
	if v.CanDeclare {
		a = append(a, act(AddrWarDeclare).Named("war.declare"))
	}
	a = append(a, back(AddrMinistry, code), refresh(AddrWarBoard, code))
	return screenWarBoard.Response(c.Lang, v, a...)
}

// Declare is the flow that declares a war: the country, the ground, confirm.
func Declare(c presentation.Ctx, v DeclareView) *presentation.Response {
	if v.Unavailable != nil {
		return screenDeclare.Response(c.Lang, v, v.Unavailable.Actions(AddrHome)...)
	}
	var a []presentation.Action
	switch {
	case v.Target == nil:
		for _, t := range v.Targets {
			a = append(a, act(AddrWarDeclare, t.Code).Named("war.declare_target").About(t.Code))
		}
		a = append(a, back(AddrWarBoard, v.Country.Code))
	case v.Ground == "":
		for _, g := range v.Grounds {
			a = append(a, act(AddrWarDeclare, v.Target.Code, g).Named("war.declare_ground").About(g))
		}
		a = append(a, back(AddrWarDeclare))
	default:
		a = append(a, confirm(AddrWarDeclare, v.Target.Code, v.Ground, WarConfirm).Named("war.declare_confirm"),
			back(AddrWarDeclare, v.Target.Code))
	}
	return screenDeclare.Response(c.Lang, v, a...)
}

// WarDecision confirms joining a war, proposing a ceasefire or a peace, or
// resuming a war.
func WarDecision(c presentation.Ctx, v WarDecisionView) *presentation.Response {
	no := n(v.WarNo)
	var yes presentation.Action
	switch v.Kind {
	case "join":
		yes = confirm(AddrWarJoin, no, WarConfirm).Named("war.join_confirm")
	case "resume":
		yes = confirm(AddrWarResume, no, WarConfirm).Named("war.resume_confirm")
	default:
		yes = confirm(AddrWarPropose, no, v.Kind, WarConfirm).Named("war.propose_confirm").About(v.Kind)
	}
	return screenDecision.Response(c.Lang, v, yes, back(AddrWarBoard, v.Country.Code))
}

// WarRoom is the war room: the enemy's cities and what is under way.
func WarRoom(c presentation.Ctx, v WarRoomView) *presentation.Response {
	if v.Unavailable != nil {
		return screenWarRoom.Response(c.Lang, v, v.Unavailable.Actions(AddrHome)...)
	}
	var a []presentation.Action
	for _, t := range v.Targets {
		a = append(a, act(AddrWarTarget, t.CityCode).Named("war.target").About(t.CityCode))
	}
	a = append(a, back(AddrWarBoard, v.Country.Code), refresh(AddrWarRoom))
	return screenWarRoom.Response(c.Lang, v, a...)
}

// WarTarget is one enemy city and the operations in reach of it.
func WarTarget(c presentation.Ctx, v WarTargetView) *presentation.Response {
	city := v.Target.CityCode
	var a []presentation.Action
	for _, o := range v.Options {
		if o.CanLaunch {
			a = append(a, act(AddrWarLaunch, city, o.Kind, o.TargetArg()).Named("war.launch_open").About(o.Kind))
		}
	}
	a = append(a, back(AddrWarRoom), refresh(AddrWarTarget, city))
	return screenWarTarget.Response(c.Lang, v, a...)
}

// WarLaunch is launching an operation: the objective, how many, then the
// estimate and confirm.
func WarLaunch(c presentation.Ctx, v LaunchView) *presentation.Response {
	city, kind, class := v.Target.CityCode, v.Option.Kind, v.Option.TargetArg()
	var a []presentation.Action
	switch {
	case v.Objective == "":
		for _, obj := range v.Objectives {
			a = append(a, act(AddrWarLaunch, city, kind, class, obj).Named("war.launch_objective").About(obj))
		}
		a = append(a, back(AddrWarTarget, city))
	case !v.Confirm:
		for _, q := range v.Quantities {
			a = append(a, act(AddrWarLaunch, city, kind, class, v.Objective, n(q)).Named("war.launch_qty"))
		}
		a = append(a, back(AddrWarLaunch, city, kind, class))
	default:
		a = append(a, confirm(AddrWarLaunch, city, kind, class, v.Objective, n(v.Qty), WarConfirm).Named("war.launch_confirm"))
		if kind != "ground" {
			a = append(a, back(AddrWarLaunch, city, kind, class, v.Objective))
		} else {
			a = append(a, back(AddrWarTarget, city))
		}
	}
	return screenWarLaunch.Response(c.Lang, v, a...)
}

// StrikeReport is an operation's report, exact, for the commander who launched
// it and the defender's head of state.
func StrikeReport(c presentation.Ctx, v StrikeReportView) *presentation.Response {
	var a []presentation.Action
	if v.Ours {
		a = append(a, act(AddrWarRoom).Named("war.room"))
	}
	a = append(a, back(AddrWarBoard, v.Country.Code))
	return screenStrike.Response(c.Lang, v, a...)
}

// WarNotice tells a player something about a war.
func WarNotice(c presentation.Ctx, v WarNoticeView) *presentation.Response {
	var a []presentation.Action
	switch v.Kind {
	case "ally":
		a = append(a, act(AddrWarJoin, n(v.WarNo)).Named("war.join"))
	case "proposal":
		a = append(a,
			act(AddrWarAnswer, n(v.ProposalNo), society.AnswerAccept).Named("war.accept").As(presentation.RolePrimary),
			act(AddrWarAnswer, n(v.ProposalNo), society.AnswerDecline).Named("war.decline"))
	}
	if v.Injury != nil && v.Injury.Hospital {
		a = append(a, act(AddrHospital).Named("war.hospital"))
	}
	a = append(a, back(AddrWarBoard, v.Country.Code))
	return screenWarNotice.Response(c.Lang, v, a...)
}

// WarRefusal is a refused decision of war.
func WarRefusal(c presentation.Ctx, v WarRefusalView) *presentation.Response {
	fallback := presentation.RefOfAddress(AddrWarBoard)
	if v.Country.Code != "" {
		fallback.Args = []string{v.Country.Code}
	}
	args := map[string]any{}
	if v.Country.Code != "" {
		args["country"] = v.Country.Code
	}
	if v.Office != "" {
		args["office"] = v.Office
	}
	if v.In > 0 {
		args["remaining_seconds"] = int64((v.In + 999999999) / 1e9)
	}
	if v.Max != 0 {
		args["max"] = v.Max
	}
	if len(args) == 0 {
		args = nil
	}
	return screenWarNo.Response(c.Lang, v, presentation.BackTo(v.Back, fallback)).
		Refused(RefusalCode("war", v.Kind), args)
}

// WarBlocked is a journey the war closes.
func WarBlocked(c presentation.Ctx, v WarBlockedView) *presentation.Response {
	kind := "city"
	args := map[string]any{"city": v.CityCode, "remaining_seconds": int64((v.In + 999999999) / 1e9)}
	if v.Border {
		kind = "border"
		args = map[string]any{"from": v.From.Code, "to": v.To.Code}
	}
	return screenWarBlocked.Response(c.Lang, v, presentation.BackTo(v.Back, presentation.RefOfAddress(AddrHome))).
		Refused(RefusalCode("war_blocked", kind), args)
}

// ---- defence licences ----

// CompanyDefence is a company's defence licence screen.
func CompanyDefence(c presentation.Ctx, v CompanyDefenceView) *presentation.Response {
	var a []presentation.Action
	if !v.Manufacturer {
		switch {
		case v.CanApply:
			a = append(a, act(AddrCompanyDefence, v.Ref.Code, "yes").Named("defence.apply").As(presentation.RolePrimary))
		case v.Licence == nil || v.Licence.Status == "rejected" || v.Licence.Status == "revoked":
			a = append(a, act(AddrLab, v.Ref.Code).Named("defence.lab"))
		}
	}
	a = append(a, back(AddrCompanyManage, v.Ref.Code), refresh(AddrCompanyDefence, v.Ref.Code))
	return screenCompanyDef.Response(c.Lang, v, a...)
}

// Licences is a country's public registry of defence licences.
func Licences(c presentation.Ctx, v LicencesView) *presentation.Response {
	if v.Unavailable != nil {
		return screenLicences.Response(c.Lang, v, v.Unavailable.Actions(AddrHome)...)
	}
	code := v.Country.Code
	if e := v.Confirm; e != nil {
		return screenLicences.Response(c.Lang, v,
			confirm(AddrLicence, n(e.No), LicenceRevoke, MilitaryConfirm).Named("defence.revoke_confirm"),
			back(AddrLicences, code))
	}
	var a []presentation.Action
	if v.CanDecide {
		for _, e := range v.Pending {
			a = append(a,
				act(AddrLicence, n(e.No), LicenceApprove).Named("defence.approve").As(presentation.RolePrimary),
				act(AddrLicence, n(e.No), LicenceReject).Named("defence.reject").As(presentation.RoleDanger))
		}
		for _, e := range v.InForce {
			if e.Status == "active" {
				a = append(a, act(AddrLicence, n(e.No), LicenceRevoke).Named("defence.revoke").As(presentation.RoleDanger))
			}
		}
	}
	a = append(a, back(AddrMinistry, code), refresh(AddrLicences, code))
	return screenLicences.Response(c.Lang, v, a...)
}

// LicenceNotice tells a minister of an application, or an owner of a verdict.
func LicenceNotice(c presentation.Ctx, v LicenceNoticeView) *presentation.Response {
	var a []presentation.Action
	if v.Kind == "applied" {
		a = append(a, act(AddrLicences, v.Country.Code).Named("defence.registry"))
	} else {
		a = append(a, act(AddrCompanyDefence, v.Company.Code).Named("defence.company"))
	}
	a = append(a, back(AddrHome))
	return screenLicenceNtc.Response(c.Lang, v, a...)
}

// ---- upgrade kits and retrofits ----

// KitPurchase is the plan or the result of buying upgrade kits for the state.
func KitPurchase(c presentation.Ctx, v KitPurchaseView) *presentation.Response {
	return screenKitBuy.Response(c.Lang, v, act(AddrProcure, v.Country).Named("military.procure"), back(AddrMinistry, v.Country))
}

// StateRetrofit is the plan of a retrofit of a state asset, or its start.
func StateRetrofit(c presentation.Ctx, v StateRetrofitView) *presentation.Response {
	var a []presentation.Action
	if !v.Started {
		a = append(a, presentation.Confirm("military.retrofit", v.Country, v.KitSerial, v.TargetSerial, MilitaryConfirm).
			Named("military.retrofit_confirm"))
	}
	a = append(a, back(AddrForces, v.Country))
	return screenRetrofit.Response(c.Lang, v, a...)
}
