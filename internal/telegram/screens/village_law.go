package screens

import (
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Police, courts, informants, the hidden board, laundering and customs (docs/adr/0078), drawn for Telegram. The long forms (a sentence, an offence's
// bounds, a manifest, a bribe's size) are written in the Mini App; here the group sees the state and presses the short acts.

const (
	ScreenLaw = village.ScreenLaw
	AddrLaw   = village.AddrLaw
)

// isLawRefusal reports a refusal kind of the law screen.
func isLawRefusal(kind string) bool { return strings.HasPrefix(kind, "law_") }

func lawNotice(c Context, notice string, blocks []string) []string {
	if notice != "" {
		return append(blocks, c.T("law.notice."+notice, nil))
	}
	return blocks
}

func lawBtn(c Context, kb *keyboards.Builder, key string, args map[string]any, parts ...string) {
	addBtn(kb, c.T("law.button."+key, args), append([]string{AddrLaw}, parts...)...)
}

// Law renders the law screen.
func Law(c Context, v village.LawView) *presenter.Response {
	return c.withView(renderLaw(c, v), ScreenLaw, v)
}

func renderLaw(c Context, v village.LawView) *presenter.Response {
	kb := keyboards.New()
	blocks := lawNotice(c, v.Notice, []string{c.T("law.title", map[string]any{"name": v.Village})})
	nav := func() *presenter.Response {
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrLaw}))
		return c.respond(paragraphs(blocks...), kb.Build())
	}
	if !v.Open.Police && !v.Open.Courts && !v.Open.Black && !v.Open.Customs && !v.Open.Launder {
		blocks = append(blocks, c.T("law.off", nil))
		return nav()
	}
	blocks = append(blocks, c.T("law.model."+orDefault(v.Model, "adversarial"), nil))
	var ps []string
	for _, p := range v.Posts {
		state := "none"
		switch {
		case p.Open:
			state = "open"
		case p.Stands:
			state = "closed"
		}
		line := c.T("law.post."+state, map[string]any{"name": p.Building.Name})
		if p.Service == "custody" && p.Open {
			line += " " + c.T("law.post.cells", map[string]any{"held": FormatNumber(c, p.Held), "cells": FormatNumber(c, p.Cells)})
		}
		ps = append(ps, line)
	}
	blocks = append(blocks, body(ps...))
	if v.Open.Police {
		blocks = append(blocks, c.T("law.points", map[string]any{"left": FormatNumber(c, v.Points-v.Spent), "all": FormatNumber(c, v.Points), "patrol": FormatNumber(c, v.Patrol)}))
		if v.Mays.Patrol {
			lawBtn(c, kb, "patrol", nil, village.LawActionPatrol)
		}
	}
	var os []string
	for _, o := range v.Offences {
		if o.Listed {
			os = append(os, c.T("law.offence", map[string]any{"name": lawOffenceName(c, o.Code, o.Name), "class": c.T("law.class."+o.Class, nil)}))
		}
	}
	if len(os) > 0 {
		blocks = append(blocks, c.T("law.offences_head", nil)+"\n"+body(os...))
	} else {
		blocks = append(blocks, c.T("law.offences_none", nil))
	}
	if v.Captured {
		blocks = append(blocks, c.T("law.captured", nil))
	}
	// the cases
	var cs []string
	for _, k := range v.Cases {
		cs = append(cs, c.T("law.case", map[string]any{"no": strconv.FormatInt(k.No, 10), "offence": lawOffenceName(c, k.Offence.Code, k.Offence.Name), "accused": k.Accused.Name,
			"state": c.T("law.state."+k.State, nil), "strength": FormatNumber(c, k.Strength)}))
		lawBtn(c, kb, "open", map[string]any{"no": strconv.FormatInt(k.No, 10)}, village.LawActionOpen, k.ID)
	}
	if len(cs) > 0 {
		blocks = append(blocks, c.T("law.cases_head", nil)+"\n"+body(cs...))
	}
	if d := v.Case; d != nil {
		var es []string
		for _, e := range d.Evidence {
			key := "law.evidence"
			if !e.Proper {
				key = "law.evidence_improper"
			}
			if e.Excluded {
				key = "law.evidence_excluded"
			}
			es = append(es, c.T(key, map[string]any{"kind": c.T("law.kind."+e.Kind, nil), "strength": FormatNumber(c, e.Strength)}))
			if !e.Proper && !e.Excluded && d.Line.Accused.Name != "" {
				lawBtn(c, kb, "exclude", nil, village.LawActionExclude, e.ID)
			}
		}
		blocks = append(blocks, c.T("law.case_detail", map[string]any{"no": strconv.FormatInt(d.Line.No, 10), "total": FormatNumber(c, d.Total), "quality": FormatNumber(c, d.Quality)})+"\n"+body(es...))
		for _, vd := range d.Verdicts {
			key := "law.verdict_not_guilty"
			if vd.Guilty {
				key = "law.verdict_guilty"
			}
			blocks = append(blocks, c.T(key, map[string]any{"instance": strconv.Itoa(vd.Instance)}))
		}
		id := d.Line.ID
		if d.MayInvestigate {
			lawBtn(c, kb, "investigate", nil, village.LawActionInvestigate, id)
		}
		if d.MayAskWarrant {
			lawBtn(c, kb, "warrant_ask", nil, village.LawActionWarrantAsk, id)
		}
		if d.MayArrest {
			lawBtn(c, kb, "arrest", nil, village.LawActionArrest, id)
		}
		if d.MayCharge {
			lawBtn(c, kb, "charge", nil, village.LawActionCharge, id)
		}
		if d.MayDrop {
			lawBtn(c, kb, "drop", nil, village.LawActionDrop, id)
		}
		if d.MayDefend && d.Line.State != "" {
			lawBtn(c, kb, "defend", nil, village.LawActionDefendNPC, id)
		}
		switch d.MayAppealAs {
		case "accused":
			lawBtn(c, kb, "appeal", nil, village.LawActionAppealAccused, id)
		case "prosecution":
			lawBtn(c, kb, "appeal", nil, village.LawActionAppealProsecution, id)
		}
		if d.MayDecide {
			lawBtn(c, kb, "appeal_confirm", nil, village.LawActionAppealConfirm, id)
			lawBtn(c, kb, "appeal_quash", nil, village.LawActionAppealQuash, id)
		}
	}
	// my sentences
	var ss []string
	for _, s := range v.Mine {
		ss = append(ss, c.T("law.sentence."+s.Kind, map[string]any{"fine": FormatMoney(c, s.Fine), "hours": FormatNumber(c, s.JailHours), "days": FormatNumber(c, s.BanDays),
			"ban": c.T("law.ban."+orDefault(s.Ban, "market"), nil), "status": c.T("law.sentence_status."+s.Status, nil)}))
		if s.MayBail {
			lawBtn(c, kb, "bail", map[string]any{"price": FormatMoney(c, s.Bail)}, village.LawActionBail, s.ID)
		}
		if s.MayPardon {
			lawBtn(c, kb, "pardon", nil, village.LawActionPardon, s.ID)
		}
	}
	if len(ss) > 0 {
		blocks = append(blocks, c.T("law.sentences_head", nil)+"\n"+body(ss...))
	}
	// informants
	var is []string
	for _, i := range v.Informants {
		is = append(is, c.T("law.informant", map[string]any{"reliability": FormatNumber(c, int64(i.Reliability)), "state": c.T("law.informant_state."+i.State, nil)}))
	}
	if len(is) > 0 {
		blocks = append(blocks, c.T("law.informants_head", nil)+"\n"+body(is...))
	}
	// bribes
	for _, b := range v.Bribes {
		blocks = append(blocks, c.T("law.bribe", map[string]any{"rung": c.T("law.rung."+b.Rung, nil), "amount": FormatMoney(c, b.Amount), "status": c.T("law.bribe_status."+b.Status, nil)}))
		if b.MayAnswer {
			lawBtn(c, kb, "bribe_accept", nil, village.LawActionBribeAccept, b.ID)
			lawBtn(c, kb, "bribe_refuse", nil, village.LawActionBribeRefuse, b.ID)
		}
	}
	// the hidden board
	if v.Open.Black {
		if !v.Board.Known {
			lawBtn(c, kb, "ask", nil, village.LawActionAsk)
		} else {
			blocks = append(blocks, c.T("law.board", map[string]any{"standing": FormatNumber(c, v.Board.Standing), "dirty": FormatMoney(c, v.Board.Dirty)}))
			for _, l := range v.Board.Listings {
				blocks = append(blocks, c.T("law.listing", map[string]any{"item": l.Item.Name, "qty": FormatNumber(c, l.Qty), "price": FormatMoney(c, l.Price)}))
				if !l.Mine {
					lawBtn(c, kb, "buy", map[string]any{"item": l.Item.Name}, village.LawActionBuy, l.ID)
				} else {
					lawBtn(c, kb, "delist", map[string]any{"item": l.Item.Name}, village.LawActionDelist, l.ID)
				}
			}
			for _, e := range v.Board.Escrows {
				if e.Status != "held" {
					continue
				}
				blocks = append(blocks, c.T("law.escrow", map[string]any{"item": e.Item.Name, "amount": FormatMoney(c, e.Amount)}))
				if e.Buyer {
					lawBtn(c, kb, "confirm", nil, village.LawActionConfirm, e.ID)
				}
				if e.Mine || e.Broker || e.Buyer {
					lawBtn(c, kb, "refund", nil, village.LawActionRefund, e.ID)
				}
				if e.Broker {
					lawBtn(c, kb, "scam", nil, village.LawActionScam, e.ID)
				}
			}
			if !v.Board.Broker {
				lawBtn(c, kb, "broker", nil, village.LawActionBroker)
			}
		}
	}
	for _, f := range v.Fronts {
		blocks = append(blocks, c.T("law.front", map[string]any{"company": f.Company.Name, "batches": strconv.Itoa(len(f.Batches))}))
	}
	for _, l := range v.Loads {
		blocks = append(blocks, c.T("law.load", map[string]any{"to": l.To.Name, "status": c.T("law.load_status."+l.Status, nil)}))
		if l.MayCross {
			lawBtn(c, kb, "cross", nil, village.LawActionCross, l.ID)
		}
	}
	return nav()
}

// lawOffenceName words an offence by its code in the viewer's language, falling back to the name the content gave.
func lawOffenceName(c Context, code, fallback string) string {
	key := "offence." + code
	if t := c.T(key, nil); t != "" && t != key {
		return t
	}
	return fallback
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
