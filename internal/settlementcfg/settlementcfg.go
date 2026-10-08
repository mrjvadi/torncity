// Package settlementcfg turns the settlement section of the config into the shapes the founding code takes. It sits
// apart from both packages so that internal/config never imports a domain package (the domain's own tests load the
// config, which would close an import cycle).
package settlementcfg

import (
	"github.com/mrjvadi/torncity/internal/config"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
)

// SpawnCircle is the spawn circle rules of the settlement section (ADR 0028 section 3.2, amendment 2026-10-09). The
// service and the tests both build it from here, so what a booted service uses is what the tests check.
func SpawnCircle(s config.Settlement) wsettle.CircleParams {
	return wsettle.CircleParams{
		RadiusKm: s.SpawnCircleRadiusKm, Capacity: s.SpawnCircleCapacity,
		FillBandKm: s.SpawnCircleFillBandKm, MaxAdvance: s.SpawnCircleMaxAdvance,
	}
}
