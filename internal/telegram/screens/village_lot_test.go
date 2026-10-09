package screens

import (
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
)

// «مدیریت قطعهٔ من» in every stage, in every language: no key is left unresolved, no English stays in the Persian,
// and the acts are buttons.
func TestLotManageRendersInEveryStageAndLanguage(t *testing.T) {
	cat := catalogue(t)
	nm := func(code string) presentation.Named { return presentation.Named{Code: code} }
	items := []village.WorkItemLine{{Item: nm("timber"), Qty: 3}}
	detail := village.LotManageView{
		Village: "ده", Stage: village.LotDetail, ID: "b1", Building: nm("private_cottage"), Built: true, CanManage: true, Mine: true,
		Function: village.LotFunctionLine{Code: "dwelling", Name: "خانه", Level: 1, MaxLevel: 2}, Storeys: 1, MaxStoreys: 2, StabilityBPS: 10000,
		AreaUsed: 4, AreaCapacity: 6, Cash: 900,
		Modules:         []village.LotModuleLine{{Module: nm("bedroom"), Count: 1, Included: 1, Max: 3}, {Module: nm("storeroom"), Count: 2, Included: 1, Max: 2, Removable: true}},
		Additions:       []village.LotAdditionLine{{Module: nm("bedroom"), Left: 2, Materials: items, Shifts: 3, Can: true}},
		Upgrade:         &village.LotUpgradeLine{To: 2, Can: true},
		StoreyUp:        &village.LotStoreyLine{To: 2, Can: true},
		Functions:       []village.LotFunctionChoice{{Function: nm("stall"), Available: true}, {Function: nm("dwelling"), Current: true, Available: true}},
		Work:            &village.LotWorkLine{ID: "w", Adds: []village.LotWorkAdd{{Module: nm("bedroom"), Count: 1}}, ProgressBPS: 3300},
		Templates:       []village.LotTemplateLine{{ID: "t1", Name: "قالب ۱", Function: nm("dwelling"), Mine: true, Applicable: true}},
		HousingCapacity: 4, PersonalStorage: 80, StallSlots: 1,
		Look: &village.LotLook{Material: "timber", Roof: "gable", Windows: 3, Storeys: 1},
	}
	ask := village.LotManageView{Village: "ده", Stage: village.LotAsk, ID: "b1", Building: nm("private_cottage"), Action: village.LotActionAdd, Code: "bedroom", N: 1,
		Quote: &village.LotQuote{Materials: []village.LotMaterialLine{{Item: nm("timber"), Need: 3, Have: 10}}, Money: 0, FeeSUP: 20, Shifts: 3, Wages: 120,
			Adds: []village.LotWorkAdd{{Module: nm("bedroom"), Count: 1}}, Salvage: items, Skipped: []presentation.Named{nm("workbench")}}}
	blocked := ask
	blocked.Reason = village.LotReasonMaterials
	done := village.LotManageView{Village: "ده", Stage: village.LotDone, ID: "b1", Building: nm("private_cottage"), Action: village.LotActionTemplateSave, ShareCode: "ABCD2345"}
	menu := village.LotManageView{Village: "ده", Stage: village.LotMenu, Buildings: []village.LotBuildingLine{{ID: "b1", Building: nm("private_cottage"), X: 1, Y: 2, Function: "dwelling", Built: true}, {ID: "b2", Building: nm("market_stall")}}}
	for _, lang := range cat.Languages() {
		c := Context{Msgs: cat, Lang: lang, MessageID: 42, Zone: snapshotZone}
		for name, v := range map[string]village.LotManageView{"detail": detail, "ask": ask, "blocked": blocked, "done": done, "menu": menu} {
			resp := LotManage(c, v)
			if resp == nil || strings.TrimSpace(resp.Text) == "" {
				t.Errorf("%s/%s: empty", lang, name)
				continue
			}
			for _, bad := range []string{"village.lot.", "citizen.button.", "module_kind.", "building_function."} {
				if strings.Contains(resp.Text, bad) {
					t.Errorf("%s/%s: an unresolved key %q in\n%s", lang, name, bad, resp.Text)
				}
			}
		}
		idle := detail
		idle.Work = nil // while an order is being built no new act is offered
		if resp := LotManage(c, idle); len(resp.Keyboard.Rows) < 6 {
			t.Errorf("%s: the idle detail offers %d rows of buttons, want the acts", lang, len(resp.Keyboard.Rows))
		}
		if resp := LotManage(c, detail); len(resp.Keyboard.Rows) > 3 {
			t.Errorf("%s: acts are offered while an order is being built (%d rows)", lang, len(resp.Keyboard.Rows))
		}
		if lang == "fa" {
			if resp := LotManage(c, detail); strings.Contains(resp.Text, "Manage") || strings.Contains(resp.Text, "Storeys") {
				t.Errorf("English in the Persian detail:\n%s", resp.Text)
			}
		}
	}
}
