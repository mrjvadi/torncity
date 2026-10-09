package settlementcfg

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
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
