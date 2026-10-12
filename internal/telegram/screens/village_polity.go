package screens

import (
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Unions, countries, treaties of settlements, tenders and loans between polities (docs/adr/0076), drawn for Telegram. The long forms (a
// proposal's charter, an office's powers, the terms of a loan) are written in the Mini App; here the group sees the state and presses the
// short acts: ratify, refuse, vote, leave, accept, decline.

// isPolityRefusal reports a refusal kind of the union, treaty, tender and debt screens.
func isPolityRefusal(kind string) bool {
	for _, p := range []string{"union_", "treaty_", "tender_", "debt_"} {
		if strings.HasPrefix(kind, p) {
			return true
		}
	}
	return false
}

// Screens and addresses.
const (
	ScreenUnion  = village.ScreenUnion
	AddrUnion    = village.AddrUnion
	ScreenTreaty = village.ScreenTreaty
	AddrTreaty   = village.AddrTreaty
	ScreenTender = village.ScreenTender
	AddrTender   = village.AddrTender
	ScreenDebt   = village.ScreenDebt
	AddrDebt     = village.AddrDebt
)

func polityNotice(c Context, notice string, blocks []string) []string {
	if notice != "" {
		return append(blocks, c.T("polity.notice."+notice, nil))
	}
	return blocks
}

// Union renders the union screen.
func Union(c Context, v village.UnionView) *presenter.Response {
	return c.withView(renderUnion(c, v), ScreenUnion, v)
}

func renderUnion(c Context, v village.UnionView) *presenter.Response {
	kb := keyboards.New()
	blocks := polityNotice(c, v.Notice, []string{c.T("polity.union.title", map[string]any{"name": v.Village})})
	if !v.Open {
		blocks = append(blocks, c.T("polity.off", nil))
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrUnion}))
		return c.respond(paragraphs(blocks...), kb.Build())
	}
	var ls []string
	for _, u := range v.Unions {
		ls = append(ls, c.T("polity.union.line", map[string]any{"name": u.Name, "kind": c.T("polity.kind."+u.Kind, nil), "members": FormatNumber(c, int64(u.Members)),
			"purse": FormatMoney(c, u.Purse), "status": c.T("polity.member."+u.MyStatus, nil)}))
		addBtn(kb, c.T("polity.union.button.open", map[string]any{"name": u.Name}), AddrUnion, village.UnionActionOpen, u.ID)
	}
	if len(ls) == 0 {
		blocks = append(blocks, c.T("polity.union.none", nil))
	} else {
		blocks = append(blocks, c.T("polity.union.head", nil)+"\n"+body(ls...))
	}
	for _, p := range v.Proposals {
		var inv []string
		for _, i := range p.Invites {
			inv = append(inv, c.T("polity.invite", map[string]any{"name": i.Settlement.Name, "decision": c.T("polity.decision."+i.Decision, nil)}))
		}
		blocks = append(blocks, c.T("polity.proposal", map[string]any{"name": p.Name, "kind": c.T("polity.kind."+p.Kind, nil), "deadline": FormatDate(c, p.Deadline)})+"\n"+body(inv...))
		if p.MayRatify {
			addBtn(kb, c.T("polity.button.ratify", map[string]any{"name": p.Name}), AddrUnion, village.UnionActionRatify, p.ID)
			addBtn(kb, c.T("polity.button.refuse", map[string]any{"name": p.Name}), AddrUnion, village.UnionActionRefuse, p.ID)
		}
		if p.Mine {
			addBtn(kb, c.T("polity.button.withdraw", map[string]any{"name": p.Name}), AddrUnion, village.UnionActionWithdraw, p.ID)
		}
		if p.Ballot != nil && p.Ballot.MayVote {
			addBtn(kb, c.T("polity.button.yes", nil), AddrUnion, village.UnionActionVote, p.Ballot.ID, "yes")
			addBtn(kb, c.T("polity.button.no", nil), AddrUnion, village.UnionActionVote, p.Ballot.ID, "no")
		}
	}
	if d := v.Detail; d != nil {
		blocks = append(blocks, renderUnionDetail(c, kb, d))
	}
	if v.MayPropose {
		blocks = append(blocks, c.T("polity.union.propose_hint", nil))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrUnion}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

func renderUnionDetail(c Context, kb *keyboards.Builder, d *village.UnionDetail) string {
	var out []string
	out = append(out, c.T("polity.detail.title", map[string]any{"name": d.Name, "kind": c.T("polity.kind."+d.Kind, nil), "purse": FormatMoney(c, d.Purse)}))
	if len(d.Delegations) > 0 {
		var cs []string
		for _, x := range d.Delegations {
			cs = append(cs, c.T("polity.class."+x, nil))
		}
		out = append(out, c.T("polity.detail.delegations", map[string]any{"list": strings.Join(cs, "، ")}))
	}
	var ms []string
	for _, m := range d.Roster {
		ms = append(ms, c.T("polity.detail.member", map[string]any{"name": m.Settlement.Name, "status": c.T("polity.member."+m.Status, nil), "residents": FormatNumber(c, m.Residents), "arrears": strconv.Itoa(m.Arrears)}))
	}
	out = append(out, c.T("polity.detail.members", nil)+"\n"+body(ms...))
	var os []string
	for _, o := range d.Offices {
		var hs []string
		for _, h := range o.Holders {
			hs = append(hs, h.Name)
		}
		os = append(os, c.T("polity.detail.office", map[string]any{"title": o.Title, "holders": strings.Join(hs, "، "), "seats": strconv.Itoa(o.Seats)}))
	}
	if len(os) > 0 {
		out = append(out, c.T("polity.detail.offices", nil)+"\n"+body(os...))
	}
	for _, m := range d.Motions {
		out = append(out, c.T("polity.detail.motion", map[string]any{"kind": c.T("polity.motion."+m.Kind, nil), "subject": m.Subject.Name, "yes": strconv.Itoa(m.Yes), "no": strconv.Itoa(m.No), "closes": FormatDate(c, m.ClosesAt)}))
		if m.MayVote {
			addBtn(kb, c.T("polity.button.yes", nil), AddrUnion, village.UnionActionMotionVote, m.ID, "yes")
			addBtn(kb, c.T("polity.button.no", nil), AddrUnion, village.UnionActionMotionVote, m.ID, "no")
		}
	}
	for _, b := range d.Ballots {
		if b.MayVote && !b.Voted {
			addBtn(kb, c.T("polity.button.yes", nil), AddrUnion, village.UnionActionVote, b.ID, "yes")
			addBtn(kb, c.T("polity.button.no", nil), AddrUnion, village.UnionActionVote, b.ID, "no")
		}
	}
	if d.MayLeave {
		addBtn(kb, c.T("polity.button.leave", nil), AddrUnion, village.UnionActionLeave, d.ID)
	}
	if d.MayAccede {
		addBtn(kb, c.T("polity.button.accede", nil), AddrUnion, village.UnionActionAccede, d.ID)
	}
	return paragraphs(out...)
}

// Treaty renders the treaty screen.
func Treaty(c Context, v village.SettlementTreatyView) *presenter.Response {
	return c.withView(renderTreaty(c, v), ScreenTreaty, v)
}

func renderTreaty(c Context, v village.SettlementTreatyView) *presenter.Response {
	kb := keyboards.New()
	blocks := polityNotice(c, v.Notice, []string{c.T("polity.treaty.title", map[string]any{"name": v.Party.Name})})
	if !v.Open {
		blocks = append(blocks, c.T("polity.off", nil))
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrTreaty}))
		return c.respond(paragraphs(blocks...), kb.Build())
	}
	var ls []string
	for _, t := range v.Treaties {
		line := c.T("polity.treaty.line", map[string]any{"no": strconv.FormatInt(t.No, 10), "kind": c.named("diplomacy.treaty."+t.Kind.Code, t.Kind.Name), "other": t.Other.Name, "status": c.T("polity.treaty.status."+t.Status, nil)})
		if t.CoolingUntil != nil {
			line += " " + c.T("polity.treaty.cooling", map[string]any{"date": FormatDate(c, *t.CoolingUntil)})
		}
		ls = append(ls, line)
		switch t.Status {
		case "proposed":
			if t.Mine && v.MayPropose {
				addBtn(kb, c.T("polity.button.withdraw_treaty", nil), AddrTreaty, village.TreatyActionWithdraw, strconv.FormatInt(t.No, 10))
			} else if !t.Mine && v.MaySign {
				addBtn(kb, c.T("polity.button.accept", nil), AddrTreaty, village.TreatyActionAccept, strconv.FormatInt(t.No, 10))
				addBtn(kb, c.T("polity.button.decline", nil), AddrTreaty, village.TreatyActionDecline, strconv.FormatInt(t.No, 10))
			}
		case "active":
			if v.MayEnd {
				addBtn(kb, c.T("polity.button.end", nil), AddrTreaty, village.TreatyActionEnd, strconv.FormatInt(t.No, 10))
			}
		}
	}
	if len(ls) == 0 {
		blocks = append(blocks, c.T("polity.treaty.none", nil))
	} else {
		blocks = append(blocks, body(ls...))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrTreaty}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// Tender renders the tender screen.
func Tender(c Context, v village.TenderView) *presenter.Response {
	return c.withView(renderTender(c, v), ScreenTender, v)
}

func tenderText(c Context, t village.TenderLine) string {
	s := c.T("polity.tender.line", map[string]any{"building": t.Building.Name, "status": c.T("polity.tender.status."+t.Status, nil), "cap": FormatMoney(c, t.BudgetCap),
		"closes": FormatDate(c, t.ClosesAt), "stages": strconv.Itoa(t.StagesPaid)})
	for _, b := range t.Bids {
		s += "\n" + c.T("polity.tender.bid", map[string]any{"company": b.Company.Name, "price": FormatMoney(c, b.Price), "days": strconv.Itoa(b.Days), "status": c.T("polity.tender.bid_status."+b.Status, nil)})
	}
	return s
}

func renderTender(c Context, v village.TenderView) *presenter.Response {
	kb := keyboards.New()
	blocks := polityNotice(c, v.Notice, []string{c.T("polity.tender.title", map[string]any{"name": v.Village})})
	if !v.Open {
		blocks = append(blocks, c.T("polity.off", nil))
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrTender}))
		return c.respond(paragraphs(blocks...), kb.Build())
	}
	if len(v.Tenders) == 0 {
		blocks = append(blocks, c.T("polity.tender.none", nil))
	}
	for _, t := range v.Tenders {
		blocks = append(blocks, tenderText(c, t))
		if t.MayCancel {
			addBtn(kb, c.T("polity.button.cancel", nil), AddrTender, village.TenderActionCancel, t.ID)
		}
		if t.MayAward {
			addBtn(kb, c.T("polity.button.award", nil), AddrTender, village.TenderActionAward, t.ID)
		}
	}
	if len(v.Board) > 0 {
		blocks = append(blocks, c.T("polity.tender.board", nil))
		for _, t := range v.Board {
			blocks = append(blocks, tenderText(c, t))
		}
	}
	if len(v.Sites) > 0 {
		var ss []string
		for _, s := range v.Sites {
			ss = append(ss, c.T("polity.tender.site", map[string]any{"building": s.Building.Name}))
		}
		blocks = append(blocks, c.T("polity.tender.sites", nil)+"\n"+body(ss...))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrTender}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// Debt renders the debt screen.
func Debt(c Context, v village.DebtView) *presenter.Response {
	return c.withView(renderDebt(c, v), ScreenDebt, v)
}

func renderDebt(c Context, v village.DebtView) *presenter.Response {
	kb := keyboards.New()
	blocks := polityNotice(c, v.Notice, []string{c.T("polity.debt.title", map[string]any{"name": v.Party.Name})})
	if !v.Open {
		blocks = append(blocks, c.T("polity.off", nil))
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrDebt}))
		return c.respond(paragraphs(blocks...), kb.Build())
	}
	blocks = append(blocks, c.T("polity.debt.record", map[string]any{"taken": FormatNumber(c, v.Record.Taken), "repaid": FormatNumber(c, v.Record.Repaid), "late": FormatNumber(c, v.Record.Late), "defaulted": FormatNumber(c, v.Record.Defaulted)}))
	if len(v.Loans) == 0 {
		blocks = append(blocks, c.T("polity.debt.none", nil))
	}
	for _, l := range v.Loans {
		blocks = append(blocks, c.T("polity.debt.line", map[string]any{"role": c.T("polity.debt.role."+l.Mine, nil), "lender": l.Lender.Name, "borrower": l.Borrower.Name,
			"principal": FormatMoney(c, l.Principal), "owed": FormatMoney(c, l.Owed), "status": c.T("polity.debt.status."+l.Status, nil), "paid": strconv.Itoa(l.Paid), "periods": strconv.Itoa(l.Periods)}))
		if l.MayAccept {
			addBtn(kb, c.T("polity.button.accept", nil), AddrDebt, village.DebtActionAccept, l.ID)
			addBtn(kb, c.T("polity.button.decline", nil), AddrDebt, village.DebtActionDecline, l.ID)
		}
		if l.MayWithdraw {
			addBtn(kb, c.T("polity.button.withdraw_offer", nil), AddrDebt, village.DebtActionWithdraw, l.ID)
		}
		if l.MayWriteOff {
			addBtn(kb, c.T("polity.button.writeoff", nil), AddrDebt, village.DebtActionWriteOff, l.ID)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrDebt}))
	return c.respond(paragraphs(blocks...), kb.Build())
}
