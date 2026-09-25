package handlers

import (
	"context"
	"hash/fnv"
	"sort"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/item"
	"github.com/mrjvadi/torncity/internal/domain/military"
	"github.com/mrjvadi/torncity/internal/domain/war"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// What a battle is built from: every piece's attributes, from its design
// (defence_industry.yml, ADR 0021's ComputeAttributes) with its class's
// combat defaults for what the design lacks; which of a garrison's pieces
// reach a target; and the domain's strike or assault assembled from them.
// The rules are internal/domain/war; this file only reads and maps.

// combatPiece is one piece as a battle takes it.
type combatPiece struct {
	asset  application.MilitaryAsset
	class  content.ForceClassDef
	combat content.CombatDef
	attrs  map[string]int64
}

func (p combatPiece) attr(name string) int64 { return p.attrs[name] }

// gain is a radar's band gain, as authored or none (×1).
func (p combatPiece) gain() int64 {
	if g := p.attrs["rcs_gain"]; g > 0 {
		return g
	}
	return military.BPSWhole
}

// designCache reads designs once per battle.
type designCache map[string]*application.Design

// combatPieces reads the pieces that fight: those of a class with a combat
// role, with their attributes.
func combatPieces(ctx context.Context, tx application.Tx, snap *content.Snapshot, assets []application.MilitaryAsset,
	designs designCache,
) ([]combatPiece, error) {
	var out []combatPiece
	for _, a := range assets {
		cl, ok := snap.ForceClass(a.ClassCode)
		if !ok || cl.Combat == nil {
			continue
		}
		attrs := map[string]int64{}
		if a.DesignID != "" {
			d, ok := designs[a.DesignID]
			if !ok {
				var err error
				if d, err = tx.Production().DesignByID(ctx, a.DesignID); err != nil {
					return nil, err
				}
				designs[a.DesignID] = d
			}
			if arch, ok := snap.Archetype(d.Archetype); ok {
				if computed, err := item.ComputeAttributes(arch, domainDesign(*d), snap.Components()); err == nil {
					attrs = computed
				}
			}
		}
		for k, v := range cl.Combat.Defaults {
			if attrs[k] == 0 {
				attrs[k] = v
			}
		}
		out = append(out, combatPiece{asset: a, class: cl, combat: *cl.Combat, attrs: attrs})
	}
	return out, nil
}

// roundTrip reports whether a role flies there and back.
func roundTrip(role string) bool { return role == content.RoleAircraft }

// kindOfRole is the operation a role takes part in, "" for none it leads.
func kindOfRole(role string) string {
	switch role {
	case content.RoleAircraft:
		return content.OperationAir
	case content.RoleMissile:
		return content.OperationMissile
	case content.RoleGround, content.RoleArtillery:
		return content.OperationGround
	}
	return ""
}

// forceOption is one operation the country's forces could mount at a
// target, with the pieces that would go.
type forceOption struct {
	view   screens.ForceOption
	branch content.BranchDef
	from   application.City
	pieces []combatPiece
	// bombs are the munitions at the garrison, for an air strike.
	bombs []combatPiece
}

// forceOptions lists, for each kind and class, the garrison of ours in reach
// of the target with the most ready pieces of it.
func (h *WarHandler) forceOptions(ctx context.Context, tx application.Tx, snap *content.Snapshot, def content.WarDef,
	countryID string, target *application.City, p *application.Player,
) ([]forceOption, error) {
	assets, err := tx.Military().Assets(ctx, countryID)
	if err != nil {
		return nil, err
	}
	var ready []application.MilitaryAsset
	for _, a := range assets {
		if a.Status == application.AssetStationed && a.Condition == application.AssetReady && a.GarrisonCityID != "" &&
			a.GarrisonCityID != target.ID {
			ready = append(ready, a)
		}
	}
	pieces, err := combatPieces(ctx, tx, snap, ready, designCache{})
	if err != nil {
		return nil, err
	}
	type key struct{ kind, class, city string }
	groups := map[key][]combatPiece{}
	bombs := map[string][]combatPiece{}
	distances := map[string]int64{}
	cities := map[string]*application.City{}
	for _, pc := range pieces {
		city := pc.asset.GarrisonCityID
		if _, ok := cities[city]; !ok {
			c, err := h.cities.ByID(ctx, city)
			if err != nil {
				return nil, err
			}
			cities[city] = c
			km, derr := snap.Routes().DistanceBetween(c.Code, target.Code)
			if derr != nil {
				km = -1
			}
			distances[city] = int64(km)
		}
		km := distances[city]
		if km < 0 {
			continue
		}
		role := pc.combat.Role
		if role == content.RoleMunition {
			bombs[city] = append(bombs[city], pc)
			continue
		}
		kind := kindOfRole(role)
		switch kind {
		case "":
			continue
		case content.OperationGround:
			if km > def.Ground.ReachKM {
				continue
			}
			groups[key{kind, screens.WarAllUnits, city}] = append(groups[key{kind, screens.WarAllUnits, city}], pc)
		default:
			if !war.InReach(km, pc.attr("range"), roundTrip(role)) {
				continue
			}
			groups[key{kind, pc.class.Code, city}] = append(groups[key{kind, pc.class.Code, city}], pc)
		}
	}
	best := map[[2]string]key{}
	for k, g := range groups {
		id := [2]string{k.kind, k.class}
		b, ok := best[id]
		if !ok || len(g) > len(groups[b]) || (len(g) == len(groups[b]) && distances[k.city] < distances[b.city]) ||
			(len(g) == len(groups[b]) && distances[k.city] == distances[b.city] && k.city < b.city) {
			best[id] = k
		}
	}
	var out []forceOption
	for id, k := range best {
		g := groups[k]
		cl := g[0].class
		branch, _ := snap.Branch(cl.Branch)
		view := screens.ForceOption{Kind: id[0], Class: named(cl.Code, cl.Name), Ready: int64(len(g)),
			FromCode: cities[k.city].Code, From: cities[k.city].Name, DistanceKM: distances[k.city],
			Office: actionOffice(snap, branch.Command)}
		if id[0] == content.OperationGround {
			view.Class = named(screens.WarAllUnits, "")
		}
		opt := forceOption{view: view, branch: branch, from: *cities[k.city], pieces: g}
		if id[0] == content.OperationAir {
			opt.bombs = bombs[k.city]
			opt.view.Munitions = int64(len(opt.bombs))
		}
		if p != nil {
			if _, opt.view.CanLaunch, err = mayAct(ctx, tx, snap, countryID, branch.Command, p); err != nil {
				return nil, err
			}
		}
		out = append(out, opt)
	}
	order := map[string]int{content.OperationAir: 0, content.OperationMissile: 1, content.OperationGround: 2}
	sort.Slice(out, func(i, j int) bool {
		if order[out[i].view.Kind] != order[out[j].view.Kind] {
			return order[out[i].view.Kind] < order[out[j].view.Kind]
		}
		return out[i].view.Class.Code < out[j].view.Class.Code
	})
	return out, nil
}

// defence is a garrison's air defence as a strike meets it, with the pieces
// behind each layer (to lose them) and the suppression targets in order.
type defence struct {
	sensors []war.Sensor
	layers  []war.Layer
	// layerPieces are the pieces of each layer, in the layer's order.
	layerPieces [][]combatPiece
	// sead are the air defence assets a suppression strike hits, highest
	// value first: the batteries by reach, then the radars.
	sead []combatPiece
	// ground are the ground units that defend the city.
	ground []combatPiece
	// ready counts the air defence assets still able to fight.
	airDefence int
}

// buildDefence reads the pieces of the defending side in a city.
func buildDefence(pieces []combatPiece, d war.Doctrine) defence {
	type group struct {
		layer  war.Layer
		pieces []combatPiece
	}
	var patrols, batteries []*group
	index := map[string]*group{}
	var out defence
	var radars []combatPiece
	for _, pc := range pieces {
		if pc.asset.Condition != application.AssetReady {
			continue
		}
		switch pc.combat.Role {
		case content.RoleRadar:
			out.sensors = append(out.sensors, war.Sensor{RangeKM: pc.attr("detection_range"), GainBPS: pc.gain()})
			radars = append(radars, pc)
			out.airDefence++
		case content.RoleSAM, content.RoleAircraft:
			if pc.combat.Role == content.RoleAircraft && pc.combat.AirToAir == 0 {
				continue
			}
			k := pc.combat.Role + ":" + pc.asset.Item + ":" + pc.asset.DesignID
			g, ok := index[k]
			if !ok {
				g = &group{layer: war.Layer{Code: k, RadarKM: pc.attr("detection_range"), GainBPS: pc.gain()}}
				if pc.combat.Role == content.RoleSAM {
					g.layer.ReachKM, g.layer.PkBPS, g.layer.Rounds = pc.attr("range"), pc.attr("interception"), pc.combat.Rounds
					g.layer.Ballistic = pc.combat.Ballistic
					batteries = append(batteries, g)
				} else {
					g.layer.ReachKM, g.layer.PkBPS, g.layer.Rounds = d.AirToAirKM, d.AirToAirPkBPS, pc.combat.AirToAir
					g.layer.Fighter, g.layer.RCSMilli, g.layer.SpeedKMH = true, pc.attr("rcs"), pc.attr("speed")
					patrols = append(patrols, g)
				}
				index[k] = g
			}
			g.layer.Count++
			g.layer.Quality += pc.asset.Quality
			g.pieces = append(g.pieces, pc)
			if pc.combat.Role == content.RoleSAM {
				out.airDefence++
			}
		case content.RoleGround, content.RoleArtillery:
			out.ground = append(out.ground, pc)
		}
	}
	sort.SliceStable(batteries, func(i, j int) bool {
		if batteries[i].layer.ReachKM != batteries[j].layer.ReachKM {
			return batteries[i].layer.ReachKM > batteries[j].layer.ReachKM
		}
		return batteries[i].layer.Code < batteries[j].layer.Code
	})
	sort.SliceStable(patrols, func(i, j int) bool { return patrols[i].layer.Code < patrols[j].layer.Code })
	for _, g := range append(patrols, batteries...) {
		g.layer.Quality /= int(max(g.layer.Count, 1))
		out.layers = append(out.layers, g.layer)
		out.layerPieces = append(out.layerPieces, g.pieces)
	}
	for _, g := range batteries {
		out.sead = append(out.sead, g.pieces...)
	}
	out.sead = append(out.sead, radars...)
	return out
}

// threatOf is a piece of the package as the strike takes it.
func threatOf(pc combatPiece) war.Threat {
	t := war.Threat{RCSMilli: pc.attr("rcs"), SpeedKMH: pc.attr("speed"), EvasionBPS: pc.combat.EvasionBPS,
		Quality: pc.asset.Quality, Ballistic: pc.combat.Ballistic, LowLevel: pc.combat.LowLevel}
	if pc.combat.Role == content.RoleAircraft {
		t.Kind, t.Load, t.AirToAir, t.RadarKM, t.GainBPS = war.Aircraft, pc.combat.Load, pc.combat.AirToAir,
			pc.attr("detection_range"), pc.gain()
		return t
	}
	t.Kind, t.Accuracy, t.Firepower = war.Missile, pc.attr("accuracy"), pc.attr("firepower")
	return t
}

// munitionOf is what a package's bombs are: their average.
func munitionOf(bombs []combatPiece) war.Munition {
	if len(bombs) == 0 {
		return war.Munition{}
	}
	var m war.Munition
	q := 0
	for _, b := range bombs {
		m.Accuracy += b.attr("accuracy")
		m.Firepower += b.attr("firepower")
		q += b.asset.Quality
	}
	n := int64(len(bombs))
	m.Accuracy, m.Firepower, m.Quality = m.Accuracy/n, m.Firepower/n, q/len(bombs)
	return m
}

// unitOf is a piece as a ground battle takes it.
func unitOf(pc combatPiece) war.Unit {
	return war.Unit{Firepower: pc.attr("firepower"), Armour: pc.attr("armour"), Accuracy: pc.attr("accuracy"),
		Quality: pc.asset.Quality, Artillery: pc.combat.Role == content.RoleArtillery}
}

// militia is a city's own defenders, fewer the more it is damaged.
func militia(def content.WarDef, damageBPS int64) []war.Unit {
	n := int64(def.Militia.Units) * (war.BPSWhole - min(max(damageBPS, 0), war.BPSWhole)) / war.BPSWhole
	out := make([]war.Unit, n)
	for i := range out {
		out[i] = war.Unit{Firepower: def.Militia.Firepower, Armour: def.Militia.Armour, Quality: def.Militia.Quality}
	}
	return out
}

// strikeOf assembles a strike.
func strikeOf(def content.WarDef, seed int64, attackers, bombs []combatPiece, d defence, attReady, defReady int64) war.Strike {
	s := war.Strike{Seed: seed, Munitions: int64(len(bombs)), Munition: munitionOf(bombs), AttackerReadiness: attReady,
		DefenderReadiness: defReady, Sensors: d.sensors, Layers: d.layers, Doctrine: def.DoctrineRules()}
	for _, pc := range attackers {
		s.Threats = append(s.Threats, threatOf(pc))
	}
	return s
}

// assaultOf assembles a ground battle.
func assaultOf(def content.WarDef, seed int64, attackers []combatPiece, d defence, damageBPS, attReady, defReady int64) war.Assault {
	g := def.Ground
	a := war.Assault{Seed: seed, AttackerReadiness: attReady, DefenderReadiness: defReady, Rounds: g.Rounds,
		DefenderAdvantageBPS: g.DefenderAdvantageBPS, BaseHitBPS: g.BaseHitBPS, HitFloorBPS: g.HitFloorBPS,
		HitCeilingBPS: g.HitCeilingBPS, HoldMin: g.HoldMin}
	for _, pc := range attackers {
		a.Attackers = append(a.Attackers, unitOf(pc))
	}
	for _, pc := range d.ground {
		if pc.asset.Condition == application.AssetReady {
			a.Defenders = append(a.Defenders, unitOf(pc))
		}
	}
	a.Defenders = append(a.Defenders, militia(def, damageBPS)...)
	return a
}

// seedOf derives an operation's dice from its id: the same operation always
// rolls the same.
func seedOf(id string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	return int64(h.Sum64() >> 1)
}

// hostilePieces keeps the pieces of the countries hostile to attacker.
func hostilePieces(pieces []combatPiece, w war.War, attacker string) []combatPiece {
	var out []combatPiece
	for _, pc := range pieces {
		if w.Opposed(attacker, pc.asset.CountryID) {
			out = append(out, pc)
		}
	}
	return out
}

// chanceBand words a chance of success in a band.
func chanceBand(successes, samples int) string {
	switch {
	case successes*3 >= samples*2:
		return "likely"
	case successes*3 >= samples:
		return "even"
	}
	return "unlikely"
}

// estimate runs the operation over the content's sample of dice against the
// defence as it stands, and tells the average in bands: the commander's
// intelligence estimate.
func (h *WarHandler) estimate(snap *content.Snapshot, def content.WarDef, kind, objective string, attackers,
	bombs []combatPiece, d defence, damageBPS, attReady, defReady int64,
) *screens.Estimate {
	n := def.PreviewSamples
	var lost, points, success int64
	for i := range n {
		seed := int64(i + 1)
		if kind == content.OperationGround {
			out, err := war.Fight(assaultOf(def, seed, attackers, d, damageBPS, attReady, defReady))
			if err != nil {
				return nil
			}
			for _, f := range out.Attackers {
				if f == war.UnitDestroyed {
					lost++
				}
			}
			if out.Taken && d.airDefence == 0 {
				success++
			}
			continue
		}
		out, err := war.Resolve(strikeOf(def, seed, attackers, bombs, d, attReady, defReady))
		if err != nil {
			return nil
		}
		lost += out.Lost
		if objective == application.ObjectiveDefences {
			points += min(out.Hits, int64(len(d.sead)))
		} else {
			points += out.Points
		}
		if out.Hits > 0 {
			success++
		}
	}
	e := &screens.Estimate{Chance: chanceBand(int(success), n),
		LossBand: military.BandOf(snap.StrengthBands(), (lost+int64(n)/2)/int64(n))}
	if kind != content.OperationGround {
		avg := points / int64(n)
		if objective == application.ObjectiveDefences {
			if len(d.sead) > 0 {
				e.DamageBand = war.BandOf(def.Bands(), avg*war.BPSWhole/int64(len(d.sead)))
			}
		} else {
			e.DamageBand = war.BandOf(def.Bands(), def.CityRules().Strike(0, avg))
		}
	}
	return e
}
