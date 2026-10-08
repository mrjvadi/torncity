package config

import wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"

// SpawnCircle is the spawn circle rules of the settlement section (ADR 0028 section 3.2, amendment 2026-10-09), in
// the shape the founding code takes. The service and the tests both build it from here, so what a booted service
// uses is what the tests check.
func (s Settlement) SpawnCircle() wsettle.CircleParams {
	return wsettle.CircleParams{
		RadiusKm: s.SpawnCircleRadiusKm, Capacity: s.SpawnCircleCapacity,
		FillBandKm: s.SpawnCircleFillBandKm, MaxAdvance: s.SpawnCircleMaxAdvance,
	}
}
