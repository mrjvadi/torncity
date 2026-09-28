package settlementknowledge

// This file holds the K2 acquisition rules ADR 0031 sections 4.1, 4.2, 4.3
// and 4.4 describe as formulas rather than as a graph: the founding grant's
// terrain pick, the scarcity price curve (section 10 point 3, the owner's
// own decision), the settlement-to-settlement price band, and the literacy
// diffusion step. All four are pure, no I/O, no clock — the caller (K2's
// application layer) supplies every input and schedules every duration on
// the game clock itself, the same boundary the rest of this package already
// draws.

import "github.com/mrjvadi/torncity/internal/domain/item"

// FoundingUniversalGrants are the four items ADR 0031 section 4.3 grants
// every new village, free, regardless of terrain — the universal baseline
// every branch in section 3.1's table builds on top of.
var FoundingUniversalGrants = []string{"oral_tradition", "communal_watch", "kin_apprenticeship", "barter_ring"}

// TerrainGrant picks the one terrain-appropriate item ADR 0031 section 4.3
// grants alongside the four universal ones: the first code in priority order
// whose Tech is TerrainRequired and whose TerrainTags intersect
// terrainTags — deterministic given the same tree, priority and terrain, the
// same rule this whole codebase applies to everything seed-derived
// (internal/domain/worldgen's own rule, reused here for a choice instead of
// a random draw). ok is false when nothing in priority matches — an
// implausible terrain the catalogue does not yet branch for; the caller
// grants no terrain item rather than guessing one.
//
// priority is content-ordered (the application layer supplies it, typically
// "every arable_farming item, then every pastoral_husbandry item," ADR 0031
// section 4.3's own wording), not alphabetical: a grassland cell that could
// satisfy both an arable and a pastoral branch should consistently prefer
// whichever the operator put first.
func TerrainGrant(tree Tree, terrainTags []string, priority []string) (string, bool) {
	for _, code := range priority {
		t, ok := tree[code]
		if !ok || t.TerrainMode != TerrainRequired {
			continue
		}
		for _, tag := range t.TerrainTags {
			for _, have := range terrainTags {
				if tag == have {
					return code, true
				}
			}
		}
	}
	return "", false
}

// HoldersShareBPS is holders/totalSettlements expressed in basis points
// (0-10000), the input ScarcityPrice's curve reads. totalSettlements <= 0
// reports 0 (no settlements exist to hold anything, the same "as scarce as
// it gets" case as holders == 0).
func HoldersShareBPS(holders, totalSettlements int) int64 {
	if totalSettlements <= 0 || holders <= 0 {
		return 0
	}
	if holders > totalSettlements {
		holders = totalSettlements
	}
	return int64(holders) * item.BPS / int64(totalSettlements)
}

// ScarcityPrice is ADR 0031 section 10 point 3's own curve, the owner's
// decision verbatim: price = base_cost x clamp(k / holders_share, floor,
// cap). Every one of kBPS, floorBPS and capBPS is a basis-point multiplier
// (10000 = 1x base_cost) — all three content (base_cost, k, floor and cap
// in content, the owner's own words) — and holdersShareBPS is
// HoldersShareBPS's own output: how widely the item is already held, from a
// periodically refreshed aggregate (settlement_knowledge_owned), never a
// hot row read on every price quote.
//
// holdersShareBPS <= 0 (nothing holds it yet: a genuinely unprecedented
// item, or the periodic aggregate has not run once) is treated as the
// smallest positive share the scale can represent, 1, rather than a
// division by zero — clamp then does the rest, the same "cap it, do not
// blow up" instinct every OTHER share already gets: an unprecedented item is
// simply priced at the cap, not undefined.
//
// The result is never below 1: Cost 0 belongs only to a founding-only grant
// (ModeEligible false), which this curve is never asked to price — CanAcquire
// already refuses a not-mode-eligible item before a caller would reach for a
// price at all.
func ScarcityPrice(baseCost int64, holdersShareBPS, kBPS, floorBPS, capBPS int64) int64 {
	if holdersShareBPS <= 0 {
		holdersShareBPS = 1
	}
	mult := kBPS * item.BPS / holdersShareBPS
	if mult < floorBPS {
		mult = floorBPS
	}
	if mult > capBPS {
		mult = capBPS
	}
	price := baseCost * mult / item.BPS
	if price < 1 {
		price = 1
	}
	return price
}

// WithinSellerBand reports whether askPrice is within bandBPS of
// referencePrice (ADR 0031 section 10 point 3's own open question, resolved:
// "the default is that the seller may set within +-X% of it") — the check a
// settlement-to-settlement sale's ask price passes before Support's own
// curve price is used as the sale's reference. bandBPS is basis points of
// referencePrice either way (500 = +-5%).
func WithinSellerBand(referencePrice, askPrice, bandBPS int64) bool {
	if referencePrice <= 0 {
		return askPrice <= 0
	}
	band := referencePrice * bandBPS / item.BPS
	low, high := referencePrice-band, referencePrice+band
	return askPrice >= low && askPrice <= high
}

// AdvanceLiteracy applies ADR 0031 section 4.4's diffusion formula once —
// one settlement_period's worth of teaching:
//
//	literacy_share' = min(10000, literacy_share
//	    + teach_rate x teacher_skill_bps x school_capacity_factor / 10000
//	      x (10000 - literacy_share) / 10000)
//
// Every argument and the result are basis points (0-10000), the identical
// scale literacy_share itself is stored on (settlement_literacy.
// literacy_share_bps). schoolCapacityFactorBPS may exceed 10000 (an
// over-provisioned school teaches faster than its raw capacity ratio would
// suggest); literacyShareBPS is clamped into range first so a caller handing
// in a corrupt or pre-clamp value cannot push the result out of bounds.
func AdvanceLiteracy(literacyShareBPS, teachRateBPS, teacherSkillBPS, schoolCapacityFactorBPS int64) int64 {
	literacyShareBPS = clampBPS(literacyShareBPS)
	if teachRateBPS < 0 {
		teachRateBPS = 0
	}
	if teacherSkillBPS < 0 {
		teacherSkillBPS = 0
	}
	if schoolCapacityFactorBPS < 0 {
		schoolCapacityFactorBPS = 0
	}
	remaining := item.BPS - literacyShareBPS
	step := teachRateBPS * teacherSkillBPS / item.BPS
	step = step * schoolCapacityFactorBPS / item.BPS
	step = step * remaining / item.BPS
	next := literacyShareBPS + step
	return clampBPS(next)
}

func clampBPS(v int64) int64 {
	if v < 0 {
		return 0
	}
	if v > item.BPS {
		return item.BPS
	}
	return v
}
