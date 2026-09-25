package screens

import (
	"sort"
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Legislatures (docs/adr/0024-property-and-politics.md): the proposals put to
// a body's vote in the player's city and country, one proposal with its
// votes, a member's vote, and the public lines of a proposal opened and
// decided. A legislator's vote is public record, unlike a ballot.

// Addresses of the legislature screens.
const (
	AddrBills   = "law:list"
	AddrBill    = "law:view"
	AddrBillVot = "law:vote"
)

// BillSubject is what a proposal would do: change a lever to a value or an
// allocation, or take an action.
type BillSubject struct {
	Kind string
	// Code is the lever's or the action's code.
	Code string
	// LeverType formats a lever's values; Value is a scalar's, Allocation an
	// allocation's shares; Categories their order.
	LeverType  string
	Value      int64
	Allocation map[string]int64
	Categories []string
	// Target is the country an action concerns (a declaration of war).
	Target *GovPlace
}

// BillVoteLine is one member's vote.
type BillVoteLine struct {
	Player GovPlayer
	Yes    bool
}

// BillView is one proposal.
type BillView struct {
	No      int64
	Place   GovPlace
	Subject BillSubject
	// Office and By are the seat and the member who proposed it.
	Office string
	By     GovPlayer
	// Body votes; Rule, Threshold and Quorum are how; Seats and Held its
	// seats and those held now; Needs the yes votes that carry it if every
	// member votes.
	Body       string
	Rule       string
	Threshold  string
	Quorum     string
	Seats      int
	Held       int
	Needs      int
	Status     string
	LapsedWhy  string
	Yes, Nay   int
	Votes      []BillVoteLine
	ClosesAt   time.Time
	Remaining  time.Duration
	DecidedAgo time.Duration
	// CanVote offers the viewer the vote: a member who has not voted.
	CanVote bool
	// Notice is what just happened: submitted, voted, already_voted.
	Notice string
}

// Notices above a proposal.
const (
	BillNoticeSubmitted    = "submitted"
	BillNoticeVoted        = "voted"
	BillNoticeAlreadyVoted = "already_voted"
	BillNoticeClosed       = "closed"
)

// BillsView is the proposals of the player's places.
type BillsView struct {
	Bills []BillView
}

// Refusals of the legislature screens.
const (
	BillRefusedNotFound  = "not_found"
	BillRefusedNotMember = "not_member"
	BillRefusedUnderWay  = "under_way"
)

// BillRefusalView is a refused request.
type BillRefusalView struct {
	Kind string
	No   int64
	Body string
}

// BillSubjectText describes what a proposal would do, in one line.
func (c Context) BillSubjectText(s BillSubject) string {
	if s.Kind == application.ProposalAction {
		target := ""
		if s.Target != nil {
			target = c.PlaceName(*s.Target)
		}
		return c.coded("legislature.action.", s.Code, "legislature.action_unnamed") + c.targetSuffix(target)
	}
	if s.LeverType == application.LeverAllocation {
		return c.T("legislature.subject_allocation", map[string]any{"lever": c.LeverName(s.Code),
			"shares": c.AllocationText(s.Allocation, s.Categories)})
	}
	return c.T("legislature.subject_lever", map[string]any{"lever": c.LeverName(s.Code),
		"value": FormatPolicyValue(c, s.Code, s.LeverType, s.Value)})
}

func (c Context) targetSuffix(target string) string {
	if target == "" {
		return ""
	}
	return c.T("legislature.action_target", map[string]any{"target": target})
}

// AllocationText lists an allocation's shares in its categories' order,
// the empty ones left out; «nothing allocated» when every share is empty.
func (c Context) AllocationText(shares map[string]int64, order []string) string {
	codes := append([]string(nil), order...)
	if len(codes) == 0 {
		for k := range shares {
			codes = append(codes, k)
		}
		sort.Strings(codes)
	}
	var parts []string
	for _, code := range codes {
		if v := shares[code]; v > 0 {
			parts = append(parts, c.T("budget.share", map[string]any{"line": c.BudgetLineName(code),
				"share": c.T("gov.percent", map[string]any{"value": PercentFromBPS(c, int(v))})}))
		}
	}
	if len(parts) == 0 {
		return c.T("budget.nothing_allocated", nil)
	}
	return joinWith(c, parts)
}

// BudgetLineName names a budget line.
func (c Context) BudgetLineName(code string) string {
	return c.coded("budget_line.", code, "budget.unnamed_line")
}

// ruleText is how a body decides, in words.
func (c Context) ruleText(v BillView) string {
	switch v.Rule {
	case "supermajority":
		return c.T("legislature.rule.supermajority", map[string]any{"threshold": c.fraction(v.Threshold)})
	case "unanimous":
		return c.T("legislature.rule.unanimous", nil)
	}
	return c.T("legislature.rule.majority", nil)
}

// fraction renders "2/3" in this language's digits.
func (c Context) fraction(raw string) string {
	for i := 0; i < len(raw); i++ {
		if raw[i] == '/' {
			num, _ := strconv.Atoi(raw[:i])
			den, _ := strconv.Atoi(raw[i+1:])
			return c.T("legislature.fraction", map[string]any{"num": FormatNumber(c, int64(num)),
				"den": FormatNumber(c, int64(den))})
		}
	}
	return raw
}

// billStatus is where a proposal stands, in words.
func (c Context) billStatus(v BillView) string {
	switch v.Status {
	case application.ProposalOpen:
		return c.T("legislature.status.open", map[string]any{"remaining": FormatSpan(c, v.Remaining),
			"yes": FormatNumber(c, int64(v.Yes)), "no": FormatNumber(c, int64(v.Nay))})
	case application.ProposalPassed:
		return c.T("legislature.status.passed", map[string]any{"yes": FormatNumber(c, int64(v.Yes)),
			"no": FormatNumber(c, int64(v.Nay))})
	case application.ProposalLapsed:
		return c.T("legislature.status.lapsed", map[string]any{"why": c.T("legislature.lapse."+v.LapsedWhy, nil)})
	}
	return c.T("legislature.status.failed", map[string]any{"yes": FormatNumber(c, int64(v.Yes)),
		"no": FormatNumber(c, int64(v.Nay))})
}

// Bills renders the proposals of the player's places.
func Bills(c Context, v BillsView) *presenter.Response {
	kb := keyboards.New()
	lines := []string{c.T("legislature.list_title", nil)}
	if len(v.Bills) == 0 {
		lines = append(lines, c.T("legislature.list_empty", nil))
	}
	var list []string
	for _, b := range v.Bills {
		no := strconv.FormatInt(b.No, 10)
		list = append(list, body(
			c.T("legislature.line", map[string]any{"no": FormatNumber(c, b.No), "subject": c.BillSubjectText(b.Subject),
				"place": c.PlaceName(b.Place)}),
			"  "+c.billStatus(b)))
		kb.Add(c.T("legislature.button.view", map[string]any{"no": FormatNumber(c, b.No)}), AddrBill, no)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrGovCity, RefreshData: AddrBills}))
	return c.respond(paragraphs(body(lines...), paragraphs(list...)), kb.Build())
}

// Bill renders one proposal.
func Bill(c Context, v BillView) *presenter.Response {
	kb := keyboards.New()
	no := strconv.FormatInt(v.No, 10)
	var notice string
	if v.Notice != "" {
		notice = c.T("legislature.notice."+v.Notice, map[string]any{"body": c.OfficeName(v.Body),
			"place": c.PlaceName(v.Place)})
	}
	head := body(
		c.T("legislature.title", map[string]any{"no": FormatNumber(c, v.No), "place": c.PlaceName(v.Place)}),
		c.BillSubjectText(v.Subject),
		c.T("legislature.proposed_by", map[string]any{"office": c.OfficeName(v.Office), "player": c.govPlayer(&v.By)}),
	)
	quorum := ""
	if v.Quorum != "" {
		quorum = c.T("legislature.quorum", map[string]any{"quorum": c.fraction(v.Quorum)})
	}
	how := body(
		c.T("legislature.body", map[string]any{"body": c.OfficeName(v.Body), "held": FormatNumber(c, int64(v.Held)),
			"seats": FormatNumber(c, int64(v.Seats))}),
		c.ruleText(v),
		quorum,
	)
	status := c.billStatus(v)
	if v.Status == application.ProposalOpen {
		status = body(status,
			c.T("legislature.needs", map[string]any{"needs": FormatNumber(c, int64(v.Needs)),
				"held": FormatNumber(c, int64(v.Held))}),
			clockLine(c, "legislature.closes_at", v.ClosesAt))
	}
	var votes []string
	for _, vote := range v.Votes {
		key := "legislature.vote_no"
		if vote.Yes {
			key = "legislature.vote_yes"
		}
		p := vote.Player
		votes = append(votes, c.T(key, map[string]any{"player": c.govPlayer(&p)}))
	}
	if len(votes) > 0 {
		votes = append([]string{c.T("legislature.votes", nil)}, votes...)
	}
	if v.CanVote && v.Status == application.ProposalOpen {
		yes, _ := keyboards.Button(c.T("legislature.button.for", nil), AddrBillVot, no, application.VoteYes)
		nay, _ := keyboards.Button(c.T("legislature.button.against", nil), AddrBillVot, no, application.VoteNo)
		kb.Row(yes, nay)
	}
	kb.Add(c.T("legislature.button.list", nil), AddrBills)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrBills, RefreshData: keyboards.Data(AddrBill, no)}))
	return c.respond(paragraphs(notice, head, how, status, body(votes...)), kb.Build())
}

// BillRefusal renders a refused request.
func BillRefusal(c Context, v BillRefusalView) *presenter.Response {
	kb := keyboards.New()
	back := AddrBills
	if v.No > 0 {
		back = keyboards.Data(AddrBill, strconv.FormatInt(v.No, 10))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(c.T("legislature.refused."+v.Kind, map[string]any{"body": c.OfficeName(v.Body),
		"no": FormatNumber(c, v.No)}), kb.Build())
}

// BillOpenedAnnouncement is the public line of a proposal put to a vote.
func BillOpenedAnnouncement(c Context, v BillView, window time.Duration) string {
	return c.T("legislature.announce.opened", map[string]any{"no": FormatNumber(c, v.No),
		"place": c.PlaceName(v.Place), "subject": c.BillSubjectText(v.Subject), "office": c.OfficeName(v.Office),
		"player": c.govPlayer(&v.By), "body": c.OfficeName(v.Body), "window": FormatSpan(c, window)})
}

// BillDecidedAnnouncement is the public line of a proposal decided.
func BillDecidedAnnouncement(c Context, v BillView) string {
	return c.T("legislature.announce."+v.Status, map[string]any{"no": FormatNumber(c, v.No),
		"place": c.PlaceName(v.Place), "subject": c.BillSubjectText(v.Subject), "body": c.OfficeName(v.Body),
		"yes": FormatNumber(c, int64(v.Yes)), "no_votes": FormatNumber(c, int64(v.Nay)),
		"why": c.T("legislature.lapse."+lapseOr(v.LapsedWhy), nil)})
}

func lapseOr(why string) string {
	if why == "" {
		return "changed"
	}
	return why
}

// BillDecidedNotice tells the member who proposed it how it went.
func BillDecidedNotice(c Context, v BillView) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("legislature.button.view", map[string]any{"no": FormatNumber(c, v.No)}), AddrBill,
		strconv.FormatInt(v.No, 10))
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrBills}))
	return c.respond(BillDecidedAnnouncement(c, v), kb.Build())
}
