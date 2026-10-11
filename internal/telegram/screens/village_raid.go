package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// «یورش» (docs/adr/0074), drawn for Telegram: the raids of the settlement, the targets it could raid, who stands where and the watch that
// sees raiders coming; and «بیمهٔ شهر», the settlement's own insurance fund.

// Screens and addresses.
const (
	ScreenRaid                = village.ScreenRaid
	AddrRaid                  = village.AddrRaid
	ScreenSettlementInsurance = village.ScreenInsurance
	AddrSettlementInsurance   = village.AddrInsurance
)

// isRaidRefusal reports a refusal kind of the raid or the insurance screen.
func isRaidRefusal(kind string) bool {
	return len(kind) > 5 && (kind[:5] == "raid_" || (len(kind) > 10 && kind[:10] == "insurance_"))
}

// Raid renders the raid screen.
func Raid(c Context, v village.RaidView) *presenter.Response {
	return c.withView(renderRaid(c, v), ScreenRaid, v)
}

func raidStatus(c Context, s string) string { return c.T("raid.status."+s, nil) }

func renderRaid(c Context, v village.RaidView) *presenter.Response {
	kb := keyboards.New()
	blocks := []string{c.T("raid.title", map[string]any{"name": v.Village})}
	if v.Notice != "" {
		blocks = append(blocks, c.T("raid.notice."+v.Notice, nil))
	}
	if !v.On {
		date := ""
		if v.StartsAt != nil {
			date = FormatDate(c, *v.StartsAt)
		}
		blocks = append(blocks, c.T("raid.off", map[string]any{"date": date}))
	} else {
		blocks = append(blocks, c.T("raid.rules", map[string]any{"fee": FormatMoney(c, v.Fee), "min": FormatNumber(c, v.MinLeadHours),
			"max": FormatNumber(c, v.MaxLeadHours), "window": FormatNumber(c, v.WindowMinutes)}))
	}
	var mine []string
	for _, r := range v.Mine {
		args := map[string]any{"other": r.Other.Name, "status": raidStatus(c, r.Status), "time": FormatClock(c, r.StartAt), "date": FormatDate(c, r.StartAt),
			"joined": FormatNumber(c, r.Joined), "standing": FormatNumber(c, r.Standing), "raiders": FormatNumber(c, r.Raiders)}
		key := "raid.line_attacker"
		if r.Side == village.RaidSideDefender {
			key = "raid.line_defender"
		}
		line := c.T(key, args)
		if r.Side == village.RaidSideDefender && (r.Status == "filed" || r.Status == "open") {
			if r.Warned {
				line = body(line, c.T("raid.warned", map[string]any{"by": c.T("settlement_raid_detector."+r.WarnedBy, nil), "lead": FormatDuration(c, time.Duration(r.LeadSecs)*time.Second)}))
			} else {
				line = body(line, c.T("raid.not_warned", nil))
			}
		}
		if r.Tribute > 0 {
			line = body(line, c.T("raid.tribute", map[string]any{"amount": FormatMoney(c, r.Tribute)}))
		}
		if o := r.Outcome; o != nil {
			key := "raid.outcome_held"
			if o.Taken {
				key = "raid.outcome_taken"
			}
			line = body(line, c.T(key, map[string]any{"cash": FormatMoney(c, o.Cash), "goods": FormatNumber(c, int64(len(o.Goods))), "buildings": FormatNumber(c, o.Buildings),
				"falls": FormatNumber(c, o.Falls)}))
			if o.FellMe {
				line = body(line, c.T("raid.fell_me", nil))
			}
		}
		mine = append(mine, line)
		if r.CanJoin {
			addBtn(kb, c.T("raid.button.join", map[string]any{"other": r.Other.Name}), village.AddrRaid, village.RaidActionJoin, r.ID)
		}
		if r.CanStand {
			addBtn(kb, c.T("raid.button.stand", map[string]any{"other": r.Other.Name}), village.AddrRaid, village.RaidActionStand, r.ID)
		}
		if r.CanAccept {
			addBtn(kb, c.T("raid.button.accept", map[string]any{"amount": FormatMoney(c, r.Tribute)}), village.AddrRaid, village.RaidActionAccept, r.ID)
		}
		if r.CanOffer && v.MayOffer {
			addBtn(kb, c.T("raid.button.offer", map[string]any{"amount": FormatMoney(c, v.TributeMin)}), village.AddrRaid, village.RaidActionOffer, r.ID, strconv.FormatInt(v.TributeMin, 10))
		}
	}
	if len(mine) == 0 {
		blocks = append(blocks, c.T("raid.none", nil))
	} else {
		blocks = append(blocks, c.T("raid.mine_head", nil)+"\n"+body(mine...))
	}
	if v.On && v.May && len(v.Targets) > 0 {
		var ts []string
		for _, t := range v.Targets {
			ts = append(ts, c.T("raid.target_line", map[string]any{"name": t.Settlement.Name, "code": t.Settlement.Code, "km": FormatNumber(c, t.DistanceKm), "residents": FormatNumber(c, t.Residents)}))
			addBtn(kb, c.T("raid.button.file", map[string]any{"name": t.Settlement.Name, "hours": FormatNumber(c, v.MinLeadHours)}), village.AddrRaid, village.RaidActionFile, t.Settlement.Code,
				strconv.FormatInt(v.MinLeadHours, 10))
		}
		blocks = append(blocks, c.T("raid.targets_head", nil)+"\n"+body(ts...))
	}
	if len(v.Watch) == 0 {
		blocks = append(blocks, c.T("raid.watch_none", nil))
	} else {
		var ws []string
		for _, w := range v.Watch {
			idle := w.Idle
			if idle == "" {
				idle = "unknown"
			}
			args := map[string]any{"kind": c.T("settlement_raid_detector."+w.Kind, nil), "sight": FormatNumber(c, w.SightM), "idle": c.T("raid.idle."+idle, nil)}
			if w.Held {
				ws = append(ws, c.T("raid.watch_open", args))
			} else {
				ws = append(ws, c.T("raid.watch_idle", args))
			}
		}
		blocks = append(blocks, c.T("raid.watch_head", nil)+"\n"+body(ws...))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrRaid}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// SettlementInsurance renders the settlement's insurance screen.
func SettlementInsurance(c Context, v village.SettlementInsuranceView) *presenter.Response {
	return c.withView(renderSettlementInsurance(c, v), ScreenSettlementInsurance, v)
}

func renderSettlementInsurance(c Context, v village.SettlementInsuranceView) *presenter.Response {
	kb := keyboards.New()
	blocks := []string{c.T("insurance.title", map[string]any{"name": v.Village})}
	if v.Notice != "" {
		blocks = append(blocks, c.T("insurance.notice."+v.Notice, nil))
	}
	if !v.Open {
		blocks = append(blocks, c.T("insurance.closed", nil))
		if v.MayRun {
			addBtn(kb, c.T("insurance.button.open", nil), village.AddrInsurance, village.InsuranceActionOpen)
		}
		kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrSettlementInsurance}))
		return c.respond(paragraphs(blocks...), kb.Build())
	}
	blocks = append(blocks, c.T("insurance.fund", map[string]any{"fund": FormatMoney(c, v.Fund), "scale": PercentFromBPS(c, int(v.ScaleBPS))}))
	var ps []string
	for _, p := range v.Products {
		args := map[string]any{"product": c.InsuranceProductName(p.Product), "premium": FormatMoney(c, p.Premium), "bps": PercentFromBPS(c, int(p.PremBPS)),
			"cover": PercentFromBPS(c, int(p.CoverBPS)), "own": PercentFromBPS(c, int(p.DeductibleBPS)), "max": FormatMoney(c, p.MaxClaim), "wait": FormatDuration(c, p.Waiting)}
		ps = append(ps, c.T("insurance.product."+p.Covers, args))
		if p.Held {
			continue
		}
		if p.Covers != "raid_damage" {
			addBtn(kb, c.T("insurance.button.buy", args), village.AddrInsurance, village.InsuranceActionBuy, p.Product.Code)
		}
		for _, t := range p.Targets {
			args["building"] = c.SettlementBuildingName(t)
			addBtn(kb, c.T("insurance.button.buy_building", args), village.AddrInsurance, village.InsuranceActionBuy, p.Product.Code, t.Code)
		}
	}
	blocks = append(blocks, c.T("insurance.products_head", nil)+"\n"+body(ps...))
	if len(v.Policies) > 0 {
		var ls []string
		for _, p := range v.Policies {
			ls = append(ls, c.T("insurance.policy", map[string]any{"no": FormatNumber(c, p.No), "product": c.InsuranceProductName(p.Product), "paid": FormatMoney(c, p.Paid),
				"status": c.T("insurance.status."+p.Status, nil)}))
		}
		blocks = append(blocks, c.T("insurance.mine", nil)+"\n"+body(ls...))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrVillageOverview, RefreshData: AddrSettlementInsurance}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// RaidDetectedLine is the warning the defender's group reads: the watch saw raiders coming.
func RaidDetectedLine(c Context, other, band string, lead time.Duration) string {
	return c.T("raid.line.detected", map[string]any{"other": other, "band": c.T("raid.band."+band, nil), "lead": FormatDuration(c, lead)})
}

// RaidOpenedLine is the line when the window of a raid opens.
func RaidOpenedLine(c Context, other string) string {
	return c.T("raid.line.opened", map[string]any{"other": other})
}

// RaidResolvedLine is how a raid ended.
func RaidResolvedLine(c Context, taken bool, cash, falls int64) string {
	key := "raid.line.resolved_held"
	if taken {
		key = "raid.line.resolved_taken"
	}
	return c.T(key, map[string]any{"cash": FormatMoney(c, cash), "falls": FormatNumber(c, falls)})
}

// RaidSettledLine is a raid ended with tribute.
func RaidSettledLine(c Context, amount int64) string {
	return c.T("raid.line.settled", map[string]any{"amount": FormatMoney(c, amount)})
}

// RaidNotice renders a raid notice for a group.
func RaidNotice(c Context, v village.RaidNoticeView) *presenter.Response {
	var line string
	switch v.Kind {
	case village.RaidNoticeDetected:
		line = RaidDetectedLine(c, v.Other, v.Band, time.Duration(v.LeadSecs)*time.Second)
	case village.RaidNoticeOpened:
		line = RaidOpenedLine(c, v.Other)
	case village.RaidNoticeResolved:
		line = RaidResolvedLine(c, v.Taken, v.Cash, v.Falls)
	default:
		line = RaidSettledLine(c, v.Amount)
	}
	return c.withView(c.respond(line, nil), ScreenRaidNoticeName, v)
}

// ScreenRaidNoticeName is the raid notice's screen name.
const ScreenRaidNoticeName = village.ScreenRaidNotice
