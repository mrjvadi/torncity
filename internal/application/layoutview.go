package application

import (
	"encoding/hex"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The buildings of a settlement as a client draws them, and the version of
// that picture. Both the layout endpoint (internal/clientapi) and the village
// events that announce a change of it (internal/application/handlers) come
// from here, so the version an event carries is, by construction, the version
// the layout will have once the change has committed
// (docs/adr/0028 section 9.4, docs/adr/0030 section 1).

// Building states a client is told. The database keeps queued, building,
// complete; a demolished or cancelled building holds no lot and is not
// listed.
const (
	ViewPlanned           = "planned"
	ViewUnderConstruction = "under_construction"
	ViewBuilt             = "built"
	ViewDamaged           = "damaged"
	ViewRuin              = "ruin"
)

// ViewBuilding is one building of the picture, before it becomes JSON.
type ViewBuilding struct {
	ID   string
	Type string
	// X, Y is the top-left lot; W, H the footprint after the turn.
	X, Y, W, H int
	Rotated    bool
	State      string
	// StartedAt and FinishAt are RFC 3339, empty where they do not apply.
	StartedAt, FinishAt string
	DamageBPS           int
	VisualSeed          uint32
}

// Footprint is the width and height of a building type's footprint, turned
// or not; a type the content no longer declares is 1 by 1.
type Footprint func(typeCode string, rotated bool) (w, h int)

// ViewState is the state a client is told for a building row.
func ViewState(b SettlementBuildingInstance) string {
	switch {
	case b.Status == "queued":
		return ViewPlanned
	case b.Status == "building":
		return ViewUnderConstruction
	case b.DamageBPS >= 10_000:
		return ViewRuin
	case b.DamageBPS > 0:
		return ViewDamaged
	}
	return ViewBuilt
}

// VisualSeed is a building's look seed: stable for its id, never stored.
func VisualSeed(id string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return h.Sum32()
}

// ViewBuildings lists the buildings a viewer sees, in the layout's order. A
// member sees every building that holds a lot, with ids, timers and damage; a
// stranger only the ones that stand.
func ViewBuildings(rows []SettlementBuildingInstance, member bool, footprint Footprint) []ViewBuilding {
	rows = append([]SettlementBuildingInstance(nil), rows...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].LotY != rows[j].LotY {
			return rows[i].LotY < rows[j].LotY
		}
		if rows[i].LotX != rows[j].LotX {
			return rows[i].LotX < rows[j].LotX
		}
		return rows[i].ID < rows[j].ID
	})
	out := []ViewBuilding{}
	for _, b := range rows {
		if !b.Holds() {
			continue
		}
		state := ViewState(b)
		if !member && state != ViewBuilt {
			continue
		}
		fw, fh := footprint(b.TypeCode, b.Rotated)
		vb := ViewBuilding{Type: b.TypeCode, X: b.LotX, Y: b.LotY, W: fw, H: fh, Rotated: b.Rotated, State: state,
			VisualSeed: VisualSeed(b.ID)}
		if member {
			vb.ID = b.ID
			vb.DamageBPS = b.DamageBPS
			if state == ViewPlanned || state == ViewUnderConstruction {
				vb.StartedAt = b.QueuedAt.UTC().Format(time.RFC3339)
			}
			if b.FinishAt != nil && (state == ViewPlanned || state == ViewUnderConstruction) {
				vb.FinishAt = b.FinishAt.UTC().Format(time.RFC3339)
			}
		}
		out = append(out, vb)
	}
	return out
}

// LayoutVersionOf hashes everything a client draws from the layout, so it
// moves exactly when the picture does. canPlace is the viewer's right to
// place (the settlement's head).
func LayoutVersionOf(settlementID, tier, name string, gridLots int, canPlace bool, buildings []ViewBuilding, tenure ...string) string {
	h := fnv.New64a()
	put := func(parts ...string) {
		_, _ = h.Write([]byte(strings.Join(parts, "|") + ";"))
	}
	put(settlementID, tier, name, strconv.Itoa(gridLots), strconv.FormatBool(canPlace))
	if len(tenure) > 0 && tenure[0] != "" {
		// Who owns which lot and building: only members see it, so only the
		// member versions carry it (citizen.go).
		put("tenure", tenure[0])
	}
	for _, b := range buildings {
		put(b.ID, b.Type, strconv.Itoa(b.X), strconv.Itoa(b.Y), strconv.Itoa(b.W), strconv.Itoa(b.H), strconv.FormatBool(b.Rotated),
			b.State, b.FinishAt, strconv.Itoa(b.DamageBPS), strconv.FormatUint(uint64(b.VisualSeed), 10))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// LayoutVersions are the versions of the layout for the three kinds of
// viewer: the settlement's head (who may place), any other member, and
// everyone else. An event that changes the picture carries all three, and a
// client compares the one that is its own with the version of the layout it
// holds.
type LayoutVersions struct {
	Head   string `json:"head"`
	Member string `json:"member"`
	Public string `json:"public"`
}

// LayoutVersionsOf computes them from a settlement's building rows.
func LayoutVersionsOf(settlementID, tier, name string, gridLots int, rows []SettlementBuildingInstance, footprint Footprint) LayoutVersions {
	member := ViewBuildings(rows, true, footprint)
	public := ViewBuildings(rows, false, footprint)
	return LayoutVersions{
		Head:   LayoutVersionOf(settlementID, tier, name, gridLots, true, member),
		Member: LayoutVersionOf(settlementID, tier, name, gridLots, false, member),
		Public: LayoutVersionOf(settlementID, tier, name, gridLots, false, public),
	}
}

// TenureMark hashes who owns which lot and which building, for the member
// versions of a layout: empty while nobody owns anything, so a village
// without private property keeps the version it always had.
func TenureMark(lots []SettlementLot, buildings []PrivateBuilding) string {
	if len(lots) == 0 && len(buildings) == 0 {
		return ""
	}
	parts := make([]string, 0, len(lots)+len(buildings))
	for _, l := range lots {
		parts = append(parts, "l"+strconv.Itoa(l.X)+","+strconv.Itoa(l.Y)+","+l.Tenure+","+l.OwnerID)
	}
	for _, b := range buildings {
		parts = append(parts, "b"+b.BuildingID+","+b.OwnerID)
	}
	sort.Strings(parts)
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.Join(parts, ";")))
	return hex.EncodeToString(h.Sum(nil))
}

// LayoutVersionsWithTenure is LayoutVersionsOf for a village with private
// property: the head's and the members' versions also move when a lot or a
// building changes hands; the public one never shows ownership.
func LayoutVersionsWithTenure(settlementID, tier, name string, gridLots int, rows []SettlementBuildingInstance,
	footprint Footprint, mark string,
) LayoutVersions {
	member := ViewBuildings(rows, true, footprint)
	public := ViewBuildings(rows, false, footprint)
	return LayoutVersions{
		Head:   LayoutVersionOf(settlementID, tier, name, gridLots, true, member, mark),
		Member: LayoutVersionOf(settlementID, tier, name, gridLots, false, member, mark),
		Public: LayoutVersionOf(settlementID, tier, name, gridLots, false, public),
	}
}
