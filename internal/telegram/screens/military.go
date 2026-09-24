package screens

import (
	"strconv"
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The armed forces (docs/adr/0022-military-and-diplomacy.md): a country's
// ministry of defence — its offices, its budget and a summary of its forces —
// the forces by branch, one branch's equipment and where it is stationed,
// stationing it, and procurement.
//
// What anyone may read is the public summary: offices, the budget (a state's
// budget is public), and each class of equipment told in a band — «a few»,
// «a squadron» — never a count. Exact counts, attributes, garrisons,
// readiness and upkeep are for the holders of the offices military.yml clears
// (MinistryView.Cleared), and only in their private chat. A branch is named
// military.branch.<code>, a class military.class.<code>, a band
// military.band.<code>.

// Callback addresses of the military screens.
const (
	AddrMinistry = "military:ministry"
	AddrForces   = "military:forces"
	AddrBranch   = "military:branch"
	AddrStation  = "military:station"
	AddrProcure  = "military:procure"
	AddrArmsBuy  = "military:buy"
)

// MilitaryConfirm confirms a purchase or a move.
const MilitaryConfirm = "yes"

// BranchName names a branch of the forces.
func (c Context) BranchName(n Named) string { return c.named("military.branch_name."+n.Code, n.Name) }

// ForceClassName names a class of equipment.
func (c Context) ForceClassName(n Named) string {
	return c.named("military.class_name."+n.Code, n.Name)
}

// BandName says how many a band covers, in words.
func (c Context) BandName(code string) string { return c.named("military.band."+code, code) }

// ForceClassLine is one class of a country's equipment: how many, told in a
// band to everyone and exactly to the cleared.
type ForceClassLine struct {
	Class Named
	Band  string
	// Count is exact; zero unless the viewer is cleared.
	Count int64
}

// BranchForces is one branch and its classes.
type BranchForces struct {
	Branch  Named
	Classes []ForceClassLine
}

// PeriodLine is a settled defence period, as the ministry reports it.
type PeriodLine struct {
	Levy          int64
	Appropriation int64
	UpkeepDue     int64
	UpkeepPaid    int64
}

// MinistryView is a country's ministry of defence.
type MinistryView struct {
	Country GovPlace
	// Offices are the defence offices and who holds or acts for each.
	Offices []GovOffice
	// Treasury and Fund are the national treasury's and the defence fund's
	// balances: a state's budget is public.
	Treasury, Fund int64
	// The levers in force: the cities' share of their revenue, the defence
	// share of it, the arms export policy.
	RevenueShareBPS, DefenceBudgetBPS, ArmsExports int64
	// Last is the last settled period, nil before the first; NextIn and
	// NextAt when the next ends, NextIn zero when unknown.
	Last   *PeriodLine
	NextIn time.Duration
	NextAt time.Time
	// Forces is the summary by branch.
	Forces []BranchForces
	// Cleared is a viewer who sees the forces in full; Readiness and
	// Upkeep are theirs to read.
	Cleared   bool
	Readiness int64
	Upkeep    int64
	// CanProcure is the viewer who buys arms for the state.
	CanProcure bool
	Notice     string
}

// armsExportsKey words the arms export policy.
func armsExportsKey(v int64) string {
	switch {
	case v <= 0:
		return "military.exports.domestic"
	case v == 1:
		return "military.exports.partners"
	}
	return "military.exports.open"
}

// Ministry renders a country's ministry of defence.
func Ministry(c Context, v MinistryView) *presenter.Response {
	country := c.PlaceName(v.Country)
	head := []string{c.T("military.ministry.title", map[string]any{"country": country})}
	if v.Notice != "" {
		head = append([]string{v.Notice, ""}, head...)
	}
	offices := []string{c.T("military.ministry.offices", nil)}
	for _, o := range v.Offices {
		offices = append(offices, govOfficeLine(c, o))
	}
	budget := []string{
		c.T("military.ministry.budget", nil),
		c.T("military.ministry.treasury", map[string]any{"amount": FormatMoney(c, v.Treasury)}),
		c.T("military.ministry.fund", map[string]any{"amount": FormatMoney(c, v.Fund)}),
		c.T("military.ministry.shares", map[string]any{
			"share":   c.T("gov.percent", map[string]any{"value": PercentFromBPS(c, int(v.RevenueShareBPS))}),
			"defence": c.T("gov.percent", map[string]any{"value": PercentFromBPS(c, int(v.DefenceBudgetBPS))}),
		}),
		c.T("military.ministry.exports", map[string]any{"policy": c.T(armsExportsKey(v.ArmsExports), nil)}),
	}
	if v.Last != nil {
		budget = append(budget, c.T("military.ministry.last", map[string]any{
			"levy": FormatMoney(c, v.Last.Levy), "appropriation": FormatMoney(c, v.Last.Appropriation)}))
		if v.Cleared {
			key := "military.ministry.upkeep_paid"
			if v.Last.UpkeepPaid < v.Last.UpkeepDue {
				key = "military.ministry.upkeep_short"
			}
			if v.Last.UpkeepDue > 0 {
				budget = append(budget, c.T(key, map[string]any{
					"paid": FormatMoney(c, v.Last.UpkeepPaid), "due": FormatMoney(c, v.Last.UpkeepDue)}))
			}
		}
	}
	if v.NextIn > 0 {
		budget = append(budget, c.T("military.ministry.next", map[string]any{
			"in": FormatDuration(c, v.NextIn), "at": FormatClock(c, v.NextAt)}))
	}
	forces := []string{c.T("military.ministry.forces", nil)}
	forces = append(forces, forceLines(c, v.Forces, v.Cleared)...)
	if v.Cleared {
		forces = append(forces, c.T("military.forces.readiness", map[string]any{"value": c.T("gov.percent", map[string]any{"value": PercentFromBPS(c, int(v.Readiness))})}),
			c.T("military.forces.upkeep", map[string]any{"amount": FormatMoney(c, v.Upkeep)}))
	}

	kb := keyboards.New()
	forcesBtn, _ := keyboards.Button(c.T("military.button.forces", nil), AddrForces, v.Country.Code)
	if v.CanProcure && !c.Shared {
		procure, _ := keyboards.Button(c.T("military.button.procure", nil), AddrProcure, v.Country.Code)
		kb.Row(forcesBtn, procure)
	} else {
		kb.Row(forcesBtn)
	}
	sanctions, _ := keyboards.Button(c.T("diplomacy.button.sanctions", nil), AddrSanctions, v.Country.Code)
	treaties, _ := keyboards.Button(c.T("diplomacy.button.treaties", nil), AddrTreaties, v.Country.Code)
	kb.Row(sanctions, treaties)
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrGovCity, RefreshData: keyboards.Data(AddrMinistry, v.Country.Code)}))
	return c.respond(paragraphs(body(head...), body(offices...), body(budget...), body(forces...),
		c.T("military.ministry.footer", nil)), kb.Build())
}

// forceLines lists classes by branch: a band to everyone, the exact count
// to the cleared.
func forceLines(c Context, branches []BranchForces, cleared bool) []string {
	var lines []string
	for _, b := range branches {
		if len(b.Classes) == 0 {
			continue
		}
		lines = append(lines, c.T("military.forces.branch", map[string]any{"branch": c.BranchName(b.Branch)}))
		for _, cl := range b.Classes {
			args := map[string]any{"class": c.ForceClassName(cl.Class), "band": c.BandName(cl.Band)}
			key := "military.forces.class_band"
			if cleared {
				key, args["count"] = "military.forces.class_count", FormatNumber(c, cl.Count)
			}
			lines = append(lines, c.T(key, args))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, c.T("military.forces.none", nil))
	}
	return lines
}

// ForcesView is a country's forces by branch.
type ForcesView struct {
	Country  GovPlace
	Branches []BranchForces
	// Cleared viewers see counts, readiness and upkeep, and a button per
	// branch.
	Cleared   bool
	Readiness int64
	Upkeep    int64
	// Moving is how many pieces are on their way to a garrison.
	Moving int64
}

// Forces renders a country's forces by branch.
func Forces(c Context, v ForcesView) *presenter.Response {
	lines := []string{c.T("military.forces.title", map[string]any{"country": c.PlaceName(v.Country)})}
	lines = append(lines, forceLines(c, v.Branches, v.Cleared && !c.Shared)...)
	kb := keyboards.New()
	if v.Cleared && !c.Shared {
		lines = append(lines, "", c.T("military.forces.readiness", map[string]any{"value": c.T("gov.percent", map[string]any{"value": PercentFromBPS(c, int(v.Readiness))})}),
			c.T("military.forces.upkeep", map[string]any{"amount": FormatMoney(c, v.Upkeep)}))
		if v.Moving > 0 {
			lines = append(lines, c.T("military.forces.moving", map[string]any{"count": FormatNumber(c, v.Moving)}))
		}
		var buttons []presenter.Button
		for _, b := range v.Branches {
			if btn, ok := keyboards.Button(c.T("military.button.branch", map[string]any{"branch": c.BranchName(b.Branch)}),
				AddrBranch, v.Country.Code, b.Branch.Code); ok {
				buttons = append(buttons, btn)
			}
		}
		kb.Grid(2, buttons...)
	}
	footer := c.T("military.forces.public", nil)
	if v.Cleared && !c.Shared {
		footer = c.T("military.forces.secret", nil)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrMinistry, v.Country.Code),
		RefreshData: keyboards.Data(AddrForces, v.Country.Code)}))
	return c.respond(paragraphs(body(lines...), footer), kb.Build())
}

// GarrisonLine is how many pieces of a group stand in one city.
type GarrisonLine struct {
	CityCode, City string
	Count          int64
}

// AssetGroup is one good (and design) of a branch's equipment.
type AssetGroup struct {
	Good  Good
	Class Named
	Count int64
	// Quality is the pieces' average.
	Quality int
	// Garrisons are where its pieces stand; Depot how many are stationed
	// nowhere yet (delivered, not deployed); Moving how many are on the
	// way.
	Garrisons []GarrisonLine
	Depot     int64
	Moving    int64
	// Attributes are the design's, in full: the cleared see the signature.
	Attributes []AttributeLine
	// SeenAt is how far a reference radar (a 1 m² target at
	// ReferenceRadarKM) sees it, for equipment with a radar cross-section;
	// zero when it has none.
	SeenAt int64
}

// MoveLine is equipment on its way to a garrison.
type MoveLine struct {
	Good           Good
	Qty            int64
	CityCode, City string
	Left           time.Duration
	At             time.Time
}

// BranchView is one branch's equipment, for a cleared viewer.
type BranchView struct {
	Country GovPlace
	Branch  Named
	Groups  []AssetGroup
	Moves   []MoveLine
	// CanStation is the viewer who commands the branch (or acts for its
	// commander).
	CanStation bool
	// ReferenceRadarKM is the reference radar SeenAt is measured against.
	ReferenceRadarKM int64
	Notice           string
}

// attributeValue renders a military attribute in its unit.
func attributeValue(c Context, name string, v int64) string {
	switch name {
	case "rcs":
		return c.T("military.unit.rcs", map[string]any{"value": formatMilli(c, v)})
	case "detection_range", "range":
		return c.T("military.unit.km", map[string]any{"value": FormatNumber(c, v)})
	case "speed":
		return c.T("military.unit.kmh", map[string]any{"value": FormatNumber(c, v)})
	case "payload":
		return c.T("military.unit.kg", map[string]any{"value": FormatNumber(c, v)})
	case "depth":
		return c.T("military.unit.m", map[string]any{"value": FormatNumber(c, v)})
	case "accuracy", "interception":
		return c.T("gov.percent", map[string]any{"value": PercentFromBPS(c, int(v))})
	case "rcs_gain":
		return c.T("military.unit.times", map[string]any{"value": formatRatio(c, v)})
	}
	return FormatNumber(c, v)
}

// formatMilli writes thousandths as a decimal: 5 as 0.005, 5000 as 5.
func formatMilli(c Context, v int64) string {
	whole, frac := v/1000, v%1000
	s := FormatNumber(c, whole)
	if frac == 0 {
		return s
	}
	digits := strconv.FormatInt(frac+1000, 10)[1:]
	for len(digits) > 0 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
	}
	return s + c.T("military.unit.decimal_point", nil) + c.numerals().localise(digits)
}

// formatRatio writes basis points as a multiple: 200000 as 20.
func formatRatio(c Context, bps int64) string {
	return formatMilli(c, bps/10)
}

// Branch renders one branch's equipment and where it stands.
func Branch(c Context, v BranchView) *presenter.Response {
	blocks := []string{}
	if v.Notice != "" {
		blocks = append(blocks, v.Notice)
	}
	blocks = append(blocks, c.T("military.equipment.title", map[string]any{"branch": c.BranchName(v.Branch),
		"country": c.PlaceName(v.Country)}))
	kb := keyboards.New()
	if len(v.Groups) == 0 {
		blocks = append(blocks, c.T("military.equipment.empty", nil))
	}
	for _, g := range v.Groups {
		lines := []string{c.T("military.equipment.group", map[string]any{"good": c.GoodName(g.Good),
			"count": FormatNumber(c, g.Count), "quality": FormatNumber(c, int64(g.Quality))})}
		for _, at := range g.Attributes {
			lines = append(lines, c.T("military.equipment.attribute", map[string]any{"name": c.AttributeName(at.Name),
				"value": attributeValue(c, at.Name, at.Value)}))
		}
		if g.SeenAt > 0 {
			lines = append(lines, c.T("military.equipment.seen_at", map[string]any{
				"km": FormatNumber(c, g.SeenAt), "radar": FormatNumber(c, v.ReferenceRadarKM)}))
		}
		for _, gl := range g.Garrisons {
			lines = append(lines, c.T("military.equipment.garrison", map[string]any{"city": c.CityName(gl.CityCode, gl.City),
				"count": FormatNumber(c, gl.Count)}))
		}
		if g.Depot > 0 {
			lines = append(lines, c.T("military.equipment.depot", map[string]any{"count": FormatNumber(c, g.Depot)}))
		}
		if g.Moving > 0 {
			lines = append(lines, c.T("military.equipment.moving", map[string]any{"count": FormatNumber(c, g.Moving)}))
		}
		blocks = append(blocks, body(lines...))
		if v.CanStation && g.Count > g.Moving {
			kb.Add(c.T("military.button.station", map[string]any{"good": c.GoodName(g.Good)}),
				AddrStation, v.Country.Code, g.Good.target())
		}
	}
	if len(v.Moves) > 0 {
		lines := []string{c.T("military.equipment.moves", nil)}
		for _, m := range v.Moves {
			lines = append(lines, c.T("military.equipment.move", map[string]any{"good": c.GoodName(m.Good),
				"count": FormatNumber(c, m.Qty), "city": c.CityName(m.CityCode, m.City),
				"left": FormatDuration(c, m.Left), "at": FormatClock(c, m.At)}))
		}
		blocks = append(blocks, body(lines...))
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrForces, v.Country.Code),
		RefreshData: keyboards.Data(AddrBranch, v.Country.Code, v.Branch.Code)}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// StationView is ordering equipment to a garrison: choose the city, then how
// many, then confirm.
type StationView struct {
	Country GovPlace
	Branch  Named
	Good    Good
	// Available is how many may be ordered: the group's pieces not moving
	// and not already in the chosen city.
	Available int64
	// Cities are the country's cities to choose from; CityCode and City the
	// chosen one.
	Cities         []GovPlace
	CityCode, City string
	Qty            int64
	// Time is how long the move takes, real time on the game clock.
	Time time.Duration
	// Confirm asks for the confirmation of Qty.
	Confirm bool
}

// Station renders the stationing flow.
func Station(c Context, v StationView) *presenter.Response {
	good := c.GoodName(v.Good)
	lines := []string{c.T("military.station.title", map[string]any{"good": good})}
	kb := keyboards.New()
	back := keyboards.Data(AddrBranch, v.Country.Code, v.Branch.Code)
	switch {
	case v.CityCode == "":
		lines = append(lines, c.T("military.station.choose_city", nil))
		var buttons []presenter.Button
		for _, city := range v.Cities {
			if b, ok := keyboards.Button(c.CityName(city.Code, city.Name), AddrStation, v.Country.Code, v.Good.target(),
				city.Code); ok {
				buttons = append(buttons, b)
			}
		}
		kb.Grid(2, buttons...)
	case !v.Confirm:
		city := c.CityName(v.CityCode, v.City)
		lines = append(lines, c.T("military.station.choose_qty", map[string]any{"city": city,
			"available": FormatNumber(c, v.Available), "time": FormatDuration(c, v.Time)}))
		var buttons []presenter.Button
		seen := map[int64]bool{}
		for _, n := range []int64{1, 5, 10, v.Available} {
			if n < 1 || n > v.Available || seen[n] {
				continue
			}
			seen[n] = true
			if b, ok := keyboards.Button(c.T("military.button.qty", map[string]any{"count": FormatNumber(c, n)}), AddrStation,
				v.Country.Code, v.Good.target(), v.CityCode, strconv.FormatInt(n, 10)); ok {
				buttons = append(buttons, b)
			}
		}
		kb.Grid(4, buttons...)
		back = keyboards.Data(AddrStation, v.Country.Code, v.Good.target())
	default:
		lines = append(lines, c.T("military.station.confirm", map[string]any{"count": FormatNumber(c, v.Qty),
			"city": c.CityName(v.CityCode, v.City), "time": FormatDuration(c, v.Time)}))
		kb.Add(c.T("military.button.confirm_station", nil), AddrStation, v.Country.Code, v.Good.target(), v.CityCode,
			strconv.FormatInt(v.Qty, 10), MilitaryConfirm)
		back = keyboards.Data(AddrStation, v.Country.Code, v.Good.target(), v.CityCode)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(body(lines...), kb.Build())
}

// ProcureOffer is one listing of military goods, as a state's buyer sees it.
type ProcureOffer struct {
	No             int64
	Good           Good
	Company        CompanyRef
	CityCode, City string
	Country        GovPlace
	Left           int64
	Price          int64
	// Blocked says why the state may not buy it: "" when it may,
	// ProcureBlockedExport or ProcureBlockedEmbargo.
	Blocked string
}

// Why a listing is not for a state.
const (
	ProcureBlockedExport  = "export"
	ProcureBlockedEmbargo = "embargo"
)

// ProcureView is procurement: the military goods for sale that the state
// may buy.
type ProcureView struct {
	Country GovPlace
	Fund    int64
	Offers  []ProcureOffer
	Notice  string
}

// Procure renders procurement.
func Procure(c Context, v ProcureView) *presenter.Response {
	blocks := []string{}
	if v.Notice != "" {
		blocks = append(blocks, v.Notice)
	}
	blocks = append(blocks, body(c.T("military.procure.title", map[string]any{"country": c.PlaceName(v.Country)}),
		c.T("military.procure.fund", map[string]any{"amount": FormatMoney(c, v.Fund)})))
	kb := keyboards.New()
	if len(v.Offers) == 0 {
		blocks = append(blocks, c.T("military.procure.none", nil))
	}
	var lines []string
	for _, o := range v.Offers {
		args := map[string]any{"good": c.GoodName(o.Good), "company": o.Company.Name,
			"city": c.CityName(o.CityCode, o.City), "country": c.PlaceName(o.Country),
			"left": FormatNumber(c, o.Left), "price": FormatMoney(c, o.Price)}
		switch o.Blocked {
		case ProcureBlockedExport:
			lines = append(lines, c.T("military.procure.offer_export", args))
		case ProcureBlockedEmbargo:
			lines = append(lines, c.T("military.procure.offer_embargo", args))
		default:
			lines = append(lines, c.T("military.procure.offer", args))
			kb.Add(c.T("military.button.offer", map[string]any{"good": c.GoodName(o.Good), "price": FormatMoney(c, o.Price)}),
				AddrArmsBuy, v.Country.Code, strconv.FormatInt(o.No, 10))
		}
	}
	if len(lines) > 0 {
		blocks = append(blocks, body(lines...))
	}
	blocks = append(blocks, c.T("military.procure.footer", nil))
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrMinistry, v.Country.Code),
		RefreshData: keyboards.Data(AddrProcure, v.Country.Code)}))
	return c.respond(paragraphs(blocks...), kb.Build())
}

// ArmsBuyView is one listing a state may buy from: how many, then confirm.
type ArmsBuyView struct {
	Country    GovPlace
	Offer      ProcureOffer
	Attributes []AttributeLine
	Fund       int64
	Qty        int64
	// Confirm asks for the confirmation of Qty at Total.
	Confirm bool
	Total   int64
}

// ArmsBuy renders a purchase of arms.
func ArmsBuy(c Context, v ArmsBuyView) *presenter.Response {
	o := v.Offer
	lines := []string{c.T("military.buy.title", map[string]any{"good": c.GoodName(o.Good)}),
		c.T("military.buy.seller", map[string]any{"company": o.Company.Name, "city": c.CityName(o.CityCode, o.City),
			"country": c.PlaceName(o.Country)}),
		c.T("military.buy.price", map[string]any{"price": FormatMoney(c, o.Price), "left": FormatNumber(c, o.Left)})}
	for _, at := range v.Attributes {
		lines = append(lines, c.T("military.equipment.attribute", map[string]any{"name": c.AttributeName(at.Name),
			"value": attributeValue(c, at.Name, at.Value)}))
	}
	lines = append(lines, c.T("military.procure.fund", map[string]any{"amount": FormatMoney(c, v.Fund)}))
	kb := keyboards.New()
	no := strconv.FormatInt(o.No, 10)
	back := keyboards.Data(AddrProcure, v.Country.Code)
	if v.Confirm {
		lines = append(lines, "", c.T("military.buy.confirm", map[string]any{"count": FormatNumber(c, v.Qty),
			"good": c.GoodName(o.Good), "total": FormatMoney(c, v.Total), "after": FormatMoney(c, v.Fund-v.Total)}))
		kb.Add(c.T("military.button.confirm_buy", nil), AddrArmsBuy, v.Country.Code, no, strconv.FormatInt(v.Qty, 10),
			MilitaryConfirm)
		back = keyboards.Data(AddrArmsBuy, v.Country.Code, no)
	} else {
		lines = append(lines, "", c.T("military.buy.choose", nil))
		var buttons []presenter.Button
		seen := map[int64]bool{}
		for _, n := range []int64{1, 2, 5, 10, o.Left} {
			if n < 1 || n > o.Left || seen[n] || n*o.Price > v.Fund {
				continue
			}
			seen[n] = true
			if b, ok := keyboards.Button(c.T("military.button.qty", map[string]any{"count": FormatNumber(c, n)}), AddrArmsBuy,
				v.Country.Code, no, strconv.FormatInt(n, 10)); ok {
				buttons = append(buttons, b)
			}
		}
		if len(buttons) == 0 {
			lines = append(lines, c.T("military.buy.cannot_afford", nil))
		}
		kb.Grid(5, buttons...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(body(lines...), kb.Build())
}

// Military refusals.
const (
	MilitaryRefusedNotFound  = "not_found"
	MilitaryRefusedNotHolder = "not_holder"
	MilitaryRefusedNotArms   = "not_arms"
	MilitaryRefusedExport    = "export"
	MilitaryRefusedFunds     = "funds"
	MilitaryRefusedStock     = "stock"
	MilitaryRefusedCity      = "city"
	MilitaryRefusedNoCountry = "no_country"
)

// MilitaryRefusalView is a refused military command.
type MilitaryRefusalView struct {
	Kind    string
	Country GovPlace
	// Office is the office whose holder may do it (not_holder).
	Office string
	// Need and Have are money (funds); Max a count (stock).
	Need, Have int64
	Max        int64
	Back       []string
}

// MilitaryRefusal renders a refused military command.
func MilitaryRefusal(c Context, v MilitaryRefusalView) *presenter.Response {
	args := map[string]any{"country": c.PlaceName(v.Country), "office": c.OfficeName(v.Office),
		"need": FormatMoney(c, v.Need), "have": FormatMoney(c, v.Have), "max": FormatNumber(c, v.Max)}
	kb := keyboards.New()
	back := AddrGovCity
	if v.Country.Code != "" {
		back = keyboards.Data(AddrMinistry, v.Country.Code)
	}
	if len(v.Back) > 0 {
		back = keyboards.Data(v.Back...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: back}))
	return c.respond(c.T("military.refused."+v.Kind, args), kb.Build())
}

// MilitaryNoticeView is a private notice of the armed forces.
type MilitaryNoticeView struct {
	Country        GovPlace
	Good           Good
	Qty            int64
	CityCode, City string
	Branch         Named
}

// MoveArrivedNotice tells the commander equipment reached its garrison.
func MoveArrivedNotice(c Context, v MilitaryNoticeView) *presenter.Response {
	kb := keyboards.New()
	kb.Add(c.T("military.button.branch", map[string]any{"branch": c.BranchName(v.Branch)}), AddrBranch, v.Country.Code, v.Branch.Code)
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrForces, v.Country.Code)}))
	return c.respond(c.T("military.notice.arrived", map[string]any{"good": c.GoodName(v.Good), "count": FormatNumber(c, v.Qty),
		"city": c.CityName(v.CityCode, v.City)}), kb.Build())
}

// ProcurementAnnouncement is a line in the groups of a country's cities: its
// state acquired arms, told in a band, never a count or a price.
func ProcurementAnnouncement(c Context, country GovPlace, class Named, band string) string {
	return c.T("military.announce.procured", map[string]any{"country": c.PlaceName(country),
		"class": c.ForceClassName(class), "band": c.BandName(band)})
}
