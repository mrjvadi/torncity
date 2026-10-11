package settlementcfg

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/landuse"
	"github.com/mrjvadi/torncity/internal/domain/lotbuild"
)

// LotRules turns the lot settings (ADR 0045 phase B1: settlement.building_*, use_change_fee_bps) into the rules of the
// manage-my-lot command. The service and the boot-shaped tests both build them from here.
func LotRules(s config.Settlement) (handlers.LotRules, error) {
	var support []lotbuild.StoreyKnowledge
	for _, row := range s.BuildingStoreyKnowledge {
		code, n, ok := strings.Cut(row, "=")
		storeys, err := strconv.Atoi(strings.TrimSpace(n))
		if !ok || err != nil || storeys < 1 || strings.TrimSpace(code) == "" {
			return handlers.LotRules{}, fmt.Errorf("settlementcfg: building_storey_knowledge entry %q is not knowledge=storeys", row)
		}
		support = append(support, lotbuild.StoreyKnowledge{Knowledge: strings.TrimSpace(code), Storeys: storeys})
	}
	return handlers.LotRules{
		Storey: lotbuild.StoreyRules{AreaPerCell: s.BuildingAreaPerCell, TimberPerCell: int64(s.BuildingStoreyTimberPerCell),
			StonePerCell: int64(s.BuildingStoreyStonePerCell), StoneFrom: s.BuildingStoreyStoneFrom, ShiftsPerCell: s.BuildingStoreyShiftsPerCell},
		StoreyKnowledge: support, SalvageBPS: int64(s.BuildingSalvageBPS), UseChangeFeeBPS: int64(s.UseChangeFeeBPS),
		LookRerolls: s.BuildingLookRerolls, TemplatesMax: s.BuildingTemplatesMax, HearthFuel: "firewood",
	}, nil
}

// Land turns the land settings (settlement.land_*, docs/adr/0065) into the rules of the land model. The game service, the client
// API and the state sync all build them from here.
func Land(s config.Settlement) application.LandRules {
	from, _ := time.Parse(time.RFC3339, s.LandRuleAt) // not an instant: the land model is off
	return application.LandRules{RuleAt: from, GraceDays: s.LandGraceDays, Ring: int(s.LandWoodlandRing),
		RegrowEvery: time.Duration(s.LandRegrowHours) * time.Hour, SaplingFor: time.Duration(s.LandSaplingHours) * time.Hour,
		FellRadius: int(s.LandFellRadius), QuarryRadius: int(s.LandQuarryRadius), RockShifts: int(s.LandRockShifts)}
}

// Farm turns the farm settings (settlement.farm_*, docs/adr/0067) into the rules of the farm cycle.
func Farm(s config.Settlement) application.FarmRules {
	from, _ := time.Parse(time.RFC3339, s.FarmRuleAt) // not an instant: every farm keeps its flat shift
	return application.FarmRules{RuleAt: from, GraceDays: s.FarmGraceDays}
}

// Care turns the care settings (settlement.care_*, docs/adr/0069) into the rules of care in a founded settlement.
func Care(s config.Settlement) application.CareRules {
	from, _ := time.Parse(time.RFC3339, s.CareRuleAt) // not an instant: the city hospital stays
	return application.CareRules{RuleAt: from, GraceDays: s.CareGraceDays}
}

// Registry turns the land registry settings (settlement.parcel_*, zone_*, slope_*, foundation_*, claim_*, docs/adr/0072) into its rules.
func Registry(s config.Settlement) application.RegistryRules {
	z, _ := time.Parse(time.RFC3339, s.ZoningRuleAt)
	sl, _ := time.Parse(time.RFC3339, s.SlopeRuleAt)
	return application.RegistryRules{
		ParcelMaxCells: s.ParcelMaxCells, ParcelChangeFee: s.ParcelChangeFee, ZoneOpenFee: s.ZoneOpenFee, ZoneMaxCells: s.ZoneMaxCells,
		ZoneNotice: time.Duration(s.ZoneNoticeMinutes) * time.Minute, ZoningRuleAt: z, ZoningGraceDays: s.ZoningGraceDays,
		SlopeRuleAt: sl, SlopeGraceDays: s.SlopeGraceDays,
		Slope:           landuse.Bands{Flat: s.SlopeFlatM, Gentle: s.SlopeGentleM, Unusable: s.SlopeUnusableM},
		FoundationStone: s.FoundationStone, FoundationWage: s.FoundationWage,
		ClaimCost: s.ClaimCost, ClaimUpkeepPerDay: s.ClaimUpkeep, ClaimMaxTiles: s.ClaimMaxTiles,
	}
}

// Survival turns the survival settings (settlement.survival_*, docs/adr/0070) into the rules of thirst and warmth.
func Survival(s config.Settlement) application.SurvivalRules {
	from, _ := time.Parse(time.RFC3339, s.SurvivalRuleAt) // not an instant: survival is off
	return application.SurvivalRules{RuleAt: from, GraceDays: s.SurvivalGraceDays}
}

// Teaching turns the teaching settings (settlement.teaching_*, docs/adr/0071) into the rule date of the teacher's skill.
func Teaching(s config.Settlement) application.TeachingRules {
	from, _ := time.Parse(time.RFC3339, s.TeachingRuleAt) // not an instant: classes keep their old rules
	return application.TeachingRules{RuleAt: from, GraceDays: s.TeachingGraceDays}
}

// Craft turns the crafting settings (settlement.crafting_*, docs/adr/0068) into the rules of the tool tiers.
func Craft(s config.Settlement) application.CraftRules {
	from, _ := time.Parse(time.RFC3339, s.CraftingRuleAt) // not an instant: the tiers are off
	return application.CraftRules{RuleAt: from, GraceDays: s.CraftingGraceDays}
}
