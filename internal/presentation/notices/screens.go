package notices

import (
	"strconv"

	"github.com/mrjvadi/torncity/internal/presentation"
)

// The notices, as the producers build them. Each constructor takes the facts
// a worker read from an event and returns a neutral response: the screen's
// name (the notice's code), the view, and the actions the player may take
// from it. Nothing here is worded, laid out or marked up.

// Applications and answers the faction notices distinguish.
const (
	factionApply = "apply"
)

var (
	screenPayment       = presentation.Define[PaymentView](ScreenPaymentNotice, "notices")
	screenAchievement   = presentation.Define[AchievementView](ScreenAchievementNotice, "notices")
	screenOffice        = presentation.Define[OfficeView](ScreenOfficeNotice, "notices")
	screenAuction       = presentation.Define[AuctionView](ScreenAuctionNotice, "notices", presentation.Private())
	screenVictim        = presentation.Define[VictimView](ScreenVictimNotice, "notices", presentation.Private())
	screenCaseSolved    = presentation.Define[CaseOutcomeView](ScreenCaseSolvedNotice, "notices", presentation.Private())
	screenConvicted     = presentation.Define[CaseOutcomeView](ScreenConvictedNotice, "notices", presentation.Private())
	screenTreaty        = presentation.Define[TreatyView](ScreenTreatyProposedNotice, "notices")
	screenElection      = presentation.Define[ElectionResultView](ScreenElectionResultNotice, "notices", presentation.Private())
	screenFactionReq    = presentation.Define[FactionRequestView](ScreenFactionRequestNotice, "notices", presentation.Private())
	screenFactionAnswer = presentation.Define[FactionAnswerView](ScreenFactionAnswerNotice, "notices", presentation.Private())
	screenFactionCrime  = presentation.Define[FactionCrimeView](ScreenFactionCrimeNotice, "notices", presentation.Private())
	screenFinance       = presentation.Define[FinanceView](ScreenFinanceNotice, "notices", presentation.Private())
	screenHospitalised  = presentation.Define[HospitalisedView](ScreenHospitalisedNotice, "notices", presentation.Private())
	screenClinicTreated = presentation.Define[ClinicTreatedView](ScreenClinicTreatedNotice, "notices", presentation.Private())
	screenBillDecided   = presentation.Define[BillDecidedView](ScreenBillDecidedNotice, "notices")
	screenRank          = presentation.Define[RankView](ScreenRankNotice, "notices")
	screenHunger        = presentation.Define[EmptyView](ScreenHungerNotice, "notices")
	screenMarketFilled  = presentation.Define[MarketFilledView](ScreenMarketFilledNotice, "notices", presentation.Private())
	screenMission       = presentation.Define[MissionCompletedView](ScreenMissionCompleted, "notices", presentation.Private())
	screenProperty      = presentation.Define[PropertyView](ScreenPropertyNotice, "notices")
	screenRecruit       = presentation.Define[RecruitView](ScreenRecruitNotice, "notices", presentation.Private())
	screenStock         = presentation.Define[StockView](ScreenStockNotice, "notices", presentation.Private())
	screenVillageNews   = presentation.Define[VillageNewsView](ScreenVillageNews, "notices")
	screenInboxBadge    = presentation.Define[InboxBadgeView](ScreenInboxBadge, "notices", presentation.Private())
	screenInboxHub      = presentation.Define[InboxHubView](ScreenInboxHub, "notices", presentation.Private())
	screenInboxCategory = presentation.Define[InboxCategoryView](ScreenInboxCategory, "notices", presentation.Private())
	screenInboxReminder = presentation.Define[InboxReminderView](ScreenInboxReminder, "notices", presentation.Private())
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

func no(n int64) string { return strconv.FormatInt(n, 10) }

// PaymentNotice tells a payee about money they received.
func PaymentNotice(c presentation.Ctx, v PaymentView) *presentation.Response {
	return screenPayment.Response(c.Lang, v,
		act(AddrBank).Named("notice.bank"), act(AddrProfile).Named("notice.profile"))
}

// AchievementNotice tells a player they earned an achievement.
func AchievementNotice(c presentation.Ctx, v AchievementView) *presentation.Response {
	return screenAchievement.Response(c.Lang, v, act(AddrAchievements).Named("notice.achievements"), back(AddrHome))
}

// OfficeNotice tells a player they were seated in, or removed from, an office.
func OfficeNotice(c presentation.Ctx, v OfficeView) *presentation.Response {
	return screenOffice.Response(c.Lang, v, act(AddrGovOffice).Named("notice.office"), back("gov:city"))
}

// AuctionNotice tells a player about an auction they sell or bid on.
func AuctionNotice(c presentation.Ctx, v AuctionView) *presentation.Response {
	if v.Kind == "outbid" {
		return screenAuction.Response(c.Lang, v, act(AddrAuction, no(v.No)).Named("notice.auction"))
	}
	return screenAuction.Response(c.Lang, v, act(AddrInventory).Named("notice.bag"))
}

// VictimNotice tells a player they were robbed and offers the report.
func VictimNotice(c presentation.Ctx, v VictimView) *presentation.Response {
	return screenVictim.Response(c.Lang, v, act(AddrCrimeReport, v.CrimeID).Named("notice.report"))
}

// CaseSolvedNotice tells a victim how their case ended.
func CaseSolvedNotice(c presentation.Ctx, v CaseOutcomeView) *presentation.Response {
	return screenCaseSolved.Response(c.Lang, v, act(AddrCrimeCases).Named("notice.cases"))
}

// ConvictedNotice tells a thief a reported theft was traced to them.
func ConvictedNotice(c presentation.Ctx, v CaseOutcomeView) *presentation.Response {
	return screenConvicted.Response(c.Lang, v, act(AddrCrimeJail).Named("notice.jail"))
}

// TreatyProposedNotice tells a foreign minister of a treaty proposed.
func TreatyProposedNotice(c presentation.Ctx, v TreatyView) *presentation.Response {
	return screenTreaty.Response(c.Lang, v,
		act(AddrTreatyAnswer, no(v.No), AnswerAccept).Named("notice.accept").As(presentation.RolePrimary),
		act(AddrTreatyAnswer, no(v.No), AnswerDecline).Named("notice.decline"),
		back(AddrTreaties, v.Country.Code))
}

// ElectionResultNotice tells a candidate how their election went.
func ElectionResultNotice(c presentation.Ctx, v ElectionResultView) *presentation.Response {
	return screenElection.Response(c.Lang, v, act(AddrElection, no(v.No)).Named("notice.election"))
}

// FactionRequestNotice tells a player they were invited, or an officer that
// someone applied, with the actions to answer.
func FactionRequestNotice(c presentation.Ctx, v FactionRequestView) *presentation.Response {
	return screenFactionReq.Response(c.Lang, v,
		act(AddrFactionAnswer, no(v.No), AnswerAccept).Named("notice.accept").As(presentation.RolePrimary),
		act(AddrFactionAnswer, no(v.No), AnswerDecline).Named("notice.decline"))
}

// FactionAnswerNotice tells the other side how a request was answered.
func FactionAnswerNotice(c presentation.Ctx, v FactionAnswerView) *presentation.Response {
	if v.Accepted && v.Kind == factionApply {
		return screenFactionAnswer.Response(c.Lang, v, act(AddrFactionMine).Named("notice.faction_mine"))
	}
	return screenFactionAnswer.Response(c.Lang, v, act(AddrFactions).Named("notice.factions"))
}

// FactionCrimeNotice tells a crew member how an organised crime ended.
func FactionCrimeNotice(c presentation.Ctx, v FactionCrimeView) *presentation.Response {
	switch {
	case v.Injury != nil && v.Injury.Hospital:
		return screenFactionCrime.Response(c.Lang, v, act(AddrHospital).Named("notice.hospital"))
	case v.Jail != nil:
		return screenFactionCrime.Response(c.Lang, v, act(AddrCrimeJail).Named("notice.jail"))
	}
	return screenFactionCrime.Response(c.Lang, v, act(AddrFactionCrime).Named("notice.faction_crime"))
}

// FinanceNotice tells a player of the bank or the insurance fund.
func FinanceNotice(c presentation.Ctx, v FinanceView) *presentation.Response {
	switch v.Kind {
	case "claimed", "lapsed", "gone":
		return screenFinance.Response(c.Lang, v, act(AddrInsurance).Named("notice.insurance"), back(AddrHome))
	}
	var a []presentation.Action
	if v.Kind != "defaulted" && v.Kind != "repaid" {
		a = append(a, act(AddrLoanView, no(v.No)).Named("notice.loan").About(v.Product.Code))
	}
	a = append(a, act(AddrLoanHub).Named("notice.loans"), back(AddrHome))
	return screenFinance.Response(c.Lang, v, a...)
}

// HospitalisedNotice tells a player they were taken to hospital.
func HospitalisedNotice(c presentation.Ctx, v HospitalisedView) *presentation.Response {
	return screenHospitalised.Response(c.Lang, v, act(AddrHospital).Named("notice.hospital"))
}

// ClinicTreatedNotice tells a clinic's owner it treated a patient.
func ClinicTreatedNotice(c presentation.Ctx, v ClinicTreatedView) *presentation.Response {
	return screenClinicTreated.Response(c.Lang, v, act(AddrClinicDesk, v.Ref.Code).Named("notice.clinic"))
}

// BillDecidedNotice tells the member who proposed a bill how it went.
func BillDecidedNotice(c presentation.Ctx, v BillDecidedView) *presentation.Response {
	return screenBillDecided.Response(c.Lang, v, act(AddrBill, no(v.No)).Named("notice.bill"), back(AddrBills))
}

// RankNotice tells a player their rank rose or fell.
func RankNotice(c presentation.Ctx, v RankView) *presentation.Response {
	return screenRank.Response(c.Lang, v, act(AddrLife).Named("notice.life"), back(AddrHome))
}

// HungerNotice is the urgent hunger nudge, with the two ways to act on it at
// once: eat something already carried, or go buy food.
func HungerNotice(c presentation.Ctx) *presentation.Response {
	return screenHunger.Response(c.Lang, EmptyView{},
		act(AddrInventory).Named("notice.eat"), act(AddrShops).Named("notice.shops"))
}

// MarketFilledNotice tells an order's owner it traded.
func MarketFilledNotice(c presentation.Ctx, v MarketFilledView) *presentation.Response {
	return screenMarketFilled.Response(c.Lang, v, act(AddrMarketMine).Named("notice.orders"))
}

// MissionCompletedNotice tells a player a mission is complete and paid.
func MissionCompletedNotice(c presentation.Ctx, v MissionCompletedView) *presentation.Response {
	return screenMission.Response(c.Lang, v, act(AddrMissions).Named("notice.missions"))
}

// PropertyNotice tells a player what happened to their property or home.
func PropertyNotice(c presentation.Ctx, v PropertyView) *presentation.Response {
	return screenProperty.Response(c.Lang, v, act(AddrPropertyMine).Named("notice.property"), back(AddrHome))
}

// RecruitNotice tells a company's owner about a campaign or a specialist.
func RecruitNotice(c presentation.Ctx, v RecruitView) *presentation.Response {
	switch v.Kind {
	case "applied", "ended", "filled":
		return screenRecruit.Response(c.Lang, v, act(AddrRecruitCamp, no(v.CampaignNo)).Named("notice.campaign"))
	}
	return screenRecruit.Response(c.Lang, v, act(AddrSpecialists, v.Company.Code).Named("notice.staff"))
}

// StockNotice tells a player of the exchange.
func StockNotice(c presentation.Ctx, v StockView) *presentation.Response {
	return screenStock.Response(c.Lang, v,
		act(AddrStock, v.Company.Code).Named("notice.stock").About(v.Company.Code),
		act(AddrPortfolio).Named("notice.portfolio"), back(AddrHome))
}

// MaxNewsLines bounds a merged village-news list; the rest is counted.
const MaxNewsLines = 6

// The kinds of village news.
const (
	NewsBuilt          = "built"
	NewsBuildStarted   = "build_started"
	NewsResearched     = "researched"
	NewsBought         = "bought"
	NewsTaught         = "taught"
	NewsResidentJoined = "resident_joined"
	NewsDonated        = "donated"
	NewsPromoted       = "promoted"
)

// VillageNews is the post a village's group reads: one event, or a short
// list. Its one action opens the screen the news is about: the construction
// progress for construction, the knowledge list for research and purchases,
// the village overview for anything else or a mix.
func VillageNews(c presentation.Ctx, v VillageNewsView) *presentation.Response {
	addr, id := AddrVillageOverview, "notice.village_overview"
	switch newsKindOf(v.Items) {
	case NewsBuilt, NewsBuildStarted:
		addr, id = AddrConstructionProgress, "notice.village_progress"
	case NewsResearched, NewsBought:
		addr, id = AddrKnowledgeList, "notice.village_knowledge"
	}
	return screenVillageNews.Response(c.Lang, v, act(addr).Named(id))
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

// InboxBadge is the one message a player's unread count is edited onto.
func InboxBadge(c presentation.Ctx, v InboxBadgeView) *presentation.Response {
	if v.Unread == 0 {
		return screenInboxBadge.Response(c.Lang, v, back(AddrHome))
	}
	return screenInboxBadge.Response(c.Lang, v, act(AddrInboxShow).Named("inbox.open"))
}

// InboxHub is the inbox opened: every category with unread items.
func InboxHub(c presentation.Ctx, v InboxHubView) *presentation.Response {
	var a []presentation.Action
	for _, cat := range v.Categories {
		a = append(a, act(AddrInboxCategory, cat.Category, "1").Named("inbox.category").About(cat.Category))
	}
	if len(v.Categories) > 0 {
		a = append(a, act(AddrInboxReadAll).Named("inbox.read_all"))
	}
	a = append(a, back(AddrHome), refresh(AddrInboxShow))
	return screenInboxHub.Response(c.Lang, v, a...)
}

// InboxCategory is one category's compact, paginated list.
func InboxCategory(c presentation.Ctx, v InboxCategoryView) *presentation.Response {
	var a []presentation.Action
	for _, it := range v.Items {
		if it.Link.Command != "" {
			a = append(a, presentation.Do(it.Link.Command, it.Link.Args...).Named("inbox.open_item"))
		}
	}
	if v.Page > 1 {
		a = append(a, act(AddrInboxCategory, v.Category, strconv.Itoa(v.Page-1)).Named("inbox.prev"))
	}
	if v.Page < v.TotalPages {
		a = append(a, act(AddrInboxCategory, v.Category, strconv.Itoa(v.Page+1)).Named("inbox.next"))
	}
	a = append(a, back(AddrInboxShow), refresh(AddrInboxCategory, v.Category, strconv.Itoa(max(v.Page, 1))))
	return screenInboxCategory.Response(c.Lang, v, a...)
}

// InboxReminder is the nudge for a pile left unread.
func InboxReminder(c presentation.Ctx, v InboxReminderView) *presentation.Response {
	return screenInboxReminder.Response(c.Lang, v, act(AddrInboxShow).Named("inbox.open"))
}
