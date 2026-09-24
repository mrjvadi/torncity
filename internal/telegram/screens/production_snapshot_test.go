package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens/screentest"
)

// The production economy joins the snapshot harness as its own area,
// testdata/snapshots/<language>/production.txt, and its group lines join
// group.txt.
func init() { snapshotAreas["production"] = productionSnapshots }

// productionNames are sample names, the way players of each language name
// their companies and their designs.
var productionNames = map[string]struct{ maker, rival, phone, copy string }{
	"fa": {maker: "صنایع نیلوفر", rival: "کارخانهٔ کاوه", phone: "نیل موبایل ایکس", copy: "نیل موبایل ایکس"},
	"en": {maker: "Nilou Industries", rival: "Kaveh Works", phone: "Nil Mobile X", copy: "Nil Mobile X"},
}

func productionSnapshots(c Context, _ people, add func(string, *presenter.Response)) {
	n := productionNames[c.Lang]
	factory := Named{Code: "factory", Name: "Factory"}
	maker := CompanyRef{Code: "Q7M2K9B", Name: n.maker, Type: factory}
	rival := CompanyRef{Code: "H4T8W2C", Name: n.rival, Type: factory}
	phone := Named{Code: "phone", Name: "Phone"}
	chipset := Named{Code: "chipset", Name: "Chipset"}
	cell := Named{Code: "cell", Name: "Battery cell"}
	casing := Named{Code: "plastic_case", Name: "Plastic case"}
	silica := Named{Code: "silica", Name: "Silica sand"}
	diesel := Named{Code: "diesel", Name: "Diesel fuel"}
	lithium := Named{Code: "lithium_salt", Name: "Lithium salt"}
	semis := Named{Code: "semiconductors", Name: "Semiconductors"}
	micro := Named{Code: "microchips", Name: "Microchips"}
	batteries := Named{Code: "batteries", Name: "Battery chemistry"}
	phoneGood := Good{Item: phone, Design: n.phone, DesignNo: 12}

	// The warehouse and the suppliers.
	add("Warehouse · stocked, the owner", Warehouse(c, WarehouseView{Ref: maker, CanResearch: true, Running: 1, Listings: 1,
		Researching: true, Lines: []WarehouseLine{
			{Good: Good{Component: true, Item: chipset}, Qty: 12, Sellable: true},
			{Good: Good{Component: true, Item: diesel}, Qty: 40, Sellable: true},
			{Good: phoneGood, Qty: 4, Quality: 63, Listed: 2, Sellable: true},
		}}))
	add("Warehouse · empty, the manager", Warehouse(c, WarehouseView{Ref: rival}))
	offers := []SupplyOffer{
		{Supplier: Named{Code: "industrial_supplier", Name: "Industrial supplier"}, Component: diesel, Price: 5, Stock: 1850},
		{Supplier: Named{Code: "industrial_supplier", Name: "Industrial supplier"}, Component: lithium, Price: 15, Stock: 60},
	}
	add("Suppliers · the city's", Suppliers(c, SuppliersView{Ref: maker, CityCode: "ostmarch", City: "Ostmarch",
		Offers: offers, Available: 84000}))
	add("Suppliers · just bought", Suppliers(c, SuppliersView{Ref: maker, CityCode: "ostmarch", City: "Ostmarch",
		Offers: offers, Available: 83500, Bought: &SupplyNotice{Component: diesel, Qty: 100, Total: 500}}))
	add("Suppliers · none", Suppliers(c, SuppliersView{Ref: maker, CityCode: "ostmarch", City: "Ostmarch", Available: 100}))

	// The research lab.
	finish := snapshotNow.Add(6 * time.Minute)
	add("Lab · the tree", Lab(c, LabView{Ref: maker, Available: 84000,
		Running: &ResearchLine{Tech: batteries, FinishAt: finish, Left: 6 * time.Minute},
		Techs: []TechLine{
			{Tech: semis, State: TechOwned, Mode: TechLicensed, Price: 9000, Cost: 30000},
			{Tech: micro, State: TechLocked, Cost: 60000, Missing: []Named{semis}},
			{Tech: batteries, State: TechRunning, Cost: 25000},
		}}))
	add("Lab · a licensee's tree", Lab(c, LabView{Ref: rival, Available: 12000, Techs: []TechLine{
		{Tech: semis, State: TechPublic, Cost: 30000},
		{Tech: micro, State: TechLicense, Cost: 60000},
		{Tech: batteries, State: TechAvailable, Cost: 25000, Offers: 2},
	}}))
	tech := TechView{Ref: maker, Tech: micro, State: TechAvailable, Cost: 60000, Time: 12 * time.Minute, Skill: "engineering",
		Level: 2, Best: 3, Available: 84000, Requires: []TechRequirement{{Tech: semis, Met: true}}, Unlocks: []Named{chipset}}
	add("Technology · ready to research", Tech(c, tech))
	started := tech
	started.State, started.Running = TechRunning, &ResearchLine{Tech: micro, FinishAt: snapshotNow.Add(12 * time.Minute), Left: 12 * time.Minute}
	started.Notice = &TechNotice{Kind: TechNoticeStarted}
	add("Technology · research started", Tech(c, started))
	locked := tech
	locked.State, locked.Requires, locked.Blocked = TechLocked, []TechRequirement{{Tech: semis}}, ProductionRefusedPrerequisite
	locked.Offers = []TechOffer{{Company: rival, Price: 12000}}
	add("Technology · locked, a license on offer", Tech(c, locked))
	poor := tech
	poor.Blocked, poor.Available = ProductionRefusedFunds, 20000
	add("Technology · the company cannot pay", Tech(c, poor))
	novice := tech
	novice.Blocked, novice.Best = ProductionRefusedSkill, 1
	add("Technology · nobody skilled enough", Tech(c, novice))
	owned := TechView{Ref: maker, Tech: micro, State: TechOwned, Cost: 60000, Time: 12 * time.Minute, Mode: TechLicensed,
		Price: 12000, Sold: 3, Unlocks: []Named{chipset}}
	add("Technology · owned, licensed", Tech(c, owned))
	private := owned
	private.Mode, private.Sold, private.Notice = TechPrivate, 0, &TechNotice{Kind: TechNoticePrivate}
	add("Technology · owned, private", Tech(c, private))
	confirmPublish := owned
	confirmPublish.ConfirmPublish = true
	add("Technology · publish, confirm", Tech(c, confirmPublish))
	public := owned
	public.Mode, public.Notice = TechPublished, &TechNotice{Kind: TechNoticePublished}
	add("Technology · published", Tech(c, public))
	buying := TechView{Ref: rival, Tech: micro, State: TechLocked, Cost: 60000, Time: 12 * time.Minute, Available: 30000,
		ConfirmLicense: &TechOffer{Company: maker, Price: 12000}}
	add("Technology · buy a license, confirm", Tech(c, buying))
	bought := TechView{Ref: rival, Tech: micro, State: TechLicense, Cost: 60000, Time: 12 * time.Minute,
		Notice: &TechNotice{Kind: TechNoticeBought, Price: 12000, Company: maker}}
	add("Technology · license bought", Tech(c, bought))

	// The design studio.
	add("Studio · designs and kinds", Studio(c, StudioView{Ref: maker, CanDesign: true, Max: 20, Need: 1,
		Designs: []DesignLine{
			{No: 12, Name: n.phone, Item: phone, Status: DesignFinal, Origin: DesignAuthored},
			{No: 14, Item: phone, Status: DesignDraft, Origin: DesignAuthored},
		},
		Kinds: []Named{phone, {Code: "car_stereo", Name: "Car stereo"}, {Code: "watch", Name: "Wristwatch"}}}))
	add("Studio · at the limit", Studio(c, StudioView{Ref: maker, Max: 2, Kinds: []Named{phone},
		Designs: []DesignLine{{No: 12, Name: n.phone, Item: phone, Status: DesignFinal}, {No: 15, Name: n.copy, Item: phone,
			Status: DesignFinal, Origin: DesignReverseEngineered}}}))
	slots := []SlotLine{
		{Slot: "board", Min: 1, Max: 1, Component: chipset, Qty: 1},
		{Slot: "power", Min: 1, Max: 1},
		{Slot: "shell", Min: 1, Max: 1, Component: casing, Qty: 1},
	}
	draft := DesignView{Ref: maker, No: 14, Item: phone, Status: DesignDraft, Origin: DesignAuthored, Slots: slots,
		Attributes: []AttributeLine{{Name: "battery_life", Observable: true}, {Name: "quality", Value: 53, Observable: true}},
		CostFloor:  440}
	add("Design · a draft", Design(c, draft))
	choosing := draft
	choosing.Choosing = "power"
	choosing.Candidates = []Candidate{{Component: cell, Price: 150, Quality: 50, Locked: true}}
	add("Design · choosing a slot, locked", Design(c, choosing))
	grams := DesignView{Ref: maker, No: 16, Item: Named{Code: "bread", Name: "Bread"}, Status: DesignDraft, Origin: DesignAuthored,
		Slots: []SlotLine{
			{Slot: "base", Min: 50, Max: 500, Unit: "g", Component: Named{Code: "flour", Name: "Flour"}, Qty: 120},
			{Slot: "leaven", Optional: true, Min: 1, Max: 20, Unit: "g"},
		}, Choosing: "base", Candidates: []Candidate{{Component: Named{Code: "flour", Name: "Flour"}, Price: 1, Quality: 50}},
		Attributes: []AttributeLine{{Name: "quality", Value: 50, Observable: true}}, CostFloor: 120, Complete: true}
	add("Design · a slot that takes an amount", Design(c, grams))
	complete := draft
	complete.Name = n.phone
	complete.Slots = []SlotLine{
		{Slot: "board", Min: 1, Max: 1, Component: chipset, Qty: 1},
		{Slot: "power", Min: 1, Max: 1, Component: cell, Qty: 1},
		{Slot: "shell", Min: 1, Max: 1, Component: casing, Qty: 1},
	}
	complete.Attributes = []AttributeLine{{Name: "battery_life", Value: 3000, Observable: true}, {Name: "quality", Value: 50, Observable: true}}
	complete.CostFloor, complete.Complete = 590, true
	add("Design · complete, named", Design(c, complete))
	lockedDraft := complete
	lockedDraft.Locked = []Named{batteries}
	add("Design · complete, a technology missing", Design(c, lockedDraft))
	final := complete
	final.Status, final.No = DesignFinal, 12
	add("Design · final", Design(c, final))
	copied := final
	copied.Ref, copied.No, copied.Origin, copied.Source = rival, 15, DesignReverseEngineered, n.phone
	copied.QualityLossBPS, copied.OverheadBPS = 1350, 1350
	add("Design · a reverse engineered copy", Design(c, copied))

	// The production floor.
	add("Floor · targets and orders", Orders(c, OrdersView{Ref: maker, Max: 3, Running: 1, Crew: 4,
		Targets: []ProduceTarget{{Good: phoneGood}, {Good: Good{Component: true, Item: chipset}, Batch: 1}},
		Orders: []ProductionLine{
			{No: 31, Good: phoneGood, Output: 5, FinishAt: snapshotNow.Add(4 * time.Minute), Left: 4 * time.Minute},
			{No: 30, Good: Good{Component: true, Item: chipset}, Output: 10, Done: true, Quality: 58},
		}}))
	add("Floor · nothing to make", Orders(c, OrdersView{Ref: rival, Max: 3, Crew: 1}))
	recipe := []RecipeLine{{Component: chipset, Per: 1, Have: 12}, {Component: cell, Per: 1, Have: 3}, {Component: casing, Per: 1, Have: 8}}
	add("Produce · choose how many", Produce(c, ProduceView{Ref: maker, Target: ProduceTarget{Good: phoneGood}, Recipe: recipe,
		Crew: 4, MaxQty: 3}))
	planned := []RecipeLine{{Component: chipset, Per: 1, Need: 3, Have: 12}, {Component: cell, Per: 1, Need: 3, Have: 3},
		{Component: casing, Per: 1, Need: 3, Have: 8}}
	add("Produce · the plan", Produce(c, ProduceView{Ref: maker, Target: ProduceTarget{Good: phoneGood}, Qty: 3, Output: 3,
		Recipe: planned, Crew: 4, MaxQty: 3, Duration: 64 * time.Second}))
	short := []RecipeLine{{Component: chipset, Per: 1, Need: 5, Have: 12}, {Component: cell, Per: 1, Need: 5, Have: 3},
		{Component: casing, Per: 1, Need: 5, Have: 8}}
	add("Produce · short of inputs", Produce(c, ProduceView{Ref: maker, Target: ProduceTarget{Good: phoneGood}, Qty: 5,
		Recipe: short, Crew: 4, MaxQty: 3, Short: []Shortage{{Component: cell, Need: 5, Have: 3}}}))
	add("Produce · a component, in batches", Produce(c, ProduceView{Ref: maker,
		Target: ProduceTarget{Good: Good{Component: true, Item: silica}, Batch: 10},
		Recipe: []RecipeLine{{Component: diesel, Per: 1, Have: 40}}, Crew: 2, MaxQty: 40}))
	add("Produce · placed", Produce(c, ProduceView{Ref: maker, Target: ProduceTarget{Good: phoneGood},
		Placed: &ProductionLine{No: 32, Good: phoneGood, Output: 3, FinishAt: snapshotNow.Add(time.Minute), Left: time.Minute}}))

	// Reverse engineering.
	sample := SampleLine{Serial: "A1B2C3D4E5", Good: phoneGood, Maker: n.maker, Quality: 61, ChanceBPS: 1500}
	add("Reverse lab · a sample and a finished job", ReverseLab(c, ReverseLabView{Ref: rival, Skill: "engineering", Level: 15,
		Time: 6 * time.Minute, Samples: []SampleLine{sample},
		Jobs: []ReverseLine{
			{No: 3, Good: phoneGood, Status: "succeeded", Result: n.copy, ResultNo: 15},
			{No: 2, Good: phoneGood, Status: "failed"},
			{No: 4, Good: phoneGood, Status: "running", FinishAt: snapshotNow.Add(5 * time.Minute), Left: 5 * time.Minute},
		}}))
	add("Reverse lab · take it apart, confirm", ReverseLab(c, ReverseLabView{Ref: rival, Confirm: &sample, Time: 6 * time.Minute}))
	add("Reverse lab · no samples", ReverseLab(c, ReverseLabView{Ref: rival, Skill: "engineering", Time: 6 * time.Minute}))

	// Selling.
	add("Sell · how many", Sell(c, SellView{Ref: maker, Good: phoneGood, Have: 4, Reference: 590}))
	add("Sell · the price", Sell(c, SellView{Ref: maker, Good: phoneGood, Have: 4, Qty: 2, Reference: 590}))
	listings := []ListingLine{{No: 7, Good: phoneGood, Left: 2, Price: 1500}, {No: 8, Good: Good{Component: true, Item: chipset}, Left: 10, Price: 420}}
	add("Listings · just listed", Listings(c, ListingsView{Ref: maker, CityCode: "ostmarch", City: "Ostmarch", Listings: listings,
		Notice: ListingNotice(c, ListingNoticeListed, listings[0])}))
	add("Listings · none", Listings(c, ListingsView{Ref: maker, CityCode: "ostmarch", City: "Ostmarch"}))
	line := GoodsLine{No: 7, Company: maker, Good: phoneGood, Left: 2, Price: 1500,
		Attributes: []AttributeLine{{Name: "battery_life", Value: 3000, Observable: true}, {Name: "quality", Value: 50, Observable: true}}}
	add("Company goods · a city's", CompanyGoods(c, GoodsView{CityCode: "ostmarch", City: "Ostmarch", Lines: []GoodsLine{line,
		{No: 8, Company: maker, Good: Good{Component: true, Item: chipset}, Left: 10, Price: 420}}}))
	add("Company goods · none", CompanyGoods(c, GoodsView{CityCode: "ostmarch", City: "Ostmarch"}))
	both := []string{MethodCash, MethodCard}
	add("Buy · a player or a company", CompanyBuy(c, BuyView{Line: line, Qty: 1,
		Payment:   &PaymentChoice{Amount: 1500, Accepted: both, Usable: both, Cash: 4000, Bank: 90000},
		Companies: []CompanyRef{rival}}))
	add("Buy · bought for a company", CompanyBuy(c, BuyView{Line: line, Qty: 1,
		Bought: &BoughtView{Qty: 1, Total: 1500, For: n.rival, ForCode: rival.Code}}))
	add("Buy · bought for the player", CompanyBuy(c, BuyView{Line: line, Qty: 1, Bought: &BoughtView{Qty: 1, Total: 1500}}))
	add("Bag · a phone a company made", Inventory(c, InventoryView{Page: 1, Pages: 1, Total: 1,
		Lines: []InventoryLine{{Item: phone, Category: "electronics", Qty: 1, Serial: "A1B2C3D4E5", Quality: 61, Design: n.phone}}}))

	// Notices.
	add("Notice · research done", ProductionNotice(sent(c), ProductionNoticeView{Kind: ProductionNoticeResearched, Company: maker, Tech: micro}))
	add("Notice · an order done", ProductionNotice(sent(c), ProductionNoticeView{Kind: ProductionNoticeProduced, Company: maker,
		Good: phoneGood, Qty: 5, Quality: 61}))
	add("Notice · a copy recovered", ProductionNotice(sent(c), ProductionNoticeView{Kind: ProductionNoticeReversedOK, Company: rival,
		Good: Good{Item: phone, Design: n.phone}, Design: n.copy, DesignNo: 15}))
	add("Notice · a sample wasted", ProductionNotice(sent(c), ProductionNoticeView{Kind: ProductionNoticeReversedBad, Company: rival,
		Good: Good{Item: phone, Design: n.phone}}))
	add("Notice · a license sold", ProductionNotice(sent(c), ProductionNoticeView{Kind: ProductionNoticeLicenseSold, Company: maker,
		Tech: micro, Buyer: n.rival, Price: 12000}))
	add("Notice · goods sold", ProductionNotice(sent(c), ProductionNoticeView{Kind: ProductionNoticeSold, Company: maker,
		Good: phoneGood, Qty: 1, Buyer: n.rival, Price: 1500}))

	// Refusals.
	refusals := []ProductionRefusalView{
		{Kind: ProductionRefusedShortage, Ref: maker, Shortages: []Shortage{{Component: cell, Need: 5, Have: 3}, {Component: chipset, Need: 5, Have: 0}}},
		{Kind: ProductionRefusedSkill, Ref: maker, Skill: "engineering", Level: 2, Have: 0},
		{Kind: ProductionRefusedTechLocked, Ref: rival, Techs: []Named{micro, batteries}},
		{Kind: ProductionRefusedFunds, Ref: maker, Need: 60000, HaveMoney: 12000},
		{Kind: ProductionRefusedBusy, Ref: maker, Techs: []Named{batteries}},
		{Kind: ProductionRefusedMaxOrders, Ref: maker, Max: 3},
		{Kind: ProductionRefusedSupplierEmpty, Ref: maker, Max: 60},
		{Kind: ProductionRefusedNameLength, Ref: maker, Level: 3, Max: 24},
		{Kind: ProductionRefusedPublished, Ref: maker},
		{Kind: ProductionRefusedNotCleared},
		{Kind: ProductionRefusedAway, CityCode: "ostmarch", City: "Ostmarch"},
		{Kind: ProductionRefusedOwnDesign, Ref: maker},
	}
	for _, r := range refusals {
		add("Refused · "+r.Kind, ProductionRefusal(c, r))
	}
	page := CompanyPageView{Ref: maker, CityCode: "ostmarch", City: "Ostmarch", Place: Named{Code: "industrial_zone", Name: "Industrial zone"},
		Owner: GovPlayer{Name: "Nilou", Code: thirdCode}, Staff: 3, MaxStaff: 12, Stars: 3, Rated: true,
		Products: []Good{phoneGood}, Published: []Named{semis}}
	if c.Lang == "fa" {
		page.Owner.Name = "نیلوفر"
	}
	add("Company page · products and published technologies", CompanyPage(group(c), page))
}

// productionAnnouncements are the public lines a city's groups read of its
// production economy; they join the group lines (group.txt).
func productionAnnouncements(c Context, book *screentest.Book) {
	n := productionNames[c.Lang]
	maker := CompanyRef{Code: "Q7M2K9B", Name: n.maker, Type: Named{Code: "factory", Name: "Factory"}}
	book.AddText("announcement · a technology published", TechPublishedAnnouncement(c, maker,
		Named{Code: "semiconductors", Name: "Semiconductors"}, "ostmarch", "Ostmarch"))
	book.AddText("announcement · a product launched", ProductLaunchedAnnouncement(c, maker, n.phone,
		Named{Code: "phone", Name: "Phone"}, "ostmarch", "Ostmarch"))
	for _, command := range []string{"company.supply", "company.techmode", "company.dqty", "company.dname", "company.sell", "company.buy"} {
		book.AddText("question · "+command, InputPrompt(c, command, ""))
		book.AddText("question · "+command+" · reply box", InputPlaceholder(c, command))
	}
}
