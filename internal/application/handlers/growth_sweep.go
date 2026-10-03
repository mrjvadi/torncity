package handlers

import (
	"sort"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
)

// The sweep is the disagreement report that does not wait for traffic (ADR 0044
// phase G1): for one settlement it asks, of EVERY availability tag, what the
// tier says and what the capabilities say, with the same two rules the gates
// use (hubSettlement.offeredByTier and capabilityAnswer). `admin growth sweep`
// runs it over every settlement in the database; the tests run it over seeded
// ones, one row per Appendix A entry.

// Gate classes of a swept tag, from how its capability gate is stated.
const (
	// SweepOpen: growth.open, or no requirement at all.
	SweepOpen = "open"
	// SweepRequires: the tag's own requires (research, buildings, staff).
	SweepRequires = "requires"
	// SweepOwn: the building or knowledge item's own prerequisites.
	SweepOwn = "own"
	// SweepGrowth: a new gate added by ADR 0044 Appendix A (growth.requires).
	SweepGrowth = "growth.requires"
	// SweepDeferred: the real gate belongs to a later phase; not compared.
	SweepDeferred = "deferred"
	// SweepSupport: the neutral city's own; not compared.
	SweepSupport = "support"
)

// SweepSettlement is one settlement to sweep.
type SweepSettlement struct {
	ID, Code, Tier string
	Standing       application.SettlementStanding
}

// SweepRow is one tag judged both ways for one settlement.
type SweepRow struct {
	Settlement, Tier string
	Kind, Code       string
	Stage, Class     string
	// Row is the Appendix A row number (0 for a tag the appendix does not number).
	Row                         int
	TierAnswer, CapabilityAnswer bool
	// Compared is false for a deferred or Support-only tag.
	Compared bool
	Missing  string
}

// Disagrees reports a compared row whose two answers differ.
func (r SweepRow) Disagrees() bool { return r.Compared && r.TierAnswer != r.CapabilityAnswer }

// sweepClass says how a tag's capability gate is stated.
func sweepClass(snap *content.Snapshot, t content.AvailabilityDef) string {
	switch {
	case t.Stage == content.StageSupport:
		return SweepSupport
	case t.Growth != nil && t.Growth.Deferred != "":
		return SweepDeferred
	case t.Growth != nil && t.Growth.Requires != nil:
		return SweepGrowth
	case needsAsk(t.Requires):
		return SweepRequires
	}
	if own, _ := snap.GrowthNeeds(content.AvailabilityDef{Kind: t.Kind, Code: t.Code}, false); len(own.Knowledge)+len(own.Buildings)+len(own.Staff) > 0 {
		return SweepOwn
	}
	return SweepOpen
}

func needsAsk(n *content.AvailabilityNeeds) bool {
	return n != nil && (len(n.Knowledge) > 0 || len(n.Buildings) > 0 || len(n.Staff) > 0)
}

// Sweep judges every availability tag for one settlement, tags in availability.yml
// order. The tier answer is the one the hubs give (the stage reached and the
// buildings the tag names standing); the capability answer ignores the stage.
func Sweep(snap *content.Snapshot, s SweepSettlement, ruinedBPS int) []SweepRow {
	caps := capabilitiesOf(snap, s.Standing, ruinedBPS)
	here := hubSettlement{
		stageRank: content.StageRank(tierStage(s.Tier)),
		stands:    standsIn(snap, s.Standing.Buildings),
		owned:     map[string]bool{},
	}
	for _, k := range s.Standing.Knowledge {
		here.owned[k.Code] = true
	}
	tags := snap.AllAvailabilityTags()
	out := make([]SweepRow, 0, len(tags))
	for _, t := range tags {
		row := SweepRow{Settlement: s.Code, Tier: s.Tier, Kind: t.Kind, Code: t.Code, Stage: t.Stage, Class: sweepClass(snap, t)}
		if t.Growth != nil {
			row.Row = t.Growth.Row
		}
		row.TierAnswer = here.offeredByTier(t)
		// A building or a knowledge item is gated by its tier AND its own research
		// where the build menu and the research list gate it (pathContext.listed):
		// compare like with like, so a disagreement is the tier label's effect.
		switch t.Kind {
		case "building":
			if d, ok := snap.SettlementBuildingDef(t.Code); ok {
				own, _ := snap.GrowthNeeds(content.AvailabilityDef{Kind: "building", Code: t.Code}, false)
				row.TierAnswer = d.Def().ListedAt(s.Tier) && caps.Satisfies(needOf(own))
			}
		case "knowledge":
			own, _ := snap.GrowthNeeds(content.AvailabilityDef{Kind: "knowledge", Code: t.Code}, false)
			row.TierAnswer = row.TierAnswer && caps.Satisfies(needOf(content.AvailabilityNeeds{Knowledge: own.Knowledge}))
		}
		cap, compared, missing := capabilityAnswer(snap, caps, t)
		row.CapabilityAnswer, row.Compared = cap, compared
		if compared {
			row.Missing = missingString(missing)
		}
		out = append(out, row)
	}
	return out
}

// SweepSummary counts the sweep of one settlement: how many tags were compared
// and how many of those disagree, by gate class.
type SweepSummary struct {
	Compared, Agree, Disagree int
	ByClass                   map[string]int // disagreements per class
	Skipped                   int
}

// Summarise folds sweep rows.
func Summarise(rows []SweepRow) SweepSummary {
	sum := SweepSummary{ByClass: map[string]int{}}
	for _, r := range rows {
		switch {
		case !r.Compared:
			sum.Skipped++
		case r.Disagrees():
			sum.Compared++
			sum.Disagree++
			sum.ByClass[r.Class]++
		default:
			sum.Compared++
			sum.Agree++
		}
	}
	return sum
}

// Classes lists the classes with disagreements, sorted, for printing.
func (s SweepSummary) Classes() []string {
	out := make([]string, 0, len(s.ByClass))
	for k := range s.ByClass {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
