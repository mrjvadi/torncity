package handlers

import (
	"context"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	wsettle "github.com/mrjvadi/torncity/internal/domain/settlement"
)

// Phase G1 of ADR 0044 (docs/adr/0044-organic-growth-alliances-countries.md
// sections 5.1 and 11): the DUAL READ. Every gate that decides by the
// settlement's tier also asks what the settlement HAS (wsettle.Capabilities:
// standing buildings, finished research, staffed roles), and the two answers
// are compared. While growth.capabilities is "shadow" the tier stays
// authoritative and each disagreement is metered (growth_disagreements,
// `admin growth report`). "off" (the default) computes nothing at all, so a
// gate behaves exactly as before. A tag that carries no stage at all (phase
// G0) can only be answered by the capabilities, so it is.
//
// The gate is process-wide, set once at start (ConfigureGrowth): the gates are
// spread over a dozen handlers, each built on its own, and a flag that every
// constructor had to be told about would be the first thing forgotten.

// Growth modes, the values of growth.capabilities.
const (
	GrowthModeOff           = "off"
	GrowthModeShadow        = "shadow"
	GrowthModeAuthoritative = "authoritative"
)

// maxPendingDisagreements bounds the in-memory meter between two flushes: a
// safety bound, not a game rule. Beyond it a new key is only counted.
const maxPendingDisagreements = 20000

// GrowthConfig is growth.* of the configuration.
type GrowthConfig struct {
	Mode      string
	CacheTTL  time.Duration
	RuinedBPS int
	// FlushInterval is how often Run writes the metered disagreements.
	FlushInterval time.Duration
}

type growthKey struct{ city, site, kind, code string }

type growthEntry struct {
	tier, capability bool
	missing          string
	count            int64
	at               time.Time
}

type growthCached struct {
	caps    wsettle.Capabilities
	found   bool
	expires time.Time
}

// GrowthGate holds the capability computation's switch, its cache and the
// disagreement meter. All methods are safe for concurrent use and on a nil
// receiver (nil means off).
type GrowthGate struct {
	cfg    GrowthConfig
	reader application.GrowthStandingReader
	store  application.GrowthDisagreementStore
	log    *slog.Logger
	now    func() time.Time

	mu      sync.Mutex
	cache   map[string]growthCached
	pending map[growthKey]*growthEntry
	dropped int64
	logged  map[growthKey]bool
}

var activeGrowth atomic.Pointer[GrowthGate]

// ConfigureGrowth sets the process-wide gate and returns it. A mode of off (or
// empty) clears it: nil is "off". reader serves the gates that hold no unit of
// work; store receives the flushed meter (either may be nil in a test).
func ConfigureGrowth(cfg GrowthConfig, reader application.GrowthStandingReader, store application.GrowthDisagreementStore,
	log *slog.Logger,
) *GrowthGate {
	if cfg.Mode == "" || cfg.Mode == GrowthModeOff {
		activeGrowth.Store(nil)
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	g := &GrowthGate{
		cfg: cfg, reader: reader, store: store, log: log, now: func() time.Time { return time.Now().UTC() },
		cache: map[string]growthCached{}, pending: map[growthKey]*growthEntry{}, logged: map[growthKey]bool{},
	}
	activeGrowth.Store(g)
	return g
}

// currentGrowth is the configured gate, nil when off.
func currentGrowth() *GrowthGate { return activeGrowth.Load() }

// baseGate is the gate buildings use when growth.capabilities is off: it computes
// capabilities from the open transaction and meters nothing. The size label lists
// no building any more, so building lists are always decided by what the
// settlement has, whatever the mode.
var baseGate = &GrowthGate{
	cfg: GrowthConfig{Mode: GrowthModeOff, RuinedBPS: 10000}, log: slog.Default(),
	now:   func() time.Time { return time.Now().UTC() },
	cache: map[string]growthCached{}, pending: map[growthKey]*growthEntry{}, logged: map[growthKey]bool{},
}

// buildingGate is the configured gate, or the base gate when the mode is off.
func buildingGate() *GrowthGate {
	if g := currentGrowth(); g != nil {
		return g
	}
	return baseGate
}

// Enabled says the capability answer is being computed.
func (g *GrowthGate) Enabled() bool { return g != nil }

// Authoritative says the capability answer decides.
func (g *GrowthGate) Authoritative() bool { return g != nil && g.cfg.Mode == GrowthModeAuthoritative }

// capabilitiesOf computes what a settlement has from its rows. A building
// stands when it is finished (and below the ruin line); a knowledge item counts
// as itself and as every capability tag it provides (ADR 0031 section 3.1).
//
// STAFF. ADR 0044 section 4.4 makes a role "staffed" by the people working in
// it, but the node model that records them (W1, ADR 0041) is not built: until
// it is, a staff role counts as filled when the building it works in stands,
// which is also what the tier gates assume today (education_here.go). Every
// building is therefore Staffed. The report names this as a known gap.
func capabilitiesOf(snap *content.Snapshot, st application.SettlementStanding, ruinedBPS int) wsettle.Capabilities {
	in := wsettle.CapabilityInput{RuinedBPS: ruinedBPS}
	for _, row := range st.Buildings {
		if row.Status != "complete" {
			continue
		}
		b := wsettle.StandingBuilding{Code: row.TypeCode, Complete: true, DamageBPS: row.DamageBPS, Staffed: true}
		if d, ok := snap.SettlementBuildingDef(row.TypeCode); ok {
			b.Role, b.Level = d.Role, d.Tier
		}
		in.Buildings = append(in.Buildings, b)
	}
	for _, o := range st.Knowledge {
		in.Knowledge = append(in.Knowledge, o.Code)
		if d, ok := snap.SettlementKnowledgeDef(o.Code); ok {
			in.Knowledge = append(in.Knowledge, d.Provides...)
		}
	}
	probe := wsettle.Compute(in)
	for _, r := range snap.StaffRoles() {
		if r.Building == nil || probe.Stands(wsettle.BuildingNeed{Code: r.Building.Code, Role: r.Building.Role, Level: r.Building.Tier}) {
			in.StaffRoles = append(in.StaffRoles, r.Code)
		}
	}
	return wsettle.Compute(in)
}

// needOf is a tag's prerequisites as the capability computation takes them.
func needOf(n content.AvailabilityNeeds) wsettle.Need {
	out := wsettle.Need{Knowledge: n.Knowledge, Staff: n.Staff}
	for _, b := range n.Buildings {
		out.Buildings = append(out.Buildings, wsettle.BuildingNeed{Code: b.Code, Role: b.Role, Level: b.Tier})
	}
	return out
}

// InTx computes the capabilities of a founded settlement from the rows of the
// open transaction; found is false for a content city (the neutral city),
// which has none to compute.
func (g *GrowthGate) InTx(ctx context.Context, tx application.Tx, snap *content.Snapshot, cityID string) (wsettle.Capabilities, bool, error) {
	if g == nil || cityID == "" || snap == nil {
		return wsettle.Capabilities{}, false, nil
	}
	var st application.SettlementStanding
	var err error
	if st.Buildings, err = tx.SettlementBuildings().List(ctx, cityID); err != nil {
		return wsettle.Capabilities{}, false, err
	}
	if st.Knowledge, err = tx.SettlementKnowledge().Owned(ctx, cityID); err != nil {
		return wsettle.Capabilities{}, false, err
	}
	if !st.Founded() {
		return wsettle.Capabilities{}, false, nil
	}
	return capabilitiesOf(snap, st, g.cfg.RuinedBPS), true, nil
}

// FromRows computes capabilities from rows a gate already read.
func (g *GrowthGate) FromRows(snap *content.Snapshot, st application.SettlementStanding) wsettle.Capabilities {
	return capabilitiesOf(snap, st, g.cfg.RuinedBPS)
}

// OutsideTx is InTx for a gate that holds no unit of work: it reads through the
// pool reader and caches the answer for growth.cache_ttl (ADR 0044 section 5.1
// "pure, cached"). Replicas keep their own caches; a stale answer only delays a
// disagreement report, it never decides anything while the tier is authoritative.
func (g *GrowthGate) OutsideTx(ctx context.Context, snap *content.Snapshot, cityID string) (wsettle.Capabilities, bool, error) {
	if g == nil || g.reader == nil || cityID == "" || snap == nil {
		return wsettle.Capabilities{}, false, nil
	}
	now := g.now()
	g.mu.Lock()
	c, ok := g.cache[cityID]
	g.mu.Unlock()
	if ok && now.Before(c.expires) {
		return c.caps, c.found, nil
	}
	st, err := g.reader.Standing(ctx, cityID)
	if err != nil {
		return wsettle.Capabilities{}, false, err
	}
	c = growthCached{found: st.Founded(), expires: now.Add(g.cfg.CacheTTL)}
	if c.found {
		c.caps = capabilitiesOf(snap, st, g.cfg.RuinedBPS)
	}
	g.mu.Lock()
	if len(g.cache) > 4096 {
		g.cache = map[string]growthCached{} // a bound, not a rule: a cold cache only costs a read
	}
	g.cache[cityID] = c
	g.mu.Unlock()
	return c.caps, c.found, nil
}

// canCompare says a tag can be judged by capabilities at all: not the neutral
// city's own (support), and not one whose real gate a later phase builds.
func canCompare(tag content.AvailabilityDef) bool {
	if tag.Stage == content.StageSupport {
		return false
	}
	return tag.Growth == nil || tag.Growth.Deferred == ""
}

// Answer is the capability answer for one tag: whether the settlement has what
// the entry needs. compared is false when the tag cannot be judged yet.
func (g *GrowthGate) Answer(snap *content.Snapshot, caps wsettle.Capabilities, tag content.AvailabilityDef) (offered, compared bool, missing wsettle.Need) {
	if g == nil {
		return false, false, wsettle.Need{}
	}
	return capabilityAnswer(snap, caps, tag)
}

// capabilityAnswer is Answer without the gate: the pure comparison, which the
// sweep and the tests call directly.
func capabilityAnswer(snap *content.Snapshot, caps wsettle.Capabilities, tag content.AvailabilityDef) (offered, compared bool, missing wsettle.Need) {
	if !canCompare(tag) {
		return false, false, wsettle.Need{}
	}
	n, _ := snap.GrowthNeeds(tag, true)
	need := needOf(n)
	missing = caps.Missing(need)
	return missing.Empty(), true, missing
}

// Decide runs the dual read for one tag at one gate and returns what the gate
// must answer: the tier's answer, unless the tag has no stage (the capability
// answer is the only one) or growth.capabilities is authoritative. A
// disagreement is metered. site names the gate; found false (a content city)
// leaves the tier answer alone.
func (g *GrowthGate) Decide(site, cityID string, snap *content.Snapshot, caps wsettle.Capabilities, found bool,
	tag content.AvailabilityDef, tierAnswer bool,
) bool {
	if g == nil || !found {
		return tierAnswer
	}
	capAnswer, compared, missing := g.Answer(snap, caps, tag)
	if !compared {
		return tierAnswer
	}
	if capAnswer != tierAnswer {
		g.note(growthKey{cityID, site, tag.Kind, tag.Code}, tierAnswer, capAnswer, missing)
	}
	if tag.Stage == "" || g.Authoritative() {
		return capAnswer
	}
	return tierAnswer
}

// missingString renders what a settlement lacks as sorted "knowledge:x",
// "building:y", "role:z>=2" (a role at its level) and "staff:w" pairs,
// comma-joined: the form growth_disagreements.missing stores.
func missingString(n wsettle.Need) string {
	var parts []string
	for _, c := range n.Knowledge {
		parts = append(parts, "knowledge:"+c)
	}
	for _, b := range n.Buildings {
		if b.Code != "" {
			parts = append(parts, "building:"+b.Code)
		} else {
			parts = append(parts, "role:"+b.Role+">="+strconv.Itoa(b.Level))
		}
	}
	for _, s := range n.Staff {
		parts = append(parts, "staff:"+s)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// DecideBuilding is Decide for the question "is this building on offer to the
// settlement" asked by the build menu, the lot grid and the placement. The
// tier's answer (tierListed) is its listing by tier, usually with the research
// the building names. What is compared is the TIER's effect alone: the tier
// answer is taken as listed AND the building's own prerequisites held, so a
// disagreement means the tier label and the capabilities disagree, never that
// the village has not yet researched something. The caller gets tierListed
// back unless the tag has no stage or growth.capabilities is authoritative.
func (g *GrowthGate) DecideBuilding(site, cityID string, snap *content.Snapshot, caps wsettle.Capabilities, found bool,
	code string, tierListed bool,
) bool {
	if g == nil || !found {
		return tierListed
	}
	tag, ok := snap.AvailabilityTag("building", code)
	if !ok {
		tag = content.AvailabilityDef{Kind: "building", Code: code}
	}
	// The size label no longer lists a building (Def.ListedAt ignores it), so the
	// capability answer decides in every mode: the building's own research and
	// building roles plus the growth gate's. The disagreement meter still notes
	// where the old tier effect would have differed.
	own, _ := snap.GrowthNeeds(tag, false)
	ownOK := caps.Satisfies(needOf(own))
	if g.cfg.Mode != GrowthModeOff {
		g.Decide(site, cityID, snap, caps, found, tag, tierListed && ownOK)
	}
	capAnswer, compared, _ := g.Answer(snap, caps, tag)
	if !compared {
		return tierListed
	}
	return capAnswer
}

// ListedInTx is DecideBuilding for a gate inside a unit of work: it reads the
// settlement's rows itself. With growth.capabilities off it never reads.
func ListedInTx(ctx context.Context, tx application.Tx, snap *content.Snapshot, site, cityID, code string, tierListed bool) (bool, error) {
	gg := buildingGate()
	caps, found, err := gg.InTx(ctx, tx, snap, cityID)
	if err != nil {
		return tierListed, err
	}
	return gg.DecideBuilding(site, cityID, snap, caps, found, code, tierListed), nil
}

// note meters one disagreement; the first sight of a key is also logged.
func (g *GrowthGate) note(k growthKey, tier, capability bool, missing wsettle.Need) {
	miss := missingString(missing)
	g.mu.Lock()
	e := g.pending[k]
	if e == nil {
		if len(g.pending) >= maxPendingDisagreements {
			g.dropped++
			g.mu.Unlock()
			return
		}
		e = &growthEntry{}
		g.pending[k] = e
	}
	e.tier, e.capability, e.missing, e.at = tier, capability, miss, g.now()
	e.count++
	first := !g.logged[k]
	g.logged[k] = true
	g.mu.Unlock()
	if first {
		g.log.Warn("growth: capabilities and tier disagree", slog.String("site", k.site), slog.String("entry", k.kind+"/"+k.code),
			slog.String("settlement", k.city), slog.Bool("tier", tier), slog.Bool("capabilities", capability), slog.String("missing", miss))
	}
}

// Pending is the disagreements metered since the last flush, sorted, for a
// test or a caller that wants them without a store.
func (g *GrowthGate) Pending() []application.GrowthDisagreement {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pendingLocked()
}

func (g *GrowthGate) pendingLocked() []application.GrowthDisagreement {
	out := make([]application.GrowthDisagreement, 0, len(g.pending))
	for k, e := range g.pending {
		out = append(out, application.GrowthDisagreement{
			SettlementID: k.city, Site: k.site, Kind: k.kind, Code: k.code,
			TierAnswer: e.tier, CapabilityAnswer: e.capability, Missing: e.missing, Count: e.count, At: e.at,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Site != b.Site {
			return a.Site < b.Site
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.SettlementID < b.SettlementID
	})
	return out
}

// Flush writes the metered disagreements to the store and forgets them. It
// runs on the pool, never inside a unit of work. A failed write puts the
// counts back so nothing is lost.
func (g *GrowthGate) Flush(ctx context.Context) error {
	if g == nil || g.store == nil {
		return nil
	}
	g.mu.Lock()
	rows := g.pendingLocked()
	g.pending = map[growthKey]*growthEntry{}
	g.mu.Unlock()
	if len(rows) == 0 {
		return nil
	}
	if err := g.store.Record(ctx, rows); err != nil {
		g.mu.Lock()
		for _, d := range rows {
			k := growthKey{d.SettlementID, d.Site, d.Kind, d.Code}
			if e := g.pending[k]; e != nil {
				e.count += d.Count
			} else {
				g.pending[k] = &growthEntry{tier: d.TierAnswer, capability: d.CapabilityAnswer, missing: d.Missing, count: d.Count, at: d.At}
			}
		}
		g.mu.Unlock()
		return err
	}
	return nil
}

// Run flushes the meter every growth.flush_interval until ctx ends, then once
// more. Safe on any number of replicas: each flushes its own counts into rows
// keyed by (settlement, site, entry), which only ever add up.
func (g *GrowthGate) Run(ctx context.Context) {
	if g == nil || g.store == nil {
		return
	}
	every := g.cfg.FlushInterval
	if every <= 0 {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			if err := g.Flush(flushCtx); err != nil {
				g.log.Warn("growth: final flush failed", slog.String("error", err.Error()))
			}
			cancel()
			return
		case <-t.C:
			if err := g.Flush(ctx); err != nil {
				g.log.Warn("growth: flush failed", slog.String("error", err.Error()))
			}
		}
	}
}
