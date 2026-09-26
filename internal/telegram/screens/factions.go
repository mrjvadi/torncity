package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Factions (docs/adr/0023-health-missions-factions.md): the factions of a
// city and a faction's page, founding one, a member's faction screen, its
// members, invitations and applications, its bank, its group, and its
// organised crimes; the notices and the lines its group reads.

// Addresses of the faction screens.
const (
	AddrFactions        = "faction:list"
	AddrFaction         = "faction:view"
	AddrFactionFound    = "faction:found"
	AddrFactionMine     = "faction:mine"
	AddrFactionMembers  = "faction:members"
	AddrFactionApply    = "faction:apply"
	AddrFactionAnswer   = "faction:answer"
	AddrFactionKick     = "faction:kick"
	AddrFactionRank     = "faction:rank"
	AddrFactionLeave    = "faction:leave"
	AddrFactionBank     = "faction:bank"
	AddrFactionCrime    = "faction:crime"
	AddrFactionPlan     = "faction:plan"
	AddrFactionJoin     = "faction:join"
	AddrFactionLaunch   = "faction:launch"
	AddrFactionCallOff  = "faction:calloff"
	AddrFactionLink     = "faction:link"
	commandFactionFound = "faction.found"
	commandFactionInv   = "faction.invite"
	commandFactionDep   = "faction.deposit"
	commandFactionWd    = "faction.withdraw"
)

// Answers a faction button carries.
const (
	FactionYes     = "yes"
	FactionAccept  = "accept"
	FactionDecline = "decline"
)

// FactionRef names a faction: its name and public code.
type FactionRef struct {
	Code string
	Name string
}

func (c Context) factionName(r FactionRef) string {
	return c.T("faction.name", map[string]any{"name": r.Name, "code": r.Code})
}

// rankName names a rank.
func (c Context) rankName(rank string) string { return c.T("faction.rank."+rank, nil) }

// FactionLine is one faction of a list.
type FactionLine struct {
	Ref     FactionRef
	Members int
}

// FactionListView is the factions of the player's city.
type FactionListView struct {
	CityCode, City string
	Fee            int64
	Factions       []FactionLine
	// Mine is the viewer's faction, nil for none.
	Mine *FactionRef
}

// FactionList renders the factions of a city.
func FactionList(c Context, v FactionListView) *presenter.Response {
	return c.withView(renderFactionList(c, v), ScreenFactionList, v)
}

func renderFactionList(c Context, v FactionListView) *presenter.Response {
	title := c.T("faction.list_title", nil)
	if v.City != "" {
		title = c.T("faction.list_title_city", map[string]any{"city": c.CityName(v.CityCode, v.City)})
	}
	kb := keyboards.New()
	var lines []string
	for _, f := range v.Factions {
		lines = append(lines, c.T("faction.list_line", map[string]any{"faction": c.factionName(f.Ref),
			"members": FormatNumber(c, int64(f.Members))}))
		kb.Add(c.T("faction.button.view", map[string]any{"name": f.Ref.Name}), AddrFaction, f.Ref.Code)
	}
	list := c.T("faction.list_none", nil)
	if len(lines) > 0 {
		list = body(lines...)
	}
	var mine string
	if v.Mine != nil {
		mine = c.T("faction.list_mine", map[string]any{"faction": c.factionName(*v.Mine)})
		kb.Add(c.T("faction.button.mine", nil), AddrFactionMine)
	} else {
		mine = c.T("faction.list_found", map[string]any{"fee": FormatMoney(c, v.Fee)})
		kb.Add(c.T("faction.button.found", nil), AddrFactionFound)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrFactions}))
	return c.respond(paragraphs(title, list, mine), kb.Build())
}

// FactionMemberLine is one member, or one crew member.
type FactionMemberLine struct {
	Player GovPlayer
	Rank   string
	Self   bool
	// What the viewer may do to them.
	CanKick, CanPromote, CanDemote, CanLead bool
}

// FactionPageView is a faction's public page.
type FactionPageView struct {
	Ref            FactionRef
	CityCode, City string
	Linked         bool
	Members        []FactionMemberLine
	// Mine is the viewer's own faction; CanApply that they may ask to join;
	// CanLink that they may tie it to the group the page is read in.
	Mine     bool
	CanApply bool
	CanLink  bool
}

// FactionPage renders a faction's public page.
func FactionPage(c Context, v FactionPageView) *presenter.Response {
	return c.withView(renderFactionPage(c, v), ScreenFactionPage, v)
}

func renderFactionPage(c Context, v FactionPageView) *presenter.Response {
	lines := []string{c.T("faction.page_city", map[string]any{"city": c.CityName(v.CityCode, v.City)}),
		c.T("faction.page_members", map[string]any{"count": FormatNumber(c, int64(len(v.Members)))})}
	for _, m := range v.Members {
		lines = append(lines, c.T("faction.member_line", map[string]any{"player": c.govPlayer(&m.Player),
			"rank": c.rankName(m.Rank)}))
	}
	if v.Linked {
		lines = append(lines, c.T("faction.page_linked", nil))
	}
	kb := keyboards.New()
	if v.CanLink {
		kb.Add(c.T("faction.button.link", nil), AddrFactionLink)
	}
	switch {
	case v.Mine:
		kb.Add(c.T("faction.button.mine", nil), AddrFactionMine)
	case v.CanApply:
		kb.Add(c.T("faction.button.apply", nil), AddrFactionApply, v.Ref.Code, FactionYes)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrFactions, RefreshData: keyboards.Data(AddrFaction, v.Ref.Code)}))
	return c.respond(paragraphs(c.T("faction.page_title", map[string]any{"faction": c.factionName(v.Ref)}), body(lines...)),
		kb.Build())
}

// FactionFoundView is founding a faction: the fee, and a way to pay each
// asking for the name.
type FactionFoundView struct {
	CityCode, City   string
	Fee              int64
	Payment          PaymentChoice
	NameMin, NameMax int
}

// FactionFound renders founding a faction.
func FactionFound(c Context, v FactionFoundView) *presenter.Response {
	return c.withView(renderFactionFound(c, v), ScreenFactionFound, v)
}

func renderFactionFound(c Context, v FactionFoundView) *presenter.Response {
	text := body(c.T("faction.found_title", nil),
		c.T("faction.found_fee", map[string]any{"fee": FormatMoney(c, v.Fee), "city": c.CityName(v.CityCode, v.City)}),
		c.T("faction.found_rules", map[string]any{"min": FormatNumber(c, int64(v.NameMin)), "max": FormatNumber(c, int64(v.NameMax))}))
	kb := keyboards.New()
	var pay string
	if len(v.Payment.Usable) > 0 {
		pay = body(c.T("faction.found_how", nil), c.paymentNote(v.Payment))
		for _, m := range v.Payment.Usable {
			if btn, ok := askButton(c.T("payment.button."+m, map[string]any{"amount": FormatMoney(c, v.Payment.Amount)}),
				commandFactionFound, m); ok {
				kb.Row(btn)
			}
		}
	} else {
		pay = body(c.T("payment.cannot_afford", nil), c.paymentNote(v.Payment))
		kb.Add(c.T("button.bank", nil), AddrBank)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrFactions}))
	return c.respond(paragraphs(text, pay), kb.Build()).MarkPrivate()
}

// FactionFoundedView is a faction founded.
type FactionFoundedView struct {
	Ref            FactionRef
	CityCode, City string
	Fee            int64
	Method         string
}

// FactionFounded renders a faction founded.
func FactionFounded(c Context, v FactionFoundedView) *presenter.Response {
	return c.withView(renderFactionFounded(c, v), ScreenFactionFounded, v)
}

func renderFactionFounded(c Context, v FactionFoundedView) *presenter.Response {
	lines := []string{c.T("faction.founded", map[string]any{"faction": c.factionName(v.Ref),
		"city": c.CityName(v.CityCode, v.City)})}
	if v.Fee > 0 {
		lines = append(lines, c.T("faction.founded_fee", map[string]any{"fee": FormatMoney(c, v.Fee)}), c.paidLine(v.Method))
	}
	lines = append(lines, c.T("faction.founded_next", map[string]any{"code": v.Ref.Code}))
	kb := keyboards.New()
	kb.Add(c.T("faction.button.mine", nil), AddrFactionMine)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// FactionHomeView is a member's faction screen.
type FactionHomeView struct {
	Ref            FactionRef
	Rank           string
	Linked         bool
	Rights         []string
	CityCode, City string
	Members        int
	MaxMembers     int
	Bank           int64
	Applications   int
	Operation      *FactionOperationLine
}

func hasRight(rights []string, r string) bool {
	for _, x := range rights {
		if x == r {
			return true
		}
	}
	return false
}

// FactionHome renders a member's faction screen.
func FactionHome(c Context, v FactionHomeView) *presenter.Response {
	return c.withView(renderFactionHome(c, v), ScreenFactionHome, v)
}

func renderFactionHome(c Context, v FactionHomeView) *presenter.Response {
	lines := []string{
		c.T("faction.home_rank", map[string]any{"rank": c.rankName(v.Rank)}),
		c.T("faction.page_city", map[string]any{"city": c.CityName(v.CityCode, v.City)}),
		c.T("faction.home_members", map[string]any{"count": FormatNumber(c, int64(v.Members)),
			"max": FormatNumber(c, int64(v.MaxMembers))}),
		c.T("faction.home_bank", map[string]any{"amount": FormatMoney(c, v.Bank)}),
	}
	if v.Applications > 0 {
		lines = append(lines, c.T("faction.home_applications", map[string]any{"count": FormatNumber(c, int64(v.Applications))}))
	}
	link := c.T("faction.home_unlinked", map[string]any{"code": v.Ref.Code})
	if v.Linked {
		link = c.T("faction.page_linked", nil)
	}
	var op string
	if v.Operation != nil {
		op = c.operationLines(*v.Operation)
	}
	kb := keyboards.New()
	members, _ := keyboards.Button(c.T("faction.button.members", nil), AddrFactionMembers)
	bank, _ := keyboards.Button(c.T("faction.button.bank", nil), AddrFactionBank)
	kb.Row(members, bank)
	kb.Add(c.T("faction.button.crime", nil), AddrFactionCrime)
	if hasRight(v.Rights, "invite") {
		if btn, ok := askButton(c.T("faction.button.invite", nil), commandFactionInv); ok {
			kb.Row(btn)
		}
	}
	kb.Add(c.T("faction.button.leave", nil), AddrFactionLeave)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrFactionMine}))
	return c.respond(paragraphs(c.T("faction.home_title", map[string]any{"faction": c.factionName(v.Ref)}),
		body(lines...), link, op), kb.Build()).MarkPrivate()
}

// FactionRequestLine is an invitation or an application waiting.
type FactionRequestLine struct {
	No        int64
	Kind      string
	Player    GovPlayer
	CanDecide bool
}

// FactionMembersView is a faction's members, and what is waiting.
type FactionMembersView struct {
	Ref       FactionRef
	Max       int
	CanInvite bool
	Members   []FactionMemberLine
	Requests  []FactionRequestLine
}

// FactionMembers renders a faction's members, with the buttons the viewer's
// rank allows on each.
func FactionMembers(c Context, v FactionMembersView) *presenter.Response {
	return c.withView(renderFactionMembers(c, v), ScreenFactionMembers, v)
}

func renderFactionMembers(c Context, v FactionMembersView) *presenter.Response {
	kb := keyboards.New()
	lines := []string{c.T("faction.members_count", map[string]any{"count": FormatNumber(c, int64(len(v.Members))),
		"max": FormatNumber(c, int64(v.Max))})}
	for _, m := range v.Members {
		key := "faction.member_line"
		if m.Self {
			key = "faction.member_line_self"
		}
		lines = append(lines, c.T(key, map[string]any{"player": c.govPlayer(&m.Player), "rank": c.rankName(m.Rank)}))
		var row []presenter.Button
		name := map[string]any{"name": m.Player.Name}
		if m.CanPromote {
			if b, ok := keyboards.Button(c.T("faction.button.promote", name), AddrFactionRank, m.Player.Code, "officer"); ok {
				row = append(row, b)
			}
		}
		if m.CanDemote {
			if b, ok := keyboards.Button(c.T("faction.button.demote", name), AddrFactionRank, m.Player.Code, "member"); ok {
				row = append(row, b)
			}
		}
		if m.CanKick {
			if b, ok := keyboards.Button(c.T("faction.button.kick", name), AddrFactionKick, m.Player.Code); ok {
				row = append(row, b)
			}
		}
		if m.CanLead {
			if b, ok := keyboards.Button(c.T("faction.button.lead", name), AddrFactionRank, m.Player.Code, "leader"); ok {
				row = append(row, b)
			}
		}
		if len(row) > 0 {
			kb.Row(row...)
		}
	}
	var waiting []string
	for _, q := range v.Requests {
		waiting = append(waiting, c.T("faction.request_line."+q.Kind, map[string]any{"player": c.govPlayer(&q.Player)}))
		if q.CanDecide {
			no := strconv.FormatInt(q.No, 10)
			yes, _ := keyboards.Button(c.T("faction.button.accept_name", map[string]any{"name": q.Player.Name}), AddrFactionAnswer, no, FactionAccept)
			no2, _ := keyboards.Button(c.T("faction.button.decline", nil), AddrFactionAnswer, no, FactionDecline)
			kb.Row(yes, no2)
		}
	}
	var pending string
	if len(waiting) > 0 {
		pending = body(append([]string{c.T("faction.requests_title", nil)}, waiting...)...)
	}
	if v.CanInvite {
		if btn, ok := askButton(c.T("faction.button.invite", nil), commandFactionInv); ok {
			kb.Row(btn)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrFactionMine, RefreshData: AddrFactionMembers}))
	return c.respond(paragraphs(c.T("faction.members_title", map[string]any{"faction": c.factionName(v.Ref)}),
		body(lines...), pending), kb.Build()).MarkPrivate()
}

// FactionInvited renders an invitation sent.
func FactionInvited(c Context, who GovPlayer) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("faction.button.members", nil), AddrFactionMembers)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrFactionMine}))
	return c.respond(c.T("faction.invited", map[string]any{"player": c.govPlayer(&who)}), kb.Build()).MarkPrivate()
}

// FactionApplied renders an application sent.
func FactionApplied(c Context, f FactionRef) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("faction.button.list", nil), AddrFactions)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(c.T("faction.applied", map[string]any{"faction": c.factionName(f)}), kb.Build()).MarkPrivate()
}

// FactionAnsweredView is an invitation or application answered.
type FactionAnsweredView struct {
	Ref      FactionRef
	Kind     string
	Accepted bool
	Player   GovPlayer
}

// FactionAnswered renders an answer given.
func FactionAnswered(c Context, v FactionAnsweredView) *presenter.Response {
	return c.withView(renderFactionAnswered(c, v), ScreenFactionAnswered, v)
}

func renderFactionAnswered(c Context, v FactionAnsweredView) *presenter.Response {
	key := "faction.answered." + v.Kind + "_"
	if v.Accepted {
		key += "accepted"
	} else {
		key += "declined"
	}
	kb := keyboards.New()
	if v.Accepted && v.Kind == application.RequestInvite {
		kb.Add(c.T("faction.button.mine", nil), AddrFactionMine)
	} else {
		kb.Add(c.T("faction.button.members", nil), AddrFactionMembers)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(c.T(key, map[string]any{"faction": c.factionName(v.Ref), "player": c.govPlayer(&v.Player)}),
		kb.Build()).MarkPrivate()
}

// Confirmations of a faction.
const (
	FactionConfirmKick    = "kick"
	FactionConfirmLead    = "lead"
	FactionConfirmLeave   = "leave"
	FactionConfirmDisband = "disband"
)

// FactionConfirmView asks to confirm an act that cannot be taken back.
type FactionConfirmView struct {
	Kind   string
	Ref    FactionRef
	Player GovPlayer
}

// FactionConfirm renders a confirmation.
func FactionConfirm(c Context, v FactionConfirmView) *presenter.Response {
	return c.withView(renderFactionConfirm(c, v), ScreenFactionConfirm, v)
}

func renderFactionConfirm(c Context, v FactionConfirmView) *presenter.Response {
	args := map[string]any{"faction": c.factionName(v.Ref), "player": c.govPlayer(&v.Player)}
	kb := keyboards.New()
	var addr []string
	switch v.Kind {
	case FactionConfirmKick:
		addr = []string{AddrFactionKick, v.Player.Code, FactionYes}
	case FactionConfirmLead:
		addr = []string{AddrFactionRank, v.Player.Code, "leader", FactionYes}
	default:
		addr = []string{AddrFactionLeave, FactionYes}
	}
	if btn, ok := keyboards.Button(c.T("faction.button.confirm_"+v.Kind, nil), addr...); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrFactionMine}))
	return c.respond(c.T("faction.confirm."+v.Kind, args), kb.Build()).MarkPrivate()
}

// FactionLeftView is a member gone, or a faction disbanded.
type FactionLeftView struct {
	Ref       FactionRef
	Disbanded bool
	PaidOut   int64
}

// FactionLeft renders leaving a faction.
func FactionLeft(c Context, v FactionLeftView) *presenter.Response {
	return c.withView(renderFactionLeft(c, v), ScreenFactionLeft, v)
}

func renderFactionLeft(c Context, v FactionLeftView) *presenter.Response {
	args := map[string]any{"faction": c.factionName(v.Ref), "amount": FormatMoney(c, v.PaidOut)}
	lines := []string{c.T("faction.left", args)}
	if v.Disbanded {
		lines = []string{c.T("faction.disbanded", args)}
		if v.PaidOut > 0 {
			lines = append(lines, c.T("faction.disbanded_paid", args))
		}
	}
	kb := keyboards.New()
	kb.Add(c.T("faction.button.list", nil), AddrFactions)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// FactionLinkedView is a faction tied to a group.
type FactionLinkedView struct{ Ref FactionRef }

// FactionLinked renders a faction tied to the group it was sent in.
func FactionLinked(c Context, v FactionLinkedView) *presenter.Response {
	return c.withView(renderFactionLinked(c, v), ScreenFactionLinked, v)
}

func renderFactionLinked(c Context, v FactionLinkedView) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("faction.button.crime", nil), AddrFactionCrime)
	return c.respond(c.T("faction.linked", map[string]any{"faction": c.factionName(v.Ref)}), kb.Build())
}

// FactionMoneyDone is money just moved in or out of a faction's bank.
type FactionMoneyDone struct {
	Deposit bool
	Amount  int64
	Method  string
}

// FactionBankView is a faction's bank.
type FactionBankView struct {
	Ref                     FactionRef
	Balance                 int64
	CanDeposit, CanWithdraw bool
	Cash, BankBalance       int64
	Min, Max                int64
	Methods                 []string
	Done                    *FactionMoneyDone
}

// FactionBank renders a faction's bank.
func FactionBank(c Context, v FactionBankView) *presenter.Response {
	return c.withView(renderFactionBank(c, v), ScreenFactionBank, v)
}

func renderFactionBank(c Context, v FactionBankView) *presenter.Response {
	var done string
	if v.Done != nil {
		if v.Done.Deposit {
			done = body(c.T("faction.bank_deposited", map[string]any{"amount": FormatMoney(c, v.Done.Amount)}), c.paidLine(v.Done.Method))
		} else {
			done = c.T("faction.bank_withdrawn", map[string]any{"amount": FormatMoney(c, v.Done.Amount)})
		}
	}
	lines := []string{c.T("faction.bank_balance", map[string]any{"amount": FormatMoney(c, v.Balance)})}
	if !c.Shared {
		lines = append(lines, c.T("payment.balances", map[string]any{"cash": FormatMoney(c, v.Cash), "bank": FormatMoney(c, v.BankBalance)}))
	}
	lines = append(lines, c.T("faction.bank_limits", map[string]any{"min": FormatMoney(c, v.Min), "max": FormatMoney(c, v.Max)}))
	kb := keyboards.New()
	if v.CanDeposit {
		var row []presenter.Button
		for _, m := range v.Methods {
			if btn, ok := askButton(c.T("faction.button.deposit_"+m, nil), commandFactionDep, m); ok {
				row = append(row, btn)
			}
		}
		if len(row) > 0 {
			kb.Row(row...)
		}
	}
	if v.CanWithdraw {
		if btn, ok := askButton(c.T("faction.button.withdraw", nil), commandFactionWd); ok {
			kb.Row(btn)
		}
	}
	rules := c.T("faction.bank_rules", nil)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrFactionMine, RefreshData: AddrFactionBank}))
	return c.respond(paragraphs(done, c.T("faction.bank_title", map[string]any{"faction": c.factionName(v.Ref)}),
		body(lines...), rules), kb.Build()).MarkPrivate()
}

// FactionOperationLine is an organised crime, gathering or under way.
type FactionOperationLine struct {
	No             int64
	Status         string
	Crime          Named
	Place          Named
	CityCode, City string
	ChanceBPS      int
	Min, Max       int
	Nerve          int
	Crew           []FactionMemberLine
	// Left and At are the gathering's end or the job's.
	Left    time.Duration
	At      time.Time
	Expired bool
}

// operationLines are an organised crime's lines.
func (c Context) operationLines(o FactionOperationLine) string {
	args := map[string]any{"crime": c.CrimeName(o.Crime), "place": c.SpotName(o.Place), "city": c.CityName(o.CityCode, o.City),
		"left": FormatDuration(c, o.Left), "crew": FormatNumber(c, int64(len(o.Crew))), "min": FormatNumber(c, int64(o.Min)),
		"max": FormatNumber(c, int64(o.Max)), "nerve": FormatNumber(c, int64(o.Nerve)), "chance": PercentFromBPS(c, o.ChanceBPS)}
	lines := []string{c.T("faction.op."+o.Status, args)}
	switch o.Status {
	case application.HeistGathering:
		lines = append(lines, clockLine(c, "faction.op.gather_until", o.At), c.T("faction.op.crew_count", args))
	case application.HeistRunning:
		lines = append(lines, clockLine(c, "faction.op.ends_at", o.At))
	}
	var crew []GovPlayer
	for _, m := range o.Crew {
		crew = append(crew, m.Player)
	}
	if len(crew) > 0 {
		lines = append(lines, c.T("faction.op.crew", map[string]any{"crew": c.govPlayers(crew)}))
	}
	return body(lines...)
}

// FactionPlanLine is an organised crime that may be planned.
type FactionPlanLine struct {
	Crime    Named
	Min, Max int
	Nerve    int
	MinLevel int
	Duration time.Duration
	Places   []Named
}

// Notices on the organised crime board.
const (
	FactionNoticePlanned   = "planned"
	FactionNoticeJoined    = "joined"
	FactionNoticeLaunched  = "launched"
	FactionNoticeCalledOff = "called_off"
)

// FactionCrimeView is a faction's organised crime board.
type FactionCrimeView struct {
	Ref                         FactionRef
	Notice                      string
	CanPlan, CanLaunch, CanJoin bool
	CutBPS                      int
	Operation                   *FactionOperationLine
	InCrew                      bool
	Crimes                      []FactionPlanLine
}

// FactionCrime renders the organised crime board.
func FactionCrime(c Context, v FactionCrimeView) *presenter.Response {
	return c.withView(renderFactionCrime(c, v), ScreenFactionCrime, v)
}

func renderFactionCrime(c Context, v FactionCrimeView) *presenter.Response {
	var notice string
	if v.Notice != "" {
		notice = c.T("faction.crime_notice."+v.Notice, nil)
	}
	kb := keyboards.New()
	var current, menu string
	if o := v.Operation; o != nil {
		current = c.operationLines(*o)
		if o.Status == application.HeistGathering {
			if v.CanJoin && !v.InCrew {
				kb.Add(c.T("faction.button.join", nil), AddrFactionJoin)
			}
			if v.CanLaunch && len(o.Crew) >= o.Min {
				kb.Add(c.T("faction.button.launch", nil), AddrFactionLaunch)
			}
			if v.CanPlan {
				kb.Add(c.T("faction.button.calloff", nil), AddrFactionCallOff)
			}
		}
	} else {
		lines := []string{c.T("faction.crime_none", nil)}
		for _, l := range v.Crimes {
			var places []string
			for _, p := range l.Places {
				places = append(places, c.SpotName(p))
			}
			lines = append(lines, c.T("faction.crime_line", map[string]any{"crime": c.CrimeName(l.Crime),
				"min": FormatNumber(c, int64(l.Min)), "max": FormatNumber(c, int64(l.Max)),
				"nerve": FormatNumber(c, int64(l.Nerve)), "level": FormatNumber(c, int64(l.MinLevel)),
				"duration": FormatDuration(c, l.Duration), "places": joinWith(c, places)}))
			if v.CanPlan {
				kb.Add(c.T("faction.button.plan", map[string]any{"crime": c.CrimeName(l.Crime)}), AddrFactionPlan, l.Crime.Code)
			}
		}
		menu = body(lines...)
	}
	rules := c.T("faction.crime_rules", map[string]any{"cut": PercentFromBPS(c, v.CutBPS)})
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrFactionMine, RefreshData: AddrFactionCrime}))
	return c.respond(paragraphs(notice, c.T("faction.crime_title", map[string]any{"faction": c.factionName(v.Ref)}),
		current, menu, rules), kb.Build())
}

// Faction refusal kinds.
const (
	FactionRefusedNone          = "none"
	FactionRefusedNotMember     = "not_member"
	FactionRefusedRank          = "rank"
	FactionRefusedNotFound      = "not_found"
	FactionRefusedAlreadyMember = "already_member"
	FactionRefusedName          = "name"
	FactionRefusedNameTaken     = "name_taken"
	FactionRefusedNotGroup      = "not_group"
	FactionRefusedGroupTaken    = "group_taken"
	FactionRefusedBankShort     = "bank_short"
	FactionRefusedNoPlayer      = "no_player"
	FactionRefusedTheirs        = "theirs"
	FactionRefusedFull          = "full"
	FactionRefusedPendingFull   = "pending_full"
	FactionRefusedPending       = "pending"
	FactionRefusedRequestGone   = "request_gone"
	FactionRefusedNotYours      = "not_yours"
	FactionRefusedNotInIt       = "not_in_it"
	FactionRefusedOnAJob        = "on_a_job"
	FactionRefusedLeaderLeaving = "leader_leaving"
	FactionRefusedNoSuchCrime   = "no_such_crime"
	FactionRefusedOperationOpen = "operation_open"
	FactionRefusedLevel         = "level"
	FactionRefusedNoPlaceHere   = "no_place_here"
	FactionRefusedNoOperation   = "no_operation"
	FactionRefusedCrewFull      = "crew_full"
	FactionRefusedElsewhere     = "elsewhere"
	FactionRefusedCrewShort     = "crew_short"
)

// FactionRefusalView is a refused faction request.
type FactionRefusalView struct {
	Kind            string
	Min, Max        int
	Amount, Balance int64
	Need, Have      int
	Level           int
}

// FactionRefusal renders a refused faction request.
func FactionRefusal(c Context, v FactionRefusalView) *presenter.Response {
	return c.withView(renderFactionRefusal(c, v), ScreenFactionRefusal, v)
}

func renderFactionRefusal(c Context, v FactionRefusalView) *presenter.Response {
	text := c.T("faction.refused."+v.Kind, map[string]any{"min": FormatNumber(c, int64(v.Min)),
		"max": FormatNumber(c, int64(v.Max)), "amount": FormatMoney(c, v.Amount), "balance": FormatMoney(c, v.Balance),
		"need": FormatNumber(c, int64(v.Need)), "have": FormatNumber(c, int64(v.Have)), "level": FormatNumber(c, int64(v.Level))})
	kb := keyboards.New()
	switch v.Kind {
	case FactionRefusedNotMember, FactionRefusedNotFound, FactionRefusedNone:
		kb.Add(c.T("faction.button.list", nil), AddrFactions)
	case FactionRefusedNoSuchCrime, FactionRefusedOperationOpen, FactionRefusedLevel, FactionRefusedNoPlaceHere,
		FactionRefusedNoOperation, FactionRefusedCrewFull, FactionRefusedElsewhere, FactionRefusedCrewShort, FactionRefusedOnAJob:
		kb.Add(c.T("faction.button.crime", nil), AddrFactionCrime)
	default:
		kb.Add(c.T("faction.button.mine", nil), AddrFactionMine)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(text, kb.Build())
}

// FactionRequestNoticeView is an invitation, or an application, as its
// recipient learns of it.
type FactionRequestNoticeView struct {
	No     int64
	Kind   string
	Ref    FactionRef
	Player GovPlayer
}

// FactionRequestNotice tells a player they were invited, or an officer
// that someone applied, with the buttons to answer.
func FactionRequestNotice(c Context, v FactionRequestNoticeView) *presenter.Response {
	return c.withView(renderFactionRequestNotice(c, v), ScreenFactionRequestNotice, v)
}

func renderFactionRequestNotice(c Context, v FactionRequestNoticeView) *presenter.Response {
	no := strconv.FormatInt(v.No, 10)
	kb := keyboards.New()
	yes, _ := keyboards.Button(c.T("faction.button.accept", nil), AddrFactionAnswer, no, FactionAccept)
	nope, _ := keyboards.Button(c.T("faction.button.decline", nil), AddrFactionAnswer, no, FactionDecline)
	kb.Row(yes, nope)
	return c.respond(c.T("faction.notice."+v.Kind, map[string]any{"faction": c.factionName(v.Ref),
		"player": c.govPlayer(&v.Player)}), kb.Build()).MarkPrivate()
}

// FactionAnswerNotice tells the other side how a request was answered.
func FactionAnswerNotice(c Context, v FactionAnsweredView) *presenter.Response {
	return c.withView(renderFactionAnswerNotice(c, v), ScreenFactionAnswerNotice, v)
}

func renderFactionAnswerNotice(c Context, v FactionAnsweredView) *presenter.Response {
	key := "faction.notice.answer_" + v.Kind + "_declined"
	if v.Accepted {
		key = "faction.notice.answer_" + v.Kind + "_accepted"
	}
	kb := keyboards.New()
	if v.Accepted && v.Kind == application.RequestApply {
		kb.Add(c.T("faction.button.mine", nil), AddrFactionMine)
	} else {
		kb.Add(c.T("faction.button.list", nil), AddrFactions)
	}
	return c.respond(c.T(key, map[string]any{"faction": c.factionName(v.Ref), "player": c.govPlayer(&v.Player)}),
		kb.Build()).MarkPrivate()
}

// FactionKickedNotice tells a player they were removed from a faction.
func FactionKickedNotice(c Context, f FactionRef, by string) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("faction.button.list", nil), AddrFactions)
	return c.respond(c.T("faction.notice.kicked", map[string]any{"faction": c.factionName(f), "player": by}),
		kb.Build()).MarkPrivate()
}

// FactionCrimeNoticeView is an organised crime's end, as one crew member
// learns of it.
type FactionCrimeNoticeView struct {
	Ref    FactionRef
	Crime  Named
	Result string
	// Share is the member's share of Take; Cut the faction bank's.
	Share, Take, Cut int64
	XP               int64
	Jail             *CrimeProgress
	Fine, FinePaid   int64
	Injury           *InjuryView
}

// FactionCrimeNotice tells a crew member how an organised crime ended.
func FactionCrimeNotice(c Context, v FactionCrimeNoticeView) *presenter.Response {
	return c.withView(renderFactionCrimeNotice(c, v), ScreenFactionCrimeNotice, v)
}

func renderFactionCrimeNotice(c Context, v FactionCrimeNoticeView) *presenter.Response {
	args := map[string]any{"crime": c.CrimeName(v.Crime), "faction": c.factionName(v.Ref),
		"share": FormatMoney(c, v.Share), "take": FormatMoney(c, v.Take), "cut": FormatMoney(c, v.Cut),
		"xp": FormatNumber(c, v.XP)}
	lines := []string{c.T("faction.crime_result."+v.Result, args)}
	if v.Result == application.HeistSucceeded {
		lines = append(lines, c.T("faction.crime_result.split", args))
	}
	if v.XP > 0 {
		lines = append(lines, c.T("crime.result.xp", map[string]any{"xp": FormatNumber(c, v.XP)}))
	}
	if v.Jail != nil {
		lines = append(lines, c.T("faction.crime_result.jail", map[string]any{"term": FormatDuration(c, v.Jail.Remaining)}),
			clockLine(c, "crime.free_at", v.Jail.EndsAt))
	}
	if v.Fine > 0 {
		lines = append(lines, c.T("crime.result.fine_short", map[string]any{"fine": FormatMoney(c, v.Fine),
			"paid": FormatMoney(c, v.FinePaid), "unpaid": FormatMoney(c, v.Fine-v.FinePaid)}))
	}
	if v.Injury != nil {
		lines = append(lines, c.injuryLines(v.Injury))
	}
	kb := keyboards.New()
	switch {
	case v.Injury != nil && v.Injury.Hospital:
		kb.Add(c.T("health.button.hospital", nil), AddrHospital)
	case v.Jail != nil:
		kb.Add(c.T("crime.button.jail", nil), AddrCrimeJail)
	default:
		kb.Add(c.T("faction.button.crime", nil), AddrFactionCrime)
	}
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// FactionGroupLine is one line a faction's group — or a city's, for a
// faction founded or disbanded there — reads. Never an amount.
func FactionGroupLine(c Context, kind string, f FactionRef, player string, crime Named, place Named, result, rank string) string {
	args := map[string]any{"faction": c.factionName(f), "player": player}
	if rank != "" {
		args["rank"] = c.rankName(rank)
	}
	if crime.Code != "" {
		args["crime"] = c.CrimeName(crime)
	}
	if place.Code != "" {
		args["place"] = c.SpotName(place)
	}
	if result != "" {
		args["result"] = c.T("faction.op_result."+result, nil)
	}
	return c.T("faction.announce."+kind, args)
}
