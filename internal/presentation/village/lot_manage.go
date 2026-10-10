package village

import (
	"github.com/mrjvadi/torncity/internal/presentation"
)

// «مدیریت قطعهٔ من» - manage my lot (docs/adr/0045 phase B1, section 3.3): the lot owner chooses the function of the
// lot and what is inside it; the game builds it and generates the look. One command, one screen, in the three
// stages every long form of the village has: the menu (the list of my buildings, or one building's tabs), the ask
// (the quote of an act) and the done (what was ordered). Nothing here places a wall by hand: an act is a module, a
// level, a storey, a conversion or a saved layout, and it becomes one order raised by the hiring board.

// Screen and address.
const (
	ScreenLotManage = "lot_manage"
	AddrLotManage   = "settlement:lot.manage"
)

// Stages of the screen.
const (
	LotMenu   = "menu"
	LotDetail = "detail"
	LotAsk    = "ask"
	LotDone   = "done"
)

// Actions of the screen (the command's action argument).
const (
	LotActionFunction       = "function"
	LotActionAdd            = "add"
	LotActionRemove         = "remove"
	LotActionLevel          = "level"
	LotActionStorey         = "storey"
	LotActionTemplateSave   = "template_save"
	LotActionTemplateApply  = "template_apply"
	LotActionTemplateDelete = "template_delete"
	LotActionKeeperHire     = "keeper_hire"
	LotActionKeeperEnd      = "keeper_end"
)

// Refusals of the screen (village_refusal kinds), as the core spells them.
const (
	LotNotYours    = "lot_not_yours"
	LotNotBuilt    = "lot_not_built"
	LotBusy        = "lot_busy"
	LotNoFunction  = "lot_no_function"
	LotNoModule    = "lot_no_module"
	LotNoArea      = "lot_no_area"
	LotSlotFull    = "lot_slot_full"
	LotNotBuilable = "lot_not_buildable"
	LotStoreys     = "lot_storeys"
	LotNothing     = "lot_nothing"
	LotTemplates   = "lot_templates_full"
	LotNoTemplate  = "lot_no_template"
	LotInvalid     = "lot_invalid"
	LotKeepOne     = "lot_keep_one"
	LotNoKeeper    = "lot_no_keeper"
	LotKeeperNone  = "lot_keeper_none"
	LotKeeperTerms = "lot_keeper_terms"
)

// Reasons an act cannot be confirmed (view.Reason).
const (
	LotReasonMaterials = "materials"
	LotReasonCash      = "cash"
	LotReasonRequires  = "requires"
	LotReasonArea      = "area"
	LotReasonSlot      = "slot"
	LotReasonStoreys   = "storeys"
	LotReasonBuilt     = "not_built"
	LotReasonBusy      = "busy"
)

// LotBuildingLine is one of my buildings in the menu.
type LotBuildingLine struct {
	ID       string
	Building presentation.Named
	// X and Y are the lot of the building; Function and FunctionName what it is ("" before it has a function row).
	X, Y         int
	Function     string
	FunctionName string
	Level        int
	Storeys      int
	// Built is false while the building is going up.
	Built bool
	// HasOrder is true while an order of the owner is being built.
	HasOrder bool
}

// LotFunctionLine is the function a lot is.
type LotFunctionLine struct {
	Code   string
	Name   string
	Family string
	Level  int
	// MaxLevel is the top rung of its ladder.
	MaxLevel int
	Status   string
	// Permit is the permit class the owner pays for.
	Permit string
}

// LotModuleLine is one kind of module in the building.
type LotModuleLine struct {
	Module presentation.Named
	Count  int
	// Included is how many the level brought with it (not paid for on their own); Max the most the function takes.
	Included int
	Max      int
	// Effect is the mechanic the module serves (housing_capacity, personal_storage, stall_slots, warmth_shelter, workbench).
	Effect string
	// AreaEach is the floor area one takes.
	AreaEach int
	// Provides are the numbers one gives.
	HousingCapacity int64
	PersonalStorage int64
	StallSlots      int64
	// Removable is true when a module beyond the included can be taken out.
	Removable bool
}

// LotAdditionLine is a module that can be added, with its price.
type LotAdditionLine struct {
	Module    presentation.Named
	Left      int
	Materials []WorkItemLine
	Shifts    int
	AreaEach  int
	// Can is true when the order could be placed now; Reason says why not (area, slot, requires, materials).
	Can    bool
	Reason string
	Needs  []VillageNeed
}

// LotUpgradeLine is the next level of the function.
type LotUpgradeLine struct {
	To        int
	Building  presentation.Named
	CostMoney int64
	Materials []WorkItemLine
	Shifts    int
	Adds      []presentation.Named
	Can       bool
	Reason    string
	Needs     []VillageNeed
}

// LotStoreyLine is the next storey.
type LotStoreyLine struct {
	To        int
	Materials []WorkItemLine
	Shifts    int
	Can       bool
	Reason    string
}

// LotFunctionChoice is a function the lot could become.
type LotFunctionChoice struct {
	Function presentation.Named
	Family   string
	Current  bool
	// Available is true when the settlement and the owner have what the function needs; Needs lists what is missing.
	Available bool
	Needs     []VillageNeed
	CostMoney int64
	Materials []WorkItemLine
	Shifts    int
	// FeeSUP is the use-change fee and PermitFee the permit when its class differs; zero for the current function.
	FeeSUP    int64
	PermitFee int64
	// Effects names what the function does once built, as codes the client words.
	Effects []string
}

// LotWorkLine is an order being built (or just finished).
type LotWorkLine struct {
	ID string
	// Summary lists what the order builds.
	Adds        []LotWorkAdd
	LevelTo     int
	StoreysTo   int
	ConvertTo   presentation.Named
	ShiftsTotal int
	WorkDone    int64
	WorkNeeded  int64
	ProgressBPS int64
	// JobOpen is true when the hiring board has the order's job; Crew is how many NPC labourers it keeps.
	JobOpen bool
	Paused  string
	Status  string
}

// LotWorkAdd is one module of an order.
type LotWorkAdd struct {
	Module presentation.Named
	Count  int
}

// LotKeeperLine is the stall keeper of a stall's owner (docs/adr/0062): hired, what he takes, or why none can be hired now.
type LotKeeperLine struct {
	Hired bool
	// Pay is how a hired keeper is paid: "share" (ShareBPS of each sale made while the owner is away) or "wage" (Wage a local
	// day, minor units). Unhired: ShareBPS and Wage are the defaults, the Min/Max the range the owner chooses within.
	Pay                                string
	ShareBPS, ShareMinBPS, ShareMaxBPS int64
	Wage, WageMin, WageMax             int64
	// SoldAway and CutTotal are what the owner's stalls sold while he was away since the hire (the notional of those sales)
	// and what the keeper took of it (his share plus the day wages), minor units; SoldAwayToday and CutToday are the same for
	// the settlement's local day so far. Zero when not hired.
	SoldAway, CutTotal, SoldAwayToday, CutToday int64
	// Left says why the last keeper went when it was not the owner's doing: "wage_unpaid".
	Left string
	// Can is true when a keeper could be hired now; Reason says why not (no_seat, no_market).
	Can    bool
	Reason string
	// SeatsFree is the people of the labour pool still free to be hired.
	SeatsFree int64
}

// LotStaffLine is a post at the function.
type LotStaffLine struct {
	Role  string
	Slots int
}

// LotLook is the look descriptor the client draws (docs/adr/0045 3.4): the server's, deterministic, never edited by hand.
type LotLook struct {
	Version  int
	Function string
	Level    int
	W, D     int
	Storeys  int
	Material string
	Roof     string
	// Modules are the counts that show.
	Modules   map[string]int
	Condition int
	Seed      int64
	Palette   string
	Wobble    int
	Windows   int
	Door      string
	Hue       int
	Prop      string
	Chimney   bool
	Awning    bool
}

// LotTemplateLine is a saved layout.
type LotTemplateLine struct {
	ID       string
	Name     string
	Code     string
	Function presentation.Named
	Level    int
	Storeys  int
	Modules  []LotWorkAdd
	// Mine is true for my own templates (only they can be deleted); Applicable whether it could be applied to this
	// building now, with the reason when not.
	Mine       bool
	Applicable bool
	Reason     string
}

// LotQuote is the price of an act.
type LotQuote struct {
	// Materials are what leaves the home store (Need), with what the owner holds (Have).
	Materials []LotMaterialLine
	// Money is the building cost to the sink, FeeSUP the use-change fee, PermitFee the permit; Wages is the labour
	// the shifts cost at today's wage (paid as they are worked).
	Money     int64
	FeeSUP    int64
	PermitFee int64
	Wages     int64
	Shifts    int
	// Cash is what the owner holds; Total what must be paid now (money, fee, permit).
	Cash  int64
	Total int64
	// Adds, LevelTo, StoreysTo and ConvertTo are what the order builds.
	Adds      []LotWorkAdd
	LevelTo   int
	StoreysTo int
	ConvertTo presentation.Named
	// Salvage lists what a removal gives back.
	Salvage []WorkItemLine
	// Skipped lists the modules of a template that no rule can build yet.
	Skipped []presentation.Named
}

// LotMaterialLine is one material of a quote.
type LotMaterialLine struct {
	Item presentation.Named
	Need int64
	Have int64
}

// LotManageView is the screen.
type LotManageView struct {
	Village string
	Stage   string
	Action  string
	// Buildings is the menu: the buildings I manage.
	Buildings []LotBuildingLine
	// The building in front of me.
	ID       string
	Building presentation.Named
	X, Y     int
	W, D     int
	// Mine is true when I own the building; Public when it belongs to the settlement and I hold public.build.
	Mine   bool
	Public bool
	// CanManage is true when I may place orders on it.
	CanManage bool
	Built     bool
	Function  LotFunctionLine
	// Storeys, MaxStoreys and StabilityBPS are the support rule; AreaUsed and AreaCapacity the floor area.
	Storeys, MaxStoreys, StabilityBPS int
	AreaUsed, AreaCapacity            int
	Modules                           []LotModuleLine
	Additions                         []LotAdditionLine
	Upgrade                           *LotUpgradeLine
	StoreyUp                          *LotStoreyLine
	Functions                         []LotFunctionChoice
	Work                              *LotWorkLine
	Staff                             []LotStaffLine
	// Keeper is set on a stall the viewer owns.
	Keeper *LotKeeperLine
	// What the lot gives: the sums of its modules beyond the level, shown with the legacy base the building has.
	HousingCapacity int64
	PersonalStorage int64
	StallSlots      int64
	// IfUnstaffed says what stands idle without the posts of the function.
	IfUnstaffed  string
	ConditionBPS int
	Look         *LotLook
	Templates    []LotTemplateLine
	Cash         int64
	// The ask and the done.
	Quote  *LotQuote
	Reason string
	// Needs are what an act lacks.
	Needs []VillageNeed
	// Code, N and Name are the act's arguments, echoed for the confirm.
	Code string
	N    int
	Name string
	// ShareCode is the code of the template just saved.
	ShareCode string
}

var screenLotManage = presentation.Define[LotManageView](ScreenLotManage, "village")

// LotManage is the screen.
func LotManage(c presentation.Ctx, v LotManageView) *presentation.Response {
	switch v.Stage {
	case LotAsk:
		a := []presentation.Action{back(AddrLotManage, v.ID)}
		if v.Reason == "" {
			a = append([]presentation.Action{confirm(AddrLotManage, v.ID, v.Action, v.Code, itoa(v.N), v.Name, ResidenceConfirm)}, a...)
		}
		return screenLotManage.Response(c.Lang, v, a...)
	case LotDone:
		return screenLotManage.Response(c.Lang, v, back(AddrLotManage, v.ID), refresh(AddrLotManage, v.ID))
	case LotMenu:
		a := []presentation.Action{back(AddrMine), refresh(AddrLotManage)}
		for _, b := range v.Buildings {
			a = append(a, act(AddrLotManage, b.ID).Named("lot.open"))
		}
		return screenLotManage.Response(c.Lang, v, a...)
	}
	a := []presentation.Action{back(AddrLotManage), refresh(AddrLotManage, v.ID)}
	if v.CanManage && v.Built && v.Keeper != nil {
		switch {
		case v.Keeper.Hired:
			a = append(a, act(AddrLotManage, v.ID, LotActionKeeperEnd).Named("lot.keeper_end"))
		case v.Keeper.Can:
			a = append(a, act(AddrLotManage, v.ID, LotActionKeeperHire).Named("lot.keeper_hire"))
		}
	}
	if v.CanManage && v.Built {
		for _, m := range v.Additions {
			if m.Can {
				a = append(a, act(AddrLotManage, v.ID, LotActionAdd, m.Module.Code, "1").Named("lot.add"))
			}
		}
		for _, m := range v.Modules {
			if m.Removable {
				a = append(a, act(AddrLotManage, v.ID, LotActionRemove, m.Module.Code, "1").Named("lot.remove"))
			}
		}
		if v.Upgrade != nil && v.Upgrade.Can {
			a = append(a, act(AddrLotManage, v.ID, LotActionLevel).Named("lot.level"))
		}
		if v.StoreyUp != nil && v.StoreyUp.Can {
			a = append(a, act(AddrLotManage, v.ID, LotActionStorey).Named("lot.storey"))
		}
		for _, f := range v.Functions {
			if f.Available && !f.Current {
				a = append(a, act(AddrLotManage, v.ID, LotActionFunction, f.Function.Code).Named("lot.function"))
			}
		}
		for _, t := range v.Templates {
			if t.Applicable {
				a = append(a, act(AddrLotManage, v.ID, LotActionTemplateApply, t.ID).Named("lot.template_apply"))
			}
		}
	}
	for _, t := range v.Templates {
		if t.Mine {
			a = append(a, act(AddrLotManage, v.ID, LotActionTemplateDelete, t.ID).Named("lot.template_delete"))
		}
	}
	return screenLotManage.Response(c.Lang, v, a...)
}

func itoa(n int) string {
	if n == 0 {
		return ""
	}
	return itoaPos(n)
}

func itoaPos(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
