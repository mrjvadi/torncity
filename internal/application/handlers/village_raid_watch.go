package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/raid"
	"github.com/mrjvadi/torncity/internal/domain/territory"
	"github.com/mrjvadi/torncity/internal/domain/worldgen"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/idempotency"
)

// The watch of a settlement (docs/adr/0074 7.2): a lookout, a watchtower and a beacon tower are daily services (village_service.go).
// On a day one is open, with its watchmen or keeper paid and its fire fed, it sees raiders coming: as far as the ground lets it (the
// sight of territory.Compute from the settlement's place, kept per building), and the sighting is told to the settlement by a runner,
// or at once by a chain of open beacons. With no one on duty it sees nothing. Nothing here checks whether anyone is online.

// detectorPost is a standing detector: the building, the function that stands for it and the content row of its range and chance.
type detectorPost struct {
	b   application.SettlementBuildingInstance
	fn  content.BuildingFunctionDef
	det content.SettlementRaidDetectorDef
}

func (p detectorPost) beacon() bool { return p.det.Relays }

// detectorPosts lists the complete buildings that are detectors.
func detectorPosts(snap *content.Snapshot, buildings []application.SettlementBuildingInstance) []detectorPost {
	byFn := map[string]content.SettlementRaidDetectorDef{}
	for _, d := range snap.SettlementRaidDetectors() {
		if d.Function != "" {
			byFn[d.Function] = d
		}
	}
	var out []detectorPost
	for _, b := range buildings {
		if b.Status != "complete" {
			continue
		}
		code, ok := snap.FunctionReplacing(b.TypeCode)
		if !ok {
			continue
		}
		det, ok := byFn[code]
		if !ok {
			continue
		}
		fn, ok := snap.BuildingFunction(code)
		if !ok || fn.Coverage == nil {
			continue
		}
		out = append(out, detectorPost{b: b, fn: fn, det: det})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].b.ID < out[j].b.ID })
	return out
}

// sightOf is the stored sight of a detector, computed from the world on first use and whenever the building's kind or the world changes
// (territory.Compute is a pure function of the terrain, so any replica makes the same numbers).
func (h *VillageHandler) sightOf(ctx context.Context, tx application.Tx, s application.FoundedSettlement, p detectorPost) ([]int32, error) {
	cov := p.fn.Coverage
	w, err := h.world(ctx)
	if err != nil || w == nil || s.WorldCellID < 0 || int(s.WorldCellID) >= len(w.Cells) {
		return nil, err
	}
	rev := fmt.Sprintf("v1:%d:%d:%d:%d:%d", w.Seed, s.WorldCellID, cov.Bins, cov.ObserverHeightM, cov.SightM)
	if got, err := tx.Raids().Sight(ctx, p.b.ID, rev); err != nil || got != nil {
		return got, err
	}
	pt := w.Cells[s.WorldCellID].Point
	kind := territory.Kind{Bins: cov.Bins, ObserverHeightM: cov.ObserverHeightM, ClaimBaseM: cov.ClaimBaseM, ClaimCapM: cov.ClaimCapM, SightM: cov.SightM,
		StepM: 305, TargetHeightM: 2, RefractionNum: 7, RefractionDen: 6}
	cv, err := territory.Compute(territory.WorldSampler{W: w, Src: worldgen.NewChunkTileSource(w, 256), Observer: w.TileOfLatLon(pt.LatDeg, pt.LonDeg)}, kind, territory.DefaultParams())
	if err != nil {
		return nil, err
	}
	if err := tx.Raids().SaveSight(ctx, p.b.ID, rev, cv.Sight, h.now()); err != nil {
		return nil, err
	}
	return cv.Sight, nil
}

// approach is where the raiders come from and how far: the bearing from the defender to the attacker and the distance between them.
func (h *VillageHandler) approach(ctx context.Context, from, to application.FoundedSettlement) (bearing, distM float64, ok bool) {
	la, lo, ok1 := h.raidWorldPoint(ctx, from)
	lb, lob, ok2 := h.raidWorldPoint(ctx, to)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	radius := 6_371_000.0
	if w, err := h.world(ctx); err == nil && w != nil && w.Params.PlanetRadiusKm > 0 {
		radius = w.Params.PlanetRadiusKm * 1000
	}
	return raid.Bearing(la, lo, lb, lob), raid.DistanceM(la, lo, lb, lob, radius), true
}

// sightingLead is how far ahead of the start a detector sees the raiders coming: the sight along their road over their speed.
func (h *VillageHandler) sightingLead(ctx context.Context, tx application.Tx, defender, attacker application.FoundedSettlement, p detectorPost, transport string) (sightM int64, lead time.Duration, err error) {
	sight, err := h.sightOf(ctx, tx, defender, p)
	if err != nil || len(sight) == 0 {
		return 0, 0, err
	}
	bearing, dist, ok := h.approach(ctx, defender, attacker)
	if !ok {
		return 0, 0, nil
	}
	sightM = raid.SightAlong(sight, bearing, int64(dist))
	return sightM, raid.Lead(sightM, h.raid.Speed(transport)), nil
}

// scheduleSightings puts one sighting on the clock for each detector the defender has, at the moment the raiders would come into its sight.
func (h *VillageHandler) scheduleSightings(ctx context.Context, tx application.Tx, snap *content.Snapshot, r application.SettlementRaid,
	defender, attacker application.FoundedSettlement, transport string, now time.Time,
) error {
	buildings, err := buildingsWithWorks(ctx, tx, snap, defender.CityID)
	if err != nil {
		return err
	}
	for _, p := range detectorPosts(snap, buildings) {
		sightM, lead, err := h.sightingLead(ctx, tx, defender, attacker, p, transport)
		if err != nil {
			return err
		}
		if sightM <= 0 || lead <= 0 {
			continue
		}
		at := r.StartAt.Add(-lead)
		if at.Before(now) {
			at = now
		}
		if err := h.scheduleRaid(ctx, tx, r, application.RaidSightingActionType, at, p.b.ID); err != nil {
			return err
		}
	}
	return nil
}

// RaidSighting handles raid.sighting, the scheduler's: a detector's turn at the raiders. It rolls once per raid and detector (the row is
// the fence), and tells the defender once if the sighting reached the settlement in time.
func (h *VillageHandler) RaidSighting(ctx context.Context, meta envelope.Metadata, req RaidScheduledRequest) (*presentation.Response, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.InvalidInput("malformed request context").WithCause(err)
	}
	var pl raidPayload
	if len(req.Payload) > 0 {
		_ = json.Unmarshal(req.Payload, &pl)
	}
	if pl.RaidID == "" {
		pl.RaidID = req.ReferenceID
	}
	if pl.RaidID == "" || pl.BuildingID == "" {
		return nil, errors.InvalidInput("raid.sighting names no raid or no building")
	}
	snap := h.content.Current()
	return nil, h.uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		if req.ActorID != "" {
			key := idempotency.Derive(req.ActorID, meta.Command, pl.RaidID+":"+pl.BuildingID+":"+req.ActionID)
			if fresh, err := tx.Idempotency().Reserve(ctx, string(key), req.ActorID, meta.RequestID, meta.Command, h.idempotencyTTL); err != nil || !fresh {
				return err
			}
		}
		row, err := tx.Raids().ByID(ctx, pl.RaidID, true)
		if err != nil {
			if isSentinel(err, application.ErrRaidNotFound) {
				return nil
			}
			return err
		}
		if row.Status != application.RaidFiled && row.Status != application.RaidOpen {
			return nil
		}
		now := h.now()
		defender, err := tx.Settlements().ByID(ctx, row.DefenderID)
		if err != nil {
			return err
		}
		attacker, err := tx.Settlements().ByID(ctx, row.AttackerID)
		if err != nil {
			return err
		}
		buildings, err := buildingsWithWorks(ctx, tx, snap, defender.CityID)
		if err != nil {
			return err
		}
		posts := detectorPosts(snap, buildings)
		var post *detectorPost
		for i := range posts {
			if posts[i].b.ID == pl.BuildingID {
				post = &posts[i]
			}
		}
		if post == nil {
			return nil // demolished or never a detector: it sees nothing
		}
		out := decodeOutcome(row.Outcome)
		sightM, _, err := h.sightingLead(ctx, tx, defender, attacker, *post, out.Transport)
		if err != nil {
			return err
		}
		day, err := h.SettleServiceDay(ctx, tx, snap, defender, buildings)
		if err != nil {
			return err
		}
		held := day != nil && day.HeldPost(post.b.ID)
		night := nightAt(now, defender.Zone())
		chance := h.raid.Rules.Chance(int64(post.det.DetectChanceBPS), held, night, 0)
		// a post blinded by sabotage sees less (docs/adr/0077 6.5)
		if blind, err := tx.Intel().BlindBPS(ctx, post.b.ID, now); err != nil {
			return err
		} else if blind > 0 {
			chance -= chance * int64(blind) / raid.BPS
		}
		roll := raid.Roll(row.Seed, "detect:"+post.b.ID)
		detected := held && raid.Detected(chance, roll)
		// told: by a runner from the post, or at once along a chain of open beacons
		allied, err := h.alliedBeacons(ctx, tx, snap, defender)
		if err != nil {
			return err
		}
		hops, delay := h.relayDelay(buildings, posts, day, *post, allied...)
		toldAt := now.Add(delay)
		lead := row.StartAt.Sub(toldAt)
		fresh, err := tx.Raids().RecordDetection(ctx, application.RaidDetection{RaidID: row.ID, BuildingID: post.b.ID, Code: post.det.Code, ChanceBPS: chance,
			Roll: roll, Detected: detected, SightM: sightM, Hops: int64(hops), LeadSecs: max(int64(lead/time.Second), 0), At: now})
		if err != nil || !fresh || !detected || lead <= 0 {
			return err
		}
		// the first sighting that reached the settlement in time is the warning; the others add nothing
		dets, err := tx.Raids().Detections(ctx, row.ID)
		if err != nil {
			return err
		}
		for _, d := range dets {
			if d.BuildingID != post.b.ID && d.Detected && d.LeadSecs > 0 {
				return nil
			}
		}
		return appendVillageEvent(ctx, tx, meta, "raid_detected", defender.CityID, map[string]any{"raid_id": row.ID, "defender_id": defender.CityID,
			"attacker_id": attacker.CityID, "by": post.det.Code, "lead_secs": int64(lead / time.Second), "start_at": row.StartAt, "hops": hops,
			"band": sizeBand(out.Raiders)})
	})
}

// sizeBand is how many raiders a detector can tell: a lookout sees "a group", never a count.
func sizeBand(n int64) string {
	switch {
	case n <= 0:
		return "few"
	case n < 6:
		return "group"
	case n < 15:
		return "band"
	}
	return "host"
}

// nightAt reports whether it is night by the settlement's own clock.
func nightAt(t time.Time, zone time.Duration) bool {
	hour := t.Add(zone).Hour()
	return hour >= 21 || hour < 5
}

// lotMetres is the distance between two lots in metres (a lot is thirty metres).
func lotMetres(a, b application.SettlementBuildingInstance) float64 {
	return 30 * math.Hypot(float64(a.LotX-b.LotX), float64(a.LotY-b.LotY))
}

// relayDelay is the time a sighting takes to reach the settlement's centre (its civic hall): the length of the walk of a runner from
// the post at the pace of the foot, or the hops of a chain of open beacons within the relay distance of one another, a hop each.
func (h *VillageHandler) relayDelay(buildings []application.SettlementBuildingInstance, posts []detectorPost, day *application.ServiceDay, from detectorPost, allied ...raid.Beacon) (hops int, delay time.Duration) {
	centre := from.b
	for _, b := range buildings {
		if b.TypeCode == "civic_hall" && b.Holds() {
			centre = b
		}
	}
	metres := func(b application.SettlementBuildingInstance) (float64, float64) {
		return float64(b.LotX) * 30, float64(b.LotY) * 30
	}
	cx, cy := metres(centre)
	fx, fy := metres(from.b)
	nodes := []raid.Beacon{{ID: "src", X: fx, Y: fy, Held: true}, {ID: "centre", X: cx, Y: cy, Held: true}}
	for _, p := range posts {
		if p.beacon() && p.b.ID != from.b.ID && day != nil && day.HeldPost(p.b.ID) {
			x, y := metres(p.b)
			nodes = append(nodes, raid.Beacon{ID: p.b.ID, X: x, Y: y, Held: true})
		}
	}
	// the open beacons of allies relay too, where an alliance is in force (docs/adr/0074 R7, docs/adr/0076 7.3)
	nodes = append(nodes, allied...)
	srcBeacon := from.beacon()
	los := func(a, b raid.Beacon) bool {
		if (a.ID == "src" && b.ID == "centre") || (a.ID == "centre" && b.ID == "src") {
			return srcBeacon // only a beacon can pass the news on to the village without a runner
		}
		return true
	}
	if n := raid.Reach(nodes, "src", "centre", float64(h.raid.RelayM), los); n > 0 && (srcBeacon || n > 1) {
		return n, time.Duration(n) * h.raid.Rules.BeaconHop
	}
	walk := lotMetres(from.b, centre)
	return 0, raid.Lead(int64(walk), h.raid.Speed("foot"))
}

// watchLines reads the settlement's detectors and whether each was open on its last day.
func (h *VillageHandler) watchLines(ctx context.Context, tx application.Tx, snap *content.Snapshot, s application.FoundedSettlement) ([]village.WatchLine, error) {
	buildings, err := buildingsWithWorks(ctx, tx, snap, s.CityID)
	if err != nil {
		return nil, err
	}
	posts := detectorPosts(snap, buildings)
	if len(posts) == 0 {
		return nil, nil
	}
	day, err := tx.ServiceDays().Last(ctx, s.CityID)
	if err != nil {
		return nil, err
	}
	var out []village.WatchLine
	for _, p := range posts {
		line := village.WatchLine{Building: presentation.Named{Code: p.b.ID, Name: p.det.Code}, Kind: p.det.Code, SightM: int64(p.fn.Coverage.SightM)}
		if day != nil {
			for _, x := range day.Posts {
				if x.BuildingID == p.b.ID {
					line.Held, line.Idle = x.Held, x.Idle
				}
			}
		}
		out = append(out, line)
	}
	return out, nil
}
