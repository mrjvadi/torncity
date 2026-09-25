package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens/screentest"
)

// Staged production (docs/adr/0021, section 14) and defence licences
// (docs/adr/0022, section 2.14) join the snapshot harness as their own area,
// testdata/snapshots/<language>/staging.txt; their group lines join
// group.txt.
func init() { snapshotAreas["staging"] = stagingSnapshots }

// stagingNames are the sample companies of the area.
var stagingNames = map[string]struct{ studio, arms, torch, phone string }{
	"fa": {studio: "فناوران آریا", arms: "صنایع دفاعی البرز", torch: "نورافشان", phone: "آریا فون"},
	"en": {studio: "Aria Tech", arms: "Alborz Defence", torch: "Beacon", phone: "Aria Phone"},
}

func stagingSnapshots(c Context, _ people, add func(string, *presenter.Response)) {
	n := stagingNames[c.Lang]
	studioType := Named{Code: "tech_studio", Name: "Tech studio"}
	studio := CompanyRef{Code: "T3C5T8D", Name: n.studio, Type: studioType}
	arms := CompanyRef{Code: "D3F2N5S", Name: n.arms, Type: Named{Code: "aerospace", Name: "Aerospace manufacturer"}}
	torch := Named{Code: "led_torch", Name: "LED torch"}
	radio := Named{Code: "pocket_radio", Name: "Pocket radio"}
	phone := Named{Code: "phone", Name: "Phone"}
	board := Named{Code: "circuit_board", Name: "Circuit board"}
	chipset := Named{Code: "chipset", Name: "Chipset"}
	wire := Named{Code: "copper_wire", Name: "Copper wire"}
	resin := Named{Code: "resin", Name: "Epoxy resin"}
	semis := Named{Code: "semiconductors", Name: "Semiconductors"}
	micro := Named{Code: "microchips", Name: "Microchips"}
	batteries := Named{Code: "batteries", Name: "Battery chemistry"}
	boardGood := Good{Component: true, Item: board}
	torchGood := Good{Item: torch, Design: n.torch, DesignNo: 21}

	// The studio, step by step.
	add("Studio · a new tech studio: basic goods only", Studio(c, StudioView{Ref: studio, CanDesign: true, Max: 20, Need: 1,
		CanResearch: true, Kinds: []Named{torch, radio}, Hidden: true}))
	add("Studio · one research away from a phone", Studio(c, StudioView{Ref: studio, CanDesign: true, Max: 20, Need: 1,
		CanResearch: true, Kinds: []Named{torch, radio},
		Next:    []StudioKind{{Item: phone, Steps: []TechStep{{Tech: micro, Research: true}, {Tech: batteries, Research: true}}}},
		Designs: []DesignLine{{No: 21, Name: n.torch, Item: torch, Status: DesignFinal, Origin: DesignAuthored}}}))
	add("Studio · a phone behind a license", Studio(c, StudioView{Ref: studio, CanDesign: true, Max: 20, Need: 1,
		Next: []StudioKind{{Item: phone, Steps: []TechStep{{Tech: micro}, {Tech: batteries, Research: true}}}}}))
	add("Studio · the phone open", Studio(c, StudioView{Ref: studio, CanDesign: true, Max: 20, Need: 1, CanResearch: true,
		Kinds: []Named{torch, radio, phone}}))

	// The lab, staged.
	add("Lab · a new tech studio", Lab(c, LabView{Ref: studio, Available: 90000, Hidden: 2, Techs: []TechLine{
		{Tech: semis, State: TechAvailable, Cost: 30000},
		{Tech: batteries, State: TechAvailable, Cost: 25000},
		{Tech: micro, State: TechLocked, Cost: 60000, Missing: []Named{semis}},
	}}))

	// The floor, with what is one step away.
	add("Floor · a component one step away", Orders(c, OrdersView{Ref: studio, Max: 3, Crew: 1,
		Targets: []ProduceTarget{{Good: boardGood, Batch: 2}},
		Locked:  []LockedTarget{{Good: Good{Component: true, Item: chipset}, Steps: []TechStep{{Tech: micro, Research: true}}}}}))
	recipe := []RecipeLine{{Component: wire, Per: 2, Need: 10, Have: 0}, {Component: resin, Per: 1, Need: 5, Have: 1}}
	shortView := ProduceView{Ref: studio, Target: ProduceTarget{Good: boardGood, Batch: 2}, Qty: 5, Recipe: recipe, Crew: 1,
		Short: []Shortage{{Component: wire, Need: 10, Have: 0, Source: ShortFromSupplier},
			{Component: resin, Need: 5, Have: 1, Source: ShortFromSupplier}}, StockUp: 42}
	add("Produce · short, the supplier sells it all", Produce(c, shortView))
	mixed := ProduceView{Ref: studio, Target: ProduceTarget{Good: torchGood}, Qty: 5,
		Recipe: []RecipeLine{{Component: board, Per: 1, Need: 5, Have: 2}}, Crew: 1,
		Short: []Shortage{{Component: board, Need: 5, Have: 2, Source: ShortMadeHere}}}
	add("Produce · short, a part made here", Produce(c, mixed))
	bought := ProduceView{Ref: studio, Target: ProduceTarget{Good: boardGood, Batch: 2}, Qty: 5, Output: 10, Crew: 1,
		Recipe:   []RecipeLine{{Component: wire, Per: 2, Need: 10, Have: 10}, {Component: resin, Per: 1, Need: 5, Have: 6}},
		Duration: 4 * time.Minute, Bought: 42}
	add("Produce · the inputs bought in one tap", Produce(c, bought))

	// The next step, on the warehouse.
	steps := []NextStep{
		{Kind: StepDesignFirst},
		{Kind: StepDesignDraft, DesignNo: 22},
		{Kind: StepSupply, Good: boardGood, Qty: 5, Total: 45},
		{Kind: StepProduce, Good: boardGood, Qty: 5, Batch: 2},
		{Kind: StepProducing, Good: torchGood, Qty: 5, FinishAt: snapshotNow.Add(3 * time.Minute), Left: 3 * time.Minute},
		{Kind: StepSell, Good: torchGood},
		{Kind: StepBuyGoods, Good: Good{Item: phone, Design: n.phone, DesignNo: 23}, Component: Named{Code: "plastic_case", Name: "Plastic case"}},
		{Kind: StepResearch, Item: phone, Tech: micro, CanResearch: true},
		{Kind: StepDesignNext, Item: phone},
	}
	for i := range steps {
		s := steps[i]
		add("Warehouse · next step: "+s.Kind, Warehouse(c, WarehouseView{Ref: studio, CanResearch: true, Next: &s}))
	}

	// The management screen: the next step and the defence licence.
	manage := CompanyManageView{Ref: studio, CityCode: "ostmarch", City: "Ostmarch", Owner: true, Balance: 90000,
		Available: 90000, Upkeep: 1800, PriceBPS: 10000, PriceMin: 6000, PriceMax: 20000, PriceStep: 1000, MaxStaff: 6,
		TaxBPS: 1000, Step: &NextStep{Kind: StepResearch, Item: phone, Tech: micro, CanResearch: true},
		Defence: &DefenceBadge{Contractor: true, Eligible: true}}
	add("Manage · a tech studio that may apply", CompanyManage(c, manage))
	armsManage := manage
	armsManage.Ref, armsManage.Step = arms, nil
	armsManage.Defence = &DefenceBadge{Status: "revoking", EffectiveAt: snapshotNow.Add(24 * time.Hour)}
	add("Manage · a defence company, its licence revoked", CompanyManage(c, armsManage))

	// Founding a defence company.
	add("Register · a defence company, no licence", CompanyTypeDetail(c, CompanyTypeView{Type: arms.Type, CityCode: "ostmarch",
		City: "Ostmarch", Place: Named{Code: "industrial_zone", Name: "Industrial zone"}, Fee: 250000, Upkeep: 4000, MaxStaff: 15,
		Period: 24 * time.Minute, NameMin: 3, NameMax: 24, Blocked: CompanyBlockedDefence,
		Careers: []JobRef{{CareerCode: "workshop", CareerName: "Workshop", Rank: "entry", Title: "Workshop Hand"},
			{CareerCode: "technology", CareerName: "Technology", Rank: "entry", Title: "Junior Developer"}},
		Rank: JobRef{CareerCode: "armed_forces", CareerName: "Armed Forces", Rank: "specialist", Title: "Captain"}}))
	add("Register · the kinds, one licensed", CompanyTypes(c, CompanyTypesView{CityCode: "ostmarch", City: "Ostmarch", Max: 2,
		Types: []CompanyTypeLine{{Type: studioType, Fee: 80000, Upkeep: 1800}, {Type: arms.Type, Fee: 250000, Upkeep: 4000, Licensed: true}}}))
	add("Company refused · defence", CompanyRefusal(c, CompanyRefusalView{Kind: CompanyRefusedDefence}))

	// A company's defence licence.
	add("Defence · a tech studio that may apply", CompanyDefence(c, CompanyDefenceView{Ref: studio, Owned: 3, Tier: 2,
		MinTechs: 3, MinTier: 2, CanApply: true}))
	add("Defence · not there yet", CompanyDefence(c, CompanyDefenceView{Ref: studio, Owned: 1, Tier: 1, MinTechs: 3, MinTier: 2}))
	pending := LicenceEntry{No: 4, Company: studio, Kind: "contractor", Basis: "minister", Status: "pending"}
	add("Defence · applied, no minister", CompanyDefence(c, CompanyDefenceView{Ref: studio, Owned: 3, Tier: 2, MinTechs: 3,
		MinTier: 2, Licence: &pending, Applied: true, NoMinister: true}))
	active := LicenceEntry{No: 1, Company: arms, Kind: "manufacturer", Basis: "rank", Status: "active"}
	add("Defence · a defence company's licence", CompanyDefence(c, CompanyDefenceView{Ref: arms, Manufacturer: true, Licence: &active}))

	// The registry.
	country := GovPlace{Kind: "country", Code: "default_country", Name: "Commonwealth"}
	contractor := LicenceEntry{No: 5, Company: studio, Kind: "contractor", Basis: "minister", Status: "active"}
	revoking := LicenceEntry{No: 2, Company: CompanyRef{Code: "L4N7D2S", Name: n.arms, Type: Named{Code: "land_systems",
		Name: "Land systems manufacturer"}}, Kind: "manufacturer", Basis: "grandfathered", Status: "revoking",
		EffectiveAt: snapshotNow.Add(20 * time.Hour)}
	registry := LicencesView{Country: country, CanDecide: true, Pending: []LicenceEntry{pending},
		InForce: []LicenceEntry{active, contractor, revoking}, RevokeNotice: 24 * time.Hour}
	add("Registry · the minister's", Licences(sent(c), registry))
	add("Registry · in a group", Licences(group(c), registry))
	approved := registry
	approved.Pending, approved.Notice, approved.NoticeCompany = nil, LicenceApprove, n.studio
	add("Registry · just approved", Licences(c, approved))
	confirm := registry
	confirm.Confirm = &contractor
	add("Registry · revoke, confirm", Licences(c, confirm))
	add("Registry · none", Licences(c, LicencesView{Country: country}))
	add("Military refused · licence_state", MilitaryRefusal(c, MilitaryRefusalView{Kind: MilitaryRefusedLicenceState, Country: country}))

	// Notices.
	for _, kind := range []string{"applied", "approved", "rejected", "revoked"} {
		add("Notice · licence "+kind, LicenceNotice(sent(c), LicenceNoticeView{Kind: kind, Company: studio, Country: country,
			EffectiveAt: snapshotNow.Add(24 * time.Hour)}))
	}

	// The armed forces as an employer.
	add("Job refused · the defence fund cannot pay", Refusal(c, RefusalView{Kind: RefusalArmyCannotPay}))
	add("Job openings · the armed forces", JobOpenings(c, JobOpeningsView{CityCode: "ostmarch", City: "Ostmarch", Page: 1, Pages: 1,
		Openings: []JobOpening{
			{Job: JobRef{CareerCode: "armed_forces", CareerName: "Armed Forces", Rank: "entry", Title: "Private"}, Pay: 140, Eligible: true},
			{Job: JobRef{CareerCode: "retail", CareerName: "Retail", Rank: "entry", Title: "Sales Trainee"}, Pay: 120, Eligible: true},
		}}))
}

// stagingAnnouncements are the public lines of the defence licences.
func stagingAnnouncements(c Context, book *screentest.Book) {
	n := stagingNames[c.Lang]
	country := GovPlace{Kind: "country", Code: "default_country", Name: "Commonwealth"}
	book.AddText("announcement · a contractor licence granted", LicenceAnnouncement(c, "granted", n.studio, country, time.Time{}))
	book.AddText("announcement · a defence licence revoked", LicenceAnnouncement(c, "revoked", n.arms, country,
		snapshotNow.Add(24*time.Hour)))
}
